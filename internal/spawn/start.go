package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/mateassets"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// ErrMateRunning is returned by StartMate when the recorded Mate is still
// live in Herdr.
var ErrMateRunning = errors.New("mate is already running")

func errUsage(msg string) error { return observability.NewError(observability.CodeUsage, msg) }

// StartRequest is one `mate mate start`.
type StartRequest struct {
	// Project is the registered project whose Mate is started.
	Project string
	// Harness is the harness kind to launch. Empty means the workspace
	// default for a Mate (Claude Code).
	Harness harness.Kind
	// Resume asks StartMate to resume the harness session recorded in
	// `mate.meta` (task 10) when one is there. The CLI defaults this true;
	// it only matters when Fresh is false, and only takes effect when
	// `mate.meta`'s session_id was recorded under the same harness this
	// start is launching.
	Resume bool
	// Fresh forces a brand new harness session even when `mate.meta`
	// carries one to resume: `mate mate start --fresh`.
	Fresh bool
}

// StartResult is what a successful start recorded. Every field is also a
// line of `mate/mate.meta`, except StaleMeta and TrustDialogAnswered, which
// describe what this start had to deal with.
type StartResult struct {
	Project     string
	Harness     harness.Kind
	Agent       string
	Session     string
	Workspace   string
	Tab         string
	Pane        string
	SessionID   string
	StartedAt   time.Time
	MateDir     string
	Status      runtime.AgentStatus
	StaleMeta   bool
	TrustDialog bool
	// UpdateDialog is true when the startup settle skipped the harness's
	// release-update prompt (Codex, measured 2026-09-18).
	UpdateDialog bool
	// HooksTrusted is true when the startup settle trusted the Codex Mate's
	// own SessionStart hook in Codex's hook review (task 37).
	HooksTrusted bool
	// Resumed is true when this start resumed the harness session recorded
	// in `mate.meta` (task 10) instead of minting a fresh one.
	Resumed bool
	// ResumedFrom is the session id resumed from. Empty unless Resumed.
	ResumedFrom string
	// ResumeNote explains why a resume that was requested did not happen
	// (harness mismatch, a Codex session with no rollout on disk, or a
	// resumed launch that did not come up), so this start went fresh
	// instead. Empty when nothing needed saying.
	ResumeNote string
	// Adopted reports that nothing was launched: an interrupted start had
	// left this Mate running without a mate.meta, and this start recorded it.
	Adopted bool
}

