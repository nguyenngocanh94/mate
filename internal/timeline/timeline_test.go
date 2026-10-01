package timeline_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/hook"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// A digest or an `[assign]` line reaches `sent.log` twice - once from the
// sender, once from the Mate's own UserPromptSubmit hook when the model read
// it (docs/mvp.md section 7). That pair is one handover, and the story has to
// say so: measured 2026-09-20 in TestLiveTimelineExplainsTheAcceptance, an
// ingest that treated both as handovers narrated the same `[assign]` twice,
// one second apart.
func TestTheHooksEchoOfAnAppLineIsTheMateReadingItNotASecondHandover(t *testing.T) {
	f := newFixture(t)
	// The Mate's hook records the same `[assign]` line seven seconds later,
	// which is what the live run does.
	if err := f.ws.AppendSent(fixtureProject, store.SentEntry{
		Time: fixtureAssignAt.Add(7 * time.Second), Source: store.SourceApp,
		Target: store.TargetMate, Text: assignLine,
	}); err != nil {
		t.Fatalf("AppendSent: %v", err)
	}
	f.ingest(t)

	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ?`, timeline.KindAssignClicked); n != 1 {
		t.Fatalf("%d assign.clicked event(s) for one handover recorded twice, want 1", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM message WHERE channel = ?`, timeline.ChannelHook); n != 1 {
		t.Fatalf("%d hook message(s), want the one the Mate's hook wrote", n)
	}
	var confirmed int
	if err := f.db.SQL().QueryRow(
		`SELECT COUNT(*) FROM event WHERE json_extract(payload, '$.confirms') = 1`).Scan(&confirmed); err != nil {
		t.Fatalf("count confirmations: %v", err)
	}
	if confirmed != 1 {
		t.Fatalf("%d confirmed reading(s), want 1", confirmed)
	}
	var narrated bool
	for _, e := range f.story(t) {
		if timeline.Narrate(e) != "" && e.Field("confirms") == "true" {
			narrated = timeline.NarrateIn(e, time.UTC) == "10:44:47 the Mate reads it"
		}
	}
	if !narrated {
		t.Fatal("the hook's echo is not narrated as the Mate reading the line")
	}
}

// The hook's own auto-off note is spelled in two packages, because
// internal/watch calls this ingest and may not gain internal/hook through it.
func TestHookTextsMatchHook(t *testing.T) {
	if timeline.HookAutoOffText != hook.AutoOffText {
		t.Fatalf("timeline spells the auto-off note %q where the hook writes %q",
			timeline.HookAutoOffText, hook.AutoOffText)
	}
}

// The numbers below are read out of the fixtures, not out of the code. Each
// comment names the record they came from, so a reader can check them against
// the file with `jq` rather than against the ingest that produced them.

// codex-0.154-rollout.jsonl carries 11 `token_count` records, which is 11
// model calls; its last `total_token_usage` is the session total, and a turn
// table built from the per-call deltas has to add back up to it.
const (
	codexTurns     = 11
	codexToolCalls = 9
	// codexTotalInput is fresh input only - the rollout's raw cumulative
	// input_tokens (232424) net of cached_input_tokens (209152), because
	// task 27's ingest stores turn.input_tokens net of cache so it means
	// the same thing for both harnesses (internal/timeline/transcript.go's
	// codexTurns doc comment): Codex's own input_tokens is cache-inclusive,
	// so summing it again with cache_read_tokens would double the cached
	// portion. 232424 + 1544 (output) = 233968 = the rollout's own
	// total_tokens field, which is the identity this fix exists to satisfy.
	codexTotalInput  = 232424 - 209152
	codexTotalCached = 209152
	codexTotalWrite  = 0
	codexTotalOutput = 1544
	codexTotalThink  = 351
	// The last record's `last_token_usage.input_tokens` (+ cache_write): the
	// prompt that call actually carried.
	codexLastContext = 22420
)

// claude-2.1.278-transcript.jsonl carries 12 assistant message groups and 11
// tool_use blocks. ParseTranscript withholds the trailing group - a later
// write may still extend it (internal/harness/transcript.go) - so 11 turns
// and 10 tool calls are what an ingest of a live file may record, and the
// token totals are the first 11 groups'.
const (
	claudeGroupsInFile = 12
	claudeTurns        = 11
	claudeToolCalls    = 10
	claudeTotalInput   = 292
	claudeTotalCached  = 632984
	claudeTotalWrite   = 38665
	claudeTotalOutput  = 3874
	claudeTotalThink   = 889
	claudeLastContext  = 63808
)

type totals struct {
	turns, input, cacheRead, cacheWrite, output, thinking int64
}

