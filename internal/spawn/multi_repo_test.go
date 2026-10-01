package spawn_test

// A project that owns several repos (docs/mvp.md M9, task 40): each crew
// works in exactly one, named by `--repo` at spawn and by `repo=` in its
// meta afterwards, and every crew-scoped operation acts on that repo only.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// twoRepoWorkspace registers project "shop" with repos "api" and "web", two
// real git repositories each with one commit on main.
func twoRepoWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	var repos []store.RepoConfig
	for _, name := range []string{"api", "web"} {
		dir := filepath.Join(w.Root(), "shop-"+name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		initCommittedRepo(t, dir, name)
		repos = append(repos, store.RepoConfig{Name: name, Path: dir, DefaultBranch: "main"})
	}
	if err := w.AddProject("shop", store.ProjectConfig{Repos: repos}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

func initCommittedRepo(t *testing.T, dir, name string) {
	t.Helper()
	gitInit(t, dir)
	git(t, dir, "config", "user.email", "crew-test@example.com")
	git(t, dir, "config", "user.name", "crew test")
	git(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "README.md")
	git(t, dir, "commit", "-m", "init "+name)
}

func repoDir(w *store.Workspace, name string) string {
	return filepath.Join(w.Root(), "shop-"+name)
}

func branchIn(t *testing.T, repo, branch string) bool {
	t.Helper()
	ok, err := gitx.New().BranchExists(context.Background(), repo, branch)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func headIn(t *testing.T, repo, rev string) string {
	t.Helper()
	head, err := gitx.New().HeadCommit(context.Background(), repo, rev)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

func spawnIn(t *testing.T, w *store.Workspace, deps spawn.Deps, crew, repo string) spawn.CrewResult {
	t.Helper()
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: crew, Repo: repo, BriefText: brieftest.Ship("work in " + repo),
	})
	if err != nil {
		t.Fatalf("SpawnCrew %s --repo %s: %v", crew, repo, err)
	}
	return res
}

func TestSpawnCrewPutsEachCrewInTheRepoItNames(t *testing.T) {
	w := twoRepoWorkspace(t)
	deps := fakeDeps(t, runtime.NewFake())

	k3 := spawnIn(t, w, deps, "k3", "api")
	k4 := spawnIn(t, w, deps, "k4", "web")

	for _, c := range []struct {
		res         spawn.CrewResult
		repo, other string
	}{{k3, "api", "web"}, {k4, "web", "api"}} {
		meta, err := w.ReadCrewMeta("shop", c.res.Crew)
		if err != nil {
			t.Fatal(err)
		}
		if meta[store.MetaRepo] != c.repo {
			t.Errorf("crew %s meta repo = %q, want %q", c.res.Crew, meta[store.MetaRepo], c.repo)
		}
		if !branchIn(t, repoDir(w, c.repo), c.res.Branch) {
			t.Errorf("branch %s is not in repo %s", c.res.Branch, c.repo)
		}
		if branchIn(t, repoDir(w, c.other), c.res.Branch) {
			t.Errorf("branch %s leaked into repo %s", c.res.Branch, c.other)
		}
		// The worktree is a checkout of the named repo: its README is that
		// repo's, not the other's.
		readme, err := os.ReadFile(filepath.Join(c.res.Worktree, "README.md"))
		if err != nil || strings.TrimSpace(string(readme)) != c.repo {
			t.Errorf("crew %s worktree README = %q, %v; want the %s repo's", c.res.Crew, readme, err, c.repo)
		}
		brief, err := os.ReadFile(c.res.BriefPath)
		if err != nil || !strings.Contains(string(brief), repoDir(w, c.repo)) {
			t.Errorf("crew %s brief does not name repo %s: %v", c.res.Crew, repoDir(w, c.repo), err)
		}
	}
}

func TestStopCrewTearsDownInTheCrewsOwnRepo(t *testing.T) {
	w := twoRepoWorkspace(t)
	deps := fakeDeps(t, runtime.NewFake())
	spawnIn(t, w, deps, "k3", "api")
	k4 := spawnIn(t, w, deps, "k4", "web")

	stopped, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false)
	if err != nil {
		t.Fatalf("StopCrew k3: %v", err)
	}
	if !stopped.WorktreeRemoved || !stopped.BranchRemoved {
		t.Fatalf("stop = %+v, want the worktree and branch removed", stopped)
	}
	if branchIn(t, repoDir(w, "api"), "mate/k3") {
		t.Fatal("k3's branch survived its stop in repo api")
	}
	// The other crew, in the other repo, is untouched.
	if !branchIn(t, repoDir(w, "web"), "mate/k4") {
		t.Fatal("stopping k3 removed k4's branch in repo web")
	}
	if _, err := os.Stat(k4.Worktree); err != nil {
		t.Fatalf("stopping k3 removed k4's worktree: %v", err)
	}
	// A closed crew still says which repo it worked in.
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[store.MetaRepo] != "api" {
		t.Fatalf("stopped crew meta repo = %q, want api: %v", meta[store.MetaRepo], meta)
	}

	// The unlanded-work refusal reads the crew's own repo too: k4's commit
	// is ahead of web's main, which is all that matters.
	commitInWorktree(t, k4.Worktree, "web.txt", "web\n")
	if _, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k4", false); !errors.Is(err, spawn.ErrUnlandedWork) {
		t.Fatalf("StopCrew k4 with a commit ahead = %v, want ErrUnlandedWork", err)
	}
}