// StartMate starts the Mate of one project: it refreshes the Mate's
// directory, creates the Herdr workspace and tab, launches the harness,
// settles its startup screen, and records `mate.meta`.
//
// A failure after the tab exists is compensated: the agent is stopped, the
// tab is closed, and no `mate.meta` is left behind. The only state a
// successful start depends on is what Herdr reports, never the previous
// meta.
func StartMate(ctx context.Context, w *store.Workspace, deps Deps, req StartRequest) (StartResult, error) {
	if w == nil {
		return StartResult{}, errUsage("spawn: a workspace is required")
	}
	if deps.Runtime == nil {
		return StartResult{}, errUsage("spawn: a runtime adapter is required")
	}
	if deps.Names == nil {
		deps.Names = runtime.NewMemoryNameRegistry()
	}
	project := req.Project
	if err := store.ValidateProjectName(project); err != nil {
		return StartResult{}, err
	}
	if _, ok := w.Project(project); !ok {
		return StartResult{}, fmt.Errorf("%w: %s", store.ErrNoProject, project)
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return StartResult{}, err
	}
	kind := req.Harness
	if kind == "" {
		kind, err = deps.defaultHarness(w.Defaults().MateHarness, harness.RoleMate)
		if err != nil {
			return StartResult{}, err
		}
	}
	profile, err := deps.Harnesses.Lookup(kind)
	if err != nil {
		return StartResult{}, err
	}

	// 1. The recorded Mate, re-checked against Herdr. The meta alone is
	// never trusted: a stale one is reported and overwritten.
	staleMeta, err := refuseIfLive(ctx, w, deps, project)
	if err != nil {
		return StartResult{}, err
	}

	// 1b. Task 10: whether this start can resume the harness session
	// `mate.meta` still carries from before the last stop. Read before
	// prepareMateDir/startInTab touch anything.
	priorMeta, err := w.ReadMateMeta(project)
	if err != nil {
		return StartResult{}, err
	}
	if staleMeta && priorMeta[MetaHarness] == string(harness.KindCodex) && strings.TrimSpace(priorMeta[MetaSessionID]) == "" {
		// A Codex Mate that died without `mate stop` never had its session
		// recorded (StopMate is where Herdr is asked); its rollout can
		// still say which one it was.
		if id := codexSessionAtStop(deps, w.MateDir(project), priorMeta, ""); id != "" {
			priorMeta[MetaSessionID] = id
		}
	}
	decision := decideResume(priorMeta, kind, req)
	decision = checkCodexResume(deps, kind, decision)

	// 2. The Mate's directory: the manual, rendered again on every start,
	// and the files the harness launches with.
	mateDir := w.MateDir(project)
	if err := prepareMateDir(w, deps, project, cfg, profile, mateDir); err != nil {
		return StartResult{}, err
	}
	prep, err := prepareMateLaunch(ctx, w, deps, profile, mateDir, decision)
	if err != nil {
		return StartResult{}, err
	}

	// 3. Herdr: the workspace session, a workspace labelled with the
	// project, and the Mate tab. All three are cwd = the Mate directory.
	sessionSpec, err := deps.sessionSpec(w)
	if err != nil {
		return StartResult{}, err
	}
	session, err := deps.Runtime.EnsureSession(ctx, sessionSpec)
	if err != nil {
		return StartResult{}, err
	}

	// 3b. A start that was interrupted after Herdr launched the agent (the
	// Console quit mid-start, the process was killed) leaves the Mate
	// running with no mate.meta. It is this project's Mate, so it is
	// adopted rather than launched a second time - which Herdr would refuse
	// by name - or stopped, which would throw its conversation away.
	if strings.TrimSpace(priorMeta[MetaAgent]) == "" {
		adopted, ok, err := adoptInterruptedStart(ctx, w, deps, project, kind, mateDir, session)
		if err != nil || ok {
			adopted.StaleMeta = staleMeta
			return adopted, err
		}
	}

	tab, err := openMateTab(ctx, deps, session, project, mateDir)
	if err != nil {
		return StartResult{}, err
	}

	// From here on every failure must undo the tab and leave no meta.
	result, err := startInTab(ctx, w, deps, project, profile, mateDir, decision, prep, session, tab)
	if err != nil {
		compensate(ctx, deps, w, project, session, tab)
		if !decision.Resume || ctx.Err() != nil {
			return StartResult{}, err
		}
		// A resume that did not come up is not a reason to leave the
		// project without a Mate: the conversation is a convenience, the
		// files are the memory (docs/mvp.md M8). One fresh attempt, in a
		// tab of its own, and the result says why it went fresh.
		fresh := resumeDecision{Note: fmt.Sprintf(
			"resuming the %s session %s failed (%v); started a fresh session instead",
			kind, decision.SessionID, oneLineErr(err))}
		if prep, err = prepareMateLaunch(ctx, w, deps, profile, mateDir, fresh); err != nil {
			return StartResult{}, err
		}
		tab, err = openMateTab(ctx, deps, session, project, mateDir)
		if err != nil {
			return StartResult{}, err
		}
		result, err = startInTab(ctx, w, deps, project, profile, mateDir, fresh, prep, session, tab)
		if err != nil {
			compensate(ctx, deps, w, project, session, tab)
			return StartResult{}, err
		}
	}
	result.StaleMeta = staleMeta
	return result, nil
}

// openMateTab adopts or creates the project's Herdr workspace and opens the
// Mate tab in it, cwd = the Mate directory.
func openMateTab(ctx context.Context, deps Deps, session runtime.SessionHandle, project, mateDir string) (runtime.TabHandle, error) {
	wsHandle, err := ensureProjectWorkspace(ctx, deps, session, project, mateDir)
	if err != nil {
		return runtime.TabHandle{}, err
	}
	return deps.Runtime.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: wsHandle,
		Label:     MateTabLabel,
		Cwd:       mateDir,
	})
}

