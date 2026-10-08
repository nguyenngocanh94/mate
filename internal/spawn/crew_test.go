package spawn_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/github"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// crewWorkspace is newWorkspace plus the one thing a crew needs that a Mate
// does not: a commit to branch from.
func crewWorkspace(t *testing.T, project string) *store.Workspace {
	t.Helper()
	w := newWorkspace(t, project)
	repo := w.RepoDir(project)
	git(t, repo, "config", "user.email", "crew-test@example.com")
	git(t, repo, "config", "user.name", "crew test")
	git(t, repo, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "README.md")
	git(t, repo, "commit", "-m", "init")
	return w
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// briefFile writes a task text inside the workspace, which is where a brief
// has to live: `crew spawn` refuses one outside it.
func briefFile(t *testing.T, w *store.Workspace, text string) string {
	t.Helper()
	path := filepath.Join(w.Root(), "brief.txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSpawnCrewCreatesWorktreeBriefAndMeta(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)

	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project:   "shop",
		Crew:      "k3",
		BriefFile: briefFile(t, w, brieftest.Ship("Add a healthcheck endpoint.\nKeep it small.\n")),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if res.Harness != codex.KindCodex {
		t.Fatalf("harness = %q, want codex (the workspace default for a crew)", res.Harness)
	}
	if res.Agent != "crew-k3" {
		t.Fatalf("agent = %q, want crew-k3", res.Agent)
	}
	if res.Branch != "mate/k3" {
		t.Fatalf("branch = %q, want mate/k3", res.Branch)
	}
	if res.Worktree != w.WorktreeDir("shop", "k3") {
		t.Fatalf("worktree = %q, want %q", res.Worktree, w.WorktreeDir("shop", "k3"))
	}
	if !res.BriefDelivered {
		t.Fatalf("the brief was not confirmed delivered: %s", res.DeliveryWarning)
	}

	// The worktree is a real, isolated checkout of the crew's own branch.
	if got := strings.TrimSpace(git(t, res.Worktree, "rev-parse", "--abbrev-ref", "HEAD")); got != "mate/k3" {
		t.Fatalf("worktree branch = %q, want mate/k3", got)
	}
	top := strings.TrimSpace(git(t, res.Worktree, "rev-parse", "--show-toplevel"))
	if !gitx.SamePath(top, res.Worktree) || gitx.SamePath(top, w.RepoDir("shop")) {
		t.Fatalf("worktree top level %q is not an isolated worktree of %q", top, w.RepoDir("shop"))
	}

	// The brief is the rendered template with the caller's task in it.
	brief, err := os.ReadFile(w.CrewBrief("shop", "k3"))
	if err != nil {
		t.Fatalf("brief.md: %v", err)
	}
	for _, want := range []string{
		"Add a healthcheck endpoint.\nKeep it small.",
		res.Worktree,
		w.RepoDir("shop"),
		"mate/k3",
		"$MATE_STATUS",
	} {
		if !strings.Contains(string(brief), want) {
			t.Errorf("brief.md does not carry %q:\n%s", want, brief)
		}
	}
	if strings.Contains(string(brief), "{TASK}") {
		t.Error("brief.md still carries the {TASK} placeholder")
	}
	// Codex reads AGENTS.override.md at its cwd and nothing else.
	override, err := os.ReadFile(codex.CodexInstructionPath(res.Worktree))
	if err != nil {
		t.Fatalf("AGENTS.override.md: %v", err)
	}
	if string(override) != string(brief) {
		t.Error("the Codex discovery file must carry the same brief")
	}
	if _, err := os.Stat(w.CrewStatus("shop", "k3")); err != nil {
		t.Fatalf("crews/k3.status: %v", err)
	}

	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		spawn.MetaTask:       "Add a healthcheck endpoint.",
		spawn.MetaHarness:    "codex",
		spawn.MetaAgent:      "crew-k3",
		spawn.MetaPane:       res.Pane,
		spawn.MetaTab:        res.Tab,
		spawn.MetaWorkspace:  res.Workspace,
		spawn.MetaSession:    w.Session(),
		spawn.MetaWorktree:   ".worktrees/shop-k3",
		spawn.MetaBranch:     "mate/k3",
		spawn.MetaKind:       "ship",
		spawn.MetaDelivery:   "local",
		store.MetaRepo:       "shop",
		spawn.MetaSessionID:  "",
		spawn.MetaTranscript: "",
		spawn.MetaStartedAt:  "2026-09-17T10:00:00Z",
		spawn.MetaLaunchedAt: "2026-09-17T10:00:00Z",
		// The crew exists and has written nothing yet (mvp.md section 4b).
		spawn.MetaState: spawn.CrewStateSpawned,
	}
	for k, v := range want {
		got, ok := meta[k]
		if !ok {
			t.Errorf("meta has no %s", k)
			continue
		}
		if got != v {
			t.Errorf("meta[%s] = %q, want %q", k, got, v)
		}
	}
	if len(meta) != len(want) {
		t.Errorf("meta has %d keys (%v), want exactly %d", len(meta), meta, len(want))
	}

	// The pane: labelled crew-<id>, cwd the worktree, and carrying the
	// status file the brief tells the crew to echo into.
	tab, ok := rt.Tabs[res.Pane]
	if !ok {
		t.Fatalf("pane %s is not a live tab", res.Pane)
	}
	if tab.Label != "crew-k3" {
		t.Fatalf("tab label = %q, want crew-k3", tab.Label)
	}
	if tab.Cwd != res.Worktree {
		t.Fatalf("tab cwd = %q, want the worktree %q", tab.Cwd, res.Worktree)
	}
	env := map[string]string{}
	for _, v := range tab.Env {
		env[v.Key] = v.Value
	}
	if env[config.EnvStatusFile] != w.CrewStatus("shop", "k3") {
		t.Fatalf("%s = %q, want %q", config.EnvStatusFile, env[config.EnvStatusFile], w.CrewStatus("shop", "k3"))
	}
	if env[config.EnvCrewID] != "k3" || env[config.EnvAgentRole] != "crew" {
		t.Fatalf("pane env does not identify the crew: %v", env)
	}
}

// The Codex discovery file is mate's, not the crew's work: a crew that
// runs `git add -A` must not put it on the branch the Mate reviews.
func TestSpawnCrewKeepsTheCodexDiscoveryFileOutOfTheBranch(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	res, err := spawn.SpawnCrew(context.Background(), w, fakeDeps(t, rt), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	git(t, res.Worktree, "add", "-A")
	staged := git(t, res.Worktree, "diff", "--cached", "--name-only")
	if strings.Contains(staged, "AGENTS.override.md") {
		t.Fatalf("git add -A staged the generated context file:\n%s", staged)
	}
	if strings.TrimSpace(staged) != "" {
		t.Fatalf("a fresh crew worktree has nothing to stage, got:\n%s", staged)
	}
}

func TestSpawnCrewDeliversThePointerNotThePastedBrief(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := &promptRecorder{Fake: runtime.NewFake()}
	deps := fakeDeps(t, rt.Fake)
	deps.Runtime = rt

	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3",
		BriefText: brieftest.Ship("Line one.\n\nLine two.\n"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if len(rt.prompts) != 1 {
		t.Fatalf("prompts = %v, want exactly one", rt.prompts)
	}
	got := rt.prompts[0]
	if strings.Contains(got, "\n") {
		t.Fatalf("the first prompt is multi-line, which a TUI composer submits early:\n%q", got)
	}
	if got != spawn.BriefPrompt(res.BriefPath) {
		t.Fatalf("prompt = %q, want %q", got, spawn.BriefPrompt(res.BriefPath))
	}
}

func TestSpawnCrewRefusesADuplicateIDWhileTheAgentIsLive(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	req := spawn.SpawnCrewRequest{Project: "shop", Crew: "k3", BriefText: brieftest.Ship("first")}
	if _, err := spawn.SpawnCrew(context.Background(), w, deps, req); err != nil {
		t.Fatalf("first SpawnCrew: %v", err)
	}

	req.BriefText = "second"
	_, err := spawn.SpawnCrew(context.Background(), w, deps, req)
	if err == nil {
		t.Fatal("a second spawn of a live crew id must be refused")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("error = %v, want it to say the crew is already running", err)
	}
	// Nothing of the second attempt reached git or Herdr.
	if got := strings.TrimSpace(git(t, w.RepoDir("shop"), "worktree", "list")); strings.Count(got, "\n") != 1 {
		t.Fatalf("worktree list changed:\n%s", got)
	}
	brief, err := os.ReadFile(w.CrewBrief("shop", "k3"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brief), "first") {
		t.Fatal("the refused spawn overwrote the live crew's brief")
	}
}

func TestSpawnCrewRefusesAnExistingBranchBeforeCreatingAnything(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	git(t, w.RepoDir("shop"), "branch", "mate/k3")

	_, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err == nil {
		t.Fatal("an existing branch must be refused")
	}
	if !strings.Contains(err.Error(), "mate/k3 already exists") {
		t.Fatalf("error = %v, want it to name the branch", err)
	}
	if len(rt.Calls) != 0 {
		t.Fatalf("calls %v, want nothing to have reached Herdr", rt.Calls)
	}
	if _, statErr := os.Stat(w.WorktreeDir("shop", "k3")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused spawn created a worktree: %v", statErr)
	}
	if _, statErr := os.Stat(w.CrewMeta("shop", "k3")); !os.IsNotExist(statErr) {
		t.Fatal("a refused spawn wrote a meta")
	}
}

func TestSpawnCrewRefusesAnExistingWorktreePath(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	if err := os.MkdirAll(w.WorktreeDir("shop", "k3"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err == nil {
		t.Fatal("an existing worktree path must be refused")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v", err)
	}
}

func TestSpawnCrewRefusesABriefOutsideTheWorkspace(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	outside := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(outside, []byte("do a thing"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := spawn.SpawnCrew(context.Background(), w, fakeDeps(t, rt), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefFile: outside,
	})
	if err == nil {
		t.Fatal("a brief outside the workspace must be refused")
	}
	if !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("error = %v", err)
	}
}

// TestSpawnCrewCompensatesAfterTheTabExists is the saga proof: a failure
// once the tab is live must leave no worktree, no branch and no tab - and
// must leave the brief, which is the evidence of what was asked for, plus a
// meta recording `state=failed` and why (mvp.md section 4b: the directory
// survives, so no orphan is left reading as `spawned`).
func TestSpawnCrewCompensatesAfterTheTabExists(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	rt.StartErr = errors.New("herdr refused the launch")

	_, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("Add a healthcheck endpoint."),
	})
	if err == nil {
		t.Fatal("SpawnCrew must fail when the agent cannot start")
	}

	if _, statErr := os.Stat(w.WorktreeDir("shop", "k3")); !os.IsNotExist(statErr) {
		t.Fatalf("the worktree survived compensation: %v", statErr)
	}
	exists, branchErr := gitx.New().BranchExists(context.Background(), w.RepoDir("shop"), "mate/k3")
	if branchErr != nil {
		t.Fatal(branchErr)
	}
	if exists {
		t.Fatal("branch mate/k3 survived compensation")
	}
	if listed := git(t, w.RepoDir("shop"), "worktree", "list"); strings.Contains(listed, "shop-k3") {
		t.Fatalf("git still lists the crew worktree:\n%s", listed)
	}
	meta, metaErr := w.ReadCrewMeta("shop", "k3")
	if metaErr != nil {
		t.Fatalf("a failed spawn must still record what happened: %v", metaErr)
	}
	if meta[spawn.MetaState] != spawn.CrewStateFailed {
		t.Fatalf("meta state = %q, want %q", meta[spawn.MetaState], spawn.CrewStateFailed)
	}
	if !strings.Contains(meta[spawn.MetaFailedReason], "herdr refused the launch") {
		t.Fatalf("failed_reason = %q, want the cause of the failure", meta[spawn.MetaFailedReason])
	}
	if meta[spawn.MetaAgent] != "" || meta[spawn.MetaPane] != "" {
		t.Fatalf("meta = %+v, want no agent or pane: compensation removed them", meta)
	}
	if meta[spawn.MetaTask] != "Add a healthcheck endpoint." {
		t.Fatalf("meta task = %q, want what the crew was asked for", meta[spawn.MetaTask])
	}
	// The brief stays: it is what the crew was asked to do.
	brief, readErr := os.ReadFile(w.CrewBrief("shop", "k3"))
	if readErr != nil {
		t.Fatalf("the brief must survive compensation: %v", readErr)
	}
	if !strings.Contains(string(brief), "Add a healthcheck endpoint.") {
		t.Fatal("the surviving brief does not carry the task")
	}
	// The crew's tab is gone; the workspace's own root tab is not this
	// spawn's to remove.
	for pane, tab := range rt.Tabs {
		if tab.Label == "crew-k3" {
			t.Fatalf("crew tab %s survived compensation", pane)
		}
	}
	if !slices.Contains(rt.Calls, "RemoveTab") {
		t.Fatalf("calls %v: compensation did not close the tab", rt.Calls)
	}
}

// TestSpawnCrewRefusesATangledWorktree covers the guard v1 paid for: if
// `worktree add` reported success but the directory is really the project's
// primary checkout, a crew would commit on the user's own branch. git is
// faked here because a real git cannot be made to produce that answer.
func TestSpawnCrewRefusesATangledWorktree(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	repo := w.RepoDir("shop")
	deps.Git = gitx.Git{Runner: &tangledGit{repo: repo, worktree: w.WorktreeDir("shop", "k3")}}

	_, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err == nil {
		t.Fatal("a worktree that resolves to the primary checkout must be refused")
	}
	if !strings.Contains(err.Error(), "primary checkout") {
		t.Fatalf("error = %v, want the tangle guard", err)
	}
	if _, statErr := os.Stat(w.CrewMeta("shop", "k3")); !os.IsNotExist(statErr) {
		t.Fatal("a tangled spawn wrote a meta")
	}
	if len(rt.Calls) != 0 {
		t.Fatalf("calls %v: the guard must run before anything reaches Herdr", rt.Calls)
	}
}

// tangledGit answers every precondition with "fine" and then reports the
// new worktree's top level as the primary checkout.
type tangledGit struct {
	repo     string
	worktree string
	calls    []string
}

func (g *tangledGit) Run(_ context.Context, cmd gitx.Command) (gitx.Result, error) {
	key := strings.Join(cmd.Args, " ")
	g.calls = append(g.calls, cmd.Dir+": "+key)
	switch {
	case key == "rev-parse --show-toplevel":
		return gitx.Result{Stdout: g.repo + "\n"}, nil
	case strings.HasPrefix(key, "show-ref"):
		return gitx.Result{ExitCode: 1}, nil
	default:
		return gitx.Result{}, nil
	}
}

func TestSpawnCrewWarnsWhenThePaneStaysIdle(t *testing.T) {
	w := crewWorkspace(t, "shop")
	fake := runtime.NewFake()
	rt := &silentPrompt{Fake: fake}
	deps := readingVisible(t, fake)
	deps.Runtime = rt
	deps.BriefDeliveryTimeout = 20 * time.Millisecond

	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("an undelivered brief must not fail the spawn: %v", err)
	}
	if res.BriefDelivered {
		t.Fatal("the pane never left idle; delivery must not be claimed")
	}
	if !strings.Contains(res.DeliveryWarning, "brief may not have been delivered") {
		t.Fatalf("warning = %q", res.DeliveryWarning)
	}
	if res.PaneTail == "" {
		t.Fatal("the warning must carry the pane tail")
	}
	// The settle and the tail both read through the profile's source.
	assertReadVisible(t, fake)
	// The crew itself is real: the meta is written and the worktree exists.
	if _, statErr := os.Stat(w.CrewMeta("shop", "k3")); statErr != nil {
		t.Fatalf("an undelivered brief must still leave a recorded crew: %v", statErr)
	}
}

func TestListCrewsReportsTheRecordedCrews(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("Add a healthcheck endpoint."),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if err := w.AppendStatus("shop", "k3", "working: reading the repo"); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "done: ready in branch mate/k3"); err != nil {
		t.Fatal(err)
	}

	crews, err := spawn.ListCrews(w, "shop")
	if err != nil {
		t.Fatalf("ListCrews: %v", err)
	}
	if len(crews) != 1 {
		t.Fatalf("crews = %v, want one", crews)
	}
	got := crews[0]
	if got.Crew != "k3" || got.Harness != "codex" || got.Branch != "mate/k3" || got.Pane != res.Pane {
		t.Fatalf("row = %+v", got)
	}
	if got.State != "wait-mate" {
		t.Fatalf("state = %q, want wait-mate: the legacy done: verb reads as wait-mate", got.State)
	}
	if got.Note != "ready in branch mate/k3" {
		t.Fatalf("note = %q, want the last status line's text", got.Note)
	}
	if got.Closed {
		t.Fatal("a crew that reported wait-mate is still open; only crew stop closes one")
	}
}

// TestListCrewsStateReflectsTheMeta is the STATE column's contract
// (mvp.md section 4b): before any stop it is the crew's own last verb;
// a refused stop changes nothing at all, so it stays there; and once the
// stop actually runs the meta's terminal state is what the column shows.
func TestListCrewsStateReflectsTheMeta(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	if _, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	}); err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if err := w.AppendStatus("shop", "k3", "done: ready in branch mate/k3"); err != nil {
		t.Fatal(err)
	}

	row := func() spawn.CrewSummary {
		crews, err := spawn.ListCrews(w, "shop")
		if err != nil {
			t.Fatalf("ListCrews: %v", err)
		}
		if len(crews) != 1 {
			t.Fatalf("crews = %v, want one", crews)
		}
		return crews[0]
	}

	if got := row().State; got != "wait-mate" {
		t.Fatalf("state before any stop = %q, want the crew's own last verb", got)
	}

	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k4", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew k4: %v", err)
	}
	commitInWorktree(t, res.Worktree, "unlanded.txt", "wip\n")
	if _, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k4", false); !errors.Is(err, spawn.ErrUnlandedWork) {
		t.Fatalf("StopCrew k4: err = %v, want ErrUnlandedWork", err)
	}
	crews, err := spawn.ListCrews(w, "shop")
	if err != nil {
		t.Fatalf("ListCrews: %v", err)
	}
	var k4 spawn.CrewSummary
	for _, c := range crews {
		if c.Crew == "k4" {
			k4 = c
		}
	}
	// The refusal changed nothing, so the state is still whatever the crew
	// itself last said - here nothing at all.
	if k4.State != "spawned" || k4.Closed {
		t.Fatalf("k4 after a refused stop = %+v, want state spawned and open", k4)
	}

	if _, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k4", true); err != nil {
		t.Fatalf("StopCrew k4 --discard: %v", err)
	}
	crews, err = spawn.ListCrews(w, "shop")
	if err != nil {
		t.Fatalf("ListCrews: %v", err)
	}
	for _, c := range crews {
		if c.Crew != "k4" {
			continue
		}
		if c.State != "failed" || !c.Closed {
			t.Fatalf("k4 after --discard = %+v, want state failed and closed: the work was thrown away", c)
		}
	}
}

