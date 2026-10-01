package spawn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// RelaunchResult is what `mate crew relaunch` established. It is the
// spawn-side facts of the new agent plus what happened to the old one.
type RelaunchResult struct {
	Project   string
	Crew      string
	Harness   harness.Kind
	Model     string
	Effort    harness.Effort
	Agent     string
	Session   string
	Workspace string
	Tab       string
	Pane      string
	SessionID string
	Repo      string
	Branch    string
	Worktree  string
	BriefPath string
	// Stopped is true when a live agent was found and stopped before the
	// new one was started: a restart of a working pane, not a recovery from
	// a dead one.
	Stopped bool
	// AlreadyGone is true when the recorded agent was not in Herdr before
	// this call: the shape a Herdr crash leaves behind.
	AlreadyGone bool
	// TrustDialog and UpdateDialog are what the startup settle had to
	// answer, exactly as a spawn reports them.
	TrustDialog  bool
	UpdateDialog bool
	// BriefDelivered is true when the pane was observed to leave idle after
	// the prompt was sent. DeliveryWarning is the honest report of one that
	// was not, with PaneTail behind it.
	BriefDelivered  bool
	DeliveryWarning string
	PaneTail        string

	// launchedAt is when the harness process was started, for the meta's
	// launched_at= key. Unexported: it is a recording detail, not part of
	// what a caller reads.
	launchedAt time.Time
}

