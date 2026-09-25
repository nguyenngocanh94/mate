package spawn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/mateassets"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Crew naming. A crew's branch, worktree, Herdr tab and live agent name are
// all derived from the crew id, so nothing has to be looked up to find them
// again after a crash.
const (
	// CrewBranchPrefix + id is the branch a crew works on.
	CrewBranchPrefix = "mate/"
	// CrewTabLabelPrefix + id is the Herdr tab label of a crew pane.
	CrewTabLabelPrefix = "crew-"
	// CrewAgentNamePrefix is the first half of a crew's live agent name.
	CrewAgentNamePrefix = "crew"
)

// Meta keys written to `crews/<id>.meta` on top of the ones a Mate shares
// (harness, agent, pane, tab, workspace, session, session_id, transcript,
// started_at, stopped_at).
const (
	// MetaTask is the one-line description of what this crew was spawned for.
	MetaTask = "task"
	// MetaWorktree is the crew's worktree, relative to the workspace root, so
	// a moved workspace does not invalidate every crew record.
	MetaWorktree = "worktree"
	// MetaBranch is the branch the worktree is checked out on.
	MetaBranch = "branch"
	// MetaState is the crew's declared state (mvp.md section 4b). Only the
	// app writes it, and only three values ever land in it: `spawned` at
	// spawn, `finished` or `failed` at `crew stop`, and `failed` when a
	// spawn could not finish. A crew's own `.status` is a different file and
	// a different vocabulary; the two never overwrite each other.
	MetaState = crewstate.MetaState
	// MetaFailedReason is why a spawn failed, one line, written beside
	// `state=failed` so a crew record that never came up still says what
	// happened.
	MetaFailedReason = "failed_reason"
)

// The values MetaState may hold. They are crewstate's, so the file the app
// writes and the table that reads it cannot drift.
const (
	CrewStateSpawned  = string(crewstate.StateSpawned)
	CrewStateFinished = string(crewstate.StateFinished)
	CrewStateFailed   = string(crewstate.StateFailed)
)

// ErrCrewRunning is returned by SpawnCrew when the recorded crew is still
// live in Herdr.
var ErrCrewRunning = errors.New("crew is already running")

// DefaultBriefDeliveryTimeout bounds the wait for the pane to leave idle
// after the brief prompt is sent. It is short on purpose: the answer is
// "did the harness accept the line", not "is the task done", and a crew that
// is still idle after this long is reported as a warning, never as a failure.
const DefaultBriefDeliveryTimeout = 30 * time.Second

// SpawnCrewRequest is one `mate crew spawn`.
type SpawnCrewRequest struct {
	// Project is the registered project the crew works in.
	Project string
	// Crew is the crew id: `[a-z][a-z0-9]{1,15}` (store.ValidateCrewID).
	Crew string
	// Harness is the harness kind to launch. Empty means the workspace
	// default for a Crew (Codex).
	Harness harness.Kind
	// BriefFile is the file holding the task text. It must be readable and
	// inside the workspace. Empty means BriefText is already the text
	// (`--brief -` reads it from stdin).
	BriefFile string
	// BriefText is the task text itself. Ignored when BriefFile is set.
	BriefText string
	// Task is the one line recorded as `task=`. Empty means the first
	// non-empty line of the brief.
	Task string
	// Scout selects the scout shape (manual section 5): the brief must have
	// a `## Deliverable`, and the template asks for a report instead of a
	// commit. The caller says so explicitly rather than the app inferring it
	// from a `## Deliverable` heading, because the mistake the check exists
	// to catch is exactly a scout brief that forgot that section - inferred,
	// it would silently become a ship.
	Scout bool
}