func TestMergeCrewLandsEachCrewInItsOwnRepo(t *testing.T) {
	w := twoRepoWorkspace(t)
	deps := fakeDeps(t, runtime.NewFake())
	k3 := spawnIn(t, w, deps, "k3", "api")
	k4 := spawnIn(t, w, deps, "k4", "web")
	commitInWorktree(t, k3.Worktree, "api.txt", "api\n")
	commitInWorktree(t, k4.Worktree, "web.txt", "web\n")
	apiTip, webTip := headIn(t, repoDir(w, "api"), "mate/k3"), headIn(t, repoDir(w, "web"), "mate/k4")
	webBefore := headIn(t, repoDir(w, "web"), "main")

	if _, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerUser); err != nil {
		t.Fatalf("MergeCrew k3: %v", err)
	}
	if got := headIn(t, repoDir(w, "api"), "main"); got != apiTip {
		t.Fatalf("api main = %s after merging k3, want %s", got, apiTip)
	}
	if got := headIn(t, repoDir(w, "web"), "main"); got != webBefore {
		t.Fatalf("merging k3 moved web's main to %s", got)
	}

	if _, err := spawn.MergeCrew(context.Background(), w, deps, "shop", "k4", spawn.CallerUser); err != nil {
		t.Fatalf("MergeCrew k4: %v", err)
	}
	if got := headIn(t, repoDir(w, "web"), "main"); got != webTip {
		t.Fatalf("web main = %s after merging k4, want %s", got, webTip)
	}
	if got := headIn(t, repoDir(w, "api"), "main"); got != apiTip {
		t.Fatalf("merging k4 moved api's main to %s", got)
	}
}

// assertSpawnLeftNothing is the other half of every spawn refusal: no
// worktree, no crew record, no Herdr call, no branch in any repo.
func assertSpawnLeftNothing(t *testing.T, w *store.Workspace, rt *runtime.Fake, repos ...string) {
	t.Helper()
	if _, err := os.Stat(w.WorktreeDir("shop", "k3")); !os.IsNotExist(err) {
		t.Fatalf("a refused spawn left a worktree: %v", err)
	}
	if _, err := os.Stat(w.CrewDir("shop", "k3")); !os.IsNotExist(err) {
		t.Fatalf("a refused spawn left crews/k3/: %v", err)
	}
	if _, err := os.Stat(w.CrewMeta("shop", "k3")); !os.IsNotExist(err) {
		t.Fatalf("a refused spawn left a meta: %v", err)
	}
	if len(rt.Calls) != 0 {
		t.Fatalf("calls %v: a refused spawn reached Herdr", rt.Calls)
	}
	for _, r := range repos {
		if branchIn(t, repoDir(w, r), "mate/k3") {
			t.Fatalf("a refused spawn left branch mate/k3 in repo %s", r)
		}
	}
}

