package harness

import (
	"context"
	"time"
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
}

// GracefulStopper is a harness's own way to exit.
type GracefulStopper interface {
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
	// before anything is launched.
	Resumable(id string) error
}

// HookInstaller is the hooks a Mate's launch installs. Launcher.Prepare
// names their files; this is what the rest of the core asks about them.
type HookInstaller interface {
	// Own are the hooks a Mate launch in cwd wrote that the harness asks
	// the operator to trust at startup. The startup settle trusts these and
	// nothing else.
	Own(binary, cwd string) []OwnHook
	// DigestMaxBytes bounds the session-start digest the hook prints, to
	// what the harness puts in context whole.
	DigestMaxBytes() int
}

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

// CapStatus is what is known about one capability of one harness. The words
// are the ones the TUI probe plan uses (tui-probe-redesign-2026-09-27.md,
// section 5.1), so the two plans share one vocabulary.
type CapStatus string

const (
	// CapUndeclared is the zero value: the harness never answered. It is
	// always a bug.
	CapUndeclared CapStatus = ""
	// CapVerified means measured on a named harness version; Impl is set.
	CapVerified CapStatus = "verified"
	// CapUnsupported means the harness does not have it; Reason says why.
	CapUnsupported CapStatus = "unsupported"
	// CapUnknown means not measured yet; Reason says what is missing.
	CapUnknown CapStatus = "unknown"
)

// Cap is one capability's declaration. Impl is set exactly when Status is
// CapVerified: code still being measured belongs in tests and labs, not
// under an unknown capability.
type Cap[T any] struct {
	Status   CapStatus
	Impl     T
	Evidence Evidence
	Reason   string
}

// Verified reports whether the capability can be used.
func (c Cap[T]) Verified() bool { return c.Status == CapVerified }

// Evidence is what a verified capability rests on.
type Evidence struct {
	// Version is the harness version it was measured on.
	Version string
	// Measured is when.
	Measured string
	// Proof names the test, capture or record that shows it.
	Proof string
}

// exitPrompt is a GracefulStopper that types one line.
type exitPrompt string

// ExitPrompt implements GracefulStopper.
func (p exitPrompt) ExitPrompt() string { return string(p) }