// CrewResult is what a successful spawn established. Every field except the
// three delivery ones is also a line of `crews/<id>.meta`.
type CrewResult struct {
	Project   string
	Crew      string
	Harness   harness.Kind
	Task      string
	Agent     string
	Session   string
	Workspace string
	Tab       string
	Pane      string
	SessionID string
	Branch    string
	// Worktree and BriefPath are absolute; the meta records the worktree
	// relative to the workspace root.
	Worktree   string
	BriefPath  string
	StatusPath string
	StartedAt  time.Time
	// StaleMeta is true when a crew record existed for this id but Herdr no
	// longer had its agent, so this spawn replaced it.
	StaleMeta bool
	// TrustDialog is true when the startup settle answered a directory-trust
	// dialog. v1's ADR 0028 expected this to stay false for a crew, because a
	// linked worktree inherits the primary repo's trust decision - but a live
	// Codex 0.154 crew was measured showing the dialog on 2026-09-17
	// (TestLiveSpawnCrewCodex): Codex confirms trust per absolute path, and a
	// crew worktree is always a path it has never seen. The settle step is
	// therefore load-bearing for crews, not a formality.
	TrustDialog bool
	// UpdateDialog is true when the startup settle skipped the harness's
	// release-update prompt. Codex draws it before the trust dialog on every
	// launch after a new release is published until the operator dismisses
	// that version (measured 2026-09-18), so a crew spawn that could not
	// answer it never reached the composer at all.
	UpdateDialog bool
	// BriefDelivered is true when the pane was observed to leave idle after
	// the brief prompt was sent.
	BriefDelivered bool
	// DeliveryWarning is the honest report of a delivery that could not be
	// confirmed. It is never an error: the crew exists, its worktree exists,
	// and a human or the Mate can re-send the line.
	DeliveryWarning string
	// PaneTail is the screen excerpt behind DeliveryWarning. Empty otherwise.
	PaneTail string
}

// SpawnCrew creates one crew: a git worktree on its own branch, the brief
// the crew is to follow, a Herdr tab in the project's workspace, the harness
// running in it, and `crews/<id>.meta` written last.
//
// A successful spawn records `state=spawned`: the crew exists and has said
// nothing yet (mvp.md section 4b).
//
// Everything created after the worktree exists is compensated on failure:
// the agent is stopped, the tab closed, the worktree removed and the branch
// deleted. `crews/<id>/brief.md` is deliberately kept - it is the evidence
// of what was asked for - and because that directory survives, the meta is
// written too, carrying `state=failed` and `failed_reason=`. A spawn that
// could not come up must not leave a record that reads as a crew about to
// start; `failed` is the honest word, and it is the app's to write (nothing
// else can: the crew never existed to say so).
func SpawnCrew(ctx context.Context, w *store.Workspace, deps Deps, req SpawnCrewRequest) (CrewResult, error) {
	if w == nil {
		return CrewResult{}, errUsage("spawn: a workspace is required")
	}
	if deps.Runtime == nil {
		return CrewResult{}, errUsage("spawn: a runtime adapter is required")
	}
	if deps.Names == nil {
		deps.Names = runtime.NewMemoryNameRegistry()
	}
	project, crew := req.Project, req.Crew
	if err := store.ValidateProjectName(project); err != nil {
		return CrewResult{}, err
	}
	if err := store.ValidateCrewID(crew); err != nil {
		return CrewResult{}, err
	}
	if _, ok := w.Project(project); !ok {
		return CrewResult{}, fmt.Errorf("%w: %s", store.ErrNoProject, project)
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return CrewResult{}, err
	}
	repoCfg, err := cfg.SoleRepo()
	if err != nil {
		return CrewResult{}, observability.WrapError(observability.CodeUsage,
			"crew spawn refused, nothing was created", err)
	}
	harnessKind := req.Harness
	if harnessKind == "" {
		if harnessKind, err = harness.ParseKind(w.Defaults().CrewHarness); err != nil {
			return CrewResult{}, err
		}
	}

	// 1. The recorded crew, re-checked against Herdr, and the brief. Both
	// are refusals: nothing has been created yet.
	staleMeta, err := refuseIfCrewLive(ctx, w, deps, project, crew)
	if err != nil {
		return CrewResult{}, err
	}
	briefText, err := readBriefText(w, req)
	if err != nil {
		return CrewResult{}, err
	}
	// The brief's shape (docs/mvp.md M7), checked before anything exists -
	// worktree, brief.md, pane or meta - so a refused brief leaves no trace
	// at all and the Mate fixes its file and runs the same command again.
	kind := brief.Ship
	if req.Scout {
		kind = brief.Scout
	}
	if problems := brief.Check(briefText, kind); len(problems) > 0 {
		return CrewResult{}, observability.WrapError(observability.CodeUsage,
			"crew spawn refused the brief, nothing was created",
			&brief.Error{Kind: kind, Problems: problems})
	}
	briefText = brief.TaskBody(briefText)
	crewRulesWS, crewRulesProject, err := w.CrewRules(project)
	if err != nil {
		return CrewResult{}, err
	}
	task := oneLineTask(req.Task, briefText)

	repo := w.RepoDir(repoCfg.Path)
	branch := CrewBranchPrefix + crew
	worktree := w.WorktreeDir(project, crew)
	git := deps.git()
	if err := checkWorktreePreconditions(ctx, git, repo, worktree, branch, repoCfg.DefaultBranch); err != nil {
		return CrewResult{}, err
	}

	// 2. The worktree. From here on every failure is compensated.
	if err := os.MkdirAll(w.WorktreesDir(), 0o755); err != nil {
		return CrewResult{}, err
	}
	if err := git.AddWorktree(ctx, repo, worktree, branch, repoCfg.DefaultBranch); err != nil {
		return CrewResult{}, err
	}
	saga := &crewSaga{deps: deps, git: git, repo: repo, worktree: worktree, branch: branch}
	result, err := spawnInWorktree(ctx, w, deps, saga, crewPlan{
		project:          project,
		crew:             crew,
		kind:             harnessKind,
		task:             task,
		brief:            briefText,
		scout:            req.Scout,
		crewRulesWS:      crewRulesWS,
		crewRulesProject: crewRulesProject,
		repo:             repo,
		branch:           branch,
		worktree:         worktree,
		repoCfg:          repoCfg,
	})
	if err != nil {
		saga.compensate(ctx)
		recordFailedSpawn(w, project, crew, harnessKind, task, branch, err)
		return CrewResult{}, err
	}
	result.StaleMeta = staleMeta
	return result, nil
}

