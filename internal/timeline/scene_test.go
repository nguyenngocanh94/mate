package timeline_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/store"
	"github.com/nguyenngocanh94/matev2/internal/timeline"
	"github.com/nguyenngocanh94/matev2/internal/timeline/scene"
)

// The depth test of docs/mvp.md task 26. The claim is that the office scene
// is a consequence of the data and not an illustration drawn beside it: every
// event of a real run moves somebody or is explicitly explained as moving
// nobody, every wait at the CEO's door is a measurable stretch that ends with
// the crew back at its desk, and the whole thing reads as a story.
//
// The fixture is the acceptance run task 25 captured, plus the parts of a run
// that fixture stops short of: the observer's two findings, a daemon digest,
// and the merge that closes the crew. Those are appended as the files
// themselves record them - `incidents.log` lines, a `sent.log` line, a
// `.meta` state and a branch merged in git - so the scene is still read out
// of sources and not out of a hand-written event list.

// The moments the extra lines happened at. They are inside the captured run's
// clock, which is what makes them a continuation of it rather than a second
// story.
var (
	sceneStaleAt    = mustTime("2026-09-19T10:45:05Z")
	sceneAwakeAt    = mustTime("2026-09-19T10:45:20Z")
	sceneDigestAt   = mustTime("2026-09-19T10:45:30Z")
	sceneLostAt     = mustTime("2026-09-19T10:46:30Z")
	sceneFoundAt    = mustTime("2026-09-19T10:46:35Z")
	sceneClosedAt   = mustTime("2026-09-19T10:46:50Z")
	sceneDigestText = `digest: 1 item(s) — buybtn needs-decision: "what is the checkout page URL for the Buy button?"`
)

// newSceneFixture is newFixture carried to the end of the run.
func newSceneFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)

	for _, in := range []store.IncidentEntry{
		{Time: sceneStaleAt, Crew: fixtureCrew, Kind: "stale", State: store.IncidentOpen,
			Text: "no status line and no pane change for 3m0s; composer empty"},
		{Time: sceneAwakeAt, Crew: fixtureCrew, Kind: "stale", State: store.IncidentResolved,
			Text: "the pane moved again"},
		{Time: sceneLostAt, Crew: fixtureCrew, Kind: "runtime_lost", State: store.IncidentOpen,
			Text: "herdr does not have agent crew-buybtn"},
		{Time: sceneFoundAt, Crew: fixtureCrew, Kind: "runtime_lost", State: store.IncidentResolved,
			Text: "the agent answered again"},
	} {
		if err := f.ws.AppendIncident(fixtureProject, in); err != nil {
			t.Fatalf("AppendIncident: %v", err)
		}
	}
	if err := f.ws.AppendSent(fixtureProject, store.SentEntry{
		Time: sceneDigestAt, Source: store.SourceApp, Target: store.TargetMate, Text: sceneDigestText,
	}); err != nil {
		t.Fatalf("AppendSent: %v", err)
	}

	// The captain merged from the console and the merge closed the crew
	// (docs/mvp.md M4). Nothing writes that down but git and the `.meta`.
	mergeFixtureBranch(t, f)
	meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
	if err != nil {
		t.Fatalf("ReadCrewMeta: %v", err)
	}
	meta["state"] = "finished"
	meta["stopped_at"] = sceneClosedAt.Format(time.RFC3339)
	if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
	return f
}

// mergeFixtureBranch puts the crew's branch into the default branch the way
// `matev2 merge` does: fast-forward, and the branch left behind is not what
// the ingest keys on anyway.
func mergeFixtureBranch(t *testing.T, f *fixture) {
	t.Helper()
	repo := filepath.Join(f.root, fixtureProject)
	readme := filepath.Join(repo, "README.md")
	body, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	for _, args := range [][]string{
		{"checkout", "-b", fixtureBranch},
	} {
		runGit(t, repo, args...)
	}
	if err := os.WriteFile(readme, append(body, []byte("\n[Buy](pages/checkout-express.html)\n")...), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "docs: add Buy link")
	runGit(t, repo, "checkout", "main")
	runGit(t, repo, "merge", "--ff-only", fixtureBranch)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func sceneRows(t *testing.T, f *fixture) []scene.Row {
	t.Helper()
	rows, err := scene.Transitions(context.Background(), f.db.SQL(),
		scene.TransitionQuery{Project: fixtureProject})
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the projection wrote no transition at all")
	}
	return rows
}

