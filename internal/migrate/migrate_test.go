package migrate_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/migrate"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// These tests move real git repositories in temporary workspaces: what a
// migrate must not break is what git does with a moved repository and its
// linked worktrees, which a fake would not show.

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_AUTHOR_NAME=mate test", "GIT_AUTHOR_EMAIL=mate-test@example.com",
		"GIT_COMMITTER_NAME=mate test", "GIT_COMMITTER_EMAIL=mate-test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %s: %v\n%s", dir, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// commitRepo makes dir a repository on main with one committed README.
func commitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(dir, "README.md"), filepath.Base(dir)+"\n")
	git(t, dir, "add", "README.md")
	git(t, dir, "commit", "-q", "-m", "readme")
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type project struct {
	name  string
	repos []string // workspace-relative paths, beside .mate/
}

// oldWorkspace writes a workspace as it was before layout 2: every repo a
// committed git repository at its path beside `.mate/`, project.yaml naming
// it there, and a workspace.yaml with no `layout:`. It is built by hand
// because store no longer makes one.
func oldWorkspace(t *testing.T, projects ...project) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	ws := "version: 1\nsession: mate-old\nroot: " + w.Root() + "\ndefaults:\n    mate_harness: claude\n    crew_harness: codex\nprojects:\n"
	for _, p := range projects {
		ws += "    - name: " + p.name + "\n"
		yaml := "repos:\n"
		for _, rel := range p.repos {
			commitRepo(t, filepath.Join(w.Root(), rel))
			yaml += "    - name: " + filepath.Base(rel) + "\n      path: " + rel + "\n      default_branch: main\n"
		}
		for _, dir := range []string{w.MateDir(p.name), w.CrewsDir(p.name)} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		writeFile(t, w.ProjectFile(p.name), yaml)
	}
	writeFile(t, w.WorkspaceFile(), ws)
	old, err := store.OpenForMigrate(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	if !old.LayoutOld() {
		t.Fatal("the hand-built workspace does not read as the old layout")
	}
	return old
}

