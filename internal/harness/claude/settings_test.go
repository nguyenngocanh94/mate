package claude

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateSettings = flag.Bool("update-settings", false, "update testdata/claude-settings.json.golden")

func TestClaudeSettingsGolden(t *testing.T) {
	got, err := ClaudeSettings("/usr/local/bin/mate")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "claude-settings.json.golden")
	if *updateSettings {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run go test -run %s -update-settings to create it)", path, err, t.Name())
	}
	if !bytes.Equal(want, got) {
		t.Errorf("ClaudeSettings mismatch: rerun with -update-settings if intended\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

// TestClaudeSettingsParsesAndPointsAtTheBinary is the shape task 08 asks for
// beyond byte equality: the generated file must be valid Claude Code hook
// JSON, and both hooks must invoke the given binary with the right
// subcommand.
func TestClaudeSettingsParsesAndPointsAtTheBinary(t *testing.T) {
	const binary = "/opt/mate space/mate"
	data, err := ClaudeSettings(binary)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("settings.json does not parse: %v\n%s", err, data)
	}
	command := func(event string) string {
		t.Helper()
		matchers, ok := parsed.Hooks[event]
		if !ok || len(matchers) != 1 || len(matchers[0].Hooks) != 1 {
			t.Fatalf("hooks[%s] = %#v, want exactly one matcher with one command hook", event, parsed.Hooks[event])
		}
		if matchers[0].Hooks[0].Type != "command" {
			t.Fatalf("hooks[%s][0].hooks[0].type = %q, want command", event, matchers[0].Hooks[0].Type)
		}
		return matchers[0].Hooks[0].Command
	}
	prompt := command("UserPromptSubmit")
	if !strings.Contains(prompt, binary) || !strings.HasSuffix(prompt, "hook mate-prompt") {
		t.Fatalf("UserPromptSubmit command = %q, want it to run %q hook mate-prompt", prompt, binary)
	}
	stop := command("Stop")
	if !strings.Contains(stop, binary) || !strings.HasSuffix(stop, "hook mate-stop") {
		t.Fatalf("Stop command = %q, want it to run %q hook mate-stop", stop, binary)
	}
}

// settingsKeys decodes a settings file into its top-level keys.
func settingsKeys(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("settings do not parse: %v\n%s", err, data)
	}
	return keys
}

// TestMateSettingsTurnAutoMemoryOff is B6 (task 35): the Mate's memory is
// mate/memory.md, so Claude Code's own auto-memory must be off in the file
// every Mate launches with.
func TestMateSettingsTurnAutoMemoryOff(t *testing.T) {
	data, err := ClaudeSettings("/usr/local/bin/mate")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(settingsKeys(t, data)[AutoMemoryKey]); got != "false" {
		t.Fatalf("%s = %q, want false", AutoMemoryKey, got)
	}
}

func TestCrewSettingsTurnAutoMemoryOffAndWireNoHooks(t *testing.T) {
	keys := settingsKeys(t, CrewClaudeSettings())
	if got := string(keys[AutoMemoryKey]); got != "false" {
		t.Fatalf("%s = %q, want false", AutoMemoryKey, got)
	}
	if _, ok := keys["hooks"]; ok {
		t.Fatal("a Crew's settings must wire no hooks: the Mate's hooks are the Mate's")
	}
}

func TestEnsureAutoMemoryOffAddsTheKeyAndKeepsTheRest(t *testing.T) {
	in := []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"x"}]}]},"model":"opus"}`)
	out, changed, err := EnsureAutoMemoryOff(in)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("a file without the key must be changed")
	}
	keys := settingsKeys(t, out)
	if string(keys[AutoMemoryKey]) != "false" {
		t.Fatalf("key not added: %s", out)
	}
	if string(keys["model"]) != `"opus"` {
		t.Fatalf("model lost: %s", out)
	}
	var hooks struct {
		Stop []struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"Stop"`
	}
	if err := json.Unmarshal(keys["hooks"], &hooks); err != nil || len(hooks.Stop) != 1 || hooks.Stop[0].Hooks[0].Command != "x" {
		t.Fatalf("hooks lost: %s (%v)", out, err)
	}
}

// A file that already says, either way, is the captain's decision.
func TestEnsureAutoMemoryOffLeavesAnExplicitValueAlone(t *testing.T) {
	for _, in := range []string{`{"autoMemoryEnabled": true}`, `{"autoMemoryEnabled":false,"x":1}`} {
		out, changed, err := EnsureAutoMemoryOff([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if changed || string(out) != in {
			t.Fatalf("%s: changed=%v out=%s, want untouched", in, changed, out)
		}
	}
}

func TestEnsureAutoMemoryOffRefusesAFileThatIsNotAnObject(t *testing.T) {
	for _, in := range []string{`not json`, `[1,2]`} {
		if _, _, err := EnsureAutoMemoryOff([]byte(in)); err == nil {
			t.Fatalf("%s: want an error", in)
		}
	}
}

// TestEnsureSessionHookAddsItAndKeepsTheRest is task 37's path for a Mate
// directory made before it: the SessionStart hook is added beside whatever
// the file already runs, the captain's own SessionStart entries included.
func TestEnsureSessionHookAddsItAndKeepsTheRest(t *testing.T) {
	in := []byte(`{"theme":"dark","hooks":{"Stop":[{"hooks":[{"type":"command","command":"'/old/mate' hook mate-stop"}]}],"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"echo captain"}]}]}}`)
	out, changed, err := EnsureSessionHook(in, "/usr/local/bin/mate")
	if err != nil || !changed {
		t.Fatalf("EnsureSessionHook = changed %v, %v", changed, err)
	}
	var got struct {
		Theme string `json:"theme"`
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Theme != "dark" || len(got.Hooks["Stop"]) != 1 || got.Hooks["Stop"][0].Hooks[0].Command != "'/old/mate' hook mate-stop" {
		t.Fatalf("the file's other keys were not kept: %s", out)
	}
	starts := got.Hooks["SessionStart"]
	if len(starts) != 2 || starts[0].Matcher != "startup" || starts[0].Hooks[0].Command != "echo captain" {
		t.Fatalf("the captain's SessionStart entry was not kept first: %s", out)
	}
	if starts[1].Matcher != "" || starts[1].Hooks[0].Command != "'/usr/local/bin/mate' hook mate-session" {
		t.Fatalf("mate's entry = %+v, want an unmatched hook mate-session", starts[1])
	}

	again, changed, err := EnsureSessionHook(out, "/somewhere/else/mate")
	if err != nil || changed || string(again) != string(out) {
		t.Fatalf("a file that already runs hook mate-session was changed (%v, %v)", changed, err)
	}
	for _, bad := range []string{`[]`, `{"hooks":[]}`, `{"hooks":{"SessionStart":{}}}`} {
		if _, _, err := EnsureSessionHook([]byte(bad), "/m"); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}
