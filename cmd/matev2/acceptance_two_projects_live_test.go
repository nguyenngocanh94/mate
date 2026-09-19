package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/process"
	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/send"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/ui/console"
)

// The captain's three lines. Each is what a human types into a Mate's pane:
// no sentinel, no crew id, no command. Everything after them is the Mate's
// own reading of the rendered manual.
const (
	// A ship task that leaves exactly one product choice open: the repo
	// holds two checkout pages and the request names neither, so the Crew
	// cannot finish without asking which one the button points at.
	twoProjectsShipShop = `Add a Buy button to the end of README.md in project shop, linking to our checkout page. Use a crew.`
	// A scout task: the deliverable is knowledge, so it ends in a report
	// and never in a merge.
	twoProjectsScoutShop = `Find out which files mention ESP32 and write me a short report.`
	// The same shape as the shop ship task with the choice removed, so
	// nothing in it needs the captain once it is typed.
	twoProjectsShipBlog = `Add the line "Published with matev2" to the end of README.md in project blog. Use a crew.`
	// The line the captain types at the very end, to prove an unmarked
	// prompt ends auto mode.
	twoProjectsEndsAuto = `Thanks - that is all for today.`
)

// TestLiveAcceptanceTwoProjects is the MVP capstone (docs/mvp.md task 24):
// one workspace, two registered repositories, one Mate each, and the whole
// of section 5's two modes driven end to end without a hand-built loop
// anywhere.
//
// `shop` runs in manual mode, the default. The captain types one ship
// request, and every step after it is asserted from the files rather than
// from a pane: the Mate spawns a Crew, the Crew works and then asks, the
// captain hands the question to the Mate with `[assign]`, the Mate answers
// the Crew, the Crew hands back, the Mate reports the branch ready and does
// not land it, and the captain merges from the Console. Then a scout task,
// which ends in a report the Mate summarises and the captain tells it to
// close.
//
// `blog` runs in auto mode with `yolo` on. The captain types one ship
// request that needs no decision and then touches nothing: the daemon
// delivers what is new, the Mate reviews and lands the branch itself, and
// the only `Source: user` line in that project's whole log is the request.
// The last line the captain types is unmarked, which ends auto mode.
//
// The observer and the daemon are the ones cmdConsole starts, over the real
// Herdr adapter, and every Console gesture goes through consoleAction, the
// ActionFunc cmdConsole installs. Spawning is never called directly here:
// spawning is the Mate's job, and a test that did it would be proving its
// own wiring rather than the Mate's.
//
// A failure of this test is a manual bug, not a test bug: the fix belongs in
// assets/mate/AGENTS.md.tmpl or assets/crew/brief.md.tmpl, never in an
// assertion made weaker.
func TestLiveAcceptanceTwoProjects(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	// TMPDIR must not go through a symlink (docs/mvp.md section 7).
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	consoleUseLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	shop := filepath.Join(w.Root(), "shop")
	writeRepo(t, shop, map[string]string{
		"README.md": "# shop\n\nA tiny shop.\n",
		// Two candidate checkout pages and no rule saying which: the open
		// product choice the ship task has to come back and ask about.
		"pages/checkout-classic.html": "<h1>Classic checkout</h1>\n",
		"pages/checkout-express.html": "<h1>Express checkout</h1>\n",
		// Three files for the scout to sort through, two of which mention
		// the part it is asked about.
		"firmware/sensor.md": "The sensor board is an ESP32-WROOM module.\n",
		"docs/hardware.md":   "Bench rig: one ESP32 devkit and one relay board.\n",
		"docs/pricing.md":    "Prices are in VND, inclusive of tax.\n",
	})
	if err := w.AddProject("shop", store.ProjectConfig{Repo: shop, DefaultBranch: "main"}); err != nil {
		t.Fatalf("AddProject shop: %v", err)
	}

	blog := filepath.Join(w.Root(), "blog")
	writeRepo(t, blog, map[string]string{"README.md": "# blog\n\nA tiny blog.\n"})
	if err := w.AddProject("blog", store.ProjectConfig{Repo: blog, DefaultBranch: "main"}); err != nil {
		t.Fatalf("AddProject blog: %v", err)
	}
	// `matev2 project yolo blog on`, through the command itself: the flag
	// is read out of project.yaml when the Mate's manual is rendered, so it
	// has to be on before that Mate starts.
	if err := run([]string{"project", "yolo", "blog", "on", "--workspace", root}, os.Stdout, os.Stderr); err != nil {
		t.Fatalf("project yolo blog on: %v", err)
	}
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open after yolo: %v", err)
	}

	shopBefore := strings.TrimSpace(gitOut(t, shop, "rev-parse", "main"))
	blogBefore := strings.TrimSpace(gitOut(t, blog, "rev-parse", "main"))

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

	// The budget of docs/mvp.md task 24. Every wait below is bounded by it
	// as well as by its own deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	action := consoleAction(w, deps)

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer stopCancel()
		for _, project := range []string{"shop", "blog"} {
			crews, _ := spawn.ListCrews(w, project)
			for _, c := range crews {
				_, _ = spawn.StopCrew(stopCtx, w, deps, project, c.Crew, true)
			}
			_, _ = spawn.StopMate(stopCtx, w, deps, project)
		}
	})

	// The observer and the daemon, started the way cmdConsole starts them
	// (console.go): one per workspace, both living only as long as the
	// console does.
	watcher, err := consoleWatcher(root, deps)
	if err != nil {
		t.Fatalf("consoleWatcher: %v", err)
	}
	watcher.Start(ctx)
	defer watcher.Stop()
	pilot, err := consolePilot(root, deps)
	if err != nil {
		t.Fatalf("consolePilot: %v", err)
	}
	pilot.Start(ctx)
	defer pilot.Stop()

	// ---------- both Mates ----------

	for _, project := range []string{"shop", "blog"} {
		out, startErr := action(ctx, console.ActionRequest{
			Action: console.ActionStart, Target: project, TargetKind: "mate", Harness: query.HarnessClaude})
		if startErr != nil {
			t.Fatalf("start the %s Mate: %v", project, startErr)
		}
		t.Logf("%s: %s", project, out)
	}
	shopMate, shopKind := mateHandleOrFatal(t, ctx, w, deps, "shop")
	blogMate, blogKind := mateHandleOrFatal(t, ctx, w, deps, "blog")
	shopPane := paneReader(rt, shopMate)
	blogPane := paneReader(rt, blogMate)
	t.Cleanup(func() {
		t.Logf("final shop Mate pane:\n%s", shopPane())
		t.Logf("final blog Mate pane:\n%s", blogPane())
		dumpProject(t, w, "shop")
		dumpProject(t, w, "blog")
	})

	// ---------- blog: auto mode, yolo on ----------
	//
	// The blog request is typed first and then left entirely alone, so the
	// project runs beside the shop half on the console's own clock rather
	// than on a serialised script. Auto mode is turned on *after* the
	// request has reached the model: an unmarked prompt ends auto mode
	// (section 5), and the hook that ends it runs when Claude reads the
	// prompt, not when the line cleared the composer. Measured 2026-09-19:
	// setting the flag between those two moments has it deleted a second
	// later by the captain's own request. The hook's `Source: user` line in
	// sent.log is the event to wait for, because the same hook writes it.
	typeCaptainLine(t, ctx, deps, action, "blog", blogMate, blogKind, twoProjectsShipBlog, 4*time.Minute, blogPane)
	waitForSent(t, ctx, w, "blog", 3*time.Minute, func(e store.SentEntry) bool {
		return e.Source == store.SourceUser && e.Text == twoProjectsShipBlog
	})
	modeOut, err := action(ctx, console.ActionRequest{Action: console.ActionMode, Target: "blog", TargetKind: "project"})
	if err != nil {
		t.Fatalf("mode action on blog: %v", err)
	}
	if !w.Auto("blog") {
		t.Fatal("the mode action did not put blog into auto mode")
	}
	t.Logf("blog: %s", modeOut)

	// ---------- shop: manual mode, the ship task ----------

	typeCaptainLine(t, ctx, deps, action, "shop", shopMate, shopKind, twoProjectsShipShop, 4*time.Minute, shopPane)

	ship := waitForCrewRecord(t, ctx, w, "shop", 4*time.Minute, shopPane)
	t.Logf("shop: the Mate spawned crew %q for the ship task", ship)
	shipMeta, err := w.ReadCrewMeta("shop", ship)
	if err != nil {
		t.Fatalf("ReadCrewMeta: %v", err)
	}
	if got := shipMeta[spawn.MetaState]; got != spawn.CrewStateSpawned {
		t.Fatalf("crew %s records state %q at spawn, want %q", ship, got, spawn.CrewStateSpawned)
	}
	shipBranch := shipMeta[spawn.MetaBranch]
	shipWorktree := filepath.Join(w.Root(), shipMeta[spawn.MetaWorktree])
	t.Logf("shop/%s: branch %s, worktree %s, task %q", ship, shipBranch, shipWorktree, shipMeta[spawn.MetaTask])

	shipPane := crewPaneTail(ctx, rt, session, configHome, agentOf(t, w, "shop", ship), ship)
	working := waitForBoxEntry(t, ctx, w, "shop", 4*time.Minute, shipPane, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Crew == ship && e.Verb == "working"
	})
	t.Logf("shop/%s: %s: %s", ship, working.Verb, working.Text)

	// The Crew hits the choice the request left open and stops.
	ask := waitForBoxEntry(t, ctx, w, "shop", 5*time.Minute, shipPane, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Crew == ship && e.Verb == "needs-decision"
	})
	t.Logf("shop/%s asked: %s", ship, ask.Text)
	inbox := waitForInboxItem(t, ctx, w, "shop", time.Minute, shipPane)
	if inbox.Crew != ship || inbox.Verb != "needs-decision" {
		t.Fatalf("the shop inbox holds %+v, want the ship crew's question", inbox)
	}

	// `[assign]`: the captain hands the question to the Mate.
	resolveOut := assignUntilDelivered(t, ctx, action, "shop", inbox, 6*time.Minute, shopPane)
	t.Logf("shop [assign]: %s", resolveOut)
	t.Logf("shop resolve line: %s", inbox.Resolve)
	assertSentLine(t, w, "shop", store.SourceApp, store.TargetMate, inbox.Resolve)

	// The Mate answers the Crew itself: `Source: mate` is written only by a
	// `matev2 send` run from inside the Mate's own pane.
	answered := waitForSent(t, ctx, w, "shop", 5*time.Minute, func(e store.SentEntry) bool {
		return e.Source == store.SourceMate && e.Target == store.CrewTarget(ship)
	})
	t.Logf("shop Mate → crew:%s: %s", ship, answered.Text)

	// The Crew takes it as a new prompt and hands the branch back.
	// The Mate's own answer is the last thing in the log before it does, so
	// the report below has to be a line written after this point: a Mate
	// that named the branch while telling the captain the task had *started*
	// has not reported anything ready.
	beforeHandback := len(sentEntries(t, w, "shop"))
	handback := waitForBoxEntry(t, ctx, w, "shop", 6*time.Minute, shipPane, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Crew == ship && e.Verb == "wait-mate"
	})
	t.Logf("shop/%s handed back: %s", ship, handback.Text)

	// The Mate reports and does not land it: yolo is off for shop.
	report := waitForSentAfter(t, ctx, w, "shop", beforeHandback, 6*time.Minute, func(e store.SentEntry) bool {
		return e.Source == store.SourceMate && e.Target == store.SourceUser &&
			(strings.Contains(e.Text, shipBranch) || strings.Contains(strings.ToLower(e.Text), "ready"))
	})
	t.Logf("shop Mate → captain: %s", report.Text)
	if now := strings.TrimSpace(gitOut(t, shop, "rev-parse", "main")); now != shopBefore {
		t.Fatalf("shop's main moved to %s before the captain merged; the Mate landed a branch with yolo off", now)
	}
	if meta, metaErr := w.ReadCrewMeta("shop", ship); metaErr != nil {
		t.Fatalf("ReadCrewMeta: %v", metaErr)
	} else if state := meta[spawn.MetaState]; state == spawn.CrewStateFinished {
		t.Fatalf("crew %s is already %q; closing a ship crew is the merge's job, after the captain's word", ship, state)
	}

	// The captain merges, from the Console's own Actions menu.
	mergeLine, err := action(ctx, console.ActionRequest{
		Action: console.ActionMerge, Target: "shop", TargetKind: "crew", Crew: ship})
	if err != nil {
		t.Fatalf("console merge action: %v\nmate pane:\n%s", err, shopPane())
	}
	t.Logf("shop merge: %s", mergeLine)
	for _, want := range []string{"shop/" + ship + ": merged", "into main", "crew finished"} {
		if !strings.Contains(mergeLine, want) {
			t.Fatalf("the merge line %q is missing %q", mergeLine, want)
		}
	}
	shopAfterShip := strings.TrimSpace(gitOut(t, shop, "rev-parse", "main"))
	if shopAfterShip == shopBefore {
		t.Fatalf("shop's main is still %s after the merge", shopBefore)
	}
	if meta, metaErr := w.ReadCrewMeta("shop", ship); metaErr != nil {
		t.Fatalf("ReadCrewMeta: %v", metaErr)
	} else if state := meta[spawn.MetaState]; state != spawn.CrewStateFinished {
		t.Fatalf("crew %s records %q after the merge, want finished", ship, state)
	}
	shopReadme := gitOut(t, shop, "show", "main:README.md")
	t.Logf("shop README.md on main after the merge:\n%s", shopReadme)
	if !strings.Contains(strings.ToLower(shopReadme), "checkout") {
		t.Fatalf("shop's README.md on main carries no link to a checkout page:\n%s", shopReadme)
	}
	t.Logf("shop commits landed on main:\n%s", gitOut(t, shop, "log", "--format=%h %s", shopBefore+"..main"))

	// ---------- shop: the scout task ----------

	typeCaptainLine(t, ctx, deps, action, "shop", shopMate, shopKind, twoProjectsScoutShop, 4*time.Minute, shopPane)

	scout := waitForAnotherCrewRecord(t, ctx, w, "shop", ship, 4*time.Minute, shopPane)
	t.Logf("shop: the Mate spawned crew %q for the scout task", scout)
	scoutPane := crewPaneTail(ctx, rt, session, configHome, agentOf(t, w, "shop", scout), scout)
	scoutDone := waitForBoxEntry(t, ctx, w, "shop", 6*time.Minute, scoutPane, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Crew == scout && e.Verb == "wait-mate"
	})
	t.Logf("shop/%s handed back: %s", scout, scoutDone.Text)

	// A scout's deliverable is the report in its own directory, which
	// outlives the Crew. Nothing may have been merged for it.
	reportPath := filepath.Join(w.CrewsDir("shop"), scout, "report.md")
	reportText := readReportOrFatal(t, reportPath, scoutPane)
	t.Logf("shop/%s report.md (%d bytes):\n%s", scout, len(reportText), reportText)
	if !strings.Contains(strings.ToUpper(reportText), "ESP32") {
		t.Fatalf("%s does not name the thing it was asked about:\n%s", reportPath, reportText)
	}

	// The Mate relays the findings to the captain in its own words.
	summary := waitForSent(t, ctx, w, "shop", 5*time.Minute, func(e store.SentEntry) bool {
		return e.Source == store.SourceMate && strings.Contains(strings.ToUpper(e.Text), "ESP32")
	})
	t.Logf("shop Mate → captain: %s", summary.Text)

	// The captain is satisfied and says so; closing is the captain's word.
	typeCaptainLine(t, ctx, deps, action, "shop", shopMate, shopKind, "close it", 4*time.Minute, shopPane)
	waitForCrewState(t, ctx, w, "shop", scout, spawn.CrewStateFinished, 5*time.Minute, shopPane)
	t.Logf("shop/%s is finished", scout)
	if _, statErr := os.Stat(reportPath); statErr != nil {
		t.Fatalf("the report did not survive the crew being closed: %v", statErr)
	}
	if now := strings.TrimSpace(gitOut(t, shop, "rev-parse", "main")); now != shopAfterShip {
		t.Fatalf("shop's main moved to %s during the scout task; a scout never merges", now)
	}

	// ---------- blog: what the daemon and the Mate did unattended ----------

	blogCrew := waitForCrewRecord(t, ctx, w, "blog", 6*time.Minute, blogPane)
	t.Logf("blog: the Mate spawned crew %q", blogCrew)
	waitForCrewState(t, ctx, w, "blog", blogCrew, spawn.CrewStateFinished, 8*time.Minute, blogPane)
	blogAfter := strings.TrimSpace(gitOut(t, blog, "rev-parse", "main"))
	if blogAfter == blogBefore {
		t.Fatalf("blog's main is still %s; nothing was landed under yolo", blogBefore)
	}
	t.Logf("blog commits landed on main:\n%s", gitOut(t, blog, "log", "--format=%h %s", blogBefore+"..main"))
	blogReadme := gitOut(t, blog, "show", "main:README.md")
	t.Logf("blog README.md on main:\n%s", blogReadme)
	if !strings.Contains(blogReadme, "Published with matev2") {
		t.Fatalf("blog's README.md on main does not carry the line the captain asked for:\n%s", blogReadme)
	}

	// The captain typed once and then nothing: every other line in blog's
	// log came from the app, the Mate or the Crew.
	var typed []store.SentEntry
	for _, e := range sentEntries(t, w, "blog") {
		if e.Source == store.SourceUser {
			typed = append(typed, e)
		}
	}
	if len(typed) != 1 || typed[0].Text != twoProjectsShipBlog {
		t.Fatalf("blog's sent.log holds %d user line(s), want exactly the one request:\n%+v", len(typed), typed)
	}
	t.Logf("blog: exactly one captain line in the whole log: %q", typed[0].Text)

	// ---------- an unmarked prompt ends auto mode ----------

	typeCaptainLine(t, ctx, deps, action, "blog", blogMate, blogKind, twoProjectsEndsAuto, 4*time.Minute, blogPane)
	waitForAutoOff(t, ctx, w, "blog", 3*time.Minute)
}

