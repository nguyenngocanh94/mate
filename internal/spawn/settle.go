package spawn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/screen"
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
// highlight is on the option mate means to confirm, only then confirm),
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
	// was on screen and mate accepted it.
	TrustDialogAnswered bool
	// UpdateDialogAnswered is true when the harness's release-update prompt
	// was on screen and mate skipped it until the next version.
	UpdateDialogAnswered bool
	// HooksTrusted is true when Codex's hook review was on screen and
	// mate trusted its own hooks in it, and nothing else.
	HooksTrusted bool
	// Presses is the key sequence sent, one entry per press.
	Presses []string
}

// markAnswered records one answered dialog and reports whether the state is
// already set - the harness redrew a dialog mate had confirmed.
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
	case harness.StartupScreenHooksReview:
		if s.HooksTrusted {
			return true
		}
		s.HooksTrusted = true
	}
	return false
}

// startupDialogs are the dialogs the settle answers with the harness's
// measured keys (harness.ScreenProfile.StartupAnswer), each with the name it
// carries in refusals. A hook review is walked by the harness's own
// HookInstaller instead (reviewOwnHooks).
var startupDialogs = map[harness.StartupScreen]string{
	harness.StartupScreenTrustDialog:  "trust dialog",
	harness.StartupScreenUpdateDialog: "update dialog",
}

// settleStartupPrompt polls the agent's pane until it shows the harness's
// empty composer, answering the directory-trust dialog once if it appears.
// It returns a target_blocked error, with the screen tail in the message and
// in details, when the budget runs out on a screen it does not recognise,
// when a select press does not move the highlight onto the accept option, or
// when the dialog is still on screen after the confirm press.
//
// observer reads every snapshot: the dialog on screen is its Observation's
// Startup, and a dialog is confirmed only when its Highlight is the option
// the harness's StartupAnswer confirms. The keys themselves are always the
// profile's measured StartupAnswer.
//
// trusted names the hooks mate itself installed for this launch (a Codex
// Mate's SessionStart hook). Codex's hook review is walked, and those hooks
// trusted, only when every hook the review lists is one of them; with none,
// the review is refused at once.
func settleStartupPrompt(ctx context.Context, rt runtime.Adapter, handle runtime.AgentHandle, profile harness.Profile, observer screen.Observer, budget time.Duration, sleep sleeper, trusted ...harness.OwnHook) (Settlement, error) {
	if sleep == nil {
		sleep = sleepCtx
	}
	kind, screens := profile.Kind(), profile.Screen()
	deadline := time.Now().Add(budget)
	var settled Settlement
	answered := 0
	for {
		screen, err := rt.ReadAgent(ctx, handle, screens.ReadSource(), startupScreenLines)
		if err != nil {
			return settled, err
		}
		observed, err := observer.Observe(ctx, screens, screen)
		if err != nil {
			return settled, err
		}
		class := observed.Startup
		if class == harness.StartupScreenReady {
			return settled, nil
		}
		if class == harness.StartupScreenHooksReview {
			if len(trusted) == 0 {
				return settled, startupRefusal(handle, kind, screen,
					fmt.Sprintf("%s asks to review hooks mate did not install; mate trusts only its own hooks and pressed nothing. Review them once in %s yourself (/hooks), then start again", kind, kind))
			}
			if settled.markAnswered(class) {
				return settled, startupRefusal(handle, kind, screen,
					fmt.Sprintf("%s hook review is on screen again after mate trusted its own hooks; not pressing anything further", kind))
			}
			answered++
			if answered > startupMaxDialogs {
				return settled, startupRefusal(handle, kind, screen,
					fmt.Sprintf("%s drew more than %d startup dialogs in one launch; mate stops answering rather than press keys in a loop", kind, startupMaxDialogs))
			}
			presses, err := reviewOwnHooks(ctx, rt, handle, profile, screen, trusted, sleep)
			settled.Presses = append(settled.Presses, presses...)
			if err != nil {
				return settled, err
			}
		} else if class == harness.StartupScreenBypassDialog {
			answer, err := screens.StartupAnswer(class)
			if err != nil {
				return settled, err
			}
			return settled, startupRefusal(handle, kind, screen,
				fmt.Sprintf("%s asks to accept Bypass Permissions mode, which mate launches it with; mate does not accept it for you and pressed nothing. Run `%s --dangerously-skip-permissions` once yourself and choose %q; %s saves the choice and does not ask again. Then start again", kind, kind, answer.TargetLabel, kind))
		} else if what, ok := startupDialogs[class]; ok {
			if settled.markAnswered(class) {
				return settled, startupRefusal(handle, kind, screen,
					fmt.Sprintf("%s %s is still on screen after mate confirmed its selection; not pressing anything further", kind, what))
			}
			answered++
			if answered > startupMaxDialogs {
				return settled, startupRefusal(handle, kind, screen,
					fmt.Sprintf("%s drew more than %d startup dialogs in one launch; mate stops answering rather than press keys in a loop", kind, startupMaxDialogs))
			}
			presses, err := answerStartupDialog(ctx, rt, handle, kind, screens, observer, class, what, sleep)
			settled.Presses = append(settled.Presses, presses...)
			if err != nil {
				return settled, err
			}
			// Fall through to the next poll, which must find the next
			// dialog or the composer.
		} else if time.Now().After(deadline) {
			return settled, startupRefusal(handle, kind, screen,
				fmt.Sprintf("%s startup screen not recognised after %s: not the empty composer and not a measured startup dialog; mate refuses to press keys into a screen it cannot name%s", kind, budget.Round(time.Millisecond), jevSays(observed)))
		}
		if err := sleep(ctx, startupPollInterval); err != nil {
			return settled, err
		}
	}
}