func assertUsageRefusal(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("SpawnCrew succeeded, want a refusal")
	}
	if !errors.Is(err, spawn.ErrRepoRefused) {
		t.Errorf("error %v does not match spawn.ErrRepoRefused", err)
	}
	if code := observability.ExitCode(err); code != observability.ExitUsage {
		t.Errorf("exit code = %d, want %d (usage): %v", code, observability.ExitUsage, err)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestSpawnCrewWithSeveralReposWantsRepo(t *testing.T) {
	w := twoRepoWorkspace(t)
	rt := runtime.NewFake()
	_, err := spawn.SpawnCrew(context.Background(), w, fakeDeps(t, rt), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	assertUsageRefusal(t, err, "nothing was created", "api, web", "--repo <name>")
	assertSpawnLeftNothing(t, w, rt, "api", "web")
}

func TestSpawnCrewRefusesAnUnknownRepo(t *testing.T) {
	w := twoRepoWorkspace(t)
	rt := runtime.NewFake()
	_, err := spawn.SpawnCrew(context.Background(), w, fakeDeps(t, rt), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Repo: "mobile", BriefText: brieftest.Ship("work"),
	})
	assertUsageRefusal(t, err, "nothing was created", `"mobile"`, "api, web")
	assertSpawnLeftNothing(t, w, rt, "api", "web")
}

func TestSpawnCrewRefusesAProjectWithNoRepo(t *testing.T) {
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatalf("AddProject with no repo: %v", err)
	}
	for _, repo := range []string{"", "api"} {
		rt := runtime.NewFake()
		_, err := spawn.SpawnCrew(context.Background(), w, fakeDeps(t, rt), spawn.SpawnCrewRequest{
			Project: "shop", Crew: "k3", Repo: repo, BriefText: brieftest.Ship("work"),
		})
		// Not a usage mistake: only the captain can add a repo, so it is
		// a state refusal (exit 1), and not ErrRepoRefused.
		if err == nil || errors.Is(err, spawn.ErrRepoRefused) || observability.ExitCode(err) != observability.ExitStateConflict {
			t.Fatalf("repo %q: err = %v (exit %d), want a state refusal", repo, err, observability.ExitCode(err))
		}
		for _, want := range []string{"nothing was created", "has no repo yet", "mate project repo add shop <repo-path>"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("repo %q: %v does not contain %q", repo, err, want)
			}
		}
		assertSpawnLeftNothing(t, w, rt)
	}
}

// A single-repo project needs no --repo, and naming its one repo works too.
func TestSpawnCrewDefaultsToTheSoleRepo(t *testing.T) {
	w := crewWorkspace(t, "shop")
	deps := fakeDeps(t, runtime.NewFake())
	for crew, repo := range map[string]string{"k3": "", "k4": "shop"} {
		if _, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
			Project: "shop", Crew: crew, Repo: repo, BriefText: brieftest.Ship("work"),
		}); err != nil {
			t.Fatalf("SpawnCrew %s --repo %q: %v", crew, repo, err)
		}
		meta, err := w.ReadCrewMeta("shop", crew)
		if err != nil {
			t.Fatal(err)
		}
		if meta[store.MetaRepo] != "shop" {
			t.Fatalf("crew %s meta repo = %q, want the sole repo shop", crew, meta[store.MetaRepo])
		}
	}
}