// waitForSentAfter is waitForSent bounded to the lines a project's log
// gained after a known point, so a match cannot be an older line that
// happens to carry the same words.
func waitForSentAfter(t *testing.T, ctx context.Context, w *store.Workspace, project string, after int,
	within time.Duration, match func(store.SentEntry) bool) store.SentEntry {
	t.Helper()
	deadline := time.Now().Add(within)
	var last []store.SentEntry
	for time.Now().Before(deadline) {
		entries := sentEntries(t, w, project)
		last = entries
		if len(entries) > after {
			for _, e := range entries[after:] {
				if match(e) {
					return e
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for a %s sent.log line after entry %d; log:\n%+v", project, after, last)
		case <-time.After(3 * time.Second):
		}
	}
	t.Fatalf("no matching %s sent.log line after entry %d within %s; log:\n%+v", project, after, within, last)
	return store.SentEntry{}
}

// typeCaptainLine types one of the captain's own lines into a Mate's pane
// and keeps at it until it lands, answering each refusal the way the reader
// sitting at that console would.
//
// A Mate mid-turn is waited out. A composer holding unsubmitted text is
// cleared with the Console's own `[clear composer]` action and typed into
// again - which is legitimate here and nowhere else, because in this test
// the captain is the only human at the keyboard and knows they typed
// nothing: the text is the harness's, not theirs. Measured 2026-09-19: a
// Claude Code Mate that has finished a turn sometimes leaves a suggestion
// in its own composer, which internal/send correctly refuses to type over
// and which no amount of waiting clears.
func typeCaptainLine(t *testing.T, ctx context.Context, deps spawn.Deps, action console.ActionFunc,
	project string, handle runtime.AgentHandle, kind harness.Kind, text string,
	within time.Duration, evidence func() string) {
	t.Helper()
	deadline := time.Now().Add(within)
	var last error
	for time.Now().Before(deadline) {
		_, err := send.Send(ctx, send.Deps{Runtime: deps.Runtime}, handle, kind, text, send.Options{})
		if err == nil {
			t.Logf("captain → %s Mate: %s", project, text)
			return
		}
		if !boxSendRefusal(err) {
			t.Fatalf("typing the captain's line into %s: %v\nmate pane:\n%s", project, err, evidence())
		}
		last = err
		t.Logf("the captain's line was refused (%v)", err)
		if errors.Is(err, send.ErrComposerPending) {
			out, clearErr := action(ctx, console.ActionRequest{
				Action: console.ActionClearComposer, Target: project, TargetKind: "mate"})
			if clearErr != nil {
				t.Fatalf("[clear composer] on %s: %v", project, clearErr)
			}
			t.Logf("%s [clear composer]: %s", project, out)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended typing the captain's line into %s: %v\nmate pane:\n%s", project, last, evidence())
		case <-time.After(8 * time.Second):
		}
	}
	t.Fatalf("the captain's line never reached the %s Mate within %s: %v\nmate pane:\n%s",
		project, within, last, evidence())
}

// assignUntilDelivered presses `[assign]` until the line reaches the Mate's
// composer, and returns the Console's own outcome line.
//
// The retry is the captain's, not a weakening of the claim. Measured
// 2026-09-19: a Mate in manual mode supervising a Crew it dispatched is
// inside a tool call for most of every twenty-second cycle (section 9's
// poll loop), so `[assign]` meets `target_blocked: agent is mid-turn` far
// more often than it meets an idle composer. The console reports that
// refusal on its outcome line and the reader presses again; this is that
// reader. A composer holding text is answered with the Console's own
// `[clear composer]`, for the reason typeCaptainLine gives. Every other
// failure is fatal, because a refusal that is not one of those two is not
// something pressing again would fix.
func assignUntilDelivered(t *testing.T, ctx context.Context, action console.ActionFunc, project string,
	item query.BoxEntry, within time.Duration, evidence func() string) string {
	t.Helper()
	deadline := time.Now().Add(within)
	var last error
	for time.Now().Before(deadline) {
		out, err := action(ctx, console.ActionRequest{
			Action: console.ActionResolve, Target: project, TargetKind: "project",
			Crew: item.Crew, Input: item.Resolve})
		if err == nil {
			return out
		}
		if !boxSendRefusal(err) {
			t.Fatalf("[assign] on the %s inbox item: %v\nmate pane:\n%s", project, err, evidence())
		}
		last = err
		t.Logf("[assign] refused (%v); pressing again", err)
		if errors.Is(err, send.ErrComposerPending) {
			clearOut, clearErr := action(ctx, console.ActionRequest{
				Action: console.ActionClearComposer, Target: project, TargetKind: "mate"})
			if clearErr != nil {
				t.Fatalf("[clear composer] on %s: %v", project, clearErr)
			}
			t.Logf("%s [clear composer]: %s", project, clearOut)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended pressing [assign]: %v\nmate pane:\n%s", last, evidence())
		case <-time.After(10 * time.Second):
		}
	}
	t.Fatalf("[assign] never reached the %s Mate within %s: %v\nmate pane:\n%s", project, within, last, evidence())
	return ""
}

// writeRepo creates a git repository with the given files, committed on
// `main`. The files are what makes each acceptance task real: a ship task
// needs something ambiguous to ask about, a scout task needs something to
// find.
func writeRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, dir)
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGitOrFatal(t, dir, "add", "-A")
	runGitOrFatal(t, dir, "commit", "-m", "seed the repository")
}

