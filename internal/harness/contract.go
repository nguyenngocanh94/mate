package harness

import "context"

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
}

// Launcher builds a harness's LaunchSpec. It never forks the harness; the
// runtime hands the spec to Herdr.
type Launcher interface {
	BuildLaunchSpec(ctx context.Context, spec AgentSpec) (LaunchSpec, error)
}

// ScreenProfile recognises a harness's pane. It names what the screen shows
// and leaves what to do about it to the caller.
type ScreenProfile interface {
	// ClassifyStartup names what the pane shows right after launch.
	ClassifyStartup(screen string) (StartupScreen, error)
}

// Capabilities are the optional parts of the contract. Every field must be
// declared by every harness: the zero Cap is "undeclared", which the
// contract suite (internal/harness/catalog) refuses, so a new field forces
// every registered harness to answer it.
type Capabilities struct {
	// GracefulStop is a stop that lets the harness exit on its own. Without
	// it a stop closes the pane.
	GracefulStop Cap[GracefulStopper]
}

// GracefulStopper is a harness's own way to exit.
type GracefulStopper interface {
	// ExitPrompt is the line that, typed into the composer, ends the agent.
	ExitPrompt() string
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