func turnTotals(t *testing.T, f *fixture, actorID string) totals {
	t.Helper()
	var out totals
	err := f.db.SQL().QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(cache_read_tokens),0),
		        COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(output_tokens),0),
		        COALESCE(SUM(thinking_tokens),0)
		   FROM turn WHERE actor_id = ?`, actorID).
		Scan(&out.turns, &out.input, &out.cacheRead, &out.cacheWrite, &out.output, &out.thinking)
	if err != nil {
		t.Fatalf("turn totals for %s: %v", actorID, err)
	}
	return out
}

// A Codex rollout's turns are the groups between its `token_count` records,
// and the deltas they carry have to add back up to the session total the
// rollout itself reports. Anything else means the cumulative counter was
// added twice or a group was missed.
//
// codexTotalInput is net of the cache-read total, not the rollout's raw
// input_tokens: task 27's ingest stores it that way so summing all four
// buckets means the same thing for both harnesses (see codexTurns's doc
// comment). codexTotalInput + codexTotalCached recovers the raw cumulative
// input_tokens (23272 + 209152 = 232424), and that raw number plus
// codexTotalOutput (232424 + 1544 = 233968) is exactly the rollout's own
// last `total_token_usage.total_tokens` - the identity
// TestLiveUsageMatchesTheHarness checks live against a real rollout.
func TestCodexRolloutBecomesTurnsWhoseTokensAddUpToTheRolloutsOwnTotal(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)

	got := turnTotals(t, f, f.crewActor())
	want := totals{codexTurns, codexTotalInput, codexTotalCached, codexTotalWrite, codexTotalOutput, codexTotalThink}
	if got != want {
		t.Fatalf("crew turn totals = %+v, want %+v", got, want)
	}

	var context int64
	if err := f.db.SQL().QueryRow(
		`SELECT context_tokens_after FROM turn WHERE actor_id = ? ORDER BY ordinal DESC LIMIT 1`,
		f.crewActor()).Scan(&context); err != nil {
		t.Fatalf("last context: %v", err)
	}
	if context != codexLastContext {
		t.Fatalf("the last Codex turn reports context_tokens_after %d, want %d (the record's last_token_usage)",
			context, codexLastContext)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM turn WHERE actor_id = ? AND model = ''`, f.crewActor()); n != 0 {
		t.Fatalf("%d Codex turn(s) have no model; the rollout's turn_context names one", n)
	}
	// usage_sample keeps the raw shape: Codex reports cumulative totals, and
	// M5 keeps the sample rather than only the arithmetic.
	if n := f.count(t, `SELECT COUNT(*) FROM usage_sample u
	                     JOIN session s ON s.id = u.session_id
	                    WHERE s.actor_id = ? AND u.cumulative = 1`, f.crewActor()); n != codexTurns {
		t.Fatalf("%d cumulative usage sample(s) for the crew, want %d", n, codexTurns)
	}
}

