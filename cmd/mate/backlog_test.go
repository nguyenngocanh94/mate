package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// backlogFixtureWorkspace builds one project "shop" with six crews, one per
// state task 23 asks for: k1 spawned, k2 working, k3 needs-decision, k4
// wait-mate, k5 finished (closed), and k6 which last said `working:` but
// has an open incident, so it reads `blocked` (mvp.md section 4b overrides
// the crew's own verb with an open incident). Ages are spaced far enough
// apart that "oldest first" has one unambiguous order.
func backlogFixtureWorkspace(t *testing.T) (*store.Workspace, time.Time) {
	t.Helper()
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repo: repo, DefaultBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	started := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339) }

	type fixtureCrew struct {
		id     string
		meta   map[string]string
		status []string
	}
	crews := []fixtureCrew{
		{"k1", map[string]string{
			"harness": "codex", "branch": "mate/k1", "task": "Add health endpoint",
			"started_at": started(72 * time.Hour),
		}, nil},
		{"k2", map[string]string{
			"harness": "codex", "branch": "mate/k2", "task": "Fix flaky test",
			"started_at": started(2 * time.Hour),
		}, []string{"working: running the suite"}},
		{"k3", map[string]string{
			"harness": "codex", "branch": "mate/k3", "task": "Pick a colour",
			"started_at": started(4 * time.Minute),
		}, []string{"needs-decision: choose red or blue for the button"}},
		{"k4", map[string]string{
			"harness": "codex", "branch": "mate/k4", "task": "Ship the button",
			"started_at": started(30 * time.Minute),
		}, []string{"wait-mate: ready in branch mate/k4"}},
		{"k5", map[string]string{
			"harness": "codex", "branch": "mate/k5", "task": "Old ship",
			"started_at": started(120 * time.Hour),
			"state":      "finished", "teardown": "clean",
			"stopped_at": started(1 * time.Hour),
		}, nil},
		{"k6", map[string]string{
			"harness": "codex", "branch": "mate/k6", "task": "Investigate a crash",
			"started_at": started(10 * time.Minute),
		}, []string{"working: reading logs"}},
	}
	for _, c := range crews {
		if err := w.WriteCrewMeta("shop", c.id, c.meta); err != nil {
			t.Fatalf("WriteCrewMeta %s: %v", c.id, err)
		}
		for _, line := range c.status {
			if err := w.AppendStatus("shop", c.id, line); err != nil {
				t.Fatalf("AppendStatus %s: %v", c.id, err)
			}
		}
	}
	if err := w.AppendIncident("shop", store.IncidentEntry{
		Time: now.Add(-15 * time.Minute), Crew: "k6", Kind: "stale",
		State: store.IncidentOpen, Text: "no status for 15m",
	}); err != nil {
		t.Fatalf("AppendIncident: %v", err)
	}
	return w, now
}

// tableRowIDs returns the crew ids that lead each table row, in the order
// they were printed - the ID column always comes first and never carries
// whitespace, so the first whitespace-separated field of a row line is
// exactly the id (the same assumption cmd/mate/crew_test.go makes of
// `crew list`'s own table).
func tableRowIDs(out string, known map[string]bool) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if known[fields[0]] {
			ids = append(ids, fields[0])
		}
	}
	return ids
}

func allCrewIDs() map[string]bool {
	return map[string]bool{"k1": true, "k2": true, "k3": true, "k4": true, "k5": true, "k6": true}
}

