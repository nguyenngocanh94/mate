package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/config"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
)

// Adapter is the only owner of Herdr integration. It does not update domain
// state; the application service persists bindings and audit events.
type Adapter interface {
	EnsureSession(ctx context.Context, spec SessionSpec) (SessionHandle, error)
	// LookupSession binds a named session if it is already running. It does
	// not write the owner marker and does not start a server. Status and
	// stop use this so a diagnostic cannot provision a Herdr process.
	LookupSession(ctx context.Context, spec SessionSpec) (SessionHandle, bool, error)
	EnsureProjectWorkspace(ctx context.Context, spec WorkspaceSpec) (WorkspaceHandle, error)
	// LookupProjectWorkspace finds a workspace by label+cwd. It does not
	// create one. Status uses this so a diagnostic cannot provision a slot.
	LookupProjectWorkspace(ctx context.Context, spec WorkspaceSpec) (WorkspaceHandle, bool, error)
	CreateAgentTab(ctx context.Context, spec TabSpec) (TabHandle, error)
	StartAgent(ctx context.Context, spec AgentStartSpec) (AgentHandle, error)
	InspectAgent(ctx context.Context, handle AgentHandle) (ObservedAgent, error)
	// ReadAgent returns a bounded recent terminal snapshot. It is deliberately
	// separate from InspectAgent: pane output is live runtime data, not state.
	ReadAgent(ctx context.Context, handle AgentHandle, lines int) (string, error)
	// ReadAgentStyled is the same snapshot with the harness's own SGR
	// attributes still in it. It exists because one question cannot be
	// answered without them: whether the text in a composer is a person's
	// unsubmitted line or the harness's own faint suggestion, which are the
	// same characters and differ only by being drawn dim (docs/mvp.md
	// section 7, measured 2026-09-19). Everything a human reads uses
	// ReadAgent; only the composer classifier needs this.
	ReadAgentStyled(ctx context.Context, handle AgentHandle, lines int) (string, error)
	// ListAgents is the live inventory of one named session. Identity and
	// occupancy questions must be answered from this list (and Inspect of a
	// recorded name), never from focus, most-recent, or whoever currently
	// occupies a recorded pane.
	ListAgents(ctx context.Context, session SessionHandle) ([]ObservedAgent, error)
	WaitAgent(ctx context.Context, handle AgentHandle, until WaitCondition) (ObservedAgent, error)
	PromptAgent(ctx context.Context, handle AgentHandle, text string) error
	// SendKeys presses named keys (Herdr `pane send-keys` names such as
	// "down", "1", "enter") into the pane the agent currently occupies. It
	// exists for exactly one caller - the startup-prompt settle step that
	// answers a recognised directory-trust dialog (ADR 0028) - and is not a
	// general typing primitive: PromptAgent is how text reaches a harness.
	SendKeys(ctx context.Context, handle AgentHandle, keys []string) error
	// SendText types one line of literal text into the pane the agent
	// currently occupies, without submitting it. It is the typing half of
	// the verified send in internal/send: PromptAgent (`herdr agent
	// prompt`) reports success while the prompt is concatenated onto a
	// half-typed line or swallowed by a modal (docs/mvp.md section 7), so
	// mate types the text itself, re-reads the composer, and presses enter
	// separately.
	SendText(ctx context.Context, handle AgentHandle, text string) error
	AttachAgent(ctx context.Context, handle AgentHandle, target AttachTarget) (AttachedTo, error)
	StopAgent(ctx context.Context, handle AgentHandle, mode StopMode) error
	RemoveTab(ctx context.Context, handle TabHandle) error
}

// ReadAdapter is the deliberately narrow runtime port used by diagnostics
// and Crew health observation. Keeping it separate from Adapter makes the
// read-only boundary visible at the type level: an observer cannot start,
// stop, prompt, attach, or remove anything through this port.
type ReadAdapter interface {
	LookupSession(ctx context.Context, spec SessionSpec) (SessionHandle, bool, error)
	ListAgents(ctx context.Context, session SessionHandle) ([]ObservedAgent, error)
	InspectAgent(ctx context.Context, handle AgentHandle) (ObservedAgent, error)
}