// Claude reports usage per API call, so its turns are its assistant message
// groups and its samples are per message. The trailing group is withheld by
// the parser, and the ingest must not invent it.
func TestClaudeTranscriptBecomesOneTurnPerMessageGroupExceptTheOpenOne(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)

	got := turnTotals(t, f, f.mateActor())
	want := totals{claudeTurns, claudeTotalInput, claudeTotalCached, claudeTotalWrite, claudeTotalOutput, claudeTotalThink}
	if got != want {
		t.Fatalf("Mate turn totals = %+v, want %+v", got, want)
	}
	if claudeTurns != claudeGroupsInFile-1 {
		t.Fatalf("the fixture has %d groups and the test expects %d turns; the withheld group is exactly one",
			claudeGroupsInFile, claudeTurns)
	}
	var context int64
	if err := f.db.SQL().QueryRow(
		`SELECT context_tokens_after FROM turn WHERE actor_id = ? ORDER BY ordinal DESC LIMIT 1`,
		f.mateActor()).Scan(&context); err != nil {
		t.Fatalf("last context: %v", err)
	}
	if context != claudeLastContext {
		t.Fatalf("the last Claude turn reports context_tokens_after %d, want %d (input + cache_read + cache_creation)",
			context, claudeLastContext)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM usage_sample u
	                     JOIN session s ON s.id = u.session_id
	                    WHERE s.actor_id = ? AND u.cumulative = 0`, f.mateActor()); n != claudeTurns {
		t.Fatalf("%d per-message usage sample(s) for the Mate, want %d", n, claudeTurns)
	}
}

// Every tool call in both transcripts becomes an action with a target a
// reader can act on: a file for an edit, the command for a shell call.
func TestEveryToolCallBecomesAnActionWithATarget(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)

	if n := f.count(t, `SELECT COUNT(*) FROM action WHERE actor_id = ? AND tool <> ?`,
		f.crewActor(), timeline.ToolThinking); n != codexToolCalls {
		t.Fatalf("%d crew action(s), want %d - one per tool call in the rollout", n, codexToolCalls)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM action WHERE actor_id = ? AND tool <> ?`,
		f.mateActor(), timeline.ToolThinking); n != claudeToolCalls {
		t.Fatalf("%d Mate action(s), want %d - one per tool_use block outside the open group", n, claudeToolCalls)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM action WHERE tool <> ? AND target = ''`,
		timeline.ToolThinking); n != 0 {
		t.Fatalf("%d action(s) have no target", n)
	}
	// Codex wraps its shell calls in a JavaScript snippet and its edits in a
	// patch built by one; an ingest that did not unwrap them records the
	// escaping instead of the command (measured 2026-09-20, codex-cli 0.154).
	var target string
	if err := f.db.SQL().QueryRow(
		`SELECT target FROM action WHERE actor_id = ? AND summary LIKE '%git add README.md%'`,
		f.crewActor()).Scan(&target); err != nil {
		t.Fatalf("find the commit action: %v", err)
	}
	if !strings.HasPrefix(target, "git add README.md && git commit") {
		t.Fatalf("the commit action's target is %q; it should be the command the crew ran", target)
	}
	if err := f.db.SQL().QueryRow(
		`SELECT target FROM action WHERE actor_id = ? AND summary LIKE '%Begin Patch%'`,
		f.crewActor()).Scan(&target); err != nil {
		t.Fatalf("find the patch action: %v", err)
	}
	if !strings.HasSuffix(target, "README.md") {
		t.Fatalf("the patch action's target is %q; it should name the file the patch edits", target)
	}
	// Every non-thinking action of a finished call carries its outcome.
	if n := f.count(t, `SELECT COUNT(*) FROM action WHERE tool <> ? AND (ok IS NULL OR duration_ms IS NULL)`,
		timeline.ToolThinking); n != 0 {
		t.Fatalf("%d action(s) have no result; the fixtures' calls all completed", n)
	}
}

// A status line has no timestamp. The crew's own `echo` is in its transcript
// with one, and that is the rule: dating every line by the file's mtime would
// put the question after the answer the moment the crew wrote anything else.
func TestStatusLinesAreDatedFromTheCrewsOwnShellCommand(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)

	rows, err := f.db.SQL().Query(
		`SELECT at, json_extract(payload, '$.verb'), json_extract(payload, '$.dated_by')
		   FROM event WHERE actor_id = ? AND kind = ? ORDER BY ref_offset`,
		f.crewActor(), timeline.KindStatusAppend)
	if err != nil {
		t.Fatalf("read status events: %v", err)
	}
	defer rows.Close()
	type line struct{ at, verb, dated string }
	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.at, &l.verb, &l.dated); err != nil {
			t.Fatalf("scan: %v", err)
		}
		lines = append(lines, l)
	}
	if len(lines) != 3 {
		t.Fatalf("%d status event(s), want one per line in the file", len(lines))
	}
	for i, l := range lines {
		if l.dated != "transcript.shell" {
			t.Fatalf("status line %d was dated by %q, want the crew's own shell command", i, l.dated)
		}
		if i > 0 && !(lines[i-1].at < l.at) {
			t.Fatalf("status line %d is at %s, not after line %d at %s", i, l.at, i-1, lines[i-1].at)
		}
	}
	for i, verb := range []string{"working", "needs-decision", "wait-mate"} {
		if lines[i].verb != verb {
			t.Fatalf("status line %d reads %q, want %q", i, lines[i].verb, verb)
		}
	}
}

// The question the crew asked is linked to the line that answered it, with
// the wait measured between them - which is the number M5's third question
// ("thời gian chờ ở cửa CEO") is asked in.
func TestTheQuestionIsLinkedToTheAnswerWithThePositiveWaitBetweenThem(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)

	var text, askedAt, answeredAt, answeredBy string
	var waited int64
	if err := f.db.SQL().QueryRow(
		`SELECT text, asked_at, answered_at, answered_by_actor_id, waited_ms
		   FROM question WHERE crew_actor_id = ?`, f.crewActor()).
		Scan(&text, &askedAt, &answeredAt, &answeredBy, &waited); err != nil {
		t.Fatalf("read the question: %v", err)
	}
	if text != "what is the checkout page URL for the Buy button?" {
		t.Fatalf("the question reads %q", text)
	}
	if answeredBy != f.mateActor() {
		t.Fatalf("the question was answered by %q, want the Mate", answeredBy)
	}
	if waited <= 0 {
		t.Fatalf("waited_ms = %d; a question answered after it was asked waits a positive time", waited)
	}
	// 10:44:33.279 (the crew's echo) to 10:46:00 (the Mate's `mate send`,
	// which its own transcript shows finishing at 10:46:00.581).
	if want := int64(86721); waited != want {
		t.Fatalf("waited_ms = %d, want %d", waited, want)
	}
	if !(askedAt < answeredAt) {
		t.Fatalf("the question is at %s and the answer at %s", askedAt, answeredAt)
	}
	if n := f.count(t, `SELECT question_count FROM task WHERE crew_actor_id = ?`, f.crewActor()); n != 1 {
		t.Fatalf("the task counts %d question(s), want 1", n)
	}
	if n := f.count(t, `SELECT handback_count FROM task WHERE crew_actor_id = ?`, f.crewActor()); n != 1 {
		t.Fatalf("the task counts %d handback(s), want 1", n)
	}
}

// The causality chain of M5's first question: crew asks → the captain hands
// it over → the Mate's turn → the answer → the crew's next turn. Each edge is
// derived, not inferred from two events being close in time.
func TestTheCausalChainFromQuestionToAnswerIsLinked(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)
	events := f.story(t)
	byKind := map[string][]timeline.StoryEvent{}
	for _, e := range events {
		byKind[e.Kind] = append(byKind[e.Kind], e)
	}

	asked := only(t, byKind, timeline.KindQuestionAsked)
	if asked.CauseKind != timeline.KindTurnEnded {
		t.Fatalf("question.asked is caused by %q, want the end of the turn the crew asked in", asked.CauseKind)
	}
	assign := only(t, byKind, timeline.KindAssignClicked)
	if assign.Cause != asked.ID {
		t.Fatalf("assign.clicked is caused by event %d, want the question %d it carries", assign.Cause, asked.ID)
	}
	answered := only(t, byKind, timeline.KindQuestionAnsw)
	if answered.CauseKind != timeline.KindTurnStarted {
		t.Fatalf("question.answered is caused by %q, want the Mate turn that ran mate send", answered.CauseKind)
	}
	spawned := only(t, byKind, timeline.KindCrewSpawned)
	if spawned.CauseKind != timeline.KindTurnStarted {
		t.Fatalf("crew.spawned is caused by %q, want the Mate turn that ran crew spawn", spawned.CauseKind)
	}

	// The crew's first turn after the answer is caused by the answer.
	var linked bool
	for _, e := range byKind[timeline.KindTurnStarted] {
		if e.ActorKind == timeline.ActorCrew && e.Cause == answered.ID {
			linked = true
			break
		}
	}
	if !linked {
		t.Fatal("no crew turn is caused by the answer; the crew took the answer as a new prompt")
	}

	// A Mate turn is caused by the line that reached its composer.
	var mateTriggered int
	for _, e := range byKind[timeline.KindTurnStarted] {
		if e.ActorKind != timeline.ActorMate {
			continue
		}
		switch e.CauseKind {
		case timeline.KindMessageSent, timeline.KindAssignClicked, timeline.KindDigestSent:
			mateTriggered++
		}
	}
	if mateTriggered == 0 {
		t.Fatal("no Mate turn names the line that triggered it")
	}
}

// No busy stretch is left unexplained: a gap inside a turn with no tool call
// in it becomes an action with the tool `thinking`.
func TestAGapInsideATurnBecomesAThinkingAction(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)

	if n := f.count(t, `SELECT COUNT(*) FROM action WHERE tool = ?`, timeline.ToolThinking); n == 0 {
		t.Fatal("the fixture has turns with long gaps between tool calls and no thinking action was recorded")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM action WHERE tool = ? AND duration_ms < 5000`,
		timeline.ToolThinking); n != 0 {
		t.Fatalf("%d thinking action(s) are shorter than the gap threshold", n)
	}
	// Each one belongs to the turn whose gap it fills, so a reader of a turn
	// sees its whole span accounted for.
	if n := f.count(t, `SELECT COUNT(*) FROM action WHERE tool = ? AND turn_id IS NULL`,
		timeline.ToolThinking); n != 0 {
		t.Fatalf("%d thinking action(s) belong to no turn", n)
	}
}

