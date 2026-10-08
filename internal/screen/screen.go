// Package screen names what one harness pane shows: an Observation, and
// the Observer that makes one.
//
// An Observation is an observation, not a verdict. It says what is on the
// screen - the composer's state, a dialog and where its highlight is - and
// never what to do about it. Whether to type, wait, answer a dialog or
// refuse is the caller's policy (internal/send before it types,
// internal/watch for the health column, internal/spawn's startup settle),
// and stays in the caller's code where it is tested, whichever Observer
// read the pane (docs/plans/jev-observer-2026-10-08.md sections 4.1, 4.2).
//
// The default Observer is internal/screen/fixture, which reads the screen
// through the harness's own measured harness.ScreenProfile and never calls
// the network.
package screen

import (
	"context"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// ComposerState names what a harness pane is showing, as far as typing one
// line into it is concerned. Four values, because the three a caller may not
// type into are genuinely different and the caller answers them differently:
// Busy waits, Draft belongs to whoever typed it, and Unknown is a screen
// the observer cannot read at all.
type ComposerState string

const (
	// ComposerEmpty is the harness composer drawn with nothing in it (or
	// only its placeholder or its own faint suggestion).
	ComposerEmpty ComposerState = "empty"
	// ComposerDraft is the composer drawn with text after the prompt glyph:
	// a human mid-typing, or an earlier send whose Enter was swallowed.
	ComposerDraft ComposerState = "draft"
	// ComposerBusy is the harness's own mid-turn signature on screen. The
	// composer may look empty underneath it.
	ComposerBusy ComposerState = "busy"
	// ComposerUnknown is any screen with no recognised composer: a dialog,
	// a scrolled transcript, a harness still drawing itself.
	ComposerUnknown ComposerState = "unknown"
)

func (s ComposerState) String() string { return string(s) }

// DialogKind names a modal drawn over the pane.
type DialogKind string

const (
	// DialogNone is a pane with no dialog over it.
	DialogNone DialogKind = "none"
	// DialogTrust is the harness's per-directory trust confirmation.
	DialogTrust DialogKind = "trust"
	// DialogUpdate is the harness's release-update prompt.
	DialogUpdate DialogKind = "update"
	// DialogHooksReview is a review of hooks the harness will not run until
	// they are trusted.
	DialogHooksReview DialogKind = "hooks-review"
	// DialogPermission is a harness asking permission for a tool call.
	DialogPermission DialogKind = "permission"
	// DialogOther is a dialog the observer can name as one but that is none
	// of the kinds above.
	DialogOther DialogKind = "other"
	// DialogUnknown is a screen the observer cannot say has or lacks a
	// dialog.
	DialogUnknown DialogKind = "unknown"
)

// Observation is what one Observer read off one pane snapshot.
type Observation struct {
	// Composer is the composer's state.
	Composer ComposerState
	// Draft is the text sitting in the composer when Composer is
	// ComposerDraft, so a caller can quote it or compare it with what it
	// typed. It is empty otherwise.
	Draft string
	// Evidence is the trimmed screen line Composer was read from, quoted
	// back in refusals and logs so a caller names the screen text rather
	// than asserting a verdict. It may be empty when nothing on screen
	// supported a reading.
	Evidence string
	// Dialog is the dialog over the pane, if any.
	Dialog DialogKind
	// Startup is the harness's own startup classification of the screen
	// (harness.ScreenProfile.ClassifyStartup), exactly as the profile
	// returned it, so the startup settle keeps answering from the
	// harness's measured key table.
	Startup harness.StartupScreen
	// Highlight is the index of the highlighted option in Dialog when it is
	// the option the harness's StartupAnswer confirms
	// (harness.StartupDialogAnswer.Target), and -1 when that is not seen.
	Highlight int
	// Notice is one of notice.Result's seven labels, or empty. The fixture
	// observer never reads notices and always leaves it empty.
	Notice string
	// Confidence is how sure the observer is, from 0 to 1. The fixture
	// observer says 1 when the harness profile recognised the screen and 0
	// when it did not.
	Confidence float64
	// Source names the observer that made this observation: "fixture",
	// "jev" or "chain".
	Source string
	// Reason says why Composer or Dialog is Unknown. It is empty when
	// neither is.
	Reason string
}

// Observer reads one pane snapshot. screen is the pane as read through the
// profile's ReadSource, styled (SGR attributes intact) or plain; an
// Observer must classify a plain screen exactly as it classifies the plain
// rendering of a styled one, except where the attributes are the evidence.
//
// An error means the observer could not read the screen at all, which is
// not an observation: the caller concludes nothing from it.
type Observer interface {
	Observe(ctx context.Context, profile harness.ScreenProfile, screen string) (Observation, error)
}
