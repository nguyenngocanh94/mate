package dashboard

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// The API's whole claim is that it repeats the database rather than
// recomputing it. Every test here therefore compares an endpoint's number
// to the view or the helper the CLI reads for the same thing - never to a
// number written down in the test.

func TestWorkspaceCardRepeatsTheViews(t *testing.T) {
	f := newFixture(t)
	var got WorkspaceResponse
	f.get(t, "/api/workspace", http.StatusOK, &got)

	if len(got.Projects) != 1 || got.Projects[0].Name != fixtureProject {
		t.Fatalf("projects = %+v, want one named %s", got.Projects, fixtureProject)
	}
	card := got.Projects[0]
	if card.Mode != "manual" {
		t.Errorf("mode = %q, want manual", card.Mode)
	}
	if card.Mate.Harness != "claude" {
		t.Errorf("mate harness = %q, want claude (actor.harness)", card.Mate.Harness)
	}
	if !card.Mate.Running {
		t.Error("the Mate has a started_at and no stopped_at; it must read as running")
	}

	// Every number against v_now itself.
	wantState, wantSince, wantTokens := f.nowRow(t, timeline.MateActorID(fixtureProject))
	if card.Mate.State != wantState || card.Mate.Since != wantSince || card.Mate.TokensToday != wantTokens {
		t.Errorf("mate card = %+v, want v_now's %q/%q/%d", card.Mate, wantState, wantSince, wantTokens)
	}
	crewState, _, _ := f.nowRow(t, timeline.CrewActorID(fixtureProject, fixtureCrew))
	if crewState == "" {
		crewState = "unknown"
	}
	if card.CrewsByState[crewState] != 1 {
		t.Errorf("crews_by_state = %v, want one crew under %q", card.CrewsByState, crewState)
	}
	if card.InboxWaiting != f.inboxLen(t) {
		t.Errorf("inbox_waiting = %d, want box.Inbox's %d", card.InboxWaiting, f.inboxLen(t))
	}
	if got.LastEventID != f.lastEventID(t) || got.GeneratedAt == "" {
		t.Errorf("envelope = %+v, want last_event_id %d and a generated_at", got.envelope, f.lastEventID(t))
	}
}

func TestProjectTasksEqualTheTaskLedger(t *testing.T) {
	f := newFixture(t)
	var got ProjectResponse
	f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &got)

	if len(got.Tasks) != 1 {
		t.Fatalf("tasks = %d, want the fixture's one crew", len(got.Tasks))
	}
	task := got.Tasks[0]
	if task.Crew != fixtureCrew {
		t.Fatalf("crew = %q, want %q", task.Crew, fixtureCrew)
	}

	// v_task_ledger is the contract: read the same row the CLI's ledger
	// reads and compare bucket by bucket.
	var turns, in, cacheRead, cacheWrite, out, thinking, asked, waited int64
	var branch string
	if err := f.read.SQL().QueryRow(`
		SELECT turns, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens,
		       thinking_tokens, question_count, waited_ms, branch
		  FROM v_task_ledger WHERE project = ? AND crew = ?`, fixtureProject, fixtureCrew).
		Scan(&turns, &in, &cacheRead, &cacheWrite, &out, &thinking, &asked, &waited, &branch); err != nil {
		t.Fatalf("read v_task_ledger: %v", err)
	}
	if task.Turns != turns {
		t.Errorf("turns = %d, want %d", task.Turns, turns)
	}
	want := Tokens{Input: in, CacheRead: cacheRead, CacheWrite: cacheWrite, Output: out,
		Thinking: thinking, Total: in + cacheRead + cacheWrite + out}
	if task.Tokens != want {
		t.Errorf("tokens = %+v, want %+v", task.Tokens, want)
	}
	if task.QuestionCount != asked || task.WaitedMs != waited || task.Branch != branch {
		t.Errorf("task = %+v, want asked %d, waited %d, branch %q", task, asked, waited, branch)
	}
	if task.Cost != nil {
		t.Errorf("cost = %v, want null: the fixture's model has no price", *task.Cost)
	}

	// tool_count is the one ledger number no view carries: it is summed off
	// `turn`, so it must equal that sum.
	var tools int64
	if err := f.read.SQL().QueryRow(`SELECT COALESCE(SUM(tool_count),0) FROM turn WHERE actor_id = ?`,
		timeline.CrewActorID(fixtureProject, fixtureCrew)).Scan(&tools); err != nil {
		t.Fatalf("sum tool_count: %v", err)
	}
	if task.ToolCount != tools {
		t.Errorf("tool_count = %d, want turn's own sum %d", task.ToolCount, tools)
	}
	if tools == 0 {
		t.Error("the fixture crew ran tools; a ledger reporting none is not reading them")
	}

	// The Mate's block is computed from `turn` because no task row exists
	// for a Mate - the same reason cmd/mate's mateLedgerRow exists.
	var mateIn, mateCacheRead, mateCacheWrite, mateOut int64
	if err := f.read.SQL().QueryRow(`
		SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(cache_read_tokens),0),
		       COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(output_tokens),0)
		  FROM turn WHERE actor_id = ?`, timeline.MateActorID(fixtureProject)).
		Scan(&mateIn, &mateCacheRead, &mateCacheWrite, &mateOut); err != nil {
		t.Fatalf("sum the Mate's turns: %v", err)
	}
	if got.Mate.Tokens.Total != mateIn+mateCacheRead+mateCacheWrite+mateOut {
		t.Errorf("mate total = %d, want %d", got.Mate.Tokens.Total, mateIn+mateCacheRead+mateCacheWrite+mateOut)
	}
	if got.Mate.LastTurn == nil {
		t.Error("the Mate has turns; last_turn must not be null")
	}
	if got.InboxError != "" {
		t.Errorf("inbox_error = %q, want none", got.InboxError)
	}
	if len(got.Inbox) != f.inboxLen(t) {
		t.Errorf("inbox = %d item(s), want box.Inbox's %d", len(got.Inbox), f.inboxLen(t))
	}
}

