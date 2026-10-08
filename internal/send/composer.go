package send

import (
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/fixture"
)

// ComposerState is screen.ComposerState under the name this package's
// callers have always used. The composer classifier itself lives in
// internal/screen/fixture.
type ComposerState = screen.ComposerState

const (
	// StateEmpty is the one state a line may be typed into.
	StateEmpty = screen.ComposerEmpty
	// StatePending is someone else's unsubmitted text in the composer.
	// Typing here concatenates two messages into one (docs/mvp.md
	// section 7).
	StatePending = screen.ComposerDraft
	// StateBusy is the harness mid-turn; a line typed now is queued or
	// dropped rather than answered.
	StateBusy = screen.ComposerBusy
	// StateUnknown is any screen with no recognised composer.
	StateUnknown = screen.ComposerUnknown
)

// Classification is what the composer classifier saw and the line it saw
// it on (fixture.Classification).
type Classification = fixture.Classification

// ClassifyComposer is fixture.ClassifyComposer, kept here for the callers
// outside this package that read a composer directly.
func ClassifyComposer(profile harness.ScreenProfile, screen string) Classification {
	return fixture.ClassifyComposer(profile, screen)
}

// StripSGR removes the escape sequences from a styled screen
// (fixture.StripSGR).
func StripSGR(screen string) string { return fixture.StripSGR(screen) }

// ScreenTail returns the last n lines of a screen, for error details.
func ScreenTail(screen string, n int) string {
	return harness.StartupScreenTail(screen, n)
}