// TestStopCrewTearsDownCleanlyWhenLanded is task 16's default path: a crew
// that never committed anything (its branch is trivially an ancestor of
// default, its worktree is clean) is fully torn down without --discard, and
// the brief survives.
func TestStopCrewTearsDownCleanlyWhenLanded(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}

	stopped, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false)
	if err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	if stopped.Agent != res.Agent || !stopped.TabClosed {
		t.Fatalf("stop = %+v", stopped)
	}
	if stopped.Teardown != spawn.TeardownClean || !stopped.WorktreeRemoved || !stopped.BranchRemoved {
		t.Fatalf("stop = %+v, want a clean teardown", stopped)
	}
	if stopped.Unlanded {
		t.Fatal("a crew with no commits and no dirty files must not be unlanded")
	}
	if _, ok := rt.Tabs[res.Pane]; ok {
		t.Fatal("the crew pane survived the stop")
	}
	if _, statErr := os.Stat(res.Worktree); !os.IsNotExist(statErr) {
		t.Fatalf("the worktree survived a clean teardown: %v", statErr)
	}
	exists, err := gitx.New().BranchExists(context.Background(), w.RepoDir("shop"), "mate/k3")
	if err != nil || exists {
		t.Fatalf("the branch survived a clean teardown: %v, %v", exists, err)
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaAgent] != "" || meta[spawn.MetaPane] != "" {
		t.Fatalf("a stopped crew must not keep naming a pane: %v", meta)
	}
	if meta[spawn.MetaBranch] != "mate/k3" || meta[spawn.MetaWorktree] != ".worktrees/shop-k3" {
		t.Fatalf("a torn-down crew must still record which branch and worktree it had: %v", meta)
	}
	if meta[spawn.MetaStoppedAt] == "" {
		t.Fatal("a stopped crew must record stopped_at")
	}
	if meta[spawn.MetaTeardown] != spawn.TeardownClean {
		t.Fatalf("meta teardown = %q, want %q", meta[spawn.MetaTeardown], spawn.TeardownClean)
	}
	// crews/<id>/ is never touched by a stop.
	if _, statErr := os.Stat(w.CrewBrief("shop", "k3")); statErr != nil {
		t.Fatalf("brief.md did not survive teardown: %v", statErr)
	}
}