// The scene machine has to know every kind the ingest can write. A kind with
// no row in the table would land in the timeline as an `unexplained`
// transition on the day it is first emitted, which is late.
func TestSceneKnowsEveryKindTheTimelineWrites(t *testing.T) {
	got := strings.Join(scene.Kinds(), "\n")
	want := strings.Join(timeline.Kinds(), "\n")
	if got != want {
		t.Fatalf("the scene machine and the event vocabulary disagree.\n--- scene ---\n%s\n--- timeline ---\n%s", got, want)
	}
}

// Nothing is silently dropped: an event that matches no edge is recorded as a
// transition carrying `unexplained: <kind>`, and there must be none of those.
func TestTheSceneExplainsEveryEventOfTheAcceptance(t *testing.T) {
	f := newSceneFixture(t)
	f.ingest(t)

	var unexplained []string
	for _, r := range sceneRows(t, f) {
		if r.Unexplained() {
			unexplained = append(unexplained,
				fmt.Sprintf("%s %s %s (event %d)", r.At.Format(time.RFC3339), r.ActorName, r.Detail, r.EventID))
		}
	}
	if len(unexplained) > 0 {
		t.Fatalf("%d event(s) no edge explains:\n%s", len(unexplained), strings.Join(unexplained, "\n"))
	}

	// And every event of the project is either one the machine moved
	// somebody with, or one the table names as moving nobody. The guard
	// above proves the second half only if the projection actually saw the
	// events, which this counts.
	events := f.story(t)
	if len(events) < 50 {
		t.Fatalf("the fixture produced %d events, which is not the acceptance run", len(events))
	}
}

// A wait at the CEO's door is the measurement M5 asks for: how long a crew
// stood there, ending when it went back to its desk. An incident may hold the
// crew in the middle of the wait - the observer calls a crew that is doing
// nothing but waiting `stale` - and the wait is still one wait.
func TestEveryWaitAtTheCEOsDoorEndsAtTheDesk(t *testing.T) {
	f := newSceneFixture(t)
	f.ingest(t)

	rows := sceneRows(t, f)
	waits := 0
	for i, r := range rows {
		if r.To != scene.WaitingAtCEO || r.ActorID != f.crewActor() {
			continue
		}
		if r.From == scene.Asleep || r.From == scene.Blocked {
			continue // the same wait, resumed after an incident
		}
		waits++
		ended := time.Time{}
		for _, next := range rows[i+1:] {
			if next.ActorID != r.ActorID {
				continue
			}
			if next.To == scene.WaitingAtCEO || next.To == scene.Asleep || next.To == scene.Blocked {
				continue
			}
			if next.To != scene.AtDeskWorking {
				t.Fatalf("the wait that began at %s ended in %s, not at the crew's desk",
					r.At.Format(time.RFC3339), next.To)
			}
			ended = next.At
			break
		}
		if ended.IsZero() {
			t.Fatalf("the wait that began at %s never ended", r.At.Format(time.RFC3339))
		}
		waited := ended.Sub(r.At)
		if waited <= 0 {
			t.Fatalf("the wait that began at %s measured %s", r.At.Format(time.RFC3339), waited)
		}
		// The same number the question table measured, from the other side.
		var recorded int64
		if err := f.db.SQL().QueryRow(
			`SELECT waited_ms FROM question WHERE crew_actor_id = ?`, f.crewActor()).Scan(&recorded); err != nil {
			t.Fatalf("read the question's wait: %v", err)
		}
		if recorded != waited.Milliseconds() {
			t.Fatalf("the scene measures %dms at the door and the question table %dms",
				waited.Milliseconds(), recorded)
		}
	}
	if waits == 0 {
		t.Fatal("the acceptance run has a question in it and the scene has nobody at the door")
	}
}

