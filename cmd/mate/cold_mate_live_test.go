package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// TestLiveAssignWorksOnAColdMate is the debt docs/mvp.md task 24 pays: the
// captain's very first gesture on a Mate is `[assign]`, and it has to work.
//
// The state this pins is the one a reader is actually in the first time they
// open a workspace. The Mate has been started and has never had a turn, so
// its pane is still Claude Code's welcome box with an empty composer under
// it; the Console's stream has resized that pane to the width stream mode
// gives a Mate, which is the console's width less the rail and its divider;
// and the first thing to happen is a crew asking a question. Before this
// task `internal/send` refused that assign with `state_conflict: agent
// mate-shop is showing a screen mate cannot name`, because at that width
// `--source recent-unwrapped` hands the composer's two rules and the
// composer itself back as one line.
//
// Nothing here is a second implementation of the button: the line is the one
// query.BoxResolveLine built for the inbox item, and it goes through
// consoleAction, which is the ActionFunc cmdConsole installs and the
// `[assign]` button's own path (TestLiveConsoleMouseDrivesTheBox drives the
// pixels).
//
// The proof the line reached the Mate rather than only its pane is the pair
// of sent.log lines docs/mvp.md section 7 names: one written by the action
// after the composer cleared, and one written by the Mate's own
// UserPromptSubmit hook when the model read it.
func TestLiveAssignWorksOnAColdMate(t *testing.T) {
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
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	names := runtime.NewMemoryNameRegistry()
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = names
	rt.StartServer = func(context.Context, string) error {
		t.Fatal("the lab session is provisioned by the runner; this test must not start a Herdr server")
		return nil
	}
	deps := spawn.Deps{
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               consoleBinaryPath(t),
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 60 * time.Second,
		StartTimeout:         60 * time.Second,
	}
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	action := consoleAction(w, deps)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})

	// 1. The Mate, through the Console's own action seam. Nothing is typed
	// into it here or anywhere below: that is the whole point.
	startOut, err := action(ctx, console.ActionRequest{
		Action: console.ActionStart, Target: "shop", TargetKind: "mate", Harness: query.HarnessClaude})
	if err != nil {
		t.Fatalf("console start action: %v", err)
	}
	t.Logf("start action: %s", startOut)

	// 2. A crew that asks and stops, so the inbox has something to assign.
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
	crewTail := func() string {
		screen, readErr := rt.ReadAgent(ctx, crewHandle, send.DefaultLines)
		if readErr != nil {
			return "(pane not readable: " + readErr.Error() + ")"
		}
		return harness.StartupScreenTail(screen, 20)
	}
	ask := waitForInboxItem(t, ctx, w, "shop", 180*time.Second, crewTail)
	t.Logf("inbox item: %s %s %s", ask.Crew, ask.Verb, ask.Text)
	if ask.Verb != "needs-decision" {
		t.Fatalf("inbox item = %+v, want the needs-decision question", ask)
	}

	// 3. The Console's stream, at the geometry stream mode gives a Mate
	// inside a 120x36 console. The width is asserted rather than assumed:
	// opening the PTY at the console's own 120 columns would measure a pane
	// no reader ever has, and that is exactly how this debt stayed hidden.
	size := console.StreamSize(console.SessionTargetMate, 120, 36)
	t.Logf("a 120x36 console gives the Mate a %dx%d pane", size.Cols, size.Rows)
	if size.Cols >= 120 {
		t.Fatalf("stream size = %+v; a Mate's pane is narrower than the console it sits in", size)
	}
	snap, err := query.Load(ctx, w)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	target := console.SessionTarget{
		Kind:        console.SessionTargetMate,
		ID:          snap.Projects[0].Mate.Designated.Value.MateID,
		ProjectID:   "shop",
		HarnessKind: query.HarnessClaude,
		AgentName:   snap.Projects[0].Mate.AgentName.Value,
		Mode:        snap.Projects[0].Mode,
	}
	channel, err := consoleSessionStream(w, rt)(ctx, target, size)
	if err != nil {
		t.Fatalf("open the Mate's session stream: %v", err)
	}
	streamClosed := false
	defer func() {
		if !streamClosed {
			_ = channel.Close(context.Background())
		}
	}()
	// Read until the composer glyph arrives, the way task 09's proof does:
	// it is what says the PTY is up and the agent has redrawn itself at the
	// new size.
	waitForStreamComposer(t, ctx, channel, 90*time.Second)

	// 4. The Mate is still cold: no line has been typed into it, so its
	// hooks have written nothing at all.
	if before := sentEntries(t, w, "shop"); len(before) != 0 {
		t.Fatalf("sent.log already holds %d line(s); this Mate has had a turn and is not the cold one the debt is about:\n%+v",
			len(before), before)
	}
	mateHandle, kind, err := spawn.MateHandle(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("MateHandle: %v", err)
	}
	screen, err := rt.ReadAgent(ctx, mateHandle, send.DefaultLines)
	if err != nil {
		t.Fatalf("ReadAgent on the Mate: %v", err)
	}
	t.Logf("the cold Mate's pane:\n%s", screen)
	cold, err := send.ClassifyComposer(kind, screen)
	if err != nil {
		t.Fatalf("ClassifyComposer: %v", err)
	}
	t.Logf("cold Mate composer: state=%s evidence=%q", cold.State, cold.Evidence)
	if cold.State != send.StateEmpty {
		t.Fatalf("the cold Mate's composer classifies %q (evidence %q); the splash under it is still an empty composer",
			cold.State, cold.Evidence)
	}

	// 5. The very first action on this Mate. It must not be refused.
	resolveOut, err := action(ctx, console.ActionRequest{
		Action: console.ActionResolve, Target: "shop", TargetKind: "project",
		Crew: ask.Crew, Input: ask.Resolve})
	if err != nil {
		t.Fatalf("[assign] on a cold Mate: %v\nmate pane:\n%s", err, screen)
	}
	t.Logf("resolve action: %s", resolveOut)
	t.Logf("resolve line: %s", ask.Resolve)

	// 6. The line reached the pane (the action's own record) and the model
	// (the Mate's UserPromptSubmit hook's record): two copies, section 7.
	assertSentLine(t, w, "shop", store.SourceApp, store.TargetMate, ask.Resolve)
	mateTail := func() string {
		s, readErr := rt.ReadAgent(ctx, mateHandle, send.DefaultLines)
		if readErr != nil {
			return "(mate pane not readable: " + readErr.Error() + ")"
		}
		return s
	}
	waitForSentCount(t, ctx, w, "shop", 180*time.Second, ask.Resolve, 2, mateTail)
	for _, e := range sentEntries(t, w, "shop") {
		t.Logf("sent.log %s → %s: %s", e.Source, e.Target, e.Text)
	}

	if err := channel.Close(ctx); err != nil {
		t.Fatalf("close the session stream: %v", err)
	}
	streamClosed = true

	if _, err := spawn.StopCrew(ctx, w, deps, "shop", "k3", true); err != nil {
		t.Fatalf("StopCrew: %v", err)
	}
	stopOut, err := action(ctx, console.ActionRequest{Action: console.ActionStop, Target: "shop", TargetKind: "mate"})
	if err != nil {
		t.Fatalf("console stop action: %v", err)
	}
	t.Logf("stop action: %s", stopOut)
}

// waitForStreamComposer reads an open session stream until Claude's composer
// glyph arrives, which is what says the PTY is up and the agent has redrawn
// itself at the size the stream asked for.
func waitForStreamComposer(t *testing.T, ctx context.Context, channel console.SessionChannel, within time.Duration) {
	t.Helper()
	var seen strings.Builder
	deadline := time.Now().Add(within)
	for !strings.Contains(seen.String(), harness.ClaudeComposerMarker) {
		if time.Now().After(deadline) {
			t.Fatalf("Claude's composer glyph never arrived in the stream within %s; last bytes:\n%s",
				within, harness.StartupScreenTail(seen.String(), 12))
		}
		readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
		data, readErr := channel.Read(readCtx)
		readCancel()
		seen.Write(data)
		if readErr != nil && !os.IsTimeout(readErr) && time.Now().After(deadline) {
			t.Fatalf("stream read: %v", readErr)
		}
	}
}