func TestBacklogOldestFirstColumnsAndFooter(t *testing.T) {
	w, now := backlogFixtureWorkspace(t)

	var out bytes.Buffer
	if err := printBacklog(w, "shop", false, now, &out); err != nil {
		t.Fatalf("printBacklog: %v", err)
	}
	text := out.String()

	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	header := lines[0]
	for _, col := range []string{"ID", "STATE", "AGE", "BRANCH", "TASK", "LAST"} {
		if !strings.Contains(header, col) {
			t.Errorf("header = %q, missing column %q", header, col)
		}
	}

	// k5 is closed and must not appear without --all.
	if strings.Contains(text, "k5") {
		t.Errorf("out = %q, closed crew k5 must not appear without --all", text)
	}

	gotOrder := tableRowIDs(text, allCrewIDs())
	wantOrder := []string{"k1", "k2", "k4", "k6", "k3"} // oldest (3d) to youngest (4m)
	if strings.Join(gotOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("row order = %v, want %v (oldest first)", gotOrder, wantOrder)
	}

	// The example ages from the task's own spec: 3d, 2h, 30m, 10m, 4m.
	wantAges := map[string]string{"k1": "3d", "k2": "2h", "k4": "30m", "k6": "10m", "k3": "4m"}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if want, ok := wantAges[fields[0]]; ok && fields[2] != want {
			t.Errorf("row %s age = %q, want %q", fields[0], fields[2], want)
		}
	}

	if !strings.Contains(text, "5 open, 1 closed") {
		t.Fatalf("out = %q, want footer \"5 open, 1 closed\"", text)
	}

	// blocked (k6), needs-decision (k3) and wait-mate (k4) need attention,
	// in the order they appear in the table.
	wantAttention := "attention: k4, k6, k3"
	if !strings.Contains(text, wantAttention) {
		t.Fatalf("out = %q, want %q", text, wantAttention)
	}
}

func TestBacklogAllIncludesClosedCrews(t *testing.T) {
	w, now := backlogFixtureWorkspace(t)

	var out bytes.Buffer
	if err := printBacklog(w, "shop", true, now, &out); err != nil {
		t.Fatalf("printBacklog --all: %v", err)
	}
	text := out.String()

	if !strings.Contains(text, "k5") || !strings.Contains(text, "finished") {
		t.Fatalf("out = %q, want the closed crew k5 with state finished", text)
	}
	gotOrder := tableRowIDs(text, allCrewIDs())
	wantOrder := []string{"k5", "k1", "k2", "k4", "k6", "k3"} // k5 is oldest at 5d
	if strings.Join(gotOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("row order = %v, want %v (oldest first, --all)", gotOrder, wantOrder)
	}
	if !strings.Contains(text, "5 open, 1 closed") {
		t.Fatalf("out = %q, want footer \"5 open, 1 closed\" (the count does not change with --all)", text)
	}
}

func TestBacklogEmptyProjectPrintsOneLine(t *testing.T) {
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.Root(), "blog")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("blog", store.ProjectConfig{Repo: repo}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := printBacklog(w, "blog", false, time.Now(), &out); err != nil {
		t.Fatalf("printBacklog: %v", err)
	}
	if got := out.String(); got != "no crews for blog\n" {
		t.Fatalf("out = %q, want \"no crews for blog\\n\"", got)
	}
}

// TestBacklogStateMatchesQueryLoad is the restart-reconciliation claim
// task 23 asks for: the STATE column of `mate backlog` must equal the
// state query.Load resolves for the same crew, over the same workspace -
// that is what "a Mate restarting sees a backlog table that matches the
// console tree" means. It runs over every recognised state the fixture
// carries, including the incident-derived `blocked`.
func TestBacklogStateMatchesQueryLoad(t *testing.T) {
	w, now := backlogFixtureWorkspace(t)

	snap, err := query.Load(context.Background(), w)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	var proj *query.ProjectNode
	for i := range snap.Projects {
		if snap.Projects[i].ProjectID == "shop" {
			proj = &snap.Projects[i]
		}
	}
	if proj == nil {
		t.Fatal("query.Load did not return the shop project")
	}
	wantState := map[string]string{}
	for _, c := range proj.Crews {
		wantState[c.CrewID] = string(c.Status)
	}
	// query.Load never lists a closed crew (mvp.md section 4b); k5 is
	// checked separately below from its own meta, which is unambiguous.
	if _, ok := wantState["k5"]; ok {
		t.Fatal("query.Load must not list the closed crew k5 as a row")
	}

	var out bytes.Buffer
	if err := printBacklog(w, "shop", false, now, &out); err != nil {
		t.Fatalf("printBacklog: %v", err)
	}
	gotState := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if _, known := wantState[fields[0]]; known {
			gotState[fields[0]] = fields[1]
		}
	}
	for id, want := range wantState {
		if got := gotState[id]; got != want {
			t.Errorf("crew %s: backlog state = %q, query.Load state = %q, want them equal", id, got, want)
		}
	}
	if len(gotState) != len(wantState) {
		t.Fatalf("backlog reported %d open crews %v, query.Load reported %d %v", len(gotState), gotState, len(wantState), wantState)
	}
}