// Every state of the machine is reached by the fixtures, or this test says
// which are not and why. The list is the point: a state nothing can reach is
// either a state nobody needs or a producer nobody wrote.
func TestTheSceneReachesEveryStateOrNamesTheOnesItCannot(t *testing.T) {
	f := newSceneFixture(t)
	f.ingest(t)

	reached := map[scene.State]bool{}
	details := map[string]bool{}
	for _, r := range sceneRows(t, f) {
		reached[r.To] = true
		if r.Detail != "" {
			details[string(r.To)+"("+r.Detail+")"] = true
		}
	}

	// What the acceptance run cannot reach, and why. Each of these is
	// covered by the scene package's own table-driven test instead.
	expectedMisses := map[scene.State]string{
		scene.Reviewing: "the captain reviewed the diff in the console, which writes no file; " +
			"the Mate never ran `matev2 diff` and nothing emits `review.started`",
		scene.Merging: "the captain merged from the console, so `merge.done` carries `by: captain` " +
			"and the Mate's own hands stayed clean",
	}
	for _, state := range []scene.State{
		scene.Arriving, scene.AtDeskWorking, scene.WalkingToCEO, scene.WaitingAtCEO,
		scene.WaitingReview, scene.Leaving, scene.Gone, scene.Blocked, scene.Asleep,
		scene.Idle, scene.Reading, scene.Deciding, scene.Answering, scene.Reviewing,
		scene.Merging, scene.OnPhone, scene.ReceivingDigest,
	} {
		if reached[state] {
			if why, ok := expectedMisses[state]; ok {
				t.Fatalf("%s is reached after all; the note saying it cannot be is stale: %s", state, why)
			}
			continue
		}
		why, ok := expectedMisses[state]
		if !ok {
			t.Fatalf("no event of the acceptance run puts anybody in %s", state)
		}
		t.Logf("not reached by this fixture: %s - %s", state, why)
	}

	// Both walks and both ways a note arrives, which are the parameterised
	// states the spec names.
	for _, want := range []string{
		"walking_to_ceo(question)", "walking_to_ceo(handback)", "leaving(merged)",
		"receiving_digest(assign)", "receiving_digest(digest)",
		"asleep(stale)", "blocked(runtime_lost)",
	} {
		if !details[want] {
			t.Fatalf("the acceptance run never reaches %s", want)
		}
	}
}

// v_now is the question "who is doing what, right now" (docs/mvp.md M5
// question 1), and it is NULL until this projection runs.
func TestVNowReadsTheProjection(t *testing.T) {
	f := newSceneFixture(t)
	f.ingest(t)

	rows, err := scene.Now(context.Background(), f.db.SQL(), scene.NowQuery{Project: fixtureProject})
	if err != nil {
		t.Fatalf("Now: %v", err)
	}
	seen := map[string]scene.NowRow{}
	for _, row := range rows {
		seen[row.ActorID] = row
	}
	crew, ok := seen[f.crewActor()]
	if !ok {
		t.Fatalf("v_now has no row for the crew; it has %d row(s)", len(rows))
	}
	if crew.State != scene.Gone || crew.Detail != scene.DetailMerged {
		t.Fatalf("the crew ends the run in %s(%s), want gone(merged)", crew.State, crew.Detail)
	}
	if crew.Since.IsZero() {
		t.Fatal("the crew's state has no `since`")
	}
	mate, ok := seen[f.mateActor()]
	if !ok {
		t.Fatal("v_now has no row for the Mate")
	}
	if mate.State != scene.Idle {
		t.Fatalf("the Mate ends the run in %s, want idle", mate.State)
	}
	// The captain, matev2 and the observer are actors with no scene, and
	// v_now still lists them: a row with no state is the honest answer.
	if len(rows) < 3 {
		t.Fatalf("v_now lists %d actor(s)", len(rows))
	}
}