// mateHandleOrFatal resolves one project's Mate pane.
func mateHandleOrFatal(t *testing.T, ctx context.Context, w *store.Workspace, deps spawn.Deps,
	project string) (runtime.AgentHandle, harness.Kind) {
	t.Helper()
	handle, kind, err := spawn.MateHandle(ctx, w, deps, project)
	if err != nil {
		t.Fatalf("MateHandle %s: %v", project, err)
	}
	return handle, kind
}

// paneReader returns the evidence function a timeout prints: one agent's own
// screen, which is where a Mate doing something the manual did not prepare
// it for is visible.
//
// It takes no context from the caller. The reads that matter most are the
// ones a cleanup makes after the test's own deadline has passed, and a
// reader closed over that deadline answers "context canceled" exactly when
// the screen is wanted.
func paneReader(rt runtime.Adapter, handle runtime.AgentHandle) func() string {
	return func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		screen, err := rt.ReadAgent(ctx, handle, 60)
		if err != nil {
			return "(pane not readable: " + err.Error() + ")"
		}
		return screen
	}
}

// agentOf is the agent name a crew's record carries, for reading its pane.
func agentOf(t *testing.T, w *store.Workspace, project, crew string) string {
	t.Helper()
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		t.Fatalf("ReadCrewMeta %s/%s: %v", project, crew, err)
	}
	return meta[spawn.MetaAgent]
}