// closedCrew records a finished crew of repo, with its linked worktree still
// on disk and a brief naming paths in the workspace.
func closedCrew(t *testing.T, w *store.Workspace, proj, crew, repo, repoRel, brief string) string {
	t.Helper()
	wt := w.WorktreeDir(proj, crew)
	git(t, filepath.Join(w.Root(), repoRel), "worktree", "add", "-q", "-b", "mate/"+crew, wt)
	if err := w.WriteCrewMeta(proj, crew, map[string]string{
		"task": "done work", "state": "finished", "repo": repo,
		"worktree": ".worktrees/" + proj + "-" + crew, "branch": "mate/" + crew,
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, w.CrewBrief(proj, crew), brief)
	return wt
}

func liveDeps(t *testing.T) (migrate.Deps, *runtime.Fake, runtime.SessionHandle) {
	t.Helper()
	rt := runtime.NewFake()
	spec := runtime.SessionSpec{WorkspaceID: "ws-migrate", Name: "fm-migrate", ConfigHome: t.TempDir(), Names: rt.Names}
	session, err := rt.EnsureSession(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return migrate.Deps{Runtime: rt, Session: spec}, rt, session
}

// tree is every path under root with a digest of its content, so a test can
// prove that nothing changed.
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			out[rel] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func diffTrees(before, after map[string]string) []string {
	var d []string
	for k, v := range before {
		if after[k] != v {
			d = append(d, "changed or gone: "+k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			d = append(d, "new: "+k)
		}
	}
	sort.Strings(d)
	return d
}

func attached(t *testing.T, wt string) bool {
	t.Helper()
	ok, err := gitx.New().WorktreeAttached(context.Background(), wt)
	return err == nil && ok
}

func repoPaths(t *testing.T, w *store.Workspace, proj string) []string {
	t.Helper()
	cfg, err := w.LoadProject(proj)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range cfg.Repos {
		out = append(out, r.Path)
	}
	return out
}

func refused(t *testing.T, err error) []string {
	t.Helper()
	var re *migrate.RefusedError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	lines := strings.Split(re.Error(), "\n")
	if lines[0] != "nothing moved" {
		t.Fatalf("a refusal's first line is %q, want `nothing moved`", lines[0])
	}
	return lines[1:]
}

// TestMigrateMovesTwoReposOfAProject is plan test 8: the dry run lists the
// moves and changes nothing; the migrate moves both repos with the dirty
// working tree as it was, re-attaches the closed crew's worktree, records
// the new paths and the new brief, and a second run has nothing to do.
func TestMigrateMovesTwoReposOfAProject(t *testing.T) {
	w := oldWorkspace(t, project{"shop", []string{"web", "api"}})
	root := w.Root()
	deps, _, _ := liveDeps(t)

	// Uncommitted work in web: a changed tracked file and an untracked one.
	writeFile(t, filepath.Join(root, "web", "README.md"), "web, edited\n")
	writeFile(t, filepath.Join(root, "web", "scratch.txt"), "not committed\n")
	brief := "Work in " + root + "/web/src. The API is " + root + "/api; " + root + "/webapp is another thing.\n"
	wt := closedCrew(t, w, "shop", "k1", "web", "web", brief)

	before := tree(t, root)
	var dry bytes.Buffer
	if _, err := migrate.DryRun(context.Background(), w, deps, &dry); err != nil {
		t.Fatalf("DryRun: %v\n%s", err, dry.String())
	}
	wantDry := []string{
		"would move " + root + "/web -> " + root + "/shop/web (2 uncommitted change(s) kept)",
		"would move " + root + "/api -> " + root + "/shop/api",
		"would write workspace layout 2",
	}
	if got := strings.Split(strings.TrimSpace(dry.String()), "\n"); strings.Join(got, "\n") != strings.Join(wantDry, "\n") {
		t.Fatalf("dry run printed\n%s\nwant\n%s", dry.String(), strings.Join(wantDry, "\n"))
	}
	if d := diffTrees(before, tree(t, root)); len(d) != 0 {
		t.Fatalf("the dry run changed the workspace:\n%s", strings.Join(d, "\n"))
	}

	var out bytes.Buffer
	sum, err := migrate.Run(context.Background(), w, deps, &out)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	want := []string{
		"moved " + root + "/web -> " + root + "/shop/web (2 uncommitted change(s) kept)",
		"moved " + root + "/api -> " + root + "/shop/api",
		"repaired 1 worktree(s)",
		"rewrote 1 brief(s)",
		"workspace layout 2",
	}
	if got := strings.TrimSpace(out.String()); got != strings.Join(want, "\n") {
		t.Fatalf("Run printed\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}
	if len(sum.Moves) != 2 || sum.Repaired != 1 || sum.Rewrote != 1 || !sum.Layout {
		t.Fatalf("summary = %+v", sum)
	}

	for _, gone := range []string{"web", "api"} {
		if _, err := os.Lstat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s is still beside .mate/: %v", gone, err)
		}
	}
	web := filepath.Join(root, "shop", "web")
	if got := readFile(t, filepath.Join(web, "README.md")); got != "web, edited\n" {
		t.Fatalf("the uncommitted edit is lost: %q", got)
	}
	if got := readFile(t, filepath.Join(web, "scratch.txt")); got != "not committed\n" {
		t.Fatalf("the untracked file is lost: %q", got)
	}
	if status := git(t, web, "status", "--porcelain"); !strings.Contains(status, " M README.md") || !strings.Contains(status, "?? scratch.txt") {
		t.Fatalf("git status of the moved repo:\n%s", status)
	}
	list := git(t, web, "worktree", "list", "--porcelain")
	if !strings.Contains(list, "worktree "+wt+"\n") || strings.Contains(list, "prunable") {
		t.Fatalf("git worktree list of the moved repo does not hold the crew's worktree as valid:\n%s", list)
	}
	if !attached(t, wt) {
		t.Fatal("the closed crew's worktree is not attached after the migrate")
	}
	if got := strings.TrimSpace(git(t, wt, "rev-parse", "--abbrev-ref", "HEAD")); got != "mate/k1" {
		t.Fatalf("the worktree is on %q, want mate/k1", got)
	}

	if got := repoPaths(t, w, "shop"); strings.Join(got, ",") != "shop/web,shop/api" {
		t.Fatalf("project.yaml repo paths = %v", got)
	}
	wantBrief := "Work in " + root + "/shop/web/src. The API is " + root + "/shop/api; " + root + "/webapp is another thing.\n"
	if got := readFile(t, w.CrewBrief("shop", "k1")); got != wantBrief {
		t.Fatalf("brief = %q\nwant  %q", got, wantBrief)
	}
	entries, err := w.ReadMigrateLog()
	if err != nil {
		t.Fatal(err)
	}
	var done []string
	for _, e := range entries {
		if e.Done() {
			done = append(done, e.Project+" "+e.Old+" -> "+e.New)
		}
	}
	if strings.Join(done, "\n") != "shop "+root+"/web -> "+root+"/shop/web\nshop "+root+"/api -> "+root+"/shop/api" {
		t.Fatalf("migrate.log finished lines:\n%s", strings.Join(done, "\n"))
	}
	reopened, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Layout() != 2 {
		t.Fatalf("workspace.yaml layout = %d, want 2", reopened.Layout())
	}

	// The workspace is a layout 2 workspace now: a second run does nothing.
	settled := tree(t, root)
	out.Reset()
	if _, err := migrate.Run(context.Background(), reopened, deps, &out); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "nothing to do" {
		t.Fatalf("second run printed %q, want `nothing to do`", got)
	}
	if d := diffTrees(settled, tree(t, root)); len(d) != 0 {
		t.Fatalf("the second run changed the workspace:\n%s", strings.Join(d, "\n"))
	}
}

// TestMigrateMovesARepoNamedLikeItsProject is the shape the captain's own
// workspace has: project shop's repo is `<root>/shop`, which is the
// project's directory itself. The repo is moved aside and back in under
// the new directory, beside the project's other repo, and every path in a
// brief lands where it now is.
func TestMigrateMovesARepoNamedLikeItsProject(t *testing.T) {
	w := oldWorkspace(t, project{"shop", []string{"web", "shop"}})
	root := w.Root()
	deps, _, _ := liveDeps(t)
	writeFile(t, filepath.Join(root, "shop", "notes.txt"), "uncommitted\n")
	// A directory inside the shop repo named like the other repo: its path
	// today is <root>/shop/web, the web repo's path tomorrow.
	writeFile(t, filepath.Join(root, "shop", "web", "index.html"), "<p>shop</p>\n")
	git(t, filepath.Join(root, "shop"), "add", "web")
	git(t, filepath.Join(root, "shop"), "commit", "-q", "-m", "web dir")
	brief := "Repo " + root + "/shop, page " + root + "/shop/web/index.html, sibling " + root + "/web.\n"
	wt := closedCrew(t, w, "shop", "k1", "shop", "shop", brief)

	var out bytes.Buffer
	if _, err := migrate.Run(context.Background(), w, deps, &out); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	want := []string{
		"moved " + root + "/shop -> " + root + "/shop/shop (1 uncommitted change(s) kept)",
		"moved " + root + "/web -> " + root + "/shop/web",
		"repaired 1 worktree(s)",
		"rewrote 1 brief(s)",
		"workspace layout 2",
	}
	if got := strings.TrimSpace(out.String()); got != strings.Join(want, "\n") {
		t.Fatalf("Run printed\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}

	shop := filepath.Join(root, "shop", "shop")
	if !gitx.HasGitDir(shop) || !gitx.HasGitDir(filepath.Join(root, "shop", "web")) {
		t.Fatal("the repos are not at shop/shop and shop/web")
	}
	if gitx.HasGitDir(filepath.Join(root, "shop")) {
		t.Fatal("the project's directory is still a repository")
	}
	if got := readFile(t, filepath.Join(shop, "notes.txt")); got != "uncommitted\n" {
		t.Fatalf("the uncommitted file is lost: %q", got)
	}
	if got := readFile(t, filepath.Join(shop, "web", "index.html")); got != "<p>shop</p>\n" {
		t.Fatalf("shop's own web directory is lost: %q", got)
	}
	if got := readFile(t, filepath.Join(root, "shop", "web", "README.md")); got != "web\n" {
		t.Fatalf("shop/web is not the web repo: %q", got)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".migrating-") {
			t.Fatalf("a staging directory is left: %s", e.Name())
		}
	}
	if !attached(t, wt) || !strings.Contains(git(t, shop, "worktree", "list", "--porcelain"), "worktree "+wt+"\n") {
		t.Fatal("the closed crew's worktree is not attached to shop/shop")
	}
	if got := repoPaths(t, w, "shop"); strings.Join(got, ",") != "shop/web,shop/shop" {
		t.Fatalf("project.yaml repo paths = %v", got)
	}
	wantBrief := "Repo " + root + "/shop/shop, page " + root + "/shop/shop/web/index.html, sibling " + root + "/shop/web.\n"
	if got := readFile(t, w.CrewBrief("shop", "k1")); got != wantBrief {
		t.Fatalf("brief = %q\nwant  %q", got, wantBrief)
	}
	log := readFile(t, w.MigrateLog())
	if !strings.Contains(log, "\tshop\t"+root+"/shop\t"+root+"/shop/shop\tstaged "+root+"/shop.migrating-") ||
		!strings.Contains(log, "\tshop\t"+root+"/shop\t"+root+"/shop/shop\trenamed\n") ||
		!strings.Contains(log, "\tshop\t"+root+"/shop\t"+root+"/shop/shop\n") {
		t.Fatalf("migrate.log does not record both renames and the finished move:\n%s", log)
	}
}

// TestMigrateRefusesBeforeMovingAnything is plan test 9: an open crew on a
// repo to move, a running Mate, and a destination that exists each refuse,
// every reason is said at once, and nothing is renamed in either project.
func TestMigrateRefusesBeforeMovingAnything(t *testing.T) {
	w := oldWorkspace(t,
		project{"shop", []string{"web", "api"}},
		project{"blog", []string{"site"}},
	)
	root := w.Root()
	deps, rt, session := liveDeps(t)

	// shop: crew k2 is open on web.
	if err := w.WriteCrewMeta("shop", "k2", map[string]string{"task": "fix", "state": "spawned", "repo": "web"}); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k2", "working: on it"); err != nil {
		t.Fatal(err)
	}
	// shop: its Mate is running, as Herdr says.
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude", "pane": "w1:p1", "agent": "mate-shop"}); err != nil {
		t.Fatal(err)
	}
	rt.PutAgent(runtime.AgentHandle{Session: session, Name: "mate-shop", Tab: runtime.TabHandle{Session: session, PaneID: "w1:p1"}}, runtime.AgentIdle)
	// blog: the destination of site already exists.
	writeFile(t, filepath.Join(root, "blog", "site", "keep.txt"), "the captain's\n")

	before := tree(t, root)
	var out bytes.Buffer
	_, err := migrate.Run(context.Background(), w, deps, &out)
	reasons := refused(t, err)
	joined := strings.Join(reasons, "\n")
	for _, want := range []string{
		"crew shop/k2 is working in repo web",
		"the Mate of shop is running",
		root + "/blog/site already exists",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, joined)
		}
	}
	if len(reasons) != 3 {
		t.Errorf("the refusal has %d reasons, want one line each for 3:\n%s", len(reasons), joined)
	}
	if out.Len() != 0 {
		t.Errorf("a refused run printed moves:\n%s", out.String())
	}
	if d := diffTrees(before, tree(t, root)); len(d) != 1 || d[0] != "new: .mate/migrate.lock" {
		t.Fatalf("a refused run changed the workspace beyond its lock file:\n%s", strings.Join(d, "\n"))
	}

	// The dry run says the same, and writes nothing at all.
	before = tree(t, root)
	var dry bytes.Buffer
	_, err = migrate.DryRun(context.Background(), w, deps, &dry)
	if got := refused(t, err); strings.Join(got, "\n") != joined {
		t.Fatalf("dry run refused with\n%s\nwant\n%s", strings.Join(got, "\n"), joined)
	}
	if d := diffTrees(before, tree(t, root)); len(d) != 0 {
		t.Fatalf("the dry run changed the workspace:\n%s", strings.Join(d, "\n"))
	}

	// Each reason goes away on its own. With only the crew left, only the
	// crew is said; a closed crew is no reason.
	rt.DropAgent(runtime.AgentHandle{Session: session, Name: "mate-shop"})
	if err := os.Rename(filepath.Join(root, "blog", "site"), filepath.Join(root, "blog", "site-kept")); err != nil {
		t.Fatal(err)
	}
	_, err = migrate.Run(context.Background(), w, deps, &out)
	if got := refused(t, err); len(got) != 1 || !strings.Contains(got[0], "crew shop/k2") {
		t.Fatalf("with the Mate stopped and the destination free, refused with %v", got)
	}
	// blog has nothing in its way, and still nothing of it moved.
	if !gitx.HasGitDir(filepath.Join(root, "site")) {
		t.Fatal("blog's site moved while shop was refused")
	}
	if got := repoPaths(t, w, "blog"); strings.Join(got, ",") != "site" {
		t.Fatalf("blog's project.yaml was rewritten while shop was refused: %v", got)
	}
	if err := w.WriteCrewMeta("shop", "k2", map[string]string{"task": "fix", "state": "finished", "repo": "web"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if _, err := migrate.Run(context.Background(), w, deps, &out); err != nil {
		t.Fatalf("Run with nothing in the way: %v\n%s", err, out.String())
	}
	if got := repoPaths(t, w, "blog"); strings.Join(got, ",") != "blog/site" {
		t.Fatalf("blog's repo paths = %v", got)
	}
}

// TestMigrateMateWithHerdrDownIsNotRunning: a Mate whose meta records a pane
// is not running when Herdr is not running at all; one Herdr cannot be
// asked about may be, and is refused.
func TestMigrateMateWithHerdrDownIsNotRunning(t *testing.T) {
	w := oldWorkspace(t, project{"shop", []string{"web"}})
	deps, rt, _ := liveDeps(t)
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude", "pane": "w1:p1", "agent": "mate-shop"}); err != nil {
		t.Fatal(err)
	}
	rt.SessionLookupErr = errors.New("herdr: connection reset")
	var out bytes.Buffer
	_, err := migrate.Run(context.Background(), w, deps, &out)
	if got := refused(t, err); len(got) != 1 || !strings.Contains(got[0], "the Mate of shop may be running") {
		t.Fatalf("with Herdr unreadable, refused with %v", got)
	}
	rt.SessionLookupErr = nil
	rt.SessionNotRunning = true
	if _, err := migrate.Run(context.Background(), w, deps, &out); err != nil {
		t.Fatalf("with Herdr down: %v", err)
	}
}

// TestMigrateFinishesAfterACrashBetweenRenameAndYAML is plan test 10: the
// repo was renamed and the machine went down before project.yaml was
// written. The next run takes the rename as done and finishes; nothing is
// renamed back.
func TestMigrateFinishesAfterACrashBetweenRenameAndYAML(t *testing.T) {
	w := oldWorkspace(t, project{"shop", []string{"web"}})
	root := w.Root()
	deps, _, _ := liveDeps(t)
	wt := closedCrew(t, w, "shop", "k1", "web", "web", "In "+root+"/web.\n")
	if err := os.MkdirAll(filepath.Join(root, "shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "web"), filepath.Join(root, "shop", "web")); err != nil {
		t.Fatal(err)
	}
	if attached(t, wt) {
		t.Fatal("fixture: the worktree is still attached after the rename")
	}

	var dry bytes.Buffer
	if _, err := migrate.DryRun(context.Background(), w, deps, &dry); err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if !strings.Contains(dry.String(), "would move "+root+"/web -> "+root+"/shop/web (finishes a move that stopped half way)") {
		t.Fatalf("dry run:\n%s", dry.String())
	}
	var out bytes.Buffer
	if _, err := migrate.Run(context.Background(), w, deps, &out); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "repaired 1 worktree(s)\nrewrote 1 brief(s)\nworkspace layout 2\n") {
		t.Fatalf("Run printed:\n%s", out.String())
	}
	if _, err := os.Lstat(filepath.Join(root, "web")); !os.IsNotExist(err) {
		t.Fatalf("web was renamed back: %v", err)
	}
	if !attached(t, wt) {
		t.Fatal("the worktree is not attached")
	}
	if got := repoPaths(t, w, "shop"); strings.Join(got, ",") != "shop/web" {
		t.Fatalf("project.yaml repo paths = %v", got)
	}
	if got := readFile(t, w.CrewBrief("shop", "k1")); got != "In "+root+"/shop/web.\n" {
		t.Fatalf("brief = %q", got)
	}
}

// TestMigrateFinishesAfterACrashAfterTheYAML: project.yaml already names
// the new path, so only the journal in migrate.log says the briefs and the
// finished line are still to come.
func TestMigrateFinishesAfterACrashAfterTheYAML(t *testing.T) {
	w := oldWorkspace(t, project{"shop", []string{"web"}})
	root := w.Root()
	deps, _, _ := liveDeps(t)
	closedCrew(t, w, "shop", "k1", "web", "web", "In "+root+"/web.\n")
	if err := os.MkdirAll(filepath.Join(root, "shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "web"), filepath.Join(root, "shop", "web")); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendMigrateLog(store.MigrateEntry{Project: "shop", Old: root + "/web", New: root + "/shop/web", Step: store.MigrateRenamed}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, w.ProjectFile("shop"), "repos:\n    - name: web\n      path: shop/web\n      default_branch: main\n")

	var out bytes.Buffer
	if _, err := migrate.Run(context.Background(), w, deps, &out); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if !strings.HasPrefix(out.String(), "moved "+root+"/web -> "+root+"/shop/web (finishes a move that stopped half way)\n") {
		t.Fatalf("Run printed:\n%s", out.String())
	}
	if got := readFile(t, w.CrewBrief("shop", "k1")); got != "In "+root+"/shop/web.\n" {
		t.Fatalf("brief = %q", got)
	}
	out.Reset()
	if _, err := migrate.Run(context.Background(), w, deps, &out); err != nil || strings.TrimSpace(out.String()) != "nothing to do" {
		t.Fatalf("second run: %v\n%s", err, out.String())
	}
}

// TestMigrateSameNameFinishesAfterAFailedSecondRename: the shop repo was
// moved aside and the project's directory made, and the rename into it
// failed. The next run finds the staging directory and finishes.
func TestMigrateSameNameFinishesAfterAFailedSecondRename(t *testing.T) {
	w := oldWorkspace(t, project{"shop", []string{"shop"}})
	root := w.Root()
	deps, _, _ := liveDeps(t)
	wt := closedCrew(t, w, "shop", "k1", "shop", "shop", "In "+root+"/shop.\n")

	renames := 0
	deps.Rename = func(from, to string) error {
		renames++
		if renames == 2 {
			return errors.New("disk on fire")
		}
		return os.Rename(from, to)
	}
	var out bytes.Buffer
	if _, err := migrate.Run(context.Background(), w, deps, &out); err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("Run with a failing rename = %v", err)
	}
	if gitx.HasGitDir(filepath.Join(root, "shop")) {
		t.Fatal("fixture: shop is still the repo")
	}

	deps.Rename = nil
	out.Reset()
	if _, err := migrate.Run(context.Background(), w, deps, &out); err != nil {
		t.Fatalf("second Run: %v\n%s", err, out.String())
	}
	if !strings.HasPrefix(out.String(), "moved "+root+"/shop -> "+root+"/shop/shop (finishes a move that stopped half way)\n") {
		t.Fatalf("second run printed:\n%s", out.String())
	}
	if !gitx.HasGitDir(filepath.Join(root, "shop", "shop")) || !attached(t, wt) {
		t.Fatal("shop/shop is not the repo with its worktree attached")
	}
	if got := readFile(t, w.CrewBrief("shop", "k1")); got != "In "+root+"/shop/shop.\n" {
		t.Fatalf("brief = %q", got)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".migrating-") {
			t.Fatalf("a staging directory is left: %s", e.Name())
		}
	}
}

// TestMigrateLockRefusesASecondMigrate: one migrate at a time.
func TestMigrateLockRefusesASecondMigrate(t *testing.T) {
	w := oldWorkspace(t, project{"shop", []string{"web"}})
	deps, _, _ := liveDeps(t)
	unlock, ok, err := w.LockMigrate()
	if err != nil || !ok {
		t.Fatalf("LockMigrate: %v %v", ok, err)
	}
	defer unlock()
	var out bytes.Buffer
	_, err = migrate.Run(context.Background(), w, deps, &out)
	if got := refused(t, err); len(got) != 1 || !strings.Contains(got[0], "another mate migrate is running") {
		t.Fatalf("Run while locked: %v", got)
	}
	_, err = migrate.DryRun(context.Background(), w, deps, &out)
	if got := refused(t, err); !strings.Contains(strings.Join(got, "\n"), "another mate migrate is running") {
		t.Fatalf("DryRun while locked: %v", got)
	}
	if _, err := os.Lstat(filepath.Join(w.Root(), "web")); err != nil {
		t.Fatal("web moved while another migrate held the lock")
	}
}

// assertRefusedUntouched runs the migrate, expects it refused with a reason
// containing want, and the workspace unchanged beyond its lock file.
func assertRefusedUntouched(t *testing.T, w *store.Workspace, deps migrate.Deps, want string) {
	t.Helper()
	before := tree(t, w.Root())
	var out bytes.Buffer
	_, err := migrate.Run(context.Background(), w, deps, &out)
	reasons := refused(t, err)
	if !strings.Contains(strings.Join(reasons, "\n"), want) {
		t.Fatalf("refused with\n%s\nwant a reason containing %q", strings.Join(reasons, "\n"), want)
	}
	if d := diffTrees(before, tree(t, w.Root())); len(d) > 1 || (len(d) == 1 && d[0] != "new: .mate/migrate.lock") {
		t.Fatalf("a refused run changed the workspace:\n%s", strings.Join(d, "\n"))
	}
}

// TestMigrateRefusesARepoNestedInTheSameNameRepo: on the old layout project
// shop could register both `shop` and `shop/api`. Moving shop to shop/shop
// would carry api along and leave project.yaml naming a path that is gone,
// so nothing moves.
func TestMigrateRefusesARepoNestedInTheSameNameRepo(t *testing.T) {
	w := oldWorkspace(t, project{"shop", []string{"shop", "shop/api"}})
	root := w.Root()
	deps, _, _ := liveDeps(t)
	assertRefusedUntouched(t, w, deps,
		"repo api of project shop at "+root+"/shop/api is inside repo shop of project shop at "+root+"/shop, which would move")
}

// TestMigrateRefusesARepoNestedInAnotherProjectsRepo: project plug's repo
// lives inside project site's repo web. Moving web first would strand
// plug's move half way, so nothing moves.
func TestMigrateRefusesARepoNestedInAnotherProjectsRepo(t *testing.T) {
	w := oldWorkspace(t,
		project{"site", []string{"web"}},
		project{"plug", []string{"web/plugin"}},
	)
	root := w.Root()
	deps, _, _ := liveDeps(t)
	assertRefusedUntouched(t, w, deps,
		"repo plugin of project plug at "+root+"/web/plugin is inside repo web of project site at "+root+"/web, which would move")
}

// TestMigrateWarnsWhenTheDirtyCheckFails: a registered directory git cannot
// read is not reported clean; the line says so, and it still moves.
func TestMigrateWarnsWhenTheDirtyCheckFails(t *testing.T) {
	w := oldWorkspace(t, project{"shop", []string{"web"}})
	root := w.Root()
	deps, _, _ := liveDeps(t)
	writeFile(t, filepath.Join(root, "docs", "notes.md"), "not a repo\n")
	writeFile(t, w.ProjectFile("shop"), "repos:\n    - name: web\n      path: web\n      default_branch: main\n    - name: docs\n      path: docs\n      default_branch: main\n")
	want := "warning: could not tell whether " + root + "/docs has uncommitted changes ("

	var out bytes.Buffer
	if _, err := migrate.DryRun(context.Background(), w, deps, &out); err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if !strings.Contains(out.String(), "would move "+root+"/docs -> "+root+"/shop/docs\n") || !strings.Contains(out.String(), want) {
		t.Fatalf("dry run printed:\n%s", out.String())
	}
	out.Reset()
	if _, err := migrate.Run(context.Background(), w, deps, &out); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), want) || readFile(t, filepath.Join(root, "shop", "docs", "notes.md")) != "not a repo\n" {
		t.Fatalf("Run printed:\n%s", out.String())
	}
}