// oneLineErr flattens an error for a one-line note: startup refusals carry
// the pane's screen tail on the lines after the message.
func oneLineErr(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	return strings.TrimSpace(msg)
}

// checkCodexResume refuses, before anything is launched, to resume a Codex
// session whose rollout is not on disk. codex-cli 0.154.0 answers `codex
// resume <unknown id>` with "No saved session found with ID ..." and drops to
// the shell, which Herdr reports only as an agent-start timeout a minute
// later (measured 2026-09-24, task 35); the file name says it at once.
func checkCodexResume(deps Deps, kind harness.Kind, decision resumeDecision) resumeDecision {
	if kind != harness.KindCodex || !decision.Resume {
		return decision
	}
	dir, err := deps.codexSessionsDir()
	if err != nil {
		return resumeDecision{Note: fmt.Sprintf("cannot look for the Codex session %s to resume (%v); starting a fresh session instead", decision.SessionID, err)}
	}
	if _, ok := harness.CodexRolloutPath(dir, decision.SessionID); !ok {
		return resumeDecision{Note: fmt.Sprintf("mate.meta recorded the Codex session %s but %s has no rollout for it; starting a fresh session instead", decision.SessionID, dir)}
	}
	return decision
}

// resumeDecision is what task 10's resume logic concluded before a single
// Herdr call is made: whether this start resumes a recorded session, and if
// not, why not (so the caller can say so instead of silently going fresh).
type resumeDecision struct {
	Resume    bool
	SessionID string
	Note      string
}

// decideResume applies task 10's rule: resume only when the caller did not
// force Fresh, asked to Resume, `mate.meta` still carries a non-empty
// session_id from a previous stop, and that id was recorded under the same
// harness this start is launching. A harness mismatch is reported, not
// silently overridden - Stop keeps session_id= across a harness switch, so
// meta alone cannot tell a stale id from a live one.
func decideResume(meta map[string]string, kind harness.Kind, req StartRequest) resumeDecision {
	if req.Fresh || !req.Resume {
		return resumeDecision{}
	}
	priorID := strings.TrimSpace(meta[MetaSessionID])
	if priorID == "" {
		return resumeDecision{}
	}
	if priorHarness := meta[MetaHarness]; priorHarness != "" && priorHarness != string(kind) {
		return resumeDecision{Note: fmt.Sprintf(
			"mate.meta recorded harness %q but this start is launching %q; starting a fresh %s session instead of resuming",
			priorHarness, kind, kind,
		)}
	}
	return resumeDecision{Resume: true, SessionID: priorID}
}

// startInTab is everything a failure has to compensate for: the launch spec,
// the agent, its startup screen, the readiness wait and the meta. The
// session id is the one the launch was prepared with: the resumed one, a
// fresh one for a harness that names its session at launch, or none for a
// harness whose id exists only once the first prompt opens its session,
// which StopMate records (task 35).
func startInTab(ctx context.Context, w *store.Workspace, deps Deps, project string, profile harness.Profile, mateDir string, decision resumeDecision, prep harness.Prepared, session runtime.SessionHandle, tab runtime.TabHandle) (StartResult, error) {
	env, err := mateEnv(project, session)
	if err != nil {
		return StartResult{}, err
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return StartResult{}, err
	}
	launch, err := buildLaunchSpec(ctx, profile, project, mateDir, decision, prep, env, cfg.Mate)
	if err != nil {
		return StartResult{}, err
	}
	reservation, err := runtime.AllocateAgentName(deps.Names, session.Name, AgentNamePrefix, project, runtime.FailOnCollision)
	if err != nil {
		return StartResult{}, err
	}
	spec, err := runtime.NewAgentStartSpec(tab, reservation, launch, deps.startTimeout())
	if err != nil {
		return StartResult{}, err
	}
	launchedAt := deps.now()
	handle, err := deps.Runtime.StartAgent(ctx, spec)
	if err != nil {
		return StartResult{}, err
	}
	return settleAndRecord(ctx, w, deps, project, profile.Kind(), mateDir, decision, session, tab, handle, prep.SessionID, launchedAt, launch)
}