// Ingesting twice changes nothing: every fact carries a natural key, so a
// pass that re-reads a source it has already read writes no second row.
func TestIngestingTwiceRecordsNothingNew(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)
	before := f.story(t)
	counts := map[string]int{}
	for _, table := range []string{"event", "turn", "action", "message", "question", "usage_sample", "actor"} {
		counts[table] = f.count(t, `SELECT COUNT(*) FROM `+table)
	}

	f.ingest(t)
	for table, want := range counts {
		if got := f.count(t, `SELECT COUNT(*) FROM `+table); got != want {
			t.Fatalf("a second ingest changed %s from %d rows to %d", table, want, got)
		}
	}
	after := f.story(t)
	if len(before) != len(after) {
		t.Fatalf("the story grew from %d events to %d without the files changing", len(before), len(after))
	}
	for i := range before {
		if lineOf(t, before[i]) != lineOf(t, after[i]) {
			t.Fatalf("event %d changed between two ingests:\n%s\n%s", i, lineOf(t, before[i]), lineOf(t, after[i]))
		}
	}
}

// `mate reindex` drops every derived table and rebuilds from the files, and
// two rebuilds are byte-identical - including the `event.id`s, which is what
// lets a reader quote one.
func TestReindexingTwiceProducesAByteIdenticalStory(t *testing.T) {
	f := newFixture(t)
	if err := f.ing.Reindex(context.Background()); err != nil {
		t.Fatalf("first reindex: %v", err)
	}
	first := storyLines(t, f)
	if len(first) == 0 {
		t.Fatal("the first reindex produced no story")
	}
	if err := f.ing.Reindex(context.Background()); err != nil {
		t.Fatalf("second reindex: %v", err)
	}
	second := storyLines(t, f)
	if strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Fatalf("two reindexes of the same files produced different stories\nfirst %d line(s), second %d",
			len(first), len(second))
	}
	if first[0] == "" || !strings.Contains(first[0], `"id":1`) {
		t.Fatalf("the first event of a rebuild is %q; a rebuild must start the ids again at 1", first[0])
	}
}