// ReadOnlyAdapter is the descriptive alias used by health/diagnostic code.
// ReadAdapter remains the short form for existing callers.
type ReadOnlyAdapter = ReadAdapter

var _ ReadAdapter = (*Herdr)(nil)
var _ ReadAdapter = (*Fake)(nil)

// SessionSpec names a Herdr session for one app Workspace. Name, when empty,
// is derived from the workspace id (never from a folder basename).
type SessionSpec struct {
	WorkspaceID WorkspaceID
	Name        string
	ConfigHome  string // $HERDR_CONFIG_HOME / XDG config home; used for socket paths
	// Names reserves the derived session name. Required whenever the name is
	// derived from WorkspaceID, because the slug is not injective.
	Names     LiveNameRegistry
	Collision CollisionPolicy
}

// SessionHandle is an opaque named-session binding.
type SessionHandle struct {
	Name       string
	SocketPath string
	ConfigHome string
}

// WorkspaceSpec creates a Herdr workspace (Project) inside a named session.
// Headless Herdr starts with zero workspaces; callers must create one.
type WorkspaceSpec struct {
	Session SessionHandle
	Label   string
	Cwd     string
	// Env is allowlisted MATEV2_* for the workspace-create root pane (the Mate
	// tab). Crew env still goes through TabSpec on tab create.
	Env []EnvVar
}

// WorkspaceHandle is an opaque Herdr workspace id (for example w1).
// RootTab is the tab Herdr creates with the workspace (label "1" on 0.8.2).
// Mate uses that tab; Crew uses a later CreateAgentTab.
type WorkspaceHandle struct {
	Session     SessionHandle
	WorkspaceID string
	Label       string
	Cwd         string
	RootTab     TabHandle
}

// TabSpec creates a Mate or Crew tab. Env is allowlisted MATEV2_* keys only.
type TabSpec struct {
	Workspace WorkspaceHandle
	Label     string
	Cwd       string
	Env       []EnvVar
}

// EnvVar is a single allowlisted environment assignment.
type EnvVar struct {
	Key   string
	Value string
}

// TabHandle stores opaque Herdr tab/pane/terminal ids. Closed ids are
// unproven-not-reused; Inspect/Start must re-check live handles rather than
// trust a persisted id.
type TabHandle struct {
	Session     SessionHandle
	WorkspaceID string
	TabID       string
	PaneID      string
	TerminalID  string
	Cwd         string
	Label       string
	Env         []EnvVar // allowlisted MATEV2_* only
}

// AgentStartSpec is a validated plan to start a harness process *through
// Herdr*. Extra harness args come from a validated LaunchSpec; the adapter
// never forks the harness itself.
//
// The exact guarantee (Go cannot promise more): the only implementation is
// the unexported, sealed agentStartSpec, so every non-nil AgentStartSpec was
// produced by NewAgentStartSpec and passed its validation. The one value Go
// still lets an outside caller hand an adapter is a nil interface, so
// refusing nil is the entire boundary obligation; Validate stays available
// as defence in depth and is trivially nil on a constructor-produced value.
//
// The spec carries no redundant fields a caller could set inconsistently
// (G1 lesson): the harness kind is the launch spec's kind, the session is
// the tab's session, and the live name arrives only as a NameReservation
// binding it to the raw domain id that owns it — a kind mismatch, a
// cross-session start, or a name without its owner cannot be expressed.
type AgentStartSpec interface {
	// Session is the named session the tab (and therefore the start) lives in.
	Session() SessionHandle
	// Tab is the pane the harness process starts in.
	Tab() TabHandle
	// Name is the reserved live agent name.
	Name() string
	// RawID is the domain agent id that owns Name.
	RawID() string
	// Kind is the harness kind, derived from the launch spec so it cannot
	// disagree with the argv the spec validated.
	Kind() harness.Kind
	// Launch is the validated harness launch plan.
	Launch() harness.LaunchSpec
	// Timeout is passed to `agent start --timeout`. G1 never observed expiry
	// when the process never reached blocked or idle; treat that path as
	// unproven (see AssumptionStartTimeoutNeverReady).
	Timeout() time.Duration
	// Validate re-checks the constructor invariants (defence in depth).
	Validate() error
	// sealed keeps every implementation inside this package.
	sealed()
}

