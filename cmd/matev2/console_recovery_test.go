package main

import (
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/ui/console"
)

// The two recovery actions the Mate row's Actions menu offers as
// restart_mate and clear_composer (internal/ui/console/actions.go), over the
// fake Herdr adapter. They exist because a Mate shares its composer with the
// reader: a stray key sequence leaves junk in it, and an agent can stop
// answering altogether. What is under test is the bridge - which spawn seams
// each one drives, and what it does *not* touch.

// TestConsoleRestartMateStopsThenStartsTheSameProject.
func TestConsoleRestartMateStopsThenStartsTheSameProject(t *testing.T) {
	f := newBoxFixture(t)
	before, err := f.ws.ReadMateMeta("shop")
	if err != nil {
		t.Fatalf("ReadMateMeta: %v", err)
	}

	out, err := f.action(context.Background(), console.ActionRequest{
		Action: console.ActionRestartMate, Target: "shop", TargetKind: "project",
	})
	if err != nil {
		t.Fatalf("restart action: %v", err)
	}
	t.Logf("restart action: %s", out)
	if !strings.Contains(out, "stopped") || !strings.Contains(out, "is running") {
		t.Fatalf("restart line = %q, want it to name both halves of what happened", out)
	}

	status, err := spawn.MateStatus(context.Background(), f.ws, f.deps, "shop")
	if err != nil {
		t.Fatalf("MateStatus after the restart: %v", err)
	}
	if status.State != spawn.StateRunning {
		t.Fatalf("state after the restart = %q, want a running Mate", status.Line())
	}
	after, err := f.ws.ReadMateMeta("shop")
	if err != nil {
		t.Fatalf("ReadMateMeta after the restart: %v", err)
	}
	if after[spawn.MetaAgent] == "" {
		t.Fatalf("mate.meta names no agent after the restart: %+v", after)
	}
	if before[spawn.MetaAgent] == "" {
		t.Fatalf("setup: the fixture Mate was not running before the restart: %+v", before)
	}

	// A restart is not a message: nothing about it goes into anybody's
	// composer, so sent.log must be exactly as it was.
	if lines := sentLines(t, f.ws); len(lines) != 0 {
		t.Fatalf("the restart wrote to sent.log: %+v", lines)
	}
}

// TestConsoleRestartMateStartsAMateThatWasAlreadyGone: the state a reader
// reaching for a restart is most often already in. StopMate reporting
// "already gone" is not a failure, and the start must still happen -
// otherwise the one gesture that exists to bring a Mate back leaves the
// project with none at all.
func TestConsoleRestartMateStartsAMateThatWasAlreadyGone(t *testing.T) {
	f := newBoxFixture(t)
	if _, err := f.action(context.Background(), console.ActionRequest{
		Action: console.ActionStop, Target: "shop", TargetKind: "mate",
	}); err != nil {
		t.Fatalf("stop the Mate first: %v", err)
	}

	out, err := f.action(context.Background(), console.ActionRequest{
		Action: console.ActionRestartMate, Target: "shop", TargetKind: "project",
	})
	if err != nil {
		t.Fatalf("restart action on a stopped Mate: %v", err)
	}
	t.Logf("restart action: %s", out)
	if !strings.Contains(out, "already gone") {
		t.Fatalf("restart line = %q, want it to say the previous Mate was already gone", out)
	}
	status, err := spawn.MateStatus(context.Background(), f.ws, f.deps, "shop")
	if err != nil {
		t.Fatalf("MateStatus: %v", err)
	}
	if status.State != spawn.StateRunning {
		t.Fatalf("state after the restart = %q, want a running Mate", status.Line())
	}
}

// TestConsoleClearComposerPressesCtrlUAndSendsNothing: the point of this
// action is that the composer is in a state nobody can classify, so it types
// nothing, submits nothing and records nothing - it only presses the key
// that removes what is there.
func TestConsoleClearComposerPressesCtrlUAndSendsNothing(t *testing.T) {
	f := newBoxFixture(t)
	f.rt.SetReadOutput(f.mate, claudePendingScreen)

	out, err := f.action(context.Background(), console.ActionRequest{
		Action: console.ActionClearComposer, Target: "shop", TargetKind: "project",
	})
	if err != nil {
		t.Fatalf("clear composer action: %v", err)
	}
	t.Logf("clear composer action: %s", out)
	if !strings.Contains(out, clearComposerKey) || !strings.Contains(out, "nothing was sent") {
		t.Fatalf("clear line = %q, want it to name the key and say nothing was sent", out)
	}

	keys := f.rt.SentKeys
	if len(keys) != 1 || len(keys[0].Keys) != 1 || keys[0].Keys[0] != clearComposerKey {
		t.Fatalf("SendKeys calls = %+v, want exactly one %q", keys, clearComposerKey)
	}
	if keys[0].Handle.Name != f.mate.Name {
		t.Fatalf("SendKeys went to %q, want the Mate %q", keys[0].Handle.Name, f.mate.Name)
	}
	if len(f.rt.SentText) != 0 {
		t.Fatalf("the composer clear typed text: %+v", f.rt.SentText)
	}
	if lines := sentLines(t, f.ws); len(lines) != 0 {
		t.Fatalf("the composer clear wrote to sent.log: %+v", lines)
	}
}

// TestConsoleClearComposerRefusesAProjectWithNoMate: there is no pane to
// press a key in, and the refusal says so rather than reporting a success it
// did not establish.
func TestConsoleClearComposerRefusesAProjectWithNoMate(t *testing.T) {
	f := newBoxFixture(t)
	if _, err := f.action(context.Background(), console.ActionRequest{
		Action: console.ActionStop, Target: "shop", TargetKind: "mate",
	}); err != nil {
		t.Fatalf("stop the Mate first: %v", err)
	}
	if _, err := f.action(context.Background(), console.ActionRequest{
		Action: console.ActionClearComposer, Target: "shop", TargetKind: "project",
	}); err == nil {
		t.Fatal("clearing the composer of a stopped Mate returned no error")
	}
	if len(f.rt.SentKeys) != 0 {
		t.Fatalf("a refused clear still pressed keys: %+v", f.rt.SentKeys)
	}
}
