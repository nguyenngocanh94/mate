package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestRunUnknownCrewSubcommandIsUsageError(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"crew", "bogus"}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestCrewSpawnRequiresProjectIDAndBrief(t *testing.T) {
	cases := [][]string{
		{"crew", "spawn"},
		{"crew", "spawn", "shop"},
		{"crew", "spawn", "shop", "k3"}, // no --brief
		{"crew", "spawn", "shop", "k3", "--brief", "b", "--harness", "gemini"}, // unknown harness
	}
	for _, args := range cases {
		var out, errw bytes.Buffer
		err := run(args, &out, &errw)
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Fatalf("%v: err = %v, want *usageError", args, err)
		}
	}
}

func TestCrewListPrintsTheRecordedCrews(t *testing.T) {
	root := t.TempDir()
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo}}}); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if err := run([]string{"crew", "list", "shop", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew list: %v", err)
	}
	if !strings.Contains(out.String(), "no crews recorded") {
		t.Fatalf("out = %q, want the empty-list line", out.String())
	}

	if err := w.WriteCrewMeta("shop", "k3", map[string]string{
		"harness": "codex",
		"model":   "gpt-5.5",
		"effort":  "high",
		"branch":  "mate/k3",
		"pane":    "w1:p2",
		"task":    "Add a healthcheck endpoint.",
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "done: ready in branch mate/k3"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"crew", "list", "shop", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew list: %v", err)
	}
	// STATE is the app's word, NOTE the crew's own. A legacy `done:` line
	// reads as `wait-mate` and its text lands in NOTE (mvp.md section 4b).
	// HARNESS carries the launch profile the crew was spawned with.
	for _, want := range []string{"k3", "codex gpt-5.5/high", "mate/k3", "wait-mate", "ready in branch mate/k3", "w1:p2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out = %q, want it to carry %q", out.String(), want)
		}
	}
}

func TestCrewListStateColumnShowsTheDeclaredState(t *testing.T) {
	root := t.TempDir()
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo}}}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		crew string
		meta map[string]string
		want string
	}{
		{"k1", map[string]string{"state": "finished", "teardown": "clean", "stopped_at": "2026-09-17T10:00:00Z"}, "finished"},
		{"k2", map[string]string{"state": "failed", "teardown": "discarded", "stopped_at": "2026-09-17T10:00:00Z"}, "failed"},
		{"k3", map[string]string{"state": "failed", "failed_reason": "startup screen not recognised"}, "failed"},
		// Pre-4b: a meta with stopped_at and no state= reads as finished.
		{"k4", map[string]string{"teardown": "clean", "stopped_at": "2026-09-17T10:00:00Z"}, "finished"},
	}
	for _, tc := range cases {
		if err := w.WriteCrewMeta("shop", tc.crew, tc.meta); err != nil {
			t.Fatal(err)
		}
	}

	// Every crew here is closed, so the default listing is the hint alone
	// and --all is what shows the rows.
	var out, errw bytes.Buffer
	if err := run([]string{"crew", "list", "shop", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew list: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "no open crews") || !strings.Contains(got, "4 closed") || !strings.Contains(got, "--all") {
		t.Fatalf("default crew list over closed crews = %q, want the count and the --all hint", got)
	}
	out.Reset()
	if err := run([]string{"crew", "list", "shop", "--all", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew list --all: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	rows := map[string]string{}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		rows[fields[0]] = line
	}
	for _, tc := range cases {
		line, ok := rows[tc.crew]
		if !ok {
			t.Fatalf("out = %q, missing row for %s", out.String(), tc.crew)
		}
		if !strings.Contains(line, tc.want) {
			t.Errorf("row for %s = %q, want it to carry %q", tc.crew, line, tc.want)
		}
	}
}

func TestCrewStopReportLineDescribesEachOutcome(t *testing.T) {
	cases := []struct {
		name string
		res  spawn.StopResult
		want []string
	}{
		{
			name: "clean",
			res: spawn.StopResult{
				Agent: "crew-k3", TabClosed: true,
				Teardown: spawn.TeardownClean, State: spawn.CrewStateFinished, Branch: "mate/k3",
			},
			want: []string{"removed", "already landed", "state finished"},
		},
		{
			name: "discarded",
			res: spawn.StopResult{
				Agent: "crew-k3", TabClosed: true,
				Teardown: spawn.TeardownDiscarded, State: spawn.CrewStateFailed, Ahead: 3, DirtyFiles: 2,
			},
			want: []string{"removed", "--discard", "3 commit(s)", "2 dirty file(s)", "state failed"},
		},
		{
			name: "already gone",
			res: spawn.StopResult{
				Agent: "crew-k3", AlreadyGone: true, TabClosed: true,
			},
			want: []string{"already gone", "kept"},
		},
		{
			name: "already closed",
			res: spawn.StopResult{
				AlreadyClosed: true, AlreadyGone: true, TabClosed: true,
				Teardown: spawn.TeardownClean, State: spawn.CrewStateFinished,
			},
			want: []string{"already closed", "state finished", "nothing changed"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := crewStopReport("shop", "k3", tc.res)
			for _, want := range tc.want {
				if !strings.Contains(line, want) {
					t.Errorf("line = %q, want it to carry %q", line, want)
				}
			}
		})
	}
}

func TestCrewStopRequiresProjectAndID(t *testing.T) {
	cases := [][]string{
		{"crew", "stop"},
		{"crew", "stop", "shop"},
	}
	for _, args := range cases {
		var out, errw bytes.Buffer
		err := run(args, &out, &errw)
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Fatalf("%v: err = %v, want *usageError", args, err)
		}
	}
}

// TestCrewListShowsOnlyOpenCrewsByDefault: a `wait-mate:` line is the
// crew's report, not the end of its task - the Mate or the captain ends it
// with `crew stop` (2026-09-18). So a crew that reported is still listed, a
// closed one is not, and the footer says how many are hidden.
func TestCrewListShowsOnlyOpenCrewsByDefault(t *testing.T) {
	w, err := store.Init(t.TempDir(), workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "k1", map[string]string{"task": "ship it", "agent": "crew-k1", "pane": "w1:p2"}); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k1", "wait-mate: ready in branch mate/k1"); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "k2", map[string]string{"task": "old", "state": "finished", "teardown": "clean", "stopped_at": "2026-09-18T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if err := run([]string{"crew", "list", "shop", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew list: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "k1") || !strings.Contains(got, "wait-mate") || !strings.Contains(got, "ready in branch") {
		t.Fatalf("crew list = %q, want the reported-but-open crew listed with its own note", got)
	}
	if strings.Contains(got, "k2") {
		t.Fatalf("crew list = %q, want the closed crew hidden", got)
	}
	if !strings.Contains(got, "1 closed") {
		t.Fatalf("crew list = %q, want the hidden count", got)
	}
}

func TestCrewRelaunchReportNamesTheLaunchProfileAndEndsTheTurn(t *testing.T) {
	var out bytes.Buffer
	writeCrewRelaunchReport(&out, spawn.RelaunchResult{
		Project: "shop", Crew: "k3", Agent: "crew-k3", Pane: "p9", Harness: claude.KindClaude,
		Model: "haiku", Effort: harness.EffortLow, Repo: "shop", Branch: "mate/k3",
		Worktree: "/w/.worktrees/shop-k3", BriefPath: "/w/brief.md", Stopped: true,
	})
	got := out.String()
	for _, want := range []string{
		"relaunched shop/k3: agent crew-k3 in pane p9 (harness claude, model haiku, effort low,",
		"the previous agent was stopped",
		"brief /w/brief.md",
		turnRelaunchLine("k3"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}
