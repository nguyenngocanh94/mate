package harness

import (
	"testing"
)

// The paths and command the task 37 lab captures were taken with: a lab
// Mate directory under /private/tmp, and a stand-in matev2 path.
const (
	fixtureOwnCommand = "'/usr/local/bin/matev2' hook mate-session"
	fixtureOneSource  = "/private/tmp/TestLiveZZMeasureCodexHookReview323707510/001/codexlab/.codex/hooks.json"
	fixtureTwoSource  = "/private/tmp/TestLiveZZMeasureCodexHookReview8854139/001/codexlab/.codex/hooks.json"
)

func TestHooksReviewDialogIsRecognisedInBothLayouts(t *testing.T) {
	for _, name := range []string{"codex-0.154.0-hooks-review.txt", "codex-0.156.1-hooks-review.txt", "codex-0.156.1-hooks-review-two.txt"} {
		screen := startupFixture(t, name)
		got, err := ClassifyStartupScreen(KindCodex, screen)
		if err != nil {
			t.Fatal(err)
		}
		if got != StartupScreenHooksReview {
			t.Errorf("%s = %s, want %s", name, got, StartupScreenHooksReview)
		}
		if !HooksReviewSelected(screen) {
			t.Errorf("%s: the highlight opens on 1. Review hooks, but HooksReviewSelected is false", name)
		}
	}
	// Claude never draws it.
	if got, _ := ClassifyStartupScreen(KindClaude, startupFixture(t, "codex-0.156.1-hooks-review.txt")); got != StartupScreenUnrecognized {
		t.Errorf("claude classified Codex's hook review as %s", got)
	}
}

func TestHooksTableParsesTheReviewCounts(t *testing.T) {
	table, ok := ParseCodexHooksTable(startupFixture(t, "codex-0.156.1-hooks-table-review.txt"))
	if !ok {
		t.Fatal("the measured hook table did not parse")
	}
	if !table.Reviewing || table.NeedReview != 1 {
		t.Fatalf("table = %+v, want reviewing with 1 hook to review", table)
	}
	sel, ok := table.Selected()
	if !ok || sel.Event != "SessionStart" || sel.Installed != 1 || sel.Active != 0 || sel.Review != 1 {
		t.Fatalf("selected row = %+v, want SessionStart 1/0/1", sel)
	}
	if row, _ := table.Row("PreToolUse"); row.Review != 0 {
		t.Fatalf("PreToolUse = %+v", row)
	}

	done, ok := ParseCodexHooksTable(startupFixture(t, "codex-0.156.1-hooks-table-trusted.txt"))
	if !ok || done.Reviewing || done.NeedReview != 0 {
		t.Fatalf("the table after trusting = %+v (ok %v), want nothing to review", done, ok)
	}
	if row, _ := done.Row("SessionStart"); row.Installed != 2 || row.Active != 2 || row.Review != -1 {
		t.Fatalf("SessionStart after trusting = %+v", row)
	}
	// Other screens are not the table.
	for _, name := range []string{"codex-0.156.1-hooks-review.txt", "codex-0.156.1-hooks-sessionstart-own.txt", "codex-0.156.1-ready.txt"} {
		if _, ok := ParseCodexHooksTable(startupFixture(t, name)); ok {
			t.Errorf("%s parsed as the hook table", name)
		}
	}
}

func TestHookEventReadsTheSelectedHook(t *testing.T) {
	own := OwnHook{Event: "SessionStart", Source: fixtureOneSource, Command: fixtureOwnCommand}
	e, ok := ParseCodexHookEvent(startupFixture(t, "codex-0.156.1-hooks-sessionstart-own.txt"))
	if !ok {
		t.Fatal("the measured SessionStart hook list did not parse")
	}
	if e.Event != "SessionStart" || e.NeedReview != 1 || len(e.Hooks) != 1 || !e.Hooks[0].NeedsReview || e.Selected() != 0 || !e.Reviewing {
		t.Fatalf("event = %+v", e)
	}
	if e.Detail.Source != "Project config - "+fixtureOneSource {
		t.Fatalf("source = %q: the wrapped path was not joined", e.Detail.Source)
	}
	if !own.Matches(e.Detail) || e.Detail.Trusted() {
		t.Fatalf("detail %+v: want matev2's own, untrusted", e.Detail)
	}
	// A different command, or the same command from another file, is not
	// matev2's own hook.
	for _, other := range []OwnHook{
		{Event: "SessionStart", Source: fixtureOneSource, Command: "'/usr/local/bin/matev2' hook mate-stop"},
		{Event: "SessionStart", Source: "/elsewhere/.codex/hooks.json", Command: fixtureOwnCommand},
		{Event: "Stop", Source: fixtureOneSource, Command: fixtureOwnCommand},
	} {
		if other.Matches(e.Detail) {
			t.Errorf("%+v matched %+v", other, e.Detail)
		}
	}
}

