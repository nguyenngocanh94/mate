package spawn

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
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
// carries in refusals. Codex's hook review is walked by reviewOwnHooks
// instead.
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
// trusted names the hooks mate itself installed for this launch (a Codex
// Mate's SessionStart hook). Codex's hook review is walked, and those hooks
// trusted, only when every hook the review lists is one of them; with none,
// the review is refused at once.
func settleStartupPrompt(ctx context.Context, rt runtime.Adapter, handle runtime.AgentHandle, profile harness.Profile, budget time.Duration, sleep sleeper, trusted ...harness.OwnHook) (Settlement, error) {
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
		class := screens.ClassifyStartup(screen)
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
			presses, err := reviewOwnHooks(ctx, rt, handle, kind, screens, screen, trusted, sleep)
			settled.Presses = append(settled.Presses, presses...)
			if err != nil {
				return settled, err
			}
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
			presses, err := answerStartupDialog(ctx, rt, handle, kind, screens, class, what, sleep)
			settled.Presses = append(settled.Presses, presses...)
			if err != nil {
				return settled, err
			}
			// Fall through to the next poll, which must find the next
			// dialog or the composer.
		} else if time.Now().After(deadline) {
			return settled, startupRefusal(handle, kind, screen,
				fmt.Sprintf("%s startup screen not recognised after %s: not the empty composer and not a measured startup dialog; mate refuses to press keys into a screen it cannot name", kind, budget.Round(time.Millisecond)))
		}
		if err := sleep(ctx, startupPollInterval); err != nil {
			return settled, err
		}
	}
}