// settleAndRecord takes a launched Mate agent the rest of the way: the
// startup dialogs, readiness, and the mate.meta that makes it this
// project's Mate. A fresh launch and an adopted interrupted one share it.
func settleAndRecord(ctx context.Context, w *store.Workspace, deps Deps, project string, kind harness.Kind, mateDir string, decision resumeDecision, session runtime.SessionHandle, tab runtime.TabHandle, handle runtime.AgentHandle, sessionID string, launchedAt time.Time, launches ...harness.LaunchSpec) (StartResult, error) {
	resume, resumeNote := decision.Resume, decision.Note
	trusted, err := ownHooks(deps, kind, mateDir)
	if err != nil {
		return StartResult{}, err
	}
	profile, err := deps.Harnesses.Lookup(kind)
	if err != nil {
		return StartResult{}, err
	}
	settled, err := settleStartupPrompt(ctx, deps.Runtime, handle, profile, deps.startupPromptTimeout(), deps.sleep(), trusted...)
	if err != nil {
		return StartResult{}, err
	}
	observed, err := deps.Runtime.WaitAgent(ctx, handle, runtime.DefaultWait(deps.readinessTimeout()))
	if err != nil {
		return StartResult{}, err
	}
	if readiness := runtime.ClassifyObservation(observed); readiness.Kind == runtime.ReadinessFailed {
		return StartResult{}, readiness.Err
	}

	startedAt := deps.now()
	meta := map[string]string{
		MetaHarness:   string(kind),
		MetaAgent:     handle.Name,
		MetaPane:      tab.PaneID,
		MetaTab:       tab.TabID,
		MetaWorkspace: tab.WorkspaceID,
		MetaSession:   session.Name,
		MetaSessionID: sessionID,
		MetaStartedAt: startedAt.Format(time.RFC3339),
		// See MetaLaunchedAt: a Codex Mate's rollout is adopted from it.
		MetaLaunchedAt: launchedAt.Format(time.RFC3339),
	}
	if len(launches) > 0 {
		if launches[0].Model() != "" {
			meta[MetaModel] = launches[0].Model()
		}
		if launches[0].Effort() != "" {
			meta[MetaEffort] = string(launches[0].Effort())
		}
	}
	var resumedFrom string
	if resume {
		resumedFrom = decision.SessionID
		meta[MetaResumed] = "true"
		meta[MetaResumedFrom] = resumedFrom
	}
	if err := w.WriteMateMeta(project, meta); err != nil {
		return StartResult{}, err
	}
	return StartResult{
		Project:      project,
		Harness:      kind,
		Agent:        handle.Name,
		Session:      session.Name,
		Workspace:    tab.WorkspaceID,
		Tab:          tab.TabID,
		Pane:         tab.PaneID,
		SessionID:    sessionID,
		StartedAt:    startedAt,
		MateDir:      mateDir,
		Status:       observed.Status,
		TrustDialog:  settled.TrustDialogAnswered,
		UpdateDialog: settled.UpdateDialogAnswered,
		HooksTrusted: settled.HooksTrusted,
		Resumed:      resume,
		ResumedFrom:  resumedFrom,
		ResumeNote:   resumeNote,
	}, nil
}

// refuseIfLive re-asks Herdr about the agent name in `mate.meta`. It returns
// true when the meta named an agent Herdr does not have - a stale record the
// caller may overwrite - and an error when the Mate really is running.
func refuseIfLive(ctx context.Context, w *store.Workspace, deps Deps, project string) (stale bool, err error) {
	meta, err := w.ReadMateMeta(project)
	if err != nil {
		return false, err
	}
	name := meta[MetaAgent]
	if name == "" {
		return false, nil
	}
	spec, err := deps.sessionSpec(w)
	if err != nil {
		return false, err
	}
	session, running, err := deps.Runtime.LookupSession(ctx, spec)
	if err != nil {
		return false, err
	}
	if !running {
		return true, nil
	}
	if _, err := deps.Runtime.InspectAgent(ctx, runtime.AgentHandle{Session: session, Name: name}); err != nil {
		if runtime.IsAgentNotFound(err) {
			return true, nil
		}
		return false, err
	}
	return false, observability.NewError(observability.CodeAlreadyExists,
		fmt.Sprintf("%v: project %s is served by agent %q in session %s; stop it first", ErrMateRunning, project, name, session.Name)).
		WithDetails(map[string]any{"agent_name": name, "herdr_session": session.Name})
}