// NewAgentStartSpec is the only producer of an AgentStartSpec. It fails
// closed on a zero reservation, a reservation from another session, an
// unstartable launch spec, a pane cwd that disagrees with the cwd the launch
// spec was validated against, or a timeout outside Herdr's documented
// --timeout range (zero is valid and emits no flag). On error the returned
// spec is nil, so a failed construction cannot be started.
func NewAgentStartSpec(tab TabHandle, reservation NameReservation, launch harness.LaunchSpec, timeout time.Duration) (AgentStartSpec, error) {
	spec := agentStartSpec{tab: tab, reservation: reservation, launch: launch, timeout: timeout}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return spec, nil
}

// agentStartSpec is the sealed AgentStartSpec implementation.
type agentStartSpec struct {
	tab         TabHandle
	reservation NameReservation
	launch      harness.LaunchSpec
	timeout     time.Duration
}

func (s agentStartSpec) sealed() {}

func (s agentStartSpec) Session() SessionHandle { return s.tab.Session }

func (s agentStartSpec) Tab() TabHandle { return s.tab }

func (s agentStartSpec) Name() string { return s.reservation.Name() }

func (s agentStartSpec) RawID() string { return s.reservation.RawID() }

func (s agentStartSpec) Kind() harness.Kind { return harness.Kind(s.launch.Kind()) }

func (s agentStartSpec) Launch() harness.LaunchSpec { return s.launch }

func (s agentStartSpec) Timeout() time.Duration { return s.timeout }

// CheckWorkspaceSpec is the shared precondition for creating a Herdr
// workspace: a named session and the absolute directory the Project lives in.
func CheckWorkspaceSpec(spec WorkspaceSpec) error {
	if strings.TrimSpace(spec.Session.Name) == "" {
		return observability.NewError(observability.CodeUsage, "workspace requires a named session")
	}
	if strings.TrimSpace(spec.Label) == "" {
		return observability.NewError(observability.CodeUsage, "workspace requires a label")
	}
	if _, err := AllowlistedEnv(spec.Env); err != nil {
		return err
	}
	return checkAgentCwd("workspace", spec.Cwd)
}

// CheckTabSpec is the shared precondition for creating an agent tab: the
// pane's cwd is where the agent process will discover its context, so it must
// be an absolute path.
func CheckTabSpec(spec TabSpec) error {
	if strings.TrimSpace(spec.Workspace.Session.Name) == "" {
		return observability.NewError(observability.CodeUsage, "tab requires a named session")
	}
	if strings.TrimSpace(spec.Workspace.WorkspaceID) == "" {
		return observability.NewError(observability.CodeUsage, "tab requires a workspace")
	}
	if _, err := AllowlistedEnv(spec.Env); err != nil {
		return err
	}
	return checkAgentCwd("tab", spec.Cwd)
}

func checkAgentCwd(what, cwd string) error {
	clean := filepath.Clean(strings.TrimSpace(cwd))
	if strings.TrimSpace(cwd) == "" || !filepath.IsAbs(clean) {
		return observability.NewError(
			observability.CodeUsage,
			what+" cwd must be an absolute path (the directory the agent will run in)",
		)
	}
	return nil
}

