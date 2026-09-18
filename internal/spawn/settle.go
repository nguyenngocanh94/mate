package spawn

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
)

// Startup-prompt settlement, ported from v1's ADR 0028. Measured 2026-09-14
// against Herdr 0.8.2: a Codex pane sitting on its directory-trust dialog is
// reported by `agent start` as idle and interactive, and a Claude pane on the
// same dialog is reported blocked, which the default wait accepts as
// readiness. Either way a launch that only waited would record a Mate running
// behind a question nobody would answer.
//
// This step runs between StartAgent and the readiness wait: it reads the
// pane, answers each recognised dialog once (select, re-read, verify the
// highlight is on the option matev2 means to confirm, only then confirm),
// and refuses - with no key pressed - anything it cannot name.
//
// Codex draws a sequence, not a single modal: measured 2026-09-18 with
// codex-cli 0.154.0 and 0.155.0 published, a launch in an unseen directory
// shows the release-update prompt first and only reaches the directory-trust
// dialog after it is answered. Each is answered with the same discipline, and
// startupMaxDialogs bounds the sequence so a harness that redraws a dialog
// forever cannot turn this into a keypress loop.

const (
	// startupScreenLines bounds each pane read. The dialogs are under 20
	// lines and the Claude banner plus dialog under 25, and the composer is
	// at the bottom, so a tail this long always contains what is classified.
	startupScreenLines = 40
	// startupPollInterval is how often an unrecognised screen is re-read
	// while the harness is still drawing.
	startupPollInterval = 250 * time.Millisecond
	// startupKeySettle is the pause after a key press before the pane is
	// re-read: both harnesses redraw within a frame, but the read is a
	// separate Herdr round trip and must not race the redraw.
	startupKeySettle = 400 * time.Millisecond
	// startupErrorTailLines bounds the screen excerpt carried in a refusal.
	startupErrorTailLines = 12
	// startupMaxDialogs caps how many startup dialogs one launch answers.
	// Two are measured (Codex's update prompt then the directory-trust
	// dialog); the cap leaves room for a third without ever letting a
	// redrawing harness be answered indefinitely.
	startupMaxDialogs = 3
)

// sleeper is the wait between polls; tests shorten it.
type sleeper func(ctx context.Context, d time.Duration) error

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Settlement is what settleStartupPrompt established before readiness was
// awaited.
type Settlement struct {
	// TrustDialogAnswered is true when the harness's directory-trust dialog
	// was on screen and matev2 accepted it.
	TrustDialogAnswered bool
	// UpdateDialogAnswered is true when the harness's release-update prompt
	// was on screen and matev2 skipped it until the next version.
	UpdateDialogAnswered bool
	// Presses is the key sequence sent, one entry per press.
	Presses []string
}

// markAnswered records one answered dialog and reports whether the state is
// already set - the harness redrew a dialog matev2 had confirmed.
func (s *Settlement) markAnswered(screen harness.StartupScreen) bool {
	switch screen {
	case harness.StartupScreenTrustDialog:
		if s.TrustDialogAnswered {
			return true
		}
		s.TrustDialogAnswered = true
	case harness.StartupScreenUpdateDialog:
		if s.UpdateDialogAnswered {
			return true
		}
		s.UpdateDialogAnswered = true
	}
	return false
}

// startupDialog describes how one recognised dialog is answered: its keys and
// the check that the highlight is on the option the confirm key will take.
type startupDialog struct {
	answer   func(harness.Kind) (harness.StartupDialogAnswer, error)
	selected func(harness.Kind, string) (bool, error)
	// what names the dialog in refusals.
	what string
}

var startupDialogs = map[harness.StartupScreen]startupDialog{
	harness.StartupScreenTrustDialog: {
		answer:   harness.TrustDialogAnswerFor,
		selected: harness.TrustDialogAcceptSelected,
		what:     "trust dialog",
	},
	harness.StartupScreenUpdateDialog: {
		answer:   harness.UpdateDialogAnswerFor,
		selected: harness.UpdateDialogSkipSelected,
		what:     "update dialog",
	},
}

