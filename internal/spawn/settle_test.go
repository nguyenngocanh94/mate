package spawn_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
)

// The startup-prompt settle is driven through StartMate itself, over the
// screens captured from the real harnesses (ADR 0028). Claude's dialog opens
// with the cursor on "No, exit", so the order of the presses is the whole
// point: Enter must never be sent while that is what is highlighted.

func TestStartMateAnswersTheClaudeTrustDialog(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	dialog := screen(t, "claude-2.1.270-trust-dialog.txt")
	accepted := screen(t, "claude-2.1.270-trust-dialog-accept-selected.txt")
	ready := screen(t, "claude-2.1.270-ready.txt")
	rt.NextStartupScreen = dialog

	// What the pane showed at the moment of each press, recorded so the
	// test can prove Enter was sent only once the accept option was
	// highlighted.
	var mu sync.Mutex
	var pressedOn []string
	current := dialog
	rt.OnSendKeys = func(handle runtime.AgentHandle, keys []string) {
		mu.Lock()
		pressedOn = append(pressedOn, current)
		switch keys[0] {
		case "down":
			current = accepted
		case "enter":
			current = ready
		}
		next := current
		mu.Unlock()
		rt.SetReadOutput(handle, next)
	}

	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if !res.TrustDialog {
		t.Fatal("the start must report that it answered the trust dialog")
	}

	var keys []string
	for _, sent := range rt.SentKeys {
		if len(sent.Keys) != 1 {
			t.Fatalf("send-keys call %v carries more than one key; ADR 0028 measured that as leaving the dialog up", sent.Keys)
		}
		keys = append(keys, sent.Keys[0])
	}
	if strings.Join(keys, ",") != "down,enter" {
		t.Fatalf("presses = %v, want down then enter", keys)
	}
	if len(pressedOn) != 2 {
		t.Fatalf("recorded %d presses, want 2", len(pressedOn))
	}
	if selected, _ := harness.TrustDialogAcceptSelected(harness.KindClaude, pressedOn[0]); selected {
		t.Fatal("the fixture must start with the accept option NOT selected")
	}
	selected, err := harness.TrustDialogAcceptSelected(harness.KindClaude, pressedOn[1])
	if err != nil {
		t.Fatal(err)
	}
	if !selected {
		t.Fatal("enter was sent while the highlight was not on the accept option")
	}
}

func TestStartMateAnswersTheCodexTrustDialog(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	dialog := screen(t, "codex-0.154.0-trust-dialog.txt")
	ready := screen(t, "codex-0.154.0-ready.txt")
	rt.NextStartupScreen = dialog
	rt.OnSendKeys = func(handle runtime.AgentHandle, keys []string) {
		if keys[0] == "enter" {
			rt.SetReadOutput(handle, ready)
		}
	}

	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if !res.TrustDialog {
		t.Fatal("the start must report that it answered the trust dialog")
	}
	var keys []string
	for _, sent := range rt.SentKeys {
		keys = append(keys, sent.Keys...)
	}
	if strings.Join(keys, ",") != "1,enter" {
		t.Fatalf("presses = %v, want 1 then enter (codex opens with option 1 highlighted)", keys)
	}
}

func TestStartMateRefusesToConfirmAnUnmovedHighlight(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	// The select press does not move the highlight: "No, exit" stays under
	// the cursor, so Enter would kill the agent.
	rt.NextStartupScreen = screen(t, "claude-2.1.270-trust-dialog.txt")

	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err == nil {
		t.Fatal("the start must be refused when the highlight did not move")
	}
	if !strings.Contains(err.Error(), "Yes, I trust this folder") {
		t.Fatalf("error = %v, want it to name the accept option", err)
	}
	var keys []string
	for _, sent := range rt.SentKeys {
		keys = append(keys, sent.Keys...)
	}
	if strings.Join(keys, ",") != "down" {
		t.Fatalf("presses = %v, want the select press and nothing else", keys)
	}
	if _, statErr := os.Stat(w.MateMeta("shop")); !os.IsNotExist(statErr) {
		t.Fatal("a refused start must leave no mate.meta")
	}
}