// commitInWorktree makes one real commit on the crew's branch, so the
// branch is strictly ahead of the project's default branch.
func commitInWorktree(t *testing.T, worktree, file, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(worktree, file), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, worktree, "add", file)
	git(t, worktree, "commit", "-m", "crew commit")
}

func TestStopCrewRefusesWhenTheBranchIsAhead(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	commitInWorktree(t, res.Worktree, "new.txt", "unlanded\n")

	_, err = spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false)
	if !errors.Is(err, spawn.ErrUnlandedWork) {
		t.Fatalf("err = %v, want ErrUnlandedWork", err)
	}
	if _, statErr := os.Stat(res.Worktree); statErr != nil {
		t.Fatalf("a refused stop must keep the worktree: %v", statErr)
	}
	exists, existsErr := gitx.New().BranchExists(context.Background(), w.RepoDir("shop"), "mate/k3")
	if existsErr != nil || !exists {
		t.Fatalf("a refused stop must keep the branch: %v, %v", exists, existsErr)
	}
	// The refusal happens before anything is touched (mvp.md section 4b):
	// the agent is still alive, its tab still open, and the meta unchanged.
	// The old shape - kill the agent, close the tab, then refuse the
	// cleanup - left a crew that was dead but not closed, which is a third
	// outcome nobody could name or act on.
	if _, ok := rt.Tabs[res.Pane]; !ok {
		t.Fatal("a refused stop must leave the crew's tab open; nothing was decided")
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaTeardown] != "" || meta[spawn.MetaStoppedAt] != "" || meta[spawn.MetaState] != spawn.CrewStateSpawned {
		t.Fatalf("meta after a refused stop = %+v, want it untouched at state=spawned", meta)
	}
	if meta[spawn.MetaAgent] != res.Agent {
		t.Fatalf("meta agent = %q, want the still-running %q", meta[spawn.MetaAgent], res.Agent)
	}

	// A rerun with --discard finishes the job.
	discarded, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", true)
	if err != nil {
		t.Fatalf("StopCrew with --discard: %v", err)
	}
	if discarded.Teardown != spawn.TeardownDiscarded || discarded.Ahead != 1 {
		t.Fatalf("discarded stop = %+v", discarded)
	}
	if discarded.State != spawn.CrewStateFailed {
		t.Fatalf("discarded stop state = %q, want failed: the work was thrown away", discarded.State)
	}
	if meta, err := w.ReadCrewMeta("shop", "k3"); err != nil {
		t.Fatal(err)
	} else if meta[spawn.MetaState] != spawn.CrewStateFailed || meta[spawn.MetaStoppedAt] == "" {
		t.Fatalf("meta after --discard = %+v, want state=failed and stopped_at", meta)
	}
	if _, statErr := os.Stat(res.Worktree); !os.IsNotExist(statErr) {
		t.Fatal("--discard must remove the worktree")
	}
	exists, existsErr = gitx.New().BranchExists(context.Background(), w.RepoDir("shop"), "mate/k3")
	if existsErr != nil || exists {
		t.Fatal("--discard must remove the branch")
	}
	if _, statErr := os.Stat(w.CrewBrief("shop", "k3")); statErr != nil {
		t.Fatalf("brief.md did not survive a discarded teardown: %v", statErr)
	}
}