// prepareMateDir renders the Mate's cwd: the operating manual, the skills
// where the harness finds them, and the memory and backlog files mateassets
// creates only when missing.
func prepareMateDir(w *store.Workspace, deps Deps, project string, cfg store.ProjectConfig, profile harness.Profile, mateDir string) error {
	if err := os.MkdirAll(mateDir, 0o755); err != nil {
		return err
	}
	binary, err := deps.binary()
	if err != nil {
		return err
	}
	repos := make([]mateassets.RepoParams, 0, len(cfg.Repos))
	for _, r := range cfg.Repos {
		repos = append(repos, mateassets.RepoParams{Name: r.Name, Path: w.RepoDir(r.Path), DefaultBranch: r.DefaultBranch})
	}
	return mateassets.Write(mateDir, mateassets.Params{
		ProjectName:      project,
		WorkspaceRoot:    w.Root(),
		Repos:            repos,
		Mode:             cfg.Mode,
		Yolo:             cfg.Yolo,
		Harness:          string(profile.Kind()),
		SkillsDir:        profile.Info().SkillsDir,
		WorkspaceDoc:     w.WorkspaceDoc(),
		ProjectDoc:       w.ProjectDoc(project),
		WorkspaceCrewDoc: w.WorkspaceCrewDoc(),
		ProjectCrewDoc:   w.ProjectCrewDoc(project),
		MemoryFile:       w.MemoryFile(project),
		BacklogFile:      w.BacklogFile(project),
		MatevBin:         binary,
		MateDir:          mateDir,
		CrewsDir:         w.CrewsDir(project),
	})
}

// prepareMateLaunch asks the harness to lay out this start and writes what
// it names: the name it reads the manual under, its settings and hooks. A
// fresh start's session id is minted here, for a harness that names its
// session at launch.
func prepareMateLaunch(ctx context.Context, w *store.Workspace, deps Deps, profile harness.Profile, mateDir string, decision resumeDecision) (harness.Prepared, error) {
	binary, err := deps.binary()
	if err != nil {
		return harness.Prepared{}, err
	}
	req := harness.PrepareRequest{
		Role:         harness.RoleMate,
		Cwd:          mateDir,
		StateDir:     mateDir,
		ContextPath:  filepath.Join(mateDir, mateassets.ManualName),
		Binary:       binary,
		NewSessionID: deps.NewSessionID,
	}
	if decision.Resume {
		req.ResumeSessionID = decision.SessionID
	}
	prep, err := profile.Launcher().Prepare(ctx, req)
	if err != nil {
		return harness.Prepared{}, err
	}
	// A Mate's directory is not a git working tree: nothing to exclude.
	if err := writeLaunchFiles(ctx, w, nil, mateDir, prep.Files); err != nil {
		return harness.Prepared{}, err
	}
	return prep, nil
}

// ownHooks is what the startup settle may trust in Codex's hook review for
// this launch: the Codex Mate's own SessionStart hook, from its own file,
// and nothing else. A Claude Mate and every Crew get none.
func ownHooks(deps Deps, kind harness.Kind, mateDir string) ([]harness.OwnHook, error) {
	if kind != harness.KindCodex {
		return nil, nil
	}
	binary, err := deps.binary()
	if err != nil {
		return nil, err
	}
	return []harness.OwnHook{{
		Event:   "SessionStart",
		Source:  harness.CodexHooksPath(mateDir),
		Command: harness.SessionHookCommand(binary, harness.KindCodex),
	}}, nil
}

