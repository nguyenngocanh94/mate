package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
)

// Fake is a deterministic RuntimeAdapter for later gates. It simulates
// timeout, blocked, missing target, pane-not-found, name collision, and
// stale handles. It never talks to Herdr.
type Fake struct {
	mu         sync.Mutex
	Names      *MemoryNameRegistry
	Sessions   map[string]SessionHandle
	Workspaces map[string]WorkspaceHandle // session/label
	Tabs       map[string]TabHandle       // pane id
	Agents     map[string]*fakeAgent      // live name
	StartErr   error
	// StartErrAfterCreate makes StartAgent install a live agent and then
	// report an error, modelling a lost response after Herdr accepted the
	// launch.
	StartErrAfterCreate error
	CreateTabErr        error
	RemoveTabErr        error
	WaitErr             error
	PromptErr           error
	ReadErr             error
	AttachErr           error
	StopErr             error
	LookupErr           error
	ListErr             error
	InspectErr          error
	// SessionNotRunning makes LookupSession report the named session as
	// not running, so status/stop can be tested without EnsureSession.
	SessionNotRunning bool
	// StopLeavesAgent makes StopAgent return success without removing the
	// agent. That is the unconfirmed-stop case: the call looked fine, the
	// inventory still has the name.
	StopLeavesAgent bool
	Now             time.Time
	Calls           []string
	ReadOutputs     map[string]string
	// StyledOutputs are the screens ReadAgentStyled returns, for the tests
	// that need SGR attributes. An agent with no entry here falls back to
	// its plain ReadOutputs screen.
	StyledOutputs map[string]string
	ReadCalls     []string
	StartArgv     [][]string
	// StartEnv is, per pane, the environment the latest start in it would
	// have exported into the pane's shell (Herdr.StartAgent's `export`),
	// through the same allowlist.
	StartEnv map[string][]EnvVar
	// PromptGate, if set, is called after PromptAgent is recorded and with
	// the Fake lock released, so a test can interleave another command with
	// an in-flight delivery (claim committed, outcome not yet recorded).
	PromptGate func()
	// StaleListEntries are appended to every ListAgents result unconditionally,
	// regardless of f.Agents. This models the real divergence ADR 0011 cites
	// between Herdr's per-target `agent get` (backing InspectAgent/PromptAgent,
	// which key off f.Agents here) and its full `agent list` inventory: a name
	// `agent get` already reports gone can still be named by `agent list` for a
	// window. Without it, InspectAgent and ListAgents can never disagree in
	// this fake, which left the re-list confirmation a stop's success depends
	// on structurally untested (PR #12 counter-review, S3/M7/M13).
	StaleListEntries []ObservedAgent
	// AttachModes records the mode each successful AttachAgent resolved to,
	// so a caller can assert the agent/terminal distinction was detected.
	AttachModes []AttachMode
	// ListOmit hides a live agent (keyed by "session/name") from ListAgents
	// while InspectAgent still finds it - the false-positive shape ADR 0016
	// amendment item 6's gate 1 exists to close (a ListAgents omission alone
	// is not proof of absence). Without it ListAgents and InspectAgent can
	// only ever agree on liveness in this fake, leaving that gate
	// structurally untested.
	ListOmit map[string]bool
	// SentKeys records every SendKeys call in order, one entry per call.
	SentKeys []SentKeys
	// SendKeysErr makes every SendKeys call fail.
	SendKeysErr error
	// OnSendKeys, if set, runs after a SendKeys call is recorded, with the
	// Fake lock released, so a test can script what the pane shows next
	// (SetReadOutput) the way a real harness redraws after a key press.
	OnSendKeys func(handle AgentHandle, keys []string)
	// SentText records every SendText call in order, one entry per call.
	SentText []SentText
	// SendTextErr makes every SendText call fail.
	SendTextErr error
	// OnSendText, if set, runs after a SendText call is recorded, with the
	// Fake lock released, so a test can script what the pane shows next
	// (SetReadOutput) the way a real harness redraws after typing.
	OnSendText func(handle AgentHandle, text string)
	// NextStartupScreen is what the pane of the next StartAgent shows when
	// read, consumed by that one start. Empty means the harness's own empty
	// composer (a clean start), which is what every launch that does not
	// exercise a startup dialog expects to find.
	NextStartupScreen string
	seq               int
}

