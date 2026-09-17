package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/nguyenngocanh94/matev2/internal/config"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/mateassets"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// ClaudeSettingsDir and ClaudeSettingsFile are the Claude settings the Mate
// launches with. Task 08 fills the hooks; start only guarantees the file
// exists, and never overwrites one that does.
const (
	ClaudeSettingsDir  = ".claude"
	ClaudeSettingsFile = "settings.json"
)

// ErrMateRunning is returned by StartMate when the recorded Mate is still
// live in Herdr.
var ErrMateRunning = errors.New("mate is already running")

func errUsage(msg string) error { return observability.NewError(observability.CodeUsage, msg) }

// StartRequest is one `matev2 mate start`.
type StartRequest struct {
	// Project is the registered project whose Mate is started.
	Project string
	// Harness is the harness kind to launch. Empty means the workspace
	// default for a Mate (Claude Code).
	Harness harness.Kind
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

	// 2. The Mate's directory: the manual, rendered again on every start,
	// and the settings file Claude launches with.
	mateDir := w.MateDir(project)
	sessionID, err := prepareMateDir(w, deps, project, cfg, kind, mateDir)
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
	wsHandle, err := ensureProjectWorkspace(ctx, deps, session, project, mateDir)
	if err != nil {
		return StartResult{}, err
	}
	tab, err := deps.Runtime.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: wsHandle,
		Label:     MateTabLabel,
		Cwd:       mateDir,
	})
	if err != nil {
		return StartResult{}, err
	}

	// From here on every failure must undo the tab and leave no meta.
	result, err := startInTab(ctx, w, deps, project, kind, mateDir, sessionID, session, tab)
	if err != nil {
		compensate(ctx, deps, w, project, session, tab)
		return StartResult{}, err
	}
	result.StaleMeta = staleMeta
	return result, nil
}

// startInTab is everything a failure has to compensate for: the launch spec,
// the agent, its startup screen, the readiness wait and the meta.
func startInTab(ctx context.Context, w *store.Workspace, deps Deps, project string, kind harness.Kind, mateDir, sessionID string, session runtime.SessionHandle, tab runtime.TabHandle) (StartResult, error) {
	launch, err := buildLaunchSpec(ctx, project, kind, mateDir, sessionID)
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
	handle, err := deps.Runtime.StartAgent(ctx, spec)
	if err != nil {
		return StartResult{}, err
	}
	settled, err := settleStartupPrompt(ctx, deps.Runtime, handle, kind, deps.startupPromptTimeout(), deps.sleep())
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
	}
	if err := w.WriteMateMeta(project, meta); err != nil {
		return StartResult{}, err
	}
	return StartResult{
		Project:     project,
		Harness:     kind,
		Agent:       handle.Name,
		Session:     session.Name,
		Workspace:   tab.WorkspaceID,
		Tab:         tab.TabID,
		Pane:        tab.PaneID,
		SessionID:   sessionID,
		StartedAt:   startedAt,
		MateDir:     mateDir,
		Status:      observed.Status,
		TrustDialog: settled.TrustDialogAnswered,
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
func prepareMateDir(w *store.Workspace, deps Deps, project string, cfg store.ProjectConfig, kind harness.Kind, mateDir string) (string, error) {
	if err := os.MkdirAll(mateDir, 0o755); err != nil {
		return "", err
	}
	binary, err := deps.binary()
	if err != nil {
		return "", err
	}
	if err := mateassets.Write(mateDir, mateassets.Params{
		ProjectName:   project,
		WorkspaceRoot: w.Root(),
		ProjectRepo:   w.RepoDir(cfg.Repo),
		DefaultBranch: cfg.DefaultBranch,
		Mode:          cfg.Mode,
		Yolo:          cfg.Yolo,
		Harness:       string(kind),
		WorkspaceDoc:  w.WorkspaceDoc(),
		ProjectDoc:    w.ProjectDoc(project),
		MemoryFile:    w.MemoryFile(project),
		BacklogFile:   w.BacklogFile(project),
		MatevBin:      binary,
	}); err != nil {
		return "", err
	}
	if err := ensureClaudeSettings(mateDir); err != nil {
		return "", err
	}
	if kind == harness.KindCodex {
		// Codex discovers AGENTS.override.md, in preference to a tracked
		// AGENTS.md, at the directory it runs in. The manual is the same
		// text either way; this is the name Codex reads it under.
		if err := writeCodexOverride(mateDir); err != nil {
			return "", err
		}
	}
	if kind == harness.KindClaude {
		if deps.NewSessionID != nil {
			return deps.NewSessionID(), nil
		}
		return uuid.NewString(), nil
	}
	return "", nil
}

// ensureClaudeSettings creates `<mate>/.claude/settings.json` if it is not
// there. An existing file - the user's, or the hooks task 08 writes - is
// never touched.
func ensureClaudeSettings(mateDir string) error {
	dir := filepath.Join(mateDir, ClaudeSettingsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, ClaudeSettingsFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte("{}\n"), 0o644)
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
func buildLaunchSpec(ctx context.Context, project string, kind harness.Kind, mateDir, sessionID string) (harness.LaunchSpec, error) {
	adapter, err := harness.AdapterFor(kind)
	if err != nil {
		return harness.LaunchSpec{}, err
	}
	spec := harness.AgentSpec{
		ID:   AgentNamePrefix + "-" + project,
		Role: harness.RoleMate,
		Kind: kind,
		Cwd:  mateDir,
		// No launch env here: Herdr 0.8.2 applies `--env` when a pane is
		// created, not at `agent start`, so the Mate's identity variables
		// are injected by the workspace create in ensureProjectWorkspace.
		Config: harness.Config{Kind: kind},
	}
	switch kind {
	case harness.KindClaude:
		spec.ContextPath = filepath.Join(mateDir, "AGENTS.md")
		// A fresh session id is what task 10's `--resume` will name, and
		// the settings file is where task 08's hooks go. Claude refuses one
		// without the other.
		spec.ClaudeSessionID = sessionID
		spec.ClaudeSettingsPath = filepath.Join(mateDir, ClaudeSettingsDir, ClaudeSettingsFile)
	case harness.KindCodex:
		spec.ContextPath = harness.CodexInstructionPath(mateDir)
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
	spec.Env = []runtime.EnvVar{
		{Key: config.EnvProjectID, Value: project},
		{Key: config.EnvAgentID, Value: AgentNamePrefix + "-" + project},
		{Key: config.EnvAgentRole, Value: string(harness.RoleMate)},
		{Key: config.EnvRuntimeSessionID, Value: session.Name},
	}
	return deps.Runtime.EnsureProjectWorkspace(ctx, spec)
}

// compensate undoes a start that failed after the tab existed: the agent is
// force-stopped, the tab is closed, and any `mate.meta` is removed, because
// a meta naming a pane nobody owns is worse than none. Its own failures are
// deliberately swallowed: the caller must see why the start was refused.
func compensate(ctx context.Context, deps Deps, w *store.Workspace, project string, session runtime.SessionHandle, tab runtime.TabHandle) {
	name, _ := runtime.SanitizeAgentName(AgentNamePrefix, project)
	if name != "" {
		handle := runtime.AgentHandle{Session: session, Name: name, RawID: project, Tab: tab}
		_ = deps.Runtime.StopAgent(ctx, handle, runtime.StopForce)
	}
	if err := deps.Runtime.RemoveTab(ctx, tab); err != nil && !runtime.IsTabGone(err) {
		_ = err
	}
	deps.Names.Release(session.Name, name)
	_ = os.Remove(w.MateMeta(project))
}
