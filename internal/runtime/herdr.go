package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/process"
)

// Herdr is the live RuntimeAdapter. It talks to the herdr executable through
// a ProcessRunner and never imports application or spike/g1.
//
// EnsureSession binds a named session. Headless Herdr 0.8.2 starts with zero
// workspaces (live-captured); EnsureProjectWorkspace lists, matches label+cwd
// (labels are not unique), and CreateAgentTab renames the workspace-create
// root tab for Mate. There is no `herdr agent stop`.
type Herdr struct {
	Runner process.Runner
	// Binary is the herdr executable; empty means "herdr" on PATH.
	Binary string
	// Names is the live-name registry used to re-reserve at start. Required
	// for StartAgent. A reservation proves allocation, not current ownership.
	Names LiveNameRegistry
	Clock Clock
	// StartServer starts a named headless `herdr server --session`. Herdr
	// 0.8.2 does not daemonize (detached_server_daemon: false). Nil uses
	// the default starter, which is process-start + Wait in a goroutine.
	// Lab isolation owns provision; tests inject a stub.
	StartServer func(ctx context.Context, session string) error
	// Attach, when set, is the TTY handoff for AttachAgent (TTYHandoff.Attach
	// in production). Nil means this adapter resolves the attach argv and
	// then refuses, rather than pretending it attached.
	Attach func(ctx context.Context, argv []string) error
	// PaneShellBudget bounds the best-effort wait for a new pane to draw a
	// shell title. Zero means paneShellBudget. Tests shorten it; it is a
	// budget, not a gate - see waitPaneShell.
	PaneShellBudget time.Duration
	// AttachPreflight, when set, runs before AttachAgent probes Herdr. It is
	// where the caller checks preconditions no probe can establish - in
	// production, that its own stdio is a terminal - so a caller that cannot
	// use the answers does not ask the questions.
	AttachPreflight func(ctx context.Context) error
}

// paneShellBudget is how long a freshly created pane is given to draw a shell
// title before the caller proceeds anyway. paneBusyAttempts/paneBusyRetry are
// StartAgent's retry of Herdr's own agent_pane_busy, which is the
// authoritative "this pane cannot host an agent yet" (unchanged since G4-04).
const (
	paneShellBudget  = 5 * time.Second
	paneBusyAttempts = 25
	paneBusyRetry    = 100 * time.Millisecond
)

// NewHerdr returns a Herdr adapter over runner.
func NewHerdr(runner process.Runner) *Herdr {
	return &Herdr{Runner: runner}
}

var (
	_ Adapter = (*Herdr)(nil)
	_ Adapter = (*Fake)(nil)
)

func (h *Herdr) binary() string {
	if h == nil || h.Binary == "" {
		return "herdr"
	}
	return h.Binary
}

func (h *Herdr) paneShellBudget() time.Duration {
	if h != nil && h.PaneShellBudget > 0 {
		return h.PaneShellBudget
	}
	return paneShellBudget
}

func (h *Herdr) now() time.Time {
	if h != nil && h.Clock != nil {
		return h.Clock.Now()
	}
	return time.Now().UTC()
}

// WithSession inserts `--session NAME` in the option region (before `--`).
// Extra harness args after `--` swallow a trailing --session; G1 observed
// that as agent_pane_not_found. If args already carry --session before `--`,
// they are returned unchanged so AgentStartCommand.Argv is not doubled.
func WithSession(session string, args []string) []string {
	if strings.TrimSpace(session) == "" {
		return append([]string(nil), args...)
	}
	if SessionBeforeTerminator(args) {
		return append([]string(nil), args...)
	}
	out := make([]string, 0, len(args)+2)
	inserted := false
	for _, a := range args {
		if a == "--" && !inserted {
			out = append(out, "--session", session)
			inserted = true
		}
		out = append(out, a)
	}
	if !inserted {
		out = append(out, "--session", session)
	}
	return out
}

func (h *Herdr) run(ctx context.Context, session string, args []string) (process.Result, error) {
	if h == nil || h.Runner == nil {
		return process.Result{}, observability.NewError(observability.CodeUnknown, "herdr: process runner is required")
	}
	if strings.TrimSpace(session) == "" {
		return process.Result{}, observability.NewError(observability.CodeUsage, "herdr: named session is required")
	}
	res, err := h.Runner.Run(ctx, process.Spec{Name: h.binary(), Args: WithSession(session, args)})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return process.Result{}, err
		}
		return process.Result{}, observability.WrapError(observability.CodeRuntimeUnavailable, "herdr executable failed to run", err)
	}
	if res.ExitCode != 0 {
		return res, mapProcessFailure(res.ExitCode, res.Stdout, res.Stderr)
	}
	return res, nil
}

// EnsureSession implements Adapter. The default session is never an app
// Workspace. A named session that is not running is started through
// StartServer; Herdr 0.8.2 does not daemonize, so that start is a long-lived
// process, not ProcessRunner.Run.
func (h *Herdr) EnsureSession(ctx context.Context, spec SessionSpec) (SessionHandle, error) {
	handle, err := ResolveSession(spec)
	if err != nil {
		return SessionHandle{}, err
	}
	if err := claimSessionOwner(handle.ConfigHome, handle.Name, spec.WorkspaceID.String()); err != nil {
		return SessionHandle{}, err
	}
	listed, err := h.lookupSession(ctx, handle.Name)
	if err != nil {
		return SessionHandle{}, err
	}
	if listed != nil {
		if listed.Default {
			return SessionHandle{}, fmt.Errorf("session: refusing to bind an app Workspace to the Herdr default session")
		}
		if listed.SocketPath != "" {
			handle.SocketPath = listed.SocketPath
		}
		if listed.Running {
			return handle, nil
		}
	}
	if err := h.startNamedServer(ctx, handle.Name); err != nil {
		return SessionHandle{}, err
	}
	if err := h.waitRunning(ctx, handle.Name); err != nil {
		return SessionHandle{}, err
	}
	return handle, nil
}

// LookupSession implements Adapter. It never starts a server and never
// writes the owner marker.
func (h *Herdr) LookupSession(ctx context.Context, spec SessionSpec) (SessionHandle, bool, error) {
	handle, err := sessionAddress(spec)
	if err != nil {
		return SessionHandle{}, false, err
	}
	listed, err := h.lookupSession(ctx, handle.Name)
	if err != nil {
		return SessionHandle{}, false, err
	}
	if listed == nil || !listed.Running {
		return handle, false, nil
	}
	if listed.Default {
		return SessionHandle{}, false, fmt.Errorf("session: refusing to bind an app Workspace to the Herdr default session")
	}
	if listed.SocketPath != "" {
		handle.SocketPath = listed.SocketPath
	}
	return handle, true, nil
}

func (h *Herdr) lookupSession(ctx context.Context, name string) (*sessionInfo, error) {
	res, err := h.run(ctx, name, []string{"session", "list", "--json"})
	if err != nil {
		return nil, err
	}
	list, err := parseSessionList(res.Stdout)
	if err != nil {
		return nil, observability.WrapError(observability.CodeUnknown, "herdr session list", err)
	}
	for i := range list {
		if list[i].Name == name {
			return &list[i], nil
		}
	}
	return nil, nil
}

func (h *Herdr) startNamedServer(ctx context.Context, session string) error {
	if h.StartServer != nil {
		return h.StartServer(ctx, session)
	}
	return startHerdrServer(h.binary(), session)
}