// SentKeys is one recorded SendKeys call.
type SentKeys struct {
	Handle AgentHandle
	Keys   []string
}

// SentText is one recorded SendText call.
type SentText struct {
	Handle AgentHandle
	Text   string
}

type fakeAgent struct {
	Handle        AgentHandle
	Status        AgentStatus
	LaunchPending bool
	Interactive   bool
	// SessionRef is what Herdr would report as agent_session.value; a test
	// sets it on the live entry to model a Codex agent past its first
	// prompt.
	SessionRef string
}

// NewFake returns an empty fake runtime.
func NewFake() *Fake {
	return &Fake{
		Names:       NewMemoryNameRegistry(),
		Sessions:    make(map[string]SessionHandle),
		Workspaces:  make(map[string]WorkspaceHandle),
		Tabs:        make(map[string]TabHandle),
		Agents:      make(map[string]*fakeAgent),
		Now:         time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		ReadOutputs: make(map[string]string),
	}
}

// SeedAgent registers a live agent without going through StartAgent, so a
// test can model a pane that already has one (attach, inspect, stop) without
// building a launch spec.
func (f *Fake) SeedAgent(handle AgentHandle, status AgentStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Agents[handle.Session.Name+"/"+handle.Name] = &fakeAgent{Handle: handle, Status: status}
}

func (f *Fake) record(op string) {
	f.Calls = append(f.Calls, op)
}

// EnsureSession implements Adapter.
func (f *Fake) EnsureSession(_ context.Context, spec SessionSpec) (SessionHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("EnsureSession")
	if spec.Names == nil {
		spec.Names = f.Names
	}
	h, err := ResolveSession(spec)
	if err != nil {
		return SessionHandle{}, err
	}
	f.Sessions[h.Name] = h
	return h, nil
}

// LookupSession implements Adapter. It does not record the session as started.
func (f *Fake) LookupSession(_ context.Context, spec SessionSpec) (SessionHandle, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("LookupSession")
	h, err := sessionAddress(spec)
	if err != nil {
		return SessionHandle{}, false, err
	}
	if f.SessionNotRunning {
		return h, false, nil
	}
	return h, true, nil
}

// EnsureProjectWorkspace implements Adapter.
func (f *Fake) LookupProjectWorkspace(_ context.Context, spec WorkspaceSpec) (WorkspaceHandle, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("LookupProjectWorkspace")
	if f.LookupErr != nil {
		return WorkspaceHandle{}, false, f.LookupErr
	}
	if err := CheckWorkspaceSpec(spec); err != nil {
		return WorkspaceHandle{}, false, err
	}
	key := spec.Session.Name + "/" + spec.Label + "/" + spec.Cwd
	existing, ok := f.Workspaces[key]
	return existing, ok, nil
}

func (f *Fake) EnsureProjectWorkspace(_ context.Context, spec WorkspaceSpec) (WorkspaceHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("EnsureProjectWorkspace")
	if err := CheckWorkspaceSpec(spec); err != nil {
		return WorkspaceHandle{}, err
	}
	env, err := AllowlistedEnv(spec.Env)
	if err != nil {
		return WorkspaceHandle{}, err
	}
	key := spec.Session.Name + "/" + spec.Label + "/" + spec.Cwd
	if existing, ok := f.Workspaces[key]; ok {
		if len(env) > 0 {
			return WorkspaceHandle{}, observability.NewError(
				observability.CodeUsage,
				"MATE_* env is injected only when the workspace pane is created (workspace create --env); this workspace already exists",
			)
		}
		return existing, nil
	}
	f.seq++
	n := f.seq
	wsID := fmt.Sprintf("w%d", n)
	root := TabHandle{
		Session:     spec.Session,
		WorkspaceID: wsID,
		TabID:       wsID + ":t1",
		PaneID:      wsID + ":p1",
		TerminalID:  fmt.Sprintf("term_%d", n),
		Cwd:         spec.Cwd,
		Label:       "1",
		Env:         env,
	}
	f.Tabs[root.PaneID] = root
	h := WorkspaceHandle{
		Session:     spec.Session,
		WorkspaceID: wsID,
		Label:       spec.Label,
		Cwd:         spec.Cwd,
		RootTab:     root,
	}
	f.Workspaces[key] = h
	f.record("WorkspaceCreated")
	return h, nil
}

