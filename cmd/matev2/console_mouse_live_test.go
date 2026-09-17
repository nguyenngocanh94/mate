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
//  3. a click on the rail entry, a click on its [reply] button, "A", Enter -
//     and the crew continues to `done: chose A`, which it can only do if the
//     reply reached its pane
//  4. a click in the terminal zone, then "say PONG" and Enter typed bare -
//     and sent.log records a Mate turn, which it can only do if those
//     keystrokes reached the Mate's own composer through the PTY
//
// Step 3 and step 4 together are the claim: the same bare keys go to two
// different places depending only on which zone was clicked last, and the
// hint line on the captured frames says which.
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
		BriefText: `Append needs-decision: pick A or B to the status file and stop; ` +
			`when answered, append done: chose <answer>`,
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
	ask := waitForBoxEntry(t, ctx, w, 180*time.Second, paneTail, func(e query.BoxEntry) bool {
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

	// The attention entry is the row carrying the action strip: the rail cuts
	// an entry's own words to fit the buttons, so matching on "needs-decision"
	// would depend on the rail being wide enough to spell it out.
	entryRow, ok := frameRowOf(terminalFrame, "[reply]")
	if !ok {
		t.Fatalf("no entry with an action strip in the console frame:\n%s", terminalFrame)
	}
	if line := strings.Split(terminalFrame, "\n")[entryRow]; !strings.Contains(line, "k3") {
		t.Fatalf("the strip row %q does not name the crew:\n%s", line, terminalFrame)
	}

	// 3. Click the entry, click its [reply] button, answer, Enter. The entry
	// has to be selected before [reply] is on screen at all, which is exactly
	// the sequence a reader performs.
	selectEntry := fmt.Sprintf(`1200:@move:3,%d;300:@click:3,%d`, entryRow, entryRow)
	selectedFrame := runPtysmoke(t, smoke, binary, root, enterSession+";"+selectEntry, 3*time.Second)
	t.Logf("frame 2 - box focus, entry selected:\n%s", selectedFrame)
	assertFrameSays(t, selectedFrame, "BOX", "[→ mate] [reply] [peek]", "F2 terminal")
	if row, ok := frameRowOf(selectedFrame, "[reply]"); !ok || row != entryRow {
		t.Fatalf("the action strip moved from row %d to %d between frames:\n%s", entryRow, row, selectedFrame)
	}

	replyCol, ok := frameColOf(selectedFrame, entryRow, "[reply]")
	if !ok {
		t.Fatalf("no [reply] button on row %d:\n%s", entryRow, selectedFrame)
	}
	replyScript := fmt.Sprintf(`%s;%s;800:@click:%d,%d;800:A;600:\r`,
		enterSession, selectEntry, replyCol, entryRow)
	replyFrame := runPtysmoke(t, smoke, binary, root, replyScript, 5*time.Second)
	t.Logf("frame 3 - after the [reply] button, the answer and Enter:\n%s", replyFrame)

	assertSentLine(t, w, store.SourceUser, store.CrewTarget("k3"), "A")
	done := waitForBoxEntry(t, ctx, w, 240*time.Second, paneTail, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Verb == "done" && strings.Contains(strings.ToLower(e.Text), "chose a")
	})
	t.Logf("the crew continued: %s %s", done.Verb, done.Text)

	// 4. The other zone. A click in the terminal, then bare keys - the same
	// keys that a moment ago were the box's - and the Mate answers.
	before := len(sentEntries(t, w))
	paneScript := fmt.Sprintf(`%s;1200:@click:90,20;800:say PONG;600:\r`, enterSession)
	paneFrame := runPtysmoke(t, smoke, binary, root, paneScript, 8*time.Second)
	t.Logf("frame 4 - terminal focus, after typing into the Mate:\n%s", paneFrame)
	assertFrameSays(t, paneFrame, "TERMINAL")

	user := waitForSent(t, ctx, w, 120*time.Second, func(e store.SentEntry) bool {
		return e.Source == store.SourceUser && e.Target == store.TargetMate &&
			strings.Contains(e.Text, "PONG")
	})
	t.Logf("the Mate received the typed line: %q", user.Text)
	mate := waitForSent(t, ctx, w, 240*time.Second, func(e store.SentEntry) bool {
		return e.Source == store.SourceMate
	})
	t.Logf("the Mate answered: %s", mate.Text)
	if after := len(sentEntries(t, w)); after <= before {
		t.Fatalf("sent.log did not grow: %d entries before, %d after", before, after)
	}

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

func sentEntries(t *testing.T, w *store.Workspace) []store.SentEntry {
	t.Helper()
	entries, _, err := w.ReadSent("shop", 0)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadSent: %v", err)
	}
	return entries
}