// The projection is a function of the events, so a rebuild has to produce the
// same rows byte for byte - ids included, because a renderer follows them.
func TestReindexingTwiceProducesTheSameScene(t *testing.T) {
	f := newSceneFixture(t)
	ctx := context.Background()
	if err := f.ing.Reindex(ctx); err != nil {
		t.Fatalf("first reindex: %v", err)
	}
	first := dumpTransitions(t, f)
	if err := f.ing.Reindex(ctx); err != nil {
		t.Fatalf("second reindex: %v", err)
	}
	if second := dumpTransitions(t, f); second != first {
		t.Fatalf("two rebuilds produced different scenes.\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	// And an ordinary pass over the same files changes nothing either.
	f.ingest(t)
	if third := dumpTransitions(t, f); third != first {
		t.Fatalf("a poll after a rebuild moved somebody.\n--- rebuild ---\n%s\n--- poll ---\n%s", first, third)
	}
}

func dumpTransitions(t *testing.T, f *fixture) string {
	t.Helper()
	rows, err := f.db.SQL().Query(
		`SELECT id, actor_id, from_state, to_state, at, event_id, COALESCE(target_actor_id, ''), detail
		   FROM transition ORDER BY id`)
	if err != nil {
		t.Fatalf("dump transitions: %v", err)
	}
	defer rows.Close()
	var out strings.Builder
	for rows.Next() {
		var id, actor, from, to, at, target, detail string
		var event int64
		if err := rows.Scan(&id, &actor, &from, &to, &at, &event, &target, &detail); err != nil {
			t.Fatalf("scan: %v", err)
		}
		fmt.Fprintf(&out, "%s|%s|%s|%s|%s|%d|%s|%s\n", id, actor, from, to, at, event, target, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out.String()
}

// The narrated scene is the M5 claim in one file: somebody who has never seen
// matev2 reads this and knows what happened in the office. The golden is the
// whole run, so a phrase that changes shows up as a diff rather than as a
// sentence nobody reads.
func TestSceneNarrateGolden(t *testing.T) {
	f := newSceneFixture(t)
	f.ingest(t)

	var lines []string
	for _, r := range sceneRows(t, f) {
		lines = append(lines, scene.NarrateIn(r, time.UTC))
	}
	now, err := scene.Now(context.Background(), f.db.SQL(), scene.NowQuery{Project: fixtureProject})
	if err != nil {
		t.Fatalf("Now: %v", err)
	}
	for _, n := range now {
		if n.State == scene.Unknown {
			continue
		}
		lines = append(lines, scene.NarrateNowIn(n, time.UTC))
	}
	got := strings.Join(lines, "\n") + "\n"

	golden := "testdata/scene.golden"
	if os.Getenv("MATEV2_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (re-run with MATEV2_UPDATE_GOLDEN=1 to create it)", err)
	}
	if got != string(want) {
		t.Fatalf("the narrated scene changed.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// The transition rows carry the event that caused them, so a reader who
// doubts a move can read the fact it came from.
func TestEveryTransitionNamesTheEventThatCausedIt(t *testing.T) {
	f := newSceneFixture(t)
	f.ingest(t)

	for _, r := range sceneRows(t, f) {
		var kind string
		err := f.db.SQL().QueryRow(`SELECT kind FROM event WHERE id = ?`, r.EventID).Scan(&kind)
		if err == sql.ErrNoRows {
			t.Fatalf("transition %s points at event %d, which does not exist", r.ID, r.EventID)
		}
		if err != nil {
			t.Fatalf("read event %d: %v", r.EventID, err)
		}
	}
}
