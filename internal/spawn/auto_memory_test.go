package spawn_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness/claude"
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

// TestStartMateTurnsAutoMemoryOffInAnExistingSettingsFile covers a Mate
// directory made before task 35: its settings file predates the key, and the
// start must add it without rewriting the hooks someone may have edited.
func TestStartMateTurnsAutoMemoryOffInAnExistingSettingsFile(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	path := claude.ClaudeSettingsPath(w.MateDir("shop"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
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
	if string(keys[claude.AutoMemoryKey]) != "false" {
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
