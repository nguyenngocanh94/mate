package main

import (
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
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
	if !strings.HasPrefix(out, "stowed; ") || !strings.Contains(out, "stopped") || !strings.Contains(out, "is running") {
		t.Fatalf("restart line = %q, want it to open with the stow outcome and name both halves of what happened", out)
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

	// The one line a restart types (task 37, B7) is the stow line, into
	// the Mate that is about to go, through the outbox; nothing else.
	lines := sentLines(t, f.ws)
	if len(lines) != 1 || lines[0].Source != store.SourceApp || lines[0].Target != store.TargetMate || lines[0].Text != memory.StowLine {
		t.Fatalf("sent.log after the restart = %+v, want exactly the stow line to the Mate", lines)
	}
	typed := f.rt.SentText
	if len(typed) != 1 || typed[0].Handle.Name != before[spawn.MetaAgent] || typed[0].Text != send.Marker+memory.StowLine {
		t.Fatalf("typed into panes = %+v, want the marked stow line into the old Mate only", typed)
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

// TestConsoleRestartMateKeepsItsHarness: a restart brings back the Mate
// the project had, on the same harness. StartMate alone launches the
// workspace default, and a Codex Mate restarted from the console came back
// as a fresh Claude Mate, with neither its conversation nor its harness
// (found 2026-09-24 writing task 38's acceptance).
func TestConsoleRestartMateKeepsItsHarness(t *testing.T) {
	ws, deps := consoleFixture(t, "shop")
	action := consoleAction(ws, deps)
	if _, err := action(context.Background(), console.ActionRequest{
		Action: console.ActionStart, Target: "shop", TargetKind: "mate", Harness: query.HarnessKind("codex"),
	}); err != nil {
		t.Fatalf("start a Codex Mate: %v", err)
	}
	if _, err := action(context.Background(), console.ActionRequest{
		Action: console.ActionStop, Target: "shop", TargetKind: "mate",
	}); err != nil {
		t.Fatalf("stop it: %v", err)
	}
	out, err := action(context.Background(), console.ActionRequest{
		Action: console.ActionRestartMate, Target: "shop", TargetKind: "project",
	})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if !strings.Contains(out, "is running on codex") {
		t.Fatalf("restart line = %q, want the Mate back on codex", out)
	}
	meta, err := ws.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaHarness] != "codex" {
		t.Fatalf("mate.meta harness after the restart = %q, want codex", meta[spawn.MetaHarness])
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

// TestConsoleRestartHeldByUnsentTextAsksFirst is B7's one refusal: the
// captain's own words in the Mate's composer are never typed over and never
// discarded without being asked. The first press restarts nothing and says
// what it found; the second, within the window, restarts without stowing.
func TestConsoleRestartHeldByUnsentTextAsksFirst(t *testing.T) {
	f := newBoxFixture(t)
	f.rt.SetReadOutput(f.mate, claudePendingScreen)
	before, err := f.ws.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	restart := console.ActionRequest{Action: console.ActionRestartMate, Target: "shop", TargetKind: "project"}

	out, err := f.action(context.Background(), restart)
	if err != nil {
		t.Fatalf("first press: %v", err)
	}
	t.Logf("first press: %s", out)
	if !strings.HasPrefix(out, "held: ") || !strings.Contains(out, `"half typed"`) || !strings.Contains(out, "Restart again") {
		t.Fatalf("first press = %q, want it held, quoting the unsent text and asking for a second press", out)
	}
	if len(f.rt.SentText) != 0 || len(sentLines(t, f.ws)) != 0 {
		t.Fatalf("a held restart typed or recorded something: %+v", f.rt.SentText)
	}
	if meta, _ := f.ws.ReadMateMeta("shop"); meta[spawn.MetaAgent] != before[spawn.MetaAgent] || meta[spawn.MetaStartedAt] != before[spawn.MetaStartedAt] {
		t.Fatalf("a held restart restarted the Mate: %+v", meta)
	}

	out, err = f.action(context.Background(), restart)
	if err != nil {
		t.Fatalf("second press: %v", err)
	}
	t.Logf("second press: %s", out)
	if !strings.HasPrefix(out, "not stowed: the composer held unsent text; restarted on your confirmation; stopped ") || !strings.Contains(out, "is running") {
		t.Fatalf("second press = %q, want a restart that says it did not stow and why", out)
	}
	if len(f.rt.SentText) != 0 {
		t.Fatalf("the confirmed restart typed over the captain's text: %+v", f.rt.SentText)
	}
}

// TestConsoleRestartAfterTheCeilingSaysSoAndRestarts: a Mate that takes the
// stow line and never ends its turn does not hold the restart hostage. The
// restart goes ahead once the ceiling passes, and says it did.
func TestConsoleRestartAfterTheCeilingSaysSoAndRestarts(t *testing.T) {
	f := newBoxFixture(t)
	f.rt.OnSendText = func(h runtime.AgentHandle, _ string) {
		// The stow line went in; the Mate is busy from here on.
		f.rt.SetReadOutput(h, claudeBusyBoxScreen)
	}
	out, err := f.action(context.Background(), console.ActionRequest{Action: console.ActionRestartMate, Target: "shop", TargetKind: "project"})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	t.Logf("restart: %s", out)
	if !strings.HasPrefix(out, "not stowed: the Mate had not finished its stow turn after 3 minutes; stopped ") || !strings.Contains(out, "is running") {
		t.Fatalf("restart line = %q, want the ceiling named and the restart done", out)
	}
	if len(f.rt.SentText) != 1 {
		t.Fatalf("typed %+v, want the stow line once", f.rt.SentText)
	}
}

// TestConsoleRestartOfAGoneMateSaysItDidNotStow: nothing to ask, and the
// outcome line says so rather than implying a stow happened.
func TestConsoleRestartOfAGoneMateSaysItDidNotStow(t *testing.T) {
	f := newBoxFixture(t)
	if _, err := f.action(context.Background(), console.ActionRequest{Action: console.ActionStop, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatal(err)
	}
	out, err := f.action(context.Background(), console.ActionRequest{Action: console.ActionRestartMate, Target: "shop", TargetKind: "project"})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if !strings.HasPrefix(out, "not stowed: the Mate was not running; ") {
		t.Fatalf("restart line = %q", out)
	}
	items, err := f.ws.ReadOutbox("shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("a restart of a gone Mate queued %+v", items)
	}
}

// claudeBusyBoxScreen is Claude mid-turn: its own spinner at column 0 above
// the composer.
const claudeBusyBoxScreen = "✶ Pollinating… (3s · esc to interrupt)\n" + claudeEmptyScreen