func TestStopCrewRefusesWhenOnlyTheWorktreeIsDirty(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	// Uncommitted, so the branch itself is still an ancestor of default.
	if err := os.WriteFile(filepath.Join(res.Worktree, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stopped, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false)
	if !errors.Is(err, spawn.ErrUnlandedWork) {
		t.Fatalf("err = %v, want ErrUnlandedWork", err)
	}
	if stopped.DirtyFiles != 1 {
		t.Fatalf("stop = %+v, want DirtyFiles=1", stopped)
	}
	if _, statErr := os.Stat(res.Worktree); statErr != nil {
		t.Fatalf("a refused stop must keep the dirty worktree: %v", statErr)
	}

	discarded, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", true)
	if err != nil {
		t.Fatalf("StopCrew with --discard: %v", err)
	}
	if discarded.Teardown != spawn.TeardownDiscarded {
		t.Fatalf("teardown = %q, want discarded", discarded.Teardown)
	}
	if _, statErr := os.Stat(res.Worktree); !os.IsNotExist(statErr) {
		t.Fatal("--discard must remove the dirty worktree")
	}
}

// TestStopCrewOnAClosedCrewChangesNothing: `finished` and `failed` are final
// (mvp.md section 4b). A second stop - the Mate's `crew stop` after its own
// `mate merge`, or a cleanup sweeping every crew with --discard - must not
// rewrite a merged crew as `failed`. Found 2026-09-24 (task 34): every live
// acceptance run's merged crew ended its record `state=failed`.
func TestStopCrewOnAClosedCrewChangesNothing(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	if _, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	}); err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if _, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false); err != nil {
		t.Fatalf("first StopCrew: %v", err)
	}
	closed, err := os.ReadFile(w.CrewMeta("shop", "k3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, discard := range []bool{true, false} {
		again, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", discard)
		if err != nil {
			t.Fatalf("StopCrew(discard=%v) on a finished crew: %v", discard, err)
		}
		if !again.AlreadyClosed || again.State != spawn.CrewStateFinished || again.Teardown != spawn.TeardownClean {
			t.Fatalf("StopCrew(discard=%v) = %+v, want the recorded finished/clean outcome, already closed", discard, again)
		}
		if again.WorktreeRemoved || again.BranchRemoved {
			t.Fatalf("StopCrew(discard=%v) = %+v; a closed crew has nothing left to remove", discard, again)
		}
		after, err := os.ReadFile(w.CrewMeta("shop", "k3"))
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(closed) {
			t.Fatalf("StopCrew(discard=%v) rewrote a closed crew's meta:\n%s\nwant\n%s", discard, after, closed)
		}
	}
}