// Validate is defence in depth. NewAgentStartSpec already runs it, so a
// constructed spec always passes. The LaunchSpec was validated against one
// absolute cwd; the pane is where the process actually runs. Codex discovers
// its context from that cwd and does not fail on a missing project file, so
// a disagreement would start an agent with none of its required context.
func (s agentStartSpec) Validate() error {
	if !s.launch.Startable() {
		return observability.NewError(observability.CodeUsage, "launch spec is not startable")
	}
	// Startable is only a shape check. Re-validate required-context delivery
	// here, so a launch spec whose context stopped being deliverable after
	// BuildLaunchSpec - or one hand-assembled inside the harness package -
	// cannot reach StartAgent however it was made.
	if err := s.launch.ValidateRequiredContext(); err != nil {
		return err
	}
	if s.reservation.IsZero() {
		return observability.NewError(observability.CodeUsage, "start requires a name reserved through AllocateAgentName (the reservation binds the name to the raw id that owns it)")
	}
	if strings.TrimSpace(s.tab.PaneID) == "" {
		return observability.NewError(observability.CodeUsage, "start requires a pane")
	}
	if strings.TrimSpace(s.tab.Session.Name) == "" {
		return observability.NewError(observability.CodeUsage, "start requires a named session")
	}
	if s.reservation.Session() != s.tab.Session.Name {
		return observability.NewError(
			observability.CodeUsage,
			fmt.Sprintf("agent name %q is reserved in session %q but the pane lives in session %q", s.reservation.Name(), s.reservation.Session(), s.tab.Session.Name),
		)
	}
	if err := checkAgentCwd("pane", s.tab.Cwd); err != nil {
		return err
	}
	if err := CheckStartTimeout(s.timeout); err != nil {
		return err
	}
	launchCwd := filepath.Clean(s.launch.Cwd())
	paneCwd := filepath.Clean(strings.TrimSpace(s.tab.Cwd))
	// Herdr reports the pane cwd with symlinks resolved (macOS /var -> /private/var);
	// the launch spec may hold the unresolved spelling. Same directory is what matters.
	if !samePath(launchCwd, paneCwd) {
		return observability.NewError(
			observability.CodeUsage,
			fmt.Sprintf("launch spec was validated at cwd %s but the pane runs in %s; required context would not be discovered", launchCwd, paneCwd),
		)
	}
	return nil
}

// AgentHandle identifies a live Herdr agent.
type AgentHandle struct {
	Session SessionHandle
	Name    string
	RawID   string
	Kind    harness.Kind
	Tab     TabHandle
}

// ObservedAgent is a runtime fact, not a domain state transition.
type ObservedAgent struct {
	Handle AgentHandle
	// SessionRef is the harness session Herdr records for this agent
	// (agent_session.value): the rollout session uuid for Codex, empty for
	// Claude and for any agent Herdr has no session for. It is a runtime
	// fact like the rest of this struct - the timeline's transcript locator
	// asks for it - and an empty value means "Herdr did not say", never "no
	// session exists".
	SessionRef    string
	Status        AgentStatus
	LaunchPending bool
	Interactive   bool
	ObservedAt    time.Time
	LiveHandleOK  bool // false if the persisted tab/pane id is gone (must re-check)
	UnprovenNotes []string
}

// AttachTarget selects how Console/CLI attach. Agent attach streams the
// server-owned terminal. Terminal attach is the equivalent for a pane with
// no agent. The zero value is AttachDetect: the adapter asks Herdr which of
// the two the pane actually is rather than assuming.
type AttachTarget struct {
	Mode AttachMode
}

// AttachedTo is what an attach actually connected to, read from Herdr at
// attach time. It exists because the recorded handles can be wrong: the
// agent name may be dead, and the recorded pane may since have been taken by
// a different agent. A caller reports these values, never the recorded ones,
// so an envelope or an audit row cannot name something that was not
// attached (PR #9 counter-review, B1).
type AttachedTo struct {
	// Mode is the attach that was actually performed: agent or terminal.
	Mode AttachMode
	// AgentName is the live agent whose terminal was streamed. It is empty
	// for a terminal attach, which connected to no agent at all.
	AgentName string
	// PaneID is the pane that was attached, as Herdr reported it.
	PaneID string
	// TerminalID is the terminal that was attached. It is empty for an agent
	// attach, where Herdr resolves the terminal from the agent name.
	TerminalID string
}

// AttachMode is agent vs terminal attach, or the detection that picks
// between them.
type AttachMode string

const (
	// AttachDetect (the zero value) asks Herdr whether the agent name is
	// live and falls back to the pane's terminal when it is not.
	AttachDetect   AttachMode = ""
	AttachAgent    AttachMode = "agent"
	AttachTerminal AttachMode = "terminal"
)