// A rebuild that fails leaves the timeline it started with, not an empty one:
// the drop and the refill are one transaction.
func TestAFailedReindexLeavesThePreviousTimelineInPlace(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)
	before := len(f.story(t))
	if before == 0 {
		t.Fatal("the first ingest produced no story")
	}

	// A `crews/` directory that cannot be listed fails the pass. The
	// permissions are restored by the cleanup t.TempDir does.
	crews := f.ws.CrewsDir(fixtureProject)
	if err := os.Chmod(crews, 0o000); err != nil {
		t.Skipf("cannot make %s unreadable on this filesystem: %v", crews, err)
	}
	defer func() { _ = os.Chmod(crews, 0o755) }()

	if err := f.ing.Reindex(context.Background()); err == nil {
		t.Fatal("a reindex over an unreadable crews directory reported success")
	}
	if after := len(f.story(t)); after != before {
		t.Fatalf("the story went from %d events to %d after a failed reindex", before, after)
	}
}

// The narrated story is the readable half of M5: a person who has never seen
// mate should follow it. The golden is the whole run, so a phrase that
// changes shows up as a diff rather than as a sentence nobody reads.
func TestNarrateGolden(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)
	got := f.narrate(t)

	golden := "testdata/narrate.golden"
	if os.Getenv("MATE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (re-run with MATE_UPDATE_GOLDEN=1 to create it)", err)
	}
	if got != string(want) {
		t.Fatalf("the narrated story changed.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// A crew's status line has no time of its own: it borrows the time of the
// transcript command that echoed it, so the two tie to the nanosecond. The
// tie used to be broken by comparing the two files' absolute paths, which put
// "reports" before or after the "runs: echo" that wrote it depending on
// where the workspace and the transcript lived (found 2026-10-01: the golden
// failed with TMPDIR inside the checkout). The line now sorts at the
// command's byte, so the story is the same wherever its files are.
func TestNarrateOrderDoesNotDependOnWhereTheFilesLive(t *testing.T) {
	want, err := os.ReadFile("testdata/narrate.golden")
	if err != nil {
		t.Fatal(err)
	}
	// '!' sorts before the workspace's ".mate" and '~' after it. The root is
	// resolved because the workspace's own paths are (macOS /var is
	// /private/var), so the two spellings share a prefix.
	for _, dir := range []string{"!transcripts", "~transcripts"} {
		t.Run(dir, func(t *testing.T) {
			f := newFixture(t)
			root, err := filepath.EvalSymlinks(f.root)
			if err != nil {
				t.Fatal(err)
			}
			moved := filepath.Join(root, dir)
			if err := os.MkdirAll(moved, 0o755); err != nil {
				t.Fatal(err)
			}
			mate, err := f.ws.ReadMateMeta(fixtureProject)
			if err != nil {
				t.Fatal(err)
			}
			mate[timeline.MetaTranscript] = filepath.Join(moved, filepath.Base(claudeFixture))
			copyFile(t, abs(t, claudeFixture), mate[timeline.MetaTranscript])
			if err := f.ws.WriteMateMeta(fixtureProject, mate); err != nil {
				t.Fatal(err)
			}
			crew, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
			if err != nil {
				t.Fatal(err)
			}
			crew[timeline.MetaTranscript] = filepath.Join(moved, filepath.Base(codexFixture))
			copyFile(t, abs(t, codexFixture), crew[timeline.MetaTranscript])
			if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, crew); err != nil {
				t.Fatal(err)
			}
			f.ingest(t)
			if got := f.narrate(t); got != string(want) {
				t.Fatalf("the story changed with its transcripts under %s.\n--- got ---\n%s\n--- want ---\n%s", dir, got, want)
			}
		})
	}
}

// v_task_ledger is task 27's surface, so its shape is fixed now: one row per
// task, tokens by bucket, and a cost that is NULL until `pricing` has a row
// for the model rather than zero.
func TestTaskLedgerCountsTheTaskAndLeavesCostNullUntilThereIsAPrice(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)

	var crew, model string
	var turns, input, cacheRead, output, questions, handbacks, waited int64
	var context sql.NullInt64
	var cost sql.NullFloat64
	if err := f.db.SQL().QueryRow(
		`SELECT l.crew, l.turns, l.input_tokens, l.cache_read_tokens, l.output_tokens,
		        l.question_count, l.handback_count, l.waited_ms, l.context_tokens_last, l.cost,
		        (SELECT u.model FROM turn u WHERE u.actor_id = l.crew_actor_id LIMIT 1)
		   FROM v_task_ledger l`).
		Scan(&crew, &turns, &input, &cacheRead, &output, &questions, &handbacks, &waited, &context, &cost, &model); err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	if crew != fixtureCrew || turns != codexTurns {
		t.Fatalf("the ledger reads crew %q with %d turn(s)", crew, turns)
	}
	if input != codexTotalInput || cacheRead != codexTotalCached || output != codexTotalOutput {
		t.Fatalf("the ledger's tokens are %d/%d/%d", input, cacheRead, output)
	}
	if questions != 1 || handbacks != 1 || waited <= 0 {
		t.Fatalf("the ledger reads %d question(s), %d handback(s), %dms waited", questions, handbacks, waited)
	}
	if !context.Valid || context.Int64 != codexLastContext {
		t.Fatalf("the ledger's last context is %v", context)
	}
	if cost.Valid {
		t.Fatalf("the ledger priced a task with no row in `pricing`: %v", cost.Float64)
	}

	// INSERT OR REPLACE, not a plain INSERT: task 27's ingest already seeded
	// a placeholder row for every model `.mate/pricing.yaml` names at
	// init, priced at 0 (which is why the assertion above still saw a NULL
	// cost - a price of 0 does not count as priced). This overwrites that
	// placeholder with a real price, which is what a captain editing the
	// file and mate reloading it would produce.
	if _, err := f.db.SQL().Exec(
		`INSERT OR REPLACE INTO pricing(model, input_per_m, cache_read_per_m, cache_write_per_m, output_per_m)
		 VALUES (?, 1000000, 0, 0, 0)`, model); err != nil {
		t.Fatalf("seed pricing: %v", err)
	}
	if err := f.db.SQL().QueryRow(`SELECT cost FROM v_task_ledger`).Scan(&cost); err != nil {
		t.Fatalf("read the cost: %v", err)
	}
	if !cost.Valid || int64(cost.Float64) != codexTotalInput {
		t.Fatalf("with a price of one unit per input token the cost is %v, want %d", cost, codexTotalInput)
	}
}