// TestStopCrewTearsDownAfterTheAgentIsAlreadyGone covers a crew whose Herdr
// record did not survive a crash: the meta names an agent Herdr has never
// heard of, and the tab is gone too. Neither is an error; the teardown
// still runs.
func TestStopCrewTearsDownAfterTheAgentIsAlreadyGone(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	// tab_not_found (ADR 0028): Herdr no longer has the pane either.
	rt.ClosePane(res.Pane)

	stopped, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false)
	if err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	if !stopped.AlreadyGone || !stopped.TabClosed {
		t.Fatalf("stop = %+v, want AlreadyGone and TabClosed", stopped)
	}
	if stopped.Teardown != spawn.TeardownClean {
		t.Fatalf("teardown = %q, want clean", stopped.Teardown)
	}
	if _, statErr := os.Stat(res.Worktree); !os.IsNotExist(statErr) {
		t.Fatal("teardown must still run when the agent was already gone")
	}
}

func TestStopCrewWithoutARecordedCrewIsAnError(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	_, err := spawn.StopCrew(context.Background(), w, fakeDeps(t, rt), "shop", "nope", false)
	if err == nil {
		t.Fatal("stopping an unrecorded crew must be an error")
	}
}

// promptRecorder records what reached the pane as a prompt.
type promptRecorder struct {
	*runtime.Fake
	prompts []string
}