func startHerdrServer(binary, session string) error {
	if err := refuseUndeliverableHerdrSocket(session); err != nil {
		return err
	}
	cmd := herdrServerCommand(binary, session)
	if err := cmd.Start(); err != nil {
		return observability.WrapError(observability.CodeRuntimeUnavailable, "herdr server failed to start", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func refuseUndeliverableHerdrSocket(session string) error {
	home := herdrConfigHomeFromEnv()
	if home == "" {
		return nil
	}
	p, err := namedSocketPath(home, session)
	if err != nil {
		return err
	}
	return checkUnixSocketPath(p)
}

func herdrConfigHomeFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("HERDR_CONFIG_PATH")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		return v
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".config")
	}
	return ""
}

func herdrServerCommand(binary, session string) *exec.Cmd {
	cmd := exec.Command(binary, "server", "--session", session)
	cmd.Env = herdrServerEnv()
	applyHerdrServerProcAttr(cmd)
	return cmd
}

func applyHerdrServerProcAttr(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// herdrDefaultRootTabLabel is the label Herdr 0.8.2 assigns to the tab
// created with workspace create (live-captured; not the workspace label).
const herdrDefaultRootTabLabel = "1"

func sessionOwnerFile(configHome, session string) (string, error) {
	if err := checkSessionName(session); err != nil {
		return "", err
	}
	base := filepath.Join(configHome, "mate", "session-owners")
	p := filepath.Clean(filepath.Join(base, session))
	rel, err := filepath.Rel(base, p)
	if err != nil || rel != session {
		return "", fmt.Errorf("session owner path escapes mate/session-owners")
	}
	return p, nil
}

// claimSessionOwner records which workspace id owns a Herdr session name in
// the config home, with O_EXCL so two processes cannot silently share the
// address. persistence.NameRegistry refuses this scope (ADR 0004); this file
// is the cross-workspace record. Same-owner re-claim is idempotent.
func claimSessionOwner(configHome, session, workspaceID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return observability.NewError(observability.CodeUsage, "session owner marker requires a workspace id")
	}
	if strings.TrimSpace(configHome) == "" {
		return observability.NewError(observability.CodeUsage, "session owner marker requires config home")
	}
	p, err := sessionOwnerFile(configHome, session)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return observability.WrapError(observability.CodePermission, "session owner directory", err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		_, werr := f.WriteString(workspaceID + "\n")
		cerr := f.Close()
		if werr != nil {
			return observability.WrapError(observability.CodeUnknown, "session owner marker", werr)
		}
		if cerr != nil {
			return observability.WrapError(observability.CodeUnknown, "session owner marker", cerr)
		}
		return nil
	}
	if !errors.Is(err, os.ErrExist) {
		return observability.WrapError(observability.CodeUnknown, "session owner marker", err)
	}
	raw, rerr := os.ReadFile(p)
	if rerr != nil {
		return observability.WrapError(observability.CodeUnknown, "session owner marker", rerr)
	}
	if strings.TrimSpace(string(raw)) == workspaceID {
		return nil
	}
	return observability.NewError(
		observability.CodeAlreadyExists,
		fmt.Sprintf("herdr session %q is owned by workspace %s, not %s", session, strings.TrimSpace(string(raw)), workspaceID),
	)
}