// buildLaunchSpec asks the harness's launcher for the argv of the launch it
// prepared. The cwd is the Mate directory, which is also where the manual
// is.
func buildLaunchSpec(ctx context.Context, profile harness.Profile, project, mateDir string, decision resumeDecision, prep harness.Prepared, env []runtime.EnvVar, profiles ...store.MateConfig) (harness.LaunchSpec, error) {
	spec := harness.AgentSpec{
		ID:          AgentNamePrefix + "-" + project,
		Role:        harness.RoleMate,
		Kind:        profile.Kind(),
		Cwd:         mateDir,
		ContextPath: prep.ContextPath,
		Launch:      prep.Launch,
		// The Mate's environment rides on the launch as well as on the
		// workspace create: the runtime exports it into the pane before
		// every start, which is the only way a Mate restarted into a fresh
		// `tab create` (a Crew still holds the workspace) keeps its
		// identity.
		Env: launchEnv(env, profile),
	}
	if decision.Resume {
		spec.ResumeSessionID = decision.SessionID
	}
	var err error
	if len(profiles) > 0 {
		spec.Model, err = harness.ParseModel(profiles[0].Model)
		if err != nil {
			return harness.LaunchSpec{}, err
		}
		spec.Effort, err = harness.ParseEffort(profiles[0].Effort)
		if err != nil {
			return harness.LaunchSpec{}, err
		}
	}
	return profile.Launcher().Build(ctx, spec)
}

// ensureProjectWorkspace creates the Herdr workspace for a project, injecting
// the identity environment only on the create: Herdr can apply `--env` when
// the pane is made and never afterwards, so an existing workspace is adopted
// as it is.
func ensureProjectWorkspace(ctx context.Context, deps Deps, session runtime.SessionHandle, project, mateDir string) (runtime.WorkspaceHandle, error) {
	spec := runtime.WorkspaceSpec{Session: session, Label: project, Cwd: mateDir}
	if _, found, err := deps.Runtime.LookupProjectWorkspace(ctx, spec); err != nil {
		return runtime.WorkspaceHandle{}, err
	} else if found {
		return deps.Runtime.EnsureProjectWorkspace(ctx, spec)
	}
	env, err := mateEnv(project, session)
	if err != nil {
		return runtime.WorkspaceHandle{}, err
	}
	spec.Env = env
	return deps.Runtime.EnsureProjectWorkspace(ctx, spec)
}

// mateEnv is the Mate pane's environment: its identity, and the CODEX_HOME
// every Codex agent of this project runs in.
//
// MATE_CALLER is how `mate merge` knows a Mate typed it and applies the
// project's `yolo` rule (docs/mvp.md M4 decisions), and MATE_AGENT_ROLE is
// how `mate send` records a line as the Mate's. CODEX_HOME is pinned for
// either harness because the Mate's own `mate crew spawn` launches Codex
// Crews and finds their rollouts from it: a Claude Mate that inherited
// whatever the Herdr server was started with would put its Crews' trust
// and rollouts somewhere this process never looks. harness.LaunchCodexHome
// is also what refuses the operator's home during a live test run.
func mateEnv(project string, session runtime.SessionHandle) ([]runtime.EnvVar, error) {
	codexHome, err := harness.LaunchCodexHome("")
	if err != nil {
		return nil, observability.WrapError(observability.CodeUsage, "codex home", err)
	}
	return []runtime.EnvVar{
		{Key: config.EnvProjectID, Value: project},
		{Key: config.EnvAgentID, Value: AgentNamePrefix + "-" + project},
		{Key: config.EnvAgentRole, Value: string(harness.RoleMate)},
		{Key: config.EnvRuntimeSessionID, Value: session.Name},
		{Key: config.EnvCaller, Value: CallerMate},
		{Key: config.EnvCodexHome, Value: codexHome},
	}, nil
}

// launchEnv is env as a launch carries it. The variables the harness
// declares (harness.Info.EnvKeys) are its own to set, and a launch refuses a
// second assignment, so they are left to it; another harness's variables
// ride along, because the agent may launch that harness.
func launchEnv(env []runtime.EnvVar, profile harness.Profile) []harness.EnvVar {
	own := profile.Info().EnvKeys
	out := make([]harness.EnvVar, 0, len(env))
	for _, v := range env {
		if slices.Contains(own, v.Key) {
			continue
		}
		out = append(out, harness.EnvVar{Key: v.Key, Value: v.Value})
	}
	return out
}