func TestStartMateRefusesAnUnrecognisedScreen(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	rt.NextStartupScreen = "welcome to something nobody measured\nplease choose:\n  a) yes\n  b) no\n"

	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err == nil {
		t.Fatal("an unrecognised startup screen must fail the start")
	}
	if !strings.Contains(err.Error(), "not recognised") {
		t.Fatalf("error = %v, want it to say the screen was not recognised", err)
	}
	if !strings.Contains(err.Error(), "please choose:") {
		t.Fatalf("error = %v, want the screen tail in the message", err)
	}
	if len(rt.SentKeys) != 0 {
		t.Fatalf("keys %v were pressed into a screen matev2 cannot name", rt.SentKeys)
	}
	if len(rt.Tabs) != 0 {
		t.Fatalf("tabs left behind: %v", rt.Tabs)
	}
}

// Codex draws a sequence of modals, not one: measured 2026-09-18 with
// 0.154.0 installed and 0.155.0 published, the release-update prompt comes
// first and the directory-trust dialog only after it is answered. The settle
// answers each with the same discipline - one key per call, a re-read
// between presses, Enter only once the highlight is where matev2 means it.

// scriptedPane drives the fake runtime through a measured screen sequence:
// each (screen, key) step says what the pane shows and which single key
// advances it. It records the screen that was on the pane at the moment of
// every press, so a test can prove Enter was never sent into the wrong
// highlight.
type scriptedPane struct {
	t     *testing.T
	rt    *runtime.Fake
	mu    sync.Mutex
	steps []scriptStep
	at    int
	// pressedOn is the screen each press landed on, in order.
	pressedOn []string
	keys      []string
}

type scriptStep struct {
	screen string
	// key advances the script when it is the key pressed on screen. Empty
	// means the script ends here: the screen stays.
	key string
}

func newScriptedPane(t *testing.T, rt *runtime.Fake, steps ...scriptStep) *scriptedPane {
	t.Helper()
	s := &scriptedPane{t: t, rt: rt, steps: steps}
	rt.NextStartupScreen = steps[0].screen
	rt.OnSendKeys = func(handle runtime.AgentHandle, keys []string) {
		s.mu.Lock()
		if len(keys) != 1 {
			s.mu.Unlock()
			t.Errorf("send-keys call %v carries more than one key; ADR 0028 measured that as leaving the dialog up", keys)
			return
		}
		s.pressedOn = append(s.pressedOn, s.steps[s.at].screen)
		s.keys = append(s.keys, keys[0])
		if s.at+1 < len(s.steps) && s.steps[s.at].key == keys[0] {
			s.at++
		}
		next := s.steps[s.at].screen
		s.mu.Unlock()
		rt.SetReadOutput(handle, next)
	}
	return s
}

func (s *scriptedPane) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.keys...)
}

func (s *scriptedPane) screensPressedOn() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.pressedOn...)
}

func TestStartMateSkipsTheCodexUpdateDialogThenReachesTheComposer(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	update := screen(t, "codex_update_dialog.txt")
	skipSelected := screen(t, "codex_update_dialog_skip_selected.txt")
	ready := screen(t, "codex-0.154.0-ready.txt")
	pane := newScriptedPane(t, rt,
		scriptStep{screen: update, key: "down"},
		// One `down` short of the target: the second press is what moves
		// the highlight onto "3. Skip until next version".
		scriptStep{screen: update, key: "down"},
		scriptStep{screen: skipSelected, key: "enter"},
		scriptStep{screen: ready},
	)

	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if !res.UpdateDialog {
		t.Fatal("the start must report that it skipped the update prompt")
	}
	if res.TrustDialog {
		t.Fatal("no trust dialog was on screen; the start must not claim one")
	}
	if strings.Join(pane.sent(), ",") != "down,down,enter" {
		t.Fatalf("presses = %v, want down, down, enter", pane.sent())
	}
	on := pane.screensPressedOn()
	if len(on) != 3 {
		t.Fatalf("recorded %d presses, want 3", len(on))
	}
	selected, err := harness.UpdateDialogSkipSelected(harness.KindCodex, on[2])
	if err != nil {
		t.Fatal(err)
	}
	if !selected {
		t.Fatal("enter was sent while the highlight was not on \"3. Skip until next version\"")
	}
}

