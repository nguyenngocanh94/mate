package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/dispatch"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func dispatchWorkspace(t *testing.T, table string) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if table != "" {
		if err := os.WriteFile(dispatch.Path(w.StateDir()), []byte(table), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

const sampleTable = `{
  "rules": [
    {"when": "a trivial mechanical edit", "use": {"harness": "claude", "model": "haiku", "effort": "low"}, "why": "cheap"},
    {"when": "a big or ambiguous feature", "use": [
      {"harness": "claude", "model": "opus", "effort": "high"},
      {"harness": "codex", "model": "gpt-5.5", "effort": "xhigh"}
    ]}
  ],
  "default": {"harness": "codex", "effort": "medium"}
}`

func TestCrewSpawnParsesModelAndEffort(t *testing.T) {
	for _, args := range [][]string{
		{"crew", "spawn", "shop", "k3", "--brief", "b", "--effort", "hgih"},
		{"crew", "spawn", "shop", "k3", "--brief", "b", "--model", "-c"},
	} {
		var out, errw bytes.Buffer
		var ue *usageError
		if err := run(args, &out, &errw); !errors.As(err, &ue) {
			t.Fatalf("%v: err = %v, want a usage error", args, err)
		}
	}
}

// A spawn that names its harness keeps its profile exactly as given,
// whatever the table says.
func TestAnExplicitProfileIsKept(t *testing.T) {
	want := spawn.SpawnCrewRequest{Harness: harness.KindClaude, Model: "haiku", Effort: harness.EffortLow}
	for _, table := range []string{sampleTable, ""} {
		got, note, err := applyDispatch(dispatchWorkspace(t, table), want)
		if err != nil || note != "" || got != want {
			t.Fatalf("table %q: got %+v note %q err %v, want the request unchanged", table, got, note, err)
		}
	}
}

// A spawn with no --harness runs the table's default profile on the
// workspace's default harness (codex unless the captain changed it), and
// says so; without a file of its own the workspace uses the built-in table.
func TestNoHarnessTakesTheTablesDefault(t *testing.T) {
	for _, tc := range []struct {
		name, table string
		want        spawn.SpawnCrewRequest
		source      string
	}{
		{"built-in", "", spawn.SpawnCrewRequest{Harness: harness.KindCodex, Model: "gpt-6-luna", Effort: harness.EffortHigh}, "built-in"},
		{"workspace", sampleTable, spawn.SpawnCrewRequest{Harness: harness.KindCodex, Effort: harness.EffortMedium}, dispatch.FileName},
	} {
		got, note, err := applyDispatch(dispatchWorkspace(t, tc.table), spawn.SpawnCrewRequest{})
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %+v err %v, want %+v", tc.name, got, err, tc.want)
		}
		if !strings.Contains(note, tc.source) || !strings.Contains(note, "--harness codex") {
			t.Fatalf("%s: note %q does not name the table and the profile", tc.name, note)
		}
	}
}

// The default alternative on the workspace's own default harness wins, so a
// captain who set crew_harness: claude gets the Claude default.
func TestTheDefaultFollowsTheWorkspaceHarness(t *testing.T) {
	w := dispatchWorkspace(t, "")
	raw, err := os.ReadFile(w.WorkspaceFile())
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "crew_harness: codex", "crew_harness: claude", 1)
	if edited == string(raw) {
		t.Fatalf("workspace.yaml has no crew_harness: codex line:\n%s", raw)
	}
	if err := os.WriteFile(w.WorkspaceFile(), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	got, _, err := applyDispatch(w, spawn.SpawnCrewRequest{})
	want := spawn.SpawnCrewRequest{Harness: harness.KindClaude, Model: "sonnet", Effort: harness.EffortHigh}
	if err != nil || got != want {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
	}
}

// A model name belongs to one harness, so half a profile is refused.
func TestModelOrEffortWithoutHarnessIsRefused(t *testing.T) {
	for _, req := range []spawn.SpawnCrewRequest{{Model: "opus"}, {Effort: harness.EffortHigh}} {
		_, _, err := applyDispatch(dispatchWorkspace(t, ""), req)
		var ue *usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--harness") {
			t.Fatalf("%+v: err = %v, want a usage error naming --harness", req, err)
		}
	}
}

// A malformed table is reported and corrected, never launched around, and
// never quietly replaced by the built-in one.
func TestAMalformedDispatchTableStopsEverySpawn(t *testing.T) {
	w := dispatchWorkspace(t, `{"rules": [{"when": "x", "use": {"harness": "codex", "effort": "max"}}]}`)
	_, _, err := applyDispatch(w, spawn.SpawnCrewRequest{Harness: harness.KindCodex})
	var ue *usageError
	if !errors.Is(err, dispatch.ErrInvalid) || !errors.As(err, &ue) {
		t.Fatalf("err = %v, want the table's own refusal, exit 2", err)
	}
}

// `mate crew dispatch` is how the Mate reads the table: every rule in
// order, each profile as the exact flags to pass.
func TestCrewDispatchPrintsTheTableAsFlags(t *testing.T) {
	w := dispatchWorkspace(t, sampleTable)
	var out, errw bytes.Buffer
	if err := run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch: %v\n%s", err, errw.String())
	}
	got := out.String()
	for _, want := range []string{
		"rule 1: a trivial mechanical edit",
		"use --harness claude --model haiku --effort low",
		"why cheap",
		"rule 2: a big or ambiguous feature",
		"or  --harness codex --model gpt-5.5 --effort xhigh",
		"default: --harness codex --effort medium",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
}

// Without a file of its own the workspace shows the built-in table, says
// how to replace it, and --example prints that table as JSON that loads.
func TestCrewDispatchWithoutATableShowsTheBuiltIn(t *testing.T) {
	w := dispatchWorkspace(t, "")
	var out, errw bytes.Buffer
	if err := run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch: %v", err)
	}
	for _, want := range []string{
		"crew dispatch: built-in",
		filepath.Join(".mate", "crew-dispatch.json"),
		"rule 1: A ship that is a small change",
		"use --harness claude --model sonnet --effort medium",
		"or  --harness codex --model gpt-6-luna --effort medium",
		"default: --harness codex --model gpt-6-luna --effort high",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := run([]string{"crew", "dispatch", "--example"}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch --example: %v", err)
	}
	p := filepath.Join(t.TempDir(), dispatch.FileName)
	if err := os.WriteFile(p, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := dispatch.Load(p); err != nil || !ok {
		t.Fatalf("the example does not load: %v\n%s", err, out.String())
	}
}

func TestHarnessCellNamesEachAxisThatWasSet(t *testing.T) {
	for _, tc := range []struct {
		c    spawn.CrewSummary
		want string
	}{
		{spawn.CrewSummary{Harness: "codex"}, "codex"},
		{spawn.CrewSummary{Harness: "codex", Model: "gpt-5.5", Effort: "high"}, "codex gpt-5.5/high"},
		{spawn.CrewSummary{Harness: "claude", Model: "haiku"}, "claude haiku"},
		{spawn.CrewSummary{Harness: "codex", Effort: "medium"}, "codex default/medium"},
	} {
		if got := harnessCell(tc.c); got != tc.want {
			t.Errorf("harnessCell(%+v) = %q, want %q", tc.c, got, tc.want)
		}
	}
}
