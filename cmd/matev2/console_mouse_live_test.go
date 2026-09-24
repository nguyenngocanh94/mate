package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/brief/brieftest"
	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/process"
	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/ui/console"
)

// TestLiveConsoleMouseDrivesTheBox is the focus model's live proof, and the
// one thing no unit test can establish: that the real Console, under a real
// terminal, in front of a real Mate and a real Codex crew, sends a keystroke
// to the place the screen says it will.
//
// It runs the actual `matev2 <workspace>` binary under a pty
// (scripts/ptysmoke) and drives it with SGR mouse sequences, the same bytes
// a terminal emulator sends:
//
//  1. a real Claude Mate and a real Codex crew with a needs-decision brief
//  2. the Console is opened and navigated to the Mate's session view
//  3. a click on each of the rail header's two filters - and the count line
//     under them changes from "N waiting" to the whole log and back
//  4. a click on the body of the rail entry - and the frame that comes back
//     is the crew's own session view, named after the crew's agent, which it
//     can only be if the Mate's stream was closed and the crew's opened
//  5. a click in the terminal zone, then "say PONG" and Enter typed bare -
//     and sent.log records a Mate turn, which it can only do if those
//     keystrokes reached the Mate's own composer through the PTY
//  6. a click on the row's [assign] button - and sent.log records the
//     `resolve:` line against the Mate's pane, which it can only do if the
//     button reached the same ActionFunc TestLiveConsoleInboxResolve drives
//
// Steps 4, 5 and 6 together are the claim of the 2026-09-19 box: one press
// on a row goes to that crew's pane, one press on its one button goes to
// the Mate, and the same bare keys afterwards go to whichever zone was
// clicked last - with the hint line on the captured frames saying which.
func TestLiveConsoleMouseDrivesTheBox(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	consoleUseLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repo: repo, DefaultBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	names := runtime.NewMemoryNameRegistry()
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = names
	rt.StartServer = func(context.Context, string) error {
		t.Fatal("the lab session is provisioned by the runner; this test must not start a Herdr server")
		return nil
	}
	binary := consoleBinaryPath(t)
	deps := spawn.Deps{
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               binary,
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 60 * time.Second,
		StartTimeout:         60 * time.Second,
	}
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })

	ctx, cancel := context.WithTimeout(context.Background(), 13*time.Minute)
	defer cancel()
	action := consoleAction(w, deps)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})

	// 1. The Mate and the crew, through the seams cmdConsole itself wires.
	startOut, err := action(ctx, console.ActionRequest{
		Action: console.ActionStart, Target: "shop", TargetKind: "mate", Harness: query.HarnessClaude})
	if err != nil {
		t.Fatalf("console start action: %v", err)
	}
	t.Logf("start action: %s", startOut)

	crewRes, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop",
		Crew:    "k3",
		Harness: harness.KindCodex,
		BriefText: brieftest.Ship(`Append needs-decision: pick A or B to the status file and stop; ` +
			`when answered, append wait-mate: chose <answer>`),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	t.Logf("spawned crew %s in pane %s (branch %s)", crewRes.Agent, crewRes.Pane, crewRes.Branch)

	crewHandle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    crewRes.Agent, RawID: "k3", Kind: harness.KindCodex,
	}
	paneTail := func() string {
		screen, readErr := rt.ReadAgent(ctx, crewHandle, 40)
		if readErr != nil {
			return "(pane not readable: " + readErr.Error() + ")"
		}
		return harness.StartupScreenTail(screen, 20)
	}
	ask := waitForBoxEntry(t, ctx, w, "shop", 180*time.Second, paneTail, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Verb == "needs-decision"
	})
	t.Logf("box shows %s %s (attention=%v)", ask.Verb, ask.Text, ask.Attention)

	smoke := ptysmokeBinary(t)
	// enterSession is the navigation every run starts with: Enter opens the
	// project frame, Enter again opens the Mate's session view.
	const enterSession = `3500:\r;2000:\r`

	// 2. The first run only looks. Its frame is the terminal-focused state,
	// and it is also where the rail's own coordinates are read from, so the
	// gestures below are aimed at what the Console actually drew rather than
	// at an arithmetic guess that would rot the next time the rail changed.
	terminalFrame := runPtysmoke(t, smoke, binary, root, enterSession, 6*time.Second)
	t.Logf("frame 1 - terminal focus:\n%s", terminalFrame)
	assertFrameSays(t, terminalFrame, "TERMINAL", "every key goes to the agent", "F2")

	// The inbox item is the row carrying the action strip. It is matched on
	// the strip rather than on the verb because the strip is what one of the
	// clicks below is aimed at, and a row without one has no button to hit.
	entryRow, ok := frameRowOf(terminalFrame, "[assign]")
	if !ok {
		t.Fatalf("no entry with an action strip in the console frame:\n%s", terminalFrame)
	}
	if line := strings.Split(terminalFrame, "\n")[entryRow]; !strings.Contains(line, "k3") {
		t.Fatalf("the strip row %q does not name the crew:\n%s", line, terminalFrame)
	}
	t.Logf("the inbox row says: %s", strings.Split(terminalFrame, "\n")[entryRow])

	// 3. The header's two filters, which are the only labels on it. Each one
	// names the list it wants, and the count line under them says which is
	// on in words rather than by colour alone.
	allRow, ok := frameRowOf(terminalFrame, "[all]")
	if !ok {
		t.Fatalf("no filter row in the console frame:\n%s", terminalFrame)
	}
	allCol, ok := frameColOf(terminalFrame, allRow, "[all]")
	if !ok {
		t.Fatalf("no [all] filter on row %d:\n%s", allRow, terminalFrame)
	}
	waitingCol, ok := frameColOf(terminalFrame, allRow, "[waiting]")
	if !ok {
		t.Fatalf("no [waiting] filter on row %d:\n%s", allRow, terminalFrame)
	}
	allFrame := runPtysmoke(t, smoke, binary, root,
		fmt.Sprintf(`%s;1200:@click:%d,%d`, enterSession, allCol, allRow), 4*time.Second)
	t.Logf("frame 2 - after [all]:\n%s", allFrame)
	// The count line is the colour-free half of the signal: in `[all]` it
	// names the log's own size instead of what is waiting.
	if !strings.Contains(allFrame, "all ") || strings.Contains(allFrame, "1 waiting") {
		t.Fatalf("[all] did not swap the count line for the log's own:\n%s", allFrame)
	}
	backFrame := runPtysmoke(t, smoke, binary, root,
		fmt.Sprintf(`%s;1200:@click:%d,%d;600:@click:%d,%d`,
			enterSession, allCol, allRow, waitingCol, allRow), 4*time.Second)
	t.Logf("frame 3 - after [all] then [waiting]:\n%s", backFrame)
	if !strings.Contains(backFrame, "1 waiting") {
		t.Fatalf("[waiting] did not bring the inbox back:\n%s", backFrame)
	}

	// 4. One press on the row body - not on the button - and the frame that
	// comes back is the crew's own pane. The Mate's stream has to be closed
	// before the crew's is opened, so the settle here covers both.
	openScript := fmt.Sprintf(`%s;1500:@click:3,%d`, enterSession, entryRow)
	crewFrame := runPtysmoke(t, smoke, binary, root, openScript, 10*time.Second)
	t.Logf("frame 4 - after one press on the row body:\n%s", crewFrame)
	// A Crew frame has no rail at any width (session_render.go), so its hint
	// line offers the console rather than the box - and the header names the
	// crew's own agent, which no Mate frame ever does.
	assertFrameSays(t, crewFrame, crewRes.Agent, "TERMINAL", "F2 console")
	// And the rail itself is gone: [waiting] is drawn by the Mate's rail
	// header and by nothing else, so its absence is the proof that this is a
	// different frame rather than the same one with new words on it.
	if strings.Contains(crewFrame, "[waiting]") {
		t.Fatalf("the frame is still the Mate's rail after the row was clicked:\n%s", crewFrame)
	}

	// 5. The other zone. A click in the terminal, then bare keys - the same
	// keys that a moment ago were the box's - and the Mate answers.
	//
	// It comes before the [assign] click on purpose: a Claude Code that has
	// not had a turn yet is still drawing its startup screen, and
	// internal/send refuses to type into a composer it cannot name (that
	// refusal is real, and the outcome line reports it). One exchange puts
	// the Mate on an ordinary conversation screen, which is the state a
	// reader assigning an item is actually in.
	before := len(sentEntries(t, w, "shop"))
	paneScript := fmt.Sprintf(`%s;1200:@click:90,20;800:say PONG;600:\r`, enterSession)
	paneFrame := runPtysmoke(t, smoke, binary, root, paneScript, 8*time.Second)
	t.Logf("frame 5 - terminal focus, after typing into the Mate:\n%s", paneFrame)
	assertFrameSays(t, paneFrame, "TERMINAL")

	user := waitForSent(t, ctx, w, "shop", 120*time.Second, func(e store.SentEntry) bool {
		return e.Source == store.SourceUser && e.Target == store.TargetMate &&
			strings.Contains(e.Text, "PONG")
	})
	t.Logf("the Mate received the typed line: %q", user.Text)
	mate := waitForSent(t, ctx, w, "shop", 240*time.Second, func(e store.SentEntry) bool {
		return e.Source == store.SourceMate
	})
	t.Logf("the Mate answered: %s", mate.Text)
	if after := len(sentEntries(t, w, "shop")); after <= before {
		t.Fatalf("sent.log did not grow: %d entries before, %d after", before, after)
	}

	// 6. The one button. Its coordinates are read off a frame captured in
	// the same state the click will find, so they are the Console's own
	// rather than an arithmetic guess.
	assignFrame := runPtysmoke(t, smoke, binary, root, enterSession, 6*time.Second)
	assignRow, ok := frameRowOf(assignFrame, "[assign]")
	if !ok {
		t.Fatalf("no [assign] button in the console frame:\n%s", assignFrame)
	}
	assignCol, ok := frameColOf(assignFrame, assignRow, "[assign]")
	if !ok {
		t.Fatalf("no [assign] button on row %d:\n%s", assignRow, assignFrame)
	}
	assignScript := fmt.Sprintf(`%s;1500:@move:%d,%d;500:@click:%d,%d`,
		enterSession, assignCol, assignRow, assignCol, assignRow)
	assignedFrame := runPtysmoke(t, smoke, binary, root, assignScript, 10*time.Second)
	t.Logf("frame 6 - after the [assign] button:\n%s", assignedFrame)

	// The claim is the same one TestLiveConsoleInboxResolve makes of the
	// action itself: the `resolve:` line reached the Mate's pane, recorded
	// from the app against sent.log.
	assertSentLine(t, w, "shop", store.SourceApp, store.TargetMate, ask.Resolve)
	t.Logf("assigned: %s", ask.Resolve)

	if _, err := spawn.StopCrew(ctx, w, deps, "shop", "k3", true); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	stopOut, err := action(ctx, console.ActionRequest{Action: console.ActionStop, Target: "shop", TargetKind: "mate"})
	if err != nil {
		t.Fatalf("console stop action: %v", err)
	}
	t.Logf("stop action: %s", stopOut)
}