func TestStartMateAnswersTheUpdateDialogThenTheTrustDialog(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	update := screen(t, "codex_update_dialog.txt")
	skipSelected := screen(t, "codex_update_dialog_skip_selected.txt")
	// The live capture of what Enter on option 3 produced.
	trust := screen(t, "codex_update_dialog_after_enter.txt")
	ready := screen(t, "codex-0.154.0-ready.txt")
	pane := newScriptedPane(t, rt,
		scriptStep{screen: update, key: "down"},
		scriptStep{screen: update, key: "down"},
		scriptStep{screen: skipSelected, key: "enter"},
		scriptStep{screen: trust, key: "1"},
		scriptStep{screen: trust, key: "enter"},
		scriptStep{screen: ready},
	)

	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if !res.UpdateDialog || !res.TrustDialog {
		t.Fatalf("both dialogs were answered; result update=%v trust=%v", res.UpdateDialog, res.TrustDialog)
	}
	if strings.Join(pane.sent(), ",") != "down,down,enter,1,enter" {
		t.Fatalf("presses = %v, want the update sequence then the trust sequence", pane.sent())
	}
}

func TestStartMateRefusesToConfirmAnUnmovedUpdateHighlight(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	// The downs do not move the highlight: "1. Update now" stays under the
	// cursor, so Enter would run a package install under the agent.
	rt.NextStartupScreen = screen(t, "codex_update_dialog.txt")

	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex})
	if err == nil {
		t.Fatal("the start must be refused when the highlight did not move")
	}
	if !strings.Contains(err.Error(), "3. Skip until next version") {
		t.Fatalf("error = %v, want it to name the option matev2 meant to take", err)
	}
	var keys []string
	for _, sent := range rt.SentKeys {
		keys = append(keys, sent.Keys...)
	}
	if strings.Join(keys, ",") != "down,down" {
		t.Fatalf("presses = %v, want the two select presses and nothing else", keys)
	}
	if _, statErr := os.Stat(w.MateMeta("shop")); !os.IsNotExist(statErr) {
		t.Fatal("a refused start must leave no mate.meta")
	}
}

// A screen carrying the update prompt's words without its shape is still
// unrecognised, and still refused with no key pressed.
func TestStartMateRefusesAnUnrecognisedScreenCarryingUpdateWords(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	rt.NextStartupScreen = "✨ Update available! 0.154.0 -> 0.155.0\nsomething nobody measured\n  a) yes\n  b) no\n"

	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex})
	if err == nil {
		t.Fatal("an unrecognised startup screen must fail the start")
	}
	if !strings.Contains(err.Error(), "not recognised") {
		t.Fatalf("error = %v, want it to say the screen was not recognised", err)
	}
	if len(rt.SentKeys) != 0 {
		t.Fatalf("keys %v were pressed into a screen matev2 cannot name", rt.SentKeys)
	}
}

// A harness that redraws a dialog matev2 has already confirmed is refused
// rather than answered again: the cap exists so a redraw loop can never
// become an unbounded stream of keypresses.
func TestStartMateRefusesAnUpdateDialogItAlreadyAnswered(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	update := screen(t, "codex_update_dialog.txt")
	skipSelected := screen(t, "codex_update_dialog_skip_selected.txt")
	pane := newScriptedPane(t, rt,
		scriptStep{screen: update, key: "down"},
		scriptStep{screen: update, key: "down"},
		scriptStep{screen: skipSelected, key: "enter"},
		// Enter left the same prompt on screen.
		scriptStep{screen: update},
	)

	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindCodex})
	if err == nil {
		t.Fatal("a dialog still on screen after matev2 confirmed it must fail the start")
	}
	if !strings.Contains(err.Error(), "still on screen") {
		t.Fatalf("error = %v, want it to say the dialog was still on screen", err)
	}
	if strings.Join(pane.sent(), ",") != "down,down,enter" {
		t.Fatalf("presses = %v, want the one answer and nothing after it", pane.sent())
	}
}