// waitForAnotherCrewRecord polls for the first crew record that is not one
// the caller already knows about, which is how a second task's crew is
// found without the test choosing its id.
func waitForAnotherCrewRecord(t *testing.T, ctx context.Context, w *store.Workspace, project string,
	known string, budget time.Duration, evidence func() string) string {
	t.Helper()
	deadline := time.Now().Add(budget)
	for {
		entries, err := os.ReadDir(w.CrewsDir(project))
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read crews dir: %v", err)
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".meta") {
				continue
			}
			if id := strings.TrimSuffix(name, ".meta"); id != known {
				return id
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the %s Mate spawned no second crew within %s\n%s", project, budget, evidence())
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for a second crew record\n%s", evidence())
		case <-time.After(3 * time.Second):
		}
	}
}

// readReportOrFatal reads a scout's report, failing with the crew's own pane
// when it is missing or empty: an empty report is a Crew that reported a
// deliverable it did not write.
func readReportOrFatal(t *testing.T, path string, evidence func() string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the scout's report is not at %s: %v\ncrew pane:\n%s", path, err, evidence())
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		t.Fatalf("the scout's report at %s is empty\ncrew pane:\n%s", path, evidence())
	}
	return string(data)
}

// dumpProject prints one project's whole record: every line typed into a
// pane and every status line every crew wrote. It runs in a cleanup so a
// passing run leaves the evidence docs/mvp.md task 24 asks to keep, and a
// failing one says what had happened by the time it failed.
func dumpProject(t *testing.T, w *store.Workspace, project string) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "==== %s sent.log ====\n", project)
	entries, _, err := w.ReadSent(project, 0)
	if err != nil {
		fmt.Fprintf(&b, "(unreadable: %v)\n", err)
	}
	for _, e := range entries {
		fmt.Fprintf(&b, "%s | %-5s → %-8s | %s\n", e.Time.UTC().Format("15:04:05"), e.Source, e.Target, e.Text)
	}
	crews, err := spawn.ListCrews(w, project)
	if err != nil {
		fmt.Fprintf(&b, "(crew list failed: %v)\n", err)
	}
	for _, c := range crews {
		fmt.Fprintf(&b, "==== %s/%s (%s, state %s) ====\n", project, c.Crew, c.Harness, c.State)
		lines, _, statusErr := w.ReadStatus(project, c.Crew, 0)
		if statusErr != nil {
			fmt.Fprintf(&b, "(status unreadable: %v)\n", statusErr)
			continue
		}
		for _, l := range lines {
			fmt.Fprintf(&b, "%s\n", l.Line)
		}
	}
	t.Log(b.String())
}