// herdrServerEnv is the environment for a herdr server mate itself
// cold-starts (EnsureSession's startNamedServer, when no session is already
// running). ADR 0007 (G4-04) originally allowlisted a small, fixed set of
// keys "so operator MATEV2_* / secrets are not inherited" and explicitly
// flagged "whether Herdr needs env keys beyond the server allowlist" as
// unproven.
//
// It is now proven, live, that Herdr does: a herdr server spawned with that
// allowlist forks the login shell for every new pane with the SAME
// restricted environment, and an operator's shell profile can depend on
// variables the allowlist has no way to anticipate (on the machine this was
// diagnosed on, Amazon Q's shell integration - Q_TERM/QTERM_SESSION_ID/
// Q_SET_PARENT_CHECK - whose absence made every freshly created pane's shell
// behave in a way Herdr's own `agent start` permanently refuses as
// agent_pane_busy: not a transient race - proven not to clear after 20s of
// extra warm-up, and not to clear on a second pane in the same session
// either, only on the presence of those specific variables). This defect
// hits the very first `mate start`/onboard against any brand-new workspace,
// because that is exactly when EnsureSession must cold-start a server rather
// than bind one already running (see internal/orchestration TestStartMate*
// and the live evidence in AGENTS.md).
//
// Enumerating every shell integration tool an operator might have installed
// is not viable, so the fix is directional rather than another fixed list:
// inherit the calling process's own environment (which is already just the
// operator's login shell environment - mate itself runs under it) and strip
// only mate's own MATEV2_* namespace, the one thing ADR 0007 named as
// deliberately not ambient here (it is injected explicitly, per pane, via
// `--env` at tab/workspace create - runtime.AllowlistedEnv - not via the
// server's own startup environment).
func herdrServerEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if refusedServerEnvKey(key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// refusedServerEnvKey reports whether a caller environment variable must not
// reach the spawned server. Prefix-matching MATEV2_ rather than listing
// config.IdentityEnvKeys is deliberate: a key added later is refused by
// default, and the failure of a new key leaking is worse than the failure of
// an unrelated MATEV2_-prefixed one being dropped.
// harness.NestedSessionEnv is refused for the same reason: the server hands
// its environment to every pane, and an agent that starts the server must not
// make every Mate and Crew look like its own child session.
func refusedServerEnvKey(key string) bool {
	return strings.HasPrefix(key, "MATEV2_") || key == "HERDR_SESSION" || harness.IsNestedSessionEnv(key)
}

func (h *Herdr) waitRunning(ctx context.Context, session string) error {
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for {
		res, err := h.run(ctx, session, []string{"status", "--json"})
		if err == nil {
			st, parseErr := parseStatus(res.Stdout)
			if parseErr == nil && st.Running {
				return nil
			}
			if parseErr != nil {
				lastErr = parseErr
			} else {
				lastErr = observability.NewError(
					observability.CodeRuntimeUnavailable,
					fmt.Sprintf("named herdr session %q is listed but not running", session),
				)
			}
		} else if ctx.Err() == nil || lastErr == nil {
			lastErr = err
		}
		timedOut := time.Now().After(deadline) || ctx.Err() != nil
		if timedOut {
			return waitRunningTimeout(session, lastErr, ctx.Err())
		}
		select {
		case <-ctx.Done():
			return waitRunningTimeout(session, lastErr, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitRunningTimeout(session string, lastErr, ctxErr error) error {
	msg := fmt.Sprintf("named herdr session %q did not become running", session)
	if lastErr != nil {
		return observability.WrapError(observability.CodeRuntimeUnavailable, msg, lastErr)
	}
	if ctxErr != nil {
		return ctxErr
	}
	return observability.NewError(observability.CodeRuntimeUnavailable, msg)
}

// EnsureProjectWorkspace implements Adapter. Headless Herdr starts with zero
// workspaces; this never assumes a default layout. Labels are not unique
// (live: two --label Duplicate creates produced w1 and w2 at different cwds),
// so reuse requires label and the root-tab cwd (not the focused/active tab).
func (h *Herdr) EnsureProjectWorkspace(ctx context.Context, spec WorkspaceSpec) (WorkspaceHandle, error) {
	if err := CheckWorkspaceSpec(spec); err != nil {
		return WorkspaceHandle{}, err
	}
	env, err := AllowlistedEnv(spec.Env)
	if err != nil {
		return WorkspaceHandle{}, err
	}
	found, ok, err := h.LookupProjectWorkspace(ctx, spec)
	if err != nil {
		return WorkspaceHandle{}, err
	}
	if ok {
		if len(env) > 0 {
			return WorkspaceHandle{}, observability.NewError(
				observability.CodeUsage,
				"launch environment is injected only when the workspace pane is created (workspace create --env); this workspace already exists",
			)
		}
		h.collapseEmptyProjectWorkspaces(ctx, spec, found.WorkspaceID)
		return found, nil
	}
	args := []string{"workspace", "create", "--label", spec.Label, "--cwd", spec.Cwd, "--no-focus"}
	args = append(args, envFlags(env)...)
	res, err := h.run(ctx, spec.Session.Name, args)
	if err != nil {
		return WorkspaceHandle{}, err
	}
	created, err := parseWorkspaceCreated(res.Stdout)
	if err != nil {
		return WorkspaceHandle{}, observability.WrapError(observability.CodeUnknown, "herdr workspace create", err)
	}
	createdHandle := WorkspaceHandle{
		Session:     spec.Session,
		WorkspaceID: created.WorkspaceID,
		Label:       created.Label,
		Cwd:         created.Cwd,
		RootTab: TabHandle{
			Session: spec.Session, WorkspaceID: created.WorkspaceID,
			TabID: created.TabID, PaneID: created.PaneID, TerminalID: created.TerminalID,
			Cwd: created.Cwd, Label: created.TabLabel, Env: env,
		},
	}
	// Re-list: a concurrent ensure may have created another workspace with
	// the same label+cwd. Adopt one deterministically and close empty extras
	// so the Project is startable again without a human closing workspaces.
	// Nothing here closes on a pane heuristic: only an unoccupied duplicate
	// of this same label+cwd goes, and only once one has been adopted.
	found, ok, err = h.LookupProjectWorkspace(ctx, spec)
	if err != nil {
		return WorkspaceHandle{}, err
	}
	if !ok {
		return createdHandle, nil
	}
	h.collapseEmptyProjectWorkspaces(ctx, spec, found.WorkspaceID)
	return found, nil
}

// LookupProjectWorkspace implements Adapter. It never creates.
func (h *Herdr) LookupProjectWorkspace(ctx context.Context, spec WorkspaceSpec) (WorkspaceHandle, bool, error) {
	if err := CheckWorkspaceSpec(spec); err != nil {
		return WorkspaceHandle{}, false, err
	}
	listed, err := h.listWorkspaces(ctx, spec.Session.Name)
	if err != nil {
		return WorkspaceHandle{}, false, err
	}
	if len(listed) == 0 {
		return WorkspaceHandle{}, false, nil
	}
	panes, err := h.listPanes(ctx, spec.Session.Name)
	if err != nil {
		return WorkspaceHandle{}, false, err
	}
	tabs, err := h.listTabs(ctx, spec.Session.Name, "")
	if err != nil {
		return WorkspaceHandle{}, false, err
	}
	var matches []listedWorkspace
	for i := range listed {
		w := listed[i]
		if w.Label != spec.Label {
			continue
		}
		if cwd := workspaceRootCwd(w, tabs, panes); cwd != "" && samePath(cwd, spec.Cwd) {
			matches = append(matches, w)
		}
	}
	if len(matches) == 0 {
		return WorkspaceHandle{}, false, nil
	}
	occupied, occErr := h.occupiedWorkspaces(ctx, spec.Session)
	if occErr != nil {
		occupied = nil
	}
	ids := make([]string, 0, len(matches))
	byID := map[string]listedWorkspace{}
	for _, m := range matches {
		ids = append(ids, m.WorkspaceID)
		byID[m.WorkspaceID] = m
	}
	chosen := pickWorkspaceID(ids, occupied)
	return h.workspaceHandle(spec.Session, byID[chosen], tabs, panes), true, nil
}

func (h *Herdr) occupiedWorkspaces(ctx context.Context, session SessionHandle) (map[string]bool, error) {
	out := map[string]bool{}
	agents, err := h.ListAgents(ctx, session)
	if err != nil {
		return nil, err
	}
	for _, ag := range agents {
		if id := ag.Handle.Tab.WorkspaceID; id != "" {
			out[id] = true
		}
	}
	return out, nil
}

func (h *Herdr) collapseEmptyProjectWorkspaces(ctx context.Context, spec WorkspaceSpec, keepID string) {
	listed, err := h.listWorkspaces(ctx, spec.Session.Name)
	if err != nil || len(listed) == 0 {
		return
	}
	panes, err := h.listPanes(ctx, spec.Session.Name)
	if err != nil {
		return
	}
	tabs, err := h.listTabs(ctx, spec.Session.Name, "")
	if err != nil {
		return
	}
	occupied, err := h.occupiedWorkspaces(ctx, spec.Session)
	if err != nil {
		return
	}
	for _, w := range listed {
		if w.WorkspaceID == keepID || w.Label != spec.Label {
			continue
		}
		if cwd := workspaceRootCwd(w, tabs, panes); cwd == "" || !samePath(cwd, spec.Cwd) {
			continue
		}
		if occupied[w.WorkspaceID] {
			continue
		}
		_, _ = h.run(ctx, spec.Session.Name, []string{"workspace", "close", w.WorkspaceID})
	}
}

func (h *Herdr) listWorkspaces(ctx context.Context, session string) ([]listedWorkspace, error) {
	res, err := h.run(ctx, session, []string{"workspace", "list"})
	if err != nil {
		return nil, err
	}
	list, err := parseWorkspaceList(res.Stdout)
	if err != nil {
		return nil, observability.WrapError(observability.CodeUnknown, "herdr workspace list", err)
	}
	return list, nil
}

// CreateAgentTab implements Adapter. The first tab in a Project workspace is
// the unused root tab from workspace create (label "1" on 0.8.2); that is the
// Mate tab and is renamed. A later call issues `tab create` for Crew. Env on
// the rename path cannot be applied after the fact — set WorkspaceSpec.Env at
// create. TabSpec.Env is applied on the Crew `tab create` path.
func (h *Herdr) CreateAgentTab(ctx context.Context, spec TabSpec) (TabHandle, error) {
	if err := CheckTabSpec(spec); err != nil {
		return TabHandle{}, err
	}
	env, err := AllowlistedEnv(spec.Env)
	if err != nil {
		return TabHandle{}, err
	}
	session := spec.Workspace.Session.Name
	if existing, pane, ok, err := h.tabByLabel(ctx, spec.Workspace, spec.Label); err != nil {
		return TabHandle{}, err
	} else if ok {
		if err := h.waitPaneShell(ctx, session, pane.PaneID); err == nil {
			if len(env) > 0 {
				return TabHandle{}, observability.NewError(
					observability.CodeUsage,
					"launch environment is injected only when the pane is created (tab create --env); this tab already exists",
				)
			}
			if err := checkObservedTabCwd(spec.Cwd, pane.Cwd); err != nil {
				return TabHandle{}, err
			}
			return TabHandle{
				Session:     spec.Workspace.Session,
				WorkspaceID: spec.Workspace.WorkspaceID,
				TabID:       existing.TabID,
				PaneID:      pane.PaneID,
				TerminalID:  pane.TerminalID,
				Cwd:         pane.Cwd,
				Label:       spec.Label,
			}, nil
		} else if !paneMissing(err) {
			return TabHandle{}, err
		}
		// Pane is gone (typical after pane-close). Do not reuse the label.
	}
	if root, pane, ok, err := h.unusedRootTab(ctx, spec.Workspace); err != nil {
		return TabHandle{}, err
	} else if ok {
		if err := h.waitPaneShell(ctx, session, pane.PaneID); err == nil {
			crewTab := len(env) > 0 && spec.Cwd != "" && pane.Cwd != "" && !samePath(pane.Cwd, spec.Cwd)
			if len(env) > 0 && !crewTab {
				return TabHandle{}, observability.NewError(
					observability.CodeUsage,
					"pane environment is injected by workspace create --env; tab rename cannot apply environment after the pane exists",
				)
			}
			if !crewTab {
				if err := checkObservedTabCwd(spec.Cwd, pane.Cwd); err != nil {
					return TabHandle{}, err
				}
				if _, err := h.run(ctx, session, []string{"tab", "rename", root.TabID, spec.Label}); err != nil {
					return TabHandle{}, err
				}
				return TabHandle{
					Session:     spec.Workspace.Session,
					WorkspaceID: spec.Workspace.WorkspaceID,
					TabID:       root.TabID,
					PaneID:      pane.PaneID,
					TerminalID:  pane.TerminalID,
					Cwd:         pane.Cwd,
					Label:       spec.Label,
					Env:         spec.Workspace.RootTab.Env,
				}, nil
			}
			// Crew: unused root pane stays the Mate slot; create a new tab
			// so --env is applied at pane create.
		} else if !paneMissing(err) {
			// Slow shell, not a missing pane: creating a second tab would
			// leave the root behind after stop (B5 live leftover).
			return TabHandle{}, err
		}
	}
	args := []string{
		"tab", "create",
		"--workspace", spec.Workspace.WorkspaceID,
		"--label", spec.Label,
		"--cwd", spec.Cwd,
		"--no-focus",
	}
	args = append(args, envFlags(env)...)
	res, err := h.run(ctx, session, args)
	if err != nil {
		return TabHandle{}, err
	}
	created, err := parseTabCreated(res.Stdout)
	if err != nil {
		return TabHandle{}, observability.WrapError(observability.CodeUnknown, "herdr tab create", err)
	}
	// The tab is real from this point on. A refusal below must still hand the
	// caller a handle naming it - RemoveTab needs TabID, not Cwd - so a saga
	// can compensate instead of leaking a tab it never learned exists.
	createdHandle := TabHandle{
		Session:     spec.Workspace.Session,
		WorkspaceID: created.WorkspaceID,
		TabID:       created.TabID,
		PaneID:      created.PaneID,
		TerminalID:  created.TerminalID,
		Cwd:         created.Cwd,
		Label:       spec.Label,
		Env:         env,
	}
	if err := checkObservedTabCwd(spec.Cwd, created.Cwd); err != nil {
		return createdHandle, err
	}
	if err := h.waitPaneShell(ctx, session, created.PaneID); err != nil {
		return createdHandle, err
	}
	return createdHandle, nil
}

// checkObservedTabCwd refuses a tab whose observed pane cwd cannot be trusted
// to satisfy the requested cwd. A missing observation and a mismatched one
// are different diagnoses even though both refuse: an empty cwd means Herdr
// did not tell mate where the pane runs, while a non-matching cwd means
// Herdr told mate and it disagrees with what was asked for. Both leave the
// launch guard in adapter.go unable to prove context delivery, which is what
// this refusal protects (the tab handle must carry what Herdr reported, not
// what was requested, or that guard compares the request against itself).
func checkObservedTabCwd(requested, observed string) error {
	if strings.TrimSpace(observed) == "" {
		return observability.NewError(
			observability.CodeStateConflict,
			fmt.Sprintf("herdr did not report a cwd for this pane; cannot verify the agent will discover its context at %s", requested),
		)
	}
	if !samePath(observed, requested) {
		return observability.NewError(
			observability.CodeStateConflict,
			fmt.Sprintf("herdr reports the pane cwd as %s, not the requested %s; refusing to start an agent that would discover the wrong context", observed, requested),
		)
	}
	return nil
}

func (h *Herdr) tabByLabel(ctx context.Context, ws WorkspaceHandle, label string) (listedTab, listedPane, bool, error) {
	tabs, err := h.listTabs(ctx, ws.Session.Name, ws.WorkspaceID)
	if err != nil {
		return listedTab{}, listedPane{}, false, err
	}
	var found *listedTab
	for i := range tabs {
		if tabs[i].WorkspaceID == ws.WorkspaceID && tabs[i].Label == label {
			if found != nil {
				return listedTab{}, listedPane{}, false, observability.NewError(
					observability.CodeStateConflict,
					fmt.Sprintf("multiple tabs in workspace %s have label %q", ws.WorkspaceID, label),
				)
			}
			found = &tabs[i]
		}
	}
	if found == nil {
		return listedTab{}, listedPane{}, false, nil
	}
	pane, err := h.paneForTab(ctx, ws, found.TabID)
	if err != nil {
		return listedTab{}, listedPane{}, false, err
	}
	return *found, pane, true, nil
}

func (h *Herdr) unusedRootTab(ctx context.Context, ws WorkspaceHandle) (listedTab, listedPane, bool, error) {
	tabs, err := h.listTabs(ctx, ws.Session.Name, ws.WorkspaceID)
	if err != nil {
		return listedTab{}, listedPane{}, false, err
	}
	var root *listedTab
	for i := range tabs {
		if tabs[i].WorkspaceID == ws.WorkspaceID && tabs[i].Label == herdrDefaultRootTabLabel {
			if root != nil {
				return listedTab{}, listedPane{}, false, nil
			}
			root = &tabs[i]
		}
	}
	if root == nil {
		return listedTab{}, listedPane{}, false, nil
	}
	pane, err := h.paneForTab(ctx, ws, root.TabID)
	if err != nil {
		return listedTab{}, listedPane{}, false, err
	}
	return *root, pane, true, nil
}

func (h *Herdr) paneForTab(ctx context.Context, ws WorkspaceHandle, tabID string) (listedPane, error) {
	panes, err := h.listPanes(ctx, ws.Session.Name)
	if err != nil {
		return listedPane{}, err
	}
	for _, p := range panes {
		if p.TabID == tabID {
			return p, nil
		}
	}
	if ws.RootTab.PaneID != "" && ws.RootTab.TabID == tabID {
		return listedPane{
			PaneID:      ws.RootTab.PaneID,
			TabID:       ws.RootTab.TabID,
			WorkspaceID: ws.WorkspaceID,
			TerminalID:  ws.RootTab.TerminalID,
			Cwd:         ws.RootTab.Cwd,
		}, nil
	}
	return listedPane{}, observability.NewError(
		observability.CodeUnknown,
		fmt.Sprintf("workspace %s tab %s has no pane", ws.WorkspaceID, tabID),
	)
}

func (h *Herdr) listTabs(ctx context.Context, session, workspaceID string) ([]listedTab, error) {
	args := []string{"tab", "list"}
	if workspaceID != "" {
		args = append(args, "--workspace", workspaceID)
	}
	res, err := h.run(ctx, session, args)
	if err != nil {
		return nil, err
	}
	tabs, err := parseTabList(res.Stdout)
	if err != nil {
		return nil, observability.WrapError(observability.CodeUnknown, "herdr tab list", err)
	}
	return tabs, nil
}

func (h *Herdr) listPanes(ctx context.Context, session string) ([]listedPane, error) {
	res, err := h.run(ctx, session, []string{"pane", "list"})
	if err != nil {
		return nil, err
	}
	panes, err := parsePaneList(res.Stdout)
	if err != nil {
		return nil, observability.WrapError(observability.CodeUnknown, "herdr pane list", err)
	}
	return panes, nil
}

// workspaceRootTabID is the tab Herdr created with the workspace (tab
// number 1 on 0.8.2, or the unused default label "1"). Focus / active_tab_id
// is UI state and is not identity.
func workspaceRootTabID(w listedWorkspace, tabs []listedTab) string {
	var unlabeled string
	for _, t := range tabs {
		if t.WorkspaceID != w.WorkspaceID {
			continue
		}
		if t.Number == 1 {
			return t.TabID
		}
		if unlabeled == "" && t.Label == herdrDefaultRootTabLabel {
			unlabeled = t.TabID
		}
	}
	return unlabeled
}

func workspaceRootCwd(w listedWorkspace, tabs []listedTab, panes []listedPane) string {
	if p, ok := workspaceRootPane(w, tabs, panes); ok {
		return p.Cwd
	}
	return ""
}

func workspaceRootPane(w listedWorkspace, tabs []listedTab, panes []listedPane) (listedPane, bool) {
	root := workspaceRootTabID(w, tabs)
	if root == "" {
		return listedPane{}, false
	}
	for _, p := range panes {
		if p.TabID == root {
			return p, true
		}
	}
	return listedPane{}, false
}

func (h *Herdr) workspaceHandle(session SessionHandle, w listedWorkspace, tabs []listedTab, panes []listedPane) WorkspaceHandle {
	hnd := WorkspaceHandle{Session: session, WorkspaceID: w.WorkspaceID, Label: w.Label, Cwd: workspaceRootCwd(w, tabs, panes)}
	if p, ok := workspaceRootPane(w, tabs, panes); ok {
		hnd.RootTab = TabHandle{
			Session:     session,
			WorkspaceID: w.WorkspaceID,
			TabID:       p.TabID,
			PaneID:      p.PaneID,
			TerminalID:  p.TerminalID,
			Cwd:         p.Cwd,
		}
	}
	return hnd
}

func paneMissing(err error) bool {
	switch herdrCodeOf(err) {
	case HerdrPaneNotFound, HerdrAgentPaneNotFound:
		return true
	}
	return false
}

// StartAgent implements Adapter. agent_not_ready (blocked during startup) is
// a live agent: G4 lab capture left launch_pending=true / blocked, and
// `agent wait --until blocked` then exited 0. That is not a failed start.
func (h *Herdr) StartAgent(ctx context.Context, spec AgentStartSpec) (AgentHandle, error) {
	if spec == nil {
		return AgentHandle{}, observability.NewError(observability.CodeUsage, "start requires a spec from NewAgentStartSpec")
	}
	if err := spec.Validate(); err != nil {
		return AgentHandle{}, err
	}
	if h.Names == nil {
		return AgentHandle{}, observability.NewError(observability.CodeUsage, "start requires a live-name registry")
	}
	session := spec.Session()
	if err := h.Names.Reserve(session.Name, spec.Name(), spec.RawID()); err != nil {
		return AgentHandle{}, err
	}
	argv, err := AgentStartArgvTimeout(session.Name, spec.Name(), string(spec.Kind()), spec.Tab().PaneID, spec.Timeout(), spec.Launch().Args())
	if err != nil {
		return AgentHandle{}, err
	}
	// A long-running Herdr server can carry CLAUDE_CONFIG_DIR, and the
	// nested-session variables of whichever Claude Code session started it,
	// from the environment it was started with. The launch must remove those
	// inherited values in this pane (one `unset` for all of them), rather than
	// setting empty values or mutating the server environment shared by other
	// agents. See harness.NestedSessionEnv for the measured failure.
	if keys := spec.Launch().UnsetEnv(); len(keys) > 0 {
		argv := append([]string{"pane", "run", spec.Tab().PaneID, "unset"}, keys...)
		if _, err := h.run(ctx, session.Name, argv); err != nil {
			return AgentHandle{}, err
		}
	}
	handle := AgentHandle{
		Session: session,
		Name:    spec.Name(),
		RawID:   spec.RawID(),
		Kind:    spec.Kind(),
		Tab:     spec.Tab(),
	}
	// agent_pane_busy is Herdr's own answer to "this pane cannot host an
	// agent yet", and it is the gate mate trusts: waitPaneShell is a
	// heuristic that proceeds when it cannot tell.
	var last error
	for attempt := 0; attempt < paneBusyAttempts; attempt++ {
		_, err = h.run(ctx, session.Name, argv)
		if err == nil {
			return handle, nil
		}
		if herdrCodeOf(err) == HerdrAgentNotReady {
			if _, inspectErr := h.InspectAgent(ctx, handle); inspectErr == nil {
				return handle, nil
			}
		}
		if herdrCodeOf(err) != HerdrAgentPaneBusy {
			return AgentHandle{}, err
		}
		last = err
		select {
		case <-ctx.Done():
			return AgentHandle{}, ctx.Err()
		case <-time.After(paneBusyRetry):
		}
	}
	if last == nil {
		last = NewHerdrError(HerdrAgentPaneBusy, "pane is not an available shell")
	}
	return AgentHandle{}, h.wrapPaneBusyExhausted(ctx, session.Name, last, handle.Name, spec.Tab().PaneID, paneBusyAttempts, paneBusyRetry)
}

// wrapPaneBusyExhausted turns Herdr's raw agent_pane_busy refusal (its own
// wording names only an opaque pane id, e.g. "agent target pane w3:p1 is not
// an available shell") into a message that says what mate was doing, what it
// found in that pane, and what happens next.
//
// It must NOT tell the operator to go inspect the named pane in Herdr: by the
// time this error reaches a caller, orchestration's own reconcileStartFailure
// (internal/orchestration/start.go) has already run InspectAgent (proving no
// agent started), RemoveTab (closing the pane - and, since it is the sole tab
// of a freshly created workspace, the workspace with it) and reverted the Mate
// record - every other outcome of that compensation produces a different
// wrapped error, so whenever this exact text is what a caller sees, the pane is
// already gone. Earlier wording that suggested checking or attaching to the
// pane was actively misleading for that reason.
//
// That is also why the one fact worth having is read *here*, while the pane
// still exists: `pane process-info` names the process Herdr refused to treat as
// a shell, which is the difference between "check the session's health" and
// "your shell startup re-executes the login shell inside a wrapper". One read,
// after the budget is spent - the retry loop is the hot path and must not pay
// for a diagnostic on every attempt - and a failed read degrades the message
// rather than replacing the refusal, because the pane-busy state_conflict is
// what happened and the inspection failure is a detail of it.
//
// The observation leads and the advice follows: the Console draws this on one
// line and cuts it at the terminal edge, so an 80-column reader has to get the
// pane and its occupant before anything else.
//
// It keeps the mapped Code (state_conflict) and Herdr's own message and pane
// id in Details, so nothing observable is lost.
func (h *Herdr) wrapPaneBusyExhausted(ctx context.Context, session string, last error, agentName, paneID string, attempts int, retry time.Duration) error {
	var coded *observability.Error
	if !errors.As(last, &coded) || coded.Code != observability.CodeStateConflict {
		return last
	}
	budget := time.Duration(attempts) * retry
	occupant, inspectErr := h.paneOccupant(ctx, session, paneID)
	extra := map[string]any{"pane_id": paneID, "herdr_message": coded.Message, "waited": budget.String()}
	var found string
	switch {
	case inspectErr != nil:
		extra["pane_process_error"] = inspectErr.Error()
		found = fmt.Sprintf("pane %s never reached an interactive shell prompt (mate could not inspect it: %v)", paneID, inspectErr)
	case occupant == "":
		found = fmt.Sprintf("pane %s never reached an interactive shell prompt (Herdr reported no process running in it)", paneID)
	default:
		extra["pane_process"] = occupant
		found = fmt.Sprintf("pane %s was running %q, not a shell prompt Herdr will start an agent in", paneID, occupant)
	}
	msg := fmt.Sprintf(
		"%s: mate spent %s retrying agent %s (Herdr's own answer: %q) and has already removed the pane and tab it created "+
			"for this attempt, so there is nothing left there to inspect. A shell integration in your shell startup files "+
			"that re-executes the login shell inside its own wrapper makes every new pane refuse this way; otherwise check "+
			"the Herdr session's health rather than this pane, since a retry creates an entirely new pane and hits the same "+
			"%s budget.",
		found, budget, agentName, coded.Message, budget,
	)
	wrapped := observability.WrapError(observability.CodeStateConflict, msg, last)
	details := make(map[string]any, len(coded.Details)+len(extra))
	for k, v := range coded.Details {
		details[k] = v
	}
	for k, v := range extra {
		details[k] = v
	}
	return wrapped.WithDetails(details)
}

// paneOccupant reports what Herdr sees running in a pane, for diagnostics
// only. An empty name with a nil error means Herdr reported no foreground
// process, which is a different fact from "mate could not ask".
func (h *Herdr) paneOccupant(ctx context.Context, session, pane string) (string, error) {
	res, err := h.run(ctx, session, []string{"pane", "process-info", "--pane", pane})
	if err != nil {
		return "", err
	}
	info, err := parsePaneProcessInfo(res.Stdout)
	if err != nil {
		return "", err
	}
	return strings.Join(info.Foreground, ", "), nil
}

// waitPaneShell gives a freshly created pane a moment to draw a shell prompt.
// Live capture: tab create returns a pane with no terminal_title (revision 0);
// about a second later the title is a shell prompt and agent start succeeds,
// while an immediate start can be agent_pane_busy ("not an available shell").
//
// The title is a *positive* signal only. Its absence does not prove the pane
// cannot host an agent: a live shell blocked on an interactive startup prompt
// never draws a title at all, and `agent start` on such a pane is accepted by
// Herdr rather than refused (lab evidence in ADR 0011: a pane whose zsh sat on
// "[oh-my-zsh] Would you like to update? [Y/n]" stayed titleless indefinitely
// at revision 0, and `agent start` engaged it). Treating a missing title as a
// failure cost a whole gate's worth of starts, because callers closed and
// recreated the workspace on it.
//
// So: a title returns nil early, a real `pane get` failure (a missing pane
// above all, which callers do act on) is returned, and an expired budget
// returns nil. `herdr agent start` is the authority on whether a pane can host
// an agent, and StartAgent retries its agent_pane_busy.
func (h *Herdr) waitPaneShell(ctx context.Context, session, paneID string) error {
	if strings.TrimSpace(paneID) == "" {
		return observability.NewError(observability.CodeUsage, "pane wait requires a pane id")
	}
	deadline := time.Now().Add(h.paneShellBudget())
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := h.run(ctx, session, []string{"pane", "get", paneID})
		if err != nil {
			return err
		}
		info, err := parsePaneInfo(res.Stdout)
		if err != nil {
			return observability.WrapError(observability.CodeUnknown, "herdr pane get", err)
		}
		if strings.TrimSpace(info.TerminalTitle) != "" {
			return nil
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// InspectAgent implements Adapter.
func (h *Herdr) InspectAgent(ctx context.Context, handle AgentHandle) (ObservedAgent, error) {
	res, err := h.run(ctx, handle.Session.Name, []string{"agent", "get", handle.Name})
	if err != nil {
		return ObservedAgent{}, err
	}
	info, err := parseAgentInfo(res.Stdout)
	if err != nil {
		return ObservedAgent{}, observability.WrapError(observability.CodeUnknown, "herdr agent get", err)
	}
	return h.observed(handle, info), nil
}

// ReadAgent implements Adapter. Herdr owns the bounded snapshot; mate never
// reads a pane directly.
func (h *Herdr) ReadAgent(ctx context.Context, handle AgentHandle, lines int) (string, error) {
	if strings.TrimSpace(handle.Session.Name) == "" || strings.TrimSpace(handle.Name) == "" {
		return "", observability.NewError(observability.CodeUsage, "agent read requires a named session and agent")
	}
	if lines <= 0 {
		return "", observability.NewError(observability.CodeUsage, "agent read lines must be positive")
	}
	res, err := h.run(ctx, handle.Session.Name, []string{
		"agent", "read", handle.Name, "--source", "recent-unwrapped",
		"--lines", fmt.Sprintf("%d", lines), "--format", "text",
	})
	if err != nil {
		return "", err
	}
	return string(res.Stdout), nil
}

// ListAgents implements Adapter. The inventory is `agent list` of the named
// session. Occupancy is answered by name and recorded pane id, never by
// which tab is focused.
func (h *Herdr) ListAgents(ctx context.Context, session SessionHandle) ([]ObservedAgent, error) {
	if strings.TrimSpace(session.Name) == "" {
		return nil, observability.NewError(observability.CodeUsage, "agent list requires a named session")
	}
	res, err := h.run(ctx, session.Name, []string{"agent", "list"})
	if err != nil {
		return nil, err
	}
	infos, err := parseAgentList(res.Stdout)
	if err != nil {
		return nil, observability.WrapError(observability.CodeUnknown, "herdr agent list", err)
	}
	out := make([]ObservedAgent, 0, len(infos))
	for _, info := range infos {
		handle := AgentHandle{
			Session: session,
			Name:    info.Name,
			Kind:    harness.Kind(info.Kind),
			Tab: TabHandle{
				Session:     session,
				WorkspaceID: info.WorkspaceID,
				TabID:       info.TabID,
				PaneID:      info.PaneID,
				TerminalID:  info.TerminalID,
				Cwd:         info.Cwd,
			},
		}
		out = append(out, h.observed(handle, info))
	}
	return out, nil
}

// WaitAgent implements Adapter. A wait that matches blocked is success
// (live: `agent wait mate-g4-01 --until blocked --timeout 2000` exit 0).
func (h *Herdr) WaitAgent(ctx context.Context, handle AgentHandle, until WaitCondition) (ObservedAgent, error) {
	if err := CheckWaitTimeout(until.Timeout); err != nil {
		return ObservedAgent{}, err
	}
	args := []string{"agent", "wait", handle.Name}
	for _, s := range until.Until {
		args = append(args, "--until", string(s))
	}
	args = append(args, "--timeout", fmt.Sprintf("%d", until.Timeout.Milliseconds()))
	res, err := h.run(ctx, handle.Session.Name, args)
	if err != nil {
		return ObservedAgent{}, err
	}
	info, err := parseAgentInfo(res.Stdout)
	if err != nil {
		return ObservedAgent{}, observability.WrapError(observability.CodeUnknown, "herdr agent wait", err)
	}
	obs := h.observed(handle, info)
	if !until.Matches(obs.Status) {
		// Herdr already matched; if our condition disagrees, still return the
		// observation — do not invent a timeout the CLI did not report.
		return obs, nil
	}
	return obs, nil
}

// PromptAgent implements Adapter. agent_blocked here means the prompt was
// refused (requires interactive input). That is distinct from WaitAgent
// matching blocked, which is success.
func (h *Herdr) PromptAgent(ctx context.Context, handle AgentHandle, text string) error {
	_, err := h.run(ctx, handle.Session.Name, []string{"agent", "prompt", handle.Name, text})
	return err
}

// SendKeys implements Adapter as `herdr agent send-keys <name> <keys...>`:
// the press is addressed to the agent by name and Herdr resolves the name to
// its current pane inside that one request, so there is no window between
// looking a pane up and pressing into it during which the agent could stop
// or move and a new occupant receive the key (PR 96 counter-review B2). A
// name Herdr no longer knows - including one whose pane was just closed -
// answers agent_not_found and nothing is pressed (measured 2026-09-14). One
// call per press, so a caller can re-read the screen between a selection and
// its confirmation. The residual is Herdr's own name resolution: a different
// agent started under this exact name inside that request would receive the
// press, which mate's per-launch name allocation makes a collision, not a
// reuse.
func (h *Herdr) SendKeys(ctx context.Context, handle AgentHandle, keys []string) error {
	if strings.TrimSpace(handle.Session.Name) == "" || strings.TrimSpace(handle.Name) == "" {
		return observability.NewError(observability.CodeUsage, "send-keys requires a named session and agent")
	}
	if err := checkSendKeys(keys); err != nil {
		return err
	}
	argv := append([]string{"agent", "send-keys", handle.Name}, keys...)
	_, err := h.run(ctx, handle.Session.Name, argv)
	return err
}

// checkSendKeys refuses a key list Herdr would misread: nothing, an empty
// token, a token carrying whitespace (two presses in one argument), or a token
// that would be parsed as an option. Only the caller's measured key names
// belong here.
func checkSendKeys(keys []string) error {
	if len(keys) == 0 {
		return observability.NewError(observability.CodeUsage, "send-keys requires at least one key")
	}
	for _, k := range keys {
		if k == "" || strings.ContainsAny(k, " \t\r\n") || strings.HasPrefix(k, "-") {
			return observability.NewError(observability.CodeUsage, fmt.Sprintf("send-keys refuses key %q: one bare key name per press", k))
		}
	}
	return nil
}

// AttachAgent implements Adapter. Interactive attach is a TTY handoff, not a
// captured ProcessRunner.Run.
//
// Both live cases are real and the adapter detects which one it is instead of
// assuming: a pane running an agent attaches with `herdr agent attach <name>`,
// a pane with no agent with `herdr terminal attach <terminal_id>`. Detection
// asks Herdr; an explicit AttachTarget.Mode is the caller's decision.
//
// The returned AttachedTo describes what was actually attached, read live. A
// caller must report that rather than the handle it passed in: the recorded
// agent name may be dead and the recorded pane may belong to someone else.
//
// Detach is the shared config binding DetachKey and does not stop the agent
// (ADR 0003, re-verified live for G4-07). Ghostty-native TTY restore after the
// handoff remains unproven (AssumptionGhosttyTTYRestore); nothing here claims
// it.
func (h *Herdr) AttachAgent(ctx context.Context, handle AgentHandle, target AttachTarget) (AttachedTo, error) {
	// The terminal is the one precondition that cannot be satisfied by
	// anything Herdr says, so it is checked before any probe: a caller with
	// no terminal cannot use the answers.
	if h.AttachPreflight != nil {
		if err := h.AttachPreflight(ctx); err != nil {
			return AttachedTo{}, err
		}
	}
	attached, argv, err := h.attachArgv(ctx, handle, target)
	if err != nil {
		return AttachedTo{}, err
	}
	if h.Attach == nil {
		return AttachedTo{}, observability.NewError(
			observability.CodeUnknown,
			"attach is a TTY handoff; this adapter was built without one (see runtime.TTYHandoff)",
		).WithDetails(map[string]any{"argv": argv})
	}
	if err := h.Attach(ctx, argv); err != nil {
		return AttachedTo{}, err
	}
	return attached, nil
}

// attachArgv resolves the attach command for handle, and what that command
// will connect to. It runs before the terminal is handed over, so a target
// that cannot be attached is a JSON error rather than a half-entered alt
// screen.
func (h *Herdr) attachArgv(ctx context.Context, handle AgentHandle, target AttachTarget) (AttachedTo, []string, error) {
	session := handle.Session.Name
	switch target.Mode {
	case AttachTerminal:
		pane, err := h.attachTerminalPane(ctx, handle)
		if err != nil {
			return AttachedTo{}, nil, err
		}
		return AttachedTo{Mode: AttachTerminal, PaneID: pane.PaneID, TerminalID: pane.TerminalID},
			WithSession(session, []string{"terminal", "attach", pane.TerminalID}), nil
	case AttachAgent:
		if strings.TrimSpace(handle.Name) == "" {
			return AttachedTo{}, nil, observability.NewError(observability.CodeUsage, "agent attach requires an agent name")
		}
		// No PaneID: an explicit agent attach skips the probe by the caller's
		// choice, so nothing here observed which pane the agent is in. A
		// recorded one would be reported as if it had been.
		return AttachedTo{Mode: AttachAgent, AgentName: handle.Name},
			WithSession(session, []string{"agent", "attach", handle.Name}), nil
	case AttachDetect:
		return h.detectAttachArgv(ctx, handle)
	default:
		return AttachedTo{}, nil, observability.NewError(observability.CodeUsage,
			fmt.Sprintf("unknown attach mode %q", string(target.Mode)))
	}
}

// detectAttachArgv asks Herdr what the target actually is.
//
// The recorded agent name being live is the agent case, and the observation
// also supplies the live pane, so the outcome never repeats a stale handle.
//
// The recorded name *not* being live is not on its own the terminal case. It
// only says the name is gone; the recorded pane may have been taken by a
// different agent since, and terminal-attaching there would stream that
// agent's session under the dead name. So the pane is checked for an
// occupant, and only a genuinely agentless pane is attached as a terminal
// (PR #9 counter-review, B1).
func (h *Herdr) detectAttachArgv(ctx context.Context, handle AgentHandle) (AttachedTo, []string, error) {
	session := handle.Session.Name
	if strings.TrimSpace(handle.Name) != "" {
		obs, err := h.InspectAgent(ctx, handle)
		if err == nil {
			return AttachedTo{
					Mode:      AttachAgent,
					AgentName: obs.Handle.Name,
					PaneID:    obs.Handle.Tab.PaneID,
				},
				WithSession(session, []string{"agent", "attach", obs.Handle.Name}), nil
		}
		if herdrCodeOf(err) != HerdrAgentNotFound {
			return AttachedTo{}, nil, err
		}
		if strings.TrimSpace(handle.Tab.PaneID) == "" && strings.TrimSpace(handle.Tab.TerminalID) == "" {
			// No agent and no pane: there is nothing left to attach to, and
			// Herdr's own answer is the honest one to report.
			return AttachedTo{}, nil, err
		}
	}
	pane, err := h.attachTerminalPane(ctx, handle)
	if err != nil {
		return AttachedTo{}, nil, err
	}
	return AttachedTo{Mode: AttachTerminal, PaneID: pane.PaneID, TerminalID: pane.TerminalID},
		WithSession(session, []string{"terminal", "attach", pane.TerminalID}), nil
}

// attachTerminalPane reads the pane live and refuses one that hosts an agent.
//
// Two reasons the pane is read rather than trusted. A persisted terminal id
// may name someone else's terminal, because closed Herdr ids are
// unproven-not-reused (AssumptionClosedIDReuse). And a pane that has since
// been given to another agent must not be streamed as if it were a bare
// shell: `pane get` reports `agent` and a non-unknown `agent_status` exactly
// then, which is the signal this refuses on.
func (h *Herdr) attachTerminalPane(ctx context.Context, handle AgentHandle) (paneInfo, error) {
	pane := strings.TrimSpace(handle.Tab.PaneID)
	if pane == "" {
		if id := strings.TrimSpace(handle.Tab.TerminalID); id != "" {
			// No pane to verify against. The caller named a terminal
			// directly, which is only reachable through an explicit
			// AttachTerminal; detection never gets here without a pane.
			return paneInfo{TerminalID: id}, nil
		}
		return paneInfo{}, observability.NewError(observability.CodeUsage, "terminal attach requires a pane or terminal id")
	}
	res, err := h.run(ctx, handle.Session.Name, []string{"pane", "get", pane})
	if err != nil {
		return paneInfo{}, err
	}
	info, err := parsePaneInfo(res.Stdout)
	if err != nil {
		return paneInfo{}, observability.WrapError(observability.CodeUnknown, "herdr pane get", err)
	}
	if paneHasAgent(info) {
		return paneInfo{}, h.paneOccupiedError(ctx, handle, info)
	}
	if strings.TrimSpace(info.TerminalID) == "" {
		return paneInfo{}, observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("pane %s has no terminal to attach to", pane))
	}
	return info, nil
}

// paneHasAgent reports whether Herdr says this pane currently hosts an agent.
// Live 0.8.2: an agentless pane omits `agent` and reports
// `agent_status: unknown`; an occupied one emits `"agent":"claude"` and a
// real status.
func paneHasAgent(info paneInfo) bool {
	if strings.TrimSpace(info.Agent) != "" {
		return true
	}
	status := strings.TrimSpace(info.AgentStatus)
	return status != "" && AgentStatus(status) != AgentUnknown
}

// paneOccupiedError names the agent that actually holds the pane, so the
// refusal tells the operator what to attach to instead. The name is a best
// effort: if `agent list` cannot supply it the refusal still stands, because
// an unidentified occupant is still not the agent that was asked for.
func (h *Herdr) paneOccupiedError(ctx context.Context, handle AgentHandle, info paneInfo) error {
	occupant := h.agentNameInPane(ctx, handle.Session.Name, info.PaneID)
	details := map[string]any{
		"pane":            info.PaneID,
		"recorded_agent":  handle.Name,
		"pane_agent_kind": info.Agent,
	}
	if occupant != "" {
		details["pane_agent"] = occupant
		return observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("pane %s now hosts agent %q, not %q; attach that agent by name, or repair the binding", info.PaneID, occupant, handle.Name)).
			WithDetails(details)
	}
	return observability.NewError(observability.CodeStateConflict,
		fmt.Sprintf("pane %s hosts an agent that is not %q; refusing to attach to it", info.PaneID, handle.Name)).
		WithDetails(details)
}

// agentNameInPane returns the live agent occupying pane, or "" when Herdr
// cannot say. It is only used to make a refusal readable and never to decide
// one.
func (h *Herdr) agentNameInPane(ctx context.Context, session, pane string) string {
	res, err := h.run(ctx, session, []string{"agent", "list"})
	if err != nil {
		return ""
	}
	agents, err := parseAgentList(res.Stdout)
	if err != nil {
		return ""
	}
	for _, a := range agents {
		if a.PaneID == pane {
			return a.Name
		}
	}
	return ""
}

// StopAgent implements Adapter. There is no `herdr agent stop`. Graceful
// Claude stop is `agent prompt /exit` (G1). Force is pane close. When to
// stop (stop-before-switch) is G4-06. Codex graceful stop is unproven.
func (h *Herdr) StopAgent(ctx context.Context, handle AgentHandle, mode StopMode) error {
	if mode == StopGraceful {
		if handle.Kind != harness.KindClaude {
			return observability.NewError(observability.CodeUnknown, "graceful stop is harness-specific and unproven for this kind")
		}
		if err := h.PromptAgent(ctx, handle, "/exit"); err != nil && herdrCodeOf(err) != HerdrAgentNotFound {
			return err
		}
		_, err := h.InspectAgent(ctx, handle)
		if err != nil && herdrCodeOf(err) == HerdrAgentNotFound {
			if h.Names != nil {
				h.Names.Release(handle.Session.Name, handle.Name)
			}
			return nil
		}
		if err != nil {
			return err
		}
		return observability.NewError(observability.CodeUnknown, "graceful /exit did not end the agent")
	}
	if strings.TrimSpace(handle.Tab.PaneID) == "" {
		return observability.NewError(observability.CodeUsage, "force stop requires a pane id")
	}
	if _, err := h.run(ctx, handle.Session.Name, []string{"pane", "close", handle.Tab.PaneID}); err != nil {
		return err
	}
	if h.Names != nil {
		h.Names.Release(handle.Session.Name, handle.Name)
	}
	return nil
}

// RemoveTab implements Adapter.
func (h *Herdr) RemoveTab(ctx context.Context, handle TabHandle) error {
	if strings.TrimSpace(handle.TabID) == "" {
		return observability.NewError(observability.CodeUsage, "tab close requires a tab id")
	}
	_, err := h.run(ctx, handle.Session.Name, []string{"tab", "close", handle.TabID})
	return err
}

func (h *Herdr) observed(handle AgentHandle, info agentInfo) ObservedAgent {
	live := true
	if handle.Tab.PaneID != "" && info.PaneID != "" && handle.Tab.PaneID != info.PaneID {
		live = false
	}
	out := handle
	if info.Name != "" {
		out.Name = info.Name
	}
	if info.PaneID != "" {
		out.Tab.PaneID = info.PaneID
	}
	if info.TabID != "" {
		out.Tab.TabID = info.TabID
	}
	if info.TerminalID != "" {
		out.Tab.TerminalID = info.TerminalID
	}
	if info.WorkspaceID != "" {
		out.Tab.WorkspaceID = info.WorkspaceID
	}
	return ObservedAgent{
		Handle:        out,
		Status:        info.Status,
		LaunchPending: info.LaunchPending,
		ObservedAt:    h.now(),
		LiveHandleOK:  live,
	}
}
