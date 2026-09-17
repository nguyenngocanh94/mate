package spawn_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/spawn"
)

var updateSettings = flag.Bool("update-settings", false, "update testdata/claude-settings.json.golden")

func TestClaudeSettingsGolden(t *testing.T) {
	got, err := spawn.ClaudeSettings("/usr/local/bin/matev2")
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
	const binary = "/opt/mate space/matev2"
	data, err := spawn.ClaudeSettings(binary)
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