// A spawn that fails after its crew directory exists records state=failed;
// that record still names the repo, so `project repo remove` and a reader
// know where the attempt was made.
func TestFailedSpawnRecordKeepsTheRepo(t *testing.T) {
	w := twoRepoWorkspace(t)
	rt := runtime.NewFake()
	rt.StartErr = errors.New("herdr refused the launch")
	if _, err := spawn.SpawnCrew(context.Background(), w, fakeDeps(t, rt), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Repo: "web", BriefText: brieftest.Ship("work"),
	}); err == nil {
		t.Fatal("SpawnCrew must fail when the agent cannot start")
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaState] != spawn.CrewStateFailed || meta[store.MetaRepo] != "web" {
		t.Fatalf("failed spawn meta = %v, want state=failed and repo=web", meta)
	}
	if branchIn(t, repoDir(w, "web"), "mate/k3") {
		t.Fatal("the failed spawn's branch survived compensation in repo web")
	}
}

// A meta written before M9 names no repo. With several repos there is no
// safe guess, so every crew operation refuses and says how to fix the meta.
func TestLegacyMetaInAProjectWithSeveralReposIsRefused(t *testing.T) {
	w := twoRepoWorkspace(t)
	deps := fakeDeps(t, runtime.NewFake())
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{
		spawn.MetaTask:     "old crew",
		spawn.MetaHarness:  "codex",
		spawn.MetaBranch:   "mate/k3",
		spawn.MetaWorktree: ".worktrees/shop-k3",
		spawn.MetaState:    spawn.CrewStateSpawned,
	}); err != nil {
		t.Fatal(err)
	}
	_, stopErr := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false)
	_, mergeErr := spawn.MergeCrew(context.Background(), w, deps, "shop", "k3", spawn.CallerUser)
	for name, err := range map[string]error{"stop": stopErr, "merge": mergeErr} {
		if err == nil {
			t.Fatalf("%s of a legacy crew in a two-repo project succeeded", name)
		}
		for _, want := range []string{"names no repo", "api, web", "repo=<name>", w.CrewMeta("shop", "k3")} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s error %q does not contain %q", name, err, want)
			}
		}
	}
}

// The worktree backend is a seam on Deps: a spawn acquires through it and a
// stop releases through it, so a later backend replaces git worktrees
// without touching the saga.
func TestSpawnAndStopGoThroughTheWorktreesSeam(t *testing.T) {
	w := twoRepoWorkspace(t)
	deps := fakeDeps(t, runtime.NewFake())
	rec := &recordingWorktrees{inner: spawn.GitWorktrees{Git: gitx.New()}}
	deps.Worktrees = rec

	res := spawnIn(t, w, deps, "k3", "api")
	if len(rec.acquired) != 1 {
		t.Fatalf("acquired = %v, want one lease", rec.acquired)
	}
	got := rec.acquired[0]
	if !gitx.SamePath(got.Repo, repoDir(w, "api")) || got.Path != res.Worktree || got.Branch != "mate/k3" || got.Base != "main" {
		t.Fatalf("lease = %+v, want repo api, worktree %s, branch mate/k3, base main", got, res.Worktree)
	}
	if _, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	if len(rec.released) == 0 {
		t.Fatal("StopCrew did not release through the seam")
	}
	for _, l := range rec.released {
		if !gitx.SamePath(l.Repo, repoDir(w, "api")) || l.Branch != "mate/k3" {
			t.Fatalf("released %+v, want the api lease", l)
		}
	}
}

type recordingWorktrees struct {
	inner    spawn.Worktrees
	acquired []spawn.WorktreeLease
	released []spawn.WorktreeLease
}

func (r *recordingWorktrees) Acquire(ctx context.Context, lease spawn.WorktreeLease) error {
	r.acquired = append(r.acquired, lease)
	return r.inner.Acquire(ctx, lease)
}

func (r *recordingWorktrees) Release(ctx context.Context, lease spawn.WorktreeLease, parts spawn.ReleaseParts) error {
	r.released = append(r.released, lease)
	return r.inner.Release(ctx, lease, parts)
}
