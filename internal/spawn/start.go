package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/mateassets"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// ClaudeSettingsDir and ClaudeSettingsFile are the Claude settings the Mate
// launches with: ClaudeSettings wires its three hooks to the mate binary and
// turns Claude Code's auto-memory off. Start creates the file, and on one
// that already exists - the user's own, or one a previous start wrote - it
// only ever adds a missing autoMemoryEnabled key and a missing SessionStart
// hook.
const (
	ClaudeSettingsDir  = ".claude"
	ClaudeSettingsFile = "settings.json"
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
		kind, err = harness.ParseKind(w.Defaults().MateHarness)
		if err != nil {
			return StartResult{}, err
		}
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
	// and the settings file Claude launches with.
	mateDir := w.MateDir(project)
	if err := prepareMateDir(w, deps, project, cfg, kind, mateDir); err != nil {
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
	result, err := startInTab(ctx, w, deps, project, kind, mateDir, decision, session, tab)
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
		tab, err = openMateTab(ctx, deps, session, project, mateDir)
		if err != nil {
			return StartResult{}, err
		}
		result, err = startInTab(ctx, w, deps, project, kind, mateDir, fresh, session, tab)
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

// freshSessionID mints the Claude session uuid a non-resuming start needs.
// Codex has no launch-time session identity: its id exists only once the
// first prompt opens the rollout, so StopMate records it (task 35) and a
// fresh Codex start writes session_id= empty (start_test.go's
// TestStartMateCodexWritesTheDiscoveryFile).
func freshSessionID(deps Deps, kind harness.Kind) string {
	if kind != harness.KindClaude {
		return ""
	}
	if deps.NewSessionID != nil {
		return deps.NewSessionID()
	}
	return uuid.NewString()
}

// startInTab is everything a failure has to compensate for: the launch spec,
// the agent, its startup screen, the readiness wait and the meta.
func startInTab(ctx context.Context, w *store.Workspace, deps Deps, project string, kind harness.Kind, mateDir string, decision resumeDecision, session runtime.SessionHandle, tab runtime.TabHandle) (StartResult, error) {
	sessionID, resume := decision.SessionID, decision.Resume
	if !resume {
		sessionID = freshSessionID(deps, kind)
	}
	env, err := mateEnv(project, session)
	if err != nil {
		return StartResult{}, err
	}
	launch, err := buildLaunchSpec(ctx, project, kind, mateDir, sessionID, resume, env)
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
	return settleAndRecord(ctx, w, deps, project, kind, mateDir, decision, session, tab, handle, sessionID, launchedAt)
}

// settleAndRecord takes a launched Mate agent the rest of the way: the
// startup dialogs, readiness, and the mate.meta that makes it this
// project's Mate. A fresh launch and an adopted interrupted one share it.
func settleAndRecord(ctx context.Context, w *store.Workspace, deps Deps, project string, kind harness.Kind, mateDir string, decision resumeDecision, session runtime.SessionHandle, tab runtime.TabHandle, handle runtime.AgentHandle, sessionID string, launchedAt time.Time) (StartResult, error) {
	resume, resumeNote := decision.Resume, decision.Note
	trusted, err := ownHooks(deps, kind, mateDir)
	if err != nil {
		return StartResult{}, err
	}
	settled, err := settleStartupPrompt(ctx, deps.Runtime, handle, kind, deps.startupPromptTimeout(), deps.sleep(), trusted...)
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

// prepareMateDir renders the Mate's cwd: the operating manual, the memory
// and backlog files mateassets creates only when missing, and the Claude
// settings file. It returns the Claude session uuid for this launch (empty
// for a harness that has none).
func prepareMateDir(w *store.Workspace, deps Deps, project string, cfg store.ProjectConfig, kind harness.Kind, mateDir string) error {
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
	if err := mateassets.Write(mateDir, mateassets.Params{
		ProjectName:      project,
		WorkspaceRoot:    w.Root(),
		Repos:            repos,
		Mode:             cfg.Mode,
		Yolo:             cfg.Yolo,
		Harness:          string(kind),
		WorkspaceDoc:     w.WorkspaceDoc(),
		ProjectDoc:       w.ProjectDoc(project),
		WorkspaceCrewDoc: w.WorkspaceCrewDoc(),
		ProjectCrewDoc:   w.ProjectCrewDoc(project),
		MemoryFile:       w.MemoryFile(project),
		BacklogFile:      w.BacklogFile(project),
		MatevBin:         binary,
		MateDir:          mateDir,
		CrewsDir:         w.CrewsDir(project),
	}); err != nil {
		return err
	}
	if err := ensureClaudeSettings(mateDir, binary); err != nil {
		return err
	}
	if kind == harness.KindCodex {
		// Codex discovers AGENTS.override.md, in preference to a tracked
		// AGENTS.md, at the directory it runs in. The manual is the same
		// text either way; this is the name Codex reads it under.
		if err := writeCodexOverride(mateDir); err != nil {
			return err
		}
		if err := writeCodexHooks(mateDir, binary); err != nil {
			return err
		}
	}
	return nil
}

// CodexHooksDir and CodexHooksFile are where a Codex Mate's SessionStart
// hook lives: `.codex/hooks.json` of its cwd, which Codex loads once the
// directory is trusted (task 35, A3). The operator's own
// `$CODEX_HOME/hooks.json` is never read or written.
const (
	CodexHooksDir  = ".codex"
	CodexHooksFile = "hooks.json"
)

// CodexHooksPath is the Codex Mate's hooks file.
func CodexHooksPath(mateDir string) string {
	return filepath.Join(mateDir, CodexHooksDir, CodexHooksFile)
}

// writeCodexHooks writes CodexHooks for binary, only when it differs, so an
// unchanged file keeps its mtime as well as the trust Codex recorded for it.
func writeCodexHooks(mateDir, binary string) error {
	path := CodexHooksPath(mateDir)
	want := CodexHooks(binary)
	if have, err := os.ReadFile(path); err == nil && string(have) == string(want) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, want, 0o644)
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
		Source:  CodexHooksPath(mateDir),
		Command: SessionHookCommand(binary, harness.KindCodex),
	}}, nil
}