func (p *promptRecorder) PromptAgent(ctx context.Context, handle runtime.AgentHandle, text string) error {
	p.prompts = append(p.prompts, text)
	return p.Fake.PromptAgent(ctx, handle, text)
}

// silentPrompt is the pane that accepted the line and did nothing with it:
// Herdr reports success, the agent stays idle.
type silentPrompt struct {
	*runtime.Fake
}

func (s *silentPrompt) PromptAgent(_ context.Context, _ runtime.AgentHandle, _ string) error {
	return nil
}

// A ship the Mate spawns with `--deliver pr` (docs/mvp.md M19) is told to
// push its branch, open a pull request and watch it, and records the choice.
func TestSpawnCrewDeliverPRRendersThePullRequestBrief(t *testing.T) {
	w := crewWorkspace(t, "shop")
	git(t, w.RepoDir("shop"), "remote", "add", "origin", "git@github.com:acme/shop.git")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	deps.GitHub = github.Client{Runner: &ghScript{}}
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project:   "shop",
		Crew:      "k3",
		BriefFile: briefFile(t, w, brieftest.Ship("Add a healthcheck endpoint.\n")),
		Delivery:  crewstate.DeliveryPR,
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if res.Delivery != crewstate.DeliveryPR {
		t.Fatalf("result delivery = %q, want pr", res.Delivery)
	}
	if meta, _ := w.ReadCrewMeta("shop", "k3"); meta[spawn.MetaDelivery] != crewstate.DeliveryPR {
		t.Fatalf("meta delivery = %q, want pr", meta[spawn.MetaDelivery])
	}
	brief, err := os.ReadFile(w.CrewBrief("shop", "k3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gh pr create --base main --head mate/k3", "pr watch shop k3 <the same URL>"} {
		if !strings.Contains(string(brief), want) {
			t.Errorf("brief.md does not carry %q:\n%s", want, brief)
		}
	}
	if strings.Contains(string(brief), "Never push to any remote") {
		t.Errorf("a pull request brief still forbids pushing:\n%s", brief)
	}
}

// The default delivery is local, recorded as such, and its brief forbids
// pushing.
func TestSpawnCrewDeliversLocallyByDefault(t *testing.T) {
	w := crewWorkspace(t, "shop")
	deps := fakeDeps(t, runtime.NewFake())
	if _, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
	}); err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if meta, _ := w.ReadCrewMeta("shop", "k3"); meta[spawn.MetaDelivery] != crewstate.DeliveryLocal {
		t.Fatalf("meta delivery = %q, want local", meta[spawn.MetaDelivery])
	}
	brief, _ := os.ReadFile(w.CrewBrief("shop", "k3"))
	if !strings.Contains(string(brief), "Never push to any remote") {
		t.Errorf("a local brief does not forbid pushing:\n%s", brief)
	}
}

