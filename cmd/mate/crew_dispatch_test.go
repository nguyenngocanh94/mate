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

// With a dispatch table in the workspace, a crew is never launched on the
// workspace default by omission: the Mate chose a profile from the table,
// or it has to say which harness (firstmate's fm-spawn.sh contract).
func TestCrewDispatchTableRequiresAnExplicitHarness(t *testing.T) {
	w := dispatchWorkspace(t, sampleTable)
	err := checkDispatch(w, spawn.SpawnCrewRequest{Project: "shop", Crew: "k3"})
	var ue *usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--harness") || !strings.Contains(err.Error(), "mate crew dispatch") {
		t.Fatalf("err = %v, want a usage error naming --harness and the table", err)
	}
	if err := checkDispatch(w, spawn.SpawnCrewRequest{Harness: harness.KindClaude, Model: "haiku", Effort: harness.EffortLow}); err != nil {
		t.Fatalf("an explicit profile was refused: %v", err)
	}
}

func TestNoDispatchTableKeepsTheWorkspaceDefault(t *testing.T) {
	if err := checkDispatch(dispatchWorkspace(t, ""), spawn.SpawnCrewRequest{}); err != nil {
		t.Fatalf("no table refused a default spawn: %v", err)
	}
}

// A malformed table is reported and corrected, never launched around.
func TestAMalformedDispatchTableStopsEverySpawn(t *testing.T) {
	w := dispatchWorkspace(t, `{"rules": [{"when": "x", "use": {"harness": "codex", "effort": "max"}}]}`)
	err := checkDispatch(w, spawn.SpawnCrewRequest{Harness: harness.KindCodex})
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

func TestCrewDispatchWithoutATableSaysSoAndShowsAStart(t *testing.T) {
	w := dispatchWorkspace(t, "")
	var out, errw bytes.Buffer
	if err := run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch: %v", err)
	}
	if !strings.Contains(out.String(), "no crew dispatch table") || !strings.Contains(out.String(), filepath.Join(".mate", "crew-dispatch.json")) {
		t.Fatalf("output = %q", out.String())
	}
	out.Reset()
	if err := run([]string{"crew", "dispatch", "--example"}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch --example: %v", err)
	}
	// The example is itself a valid table.
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