// RelaunchCrew starts a fresh agent for an existing crew in the worktree it
// already has (docs/mvp.md, "Đợt 2 sau M7"; firstmate's `relaunch --note`).
//
// It is the recovery path for a crew whose Herdr pane or agent is gone -
// after a Herdr server was lost to a machine restart, or after the harness
// process died - while its branch, worktree and `.status` file are all still
// on disk. `crew spawn` cannot be used for that: the branch already exists
// and the worktree is already there, and a spawn would refuse both. Relaunch
// reuses them and starts the crew's recorded harness again.
//
// The new agent is a fresh harness session, never a resume: the brief on
// disk - plus whatever the crew already wrote to its `.status` file - is the
// durable instruction, and firstmate's relaunch deliberately does not trust a
// harness-private conversation to survive a crash. note, when non-empty, is
// one extra line of progress carried into the first prompt, so a replacement
// does not repeat work that is only described in a dead pane.
//
// A crew that is already closed (`state=finished|failed`) is refused: the
// task is over, and starting an agent for it would resurrect a decision
// nobody made. A crew whose worktree is gone is refused too - there is
// nothing to relaunch into, and a new spawn is the right answer.
func RelaunchCrew(ctx context.Context, w *store.Workspace, deps Deps, project, crew, note string) (RelaunchResult, error) {
	if w == nil {
		return RelaunchResult{}, errUsage("spawn: a workspace is required")
	}
	if deps.Runtime == nil {
		return RelaunchResult{}, errUsage("spawn: a runtime adapter is required")
	}
	if deps.Names == nil {
		deps.Names = runtime.NewMemoryNameRegistry()
	}
	if err := store.ValidateProjectName(project); err != nil {
		return RelaunchResult{}, err
	}
	if err := store.ValidateCrewID(crew); err != nil {
		return RelaunchResult{}, err
	}
	if _, ok := w.Project(project); !ok {
		return RelaunchResult{}, fmt.Errorf("%w: %s", store.ErrNoProject, project)
	}
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return RelaunchResult{}, err
	}
	if len(meta) == 0 {
		return RelaunchResult{}, observability.NewError(observability.CodeNotFound,
			fmt.Sprintf("no crew %s is recorded for project %s; %s is empty", crew, project, w.CrewMeta(project, crew)))
	}
	if state := meta[MetaState]; state == CrewStateFinished || state == CrewStateFailed {
		return RelaunchResult{}, observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("crew %s of %s is %s; a closed crew is not relaunched - spawn a new one if the task is not over",
				crew, project, state))
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return RelaunchResult{}, err
	}
	repoCfg, err := cfg.CrewRepo(meta)
	if err != nil {
		return RelaunchResult{}, observability.WrapError(observability.CodeStateConflict,
			fmt.Sprintf("crew relaunch %s/%s refused, nothing was started (meta %s)", project, crew, w.CrewMeta(project, crew)), err)
	}
	worktree := ""
	if rel := meta[MetaWorktree]; rel != "" {
		worktree = filepath.Join(w.Root(), filepath.FromSlash(rel))
	}
	if worktree == "" {
		return RelaunchResult{}, observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("crew %s of %s records no worktree; there is nothing to relaunch into - spawn a new crew", crew, project))
	}
	if _, err := os.Stat(worktree); err != nil {
		return RelaunchResult{}, observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("the worktree of crew %s is gone (%s); there is nothing to relaunch into - spawn a new crew", crew, worktree))
	}
	briefPath := w.CrewBrief(project, crew)
	if _, err := os.ReadFile(briefPath); err != nil {
		return RelaunchResult{}, observability.NewError(observability.CodeNeedsRepair,
			fmt.Sprintf("crew %s has no readable brief at %s; a relaunch would start an agent with nothing to do", crew, briefPath))
	}
	kind, err := relaunchHarness(w, deps, meta)
	if err != nil {
		return RelaunchResult{}, err
	}
	profile, err := deps.Harnesses.Lookup(kind)
	if err != nil {
		return RelaunchResult{}, err
	}
	plan := crewPlan{
		project:  project,
		crew:     crew,
		kind:     kind,
		profile:  profile,
		model:    meta[MetaModel],
		effort:   harness.Effort(strings.TrimSpace(meta[MetaEffort])),
		branch:   meta[MetaBranch],
		worktree: worktree,
		repoCfg:  repoCfg,
	}

	// 1. Everything that can be refused without touching the old agent:
	// the harness files and the argv. Claude gets a fresh settings file and
	// session id; Codex's discovery file is refreshed from the brief
	// currently on disk, so a `brief append` made after the crash is picked
	// up. The settings file is rewritten with the same content and Codex
	// reads its discovery file when a session starts, so writing them while
	// the old agent lives changes nothing for it, and a refusal here leaves
	// a live crew running.
	prep, err := prepareCrewLaunch(ctx, w, deps, deps.git(), plan)
	if err != nil {
		return RelaunchResult{}, err
	}
	launch, err := buildCrewLaunchSpec(ctx, plan, prep)
	if err != nil {
		return RelaunchResult{}, err
	}
	sessionSpec, err := deps.sessionSpec(w)
	if err != nil {
		return RelaunchResult{}, err
	}

	// 2. The old agent, if Herdr still has one. A restart of a live crew
	// stops it first so the recorded name is free and no second pane is
	// left pointing at the same task; a crew whose agent is already gone
	// pays only the inspect that establishes it.
	stopped, err := stopCrewAgentForRelaunch(ctx, w, deps, project, crew, meta)
	if err != nil {
		return RelaunchResult{}, err
	}

	// 3. Herdr: EnsureSession starts a server that a machine restart left
	// gone, which is the whole point of this path.
	session, err := deps.Runtime.EnsureSession(ctx, sessionSpec)
	if err != nil {
		return RelaunchResult{}, err
	}
	mateDir := w.MateDir(project)
	if err := os.MkdirAll(mateDir, 0o755); err != nil {
		return RelaunchResult{}, err
	}
	wsHandle, err := ensureProjectWorkspace(ctx, deps, session, project, mateDir)
	if err != nil {
		return RelaunchResult{}, err
	}
	tab, err := deps.Runtime.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: wsHandle,
		Label:     CrewTabLabelPrefix + crew,
		Cwd:       worktree,
		Env:       crewPaneEnv(plan, session, w.CrewStatus(project, crew)),
	})
	if err != nil {
		return RelaunchResult{}, err
	}
	saga := &crewRelaunchSaga{deps: deps, session: session, tab: tab, madeTab: true}
	if _, err := relaunchInTab(ctx, w, deps, saga, plan, briefPath, prep.SessionID, launch, note); err != nil {
		saga.compensate(ctx)
		return RelaunchResult{}, err
	}
	result := saga.result
	result.Project, result.Crew, result.Harness = project, crew, kind
	result.Model, result.Effort = plan.model, plan.effort
	result.Stopped, result.AlreadyGone = stopped, !stopped
	result.Repo, result.Branch, result.Worktree, result.BriefPath = repoCfg.Name, meta[MetaBranch], worktree, briefPath

	// 4. The meta, written last. Everything that named a live pane is
	// replaced; the task, branch, worktree and repo are kept. `state=spawned`
	// is not an override (mvp.md section 4b): the crew's own last status
	// verb, or an open incident, still decides what the console shows.
	next := make(map[string]string, len(meta)+4)
	for k, v := range meta {
		next[k] = v
	}
	next[MetaAgent] = result.Agent
	next[MetaPane] = result.Pane
	next[MetaTab] = result.Tab
	next[MetaWorkspace] = result.Workspace
	next[MetaSession] = result.Session
	next[MetaSessionID] = result.SessionID
	next[MetaTranscript] = ""
	next[MetaLaunchedAt] = result.launchedAt.Format(time.RFC3339)
	next[MetaState] = CrewStateSpawned
	if strings.TrimSpace(next[MetaStartedAt]) == "" {
		next[MetaStartedAt] = deps.now().Format(time.RFC3339)
	}
	delete(next, MetaStoppedAt)
	delete(next, MetaTeardown)
	delete(next, MetaFailedReason)
	if err := w.WriteCrewMeta(project, crew, next); err != nil {
		return RelaunchResult{}, err
	}
	return result, nil
}