// StopMode is graceful (harness-specific capability) vs force (pane/tab close).
// RuntimeAdapter must not hardcode send-keys for every harness. Claude
// graceful stop is `agent prompt /exit`; that is a capability of the first
// harness, not a generic Herdr API (there is no `herdr agent stop`).
type StopMode string

const (
	StopGraceful StopMode = "graceful"
	StopForce    StopMode = "force"
)

// AssumptionStatus records G1 evidence honesty for contracts that would
// otherwise silently assume the convenient case.
type AssumptionStatus string

const (
	AssumptionProven    AssumptionStatus = "proven"
	AssumptionUnproven  AssumptionStatus = "unproven"
	AssumptionDisproven AssumptionStatus = "disproven"
)

// Assumption is a named G1 leftover that G2 must not paper over.
type Assumption struct {
	ID      string
	Status  AssumptionStatus
	Summary string
}

const (
	AssumptionGhosttyTTYRestore        = "ghostty_tty_restore_after_execprocess"
	AssumptionGhosttySignalMatrix      = "ghostty_sigterm_sigint_attach"
	AssumptionPaneResizePersist        = "pane_resize_persists_after_detach"
	AssumptionStartTimeoutNeverReady   = "agent_start_timeout_never_ready"
	AssumptionClosedIDReuse            = "closed_herdr_ids_not_reused"
	AssumptionClaudeArgsThroughHerdr   = "claude_flags_survive_agent_start_dash_dash"
	AssumptionMateExpiryVsWaitTimeout  = "mate_interaction_expiry_vs_herdr_wait_timeout"
	AssumptionWindowsDirectAttach      = "windows_direct_attach"
	AssumptionCodexChainSeparatorBytes = "codex_chain_separator_bytes_count_against_max"
)

// KnownAssumptions is the G1 leftover list. Contracts that depend on one of
// these must either probe, document a failure path, or not need the answer.
func KnownAssumptions() []Assumption {
	return []Assumption{
		{
			ID:      AssumptionGhosttyTTYRestore,
			Status:  AssumptionUnproven,
			Summary: "Ghostty-native TTY restore, IME, and mouse after tea.ExecProcess were not driven; PTY + unit tests only.",
		},
		{
			ID:      AssumptionGhosttySignalMatrix,
			Status:  AssumptionUnproven,
			Summary: "Systematic SIGTERM/SIGINT matrix on Ghostty for herdr agent attach is unproven. One accidental attach-child death left agents running.",
		},
		{
			ID:      AssumptionPaneResizePersist,
			Status:  AssumptionDisproven,
			Summary: "Direct-attach WINCH size does not persist into the headless virtual terminal after detach.",
		},
		{
			ID:      AssumptionStartTimeoutNeverReady,
			Status:  AssumptionUnproven,
			Summary: "agent start --timeout when the process never reaches blocked or idle was not observed.",
		},
		{
			ID:      AssumptionClosedIDReuse,
			Status:  AssumptionUnproven,
			Summary: "Closed Herdr tab/pane IDs are stated not to be reused; G1 did not re-exercise this. Re-check live handles before using a persisted id.",
		},
		{
			ID:      AssumptionClaudeArgsThroughHerdr,
			Status:  AssumptionProven,
			Summary: "The pinned live lab proved Claude --session-id, --settings and --append-system-prompt-file survive herdr agent start; the configured Stop hook fired with the launched session id.",
		},
		{
			ID:      AssumptionMateExpiryVsWaitTimeout,
			Status:  AssumptionUnproven,
			Summary: "Mate interaction expiry vs Herdr wait timeout belongs to G5; not proven in G1.",
		},
		{
			ID:      AssumptionCodexChainSeparatorBytes,
			Status:  AssumptionDisproven,
			Summary: "Live probe 2026-09-01 (codex-cli 0.151.0, codex debug prompt-input): project_doc_max_bytes meters only project files' raw content bytes; the per-file joiners, the --- project-doc --- marker, and the global $CODEX_HOME doc are rendered after metering and never charged. harness.CodexChain derives the budget from that metered stream.",
		},
		{
			ID:      AssumptionWindowsDirectAttach,
			Status:  AssumptionUnproven,
			Summary: "Windows direct attach is documented Unix-only and was not probed.",
		},
	}
}

