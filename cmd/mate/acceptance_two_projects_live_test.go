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

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
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
	twoProjectsShipBlog = `Add the line "Published with mate" to the end of README.md in project blog. Use a crew.`
	// The captain's answer to the shop Crew's question, once the Mate has
	// escalated it: the checkout page is theirs to choose.
	twoProjectsShopChoice = `Use the express checkout page, pages/checkout-express.html.`
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
// captain hands the question to the Mate with `[assign]`, the Mate escalates
// it (the checkout page is the captain's choice), the captain answers in the
// Mate's pane, the Mate relays it to the Crew, the Crew hands back, the Mate reports the branch ready and does
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
	root := liveWorkspaceRoot(t)
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
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: shop, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject shop: %v", err)
	}

	blog := filepath.Join(w.Root(), "blog")
	writeRepo(t, blog, map[string]string{"README.md": "# blog\n\nA tiny blog.\n"})
	if err := w.AddProject("blog", store.ProjectConfig{Repos: []store.RepoConfig{{Path: blog, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject blog: %v", err)
	}
	// `mate project yolo blog on`, through the command itself: the flag
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
	// console does. The observer records the timeline as cmdConsole's does,
	// because the blog half's claim that the Mate yielded its turn to the
	// digest (task 31) is read out of the Mate's turn rows.
	watcher, timelineDB, err := consoleWatcherWithTimeline(root, deps)
	if err != nil {
		t.Fatalf("consoleWatcherWithTimeline: %v", err)
	}
	if timelineDB == nil {
		t.Fatal("the observer opened no timeline database")
	}
	defer timelineDB.Close()
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
		// What the daemon last saw of the blog Mate's composer, and why it
		// last did not deliver: a digest that never arrives is otherwise
		// indistinguishable from one that was never due.
		logComposerReading(t, rt, blogMate, blogKind, "blog")
		for project, status := range pilot.Snapshot() {
			t.Logf("daemon %s: auto=%v sends=%d last=%s notice=%q at %s", project, w.Auto(project),
				status.Sends, status.LastSentAt.UTC().Format("15:04:05"), status.Notice, status.NoticeAt.UTC().Format("15:04:05"))
		}
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

	// `[assign]`: the captain hands the question to the Mate, once. A busy
	// Mate no longer refuses it: the console queues it in `mate/.outbox`
	// and the sender loop consolePilot started delivers it when the
	// composer clears (task 30).
	resolveOut := assignAndAwaitDelivery(t, ctx, w, action, "shop", inbox, 6*time.Minute, shopPane)
	t.Logf("shop [assign]: %s", resolveOut)
	t.Logf("shop resolve line: %s", inbox.Resolve)
	afterAssign := len(sentEntries(t, w, "shop"))

	// `[assign]` hands the question to the Mate to handle, not to decide
	// (decided 2026-09-24; the decision-authority skill): which checkout
	// page "our checkout page" means is the captain's product choice, so
	// the Mate escalates it in its own pane, naming both options, and sends
	// the Crew no choice of its own. The Stop hook writes the Mate's final
	// words of that turn as `Source: mate` → `user`.
	escalation := waitForSentAfter(t, ctx, w, "shop", afterAssign, 5*time.Minute, func(e store.SentEntry) bool {
		text := strings.ToLower(e.Text)
		return e.Source == store.SourceMate && e.Target == store.SourceUser &&
			strings.Contains(text, "classic") && strings.Contains(text, "express")
	})
	t.Logf("shop Mate → captain (escalation): %s", escalation.Text)
	assertNoChoiceSentToCrew(t, w, "shop", ship, afterAssign)

	// The captain answers in the Mate's pane, unmarked, as a person does;
	// shop is in manual mode, so there is no `.auto` for the hook to clear.
	beforeAnswer := len(sentEntries(t, w, "shop"))
	typeCaptainLine(t, ctx, deps, action, "shop", shopMate, shopKind, twoProjectsShopChoice, 4*time.Minute, shopPane)

	// The Mate relays it: `Source: mate` is written only by a `mate send`
	// run from inside the Mate's own pane.
	answered := waitForSentAfter(t, ctx, w, "shop", beforeAnswer, 5*time.Minute, func(e store.SentEntry) bool {
		return e.Source == store.SourceMate && e.Target == store.CrewTarget(ship)
	})
	t.Logf("shop Mate → crew:%s: %s", ship, answered.Text)
	// The relay is either the choice itself or, the M7 way, a pointer to
	// the captain's words appended to the Crew's brief (`mate brief
	// append`); either way the choice has to reach something the Crew reads.
	briefPath := filepath.Join(w.CrewsDir("shop"), ship, "brief.md")
	briefText, _ := os.ReadFile(briefPath)
	if !strings.Contains(strings.ToLower(answered.Text), "express") && !strings.Contains(string(briefText), "checkout-express") {
		t.Fatalf("the Mate relayed %q to the Crew, and neither it nor %s carries the captain's choice (express)",
			answered.Text, briefPath)
	}

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
	if !strings.Contains(shopReadme, "checkout-express.html") {
		t.Fatalf("shop's README.md on main does not link the page the captain chose:\n%s", shopReadme)
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
	if !strings.Contains(blogReadme, "Published with mate") {
		t.Fatalf("blog's README.md on main does not carry the line the captain asked for:\n%s", blogReadme)
	}

	// The Mate yielded its turn after spawning, and the daemon's digest is
	// what woke it to land the branch (task 31).
	assertTheMateYieldedToTheDigest(t, ctx, w, timelineDB, "blog", blogCrew, 3*time.Minute, blogPane)

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

// assertNoChoiceSentToCrew fails when the Mate sent the Crew a line naming
// either checkout page after the question was assigned: that choice was the
// captain's, and a Mate that guessed it into the Crew's pane decided it.
func assertNoChoiceSentToCrew(t *testing.T, w *store.Workspace, project, crew string, after int) {
	t.Helper()
	entries := sentEntries(t, w, project)
	for _, e := range entries[after:] {
		text := strings.ToLower(e.Text)
		if e.Source == store.SourceMate && e.Target == store.CrewTarget(crew) &&
			(strings.Contains(text, "classic") || strings.Contains(text, "express")) {
			t.Fatalf("the Mate sent crew %s a checkout choice before the captain made it: %q", crew, e.Text)
		}
	}
}

// mateHarnessTurn is one prompt the Mate's harness took and everything it
// did with it. The timeline's `turn` rows are one model call each
// (docs/mvp.md section 7), so a harness turn is the run of rows sharing one
// `harness_turn_ref` (Claude's promptId).
type mateHarnessTurn struct {
	ref         string
	started     string
	ended       string
	rows        int
	triggerFrom string
	trigger     string
	actions     []string
}

func (h mateHarnessTurn) ran(fragments ...string) bool {
	for _, a := range h.actions {
		if containsEvery(a, fragments) {
			return true
		}
	}
	return false
}

func containsEvery(s string, fragments []string) bool {
	for _, f := range fragments {
		if !strings.Contains(s, f) {
			return false
		}
	}
	return true
}

// mateHarnessTurns reads one Mate's turns out of the timeline, grouped into
// harness turns, each with the line that triggered it and the commands it ran.
func mateHarnessTurns(t *testing.T, handle *db.DB, project string) []mateHarnessTurn {
	t.Helper()
	rows, err := handle.SQL().Query(`
		SELECT t.id, t.harness_turn_ref, COALESCE(t.started_at,''), COALESCE(t.ended_at,''),
		       COALESCE(m.from_actor_id,''), COALESCE(m.text,'')
		  FROM turn t LEFT JOIN message m ON m.event_id = t.trigger_event_id
		 WHERE t.actor_id = ?
		 ORDER BY t.started_at, t.ordinal`, timeline.MateActorID(project))
	if err != nil {
		t.Fatalf("read the %s Mate's turns: %v", project, err)
	}
	type row struct{ id, ref, started, ended, from, text string }
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.ref, &r.started, &r.ended, &r.from, &r.text); err != nil {
			rows.Close()
			t.Fatalf("scan a turn: %v", err)
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("read the %s Mate's turns: %v", project, err)
	}

	var out []mateHarnessTurn
	index := map[string]int{}
	for _, r := range all {
		key := r.ref
		if key == "" {
			// A row the harness named no prompt for: keep it on its own
			// rather than merging it into a neighbour and inventing a turn.
			key = "row:" + r.id
		}
		i, ok := index[key]
		if !ok {
			i = len(out)
			index[key] = i
			out = append(out, mateHarnessTurn{ref: key, started: r.started, triggerFrom: r.from, trigger: r.text})
		}
		h := &out[i]
		h.rows++
		if r.ended > h.ended {
			h.ended = r.ended
		}
		acts, err := handle.SQL().Query(`SELECT summary FROM action WHERE turn_id = ? ORDER BY at`, r.id)
		if err != nil {
			t.Fatalf("read the actions of turn %s: %v", r.id, err)
		}
		for acts.Next() {
			var summary string
			if err := acts.Scan(&summary); err != nil {
				acts.Close()
				t.Fatalf("scan an action: %v", err)
			}
			h.actions = append(h.actions, summary)
		}
		acts.Close()
	}
	return out
}

// assertTheMateYieldedToTheDigest is task 31's proof on the auto half: the
// daemon delivered at least one `digest:` line, the Mate spawned the Crew in
// the turn the captain's request started, and the merge that closed the
// Crew ran in a later turn that a digest started. Nothing else could have
// started that turn - the captain typed once - so it also proves the Mate
// ended the spawning turn rather than supervising inside it, which is the
// only way a verified send into its composer can succeed.
func assertTheMateYieldedToTheDigest(t *testing.T, ctx context.Context, w *store.Workspace, handle *db.DB,
	project, crew string, within time.Duration, evidence func() string) {
	t.Helper()

	var digests int
	for _, e := range sentEntries(t, w, project) {
		if e.Source == store.SourceApp && e.Target == store.TargetMate && strings.HasPrefix(e.Text, "digest: ") {
			digests++
			t.Logf("%s sent.log %s app → mate: %s", project, e.Time.UTC().Format("15:04:05"), e.Text)
		}
	}
	if digests == 0 {
		t.Fatalf("%s's sent.log holds no app → mate digest: line; the Mate never yielded its turn to the daemon\nmate pane:\n%s",
			project, evidence())
	}

	// The observer ingests at the end of each poll, so the rows for the
	// Mate's last turn may land a round or two after the merge did.
	spawnFragments := []string{"crew spawn " + project, crew}
	mergeFragments := []string{"merge " + project + " " + crew}
	deadline := time.Now().Add(within)
	var turns []mateHarnessTurn
	var spawnAt, mergeAt int
	for {
		turns = mateHarnessTurns(t, handle, project)
		spawnAt, mergeAt = -1, -1
		for i, h := range turns {
			if spawnAt < 0 && h.ran(spawnFragments...) {
				spawnAt = i
			}
			if h.ran(mergeFragments...) {
				mergeAt = i
			}
		}
		if spawnAt >= 0 && mergeAt >= 0 {
			break
		}
		if time.Now().After(deadline) {
			logMateHarnessTurns(t, project, turns)
			t.Fatalf("the %s timeline shows no Mate turn that ran %q (found %d) and %q (found %d) within %s",
				project, strings.Join(spawnFragments, " … "), spawnAt, mergeFragments[0], mergeAt, within)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for the %s Mate's turns in the timeline", project)
		case <-time.After(5 * time.Second):
		}
	}
	logMateHarnessTurns(t, project, turns)

	spawnTurn, mergeTurn := turns[spawnAt], turns[mergeAt]
	if spawnTurn.triggerFrom != timeline.UserActorID(project) {
		t.Fatalf("the %s Mate spawned in a turn triggered by %q (%q), want the captain's request",
			project, spawnTurn.triggerFrom, spawnTurn.trigger)
	}
	if mergeAt == spawnAt {
		t.Fatalf("the %s Mate spawned and merged inside one turn (%s); it supervised instead of yielding to the digest",
			project, spawnTurn.ref)
	}
	if mergeTurn.triggerFrom != timeline.AppActorID(project) || !strings.HasPrefix(mergeTurn.trigger, "digest: ") {
		t.Fatalf("the %s Mate merged in turn %s, triggered by %q (%q), want a digest",
			project, mergeTurn.ref, mergeTurn.triggerFrom, mergeTurn.trigger)
	}
	if spawnTurn.ended == "" || mergeTurn.started < spawnTurn.ended {
		t.Fatalf("the %s Mate's digest turn %s started at %s, before the spawning turn %s ended (%q)",
			project, mergeTurn.ref, mergeTurn.started, spawnTurn.ref, spawnTurn.ended)
	}
	t.Logf("%s: spawned in turn %s (%s .. %s, the captain's request); merged in turn %s (%s .. %s, %q)",
		project, spawnTurn.ref, spawnTurn.started, spawnTurn.ended,
		mergeTurn.ref, mergeTurn.started, mergeTurn.ended, mergeTurn.trigger)
}

// logMateHarnessTurns prints a Mate's turn boundaries, the evidence task 31
// records: when each turn started and ended, what started it, and the
// mate commands it ran.
func logMateHarnessTurns(t *testing.T, project string, turns []mateHarnessTurn) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "==== %s Mate turns ====\n", project)
	for i, h := range turns {
		fmt.Fprintf(&b, "#%d %s  %s .. %s  %d model call(s)  trigger %s: %s\n",
			i, h.ref, h.started, h.ended, h.rows, h.triggerFrom, clip(h.trigger, 160))
		for _, a := range h.actions {
			if strings.Contains(a, "mate") {
				fmt.Fprintf(&b, "    %s\n", clip(a, 200))
			}
		}
	}
	t.Log(b.String())
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
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

// assignAndAwaitDelivery presses `[assign]` once and waits for the line to
// reach the Mate. Since task 30 a busy Mate does not refuse it: the console
// answers "queued for the Mate" and its outbox sender delivers the line when
// the composer clears, so the proof of delivery is the `app → mate` line in
// `sent.log`, not the action's own answer.
func assignAndAwaitDelivery(t *testing.T, ctx context.Context, w *store.Workspace, action console.ActionFunc,
	project string, item query.BoxEntry, within time.Duration, evidence func() string) string {
	t.Helper()
	out, err := action(ctx, console.ActionRequest{
		Action: console.ActionResolve, Target: project, TargetKind: "project",
		Crew: item.Crew, Input: item.Resolve, Key: item.AssignKey})
	if err != nil {
		t.Fatalf("[assign] on the %s inbox item: %v\nmate pane:\n%s", project, err, evidence())
	}
	waitForSent(t, ctx, w, project, within, func(e store.SentEntry) bool {
		return e.Source == store.SourceApp && e.Target == store.TargetMate && e.Text == item.Resolve
	})
	return out
}

// logComposerReading classifies a Mate's composer from the styled screen,
// the reading internal/send takes before every verified send.
func logComposerReading(t *testing.T, rt runtime.Adapter, handle runtime.AgentHandle, kind harness.Kind, project string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	screen, err := rt.ReadAgentStyled(ctx, handle, send.DefaultLines)
	if err != nil {
		t.Logf("%s Mate composer: not readable: %v", project, err)
		return
	}
	cls, err := send.ClassifyComposer(kind, screen)
	t.Logf("%s Mate composer: state=%s evidence=%q err=%v\nstyled tail:\n%q", project, cls.State, cls.Evidence, err,
		lastLines(screen, 8))
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
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
	if incidents, err := os.ReadFile(w.IncidentsLog(project)); err == nil {
		fmt.Fprintf(&b, "==== %s incidents.log ====\n%s", project, incidents)
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
