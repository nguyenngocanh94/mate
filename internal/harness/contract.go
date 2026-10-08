package harness

import (
	"context"
	"slices"
	"time"

	"github.com/nguyenngocanh94/mate/internal/capability"
)

// The harness contract (docs/plans/harness-registry-2026-09-30.md, section
// 3.2): everything the core may ask about a harness. A harness is a Profile
// in a Registry; the core asks the profile instead of comparing kinds.
//
// The required part is small: who the harness is, how it launches, and how
// its screens read. Everything else is a capability with a named state, so
// a harness that lacks one says so, with a reason, and the core takes a
// named degraded path (plan section 3.7) instead of a silent default branch.

// Profile is everything the core may ask about one harness.
type Profile interface {
	Kind() Kind
	Info() Info
	Launcher() Launcher
	Screen() ScreenProfile
	Capabilities() Capabilities
}

// Info is what a harness is called, to Herdr and on disk.
type Info struct {
	// Name is the harness as people call it ("Claude Code"), for text a
	// Mate or the captain reads.
	Name string
	// RuntimeKind is what Herdr is told at `agent start --kind`.
	RuntimeKind string
	// ConfigDir, InstructionFile and EnvKeys are the names the harness reads
	// from a directory and the environment. Code outside internal/harness
	// that spells one of them knows this harness by name; the harness
	// ratchet (ratchet_test.go) counts those spellings from here, so a
	// harness enters the ratchet by being registered.
	ConfigDir       string
	InstructionFile string
	EnvKeys         []string
	// SkillsDir is where, relative to a Mate's cwd, its skills are written.
	// The manual names it for a harness that does not discover skills on
	// its own, so it is fixed before the manual is rendered.
	SkillsDir string
	// Icon is the harness's one-cell mark, as the Console draws it in each
	// glyph alphabet.
	Icon Icon
	// Documents are the files of a working tree, relative to its top, that
	// shape what the harness does there. A Crew's harness profile records
	// each under its role, for every registered harness: a repo may carry
	// files for a harness its Crew does not run on.
	Documents []Document
	// Efforts are the reasoning-effort levels the launch passes as a flag.
	// A requested effort outside them is recorded and left out of the argv.
	Efforts []Effort
	// AdapterNotes is the harness's section of the Mate's harness-adapters
	// skill: what its pane shows and how a Crew on it is steered, as
	// measured. Markdown with no heading of its own; the skill adds one.
	AdapterNotes string
}

// Icon is a harness's mark: a Nerd Font brand glyph, the Unicode stand-in a
// terminal without that font draws, and the ASCII one for a terminal that
// draws nothing else. Each is one cell.
type Icon struct {
	Nerd    string
	Unicode string
	ASCII   string
}

// Document is a file of a working tree a harness reads, slash-separated
// and relative to the tree's top, and the role a harness profile records it
// under.
type Document struct {
	Path string
	Role string
}

// SupportsEffort reports whether the harness takes e as a launch flag.
func (i Info) SupportsEffort(e Effort) bool {
	return e != "" && slices.Contains(i.Efforts, e)
}

// Launcher lays out and builds a harness's launch (launch.go). It never
// forks the harness; spawn writes the files Prepare names and the runtime
// hands Build's spec to Herdr.
type Launcher interface {
	// Prepare names the files the launch needs and the data Build reads
	// back. It reads the filesystem and writes nothing.
	Prepare(ctx context.Context, req PrepareRequest) (Prepared, error)
	// Build turns the role-generic request into a startable LaunchSpec, or
	// refuses it.
	Build(ctx context.Context, spec AgentSpec) (LaunchSpec, error)
	// PaneEnv is what a Mate's pane is given so that a launch of this
	// harness the Mate makes itself (`mate crew spawn`) runs where mate's
	// own launches of it do. Each key is one of Info().EnvKeys.
	PaneEnv() ([]EnvVar, error)
}