// `--deliver pr` is refused before anything exists when the repo cannot
// carry a pull request, a scout asks for one, or the value is unknown.
func TestSpawnCrewDeliverPRRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		origin   string
		scout    bool
		delivery string
		want     string
	}{
		"no origin":         {delivery: crewstate.DeliveryPR, want: "has no origin remote"},
		"origin not GitHub": {origin: "git@gitlab.com:acme/shop.git", delivery: crewstate.DeliveryPR, want: "does not point to GitHub"},
		"a scout":           {origin: "git@github.com:acme/shop.git", scout: true, delivery: crewstate.DeliveryPR, want: "a scout delivers a report"},
		"unknown value":     {delivery: "email", want: "--deliver wants local or pr"},
	} {
		t.Run(name, func(t *testing.T) {
			w := crewWorkspace(t, "shop")
			if tc.origin != "" {
				git(t, w.RepoDir("shop"), "remote", "add", "origin", tc.origin)
			}
			deps := fakeDeps(t, runtime.NewFake())
			deps.GitHub = github.Client{Runner: &ghScript{}}
			text := brieftest.Ship("work")
			if tc.scout {
				text = brieftest.Scout("look", "what is there?")
			}
			_, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
				Project: "shop", Crew: "k3", BriefText: text, Scout: tc.scout, Delivery: tc.delivery,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if meta, _ := w.ReadCrewMeta("shop", "k3"); len(meta) != 0 {
				t.Fatalf("a refused spawn left a meta: %v", meta)
			}
		})
	}
}