// WorkspaceCreates is how many Ensure calls took the create branch rather
// than reusing an existing workspace. Concurrent starts may Ensure more than
// once - a late resume claim re-enters the start path and reuses - but only
// one of them may create.
func (f *Fake) WorkspaceCreates() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.Calls {
		if c == "WorkspaceCreated" {
			n++
		}
	}
	return n
}

// CreateAgentTab implements Adapter.
func (f *Fake) CreateAgentTab(_ context.Context, spec TabSpec) (TabHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreateAgentTab")
	if f.CreateTabErr != nil {
		return TabHandle{}, f.CreateTabErr
	}
	if err := CheckTabSpec(spec); err != nil {
		return TabHandle{}, err
	}
	env, err := AllowlistedEnv(spec.Env)
	if err != nil {
		return TabHandle{}, err
	}
	for _, existing := range f.Tabs {
		if existing.WorkspaceID == spec.Workspace.WorkspaceID && existing.Label == spec.Label {
			if len(env) > 0 {
				return TabHandle{}, observability.NewError(
					observability.CodeUsage,
					"MATE_* env is injected only when the pane is created (tab create --env); this tab already exists",
				)
			}
			return existing, nil
		}
	}
	for _, existing := range f.Tabs {
		if existing.WorkspaceID == spec.Workspace.WorkspaceID && existing.Label == "1" {
			crewTab := len(env) > 0 && spec.Cwd != "" && existing.Cwd != "" && spec.Cwd != existing.Cwd
			if len(env) > 0 && !crewTab {
				return TabHandle{}, observability.NewError(
					observability.CodeUsage,
					"Mate pane env is injected by workspace create --env; tab rename cannot apply environment after the pane exists",
				)
			}
			if crewTab {
				break
			}
			existing.Label = spec.Label
			existing.Cwd = spec.Cwd
			f.Tabs[existing.PaneID] = existing
			return existing, nil
		}
	}
	f.seq++
	n := f.seq
	h := TabHandle{
		Session:     spec.Workspace.Session,
		WorkspaceID: spec.Workspace.WorkspaceID,
		TabID:       fmt.Sprintf("%s:t%d", spec.Workspace.WorkspaceID, n),
		PaneID:      fmt.Sprintf("%s:p%d", spec.Workspace.WorkspaceID, n),
		TerminalID:  fmt.Sprintf("term_%d", n),
		Cwd:         spec.Cwd,
		Label:       spec.Label,
		Env:         env,
	}
	f.Tabs[h.PaneID] = h
	return h, nil
}

// StartAgent implements Adapter.
func (f *Fake) StartAgent(_ context.Context, spec AgentStartSpec) (AgentHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("StartAgent")
	if f.StartErr != nil {
		return AgentHandle{}, f.StartErr
	}
	// Boundary obligation: nil is the only AgentStartSpec value an outside
	// caller can produce without NewAgentStartSpec; refuse it. Validate is
	// defence in depth and is nil for every constructor-produced spec.
	if spec == nil {
		return AgentHandle{}, observability.NewError(observability.CodeUsage, "start requires a spec from NewAgentStartSpec")
	}
	if err := spec.Validate(); err != nil {
		return AgentHandle{}, err
	}
	session := spec.Session()
	if _, ok := f.Tabs[spec.Tab().PaneID]; !ok {
		return AgentHandle{}, NewHerdrError(HerdrAgentPaneNotFound, "agent pane not found")
	}
	if existing, ok := f.Agents[session.Name+"/"+spec.Name()]; ok {
		return AgentHandle{}, NewHerdrError(HerdrAgentNameTaken, fmt.Sprintf("agent %s is taken", existing.Handle.Name))
	}
	argv, err := AgentStartArgvTimeout(session.Name, spec.Name(), string(spec.Kind()), spec.Tab().PaneID, spec.Timeout(), spec.Launch().Args())
	if err != nil {
		return AgentHandle{}, err
	}
	// A reservation proves allocation, not current ownership: re-reserve
	// atomically so a name released and re-allocated to a different raw id
	// since the caller's AllocateAgentName fails as a collision.
	if err := f.Names.Reserve(session.Name, spec.Name(), spec.RawID()); err != nil {
		return AgentHandle{}, err
	}
	env, err := AllowlistedEnv(runtimeEnv(spec.Launch().Env()))
	if err != nil {
		return AgentHandle{}, err
	}
	f.StartArgv = append(f.StartArgv, argv)
	if f.StartEnv == nil {
		f.StartEnv = map[string][]EnvVar{}
	}
	f.StartEnv[spec.Tab().PaneID] = env
	h := AgentHandle{Session: session, Name: spec.Name(), RawID: spec.RawID(), Kind: spec.Kind(), Tab: spec.Tab()}
	f.Agents[session.Name+"/"+spec.Name()] = &fakeAgent{
		Handle:      h,
		Status:      AgentIdle,
		Interactive: true,
	}
	screen := f.NextStartupScreen
	f.NextStartupScreen = ""
	if screen == "" {
		screen = startupReadyScreen(spec.Kind())
	}
	f.ReadOutputs[session.Name+"/"+spec.Name()] = screen
	if f.StartErrAfterCreate != nil {
		return AgentHandle{}, f.StartErrAfterCreate
	}
	return h, nil
}