// settleStartupPrompt polls the agent's pane until it shows the harness's
// empty composer, answering the directory-trust dialog once if it appears.
// It returns a target_blocked error, with the screen tail in the message and
// in details, when the budget runs out on a screen it does not recognise,
// when a select press does not move the highlight onto the accept option, or
// when the dialog is still on screen after the confirm press.
func settleStartupPrompt(ctx context.Context, rt runtime.Adapter, handle runtime.AgentHandle, kind harness.Kind, budget time.Duration, sleep sleeper) (Settlement, error) {
	if sleep == nil {
		sleep = sleepCtx
	}
	if _, err := harness.ClassifyStartupScreen(kind, ""); err != nil {
		// No profile for this harness: nothing can be recognised, so
		// nothing is pressed and the launch proceeds without this step.
		return Settlement{}, nil
	}
	deadline := time.Now().Add(budget)
	var settled Settlement
	answered := 0
	for {
		screen, err := rt.ReadAgent(ctx, handle, startupScreenLines)
		if err != nil {
			return settled, err
		}
		class, err := harness.ClassifyStartupScreen(kind, screen)
		if err != nil {
			return settled, err
		}
		if class == harness.StartupScreenReady {
			return settled, nil
		}
		if dialog, ok := startupDialogs[class]; ok {
			if settled.markAnswered(class) {
				return settled, startupRefusal(handle, kind, screen,
					fmt.Sprintf("%s %s is still on screen after matev2 confirmed its selection; not pressing anything further", kind, dialog.what))
			}
			answered++
			if answered > startupMaxDialogs {
				return settled, startupRefusal(handle, kind, screen,
					fmt.Sprintf("%s drew more than %d startup dialogs in one launch; matev2 stops answering rather than press keys in a loop", kind, startupMaxDialogs))
			}
			presses, err := answerStartupDialog(ctx, rt, handle, kind, dialog, sleep)
			settled.Presses = append(settled.Presses, presses...)
			if err != nil {
				return settled, err
			}
			// Fall through to the next poll, which must find the next
			// dialog or the composer.
		} else if time.Now().After(deadline) {
			return settled, startupRefusal(handle, kind, screen,
				fmt.Sprintf("%s startup screen not recognised after %s: not the empty composer and not a measured startup dialog; matev2 refuses to press keys into a screen it cannot name", kind, budget.Round(time.Millisecond)))
		}
		if err := sleep(ctx, startupPollInterval); err != nil {
			return settled, err
		}
	}
}

// answerStartupDialog performs one dialog's measured sequence for one
// harness: every select press is its own Herdr call followed by a re-read,
// and the confirm press is sent only once the highlight marker is on the
// option matev2 means to take. Claude's trust highlight opens on "No, exit"
// and Codex's update highlight opens on "1. Update now", so this order is the
// difference between settling the pane and killing the agent or starting a
// package install under it.
func answerStartupDialog(ctx context.Context, rt runtime.Adapter, handle runtime.AgentHandle, kind harness.Kind, dialog startupDialog, sleep sleeper) ([]string, error) {
	answer, err := dialog.answer(kind)
	if err != nil {
		return nil, err
	}
	var presses []string
	var screen string
	// One key per call with a re-read in between: sending `down` and `enter`
	// in a single send-keys call left the dialog on screen (ADR 0028), and
	// the read after the last press is what the accept check is made on.
	for _, key := range answer.SelectKeys {
		if err := rt.SendKeys(ctx, handle, []string{key}); err != nil {
			return presses, err
		}
		presses = append(presses, key)
		if err := sleep(ctx, startupKeySettle); err != nil {
			return presses, err
		}
		screen, err = rt.ReadAgent(ctx, handle, startupScreenLines)
		if err != nil {
			return presses, err
		}
	}
	if len(answer.SelectKeys) == 0 {
		screen, err = rt.ReadAgent(ctx, handle, startupScreenLines)
		if err != nil {
			return presses, err
		}
	}
	selected, err := dialog.selected(kind, screen)
	if err != nil {
		return presses, err
	}
	if !selected {
		return presses, startupRefusal(handle, kind, screen,
			fmt.Sprintf("%s %s: after pressing %s the highlight is not on %q; refusing to confirm a selection matev2 cannot see", kind, dialog.what, strings.Join(answer.SelectKeys, ", "), answer.TargetLabel))
	}
	if err := rt.SendKeys(ctx, handle, []string{answer.ConfirmKey}); err != nil {
		return presses, err
	}
	presses = append(presses, answer.ConfirmKey)
	if err := sleep(ctx, startupKeySettle); err != nil {
		return presses, err
	}
	return presses, nil
}

func startupRefusal(handle runtime.AgentHandle, kind harness.Kind, screen, msg string) error {
	tail := harness.StartupScreenTail(screen, startupErrorTailLines)
	return observability.NewError(observability.CodeTargetBlocked, msg+"\nlast "+fmt.Sprint(startupErrorTailLines)+" screen lines:\n"+tail).
		WithDetails(map[string]any{
			"harness_kind": string(kind),
			"agent_name":   handle.Name,
			"herdr_pane":   handle.Tab.PaneID,
			"screen_tail":  tail,
		})
}