// recordFailedSpawn writes `state=failed` over whatever the crew record is,
// once the spawn has failed and been compensated - but only when
// `crews/<id>/` exists, which is exactly the line between "nothing was
// created" and "something was". Before the brief is written a failure is a
// refusal and leaves no trace; after it, the directory survives compensation
// and a record must say why (mvp.md section 4b: no orphan directory left at
// `spawned`).
//
// It keeps only the facts that were settled before the failure. The agent,
// pane and tab are deliberately absent: compensation removed them, and a
// meta naming a pane nobody has is what `mate state` would then have to
// explain away.
//
// Its own failure is swallowed. The caller must see why the spawn was
// refused, not why the bookkeeping afterwards was untidy.
func recordFailedSpawn(w *store.Workspace, project, crew string, kind harness.Kind, task, branch string, cause error) {
	if _, err := os.Stat(w.CrewDir(project, crew)); err != nil {
		return
	}
	_ = w.WriteCrewMeta(project, crew, map[string]string{
		MetaTask:         task,
		MetaHarness:      string(kind),
		MetaBranch:       branch,
		MetaState:        CrewStateFailed,
		MetaFailedReason: oneLineReason(cause),
	})
}

// oneLineReason flattens an error into the single line a `.meta` value is,
// bounded the way `task=` is: the record is a label, and the caller already
// has the error itself.
func oneLineReason(err error) string {
	if err == nil {
		return ""
	}
	reason := strings.Join(strings.Fields(err.Error()), " ")
	if r := []rune(reason); len(r) > maxTaskLine {
		reason = strings.TrimSpace(string(r[:maxTaskLine])) + "..."
	}
	return reason
}

// crewPlan is the settled decision a spawn works from once the worktree
// exists: nothing in it is looked up again.
type crewPlan struct {
	project string
	crew    string
	kind    harness.Kind
	// task is the one line recorded as `task=`; brief is the Mate's
	// `# Task` text, already checked, that the template's `# Task` holds.
	task  string
	brief string
	// scout picks the template's scout shape.
	scout bool
	// crewRulesWS and crewRulesProject are the captain's CREW.md texts.
	crewRulesWS      string
	crewRulesProject string
	repo             string
	branch           string
	worktree         string
	repoCfg          store.RepoConfig
}