// ptysmokeBinary builds the pty driver. It is a real terminal - the frames
// it prints went through an emulator, not a byte recorder - which is what
// makes a mouse sequence written into it indistinguishable from one a
// terminal sent.
func ptysmokeBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ptysmoke")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/nguyenngocanh94/matev2/scripts/ptysmoke")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ptysmoke: %v\n%s", err, out)
	}
	return bin
}

// runPtysmoke drives one whole Console run and returns the frame it had
// drawn. -no-quit is not optional here: ptysmoke's default exit types a "q",
// and under terminal focus that is a character the Mate would receive.
func runPtysmoke(t *testing.T, smoke, binary, workspace, script string, settle time.Duration) string {
	t.Helper()
	cmd := exec.Command(smoke, "-no-quit", "-settle", settle.String(), "-in", script, binary, workspace)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ptysmoke -in %q: %v\n%s", script, err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// frameRowOf is the 0-based row a substring appears on.
func frameRowOf(frame, want string) (int, bool) {
	for i, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, want) {
			return i, true
		}
	}
	return 0, false
}

// frameColOf is the 0-based column a substring starts at on one row. It
// counts display cells rather than bytes, because the rail's own glyphs are
// multi-byte and a byte offset would aim the click a few cells to the left.
func frameColOf(frame string, row int, want string) (int, bool) {
	lines := strings.Split(frame, "\n")
	if row < 0 || row >= len(lines) {
		return 0, false
	}
	at := strings.Index(lines[row], want)
	if at < 0 {
		return 0, false
	}
	return len([]rune(lines[row][:at])), true
}

func assertFrameSays(t *testing.T, frame string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(frame, want) {
			t.Fatalf("the frame does not say %q:\n%s", want, frame)
		}
	}
}

func sentEntries(t *testing.T, w *store.Workspace, project string) []store.SentEntry {
	t.Helper()
	entries, _, err := w.ReadSent(project, 0)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadSent: %v", err)
	}
	return entries
}