func TestTaskPageCarriesTurnsStatusLinesAndAnsweredQuestions(t *testing.T) {
	f := newFixture(t)
	var got TaskResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew, http.StatusOK, &got)

	if int64(len(got.Turns)) != got.Ledger.Turns {
		t.Fatalf("%d turn(s) listed, ledger says %d", len(got.Turns), got.Ledger.Turns)
	}
	if len(got.Turns) == 0 {
		t.Fatal("the fixture crew took turns; the task page lists none")
	}
	for i, turn := range got.Turns {
		if turn.ID == "" {
			t.Fatalf("turn %d has no id", i)
		}
		if turn.Ref.Path == "" {
			t.Errorf("turn %s has no ref: every number on the page must be traceable", turn.ID)
		}
		if i > 0 && got.Turns[i-1].StartedAt > turn.StartedAt {
			t.Errorf("turns are out of order at %d: %s then %s", i, got.Turns[i-1].StartedAt, turn.StartedAt)
		}
		if turn.Tokens.Total != turn.Tokens.Input+turn.Tokens.CacheRead+turn.Tokens.CacheWrite+turn.Tokens.Output {
			t.Errorf("turn %s total does not equal its buckets: %+v", turn.ID, turn.Tokens)
		}
	}

	// The three lines the crew wrote, in the order it wrote them.
	if len(got.StatusLines) != 3 {
		t.Fatalf("status lines = %d, want the crew's 3", len(got.StatusLines))
	}
	verbs := []string{got.StatusLines[0].Verb, got.StatusLines[1].Verb, got.StatusLines[2].Verb}
	if strings.Join(verbs, ",") != "working,needs-decision,wait-mate" {
		t.Errorf("status verbs = %v, want working, needs-decision, wait-mate", verbs)
	}

	// The question and the answer joined through `cause`, which is what the
	// ingest recorded and what `question.waited_ms` measures.
	if len(got.Questions) != 1 {
		t.Fatalf("questions = %d, want the crew's 1", len(got.Questions))
	}
	q := got.Questions[0]
	if !strings.Contains(q.Text, "checkout page URL") {
		t.Errorf("question text = %q", q.Text)
	}
	if q.AnsweredEventID == 0 || q.Answer == "" {
		t.Errorf("question %+v was answered in the fixture; the page shows no answer", q)
	}
	if q.WaitedMs == nil || *q.WaitedMs <= 0 {
		t.Errorf("waited_ms = %v, want the wait the ingest measured", q.WaitedMs)
	}
	var wantWaited int64
	if err := f.read.SQL().QueryRow(`SELECT waited_ms FROM question WHERE id = ?`, q.ID).Scan(&wantWaited); err != nil {
		t.Fatalf("read question: %v", err)
	}
	if *q.WaitedMs != wantWaited {
		t.Errorf("waited_ms = %d, want the row's %d", *q.WaitedMs, wantWaited)
	}

	if got.Branch.Name != fixtureBranch || !got.Branch.Exists {
		t.Errorf("branch = %+v, want %s, present", got.Branch, fixtureBranch)
	}
}