// TestHookEventMatchesAcrossAWrapThatDroppedASlash is the screen of the
// first full lab run of TestLiveCodexMateRecallHook (2026-09-24), which the
// settle refused: the path broke at `/`, the `/` was not drawn, and the
// command broke after a hyphen.
func TestHookEventMatchesAcrossAWrapThatDroppedASlash(t *testing.T) {
	const (
		root    = "/private/tmp/TestLiveCodexMateRecallHook1077437764"
		source  = root + "/001/.matev2/projects/shop/mate/.codex/hooks.json"
		command = "'" + root + "/002/matev2' hook mate-session --harness codex"
	)
	e, ok := ParseCodexHookEvent(startupFixture(t, "codex-0.156.1-hooks-sessionstart-own-wrapped.txt"))
	if !ok {
		t.Fatal("the wrapped hook list did not parse")
	}
	if len(e.Detail.SourceLines) != 2 || len(e.Detail.CommandLines) != 2 {
		t.Fatalf("detail = %+v, want both values drawn over two lines", e.Detail)
	}
	own := OwnHook{Event: "SessionStart", Source: source, Command: command}
	if !own.Matches(e.Detail) {
		t.Fatalf("matev2's own hook did not match its wrapped drawing: %+v", e.Detail)
	}
	// Forgiving one dropped separator at a break forgives nothing else.
	// (`.matev2projects` and `mate- session` do draw exactly like the real
	// values here - the screen lost that one character - and cannot be told
	// apart by any reader of it. Neither can occur as a hook in this
	// review: Codex loads project hooks only from the Mate cwd's own
	// `.codex/hooks.json` chain, and a different command in that file means
	// the file matev2 writes was rewritten.)
	for _, other := range []OwnHook{
		{Event: "SessionStart", Source: root + "/001/.matev2//projects/shop/mate/.codex/hooks.json", Command: command},
		{Event: "SessionStart", Source: root + "/001/.matev2/projects/shop/mate/.codex/hooks.json.bak", Command: command},
		{Event: "SessionStart", Source: root + "/001/.matev2-projects/shop/mate/.codex/hooks.json", Command: command},
		{Event: "SessionStart", Source: source, Command: "'" + root + "/002/matev2' hook mate-sessionX --harness codex"},
		{Event: "SessionStart", Source: source, Command: "'" + root + "/002/matev2' hook mate-session"},
		{Event: "Stop", Source: source, Command: command},
	} {
		if other.Matches(e.Detail) {
			t.Errorf("%+v matched the drawing", other)
		}
	}
}

func TestWrapMatch(t *testing.T) {
	for _, tc := range []struct {
		lines []string
		want  string
		ok    bool
	}{
		{[]string{"/a/b/"}, "/a/b/", true},
		{[]string{"/a/", "b/c"}, "/a/b/c", true},
		{[]string{"/a", "b/c"}, "/a/b/c", true},
		{[]string{"x mate-", "session"}, "x mate-session", true},
		{[]string{"x hook", "mate"}, "x hook mate", true},
		{[]string{"/a", "b/c"}, "/ab/c", true},
		{[]string{"/a", "b/c"}, "/a//b/c", false},
		{[]string{"/a", "b/c"}, "/a-b/c", false},
		{[]string{"/a", "b/c"}, "/a/b/c/d", false},
		{nil, "", false},
	} {
		if got := wrapMatch(tc.lines, tc.want); got != tc.ok {
			t.Errorf("wrapMatch(%q, %q) = %v, want %v", tc.lines, tc.want, got, tc.ok)
		}
	}
}

func TestHookEventTwoHooksOneForeign(t *testing.T) {
	own := OwnHook{Event: "SessionStart", Source: fixtureTwoSource, Command: fixtureOwnCommand}
	first, ok := ParseCodexHookEvent(startupFixture(t, "codex-0.156.1-hooks-sessionstart-two-foreign-selected.txt"))
	if !ok || len(first.Hooks) != 2 || first.Selected() != 0 || first.NeedReview != 2 {
		t.Fatalf("two-hook list = %+v (ok %v)", first, ok)
	}
	if own.Matches(first.Detail) {
		t.Fatalf("the operator's user-config hook matched matev2's: %+v", first.Detail)
	}
	if first.Detail.Source[:len("User config - ")] != "User config - " {
		t.Fatalf("source = %q", first.Detail.Source)
	}
	second, ok := ParseCodexHookEvent(startupFixture(t, "codex-0.156.1-hooks-sessionstart-two-own-selected.txt"))
	if !ok || second.Selected() != 1 || !own.Matches(second.Detail) {
		t.Fatalf("second hook = %+v (ok %v)", second, ok)
	}
	trusted, ok := ParseCodexHookEvent(startupFixture(t, "codex-0.156.1-hooks-sessionstart-two-own-trusted.txt"))
	if !ok || trusted.NeedReview != 1 || trusted.Hooks[1].NeedsReview || !trusted.Detail.Trusted() || trusted.Reviewing {
		t.Fatalf("after t on the second hook = %+v (ok %v)", trusted, ok)
	}
	all, ok := ParseCodexHookEvent(startupFixture(t, "codex-0.156.1-hooks-sessionstart-two-all-trusted.txt"))
	if !ok || all.NeedReview != 0 || all.Hooks[0].NeedsReview || all.Hooks[1].NeedsReview {
		t.Fatalf("all trusted = %+v (ok %v)", all, ok)
	}
}
