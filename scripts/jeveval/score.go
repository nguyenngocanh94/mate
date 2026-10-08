package main

import (
	"slices"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/screen/jev"
	"github.com/nguyenngocanh94/mate/internal/send"
)

// accepts is the set of Jev labels that agree with a deterministic label
// on its axis. Agreement is about what the caller would do: a composer the
// fixture classifier cannot type into for want of one (Unknown: a dialog,
// a menu) agrees with Jev seeing no composer, or not knowing.
func accepts(axis Axis, det string) []string {
	switch axis {
	case AxisComposer:
		switch send.ComposerState(det) {
		case send.StateEmpty:
			return []string{"empty"}
		case send.StatePending:
			return []string{"draft"}
		case send.StateBusy:
			return []string{"busy"}
		case send.StateUnknown:
			return []string{"none", "unknown"}
		}
	case AxisDialog:
		switch harness.StartupScreen(det) {
		case harness.StartupScreenReady:
			return []string{"none"}
		case harness.StartupScreenTrustDialog:
			return []string{"trust"}
		case harness.StartupScreenUpdateDialog:
			return []string{"update"}
		case harness.StartupScreenHooksReview:
			return []string{"hooks_review"}
		case harness.StartupScreenBypassDialog:
			return []string{"other"}
		}
		// StartupScreenUnrecognized is the classifier abstaining, not a
		// statement about the screen (a pane mid-turn, a hook table one
		// step inside a review): the screen has no label on this axis and
		// is not scored.
	case AxisNotice:
		return []string{det}
	}
	return nil
}

// Row is one screen's result.
type Row struct {
	Screen
	Hash      string
	LatencyMS int64
	Err       string
	Resp      jev.Response
}

// Scored reports whether the screen has a deterministic label on its axis.
func (r Row) Scored() bool { return len(accepts(r.Axis, r.Det)) > 0 }

// Match reports whether Jev agreed with the deterministic label on the
// screen's axis. A failed request never matches.
func (r Row) Match() bool {
	if r.Err != "" || !r.Scored() {
		return false
	}
	return slices.Contains(accepts(r.Axis, r.Det), r.Resp.Answers[r.Axis].Choice)
}

// Dangerous is the one error the gate refuses outright: Jev calls the
// composer empty where ClassifyComposer saw a draft or a turn in flight.
// It is checked on every screen with a harness, whatever axis the screen
// is scored on, so it is stricter than the scored axis alone.
func (r Row) Dangerous() bool {
	if r.Err != "" || r.Kind == "" {
		return false
	}
	return (r.Composer == send.StatePending || r.Composer == send.StateBusy) &&
		r.Resp.Answers[AxisComposer].Choice == "empty"
}
