package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/timeline/scene"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// TestLiveTimelineExplainsTheAcceptance is task 25's proof: the M4 acceptance
// flow run again with the timeline recording, and then every claim of M5
// checked against the database rather than against the code that filled it.
//
// It runs the `shop` manual half of TestLiveAcceptanceTwoProjects - the
// captain's ship request, the Mate spawning a Crew, the Crew asking, the
// captain handing the question over with `[assign]`, the Mate answering, the
// Crew handing back, and the captain merging from the Console. That half is
// the one that exercises every source the ingest reads (two transcripts, the
// status file, `sent.log`, the `.meta` files and git), and it fits inside a
// budget that leaves room for the assertions; the `blog` auto half adds a
// second Mate and a digest but no source the timeline does not already see,
// and running both would put this test over fifteen minutes.
//
// The observer and the database handle are the ones cmdConsole builds, so
// what is being proved is the shipped wiring and not a second one.
func TestLiveTimelineExplainsTheAcceptance(t *testing.T) {
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

	shop := filepath.Join(w.Root(), "shop")
	writeRepo(t, shop, map[string]string{
		"README.md":                   "# shop\n\nA tiny shop.\n",
		"pages/checkout-classic.html": "<h1>Classic checkout</h1>\n",
		"pages/checkout-express.html": "<h1>Express checkout</h1>\n",
	})
	if err := w.AddProject("shop", store.ProjectConfig{Repo: shop, DefaultBranch: "main"}); err != nil {
		t.Fatalf("AddProject shop: %v", err)
	}
	shopBefore := strings.TrimSpace(gitOut(t, shop, "rev-parse", "main"))

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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	action := consoleAction(w, deps)

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer stopCancel()
		crews, _ := spawn.ListCrews(w, "shop")
		for _, c := range crews {
			_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", c.Crew, true)
		}
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})

	// The observer that also records the timeline: cmdConsole's own wiring.
	watcher, timelineDB, err := consoleWatcherWithTimeline(root, deps)
	if err != nil {
		t.Fatalf("consoleWatcherWithTimeline: %v", err)
	}
	if timelineDB == nil {
		t.Fatal("the observer opened no timeline database")
	}
	watcher.Start(ctx)
	defer watcher.Stop()
	pilot, err := consolePilot(root, deps)
	if err != nil {
		t.Fatalf("consolePilot: %v", err)
	}
	pilot.Start(ctx)
	defer pilot.Stop()

	out, err := action(ctx, console.ActionRequest{
		Action: console.ActionStart, Target: "shop", TargetKind: "mate", Harness: query.HarnessClaude})
	if err != nil {
		t.Fatalf("start the shop Mate: %v", err)
	}
	t.Logf("shop: %s", out)
	shopMate, shopKind := mateHandleOrFatal(t, ctx, w, deps, "shop")
	shopPane := paneReader(rt, shopMate)
	t.Cleanup(func() {
		t.Logf("final shop Mate pane:\n%s", shopPane())
		dumpProject(t, w, "shop")
	})

	typeCaptainLine(t, ctx, deps, action, "shop", shopMate, shopKind, twoProjectsShipShop, 4*time.Minute, shopPane)

	ship := waitForCrewRecord(t, ctx, w, "shop", 4*time.Minute, shopPane)
	t.Logf("shop: the Mate spawned crew %q", ship)
	shipMeta, err := w.ReadCrewMeta("shop", ship)
	if err != nil {
		t.Fatalf("ReadCrewMeta: %v", err)
	}
	shipBranch := shipMeta[spawn.MetaBranch]
	shipPane := crewPaneTail(ctx, rt, session, configHome, agentOf(t, w, "shop", ship), ship)

	ask := waitForBoxEntry(t, ctx, w, "shop", 6*time.Minute, shipPane, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Crew == ship && e.Verb == "needs-decision"
	})
	t.Logf("shop/%s asked: %s", ship, ask.Text)
	inbox := waitForInboxItem(t, ctx, w, "shop", time.Minute, shipPane)
	resolveOut := assignAndAwaitDelivery(t, ctx, w, action, "shop", inbox, 6*time.Minute, shopPane)
	t.Logf("shop [assign]: %s", resolveOut)

	answered := waitForSent(t, ctx, w, "shop", 5*time.Minute, func(e store.SentEntry) bool {
		return e.Source == store.SourceMate && e.Target == store.CrewTarget(ship)
	})
	t.Logf("shop Mate -> crew:%s: %s", ship, answered.Text)

	handback := waitForBoxEntry(t, ctx, w, "shop", 6*time.Minute, shipPane, func(e query.BoxEntry) bool {
		return e.Kind == query.BoxStatus && e.Crew == ship && e.Verb == "wait-mate"
	})
	t.Logf("shop/%s handed back: %s", ship, handback.Text)

	mergeLine, err := action(ctx, console.ActionRequest{
		Action: console.ActionMerge, Target: "shop", TargetKind: "crew", Crew: ship})
	if err != nil {
		t.Fatalf("console merge action: %v\nmate pane:\n%s", err, shopPane())
	}
	t.Logf("shop merge: %s", mergeLine)
	if now := strings.TrimSpace(gitOut(t, shop, "rev-parse", "main")); now == shopBefore {
		t.Fatalf("shop's main is still %s after the merge", shopBefore)
	}

	// One more poll so the observer records the merge and the closed crew,
	// and so the transcripts' last complete groups land.
	waitForTimelineEvent(t, ctx, timelineDB, "shop", timeline.KindMergeDone, 2*time.Minute)

	// ------------------------------------------------------------------
	// What the timeline has to be able to say about the run.
	// ------------------------------------------------------------------

	crewActor := timeline.CrewActorID("shop", ship)
	mateActor := timeline.MateActorID("shop")

	// The observer and the daemon stop first: nothing may type into a pane
	// once the assertions start, so the sources stop moving. The Mate itself
	// may still be finishing the turn it was in, which is why the tool-call
	// check re-ingests until the parser and the database agree - a
	// transcript's trailing message group is withheld until the harness
	// writes past it (internal/harness/transcript.go), so "every tool call
	// has an action" is a claim about a settled file, not a live one.
	pilot.Stop()
	watcher.Stop()
	ingestWorkspace, err := store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	ingest := timeline.New(ingestWorkspace, timelineDB, timeline.Deps{
		SessionRef: consoleSessionRef(ingestWorkspace, deps),
	})
	settle := func() error { return ingest.Ingest(ctx) }

	assertEveryToolCallHasAnAction(t, timelineDB, mateActor, settle)
	assertEveryToolCallHasAnAction(t, timelineDB, crewActor, settle)
	assertEveryStatusLineHasAnEvent(t, w, timelineDB, "shop", ship, crewActor)
	assertEveryQuestionWasAnswered(t, timelineDB, crewActor)
	assertTheMergeHasACause(t, timelineDB, "shop", ship, shipBranch)
	for _, actor := range []string{mateActor, crewActor} {
		assertNoUnexplainedBusyWindow(t, timelineDB, actor, 10*time.Second)
	}

	// The story, in the run's own words. This is what goes into
	// docs/evidence/m5-timeline-2026-09-20.md.
	events, err := timeline.Story(ctx, timelineDB.SQL(), timeline.StoryQuery{Project: "shop"})
	if err != nil {
		t.Fatalf("Story: %v", err)
	}
	var narrated strings.Builder
	for _, e := range events {
		narrated.WriteString(timeline.Narrate(e))
		narrated.WriteString("\n")
	}
	t.Logf("shop, narrated (%d events):\n%s", len(events), narrated.String())

	// Task 26: the office scene the same events project onto. The claim is
	// that a real run falls entirely inside the state machine - every event
	// either moves somebody or is a row of the table that says it moves
	// nobody - and that the scene reads as the story of the run.
	assertTheSceneExplainsTheWholeRun(t, ctx, timelineDB, "shop", crewActor)
}

