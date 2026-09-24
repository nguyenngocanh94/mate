package spawn_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

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
	data, err := spawn.ClaudeSettings("/usr/local/bin/mate")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(settingsKeys(t, data)[spawn.AutoMemoryKey]); got != "false" {
		t.Fatalf("%s = %q, want false", spawn.AutoMemoryKey, got)
	}
}

func TestCrewSettingsTurnAutoMemoryOffAndWireNoHooks(t *testing.T) {
	keys := settingsKeys(t, spawn.CrewClaudeSettings())
	if got := string(keys[spawn.AutoMemoryKey]); got != "false" {
		t.Fatalf("%s = %q, want false", spawn.AutoMemoryKey, got)
	}
	if _, ok := keys["hooks"]; ok {
		t.Fatal("a Crew's settings must wire no hooks: the Mate's hooks are the Mate's")
	}
}

func TestEnsureAutoMemoryOffAddsTheKeyAndKeepsTheRest(t *testing.T) {
	in := []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"x"}]}]},"model":"opus"}`)
	out, changed, err := spawn.EnsureAutoMemoryOff(in)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("a file without the key must be changed")
	}
	keys := settingsKeys(t, out)
	if string(keys[spawn.AutoMemoryKey]) != "false" {
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
		out, changed, err := spawn.EnsureAutoMemoryOff([]byte(in))
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
		if _, _, err := spawn.EnsureAutoMemoryOff([]byte(in)); err == nil {
			t.Fatalf("%s: want an error", in)
		}
	}
}

// TestStartMateTurnsAutoMemoryOffInAnExistingSettingsFile covers a Mate
// directory made before task 35: its settings file predates the key, and the
// start must add it without rewriting the hooks someone may have edited.
func TestStartMateTurnsAutoMemoryOffInAnExistingSettingsFile(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	dir := filepath.Join(w.MateDir("shop"), spawn.ClaudeSettingsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, spawn.ClaudeSettingsFile)
	old := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"'/old/mate' hook mate-stop"}]}]}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	keys := settingsKeys(t, data)
	if string(keys[spawn.AutoMemoryKey]) != "false" {
		t.Fatalf("auto-memory not turned off: %s", data)
	}
	if !json.Valid(keys["hooks"]) || !strings.Contains(string(keys["hooks"]), "/old/mate") {
		t.Fatalf("the existing hooks were not kept: %s", data)
	}
	// Task 37: the same start adds the SessionStart hook the old file lacks.
	if !strings.Contains(string(keys["hooks"]), `"SessionStart"`) || !strings.Contains(string(keys["hooks"]), "hook mate-session") {
		t.Fatalf("the SessionStart hook was not added: %s", data)
	}
}

// TestEnsureSessionHookAddsItAndKeepsTheRest is task 37's path for a Mate
// directory made before it: the SessionStart hook is added beside whatever
// the file already runs, the captain's own SessionStart entries included.
func TestEnsureSessionHookAddsItAndKeepsTheRest(t *testing.T) {
	in := []byte(`{"theme":"dark","hooks":{"Stop":[{"hooks":[{"type":"command","command":"'/old/mate' hook mate-stop"}]}],"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"echo captain"}]}]}}`)
	out, changed, err := spawn.EnsureSessionHook(in, "/usr/local/bin/mate")
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

	again, changed, err := spawn.EnsureSessionHook(out, "/somewhere/else/mate")
	if err != nil || changed || string(again) != string(out) {
		t.Fatalf("a file that already runs hook mate-session was changed (%v, %v)", changed, err)
	}
	for _, bad := range []string{`[]`, `{"hooks":[]}`, `{"hooks":{"SessionStart":{}}}`} {
		if _, _, err := spawn.EnsureSessionHook([]byte(bad), "/m"); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}
