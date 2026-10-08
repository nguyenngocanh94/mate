// Package fixture is the screen.Observer that reads a pane through the
// harness's own measured harness.ScreenProfile: the composer classifier
// (ClassifyComposer), the startup classifier and the dialog highlight. It is
// deterministic, never calls the network, and classifies every capture
// exactly as internal/send did before observation moved here.
package fixture

import (
	"context"
	"fmt"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/screen"
)

// Source is the Observation.Source this observer writes.
const Source = "fixture"

// Observer is the fixture observer. Its zero value is ready to use.
type Observer struct{}

// New returns the fixture observer.
func New() screen.Observer { return Observer{} }

// Observe reads one pane snapshot. It never fails: a screen the profile does
// not recognise is an Observation with Unknown in it, not an error.
func (Observer) Observe(_ context.Context, profile harness.ScreenProfile, pane string) (screen.Observation, error) {
	cls := ClassifyComposer(profile, pane)
	startup := profile.ClassifyStartup(StripSGR(pane))
	obs := screen.Observation{
		Composer:      cls.State,
		Deterministic: cls.State,
		Draft:         cls.Pending,
		Evidence:      cls.Evidence,
		Dialog:        dialogOf(startup, cls.State),
		Startup:       startup,
		Highlight:     highlight(profile, startup, StripSGR(pane)),
		Source:        Source,
	}
	if cls.State != screen.ComposerUnknown || startup != harness.StartupScreenUnrecognized {
		obs.Confidence = 1
	}
	obs.Reason = reason(obs, cls)
	return obs, nil
}

// dialogOf names the dialog the startup classifier recognised. A screen it
// does not name has no dialog the profile knows of: that is None when a
// composer was read off it, and Unknown when nothing was.
func dialogOf(startup harness.StartupScreen, composer screen.ComposerState) screen.DialogKind {
	switch startup {
	case harness.StartupScreenReady:
		return screen.DialogNone
	case harness.StartupScreenTrustDialog:
		return screen.DialogTrust
	case harness.StartupScreenUpdateDialog:
		return screen.DialogUpdate
	case harness.StartupScreenHooksReview:
		return screen.DialogHooksReview
	case harness.StartupScreenBypassDialog:
		return screen.DialogOther
	}
	if composer != screen.ComposerUnknown {
		return screen.DialogNone
	}
	return screen.DialogUnknown
}

// highlight is the index of the option the harness's answer to startup
// confirms, when the highlight is on it, and -1 otherwise.
func highlight(profile harness.ScreenProfile, startup harness.StartupScreen, plain string) int {
	answer, err := profile.StartupAnswer(startup)
	if err != nil || !profile.StartupTargetSelected(startup, plain) {
		return -1
	}
	return answer.Target
}

// reason says why the composer or the dialog is Unknown.
func reason(obs screen.Observation, cls Classification) string {
	switch {
	case obs.Composer == screen.ComposerUnknown && cls.Evidence != "":
		return fmt.Sprintf("no composer: %s", cls.Evidence)
	case obs.Composer == screen.ComposerUnknown || obs.Dialog == screen.DialogUnknown:
		return "the harness profile recognises no composer, busy line or startup dialog on screen"
	}
	return ""
}
