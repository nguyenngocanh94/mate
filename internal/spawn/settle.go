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
// pane, answers exactly one recognised dialog (select, re-read, verify the
// highlight is on the accept option, only then confirm), and refuses - with
// no key pressed - anything it cannot name.

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
	// Presses is the key sequence sent, one entry per press.
	Presses []string
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
	for {
		screen, err := rt.ReadAgent(ctx, handle, startupScreenLines)
		if err != nil {
			return settled, err
		}
		class, err := harness.ClassifyStartupScreen(kind, screen)
		if err != nil {
			return settled, err
		}
		switch class {
		case harness.StartupScreenReady:
			return settled, nil
		case harness.StartupScreenTrustDialog:
			if settled.TrustDialogAnswered {
				return settled, startupRefusal(handle, kind, screen,
					fmt.Sprintf("%s trust dialog is still on screen after matev2 confirmed the accept option; not pressing anything further", kind))
			}
			presses, err := answerTrustDialog(ctx, rt, handle, kind, sleep)
			settled.Presses = append(settled.Presses, presses...)
			if err != nil {
				return settled, err
			}
			settled.TrustDialogAnswered = true
			// Fall through to the next poll, which must find the composer.
		case harness.StartupScreenUnrecognized:
			if time.Now().After(deadline) {
				return settled, startupRefusal(handle, kind, screen,
					fmt.Sprintf("%s startup screen not recognised after %s: not the empty composer and not the measured directory-trust dialog; matev2 refuses to press keys into a screen it cannot name", kind, budget.Round(time.Millisecond)))
			}
		}
		if err := sleep(ctx, startupPollInterval); err != nil {
			return settled, err
		}
	}
}

// answerTrustDialog performs the measured accept sequence for one harness:
// every select press is its own Herdr call followed by a re-read, and the
// confirm press is sent only once the highlight marker is on the accept
// option. Claude's default highlight is "No, exit", so this order is the
// difference between accepting and killing the agent.
func answerTrustDialog(ctx context.Context, rt runtime.Adapter, handle runtime.AgentHandle, kind harness.Kind, sleep sleeper) ([]string, error) {
	answer, err := harness.TrustDialogAnswerFor(kind)
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
	selected, err := harness.TrustDialogAcceptSelected(kind, screen)
	if err != nil {
		return presses, err
	}
	if !selected {
		return presses, startupRefusal(handle, kind, screen,
			fmt.Sprintf("%s trust dialog: after pressing %s the highlight is not on %q; refusing to confirm a selection matev2 cannot see", kind, strings.Join(answer.SelectKeys, ", "), answer.AcceptLabel))
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