// task 27: `.mate/pricing.yaml` loads into the `pricing` table, and a
// second edit-then-ingest cycle updates the same row rather than adding a
// second one - the upsert docs/timeline.md's Economics section promises.
func TestIngestPricingLoadsAndUpdatesFromPricingYAML(t *testing.T) {
	f := newFixture(t)

	if err := f.ws.SavePricing(store.PricingConfig{Models: []store.PricingModel{
		{Model: "a-test-model", InputPerM: 3, OutputPerM: 15, ContextWindow: 1000},
	}}); err != nil {
		t.Fatalf("SavePricing: %v", err)
	}
	f.ingest(t)

	var input, output float64
	var window int64
	if err := f.db.SQL().QueryRow(
		`SELECT input_per_m, output_per_m, context_window FROM pricing WHERE model = ?`,
		"a-test-model").Scan(&input, &output, &window); err != nil {
		t.Fatalf("read pricing: %v", err)
	}
	if input != 3 || output != 15 || window != 1000 {
		t.Fatalf("pricing row = (%v, %v, %v), want (3, 15, 1000)", input, output, window)
	}

	// The captain edits the file; the next ingest must update the row it
	// already wrote, not add a second one.
	if err := f.ws.SavePricing(store.PricingConfig{Models: []store.PricingModel{
		{Model: "a-test-model", InputPerM: 6, OutputPerM: 30, ContextWindow: 2000},
	}}); err != nil {
		t.Fatalf("SavePricing (update): %v", err)
	}
	f.ingest(t)

	if n := f.count(t, `SELECT COUNT(*) FROM pricing WHERE model = ?`, "a-test-model"); n != 1 {
		t.Fatalf("%d row(s) for a-test-model after a second ingest, want exactly 1 (upsert, not insert)", n)
	}
	if err := f.db.SQL().QueryRow(
		`SELECT input_per_m, output_per_m, context_window FROM pricing WHERE model = ?`,
		"a-test-model").Scan(&input, &output, &window); err != nil {
		t.Fatalf("read pricing after update: %v", err)
	}
	if input != 6 || output != 30 || window != 2000 {
		t.Fatalf("pricing row after update = (%v, %v, %v), want (6, 30, 2000)", input, output, window)
	}
}

