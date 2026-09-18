package query

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

func newWorkspace(t *testing.T, projects ...string) *store.Workspace {
	t.Helper()
	root := t.TempDir()
	ws, err := store.Init(root)
	if err != nil {
		t.Fatalf("init workspace: %v", err)
	}
	for _, name := range projects {
		if err := os.MkdirAll(filepath.Join(root, name, ".git"), 0o755); err != nil {
			t.Fatalf("create repo %s: %v", name, err)
		}
		if err := ws.AddProject(name, store.ProjectConfig{Repo: name}); err != nil {
			t.Fatalf("add project %s: %v", name, err)
		}
	}
	return ws
}

func TestLoadListsRegisteredProjectsWithTheirRepo(t *testing.T) {
	ws := newWorkspace(t, "shop", "blog")
	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(snap.Projects) != 2 {
		t.Fatalf("projects = %+v, want two", snap.Projects)
	}
	if snap.Workspace.State != Known || snap.Workspace.Value.Root != ws.Root() {
		t.Fatalf("workspace field = %+v, want the store's own resolved root", snap.Workspace)
	}
	if snap.AsOf.IsZero() {
		t.Fatal("AsOf is zero; a snapshot must say how old it is")
	}
	p := snap.Projects[0]
	if p.ProjectID != "shop" || p.Name != "shop" {
		t.Fatalf("first project = %+v, want shop", p)
	}
	if p.Repos.State != Known || len(p.Repos.Value) != 1 {
		t.Fatalf("repos = %+v, want the one registered repo", p.Repos)
	}
	if got, want := p.Repos.Value[0].Path, filepath.Join(ws.Root(), "shop"); got != want {
		t.Fatalf("repo path = %q, want %q", got, want)
	}
	if got := p.Repos.Value[0].DefaultBranch; got != store.DefaultBranch {
		t.Fatalf("default branch = %q, want %q", got, store.DefaultBranch)
	}
}

// TestLoadReportsNoMateAsAbsentNotUnknown: a project whose mate.meta does
// not exist has no Mate, which the read established. That is Absent - the
// state the Console renders as "none assigned" - and never Unknown, which
// would claim the read failed.
func TestLoadReportsNoMateAsAbsentNotUnknown(t *testing.T) {
	ws := newWorkspace(t, "shop")
	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	mate := snap.Projects[0].Mate
	if mate.Designated.State != Absent {
		t.Fatalf("designation = %+v, want Absent for a project with no mate.meta", mate.Designated)
	}
	if mate.Designated.Reason == "" {
		t.Fatal("an Absent designation must carry the reason there is no Mate")
	}
	if len(snap.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none: nothing failed to read", snap.Warnings)
	}
	if a := snap.Projects[0].Attention; a.State != Known || a.Value.Kind != AttentionNoMate {
		t.Fatalf("project attention = %+v, want the no-mate kind", a)
	}
}

// TestLoadReadsMateMetaAndCrewStatus is the shape mvp.md section 3
// describes: a Mate row exists because mate.meta exists, its recorded pane
// is what makes it running, and a Crew's status is the last line of its
// own .status file.
func TestLoadReadsMateMetaAndCrewStatus(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteMateMeta("shop", map[string]string{
		"harness": "claude", "session": "fm-x", "pane": "p-1", "agent": "mate-shop",
	}); err != nil {
		t.Fatalf("write mate.meta: %v", err)
	}
	if err := ws.WriteCrewMeta("shop", "k3", map[string]string{
		"task": "wire the webhook", "harness": "codex",
		"worktree": "/w/shop-k3", "branch": "matev2/k3",
	}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	for _, line := range []string{"working: reading the brief", "needs-decision: which adapter?"} {
		if err := ws.AppendStatus("shop", "k3", line); err != nil {
			t.Fatalf("append status: %v", err)
		}
	}

	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	if p.Mate.Designated.State != Known {
		t.Fatalf("designation = %+v, want Known once mate.meta exists", p.Mate.Designated)
	}
	if got := p.Mate.Designated.Value.Status; got != MateRunning {
		t.Fatalf("mate status = %q, want running for a recorded pane", got)
	}
	if got := p.Mate.Designated.Value.HarnessKind; got != HarnessClaude {
		t.Fatalf("mate harness = %q, want claude", got)
	}
	if p.Mate.Binding.State != Known || p.Mate.Binding.Reason == "" {
		t.Fatalf("binding = %+v, want Known with the caveat that it proves nothing about the agent", p.Mate.Binding)
	}

	if len(p.Crews) != 1 {
		t.Fatalf("crews = %+v, want the one crew meta", p.Crews)
	}
	c := p.Crews[0]
	if c.CrewID != "k3" || c.Task != "wire the webhook" || c.HarnessKind != HarnessCodex {
		t.Fatalf("crew = %+v, want k3 from its meta", c)
	}
	if got := c.Status; got != CrewStatus("needs-decision") {
		t.Fatalf("crew status = %q, want the last status line's state word", got)
	}
	if c.Worktree.State != Known || c.Worktree.Value.Branch != "matev2/k3" {
		t.Fatalf("worktree = %+v, want the recorded worktree and branch", c.Worktree)
	}
}

// TestLoadGivesACrewThatWroteNothingTheReservedStatus: the meta is what
// records the crew, so a crew with no status line yet is reserved, not
// blank and not unknown.
func TestLoadGivesACrewThatWroteNothingTheReservedStatus(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteCrewMeta("shop", "k9", map[string]string{"task": "scout"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := snap.Projects[0].Crews[0].Status; got != CrewReserved {
		t.Fatalf("status = %q, want %q", got, CrewReserved)
	}
}

// TestLoadPicksUpAProjectRegisteredAfterOpen: the Console's 'r' is meant to
// show a project another process just added, so every load re-reads
// workspace.yaml rather than answering from the config cached at Open.
func TestLoadPicksUpAProjectRegisteredAfterOpen(t *testing.T) {
	ws := newWorkspace(t, "shop")
	other, err := store.Open(ws.Root())
	if err != nil {
		t.Fatalf("open workspace again: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ws.Root(), "blog", ".git"), 0o755); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := other.AddProject("blog", store.ProjectConfig{Repo: "blog"}); err != nil {
		t.Fatalf("add project: %v", err)
	}
	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(snap.Projects) != 2 {
		t.Fatalf("projects = %+v, want the newly registered one too", snap.Projects)
	}
}

// TestLoadGivesATornDownSilentCrewTheStoppedStatus: StopCrew leaves the
// meta with stopped_at and no agent. A crew that never wrote a status
// line is then stopped, not reserved - reserved is the promise of a start
// (2026-09-18: the tree showed a discarded test crew as reserved).
func TestLoadGivesATornDownSilentCrewTheStoppedStatus(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteCrewMeta("shop", "k9", map[string]string{
		"task": "scout", "stopped_at": "2026-09-18T10:18:34Z", "teardown": "clean"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	snap, err := Load(context.Background(), ws)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := snap.Projects[0].Crews[0].Status; got != CrewStopped {
		t.Fatalf("status = %q, want %q", got, CrewStopped)
	}
	// One that wrote before it was stopped keeps its own last word.
	if err := ws.AppendStatus("shop", "k9", "done: report ready"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if snap, err = Load(context.Background(), ws); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := snap.Projects[0].Crews[0].Status; got != CrewStatus("done") {
		t.Fatalf("status = %q, want done", got)
	}
}