// spawnInWorktree is everything a failure has to compensate for: the brief,
// the tab, the agent, the startup settle, the brief delivery and the meta.
func spawnInWorktree(ctx context.Context, w *store.Workspace, deps Deps, saga *crewSaga, plan crewPlan) (CrewResult, error) {
	// The tangle guard, before a single byte is written into the worktree:
	// the new worktree must be its own top level and must not be the
	// project's primary checkout. A `worktree add` that silently landed in
	// the primary repo would let a crew commit on the user's own branch.
	if err := assertIsolatedWorktree(ctx, saga.git, plan.repo, plan.worktree); err != nil {
		return CrewResult{}, err
	}

	// 3. The files. brief.md is written before anything can fail in Herdr so
	// that the compensated worktree still leaves the evidence behind.
	briefPath := w.CrewBrief(plan.project, plan.crew)
	statusPath := w.CrewStatus(plan.project, plan.crew)
	brief, err := renderCrewBrief(w, plan)
	if err != nil {
		return CrewResult{}, err
	}
	if err := writeInside(w, briefPath, brief, 0o644); err != nil {
		return CrewResult{}, err
	}
	if err := createStatusFile(w, statusPath); err != nil {
		return CrewResult{}, err
	}
	sessionID, settingsPath, err := prepareCrewHarnessFiles(ctx, w, deps, saga.git, plan, brief)
	if err != nil {
		return CrewResult{}, err
	}
	launch, err := buildCrewLaunchSpec(ctx, plan, briefPath, sessionID, settingsPath)
	if err != nil {
		return CrewResult{}, err
	}

	// 4. Herdr: the project's own workspace, a tab labelled crew-<id> whose
	// cwd is the worktree and whose environment carries MATE_STATUS.
	sessionSpec, err := deps.sessionSpec(w)
	if err != nil {
		return CrewResult{}, err
	}
	session, err := deps.Runtime.EnsureSession(ctx, sessionSpec)
	if err != nil {
		return CrewResult{}, err
	}
	saga.session = session
	mateDir := w.MateDir(plan.project)
	if err := os.MkdirAll(mateDir, 0o755); err != nil {
		return CrewResult{}, err
	}
	wsHandle, err := ensureProjectWorkspace(ctx, deps, session, plan.project, mateDir)
	if err != nil {
		return CrewResult{}, err
	}
	tab, err := deps.Runtime.CreateAgentTab(ctx, runtime.TabSpec{
		Workspace: wsHandle,
		Label:     CrewTabLabelPrefix + plan.crew,
		Cwd:       plan.worktree,
		Env:       crewPaneEnv(plan, session, statusPath),
	})
	if err != nil {
		return CrewResult{}, err
	}
	saga.tab, saga.madeTab = tab, true

	reservation, err := runtime.AllocateAgentName(deps.Names, session.Name, CrewAgentNamePrefix, plan.crew, runtime.FailOnCollision)
	if err != nil {
		return CrewResult{}, err
	}
	saga.agentName = reservation.Name()
	spec, err := runtime.NewAgentStartSpec(tab, reservation, launch, deps.startTimeout())
	if err != nil {
		return CrewResult{}, err
	}
	launchedAt := deps.now()
	handle, err := deps.Runtime.StartAgent(ctx, spec)
	if err != nil {
		return CrewResult{}, err
	}
	saga.startedAgent = true
	settled, err := settleStartupPrompt(ctx, deps.Runtime, handle, plan.kind, deps.startupPromptTimeout(), deps.sleep())
	if err != nil {
		return CrewResult{}, err
	}
	observed, err := deps.Runtime.WaitAgent(ctx, handle, runtime.DefaultWait(deps.readinessTimeout()))
	if err != nil {
		return CrewResult{}, err
	}
	if readiness := runtime.ClassifyObservation(observed); readiness.Kind == runtime.ReadinessFailed {
		return CrewResult{}, readiness.Err
	}

	// The brief is delivered as a pointer, not as a paste: a multi-line
	// document typed into a TUI composer is fragile (a blank line submits,
	// a leading slash opens a command popup), and the file is already on
	// disk where the harness can read it whole.
	delivered, warning, tail, err := deliverBrief(ctx, deps, handle, briefPath, plan.kind)
	if err != nil {
		return CrewResult{}, err
	}

	startedAt := deps.now()
	relWorktree, err := filepath.Rel(w.Root(), plan.worktree)
	if err != nil {
		return CrewResult{}, err
	}
	meta := map[string]string{
		MetaTask:       plan.task,
		MetaHarness:    string(plan.kind),
		MetaAgent:      handle.Name,
		MetaPane:       tab.PaneID,
		MetaTab:        tab.TabID,
		MetaWorkspace:  tab.WorkspaceID,
		MetaSession:    session.Name,
		MetaWorktree:   filepath.ToSlash(relWorktree),
		MetaBranch:     plan.branch,
		store.MetaRepo: plan.repoCfg.Name,
		MetaSessionID:  sessionID,
		MetaTranscript: "",
		MetaStartedAt:  startedAt.Format(time.RFC3339),
		MetaLaunchedAt: launchedAt.Format(time.RFC3339),
		// The crew exists and has written nothing yet. It is not an
		// override: the moment the crew appends its first `working:` line
		// that verb is what the state resolves to (mvp.md section 4b).
		MetaState: CrewStateSpawned,
	}
	if err := w.WriteCrewMeta(plan.project, plan.crew, meta); err != nil {
		return CrewResult{}, err
	}
	return CrewResult{
		Project:         plan.project,
		Crew:            plan.crew,
		Harness:         plan.kind,
		Task:            plan.task,
		Agent:           handle.Name,
		Session:         session.Name,
		Workspace:       tab.WorkspaceID,
		Tab:             tab.TabID,
		Pane:            tab.PaneID,
		SessionID:       sessionID,
		Branch:          plan.branch,
		Worktree:        plan.worktree,
		BriefPath:       briefPath,
		StatusPath:      statusPath,
		StartedAt:       startedAt,
		TrustDialog:     settled.TrustDialogAnswered,
		UpdateDialog:    settled.UpdateDialogAnswered,
		BriefDelivered:  delivered,
		DeliveryWarning: warning,
		PaneTail:        tail,
	}, nil
}