// A workspace with no pricing.yaml at all - store.Init not run, or the file
// removed by hand - is not an ingest error: every model is simply unpriced.
func TestIngestPricingToleratesNoFileAtAll(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(f.ws.PricingFile()); err != nil {
		t.Fatalf("remove pricing.yaml: %v", err)
	}
	f.ingest(t)
	if n := f.count(t, `SELECT COUNT(*) FROM pricing`); n != 0 {
		t.Fatalf("%d pricing row(s) with no pricing.yaml, want 0", n)
	}
}

// v_task_ledger's context_pct and v_now's context_pct both read the seeded
// pricing.yaml's context_window (store.Init seeds gpt-5.6-terra at 400000,
// matching the fixture's codex model) even though that model's price is
// still the placeholder 0 - context size is a technical fact, not something
// priced, so it is usable before the captain has entered a single price.
func TestContextPctUsesTheSeededContextWindowBeforeAnyPriceIsSet(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)

	want := 100.0 * float64(codexLastContext) / 400000.0

	var ledgerPct sql.NullFloat64
	if err := f.db.SQL().QueryRow(`SELECT context_pct FROM v_task_ledger`).Scan(&ledgerPct); err != nil {
		t.Fatalf("read v_task_ledger.context_pct: %v", err)
	}
	if !ledgerPct.Valid || diff(ledgerPct.Float64, want) > 0.01 {
		t.Fatalf("v_task_ledger.context_pct = %v, want ~%.4f", ledgerPct, want)
	}

	var nowPct sql.NullFloat64
	if err := f.db.SQL().QueryRow(`SELECT context_pct FROM v_now WHERE actor_id = ?`, f.crewActor()).
		Scan(&nowPct); err != nil {
		t.Fatalf("read v_now.context_pct: %v", err)
	}
	if !nowPct.Valid || diff(nowPct.Float64, want) > 0.01 {
		t.Fatalf("v_now.context_pct = %v, want ~%.4f", nowPct, want)
	}
}

func diff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