func TestTurnPageCarriesItsActionsAndItsStoryRows(t *testing.T) {
	f := newFixture(t)
	var task TaskResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew, http.StatusOK, &task)

	// The turn with the most tool calls is the one worth opening.
	best := task.Turns[0]
	for _, turn := range task.Turns {
		if turn.ToolCount > best.ToolCount {
			best = turn
		}
	}
	// A turn id carries `#` (`crew:shop:buybtn#<session>#turn#20`), so the
	// path segment has to be escaped - as docs/dashboard.md tells task 29's
	// UI to do. An unescaped `#` would make the browser send only the part
	// before it and treat the rest as a fragment.
	var got TurnResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew+"/turns/"+url.PathEscape(best.ID),
		http.StatusOK, &got)

	if got.Turn.ID != best.ID {
		t.Fatalf("turn = %q, want %q", got.Turn.ID, best.ID)
	}
	// `tool_count` counts the calls the harness made; `action` also holds
	// the synthesised `thinking` rows the ingest writes for a busy stretch
	// no call explains (docs/timeline.md section 4), so the two agree only
	// once those are set aside.
	real := 0
	for _, a := range got.Actions {
		if a.Tool == "" {
			t.Errorf("action %s has no tool", a.ID)
		}
		if a.Tool != "thinking" {
			real++
		}
	}
	if int64(real) != best.ToolCount {
		t.Errorf("%d tool call(s) listed beside %d thinking row(s), the turn records tool_count %d",
			real, len(got.Actions)-real, best.ToolCount)
	}

	// The events inside the turn are v_story rows, and every one of them
	// belongs to this turn.
	if len(got.Events) == 0 {
		t.Fatal("a turn with tool calls has events inside it; none were returned")
	}
	all, err := timeline.Story(context.Background(), f.read.SQL(), timeline.StoryQuery{Project: fixtureProject})
	if err != nil {
		t.Fatalf("timeline.Story: %v", err)
	}
	want := 0
	for _, e := range all {
		if e.Turn == best.ID {
			want++
		}
	}
	if len(got.Events) != want {
		t.Errorf("events = %d, want v_story's %d for this turn", len(got.Events), want)
	}
	for _, e := range got.Events {
		if e.Turn != best.ID {
			t.Errorf("event %d belongs to turn %q, not %q", e.ID, e.Turn, best.ID)
		}
	}
}

func TestEventsMatchWhatTheEventsCommandPrints(t *testing.T) {
	f := newFixture(t)
	var got EventsResponse
	f.get(t, "/api/events?since=0&project="+fixtureProject, http.StatusOK, &got)

	want, err := timeline.Story(context.Background(), f.read.SQL(), timeline.StoryQuery{Project: fixtureProject})
	if err != nil {
		t.Fatalf("timeline.Story: %v", err)
	}
	if len(got.Events) != len(want) {
		t.Fatalf("events = %d, want the story's %d", len(got.Events), len(want))
	}
	for i := range want {
		if got.Events[i].ID != want[i].ID || got.Events[i].Kind != want[i].Kind {
			t.Fatalf("event %d = %d/%s, want %d/%s", i,
				got.Events[i].ID, got.Events[i].Kind, want[i].ID, want[i].Kind)
		}
	}
	if len(got.Now) == 0 {
		t.Error("a since of 0 covers every transition; the scene rows that moved must be included")
	}
}

func TestEventsReturnsImmediatelyForANewerEventAndEmptyAfterTheWait(t *testing.T) {
	f := newFixture(t)
	last := f.lastEventID(t)

	// Newer than `since`: the wait is never entered, so even a long one
	// costs nothing.
	start := time.Now()
	var fresh EventsResponse
	f.get(t, "/api/events?since=0&wait=20&project="+fixtureProject, http.StatusOK, &fresh)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("a poll with newer events waited %s; it must return at once", elapsed)
	}
	if len(fresh.Events) == 0 {
		t.Fatal("since=0 must return the whole story")
	}

	// Nothing newer: the wait runs out and the answer is an empty list, not
	// a held connection.
	start = time.Now()
	var empty EventsResponse
	f.get(t, "/api/events?since="+strconv.FormatInt(last, 10)+"&wait=1&project="+fixtureProject, http.StatusOK, &empty)
	if len(empty.Events) != 0 {
		t.Fatalf("events after the newest id = %d, want none", len(empty.Events))
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("the poll returned after %s; it must wait the second it was asked for", elapsed)
	}
	if empty.LastEventID != last {
		t.Errorf("last_event_id = %d, want %d", empty.LastEventID, last)
	}
}

func TestACachedAnswerIsRebuiltOnlyWhenTheEventIDMoves(t *testing.T) {
	f := newFixture(t)
	var first WorkspaceResponse
	f.get(t, "/api/workspace", http.StatusOK, &first)
	builds := f.server.cache.builds

	for i := 0; i < 3; i++ {
		var again WorkspaceResponse
		f.get(t, "/api/workspace", http.StatusOK, &again)
		if again.GeneratedAt != first.GeneratedAt || again.LastEventID != first.LastEventID {
			t.Fatalf("a repeat read was recomputed: %+v then %+v", first.envelope, again.envelope)
		}
	}
	if f.server.cache.builds != builds {
		t.Fatalf("the answer was rebuilt %d time(s) while the event id stood still",
			f.server.cache.builds-builds)
	}

	id := f.appendEvent(t)
	var moved WorkspaceResponse
	f.get(t, "/api/workspace", http.StatusOK, &moved)
	if moved.LastEventID != id {
		t.Fatalf("last_event_id = %d, want the new event's %d", moved.LastEventID, id)
	}
	if f.server.cache.builds == builds {
		t.Fatal("the event id moved and nothing was recomputed")
	}
}