// crewSaga records what a spawn has created so a failure can undo it in
// reverse order.
type crewSaga struct {
	deps     Deps
	git      gitx.Git
	repo     string
	worktree string
	branch   string

	session      runtime.SessionHandle
	tab          runtime.TabHandle
	madeTab      bool
	agentName    string
	startedAgent bool
}

// compensate undoes a spawn that failed after the worktree existed. Its own
// failures are swallowed: the caller must see why the spawn was refused, not
// why the cleanup was untidy. `crews/<id>/brief.md` is left in place.
func (s *crewSaga) compensate(ctx context.Context) {
	if s.startedAgent && s.agentName != "" {
		handle := runtime.AgentHandle{Session: s.session, Name: s.agentName, Tab: s.tab}
		_ = s.deps.Runtime.StopAgent(ctx, handle, runtime.StopForce)
	}
	if s.madeTab {
		if err := s.deps.Runtime.RemoveTab(ctx, s.tab); err != nil && !runtime.IsTabGone(err) {
			_ = err
		}
	}
	if s.agentName != "" && s.session.Name != "" {
		s.deps.Names.Release(s.session.Name, s.agentName)
	}
	// The worktree is removed before the branch: git refuses to delete a
	// branch that a worktree still has checked out.
	_ = s.git.RemoveWorktree(ctx, s.repo, s.worktree)
	_ = os.RemoveAll(s.worktree)
	_ = s.git.DeleteBranch(ctx, s.repo, s.branch)
}

// refuseIfCrewLive re-asks Herdr about the agent name in `crews/<id>.meta`,
// exactly as StartMate does for a Mate. It returns true when the meta named
// an agent Herdr does not have - a stale record this spawn may replace - and
// an error when the crew really is running. A stale record is not a licence
// to reuse the id blindly: the branch and worktree checks below still refuse
// whatever the previous crew left behind.
func refuseIfCrewLive(ctx context.Context, w *store.Workspace, deps Deps, project, crew string) (stale bool, err error) {
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return false, err
	}
	name := meta[MetaAgent]
	if name == "" {
		return len(meta) > 0, nil
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
		fmt.Sprintf("%v: crew %s of project %s is served by agent %q in session %s; stop it first",
			ErrCrewRunning, crew, project, name, session.Name)).
		WithDetails(map[string]any{"agent_name": name, "herdr_session": session.Name})
}