// relaunchHarness is the harness a relaunch launches: the one the crew was
// spawned with, or the workspace's crew default for a record old enough to
// have no harness= key.
func relaunchHarness(w *store.Workspace, deps Deps, meta map[string]string) (harness.Kind, error) {
	if raw := strings.TrimSpace(meta[MetaHarness]); raw != "" {
		return deps.Harnesses.Parse(raw)
	}
	return deps.defaultHarness(w.Defaults().CrewHarness, harness.RoleCrew)
}

// stopCrewAgentForRelaunch stops whatever Herdr still has for the crew and
// closes its tab, so the relaunch can create a new tab under the same
// label. It reports whether a live agent was actually stopped. A session
// that is not running, or an agent Herdr does not know, is not an error:
// that is the crash shape this path exists for.
func stopCrewAgentForRelaunch(ctx context.Context, w *store.Workspace, deps Deps, project, crew string, meta map[string]string) (bool, error) {
	name := meta[MetaAgent]
	spec, err := deps.sessionSpec(w)
	if err != nil {
		return false, err
	}
	session, running, err := deps.Runtime.LookupSession(ctx, spec)
	if err != nil {
		return false, err
	}
	if !running {
		return false, nil
	}
	tab := runtime.TabHandle{
		Session:     session,
		WorkspaceID: meta[MetaWorkspace],
		TabID:       meta[MetaTab],
		PaneID:      meta[MetaPane],
		Label:       CrewTabLabelPrefix + crew,
	}
	if name == "" {
		// No recorded agent, but a tab may still be there from a partial
		// record. Closing it keeps the label free for the new one.
		if err := deps.Runtime.RemoveTab(ctx, tab); err != nil && !runtime.IsTabGone(err) {
			return false, err
		}
		return false, nil
	}
	kind, _ := deps.Harnesses.Parse(meta[MetaHarness])
	handle := runtime.AgentHandle{Session: session, Name: name, RawID: crew, Kind: kind, Tab: tab}
	live, err := agentLive(ctx, deps, handle)
	if err != nil {
		return false, err
	}
	stopped := false
	if live {
		if err := stopLiveAgent(ctx, deps, handle); err != nil {
			return false, err
		}
		stopped = true
	}
	if err := confirmGone(ctx, deps, handle); err != nil {
		return false, err
	}
	deps.Names.Release(session.Name, handle.Name)
	if tab.PaneID != "" || tab.TabID != "" {
		if err := deps.Runtime.RemoveTab(ctx, tab); err != nil && !runtime.IsTabGone(err) {
			return false, err
		}
	}
	return stopped, nil
}

