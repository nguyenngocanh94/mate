package beads

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The argv golden pins, byte for byte, every command the Beads wrapper hands
// its Runner on its three paths - a bd pass-through, the bv viewer, and
// init - each from a project with no tracker yet: name, argv, working
// directory, and the tracker variables of the environment. It is the safety
// net for moving Beads behind the tool registry
// (docs/plans/workspace-layout-and-tools-2026-10-08.md, PR 0). A diff here
// is a behaviour change; rerun with -update only when that change is
// intended, and read the golden diff before committing it.
var updateArgv = flag.Bool("update", false, "rewrite testdata/argv.golden")

// trackerEnvPrefixes are the families of variables the wrapper sets or
// strips (Tracker.Environment). The rest of the environment is the
// machine's, not the wrapper's, and stays out of the golden.
var trackerEnvPrefixes = []string{"BEADS_", "BD_", "BV_"}

func trackerEnvKey(entry string) bool {
	key, _, _ := strings.Cut(entry, "=")
	for _, p := range trackerEnvPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

func TestTrackerArgvGolden(t *testing.T) {
	// Only the variables set here reach the wrapper: whatever tracker
	// settings this machine has are unset for the test.
	for _, entry := range os.Environ() {
		if trackerEnvKey(entry) {
			key, value, _ := strings.Cut(entry, "=")
			t.Setenv(key, value)
			os.Unsetenv(key)
		}
	}
	// Every ambient variable the wrapper strips, so the golden proves none
	// reaches a subprocess.
	for _, key := range []string{"BEADS_DIR", "BEADS_DB", "BD_DB", "BEADS_DOLT_SERVER_HOST", "BEADS_DOLT_SERVER_PORT", "BEADS_DOLT_SERVER_SOCKET", "BEADS_DOLT_MODE", "BEADS_DOLT_DATABASE", "BEADS_DATABASE"} {
		t.Setenv(key, "/wrong")
	}

	var got strings.Builder
	for _, path := range []struct {
		name string
		call func(*Tracker) error
	}{
		{"Tracker.Run ready --json", func(tr *Tracker) error {
			return tr.Run(context.Background(), []string{"ready", "--json"}, nil, io.Discard, io.Discard)
		}},
		{"Tracker.Viewer", func(tr *Tracker) error {
			return tr.Viewer(context.Background(), nil, nil, io.Discard, io.Discard)
		}},
		{"Tracker.Init", func(tr *Tracker) error {
			return tr.Init(context.Background(), io.Discard)
		}},
	} {
		var calls []Command
		tracker, w := fixture(t, func(ctx context.Context, c Command, in io.Reader, out, stderr io.Writer) error {
			calls = append(calls, c)
			if c.Name == "bd" && c.Args[0] == "init" {
				seed(t, c)
			}
			return nil
		})
		if err := path.call(tracker); err != nil {
			t.Fatalf("%s: %v", path.name, err)
		}
		normalize := strings.NewReplacer(w.Root(), "{{WORKSPACE}}").Replace
		fmt.Fprintf(&got, "== %s\n", path.name)
		for _, c := range calls {
			fmt.Fprintf(&got, "name: %s\n", c.Name)
			fmt.Fprintf(&got, "args: %s\n", normalize(fmt.Sprintf("%q", c.Args)))
			fmt.Fprintf(&got, "dir:  %s\n", normalize(c.Dir))
			for _, e := range c.Env {
				if trackerEnvKey(e) {
					fmt.Fprintf(&got, "env:  %s\n", normalize(e))
				}
			}
			got.WriteString("\n")
		}
	}

	golden := filepath.Join("testdata", "argv.golden")
	if *updateArgv {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/beads -run TestTrackerArgvGolden -update to record it)", err)
	}
	if got.String() != string(want) {
		t.Errorf("%s changed byte for byte.\ngot:\n%s\nwant:\n%s", golden, got.String(), want)
	}
}