// readBriefText loads the Mate's task text, which the template's `# Task`
// holds. A file is refused unless it resolves inside the workspace:
// `crew spawn` is an agent-facing command, and a brief is the one argument
// that names an arbitrary path.
func readBriefText(w *store.Workspace, req SpawnCrewRequest) (string, error) {
	text := req.BriefText
	if path := strings.TrimSpace(req.BriefFile); path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		if _, err := w.Resolve(abs); err != nil {
			return "", observability.WrapError(observability.CodeUsage,
				fmt.Sprintf("brief %s is outside the workspace %s", path, w.Root()), err)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", observability.WrapError(observability.CodeUsage,
				fmt.Sprintf("brief %s is not readable", path), err)
		}
		text = string(data)
	}
	if strings.TrimSpace(text) == "" {
		return "", errUsage("crew spawn: the brief is empty; --brief <file> or --brief - (stdin) must carry the task")
	}
	return strings.TrimRight(text, "\n"), nil
}

// oneLineTask is what `task=` records: the caller's --task when given, else
// the first line of the brief's `## Captain's words` - every brief now opens
// with that heading, so "the brief's first line" would label every crew
// with the same heading. A meta value is one line by definition, so it is
// flattened and bounded rather than refused, and bounded by runes: the
// captain's words are often Vietnamese, and a byte cut splits a letter.
func oneLineTask(task, briefText string) string {
	candidate := strings.TrimSpace(task)
	if candidate == "" {
		candidate = brief.CaptainsFirstLine(briefText)
	}
	candidate = strings.Join(strings.Fields(candidate), " ")
	if r := []rune(candidate); len(r) > maxTaskLine {
		candidate = strings.TrimSpace(string(r[:maxTaskLine])) + "..."
	}
	return candidate
}

// maxTaskLine bounds `task=`: it is a label in a list, not the brief.
const maxTaskLine = 160