// ScreenProfile recognises a harness's pane: how it is read, its startup
// dialogs, and its composer. It names what the screen shows and leaves what
// to do about it to the caller (startup_prompt.go, composer.go).
//
// Plan PR 3 moved the per-kind tables here unchanged; the TUI probe plan's
// PR 1 reshapes the composer half into an observation the core's policy
// decides from.
type ScreenProfile interface {
	// ReadSource is the pane read this harness's screens were measured
	// through. Every read of its pane uses it, so what is classified is
	// what was measured.
	ReadSource() ReadSource

	// ClassifyStartup names what the pane shows right after launch.
	ClassifyStartup(screen string) StartupScreen
	// StartupAnswer is the measured key sequence that answers one startup
	// dialog, or a refusal when this harness draws no such dialog.
	StartupAnswer(dialog StartupScreen) (StartupDialogAnswer, error)
	// StartupTargetSelected reports whether that dialog is on screen,
	// structurally confirmed, with its highlight on the option
	// StartupAnswer confirms. It is false on any other screen.
	StartupTargetSelected(dialog StartupScreen, screen string) bool
	// ReadyScreen is the smallest screen ClassifyStartup calls ready: what
	// a fake runtime shows for an agent that started cleanly.
	ReadyScreen() string

	// ComposerGlyph is the glyph the harness draws at its composer.
	ComposerGlyph() string
	// Composer returns the composer line's content after the glyph, and
	// whether a composer was found at all.
	Composer(lines []string) (content string, ok bool)
	// ComposerPlaceholders are the measured texts an empty composer holds.
	ComposerPlaceholders() []string
	// ComposerRows returns every row of a composer holding text, the first
	// without its glyph, or false when the composer's extent cannot be
	// read off the screen.
	ComposerRows(lines []string) (rows []string, ok bool)
	// Busy returns the line proving the harness is mid-turn.
	Busy(lines []string) (evidence string, ok bool)
}

// ReadSource is a `herdr agent read --source`.
type ReadSource string

const (
	// ReadRecentUnwrapped is the agent's recent output with wrapped rows
	// joined. While Herdr refuses it for a working agent drawn on the
	// alternate screen, the runtime reads ReadVisible instead
	// (runtime.Herdr.readAgent).
	ReadRecentUnwrapped ReadSource = "recent-unwrapped"
	// ReadVisible is the screen as it is drawn, rows wrapped at the pane
	// width.
	ReadVisible ReadSource = "visible"
)

// Capabilities are the optional parts of the contract. Every field must be
// declared by every harness: the zero Cap is "undeclared", which the
// contract suite (internal/harness/catalog) refuses, so a new field forces
// every registered harness to answer it.
type Capabilities struct {
	// GracefulStop is a stop that lets the harness exit on its own. Without
	// it a stop closes the pane.
	GracefulStop Cap[GracefulStopper]
	// Session is how the harness names a session, so a stopped Mate can be
	// resumed. Without it every start is a fresh session, and says why.
	Session Cap[SessionIdentity]
	// Hooks are the hooks a Mate's memory and inbox rest on (docs/mvp.md
	// tasks 08 and 37). Without them the harness cannot run a Mate; a Crew
	// needs none.
	Hooks Cap[HookInstaller]
	// TurnEnd is evidence that a turn ended other than the composer, which
	// can read empty mid-turn. Without it the composer is the only evidence.
	TurnEnd Cap[TurnEndEvidence]
	// Transcript is the harness's transcript, read into turns, tool calls
	// and usage. Without it the timeline records the agent as unobserved
	// and counts no token for it.
	Transcript Cap[TranscriptSource]
	// Quota names the quota-axi row that measures the harness. Without it
	// dispatch treats the harness as having its whole allowance left.
	Quota Cap[QuotaProvider]
}

// GracefulStopper is a harness's own way to exit.
type GracefulStopper interface {
	// ClearKeys are pressed, one call, before the line is typed: what
	// empties a composer that may hold a draft, for a harness whose exit
	// line is taken only on an empty one. Nil presses nothing.
	ClearKeys() []string
	// ExitPrompt is the line that, typed into the composer, ends the agent.
	ExitPrompt() string
}

// SessionIdentity is how a harness names its sessions. One names it at
// launch, another only once the first prompt opens it, so the core asks at
// stop instead of knowing which (plan section 3.3).
type SessionIdentity interface {
	// AtStop names the session an agent launched in cwd at launched is in,
	// read while it stops: the id the runtime reports for it (runtimeRef),
	// else what the harness wrote on disk. Empty means the harness cannot
	// say, and the caller keeps the id it recorded; a harness that names
	// its session at launch always answers empty.
	AtStop(cwd string, launched time.Time, runtimeRef string) string
	// Resumable is nil when the session id can be resumed, or says why not,
	// before anything is launched: a *NoSessionError when the harness
	// looked and holds no record of it.
	Resumable(id string) error
}

// NoSessionError is Resumable's answer when the harness looked for a
// session and holds no record of it. Session is how the harness names it
// ("the Codex session <id>"); Missing says, as a clause, what is missing
// where. The caller, which knows where the id came from, frames the two.
type NoSessionError struct {
	Session string
	Missing string
}

func (e *NoSessionError) Error() string { return e.Session + ": " + e.Missing }