// ensureClaudeSettings creates `<mate>/.claude/settings.json` if it is not
// there, wired to binary's `hook mate-prompt`/`hook mate-stop`/`hook
// mate-session` (ClaudeSettings). An existing file - the user's own, or one a
// previous start already wrote - keeps every key it has; the two things a
// start adds to it are `autoMemoryEnabled: false` when the file does not say
// (EnsureAutoMemoryOff), and the SessionStart hook when no SessionStart entry
// runs it (EnsureSessionHook), so a Mate directory made before task 35 or 37
// starts with auto-memory off and its digest wired too.
func ensureClaudeSettings(mateDir, binary string) error {
	dir := filepath.Join(mateDir, ClaudeSettingsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, ClaudeSettingsFile)
	existing, err := os.ReadFile(path)
	if err == nil {
		updated, memoryChanged, err := EnsureAutoMemoryOff(existing)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		updated, hookChanged, err := EnsureSessionHook(updated, binary)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !memoryChanged && !hookChanged {
			return nil
		}
		return os.WriteFile(path, updated, 0o644)
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := ClaudeSettings(binary)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// writeCodexOverride copies the rendered manual to the name Codex reads.
func writeCodexOverride(mateDir string) error {
	manual, err := os.ReadFile(filepath.Join(mateDir, "AGENTS.md"))
	if err != nil {
		return err
	}
	return os.WriteFile(harness.CodexInstructionPath(mateDir), manual, 0o644)
}

// buildLaunchSpec asks the harness adapter for the argv. The cwd is the Mate
// directory, which is also where the manual is: the adapters require a
// context path, so the path they are given is that same manual, never a
// separate generated file.
func buildLaunchSpec(ctx context.Context, project string, kind harness.Kind, mateDir, sessionID string, resume bool, env []runtime.EnvVar) (harness.LaunchSpec, error) {
	adapter, err := harness.AdapterFor(kind)
	if err != nil {
		return harness.LaunchSpec{}, err
	}
	spec := harness.AgentSpec{
		ID:   AgentNamePrefix + "-" + project,
		Role: harness.RoleMate,
		Kind: kind,
		Cwd:  mateDir,
		// The Mate's environment rides on the launch as well as on the
		// workspace create: the runtime exports it into the pane before
		// every start, which is the only way a Mate restarted into a fresh
		// `tab create` (a Crew still holds the workspace) keeps its
		// identity. The Codex adapter pins CODEX_HOME itself.
		Env:    launchEnv(env, kind),
		Config: harness.Config{Kind: kind},
	}
	switch kind {
	case harness.KindClaude:
		// No context path: `<mate>/CLAUDE.md` is `@AGENTS.md` and Claude
		// loads it from the cwd on its own, so passing the same manual as
		// --append-system-prompt-file put it in context twice (docs/mvp.md,
		// task 17).
		spec.ManualInCwd = true
		// A fresh session id is what a first start names for a later
		// `--resume` (task 10); the settings file is where task 08's hooks
		// go. Claude refuses either without the other, fresh or resumed.
		if resume {
			spec.ResumeSessionID = sessionID
		} else {
			spec.ClaudeSessionID = sessionID
		}
		spec.ClaudeSettingsPath = filepath.Join(mateDir, ClaudeSettingsDir, ClaudeSettingsFile)
	case harness.KindCodex:
		spec.ContextPath = harness.CodexInstructionPath(mateDir)
		if resume {
			spec.ResumeSessionID = sessionID
		}
	}
	return adapter.BuildLaunchSpec(ctx, spec)
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

// launchEnv is env as a launch carries it. A Codex launch pins CODEX_HOME
// itself (harness.Codex.BuildLaunchSpec) and refuses a second assignment.
func launchEnv(env []runtime.EnvVar, kind harness.Kind) []harness.EnvVar {
	out := make([]harness.EnvVar, 0, len(env))
	for _, v := range env {
		if kind == harness.KindCodex && v.Key == config.EnvCodexHome {
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
