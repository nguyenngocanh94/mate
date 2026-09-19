package main

import (
	"context"
	"os"
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

// TestLiveConsoleDiffShowsACrewBranch is the mvp.md task 21 proof, and the
// only claim a unit test cannot make: that the branch a real crew committed
// on, in a real linked worktree, is what the Console's `diff` action shows.
//
//  1. a real Codex crew, spawned into `.worktrees/shop-k3` on `matev2/k3`
//  2. its brief tells it to commit one line to README.md and then report
//     `wait-mate: ready in branch <branch>`
//  3. the crew's own status line proves the commit was its idea of done
//  4. ActionDiff goes through consoleAction - the same ActionFunc the
//     Actions menu's `diff` entry calls - and the text that comes back names
//     the crew's commit subject and the file it touched
//  5. the same text is what `matev2 diff shop k3` prints, because both go
//     through crewDiffText; the overlay in internal/ui/console renders it and
//     nothing else
//
// The Bubble Tea Program is not run (it needs a terminal), but no part of
// the path being proved lives inside it.
func TestLiveConsoleDiffShowsACrewBranch(t *testing.T) {
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
	// A tracked file for the crew to change. A diff of a repository with no
	// files would pass for the wrong reason.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, repo, "add", "README.md")
	runGitOrFatal(t, repo, "commit", "-m", "add README")
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
	deps := spawn.Deps{
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               consoleBinaryPath(t),
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 60 * time.Second,
		StartTimeout:         60 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	action := consoleAction(w, deps)

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
	})

	// 1 & 2. The crew, with a brief whose whole job is to leave a commit
	// behind. "one line" and "one commit" are spelled out because a crew
	// that rewrites the file wholesale would still pass an assertion on the
	// filename alone.
	crewRes, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop",
		Crew:    "k3",
		Harness: harness.KindCodex,
		BriefText: "Append the single line `reviewed by the crew` to README.md in this worktree, " +
			"then make exactly one commit with the subject `note the review in README`. " +
			"Do not change any other file. When the commit exists, append " +
			"wait-mate: ready in branch <the branch name> to the status file and stop.",
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	t.Logf("spawned crew %s in pane %s (branch %s, worktree %s)",
		crewRes.Agent, crewRes.Pane, crewRes.Branch, crewRes.Worktree)
	if crewRes.DeliveryWarning != "" {
		t.Logf("brief delivery warning: %s\n%s", crewRes.DeliveryWarning, crewRes.PaneTail)
	}

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

	// 3. The crew's own word that it is done. It is the signal a reader
	// would act on before opening the diff, so the test waits on it rather
	// than polling git behind the crew's back.
	done := waitForBoxEntry(t, ctx, w, 420*time.Second, paneTail, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Verb == "wait-mate"
	})
	t.Logf("crew reported: %s: %s", done.Verb, done.Text)

	// 4. The Console's own action, with the request the Actions menu builds
	// for a Crew row: the Project in Target, the Crew in Crew.
	text, err := action(ctx, console.ActionRequest{
		Action: console.ActionDiff, Target: "shop", TargetKind: "crew", Crew: "k3"})
	if err != nil {
		t.Fatalf("console diff action: %v\ncrew pane:\n%s", err, paneTail())
	}
	t.Logf("diff action returned %d bytes:\n%s", len(text), text)

	if !strings.Contains(text, "note the review in README") {
		t.Fatalf("the diff does not name the crew's commit:\n%s\ncrew pane:\n%s", text, paneTail())
	}
	if !strings.Contains(text, "README.md") {
		t.Fatalf("the diff does not name the file the crew changed:\n%s", text)
	}
	if !strings.Contains(text, "+reviewed by the crew") {
		t.Fatalf("the diff does not carry the line the crew added:\n%s", text)
	}
	if strings.Contains(text, "no commits on") {
		t.Fatalf("the diff reports no commits although the crew made one:\n%s", text)
	}

	// 5. The overlay is the only thing the Console does with that text: it
	// renders every line it was given, so what the reader reads is what the
	// command printed.
	frame := console.RenderDiffOverlay(text, "k3", crewRes.Branch, 120, 36)
	t.Logf("overlay:\n%s", strings.Join(frame, "\n"))
	joined := strings.Join(frame, "\n")
	for _, want := range []string{"diff", "k3", crewRes.Branch, "README.md"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the overlay does not show %q:\n%s", want, joined)
		}
	}

	// --stat is the same answer in git's own summary form, and the command
	// path is the one a Mate would run.
	stat, err := crewDiffText(ctx, w, deps.Git, "shop", "k3", true)
	if err != nil {
		t.Fatalf("crewDiffText --stat: %v", err)
	}
	t.Logf("--stat:\n%s", stat)
	if !strings.Contains(stat, "README.md") || !strings.Contains(stat, "1 file changed") {
		t.Fatalf("--stat is not git's summary of the crew's change:\n%s", stat)
	}
	if strings.Contains(stat, "diff --git") {
		t.Fatalf("--stat printed the patch:\n%s", stat)
	}
}