// answerStartupDialog performs one dialog's measured sequence for one
// harness: every select press is its own Herdr call followed by a re-read,
// and the confirm press is sent only once the highlight marker is on the
// option mate means to take. Claude's trust highlight opens on "No, exit"
// and Codex's update highlight opens on "1. Update now", so this order is the
// difference between settling the pane and killing the agent or starting a
// package install under it.
func answerStartupDialog(ctx context.Context, rt runtime.Adapter, handle runtime.AgentHandle, kind harness.Kind, screens harness.ScreenProfile, dialog harness.StartupScreen, what string, sleep sleeper) ([]string, error) {
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
	if !screens.StartupTargetSelected(dialog, screen) {
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

// reviewScreenPolls bounds how many reads the hook review waits for Codex
// to redraw after one key, at startupPollInterval apiece.
const reviewScreenPolls = 20

// reviewOwnHooks walks Codex's hook review (harness/codex_hooks.go) from
// the dialog on screen to the composer, trusting the hooks in own and
// nothing else. Every hook the review lists as needing review is read, and
// matched against own, before a single one is trusted: a review that also
// lists a hook mate did not install is refused with nothing trusted.
func reviewOwnHooks(ctx context.Context, rt runtime.Adapter, handle runtime.AgentHandle, kind harness.Kind, screens harness.ScreenProfile, screen string, own []harness.OwnHook, sleep sleeper) ([]string, error) {
	var presses []string
	press := func(key string) error {
		if err := rt.SendKeys(ctx, handle, []string{key}); err != nil {
			return err
		}
		presses = append(presses, key)
		return sleep(ctx, startupKeySettle)
	}
	refuse := func(screen, msg string) error {
		return startupRefusal(handle, kind, screen, fmt.Sprintf("%s hook review: %s", kind, msg))
	}
	// readUntil re-reads until parse accepts the screen.
	readUntil := func(what string, parse func(string) bool) (string, error) {
		var last string
		for i := 0; i < reviewScreenPolls; i++ {
			s, err := rt.ReadAgent(ctx, handle, screens.ReadSource(), startupScreenLines)
			if err != nil {
				return s, err
			}
			if parse(s) {
				return s, nil
			}
			last = s
			if err := sleep(ctx, startupPollInterval); err != nil {
				return s, err
			}
		}
		return last, refuse(last, "expected "+what+" and did not find it; not pressing anything further")
	}

	// 1. The dialog: confirm "1. Review hooks", never "2. Trust all".
	if !screens.StartupTargetSelected(harness.StartupScreenHooksReview, screen) {
		return presses, refuse(screen, `the highlight is not on "1. Review hooks"; refusing to confirm a selection mate cannot see`)
	}
	if err := press("enter"); err != nil {
		return presses, err
	}

	// 2. The event table: every hook needing review must be a SessionStart
	// hook, and SessionStart must be the selected event.
	var table harness.CodexHooksTable
	screen, err := readUntil("the hook table", func(s string) bool {
		var ok bool
		table, ok = harness.ParseCodexHooksTable(s)
		return ok
	})
	if err != nil {
		return presses, err
	}
	events := map[string]bool{}
	for _, o := range own {
		events[o.Event] = true
	}
	selected, _ := table.Selected()
	row, _ := table.Row(selected.Event)
	if !table.Reviewing || len(events) != 1 || !events[selected.Event] || row.Review != table.NeedReview {
		return presses, refuse(screen, fmt.Sprintf("%d hook(s) need review, %d of them under %s (the event mate installs for); mate trusts only its own hooks and trusted nothing",
			table.NeedReview, max(row.Review, 0), selected.Event))
	}
	if err := press("enter"); err != nil {
		return presses, err
	}

	// 3. The event's hooks: read every one that needs review.
	var event harness.CodexHookEvent
	parseEvent := func(s string) bool {
		var ok bool
		event, ok = harness.ParseCodexHookEvent(s)
		return ok
	}
	if screen, err = readUntil("the "+selected.Event+" hook list", parseEvent); err != nil {
		return presses, err
	}
	// moveTo selects the item at index i, one key and one read at a time.
	moveTo := func(i int) error {
		for steps := 0; event.Selected() != i; steps++ {
			if steps > len(event.Hooks) {
				return refuse(screen, "the selection did not reach the hook mate has to read")
			}
			key := "down"
			if event.Selected() > i {
				key = "up"
			}
			if err := press(key); err != nil {
				return err
			}
			if screen, err = readUntil("the "+selected.Event+" hook list", parseEvent); err != nil {
				return err
			}
		}
		return nil
	}
	isOwn := func(h harness.CodexReviewHook) bool {
		for _, o := range own {
			if o.Matches(h) {
				return true
			}
		}
		return false
	}
	var toTrust []int
	for i, item := range event.Hooks {
		if !item.NeedsReview {
			continue
		}
		if err := moveTo(i); err != nil {
			return presses, err
		}
		if !isOwn(event.Detail) {
			return presses, refuse(screen, fmt.Sprintf("hook %d needs review and is not mate's own (Source: %s; Command: %s); mate trusted nothing. Review it once in %s yourself (/hooks), then start again",
				item.Index, event.Detail.Source, event.Detail.Command, kind))
		}
		toTrust = append(toTrust, i)
	}
	if len(toTrust) == 0 || len(toTrust) != event.NeedReview {
		return presses, refuse(screen, fmt.Sprintf("the %s list says %d hook(s) need review but mate found %d marked; trusted nothing", selected.Event, event.NeedReview, len(toTrust)))
	}

	// 4. Trust them, one `t` each, each confirmed on screen.
	for _, i := range toTrust {
		if err := moveTo(i); err != nil {
			return presses, err
		}
		if err := press("t"); err != nil {
			return presses, err
		}
		if screen, err = readUntil("the trusted hook", func(s string) bool {
			return parseEvent(s) && !event.Hooks[i].NeedsReview && event.Detail.Trusted()
		}); err != nil {
			return presses, err
		}
	}
	if event.NeedReview != 0 {
		return presses, refuse(screen, "hooks still need review after mate trusted its own")
	}

	// 5. Back out: esc to the table, which must need no review now, and
	// esc again to the composer, which the settle's own loop confirms.
	if err := press("esc"); err != nil {
		return presses, err
	}
	if screen, err = readUntil("the hook table with nothing to review", func(s string) bool {
		t, ok := harness.ParseCodexHooksTable(s)
		return ok && !t.Reviewing
	}); err != nil {
		return presses, err
	}
	if err := press("esc"); err != nil {
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