// assertTheSceneExplainsTheWholeRun is the live half of task 26's depth test:
// no `unexplained` transition on a real run, the states a ship task has to
// pass through all reached, and every wait at the CEO's door a measurable
// stretch that ends with the crew back at its desk.
//
// The narrated scene it prints is what goes into
// docs/evidence/m5-scene-2026-09-20.md.
func assertTheSceneExplainsTheWholeRun(t *testing.T, ctx context.Context, handle *db.DB,
	project, crewActor string) {
	t.Helper()

	rows, err := scene.Transitions(ctx, handle.SQL(), scene.TransitionQuery{Project: project})
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the scene projection wrote no transition at all")
	}

	var unexplained []string
	reached := map[scene.State]bool{}
	for _, r := range rows {
		if r.Unexplained() {
			unexplained = append(unexplained, r.At.Format(time.RFC3339)+" "+r.ActorName+" "+r.Detail)
			continue
		}
		reached[r.To] = true
	}
	if len(unexplained) > 0 {
		t.Fatalf("%d event(s) of the run no edge of the scene explains:\n%s",
			len(unexplained), strings.Join(unexplained, "\n"))
	}

	// What a ship task cannot happen without. `reviewing` and `merging` are
	// not here: the captain reviews the diff in the Console and merges from
	// it, and neither gesture writes a file (docs/mvp.md section 7), so the
	// Mate's own hands stay clean in this flow.
	for _, want := range []scene.State{
		scene.Arriving, scene.AtDeskWorking, scene.WalkingToCEO, scene.WaitingAtCEO,
		scene.WaitingReview, scene.Leaving, scene.Gone,
		scene.Idle, scene.OnPhone, scene.ReceivingDigest, scene.Reading, scene.Deciding, scene.Answering,
	} {
		if !reached[want] {
			t.Fatalf("nothing in the run put anybody in %s", want)
		}
	}

	for i, r := range rows {
		if r.To != scene.WaitingAtCEO || r.ActorID != crewActor {
			continue
		}
		if r.From == scene.Asleep || r.From == scene.Blocked {
			continue // the same wait, resumed after an incident
		}
		ended := time.Time{}
		for _, next := range rows[i+1:] {
			if next.ActorID != r.ActorID || next.To == scene.WaitingAtCEO ||
				next.To == scene.Asleep || next.To == scene.Blocked {
				continue
			}
			if next.To != scene.AtDeskWorking {
				t.Fatalf("the wait that began at %s ended in %s, not at the crew's desk",
					r.At.Format(time.RFC3339), next.To)
			}
			ended = next.At
			break
		}
		if ended.IsZero() || !ended.After(r.At) {
			t.Fatalf("the wait that began at %s never ended at the desk", r.At.Format(time.RFC3339))
		}
		t.Logf("%s waited %s at the CEO's door", r.ActorName, ended.Sub(r.At).Round(time.Millisecond))
	}

	var scenery strings.Builder
	for _, r := range rows {
		scenery.WriteString(scene.NarrateIn(r, time.Local))
		scenery.WriteString("\n")
	}
	now, err := scene.Now(ctx, handle.SQL(), scene.NowQuery{Project: project})
	if err != nil {
		t.Fatalf("Now: %v", err)
	}
	for _, n := range now {
		if n.State == scene.Unknown {
			continue
		}
		scenery.WriteString(scene.NarrateNow(n))
		scenery.WriteString("\n")
	}
	t.Logf("shop, the scene (%d transition(s)):\n%s", len(rows), scenery.String())

	// And the same scene as the JSON lines `mate events shop --scene`
	// prints, because that is the surface a dashboard reads.
	var lines strings.Builder
	for _, n := range now {
		line, err := n.JSONLine()
		if err != nil {
			t.Fatalf("snapshot line: %v", err)
		}
		lines.WriteString(line)
		lines.WriteString("\n")
	}
	t.Logf("shop, v_now:\n%s", lines.String())
}