// jevSays is what Jev named the dialog on a screen the harness profile
// could not, for the refusal: Jev's word is reported, never acted on.
func jevSays(observed screen.Observation) string {
	if observed.Source != "jev" {
		return ""
	}
	return " · jev says " + string(observed.Dialog)
}

// answerStartupDialog performs one dialog's measured sequence for one
// harness: every select press is its own Herdr call followed by a re-read,
// and the confirm press is sent only once the highlight marker is on the
// option mate means to take. Claude's trust highlight opens on "No, exit"
// and Codex's update highlight opens on "1. Update now", so this order is the
// difference between settling the pane and killing the agent or starting a
// package install under it.
func answerStartupDialog(ctx context.Context, rt runtime.Adapter, handle runtime.AgentHandle, kind harness.Kind, screens harness.ScreenProfile, observer screen.Observer, dialog harness.StartupScreen, what string, sleep sleeper) ([]string, error) {
	answer, err := screens.StartupAnswer(dialog)
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
		screen, err = rt.ReadAgent(ctx, handle, screens.ReadSource(), startupScreenLines)
		if err != nil {
			return presses, err
		}
	}
	if len(answer.SelectKeys) == 0 {
		screen, err = rt.ReadAgent(ctx, handle, screens.ReadSource(), startupScreenLines)
		if err != nil {
			return presses, err
		}
	}
	observed, err := observer.Observe(ctx, screens, screen)
	if err != nil {
		return presses, err
	}
	if observed.Startup != dialog {
		return presses, startupRefusal(handle, kind, screen,
			fmt.Sprintf("%s %s: after pressing %s the screen reads as %s, not the %s; refusing to confirm a selection on a dialog mate did not mean to answer", kind, what, strings.Join(answer.SelectKeys, ", "), observed.Startup, what))
	}
	if observed.Highlight != answer.Target {
		return presses, startupRefusal(handle, kind, screen,
			fmt.Sprintf("%s %s: after pressing %s the highlight is not on %q; refusing to confirm a selection mate cannot see", kind, what, strings.Join(answer.SelectKeys, ", "), answer.TargetLabel))
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

// settlePane is the pane a harness's hook review is walked in: every read
// through the profile's own source, every key one Herdr call followed by
// the redraw pause, and each key recorded for the Settlement.
type settlePane struct {
	rt      runtime.Adapter
	handle  runtime.AgentHandle
	screens harness.ScreenProfile
	sleep   sleeper
	presses []string
}

// Read implements harness.HookReviewPane.
func (p *settlePane) Read(ctx context.Context) (string, error) {
	return p.rt.ReadAgent(ctx, p.handle, p.screens.ReadSource(), startupScreenLines)
}

// Press implements harness.HookReviewPane.
func (p *settlePane) Press(ctx context.Context, key string) error {
	if err := p.rt.SendKeys(ctx, p.handle, []string{key}); err != nil {
		return err
	}
	p.presses = append(p.presses, key)
	return p.sleep(ctx, startupKeySettle)
}

// Wait implements harness.HookReviewPane.
func (p *settlePane) Wait(ctx context.Context) error { return p.sleep(ctx, startupPollInterval) }

// reviewOwnHooks walks the harness's hook review (HookInstaller.ReviewOwn)
// and frames a refusal the way every other startup refusal is framed.
func reviewOwnHooks(ctx context.Context, rt runtime.Adapter, handle runtime.AgentHandle, profile harness.Profile, screen string, own []harness.OwnHook, sleep sleeper) ([]string, error) {
	kind := profile.Kind()
	hooks := profile.Capabilities().Hooks
	if !hooks.Verified() {
		return nil, startupRefusal(handle, kind, screen,
			fmt.Sprintf("%s asks to review hooks but its Hooks capability is %s (%s); mate pressed nothing", kind, hooks.Status, hooks.Reason))
	}
	pane := &settlePane{rt: rt, handle: handle, screens: profile.Screen(), sleep: sleep}
	err := hooks.Impl.ReviewOwn(ctx, pane, screen, own)
	var refused *harness.ScreenRefusal
	if errors.As(err, &refused) {
		err = startupRefusal(handle, kind, refused.Screen, refused.Reason)
	}
	return pane.presses, err
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