// HookInstaller is the hooks a Mate's launch installs. Launcher.Prepare
// names their files; this is what the rest of the core asks about them.
type HookInstaller interface {
	// Own are the hooks a Mate launch in cwd wrote that the harness asks
	// the operator to trust at startup. The startup settle trusts these and
	// nothing else.
	Own(binary, cwd string) []OwnHook
	// ReviewOwn walks the harness's review of untrusted hooks, from the
	// StartupScreenHooksReview dialog on screen back to the composer,
	// trusting own and nothing else. A screen the walk does not expect is
	// a *ScreenRefusal, with nothing further pressed. A harness that never
	// asks the operator to trust a hook refuses.
	ReviewOwn(ctx context.Context, pane HookReviewPane, screen string, own []OwnHook) error
	// DigestMaxBytes bounds the session-start digest the hook prints, to
	// what the harness puts in context whole.
	DigestMaxBytes() int
	// Repoint rewrites the mate binary in the hook files of the Mate whose
	// cwd this is, when the binary they name no longer exists (exists
	// reports it): the shape a workspace copied to another machine takes. It
	// keeps every other hook and reports whether it changed anything. A
	// harness whose hook files a launch always rewrites changes nothing.
	Repoint(binary, cwd string, exists func(string) bool) (changed bool, err error)
	// BareSessionHook reports whether the harness's SessionStart hook runs
	// `mate hook mate-session` without --harness, so a hook that names no
	// harness is this one's. At most one registered harness says so.
	BareSessionHook() bool
}

// OwnHook is one hook the settle may trust: mate wrote it, in this file,
// with this command, for this event.
type OwnHook struct {
	Event   string
	Source  string // the absolute path of the hooks.json that holds it
	Command string
}

// HookReviewPane is the pane a hook review is walked in. Whoever settles the
// startup owns it: which pane, read through which source, and how long a
// key press is given to redraw.
type HookReviewPane interface {
	// Read re-reads the screen.
	Read(ctx context.Context) (string, error)
	// Press sends one key and waits for the harness to redraw.
	Press(ctx context.Context, key string) error
	// Wait pauses before a screen still being drawn is read again.
	Wait(ctx context.Context) error
}

// ScreenRefusal is a screen walk stopping on a screen it did not expect,
// before pressing anything into it.
type ScreenRefusal struct {
	Screen string
	Reason string
}

func (e *ScreenRefusal) Error() string { return e.Reason }

// TurnEndEvidence is what, besides the composer, shows that a turn of the
// harness ended.
type TurnEndEvidence interface {
	// LogsAnswers is true when mate's own Stop hook logs the agent's answer
	// to sent.log as the turn ends. An answer logged after a line proves
	// the turn on it ended; a missing one proves nothing, because the hook
	// does not fire on every turn.
	LogsAnswers() bool
	// EndsInTranscript is true when the harness's transcript records the
	// end of every turn. Once the agent's meta names that transcript,
	// TranscriptTurnEnded is then the only rule: an empty composer is not
	// the end of a turn.
	EndsInTranscript() bool
	// TranscriptTurnEnded reports whether the transcript records a turn
	// that ended after after.
	TranscriptTurnEnded(transcript []byte, after time.Time) bool
}

// QuotaProvider names the quota-axi (https://github.com/kunchenguid/axi)
// row that measures a harness's allowance.
type QuotaProvider interface {
	// QuotaProvider is quota-axi's provider name.
	QuotaProvider() string
	// QuotaLane is the account key a schema-6 snapshot files the harness
	// under; empty means the provider's default account.
	QuotaLane() string
}

// Cap, CapStatus and Evidence are internal/capability's, which the tool
// registry shares (docs/plans/workspace-layout-and-tools-2026-10-08.md,
// section 5.2). The names stay here so every harness and call site reads
// as it did.
type (
	// CapStatus is what is known about one capability of one harness.
	CapStatus = capability.CapStatus
	// Cap is one capability's declaration; Impl is set exactly when Status
	// is CapVerified.
	Cap[T any] = capability.Cap[T]
	// Evidence is what a verified capability rests on.
	Evidence = capability.Evidence
)

const (
	// CapUndeclared is the zero value: the harness never answered. It is
	// always a bug.
	CapUndeclared = capability.Undeclared
	// CapVerified means measured on a named harness version; Impl is set.
	CapVerified = capability.Verified
	// CapUnsupported means the harness does not have it; Reason says why.
	CapUnsupported = capability.Unsupported
	// CapUnknown means not measured yet; Reason says what is missing.
	CapUnknown = capability.Unknown
)

// ExitCommand is a GracefulStopper that types one line.
type ExitCommand string

// ClearKeys implements GracefulStopper: the line is typed as the composer
// stands.
func (ExitCommand) ClearKeys() []string { return nil }

// ExitPrompt implements GracefulStopper.
func (p ExitCommand) ExitPrompt() string { return string(p) }

// QuotaRow is a QuotaProvider that names one row.
type QuotaRow struct{ Provider, Lane string }

// QuotaProvider implements QuotaProvider.
func (q QuotaRow) QuotaProvider() string { return q.Provider }

// QuotaLane implements QuotaProvider.
func (q QuotaRow) QuotaLane() string { return q.Lane }