// crewRelaunchSaga records what a relaunch created after the old agent was
// stopped, so a failure undoes the new tab and agent while the crew's own
// branch and worktree are never touched.
type crewRelaunchSaga struct {
	deps         Deps
	session      runtime.SessionHandle
	tab          runtime.TabHandle
	madeTab      bool
	agentName    string
	startedAgent bool
	result       RelaunchResult
}

func (s *crewRelaunchSaga) compensate(ctx context.Context) {
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	if s.startedAgent && s.agentName != "" {
		handle := runtime.AgentHandle{Session: s.session, Name: s.agentName, Tab: s.tab}
		_ = s.deps.Runtime.StopAgent(ctx, handle, runtime.StopForce)
	}
	if s.madeTab {
		// Best effort: the relaunch already failed and that error is the one
		// the caller reports. A tab left behind is closed by the next
		// relaunch, which removes the recorded tab before creating its own.
		_ = s.deps.Runtime.RemoveTab(ctx, s.tab)
	}
	if s.agentName != "" && s.session.Name != "" {
		s.deps.Names.Release(s.session.Name, s.agentName)
	}
}

// relaunchInTab is everything after the tab exists that a failure has to
// compensate: the agent, its startup screen, readiness, the prompt and the
// result. The meta is deliberately not written here; the caller writes it
// in one place after this returns.
func relaunchInTab(ctx context.Context, w *store.Workspace, deps Deps, saga *crewRelaunchSaga, plan crewPlan, briefPath, sessionID string, launch harness.LaunchSpec, note string) (RelaunchResult, error) {
	reservation, err := runtime.AllocateAgentName(deps.Names, saga.session.Name, CrewAgentNamePrefix, plan.crew, runtime.FailOnCollision)
	if err != nil {
		return RelaunchResult{}, err
	}
	saga.agentName = reservation.Name()
	spec, err := runtime.NewAgentStartSpec(saga.tab, reservation, launch, deps.startTimeout())
	if err != nil {
		return RelaunchResult{}, err
	}
	if err := captureCrewHarnessProfile(ctx, w, deps, plan, launch, deps.now()); err != nil {
		return RelaunchResult{}, fmt.Errorf("capture crew harness profile: %w", err)
	}
	launchedAt := deps.now()
	handle, err := deps.Runtime.StartAgent(ctx, spec)
	if err != nil {
		return RelaunchResult{}, err
	}
	saga.startedAgent = true
	settled, err := settleStartupPrompt(ctx, deps.Runtime, handle, plan.profile, deps.startupPromptTimeout(), deps.sleep())
	if err != nil {
		return RelaunchResult{}, err
	}
	observed, err := deps.Runtime.WaitAgent(ctx, handle, runtime.DefaultWait(deps.readinessTimeout()))
	if err != nil {
		return RelaunchResult{}, err
	}
	if readiness := runtime.ClassifyObservation(observed); readiness.Kind == runtime.ReadinessFailed {
		return RelaunchResult{}, readiness.Err
	}
	delivered, warning, tail, err := deliverPrompt(ctx, deps, handle, RelaunchPrompt(briefPath, note), plan.profile)
	if err != nil {
		return RelaunchResult{}, err
	}
	saga.result = RelaunchResult{
		Agent:           handle.Name,
		Session:         saga.session.Name,
		Workspace:       saga.tab.WorkspaceID,
		Tab:             saga.tab.TabID,
		Pane:            saga.tab.PaneID,
		SessionID:       sessionID,
		TrustDialog:     settled.TrustDialogAnswered,
		UpdateDialog:    settled.UpdateDialogAnswered,
		BriefDelivered:  delivered,
		DeliveryWarning: warning,
		PaneTail:        tail,
	}
	saga.result.launchedAt = launchedAt
	return saga.result, nil
}

// RelaunchPrompt is the one line a relaunched crew is sent. It is the
// spawn-time brief pointer, with a note threaded in when the caller carried
// one: the fresh agent has no conversation, so the note is how "what was
// already tried" reaches it without a hand-written brief.
func RelaunchPrompt(briefPath, note string) string {
	note = strings.Join(strings.Fields(note), " ")
	if note == "" {
		return BriefPrompt(briefPath)
	}
	return fmt.Sprintf("Read and follow the brief at %s. Progress so far: %s. Start now.", briefPath, note)
}