// ListAgents implements Adapter.
func (f *Fake) ListAgents(_ context.Context, session SessionHandle) ([]ObservedAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ListAgents")
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	out := make([]ObservedAgent, 0)
	for key, ag := range f.Agents {
		if ag.Handle.Session.Name != session.Name {
			continue
		}
		if f.ListOmit[key] {
			continue
		}
		live := false
		if _, ok := f.Tabs[ag.Handle.Tab.PaneID]; ok {
			live = true
		}
		out = append(out, ObservedAgent{
			Handle:        ag.Handle,
			SessionRef:    ag.SessionRef,
			Status:        ag.Status,
			LaunchPending: ag.LaunchPending,
			Interactive:   ag.Interactive,
			ObservedAt:    f.Now,
			LiveHandleOK:  live,
		})
	}
	for _, ag := range f.StaleListEntries {
		if ag.Handle.Session.Name == session.Name {
			out = append(out, ag)
		}
	}
	return out, nil
}

// InspectAgent implements Adapter.
func (f *Fake) InspectAgent(ctx context.Context, handle AgentHandle) (ObservedAgent, error) {
	// A cancelled context fails the call before it reaches Herdr, as the
	// real adapter's exec does.
	if err := ctx.Err(); err != nil {
		return ObservedAgent{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("InspectAgent")
	if f.InspectErr != nil {
		return ObservedAgent{}, f.InspectErr
	}
	ag, ok := f.Agents[handle.Session.Name+"/"+handle.Name]
	if !ok {
		return ObservedAgent{}, NewHerdrError(HerdrAgentNotFound, "agent target not found")
	}
	live := false
	tab, ok := f.Tabs[ag.Handle.Tab.PaneID]
	if ok {
		live = true
	}
	return ObservedAgent{
		Handle:        ag.Handle,
		SessionRef:    ag.SessionRef,
		Cwd:           tab.Cwd,
		Status:        ag.Status,
		LaunchPending: ag.LaunchPending,
		Interactive:   ag.Interactive,
		ObservedAt:    f.Now,
		LiveHandleOK:  live,
	}, nil
}

// ReadAgent implements Adapter.
func (f *Fake) ReadAgent(_ context.Context, handle AgentHandle, lines int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ReadAgent")
	if lines <= 0 {
		return "", observability.NewError(observability.CodeUsage, "agent read lines must be positive")
	}
	key := handle.Session.Name + "/" + handle.Name
	f.ReadCalls = append(f.ReadCalls, key+":"+fmt.Sprint(lines))
	if f.ReadErr != nil {
		return "", f.ReadErr
	}
	if _, ok := f.Agents[key]; !ok {
		return "", NewHerdrError(HerdrAgentNotFound, "agent target not found")
	}
	return f.ReadOutputs[key], nil
}

// ReadAgentStyled implements Adapter. The fake's scripted screens are plain
// text, which is a screen with no attributes set rather than a screen whose
// attributes were thrown away, so the styled read returns the same bytes
// unless a test scripted a styled screen of its own with SetStyledOutput.
func (f *Fake) ReadAgentStyled(ctx context.Context, handle AgentHandle, lines int) (string, error) {
	f.mu.Lock()
	styled, ok := f.StyledOutputs[handle.Session.Name+"/"+handle.Name]
	f.mu.Unlock()
	if ok {
		if _, err := f.ReadAgent(ctx, handle, lines); err != nil {
			return "", err
		}
		return styled, nil
	}
	return f.ReadAgent(ctx, handle, lines)
}

// SetStyledOutput scripts what ReadAgentStyled returns for one agent, for a
// test that needs the SGR attributes the composer classifier reads.
func (f *Fake) SetStyledOutput(handle AgentHandle, screen string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.StyledOutputs == nil {
		f.StyledOutputs = map[string]string{}
	}
	f.StyledOutputs[handle.Session.Name+"/"+handle.Name] = screen
}

// startupReadyScreen is the smallest snapshot harness.ClassifyStartupScreen
// calls ready for a kind: a fake agent that started cleanly shows its empty
// composer. Kinds with no profile show nothing.
func startupReadyScreen(kind harness.Kind) string {
	switch kind {
	case harness.KindClaude:
		return "──────\n" + harness.ClaudeComposerMarker + "\n──────\n"
	case harness.KindCodex:
		return "› " + harness.CodexComposerPlaceholder + "\n"
	default:
		return ""
	}
}

// SetReadOutput scripts what ReadAgent returns for one agent.
func (f *Fake) SetReadOutput(handle AgentHandle, screen string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ReadOutputs[handle.Session.Name+"/"+handle.Name] = screen
}

// SendKeys implements Adapter.
func (f *Fake) SendKeys(_ context.Context, handle AgentHandle, keys []string) error {
	f.mu.Lock()
	f.record("SendKeys")
	if err := checkSendKeys(keys); err != nil {
		f.mu.Unlock()
		return err
	}
	if f.SendKeysErr != nil {
		f.mu.Unlock()
		return f.SendKeysErr
	}
	if _, ok := f.Agents[handle.Session.Name+"/"+handle.Name]; !ok {
		f.mu.Unlock()
		return NewHerdrError(HerdrAgentNotFound, "agent target not found")
	}
	f.SentKeys = append(f.SentKeys, SentKeys{Handle: handle, Keys: append([]string(nil), keys...)})
	hook := f.OnSendKeys
	f.mu.Unlock()
	if hook != nil {
		hook(handle, keys)
	}
	return nil
}

// SendText implements Adapter.
func (f *Fake) SendText(_ context.Context, handle AgentHandle, text string) error {
	f.mu.Lock()
	f.record("SendText")
	if err := ValidateSendText(text); err != nil {
		f.mu.Unlock()
		return err
	}
	if f.SendTextErr != nil {
		f.mu.Unlock()
		return f.SendTextErr
	}
	if _, ok := f.Agents[handle.Session.Name+"/"+handle.Name]; !ok {
		f.mu.Unlock()
		return NewHerdrError(HerdrAgentNotFound, "agent target not found")
	}
	f.SentText = append(f.SentText, SentText{Handle: handle, Text: text})
	hook := f.OnSendText
	f.mu.Unlock()
	if hook != nil {
		hook(handle, text)
	}
	return nil
}

// WaitAgent implements Adapter. Matching blocked is success.
func (f *Fake) WaitAgent(_ context.Context, handle AgentHandle, until WaitCondition) (ObservedAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("WaitAgent")
	if err := CheckWaitTimeout(until.Timeout); err != nil {
		return ObservedAgent{}, err
	}
	if f.WaitErr != nil {
		return ObservedAgent{}, f.WaitErr
	}
	ag, ok := f.Agents[handle.Session.Name+"/"+handle.Name]
	if !ok {
		return ObservedAgent{}, NewHerdrError(HerdrAgentNotFound, "agent target not found")
	}
	live := false
	if _, ok := f.Tabs[ag.Handle.Tab.PaneID]; ok {
		live = true
	}
	obs := ObservedAgent{
		Handle:        ag.Handle,
		Status:        ag.Status,
		LaunchPending: ag.LaunchPending,
		Interactive:   ag.Interactive,
		ObservedAt:    f.Now,
		LiveHandleOK:  live,
	}
	if until.Matches(ag.Status) {
		return obs, nil
	}
	return obs, NewHerdrError(HerdrTimeout, "timed out waiting for agent status")
}

// PromptAgent implements Adapter.
func (f *Fake) PromptAgent(_ context.Context, handle AgentHandle, text string) error {
	f.mu.Lock()
	f.record("PromptAgent")
	gate := f.PromptGate
	f.mu.Unlock()
	if gate != nil {
		gate()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.PromptErr != nil {
		return f.PromptErr
	}
	ag, ok := f.Agents[handle.Session.Name+"/"+handle.Name]
	if !ok {
		return NewHerdrError(HerdrAgentNotFound, "agent target not found")
	}
	if ag.Status == AgentBlocked {
		return NewHerdrError(HerdrAgentBlocked, "requires interactive input")
	}
	_ = text
	ag.Status = AgentDone
	return nil
}

// AttachAgent implements Adapter. It records the mode it resolved and does
// not exec. Detection mirrors the live adapter: with no explicit mode, a live
// agent name attaches as an agent; a pane with no agent attaches as a
// terminal; and a pane occupied by a *different* agent is refused rather than
// attached under the requested name (PR #9 counter-review, B1).
// Ghostty TTY restore is unproven; this fake does not claim it.
func (f *Fake) AttachAgent(_ context.Context, handle AgentHandle, target AttachTarget) (AttachedTo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.AttachErr != nil {
		f.record("AttachAgent")
		return AttachedTo{}, f.AttachErr
	}
	live, isLive := f.Agents[handle.Session.Name+"/"+handle.Name]
	mode := target.Mode
	switch mode {
	case AttachAgent:
		if !isLive {
			f.record("AttachAgent:agent")
			return AttachedTo{}, NewHerdrError(HerdrAgentNotFound, "agent target not found")
		}
	case AttachTerminal:
	case AttachDetect:
		if isLive {
			mode = AttachAgent
		} else {
			mode = AttachTerminal
		}
	default:
		f.record("AttachAgent")
		return AttachedTo{}, observability.NewError(observability.CodeUsage, "unknown attach mode "+string(target.Mode))
	}
	if mode == AttachAgent {
		f.AttachModes = append(f.AttachModes, mode)
		f.record("AttachAgent:" + string(mode))
		return AttachedTo{Mode: AttachAgent, AgentName: live.Handle.Name, PaneID: live.Handle.Tab.PaneID}, nil
	}
	pane := strings.TrimSpace(handle.Tab.PaneID)
	if pane == "" && strings.TrimSpace(handle.Tab.TerminalID) == "" {
		f.record("AttachAgent:terminal")
		return AttachedTo{}, NewHerdrError(HerdrAgentNotFound, "agent target not found")
	}
	if occupant, ok := f.agentInPane(pane); ok {
		f.record("AttachAgent:terminal")
		return AttachedTo{}, observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("pane %s now hosts agent %q, not %q", pane, occupant, handle.Name)).
			WithDetails(map[string]any{"pane": pane, "pane_agent": occupant, "recorded_agent": handle.Name})
	}
	f.AttachModes = append(f.AttachModes, mode)
	f.record("AttachAgent:" + string(mode))
	return AttachedTo{Mode: AttachTerminal, PaneID: pane, TerminalID: f.terminalForPane(pane, handle)}, nil
}

// agentInPane reports the live agent occupying pane, if any.
func (f *Fake) agentInPane(pane string) (string, bool) {
	if pane == "" {
		return "", false
	}
	for _, ag := range f.Agents {
		if ag != nil && ag.Handle.Tab.PaneID == pane {
			return ag.Handle.Name, true
		}
	}
	return "", false
}

func (f *Fake) terminalForPane(pane string, handle AgentHandle) string {
	if tab, ok := f.Tabs[pane]; ok && tab.TerminalID != "" {
		return tab.TerminalID
	}
	return handle.Tab.TerminalID
}

// StopAgent implements Adapter.
func (f *Fake) StopAgent(ctx context.Context, handle AgentHandle, mode StopMode) error {
	// A cancelled context fails the call before it reaches Herdr, as the
	// real adapter's exec does.
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("StopAgent:" + string(mode))
	if f.StopErr != nil {
		return f.StopErr
	}
	key := handle.Session.Name + "/" + handle.Name
	ag, ok := f.Agents[key]
	if !ok {
		return NewHerdrError(HerdrAgentNotFound, "agent target not found")
	}
	if mode == StopGraceful && ag.Handle.Kind != harness.KindClaude {
		return observability.NewError(observability.CodeUnknown, "graceful stop is harness-specific and unproven for this kind")
	}
	if f.StopLeavesAgent {
		return nil
	}
	delete(f.Agents, key)
	f.Names.Release(handle.Session.Name, handle.Name)
	if mode == StopForce {
		f.closePaneLocked(handle.Tab.PaneID)
	}
	return nil
}

// RemoveTab implements Adapter.
func (f *Fake) RemoveTab(ctx context.Context, handle TabHandle) error {
	// A cancelled context fails the call before it reaches Herdr, as the
	// real adapter's exec does.
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("RemoveTab")
	if f.RemoveTabErr != nil {
		return f.RemoveTabErr
	}
	if _, ok := f.Tabs[handle.PaneID]; !ok {
		return NewHerdrError(HerdrTabNotFound, "tab/pane not found; re-check live handles (closed-id reuse is unproven)")
	}
	f.closePaneLocked(handle.PaneID)
	return nil
}

// ClosePane simulates `herdr pane close` behind mate's back: the pane is
// gone, agents on it are gone, and if it was the last pane the workspace
// is gone too (live Herdr closes a workspace with its last pane).
func (f *Fake) ClosePane(paneID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closePaneLocked(paneID)
}

func (f *Fake) closePaneLocked(paneID string) {
	if paneID == "" {
		return
	}
	tab, ok := f.Tabs[paneID]
	if !ok {
		return
	}
	delete(f.Tabs, paneID)
	for k, ag := range f.Agents {
		if ag != nil && ag.Handle.Tab.PaneID == paneID {
			delete(f.Agents, k)
			f.Names.Release(ag.Handle.Session.Name, ag.Handle.Name)
		}
	}
	for _, remaining := range f.Tabs {
		if remaining.WorkspaceID == tab.WorkspaceID {
			return
		}
	}
	for k, ws := range f.Workspaces {
		if ws.WorkspaceID == tab.WorkspaceID {
			delete(f.Workspaces, k)
		}
	}
}

// SetAgentStatus is a test helper for wait/blocked injection.
func (f *Fake) SetAgentStatus(session, name string, status AgentStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ag, ok := f.Agents[session+"/"+name]; ok {
		ag.Status = status
	}
}

// DropPane simulates a stale persisted handle.
func (f *Fake) DropPane(paneID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Tabs, paneID)
}

// PutAgent injects a live agent without going through StartAgent. Tests use
// it for the "Herdr has one, state has none" disagreement.
func (f *Fake) PutAgent(handle AgentHandle, status AgentStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if handle.Tab.PaneID != "" {
		if _, ok := f.Tabs[handle.Tab.PaneID]; !ok {
			f.Tabs[handle.Tab.PaneID] = handle.Tab
		}
	}
	f.Agents[handle.Session.Name+"/"+handle.Name] = &fakeAgent{
		Handle:      handle,
		Status:      status,
		Interactive: true,
	}
}

// DropAgent removes a live name without going through StopAgent, so a test
// can model a harness that exited between start and wait.
func (f *Fake) DropAgent(handle AgentHandle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Agents, handle.Session.Name+"/"+handle.Name)
}

// LiveAgentCount is the number of agents currently in the fake inventory.
// AgentPane returns the pane a live agent occupies, or "" if no agent of
// that name is live. Tests that assert a panel was destroyed need the pane
// id before the stop, because the agent is gone by the time they can ask.
func (f *Fake) AgentPane(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Agents is keyed by session/name; match on the handle so a caller that
	// only has the agent name (what a start result reports) can ask.
	for _, ag := range f.Agents {
		if ag.Handle.Name == name {
			return ag.Handle.Tab.PaneID
		}
	}
	return ""
}

// PaneExists reports whether a pane is still open, which is how a test tells
// "the agent stopped" from "the panel is gone" - a graceful stop ends the
// process and leaves the pane behind.
func (f *Fake) PaneExists(paneID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.Tabs[paneID]
	return ok
}

func (f *Fake) LiveAgentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Agents)
}

// LiveNames returns the live agent names in session, sorted by insertion
// is not guaranteed; callers should treat the set as unordered.
func (f *Fake) LiveNames(session string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0)
	for _, ag := range f.Agents {
		if ag.Handle.Session.Name == session {
			out = append(out, ag.Handle.Name)
		}
	}
	return out
}
