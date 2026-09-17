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

	"github.com/nguyenngocanh94/matev2/internal/config"
	"github.com/nguyenngocanh94/matev2/internal/gitx"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
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
		BriefFile: briefFile(t, w, "Add a healthcheck endpoint.\nKeep it small.\n"),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if res.Harness != harness.KindCodex {
		t.Fatalf("harness = %q, want codex (the workspace default for a crew)", res.Harness)
	}
	if res.Agent != "crew-k3" {
		t.Fatalf("agent = %q, want crew-k3", res.Agent)
	}
	if res.Branch != "matev2/k3" {
		t.Fatalf("branch = %q, want matev2/k3", res.Branch)
	}
	if res.Worktree != w.WorktreeDir("shop", "k3") {
		t.Fatalf("worktree = %q, want %q", res.Worktree, w.WorktreeDir("shop", "k3"))
	}
	if !res.BriefDelivered {
		t.Fatalf("the brief was not confirmed delivered: %s", res.DeliveryWarning)
	}

	// The worktree is a real, isolated checkout of the crew's own branch.
	if got := strings.TrimSpace(git(t, res.Worktree, "rev-parse", "--abbrev-ref", "HEAD")); got != "matev2/k3" {
		t.Fatalf("worktree branch = %q, want matev2/k3", got)
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
		"matev2/k3",
		"$MATEV2_STATUS",
	} {
		if !strings.Contains(string(brief), want) {
			t.Errorf("brief.md does not carry %q:\n%s", want, brief)
		}
	}
	if strings.Contains(string(brief), spawn.BriefPlaceholder) {
		t.Error("brief.md still carries the {TASK} placeholder")
	}
	// Codex reads AGENTS.override.md at its cwd and nothing else.
	override, err := os.ReadFile(harness.CodexInstructionPath(res.Worktree))
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
		spawn.MetaBranch:     "matev2/k3",
		spawn.MetaSessionID:  "",
		spawn.MetaTranscript: "",
		spawn.MetaStartedAt:  "2026-09-17T10:00:00Z",
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

// The Codex discovery file is matev2's, not the crew's work: a crew that
// runs `git add -A` must not put it on the branch the Mate reviews.
func TestSpawnCrewKeepsTheCodexDiscoveryFileOutOfTheBranch(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	res, err := spawn.SpawnCrew(context.Background(), w, fakeDeps(t, rt), spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: "work",
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
		BriefText: "Line one.\n\nLine two.\n",
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
	req := spawn.SpawnCrewRequest{Project: "shop", Crew: "k3", BriefText: "first"}
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
	git(t, w.RepoDir("shop"), "branch", "matev2/k3")

	_, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: "work",
	})
	if err == nil {
		t.Fatal("an existing branch must be refused")
	}
	if !strings.Contains(err.Error(), "matev2/k3 already exists") {
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
		Project: "shop", Crew: "k3", BriefText: "work",
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

// TestSpawnCrewCompensatesAfterTheTabExists is the saga proof: a failure once
// the tab is live must leave no worktree, no branch, no tab and no meta - and
// must leave the brief, which is the evidence of what was asked for.
func TestSpawnCrewCompensatesAfterTheTabExists(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	rt.StartErr = errors.New("herdr refused the launch")

	_, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: "Add a healthcheck endpoint.",
	})
	if err == nil {
		t.Fatal("SpawnCrew must fail when the agent cannot start")
	}

	if _, statErr := os.Stat(w.WorktreeDir("shop", "k3")); !os.IsNotExist(statErr) {
		t.Fatalf("the worktree survived compensation: %v", statErr)
	}
	exists, branchErr := gitx.New().BranchExists(context.Background(), w.RepoDir("shop"), "matev2/k3")
	if branchErr != nil {
		t.Fatal(branchErr)
	}
	if exists {
		t.Fatal("branch matev2/k3 survived compensation")
	}
	if listed := git(t, w.RepoDir("shop"), "worktree", "list"); strings.Contains(listed, "shop-k3") {
		t.Fatalf("git still lists the crew worktree:\n%s", listed)
	}
	if _, statErr := os.Stat(w.CrewMeta("shop", "k3")); !os.IsNotExist(statErr) {
		t.Fatal("a failed spawn wrote a meta")
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
		Project: "shop", Crew: "k3", BriefText: "work",
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
	deps := fakeDeps(t, fake)
	deps.Runtime = rt
	deps.BriefDeliveryTimeout = 20 * time.Millisecond

	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: "work",
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
		Project: "shop", Crew: "k3", BriefText: "Add a healthcheck endpoint.",
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	if err := w.AppendStatus("shop", "k3", "working: reading the repo"); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "done: ready in branch matev2/k3"); err != nil {
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
	if got.Crew != "k3" || got.Harness != "codex" || got.Branch != "matev2/k3" || got.Pane != res.Pane {
		t.Fatalf("row = %+v", got)
	}
	if got.Status != "done: ready in branch matev2/k3" {
		t.Fatalf("status = %q, want the last line", got.Status)
	}
}

func TestStopCrewStopsTheAgentAndKeepsTheWork(t *testing.T) {
	w := crewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	res, err := spawn.SpawnCrew(context.Background(), w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", BriefText: "work",
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}

	stopped, err := spawn.StopCrew(context.Background(), w, deps, "shop", "k3")
	if err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	if stopped.Agent != res.Agent || !stopped.TabClosed {
		t.Fatalf("stop = %+v", stopped)
	}
	if _, ok := rt.Tabs[res.Pane]; ok {
		t.Fatal("the crew pane survived the stop")
	}
	// Task 16 owns teardown: the work stays.
	if _, statErr := os.Stat(res.Worktree); statErr != nil {
		t.Fatalf("StopCrew removed the worktree: %v", statErr)
	}
	exists, err := gitx.New().BranchExists(context.Background(), w.RepoDir("shop"), "matev2/k3")
	if err != nil || !exists {
		t.Fatalf("StopCrew removed the branch: %v, %v", exists, err)
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaAgent] != "" || meta[spawn.MetaPane] != "" {
		t.Fatalf("a stopped crew must not keep naming a pane: %v", meta)
	}
	if meta[spawn.MetaBranch] != "matev2/k3" || meta[spawn.MetaWorktree] != ".worktrees/shop-k3" {
		t.Fatalf("a stopped crew must keep its branch and worktree: %v", meta)
	}
	if meta[spawn.MetaStoppedAt] == "" {
		t.Fatal("a stopped crew must record stopped_at")
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