// A pull request merged on GitHub landed the work even though a squash or
// rebase merge leaves the crew's branch out of the default branch's history
// (docs/mvp.md M18): `crew stop` takes pr_state=merged as landed and removes
// the branch with -D. Open and closed pull requests prove nothing.
func TestStopCrewTakesAMergedPullRequestAsLanded(t *testing.T) {
	for _, tc := range []struct {
		prState    string
		wantLanded bool
	}{
		{crewstate.PRStateMerged, true},
		{crewstate.PRStateOpen, false},
		{crewstate.PRStateClosed, false},
		{"", false},
	} {
		t.Run("pr_state="+tc.prState, func(t *testing.T) {
			w := crewWorkspace(t, "shop")
			rt := runtime.NewFake()
			deps := fakeDeps(t, rt)
			res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
				Project: "shop", Crew: "k3", BriefText: brieftest.Ship("work"),
			})
			if err != nil {
				t.Fatalf("SpawnCrew: %v", err)
			}
			commitInWorktree(t, res.Worktree, "new.txt", "squashed on GitHub\n")
			// The squash: default gets a commit of its own with the same
			// content, so the crew's branch is no ancestor of it.
			repo := w.RepoDir("shop")
			if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("squashed on GitHub\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			git(t, repo, "add", "new.txt")
			git(t, repo, "commit", "-m", "squash merge of mate/k3")
			if tc.prState != "" {
				if err := w.UpdateCrewMeta("shop", "k3", map[string]string{crewstate.MetaPRURL: "https://github.com/a/b/pull/1", crewstate.MetaPRState: tc.prState}); err != nil {
					t.Fatal(err)
				}
			}

			stop, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3", false)
			if !tc.wantLanded {
				if !errors.Is(err, spawn.ErrUnlandedWork) {
					t.Fatalf("err = %v, want ErrUnlandedWork", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("StopCrew: %v", err)
			}
			if stop.State != spawn.CrewStateFinished || stop.Teardown != spawn.TeardownClean || stop.Unlanded || !stop.BranchRemoved {
				t.Fatalf("stop = %+v, want a clean finish that removed the branch", stop)
			}
			if exists, err := gitx.New().BranchExists(context.Background(), repo, "mate/k3"); err != nil || exists {
				t.Fatalf("branch exists = %v, %v; want it deleted with -D", exists, err)
			}
			if _, statErr := os.Stat(res.Worktree); !os.IsNotExist(statErr) {
				t.Fatal("the worktree was not removed")
			}
		})
	}
}
