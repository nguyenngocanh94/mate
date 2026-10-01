package codex

import (
	"context"
	"fmt"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// reviewScreenPolls bounds how many reads the hook review waits for Codex
// to redraw after one key, at the pane's poll interval apiece.
const reviewScreenPolls = 20

// ReviewOwn implements HookInstaller: it walks Codex's hook review
// (hooks.go) from the dialog on screen to the composer, trusting the
// hooks in own and nothing else. Every hook the review lists as needing
// review is read, and matched against own, before a single one is trusted: a
// review that also lists a hook mate did not install is refused with nothing
// trusted. It came here from internal/spawn's startup settle with plan PR 6;
// the settle hands it the pane and owns the refusal's framing.
func (codexHooks) ReviewOwn(ctx context.Context, pane harness.HookReviewPane, screen string, own []harness.OwnHook) error {
	kind := KindCodex
	refuse := func(screen, msg string) error {
		return &harness.ScreenRefusal{Screen: screen, Reason: fmt.Sprintf("%s hook review: %s", kind, msg)}
	}
	press := func(key string) error { return pane.Press(ctx, key) }
	// readUntil re-reads until parse accepts the screen.
	readUntil := func(what string, parse func(string) bool) (string, error) {
		var last string
		for i := 0; i < reviewScreenPolls; i++ {
			s, err := pane.Read(ctx)
			if err != nil {
				return s, err
			}
			if parse(s) {
				return s, nil
			}
			last = s
			if err := pane.Wait(ctx); err != nil {
				return s, err
			}
		}
		return last, refuse(last, "expected "+what+" and did not find it; not pressing anything further")
	}

	// 1. The dialog: confirm "1. Review hooks", never "2. Trust all".
	if !codexStartup().TargetSelected(harness.StartupScreenHooksReview, screen) {
		return refuse(screen, `the highlight is not on "1. Review hooks"; refusing to confirm a selection mate cannot see`)
	}
	if err := press("enter"); err != nil {
		return err
	}

	// 2. The event table: every hook needing review must be a SessionStart
	// hook, and SessionStart must be the selected event.
	var table CodexHooksTable
	screen, err := readUntil("the hook table", func(s string) bool {
		var ok bool
		table, ok = ParseCodexHooksTable(s)
		return ok
	})
	if err != nil {
		return err
	}
	events := map[string]bool{}
	for _, o := range own {
		events[o.Event] = true
	}
	selected, _ := table.Selected()
	row, _ := table.Row(selected.Event)
	if !table.Reviewing || len(events) != 1 || !events[selected.Event] || row.Review != table.NeedReview {
		return refuse(screen, fmt.Sprintf("%d hook(s) need review, %d of them under %s (the event mate installs for); mate trusts only its own hooks and trusted nothing",
			table.NeedReview, max(row.Review, 0), selected.Event))
	}
	if err := press("enter"); err != nil {
		return err
	}

	// 3. The event's hooks: read every one that needs review.
	var event CodexHookEvent
	parseEvent := func(s string) bool {
		var ok bool
		event, ok = ParseCodexHookEvent(s)
		return ok
	}
	if screen, err = readUntil("the "+selected.Event+" hook list", parseEvent); err != nil {
		return err
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
	isOwn := func(h CodexReviewHook) bool {
		for _, o := range own {
			if ownHookMatches(o, h) {
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
			return err
		}
		if !isOwn(event.Detail) {
			return refuse(screen, fmt.Sprintf("hook %d needs review and is not mate's own (Source: %s; Command: %s); mate trusted nothing. Review it once in %s yourself (/hooks), then start again",
				item.Index, event.Detail.Source, event.Detail.Command, kind))
		}
		toTrust = append(toTrust, i)
	}
	if len(toTrust) == 0 || len(toTrust) != event.NeedReview {
		return refuse(screen, fmt.Sprintf("the %s list says %d hook(s) need review but mate found %d marked; trusted nothing", selected.Event, event.NeedReview, len(toTrust)))
	}

	// 4. Trust them, one `t` each, each confirmed on screen.
	for _, i := range toTrust {
		if err := moveTo(i); err != nil {
			return err
		}
		if err := press("t"); err != nil {
			return err
		}
		if screen, err = readUntil("the trusted hook", func(s string) bool {
			return parseEvent(s) && !event.Hooks[i].NeedsReview && event.Detail.Trusted()
		}); err != nil {
			return err
		}
	}
	if event.NeedReview != 0 {
		return refuse(screen, "hooks still need review after mate trusted its own")
	}

	// 5. Back out: esc to the table, which must need no review now, and
	// esc again to the composer, which the settle's own loop confirms.
	if err := press("esc"); err != nil {
		return err
	}
	if _, err = readUntil("the hook table with nothing to review", func(s string) bool {
		t, ok := ParseCodexHooksTable(s)
		return ok && !t.Reviewing
	}); err != nil {
		return err
	}
	return press("esc")
}