// ReadBriefStdin reads a brief from r for `--brief -`. It is here rather
// than in the CLI so the size limit is stated once.
func ReadBriefStdin(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// checkWorktreePreconditions refuses before anything is created: the repo
// must be a git working tree, the branch it is to be based on must exist,
// the crew branch must not, and the worktree path must be free.
func checkWorktreePreconditions(ctx context.Context, git gitx.Git, repo, worktree, branch, base string) error {
	isRepo, err := git.IsRepo(ctx, repo)
	if err != nil {
		return err
	}
	if !isRepo {
		return observability.NewError(observability.CodeUsage,
			fmt.Sprintf("%s is not a git working tree; a crew needs one to branch from", repo))
	}
	baseExists, err := git.RevisionExists(ctx, repo, base)
	if err != nil {
		return err
	}
	if !baseExists {
		return observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("%s has no commit on %s to branch from", repo, base))
	}
	branchExists, err := git.BranchExists(ctx, repo, branch)
	if err != nil {
		return err
	}
	if branchExists {
		return observability.NewError(observability.CodeAlreadyExists,
			fmt.Sprintf("branch %s already exists in %s; a crew always starts on a new branch", branch, repo)).
			WithDetails(map[string]any{"branch": branch, "repo": repo})
	}
	if _, err := os.Lstat(worktree); err == nil {
		return observability.NewError(observability.CodeAlreadyExists,
			fmt.Sprintf("worktree path %s already exists", worktree)).
			WithDetails(map[string]any{"worktree": worktree})
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

// assertIsolatedWorktree is v1's tangle guard: after `worktree add`, the new
// directory must be its own git top level and must not be the primary
// checkout. git reports a symlink-resolved path (macOS /tmp is /private/tmp),
// so the comparison resolves both sides.
func assertIsolatedWorktree(ctx context.Context, git gitx.Git, repo, worktree string) error {
	top, err := git.Toplevel(ctx, worktree)
	if err != nil {
		return err
	}
	primary, err := git.Toplevel(ctx, repo)
	if err != nil {
		return err
	}
	// The primary-checkout case is checked first because it is the specific
	// disaster: a crew committing on the user's own branch. "Not its own
	// working tree" is the catch-all behind it.
	if gitx.SamePath(top, primary) {
		return observability.NewError(observability.CodeNeedsRepair,
			fmt.Sprintf("worktree %s resolves to the project's primary checkout %s; refusing to let a crew commit there", worktree, primary)).
			WithDetails(map[string]any{"worktree": worktree, "primary": primary})
	}
	if !gitx.SamePath(top, worktree) {
		return observability.NewError(observability.CodeNeedsRepair,
			fmt.Sprintf("worktree %s reports top level %s; refusing to run a crew in a directory that is not its own working tree", worktree, top)).
			WithDetails(map[string]any{"worktree": worktree, "toplevel": top})
	}
	return nil
}

// renderCrewBrief fills the embedded template with this crew's task, paths
// and the captain's standing crew rules.
func renderCrewBrief(w *store.Workspace, plan crewPlan) ([]byte, error) {
	return mateassets.RenderBrief(mateassets.BriefParams{
		Task:               plan.brief,
		Scout:              plan.scout,
		RepoPath:           plan.repo,
		WorktreePath:       plan.worktree,
		Branch:             plan.branch,
		DefaultBranch:      plan.repoCfg.DefaultBranch,
		BriefPath:          w.CrewBrief(plan.project, plan.crew),
		ReportPath:         w.CrewReport(plan.project, plan.crew),
		HandbackPath:       w.CrewHandback(plan.project, plan.crew),
		WorkspaceCrewRules: plan.crewRulesWS,
		ProjectCrewRules:   plan.crewRulesProject,
	})
}

// prepareCrewHarnessFiles writes whatever the chosen harness needs beside
// the brief and returns the harness session id and settings path for the
// launch spec.
//
// Codex reads AGENTS.override.md at its cwd and nothing else, so the brief
// is copied into the worktree under that name and excluded locally, which is
// why the crew is told to read the brief by absolute path rather than to
// trust whatever it found in its cwd. Claude takes the brief itself as
// --append-system-prompt-file, so nothing lands in the worktree.
func prepareCrewHarnessFiles(ctx context.Context, w *store.Workspace, deps Deps, git gitx.Git, plan crewPlan, brief []byte) (sessionID, settingsPath string, err error) {
	switch plan.kind {
	case harness.KindCodex:
		override := harness.CodexInstructionPath(plan.worktree)
		if err := os.WriteFile(override, brief, 0o644); err != nil {
			return "", "", err
		}
		// The discovery file is mate's, not the crew's work: excluding it
		// locally keeps a `git add -A` from committing it onto the branch the
		// Mate will review and fast-forward.
		if err := excludeGeneratedFile(ctx, git, plan.worktree, filepath.Base(override)); err != nil {
			return "", "", err
		}
		return "", "", nil
	case harness.KindClaude:
		// A Claude launch can only carry a session id together with a
		// settings file, and decision 9 wants session_id= recorded from the
		// first day, so the crew gets a settings file of its own. It wires
		// no hooks (the Mate's hooks are the Mate's) and turns auto-memory
		// off (CrewClaudeSettings).
		settingsPath = filepath.Join(w.CrewDir(plan.project, plan.crew), ClaudeSettingsFile)
		if err := writeInside(w, settingsPath, CrewClaudeSettings(), 0o644); err != nil {
			return "", "", err
		}
		return newSessionID(deps.NewSessionID), settingsPath, nil
	}
	return "", "", nil
}

// excludeGeneratedFile appends an anchored literal name to the working
// tree's local `info/exclude`, once. git shares that file across a repo's
// worktrees, which is why the rule names only this one generated file and is
// never written twice. No tracked ignore file and no global config is
// touched, and a pre-existing file of that name is never hidden - the caller
// has just written it.
func excludeGeneratedFile(ctx context.Context, git gitx.Git, worktree, name string) error {
	if name == "" || strings.ContainsAny(name, "/\\\r\n*?[]!# ") {
		return observability.NewError(observability.CodeUsage,
			fmt.Sprintf("generated context name %q must be a literal filename", name))
	}
	path, err := git.GitPath(ctx, worktree, "info/exclude")
	if err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	rule := "/" + name
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == rule {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(f, "\n# mate generated crew context (local, never committed)\n%s\n", rule)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// buildCrewLaunchSpec asks the harness adapter for the argv. The cwd is the
// worktree; the context file is the brief (Claude) or the copy of it Codex
// discovers in that cwd.
func buildCrewLaunchSpec(ctx context.Context, plan crewPlan, briefPath, sessionID, settingsPath string) (harness.LaunchSpec, error) {
	adapter, err := harness.AdapterFor(plan.kind)
	if err != nil {
		return harness.LaunchSpec{}, err
	}
	spec := harness.AgentSpec{
		ID:   CrewAgentNamePrefix + "-" + plan.crew,
		Role: harness.RoleCrew,
		Kind: plan.kind,
		Cwd:  plan.worktree,
		// No launch env: Herdr 0.8.2 applies `--env` when a pane is created,
		// so the crew's identity (and MATE_STATUS) is injected by the tab
		// create above.
		Config: harness.Config{Kind: plan.kind},
	}
	switch plan.kind {
	case harness.KindClaude:
		spec.ContextPath = briefPath
		spec.ClaudeSessionID = sessionID
		spec.ClaudeSettingsPath = settingsPath
	case harness.KindCodex:
		spec.ContextPath = harness.CodexInstructionPath(plan.worktree)
	}
	return adapter.BuildLaunchSpec(ctx, spec)
}

// crewPaneEnv is the crew's identity, injected when its pane is created.
// MATE_STATUS is what the brief's `echo ... >> $MATE_STATUS` resolves to;
// without it every status line a crew reported would go nowhere.
func crewPaneEnv(plan crewPlan, session runtime.SessionHandle, statusPath string) []runtime.EnvVar {
	return []runtime.EnvVar{
		{Key: config.EnvProjectID, Value: plan.project},
		{Key: config.EnvAgentID, Value: CrewAgentNamePrefix + "-" + plan.crew},
		{Key: config.EnvAgentRole, Value: string(harness.RoleCrew)},
		{Key: config.EnvCrewID, Value: plan.crew},
		{Key: config.EnvRuntimeSessionID, Value: session.Name},
		{Key: config.EnvStatusFile, Value: statusPath},
		// Who is typing, for the commands that answer differently to the
		// three of them (docs/mvp.md task 22): a crew may never merge its
		// own branch, and the refusal has to name the crew rather than
		// guess from an absent variable.
		{Key: config.EnvCaller, Value: CallerCrew},
	}
}

// BriefPrompt is the single line sent to a freshly started crew. Sending the
// brief's own text would mean pasting a multi-line document into a TUI
// composer, where a blank line submits early and a leading `#` or `/` is a
// command; one line naming the file is delivered whole or not at all.
func BriefPrompt(briefPath string) string {
	return fmt.Sprintf("Read and follow the brief at %s. Start now.", briefPath)
}

// deliverBrief sends the brief pointer and then checks the pane left idle.
// A pane that stayed idle is reported as a warning with its screen tail, not
// as a failure: the crew, its worktree and its meta are all real, and the
// line can be re-sent.
func deliverBrief(ctx context.Context, deps Deps, handle runtime.AgentHandle, briefPath string, kind harness.Kind) (delivered bool, warning, tail string, err error) {
	if err := deps.Runtime.PromptAgent(ctx, handle, BriefPrompt(briefPath)); err != nil {
		return false, "", "", err
	}
	observed, waitErr := deps.Runtime.WaitAgent(ctx, handle, runtime.WaitCondition{
		Until:   []runtime.AgentStatus{runtime.AgentWorking, runtime.AgentDone},
		Timeout: deps.briefDeliveryTimeout(),
	})
	if waitErr == nil {
		return true, "", "", nil
	}
	screen, readErr := deps.Runtime.ReadAgent(ctx, handle, startupScreenLines)
	if readErr != nil {
		screen = "(pane not readable: " + readErr.Error() + ")"
	}
	tail = harness.StartupScreenTail(screen, startupErrorTailLines)
	warning = fmt.Sprintf("brief may not have been delivered: after prompting %s the %s pane was still %s after %s",
		handle.Name, kind, observed.Status, deps.briefDeliveryTimeout().Round(time.Millisecond))
	return false, warning, tail, nil
}

// createStatusFile creates `crews/<id>.status` empty. An existing file is
// left alone: it is append-only history.
func createStatusFile(w *store.Workspace, path string) error {
	if _, err := w.Resolve(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// writeInside writes a file after proving the path lands inside the
// workspace.
func writeInside(w *store.Workspace, path string, data []byte, perm os.FileMode) error {
	if _, err := w.Resolve(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

// newSessionID mints a Claude session uuid. The Deps hook keeps unit tests
// deterministic.
func newSessionID(fn func() string) string {
	if fn != nil {
		return fn()
	}
	return uuid.NewString()
}