// waitForTimelineEvent waits for the observer's next passes to record a kind.
// The ingest runs at the end of each poll, so this is at most a few polls.
func waitForTimelineEvent(t *testing.T, ctx context.Context, handle *db.DB, project, kind string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		var n int
		if err := handle.SQL().QueryRow(
			`SELECT COUNT(*) FROM event WHERE project = ? AND kind = ?`, project, kind).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", kind, err)
		}
		if n > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context ended waiting for a %s event", kind)
		case <-time.After(3 * time.Second):
		}
	}
	t.Fatalf("no %s event was recorded within %s", kind, within)
}

// assertEveryToolCallHasAnAction re-parses the transcript the ingest read and
// checks that every tool call it reports has an `action` row. The parser is
// the same one, on purpose: what is being proved is that the ingest dropped
// nothing between the parser and the database.
// It re-ingests between attempts because a transcript's trailing message
// group is withheld until the harness writes past it: an agent that is still
// finishing a turn has a call the last pass could not record yet, and the
// question being asked is whether the ingest loses anything, not whether it
// can see the future. Measured 2026-09-20: without this, the Mate's last call
// before it went idle was missing from one run in two.
func assertEveryToolCallHasAnAction(t *testing.T, handle *db.DB, actorID string, settle func() error) {
	t.Helper()
	path, kind := sessionOf(t, handle, actorID)
	if path == "" {
		t.Fatalf("no transcript was located for %s; the timeline has no turns for it at all", actorID)
	}
	parser, err := timeline.ParserFor(kind)
	if err != nil {
		t.Fatalf("no parser for %s: %v", kind, err)
	}

	deadline := time.Now().Add(2 * time.Minute)
	var calls int
	var missing []string
	for {
		if err := settle(); err != nil {
			t.Fatalf("ingest: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		batch := parser.ParseTranscript(harness.TranscriptParseState{}, data)
		if len(batch.ToolCalls) == 0 {
			t.Fatalf("%s's transcript at %s carries no tool call at all", actorID, path)
		}
		calls = len(batch.ToolCalls)
		missing = missingActions(t, handle, actorID, batch)
		if len(missing) == 0 {
			t.Logf("%s: all %d tool call(s) in %s have an action", actorID, calls, path)
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("%s: %d of %d tool call(s) in %s still have no action after the ingest settled: %s",
		actorID, len(missing), calls, path, strings.Join(missing, ", "))
}

func missingActions(t *testing.T, handle *db.DB, actorID string, batch harness.TranscriptBatch) []string {
	t.Helper()
	recorded := map[string]bool{}
	rows, err := handle.SQL().Query(
		`SELECT id FROM action WHERE actor_id = ? AND tool <> ?`, actorID, timeline.ToolThinking)
	if err != nil {
		t.Fatalf("read actions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		// The action id ends in the harness's own tool id.
		if at := strings.LastIndex(id, "#tool#"); at >= 0 {
			recorded[id[at+len("#tool#"):]] = true
		}
	}
	var missing []string
	for _, call := range batch.ToolCalls {
		if !recorded[call.SourceRef] {
			missing = append(missing, call.ToolName+" ("+call.SourceRef+")")
		}
	}
	return missing
}

func sessionOf(t *testing.T, handle *db.DB, actorID string) (path string, kind harness.Kind) {
	t.Helper()
	var harnessName string
	err := handle.SQL().QueryRow(
		`SELECT s.transcript_path, a.harness FROM session s JOIN actor a ON a.id = s.actor_id
		  WHERE s.actor_id = ? AND s.transcript_path <> '' LIMIT 1`, actorID).Scan(&path, &harnessName)
	if err != nil {
		return "", ""
	}
	return path, harness.Kind(harnessName)
}

// assertEveryStatusLineHasAnEvent reads the crew's status file itself and
// checks the timeline carries one `status.appended` per line.
func assertEveryStatusLineHasAnEvent(t *testing.T, w *store.Workspace, handle *db.DB, project, crew, crewActor string) {
	t.Helper()
	lines, _, err := w.ReadStatus(project, crew, 0)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if len(lines) == 0 {
		t.Fatalf("crew %s wrote no status line at all", crew)
	}
	offsets := map[int64]bool{}
	rows, err := handle.SQL().Query(
		`SELECT ref_offset FROM event WHERE actor_id = ? AND kind = ?`, crewActor, timeline.KindStatusAppend)
	if err != nil {
		t.Fatalf("read status events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var offset int64
		if err := rows.Scan(&offset); err != nil {
			t.Fatalf("scan: %v", err)
		}
		offsets[offset] = true
	}
	for _, line := range lines {
		if !offsets[line.Offset] {
			t.Fatalf("the status line at byte %d (%q) has no status.appended event", line.Offset, line.Line)
		}
	}
	t.Logf("crew %s: all %d status line(s) have an event", crew, len(lines))
}

// assertEveryQuestionWasAnswered checks each `needs-decision` became a
// question, and each question was answered after a positive wait.
func assertEveryQuestionWasAnswered(t *testing.T, handle *db.DB, crewActor string) {
	t.Helper()
	asked := countRows(t, handle,
		`SELECT COUNT(*) FROM event WHERE actor_id = ? AND kind = ?`, crewActor, timeline.KindQuestionAsked)
	if asked == 0 {
		t.Fatal("the crew asked nothing; the ship task leaves a choice open and the acceptance waits for it")
	}
	questions := countRows(t, handle, `SELECT COUNT(*) FROM question WHERE crew_actor_id = ?`, crewActor)
	if questions != asked {
		t.Fatalf("%d question.asked event(s) and %d question row(s)", asked, questions)
	}
	rows, err := handle.SQL().Query(
		`SELECT text, answered_event_id, waited_ms FROM question WHERE crew_actor_id = ?`, crewActor)
	if err != nil {
		t.Fatalf("read questions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var text string
		var answered sql.NullInt64
		var waited sql.NullInt64
		if err := rows.Scan(&text, &answered, &waited); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !answered.Valid {
			t.Fatalf("the question %q was never answered", text)
		}
		if !waited.Valid || waited.Int64 <= 0 {
			t.Fatalf("the question %q reports waited_ms %v; a question answered after it was asked waits a positive time",
				text, waited)
		}
		t.Logf("question %q waited %dms", text, waited.Int64)
	}
}

func assertTheMergeHasACause(t *testing.T, handle *db.DB, project, crew, branch string) {
	t.Helper()
	var id, cause sql.NullInt64
	var payload, causeKind sql.NullString
	err := handle.SQL().QueryRow(
		`SELECT id, cause_event_id, payload, cause_kind FROM v_story
		  WHERE project = ? AND kind = ?`, project, timeline.KindMergeDone).
		Scan(&id, &cause, &payload, &causeKind)
	if err != nil {
		t.Fatalf("read merge.done: %v", err)
	}
	if !cause.Valid || cause.Int64 == 0 {
		t.Fatalf("merge.done %d names no cause; payload %s", id.Int64, payload.String)
	}
	if !strings.Contains(payload.String, branch) {
		t.Fatalf("merge.done does not name the branch %s: %s", branch, payload.String)
	}
	t.Logf("merge.done %d caused by %s (%d); payload %s", id.Int64, causeKind.String, cause.Int64, payload.String)

	var merged sql.NullInt64
	if err := handle.SQL().QueryRow(`SELECT merged_event_id FROM task WHERE crew_actor_id = ?`,
		timeline.CrewActorID(project, crew)).Scan(&merged); err != nil {
		t.Fatalf("read the task: %v", err)
	}
	if !merged.Valid || merged.Int64 != id.Int64 {
		t.Fatalf("the task's merged_event_id is %v, want %d", merged, id.Int64)
	}
}

// assertNoUnexplainedBusyWindow is the "no black hole" check of M5's first
// question: inside a stretch the observer saw an agent working, there is no
// window of `window` with no action in it.
func assertNoUnexplainedBusyWindow(t *testing.T, handle *db.DB, actorID string, window time.Duration) {
	t.Helper()
	type mark struct {
		at       time.Time
		composer string
	}
	rows, err := handle.SQL().Query(
		`SELECT at, json_extract(payload, '$.to') FROM event
		  WHERE actor_id = ? AND kind = ? ORDER BY at, id`, actorID, timeline.KindHealthChanged)
	if err != nil {
		t.Fatalf("read health events: %v", err)
	}
	var marks []mark
	for rows.Next() {
		var at, composer string
		if err := rows.Scan(&at, &composer); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		marks = append(marks, mark{db.ParseTime(at), composer})
	}
	rows.Close()

	var actions []actionSpan
	arows, err := handle.SQL().Query(
		`SELECT at, ended_at FROM action WHERE actor_id = ? ORDER BY at`, actorID)
	if err != nil {
		t.Fatalf("read actions: %v", err)
	}
	for arows.Next() {
		var at string
		var ended sql.NullString
		if err := arows.Scan(&at, &ended); err != nil {
			arows.Close()
			t.Fatalf("scan: %v", err)
		}
		s := actionSpan{db.ParseTime(at), db.ParseTime(at)}
		if ended.Valid {
			if end := db.ParseTime(ended.String); end.After(s.from) {
				s.to = end
			}
		}
		actions = append(actions, s)
	}
	arows.Close()
	sort.Slice(actions, func(i, j int) bool { return actions[i].from.Before(actions[j].from) })

	checked := 0
	for i, m := range marks {
		if m.composer != "busy" || i+1 >= len(marks) {
			continue
		}
		from, to := m.at, marks[i+1].at
		if to.Sub(from) < window {
			continue
		}
		checked++
		for start := from; start.Before(to); start = start.Add(window) {
			end := start.Add(window)
			if end.After(to) {
				end = to
			}
			if !coveredBy(actions, start, end) {
				t.Fatalf("%s: the %s window %s..%s is inside a busy stretch and no action explains it",
					actorID, window, start.Format(time.RFC3339), end.Format(time.RFC3339))
			}
		}
	}
	t.Logf("%s: %d busy stretch(es) longer than %s, all explained", actorID, checked, window)
}

// actionSpan is one action's window. An action with no recorded end covers
// the instant it started, because an in-flight call is not evidence of how
// long it ran.
type actionSpan struct{ from, to time.Time }

func coveredBy(actions []actionSpan, start, end time.Time) bool {
	for _, a := range actions {
		if a.from.Before(end) && !a.to.Before(start) {
			return true
		}
	}
	return false
}

func countRows(t *testing.T, handle *db.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := handle.SQL().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}