// sessionNameMax is the byte cap for a Herdr named session slug.
const sessionNameMax = 48

// SessionNameScope is the registry scope for Herdr named sessions. Session
// names are global to a Herdr install, so they share one scope rather than
// being keyed per session like agent names.
const SessionNameScope = "\x00herdr-sessions"

// AllocateSessionName reserves a session name for a Workspace through the
// live-name registry. Derived names hash the raw workspace id (case-sensitive)
// so ws_A1 and ws_a1 no longer share a slug. The registry is still required
// for explicit SessionSpec.Name values. persistence.NameRegistry refuses this
// scope by design; cross-process ownership of derived names is the hash,
// and of every name is the config-home owner marker (ADR 0007).
func AllocateSessionName(reg LiveNameRegistry, id WorkspaceID, policy CollisionPolicy) (string, error) {
	if reg == nil {
		return "", fmt.Errorf("allocate session name: live-name registry is required")
	}
	raw := strings.TrimSpace(id.String())
	if raw == "" {
		return "", fmt.Errorf("allocate session name: workspace id is required")
	}
	for attempt := 0; ; attempt++ {
		name := sessionNameWithNonce(SessionNameForWorkspace(id), attempt)
		err := reg.Reserve(SessionNameScope, name, raw)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, ErrNameCollision) {
			return "", err
		}
		if policy != RetryWithNonce || attempt >= maxCollisionRetries {
			return "", observability.WrapError(
				observability.CodeAlreadyExists,
				fmt.Sprintf("session name %q is already live for a different workspace (raw %q)", name, raw),
				ErrNameCollision,
			)
		}
	}
}

// SessionNameForWorkspace is a stable opaque slug. It does not use a folder
// basename because Herdr named sessions are global.
//
// The name is "mate-" plus 32 hex characters of SHA-256 over the raw
// workspace id (case-sensitive, not folded). That width is 128 bits, so
// distinct Mate-generated ids including the ADR 0004 pair ws_A1 / ws_a1
// produce distinct session addresses. It is still a hash, not a Go-level
// injection; SHA-256 collision remains the residual. Explicit SessionSpec.Name
// is not derived this way and can still join an existing server; EnsureSession
// writes a config-home owner marker for that case.
func SessionNameForWorkspace(id WorkspaceID) string {
	raw := strings.TrimSpace(id.String())
	if raw == "" {
		return "mate-workspace"
	}
	sum := sha256.Sum256([]byte("herdr-session\x00" + raw))
	name := "mate-" + hex.EncodeToString(sum[:16])
	if len(name) > sessionNameMax {
		name = name[:sessionNameMax]
	}
	return name
}

// sessionAddress fills Name and socket path without reserving the name or
// writing an owner marker. LookupSession uses this so a diagnostic cannot
// provision. The default session is never selected as a workspace mapping.
func sessionAddress(spec SessionSpec) (SessionHandle, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "default" {
		return SessionHandle{}, fmt.Errorf("session: refusing to bind an app Workspace to the Herdr default session")
	}
	home := strings.TrimSpace(spec.ConfigHome)
	if home == "" {
		return SessionHandle{}, fmt.Errorf("session: config home is required to resolve the socket path")
	}
	if !filepath.IsAbs(home) {
		return SessionHandle{}, fmt.Errorf("session: config home %q must be absolute", home)
	}
	if name == "" {
		if spec.WorkspaceID.String() == "" {
			return SessionHandle{}, fmt.Errorf("session: workspace id or name is required")
		}
		name = SessionNameForWorkspace(spec.WorkspaceID)
	} else if err := checkSessionName(name); err != nil {
		return SessionHandle{}, err
	}
	if strings.TrimSpace(spec.WorkspaceID.String()) == "" {
		return SessionHandle{}, fmt.Errorf("session: an explicit name requires the owning workspace id")
	}
	if err := checkSessionName(name); err != nil {
		return SessionHandle{}, err
	}
	socket, err := namedSocketPath(home, name)
	if err != nil {
		return SessionHandle{}, err
	}
	return SessionHandle{Name: name, SocketPath: socket, ConfigHome: home}, nil
}