// A transcript the locator cannot find is an event, not silence: every turn,
// token and tool call of that agent is missing, and the timeline has to say
// so once rather than look empty.
func TestAnUnresolvedTranscriptIsRecordedOnce(t *testing.T) {
	f := newFixture(t)
	meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
	if err != nil {
		t.Fatalf("ReadCrewMeta: %v", err)
	}
	meta["transcript"] = ""
	meta["session_id"] = ""
	if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
	f.ingest(t)
	f.ingest(t)

	rows := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ? AND actor_id = ?`,
		timeline.KindIngestUnresolved, f.crewActor())
	if rows != 1 {
		t.Fatalf("%d ingest.unresolved event(s) for the crew, want exactly one", rows)
	}
	var reason string
	if err := f.db.SQL().QueryRow(
		`SELECT json_extract(payload, '$.reason') FROM event WHERE kind = ? AND actor_id = ?`,
		timeline.KindIngestUnresolved, f.crewActor()).Scan(&reason); err != nil {
		t.Fatalf("read the reason: %v", err)
	}
	if reason == "" {
		t.Fatal("the unresolved event names no reason")
	}
}

// The meta keys this package spells are internal/spawn's. They are spelled
// twice because internal/watch calls the ingest and may import nothing that
// can start an agent; this is what keeps the two spellings equal.
func TestMetaKeysMatchSpawn(t *testing.T) {
	for _, pair := range []struct{ timelineKey, spawnKey string }{
		{timeline.MetaHarness, spawn.MetaHarness},
		{timeline.MetaAgent, spawn.MetaAgent},
		{timeline.MetaSessionID, spawn.MetaSessionID},
		{timeline.MetaTranscript, spawn.MetaTranscript},
		{timeline.MetaStartedAt, spawn.MetaStartedAt},
		{timeline.MetaLaunchedAt, spawn.MetaLaunchedAt},
		{timeline.MetaStoppedAt, spawn.MetaStoppedAt},
		{timeline.MetaResumedFrom, spawn.MetaResumedFrom},
		{timeline.MetaTask, spawn.MetaTask},
		{timeline.MetaWorktree, spawn.MetaWorktree},
		{timeline.MetaBranch, spawn.MetaBranch},
		{timeline.MetaState, spawn.MetaState},
		{timeline.MetaFailedReason, spawn.MetaFailedReason},
	} {
		if pair.timelineKey != pair.spawnKey {
			t.Fatalf("timeline spells a meta key %q where spawn writes %q", pair.timelineKey, pair.spawnKey)
		}
	}
}

// `--since` over an event id is what a live reader follows with, and it must
// return the events after that id and no others.
func TestStorySinceAnIDReturnsOnlyWhatFollowsIt(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)
	all := f.story(t)
	if len(all) < 3 {
		t.Fatalf("the fixture produced %d events", len(all))
	}
	cut := all[len(all)/2]
	rest, err := timeline.Story(context.Background(), f.db.SQL(),
		timeline.StoryQuery{Project: fixtureProject, SinceID: cut.ID})
	if err != nil {
		t.Fatalf("Story: %v", err)
	}
	for _, e := range rest {
		if e.ID <= cut.ID {
			t.Fatalf("--since %d returned event %d", cut.ID, e.ID)
		}
	}
	if len(rest) != len(all)-indexOf(all, cut.ID)-1 {
		t.Fatalf("--since %d returned %d event(s)", cut.ID, len(rest))
	}
}

func indexOf(events []timeline.StoryEvent, id int64) int {
	for i, e := range events {
		if e.ID == id {
			return i
		}
	}
	return -1
}

func only(t *testing.T, byKind map[string][]timeline.StoryEvent, kind string) timeline.StoryEvent {
	t.Helper()
	events := byKind[kind]
	if len(events) != 1 {
		t.Fatalf("%d %s event(s), want exactly one", len(events), kind)
	}
	return events[0]
}

func lineOf(t *testing.T, e timeline.StoryEvent) string {
	t.Helper()
	line, err := e.JSONLine()
	if err != nil {
		t.Fatalf("JSONLine: %v", err)
	}
	return line
}

func storyLines(t *testing.T, f *fixture) []string {
	t.Helper()
	var out []string
	for _, e := range f.story(t) {
		// The workspace root is a temporary directory, so the paths a story
		// carries differ between runs of the test but not between two
		// rebuilds inside one. They are compared as they are.
		out = append(out, lineOf(t, e))
	}
	return out
}

func TestReindexPreservesSpendFromARefreshedMateSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.ing.Ingest(ctx); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := f.db.SQL().QueryRow(`SELECT sum(input_tokens+cache_read_tokens+cache_write_tokens+output_tokens) FROM turn WHERE actor_id=?`, timeline.MateActorID(fixtureProject)).Scan(&before); err != nil {
		t.Fatal(err)
	}
	meta, err := f.ws.ReadMateMeta(fixtureProject)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ws.ArchiveMateSession(fixtureProject, meta); err != nil {
		t.Fatal(err)
	}
	meta["session_id"] = "new-empty-session"
	delete(meta, "transcript")
	if err := f.ws.WriteMateMeta(fixtureProject, meta); err != nil {
		t.Fatal(err)
	}
	if err := f.ing.Reindex(ctx); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := f.db.SQL().QueryRow(`SELECT sum(input_tokens+cache_read_tokens+cache_write_tokens+output_tokens) FROM turn WHERE actor_id=?`, timeline.MateActorID(fixtureProject)).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before == 0 || after != before {
		t.Fatalf("refresh lost spend: %d -> %d", before, after)
	}
}

func TestFrozenRefreshSnapshotCountsTheFinalCallExactlyOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.ing.Ingest(ctx); err != nil {
		t.Fatal(err)
	}
	var before int
	actor := timeline.MateActorID(fixtureProject)
	if err := f.db.SQL().QueryRow(`SELECT count(*) FROM turn WHERE actor_id=?`, actor).Scan(&before); err != nil {
		t.Fatal(err)
	}
	meta, err := f.ws.ReadMateMeta(fixtureProject)
	if err != nil {
		t.Fatal(err)
	}
	// Fixture transcript is a static captured file, satisfying the at-rest precondition.
	if err := f.ws.FreezeMateSession(fixtureProject, meta); err != nil {
		t.Fatal(err)
	}
	meta["session_id"] = "fresh-empty"
	delete(meta, "transcript")
	if err := f.ws.WriteMateMeta(fixtureProject, meta); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.ing.Reindex(ctx); err != nil {
			t.Fatal(err)
		}
		var after int
		if err := f.db.SQL().QueryRow(`SELECT count(*) FROM turn WHERE actor_id=?`, actor).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before+1 {
			t.Fatalf("final group: before=%d after=%d", before, after)
		}
	}
}