// compensate undoes a start that failed after the tab existed: the agent is
// force-stopped, the tab is closed, and any `mate.meta` is removed, because
// a meta naming a pane nobody owns is worse than none. Its own failures are
// deliberately swallowed: the caller must see why the start was refused.
func compensate(ctx context.Context, deps Deps, w *store.Workspace, project string, session runtime.SessionHandle, tab runtime.TabHandle) {
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	name, _ := runtime.SanitizeAgentName(AgentNamePrefix, project)
	if name != "" {
		// The agent is stopped only when Herdr has it in this attempt's own
		// pane: a launch Herdr accepted whose answer was lost is ours to
		// undo, but an agent of the same name anywhere else is not - it
		// may be a Mate whose meta was lost, and stopping it would throw
		// its conversation away.
		handle := runtime.AgentHandle{Session: session, Name: name, RawID: project, Tab: tab}
		if obs, err := deps.Runtime.InspectAgent(ctx, handle); err == nil && obs.Handle.Tab.PaneID == tab.PaneID {
			_ = deps.Runtime.StopAgent(ctx, handle, runtime.StopForce)
		}
	}
	if err := deps.Runtime.RemoveTab(ctx, tab); err != nil && !runtime.IsTabGone(err) {
		_ = err
	}
	deps.Names.Release(session.Name, name)
	_ = os.Remove(w.MateMeta(project))
}

// adoptInterruptedStart looks for this project's Mate agent already running
// in Herdr although mate.meta records none. ok is false when there is none,
// and the caller launches as usual. One running from the Mate directory is
// what an interrupted start leaves behind; it is settled and recorded like
// a fresh launch. One running anywhere else is not this Mate, and mate
// neither adopts nor stops an agent it did not start.
func adoptInterruptedStart(ctx context.Context, w *store.Workspace, deps Deps, project string, kind harness.Kind, mateDir string, session runtime.SessionHandle) (StartResult, bool, error) {
	name, err := runtime.SanitizeAgentName(AgentNamePrefix, project)
	if err != nil {
		return StartResult{}, false, err
	}
	obs, err := deps.Runtime.InspectAgent(ctx, runtime.AgentHandle{Session: session, Name: name, RawID: project})
	if err != nil {
		if runtime.IsAgentNotFound(err) {
			return StartResult{}, false, nil
		}
		return StartResult{}, false, err
	}
	pane := obs.Handle.Tab.PaneID
	if !sameDir(obs.Cwd, mateDir) {
		return StartResult{}, false, observability.NewError(observability.CodeAlreadyExists, fmt.Sprintf(
			"Herdr already runs an agent named %s in pane %s of session %s, started in %s rather than this project's Mate directory %s; mate neither adopts nor stops an agent it did not start - stop or rename it in Herdr, then start again",
			name, pane, session.Name, obs.Cwd, mateDir))
	}
	if obs.Handle.Kind != "" && obs.Handle.Kind != kind {
		return StartResult{}, false, observability.NewError(observability.CodeAlreadyExists, fmt.Sprintf(
			"an interrupted start left a %s Mate running as %s in pane %s; start with the %s harness to adopt it, or stop it in Herdr first",
			obs.Handle.Kind, name, pane, obs.Handle.Kind))
	}
	handle := obs.Handle
	handle.Session, handle.Name, handle.RawID, handle.Kind = session, name, project, kind
	decision := resumeDecision{Note: fmt.Sprintf(
		"adopted agent %s, left running in pane %s by a start that was interrupted before it finished", name, pane)}
	// Herdr reports a Codex rollout's session id; Claude's is filled in by
	// the Mate's own SessionStart hook on its next turn.
	res, err := settleAndRecord(ctx, w, deps, project, kind, mateDir, decision, session, handle.Tab, handle, obs.SessionRef, deps.now())
	res.Adopted = err == nil
	return res, true, err
}

// sameDir reports whether two paths name the same directory, following
// symlinks (a workspace under /tmp is /private/tmp on macOS).
func sameDir(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}