// ResolveSession fills Name and socket path. The default session is never
// selected as a workspace mapping.
func ResolveSession(spec SessionSpec) (SessionHandle, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "default" {
		return SessionHandle{}, fmt.Errorf("session: refusing to bind an app Workspace to the Herdr default session")
	}
	if name != "" {
		if err := checkSessionName(name); err != nil {
			return SessionHandle{}, err
		}
	}
	home := strings.TrimSpace(spec.ConfigHome)
	if home == "" {
		return SessionHandle{}, fmt.Errorf("session: config home is required to resolve the socket path")
	}
	if !filepath.IsAbs(home) {
		return SessionHandle{}, fmt.Errorf("session: config home %q must be absolute", home)
	}
	switch {
	case name == "":
		if spec.WorkspaceID.String() == "" {
			return SessionHandle{}, fmt.Errorf("session: workspace id or name is required")
		}
		allocated, err := AllocateSessionName(spec.Names, spec.WorkspaceID, spec.Collision)
		if err != nil {
			return SessionHandle{}, err
		}
		name = allocated
	default:
		// An explicit name carries the same ownership contract as a derived
		// one: it is reserved in the live-name registry for the owning
		// workspace, so within one registry instance two workspaces cannot
		// silently bind the same Herdr session (counter-review of PR #3).
		// The cross-process contract a backer owes is in ADR 0004, Runtime
		// port; the per-workspace persistence.NameRegistry (ADR 0005)
		// refuses this session scope, so a backer that arbitrates session
		// names is still a G4 decision. Same-owner re-resolution is
		// idempotent.
		raw := strings.TrimSpace(spec.WorkspaceID.String())
		if raw == "" {
			return SessionHandle{}, fmt.Errorf("session: an explicit name requires the owning workspace id")
		}
		if spec.Names == nil {
			return SessionHandle{}, fmt.Errorf("session: an explicit name requires the live-name registry; uniqueness cannot be assumed")
		}
		if err := spec.Names.Reserve(SessionNameScope, name, raw); err != nil {
			if errors.Is(err, ErrNameCollision) {
				return SessionHandle{}, observability.WrapError(
					observability.CodeAlreadyExists,
					fmt.Sprintf("session name %q is already live for a different workspace (raw %q)", name, raw),
					ErrNameCollision,
				)
			}
			return SessionHandle{}, err
		}
	}
	if err := checkSessionName(name); err != nil {
		if spec.Names != nil {
			spec.Names.Release(SessionNameScope, name)
		}
		return SessionHandle{}, err
	}
	socket, err := namedSocketPath(home, name)
	if err != nil {
		if spec.Names != nil {
			spec.Names.Release(SessionNameScope, name)
		}
		return SessionHandle{}, err
	}
	return SessionHandle{
		Name:       name,
		SocketPath: socket,
		ConfigHome: home,
	}, nil
}

// sessionNameWithNonce appends the retry nonce after truncation and shortens
// the readable part instead, so the nonce can never be cut off and a retry
// always produces a different name.
func sessionNameWithNonce(base string, attempt int) string {
	if attempt == 0 {
		return base
	}
	suffix := fmt.Sprintf("-%d", attempt)
	room := sessionNameMax - len(suffix)
	if len(base) > room {
		base = base[:room]
	}
	return strings.Trim(base, "-_") + suffix
}

func checkSessionName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("session: name %q is not a single path segment", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("session: name %q is not a single path segment", name)
	}
	return nil
}

// AllowlistedEnvKeys are identity and provider-root keys injected at pane
// create. HERDR_* keys are injected by Herdr itself and must not be set by
// Mate. Unknown keys are refused rather than dropped.
func AllowlistedEnvKeys() []string {
	return config.LaunchEnvKeys()
}

// DetachKey is the proven Herdr 0.8.2 detach binding. Detach does not stop
// the agent. Ghostty-native TTY restore after tea.ExecProcess is unproven.
const DetachKey = "ctrl+b q"