func TestAnUnknownProjectOrCrewIs404WithAReason(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{
		"/api/projects/nosuch",
		"/api/projects/nosuch/tasks/buybtn",
		"/api/projects/" + fixtureProject + "/tasks/nosuch",
		"/api/projects/" + fixtureProject + "/tasks/nosuch/diff",
		"/api/projects/" + fixtureProject + "/tasks/" + fixtureCrew + "/turns/nosuch",
		"/api/events?project=nosuch",
	} {
		var got ErrorResponse
		f.get(t, path, http.StatusNotFound, &got)
		if got.Error == "" {
			t.Errorf("GET %s: a 404 must say why", path)
		}
		if !strings.Contains(got.Error, "nosuch") {
			t.Errorf("GET %s: reason %q does not name what was missing", path, got.Error)
		}
	}
}

func TestDiffIsTheCLIsTextAndSaysSoWhenTheBranchIsGone(t *testing.T) {
	f := newFixture(t)
	var got DiffResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew+"/diff", http.StatusOK, &got)
	if !got.Exists || !strings.Contains(got.Text, "docs: add Buy link") {
		t.Fatalf("diff = %+v, want the branch's text", got)
	}

	// A branch git no longer has: the answer is empty with a reason, not an
	// error, because a crew whose branch was deleted is a crew whose work
	// landed.
	f.server.deps.BranchExists = func(context.Context, string, string) (bool, error) { return false, nil }
	f.server.cache = newCache()
	var gone DiffResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew+"/diff", http.StatusOK, &gone)
	if gone.Exists || gone.Text != "" || gone.Reason == "" {
		t.Fatalf("diff of a deleted branch = %+v, want empty with a reason", gone)
	}
}

func TestANonLoopbackBindIsRefused(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:7777", "192.168.1.10:7777", ":7777", "example.com:7777"} {
		if _, err := Listen(Options{Addr: addr}); err == nil {
			t.Errorf("Listen(%q) was allowed without --allow-remote", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:0", "localhost:0", "[::1]:0"} {
		ln, err := Listen(Options{Addr: addr})
		if err != nil {
			t.Errorf("Listen(%q): %v", addr, err)
			continue
		}
		_ = ln.Close()
	}
}

func TestTheServerRefusesAWriterHandle(t *testing.T) {
	f := newFixture(t)
	writer, err := db.Open(f.ws)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer writer.Close()
	if _, err := New(Options{Workspace: f.ws, DB: writer}); err == nil {
		t.Fatal("a dashboard built on the writer handle would hold the console out of its own timeline")
	}
}

func TestTheEmbeddedUIIsServedAtTheRoot(t *testing.T) {
	f := newFixture(t)
	resp, err := f.http.Client().Get(f.http.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("GET / content type = %q, want html", ct)
	}
}

func TestNoEndpointAcceptsAWrite(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{
		"/api/workspace",
		"/api/projects/" + fixtureProject,
		"/api/events",
	} {
		resp, err := f.http.Client().Post(f.http.URL+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("POST %s was accepted; this API has no write endpoint", path)
		}
	}
}

// --- helpers that read the database the endpoints claim to repeat ---

func (f *fixture) lastEventID(t *testing.T) int64 {
	t.Helper()
	var id sql.NullInt64
	if err := f.read.SQL().QueryRow(`SELECT MAX(id) FROM event`).Scan(&id); err != nil {
		t.Fatalf("MAX(event.id): %v", err)
	}
	return id.Int64
}

func (f *fixture) nowRow(t *testing.T, actorID string) (state, since string, tokens int64) {
	t.Helper()
	var s, at sql.NullString
	if err := f.read.SQL().QueryRow(
		`SELECT state, since, tokens_today FROM v_now WHERE actor_id = ?`, actorID).
		Scan(&s, &at, &tokens); err != nil {
		t.Fatalf("read v_now for %s: %v", actorID, err)
	}
	return s.String, at.String, tokens
}

func (f *fixture) inboxLen(t *testing.T) int {
	t.Helper()
	items, reason := f.server.inbox(fixtureProject)
	if reason != "" {
		t.Fatalf("the fixture's box must read: %s", reason)
	}
	return len(items)
}
