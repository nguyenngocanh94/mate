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

	"github.com/nguyenngocanh94/mate/internal/tool"
)

// The argv golden pins, byte for byte, every command the Beads profile
// hands its Runner on its two running paths - a bd pass-through
// (Command.Run) and init (Data.Init) - each from a project with no tracker
// yet: name, argv, working directory, and the tracker variables of the
// environment; and the command its Viewer builds for the tasks tab. It was
// recorded by internal/beads before Beads moved behind the tool registry
// (docs/plans/workspace-layout-and-tools-2026-10-08.md, PR 0 and PR 5). A
// diff here is a behaviour change; rerun with -update only when that change
// is intended, and read the golden diff before committing it.
var updateArgv = flag.Bool("update", false, "rewrite testdata/argv.golden")

// trackerEnvPrefixes are the families of variables the profile sets or
// strips (project.environment). The rest of the environment is the
// machine's, not the profile's, and stays out of the golden.
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

// writeInvocation is one process as the golden records it.
func writeInvocation(w io.Writer, inv tool.Invocation, normalize func(string) string) {
	fmt.Fprintf(w, "name: %s\n", inv.Name)
	fmt.Fprintf(w, "args: %s\n", normalize(fmt.Sprintf("%q", inv.Args)))
	fmt.Fprintf(w, "dir:  %s\n", normalize(inv.Dir))
	for _, e := range inv.Env {
		if trackerEnvKey(e) {
			fmt.Fprintf(w, "env:  %s\n", normalize(e))
		}
	}
}

func TestTrackerArgvGolden(t *testing.T) {
	// Only the variables set here reach the profile: whatever tracker
	// settings this machine has are unset for the test.
	for _, entry := range os.Environ() {
		if trackerEnvKey(entry) {
			key, value, _ := strings.Cut(entry, "=")
			t.Setenv(key, value)
			os.Unsetenv(key)
		}
	}
	// Every ambient variable the profile strips, set wrong, so the golden
	// proves each Invocation overrides it. The Runner here is a fake;
	// cmd/mate's TestToolCLISeparatesMateAndUpstreamFlags proves it at the
	// process boundary.
	for _, key := range trackerEnv {
		t.Setenv(key, "/wrong")
	}

	var got strings.Builder
	for _, path := range []struct {
		name string
		call func(tool.CommandEnv) error
	}{
		{"Command.Run ready --json", func(env tool.CommandEnv) error {
			return command{}.Run(context.Background(), env, []string{"ready", "--json"}, nil, io.Discard, io.Discard)
		}},
		{"Data.Init", func(env tool.CommandEnv) error {
			return data{}.Init(context.Background(), env, io.Discard)
		}},
	} {
		var calls []tool.Invocation
		f := newFixture(t, func(_ context.Context, inv tool.Invocation, _ io.Reader, _, _ io.Writer) error {
			calls = append(calls, inv)
			if inv.Name == "bd" && inv.Args[0] == "init" {
				seed(t, inv)
			}
			return nil
		})
		if err := path.call(f.env); err != nil {
			t.Fatalf("%s: %v", path.name, err)
		}
		fmt.Fprintf(&got, "== %s\n", path.name)
		for _, c := range calls {
			writeInvocation(&got, c, strings.NewReplacer(f.root, "{{WORKSPACE}}").Replace)
			got.WriteString("\n")
		}
	}

	// The tasks tab runs what the Viewer builds; the Console starts it,
	// not the Runner.
	f := newFixture(t, nil)
	inv, err := viewer{}.Argv(tool.ViewerContext{ProjectDir: f.env.ProjectDir}, func(name string) string { return "/bin/" + name })
	if err != nil {
		t.Fatal(err)
	}
	inv.Name = filepath.Base(inv.Name)
	fmt.Fprintf(&got, "== Viewer.Argv\n")
	writeInvocation(&got, inv, strings.NewReplacer(f.root, "{{WORKSPACE}}").Replace)

	golden := filepath.Join("testdata", "argv.golden")
	if *updateArgv {
		if err := os.WriteFile(golden, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/tool/beads -run TestTrackerArgvGolden -update to record it)", err)
	}
	if got.String() != string(want) {
		t.Errorf("%s changed byte for byte.\ngot:\n%s\nwant:\n%s", golden, got.String(), want)
	}
}
