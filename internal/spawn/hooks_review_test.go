package spawn_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

// Codex's hook review (task 37), driven through StartMate over the screens
// captured on codex-cli 0.156.1. The captures were taken with a lab Mate
// directory and a stand-in mate path; ownScreen puts this test's own path
// and command where the capture had those, and nothing else changes.

const (
	captureCommand  = "'/usr/local/bin/mate' hook mate-session"
	captureOneBlock = "  Source    Project config - /private/tmp/TestLiveZZMeasureCodexHookReview323707510/001/\n            codexlab/.codex/hooks.json"
	captureTwoBlock = "  Source    Project config - /private/tmp/TestLiveZZMeasureCodexHookReview8854139/001/\n            codexlab/.codex/hooks.json"
)

// ownScreen rewrites a capture's project source and command to the Mate's.
func ownScreen(t *testing.T, capture, block, source, command string) string {
	t.Helper()
	if !strings.Contains(capture, block) || !strings.Contains(capture, captureCommand) {
		t.Fatal("the capture does not hold the source block and command this test rewrites")
	}
	s := strings.Replace(capture, block, "  Source    Project config - "+source, 1)
	return strings.Replace(s, captureCommand, command, 1)
}

// trustedScreen is what `t` draws on the one-hook list: the item marked
// [x], its trust Trusted, and the header and footer of a list with nothing
// left to review (as the two-hook captures show them).
func trustedScreen(untrusted string) string {
	s := strings.Replace(untrusted, "› [!] Hook 1 · new", "› [x] Hook 1", 1)
	s = strings.Replace(s, "  1 hook needs review before it can run.", "  Turn hooks on or off. Your changes are saved automatically.", 1)
	s = strings.Replace(s, "Trust     New hook - review required", "Trust     Trusted", 1)
	return strings.Replace(s, "  t trust · esc back", "  space/enter toggle · esc back", 1)
}

func TestStartMateCodexTrustsItsOwnHookAndNothingElse(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	mateDir := w.MateDir("shop")
	source := harness.CodexHooksPath(mateDir)
	command := harness.SessionHookCommand(deps.Binary, harness.KindCodex)

	own := ownScreen(t, screen(t, "codex-0.156.1-hooks-sessionstart-own.txt"), captureOneBlock, source, command)
	pane := newScriptedPane(t, rt,
		scriptStep{screen: screen(t, "codex-0.156.1-hooks-review.txt"), key: "enter"},
		scriptStep{screen: screen(t, "codex-0.156.1-hooks-table-review.txt"), key: "enter"},
		scriptStep{screen: own, key: "t"},
		scriptStep{screen: trustedScreen(own), key: "esc"},
		scriptStep{screen: screen(t, "codex-0.156.1-hooks-table-trusted.txt"), key: "esc"},
		scriptStep{screen: screen(t, "codex-0.156.1-ready.txt")},
	)
	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if !res.HooksTrusted {
		t.Fatal("the start must report that it trusted the Mate's own hook")
	}
	if got := strings.Join(pane.sent(), ","); got != "enter,enter,t,esc,esc" {
		t.Fatalf("presses = %s, want enter (Review hooks), enter (SessionStart), t, esc, esc", got)
	}
	// The first enter confirmed "1. Review hooks", never "2. Trust all".
	if !(harness.Codex{}).Screen().StartupTargetSelected(harness.StartupScreenHooksReview, pane.screensPressedOn()[0]) {
		t.Fatal("enter was pressed on the dialog while the highlight was not on 1. Review hooks")
	}

	// The file the review named is the file the start wrote.
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("the Codex Mate's hooks file: %v", err)
	}
	var hooks struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &hooks); err != nil {
		t.Fatalf("hooks.json: %v\n%s", err, data)
	}
	entries := hooks.Hooks["SessionStart"]
	if len(hooks.Hooks) != 1 || len(entries) != 1 || len(entries[0].Hooks) != 1 {
		t.Fatalf("hooks.json = %s, want one SessionStart hook and nothing else", data)
	}
	h := entries[0].Hooks[0]
	if h["command"] != command || h["additionalContextLimit"] != float64(harness.CodexHookContextLimit) {
		t.Fatalf("hook = %+v, want %q with additionalContextLimit %d", h, command, harness.CodexHookContextLimit)
	}
}

func TestStartMateCodexRefusesAReviewThatListsAForeignHook(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	// The operator's user-config hook is listed first and selected; which
	// file mate's own would be in does not matter, it is never reached.
	foreignFirst := screen(t, "codex-0.156.1-hooks-sessionstart-two-foreign-selected.txt")
	pane := newScriptedPane(t, rt,
		scriptStep{screen: screen(t, "codex-0.156.1-hooks-review-two.txt"), key: "enter"},
		scriptStep{screen: screen(t, "codex-0.156.1-hooks-table-review-two.txt"), key: "enter"},
		scriptStep{screen: foreignFirst},
	)
	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex})
	if err == nil {
		t.Fatal("StartMate trusted a review that lists the operator's own hook")
	}
	if !strings.Contains(err.Error(), "not mate's own") || !strings.Contains(err.Error(), "User config") {
		t.Fatalf("refusal = %v, want it to name the foreign hook", err)
	}
	for _, k := range pane.sent() {
		if k == "t" {
			t.Fatalf("presses %v include a trust", pane.sent())
		}
	}
	if _, statErr := os.Stat(w.MateMeta("shop")); statErr == nil {
		t.Fatal("a refused start left mate.meta behind")
	}
}

func TestStartMateCodexRefusesAReviewOutsideSessionStart(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	// The table says two hooks need review, but only one is a SessionStart
	// hook: the other is under an event mate never installs for.
	table := strings.Replace(screen(t, "codex-0.156.1-hooks-table-review.txt"),
		"  ⚠ 1 hook needs review before it can run.", "  ⚠ 2 hooks need review before they can run.", 1)
	table = strings.Replace(table, "  PreToolUse            0           0           0", "  PreToolUse            1           0           1", 1)
	pane := newScriptedPane(t, rt,
		scriptStep{screen: screen(t, "codex-0.156.1-hooks-review-two.txt"), key: "enter"},
		scriptStep{screen: table},
	)
	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex})
	if err == nil || !strings.Contains(err.Error(), "2 hook(s) need review, 1 of them under SessionStart") {
		t.Fatalf("StartMate = %v, want a refusal naming the counts", err)
	}
	if got := strings.Join(pane.sent(), ","); got != "enter" {
		t.Fatalf("presses = %s, want only the enter that opened the review", got)
	}
}

func TestStartMateClaudeWritesNoCodexHooks(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude}); err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if _, err := os.Stat(harness.CodexHooksPath(w.MateDir("shop"))); !os.IsNotExist(err) {
		t.Fatalf("a Claude Mate got a Codex hooks file: %v", err)
	}
}
