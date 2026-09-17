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
