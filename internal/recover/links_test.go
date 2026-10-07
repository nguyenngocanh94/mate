package recover

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v in %s: %v\n%s", name, args, dir, err, out)
	}
	return string(out)
}

// crewWorkspace is a workspace at root with one project, a real git repo with
// a commit, and crew k1 in a real worktree with a brief that names absolute
// paths, which is everything a move can break.
func crewWorkspace(t *testing.T, root string) *store.Workspace {
	t.Helper()
	w, err := store.Init(root, store.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	repo := w.RepoDir("shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "init", "--quiet", "-b", "main")
	run(t, repo, "git", "-c", "user.email=a@b", "-c", "user.name=n", "commit", "--quiet", "--allow-empty", "-m", "init")
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop", DefaultBranch: "main"}}}); err != nil {
		t.Fatal(err)
	}
	wt := w.WorktreeDir("shop", "k1")
	if err := gitx.New().AddWorktree(context.Background(), repo, wt, "mate/k1", "main"); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteMateMeta("shop", map[string]string{spawn.MetaHarness: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "k1", map[string]string{
		"task": "ship", "state": "spawned", "repo": "shop", "branch": "mate/k1",
		spawn.MetaWorktree: ".worktrees/shop-k1",
	}); err != nil {
		t.Fatal(err)
	}
	brief := "Work in " + wt + ".\nReport to " + w.CrewReport("shop", "k1") + ".\nSibling " + w.Root() + "-other/x stays.\n"
	if err := os.MkdirAll(w.CrewDir("shop", "k1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.CrewBrief("shop", "k1"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	return w
}

func moved(t *testing.T, from *store.Workspace) (*store.Workspace, string) {
	t.Helper()
	oldRoot := from.Root()
	next := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Rename(oldRoot, next); err != nil {
		t.Fatal(err)
	}
	w, err := store.Open(next)
	if err != nil {
		t.Fatal(err)
	}
	return w, oldRoot
}

func envFor(t *testing.T, w *store.Workspace) Env {
	t.Helper()
	return Env{WS: w, Git: gitx.New(), Harnesses: catalog.Default(), Binary: filepath.Join(t.TempDir(), "mate"), ConfigHome: t.TempDir()}
}

func noErrors(t *testing.T, fixes []Fix) {
	t.Helper()
	for _, f := range fixes {
		if f.Err != nil {
			t.Fatalf("%s: %s: %v", f.Step, f.What, f.Err)
		}
	}
}

func TestRepairLinksAfterTheWorkspaceMoved(t *testing.T) {
	w, oldRoot := moved(t, crewWorkspace(t, filepath.Join(t.TempDir(), "ws")))
	env := envFor(t, w)
	wt := w.WorktreeDir("shop", "k1")
	if ok, _ := env.Git.WorktreeAttached(context.Background(), wt); ok {
		t.Fatal("the moved worktree is attached before any repair; the test proves nothing")
	}

	fixes := RepairLinks(context.Background(), env)
	noErrors(t, fixes)
	if ok, err := env.Git.WorktreeAttached(context.Background(), wt); err != nil || !ok {
		t.Fatalf("worktree attached = %v, %v after recovery", ok, err)
	}
	if out := run(t, wt, "git", "status", "--short"); out != "" {
		t.Fatalf("git status in the repaired worktree = %q", out)
	}
	brief, err := os.ReadFile(w.CrewBrief("shop", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(brief), oldRoot+"/") {
		t.Fatalf("brief still names the old root:\n%s", brief)
	}
	if !strings.Contains(string(brief), "Work in "+wt+".") || !strings.Contains(string(brief), w.CrewReport("shop", "k1")) {
		t.Fatalf("brief does not name the new paths:\n%s", brief)
	}
	if !strings.Contains(string(brief), oldRoot+"-other/x") {
		t.Fatalf("a sibling path that only shares the prefix was rewritten:\n%s", brief)
	}
	reopened, err := store.Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	if reopened.RecordedRoot() != w.Root() {
		t.Fatalf("recorded root = %q, want %q", reopened.RecordedRoot(), w.Root())
	}

	// A second pass over a recovered workspace has nothing to do.
	if again := RepairLinks(context.Background(), envFor(t, reopened)); len(again) != 0 {
		t.Fatalf("second pass fixed %+v, want nothing", again)
	}
}

func TestRepairLinksInfersTheOldRootOfAWorkspaceWithNoRecordedRoot(t *testing.T) {
	w := crewWorkspace(t, filepath.Join(t.TempDir(), "ws"))
	if err := w.SetRoot(""); err != nil {
		t.Fatal(err)
	}
	w, oldRoot := moved(t, w)
	if w.RecordedRoot() != "" {
		t.Fatal("the workspace still records a root")
	}
	noErrors(t, RepairLinks(context.Background(), envFor(t, w)))
	brief, _ := os.ReadFile(w.CrewBrief("shop", "k1"))
	if strings.Contains(string(brief), oldRoot+"/") || !strings.Contains(string(brief), w.WorktreeDir("shop", "k1")) {
		t.Fatalf("brief not rewritten from the inferred root:\n%s", brief)
	}
	if w.RecordedRoot() != w.Root() {
		t.Fatalf("recorded root = %q, want %q", w.RecordedRoot(), w.Root())
	}
}

func TestRepairHooksKeepsOtherHooksAndLiveBinaries(t *testing.T) {
	w := crewWorkspace(t, filepath.Join(t.TempDir(), "ws"))
	env := envFor(t, w)
	live := filepath.Join(t.TempDir(), "other-mate")
	if err := os.WriteFile(live, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"autoMemoryEnabled": false, "hooks": {
  "Stop": [{"hooks": [{"type": "command", "command": "'/gone/mate' hook mate-stop"}]}],
  "PreToolUse": [{"hooks": [{"type": "command", "command": "/captain/lint --quick"}]}],
  "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "` + "'" + live + "'" + ` hook mate-prompt"}]}]}}`
	path := claude.ClaudeSettingsPath(w.MateDir("shop"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	noErrors(t, RepairHooks(env))
	got, _ := os.ReadFile(path)
	for _, want := range []string{"'" + env.Binary + "' hook mate-stop", "/captain/lint --quick", live + "' hook mate-prompt"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("settings lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(string(got), "/gone/mate") {
		t.Fatalf("the missing binary is still named:\n%s", got)
	}
}

func ownerOf(t *testing.T, env Env, session string) string {
	t.Helper()
	owner, _, err := runtime.SessionOwner(env.ConfigHome, session)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestRepairOwnerMatchingMarkerChangesNothing(t *testing.T) {
	w := crewWorkspace(t, filepath.Join(t.TempDir(), "ws"))
	env := envFor(t, w)
	id := store.SessionName(w.Root())
	if err := runtime.ReclaimSessionOwner(env.ConfigHome, w.Session(), id); err != nil {
		t.Fatal(err)
	}
	if fixes := RepairOwner(env, w.Root()); len(fixes) != 0 {
		t.Fatalf("fixes = %+v, want none for a marker that names this workspace", fixes)
	}
	if w.Session() != store.SessionName(w.Root()) || ownerOf(t, env, w.Session()) != id {
		t.Fatal("a matching marker was disturbed")
	}
}

func TestRepairOwnerTakesOverFromAWorkspaceThatIsGone(t *testing.T) {
	orig := crewWorkspace(t, filepath.Join(t.TempDir(), "ws"))
	session := orig.Session()
	w, oldRoot := moved(t, orig)
	env := envFor(t, w)
	if err := runtime.ReclaimSessionOwner(env.ConfigHome, session, store.SessionName(oldRoot)); err != nil {
		t.Fatal(err)
	}
	fixes := RepairOwner(env, oldRoot)
	noErrors(t, fixes)
	if len(fixes) != 1 || w.Session() != session {
		t.Fatalf("fixes = %+v session = %q, want the same session kept", fixes, w.Session())
	}
	if got := ownerOf(t, env, session); got != store.SessionName(w.Root()) {
		t.Fatalf("owner = %q, want this workspace", got)
	}
}

func TestRepairOwnerGivesACopyBesideTheOriginalItsOwnSession(t *testing.T) {
	orig := crewWorkspace(t, filepath.Join(t.TempDir(), "ws"))
	session := orig.Session()
	copyRoot := filepath.Join(t.TempDir(), "copy")
	run(t, filepath.Dir(orig.Root()), "cp", "-R", orig.Root(), copyRoot)
	w, err := store.Open(copyRoot)
	if err != nil {
		t.Fatal(err)
	}
	env := envFor(t, w)
	if err := runtime.ReclaimSessionOwner(env.ConfigHome, session, store.SessionName(orig.Root())); err != nil {
		t.Fatal(err)
	}
	fixes := RepairOwner(env, orig.Root())
	noErrors(t, fixes)
	if len(fixes) != 1 {
		t.Fatalf("fixes = %+v, want one", fixes)
	}
	reopened, _ := store.Open(copyRoot)
	if reopened.Session() == session || reopened.Session() != store.SessionName(w.Root()) {
		t.Fatalf("copy session = %q, want its own name (original %q)", reopened.Session(), session)
	}
	if got := ownerOf(t, env, session); got != store.SessionName(orig.Root()) {
		t.Fatalf("the original's marker changed to %q", got)
	}
}
