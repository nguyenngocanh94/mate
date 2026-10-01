package harness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Claude must satisfy the ingest port; a signature drift should fail here
// rather than at the caller.
var _ TranscriptParser = Claude{}

// claudeSplitMessageFixture is a synthetic transcript, never a real one: every
// record is hand-written to the shapes measured on a real Claude session. One
// assistant API response (msg_1) is deliberately written as three records, one
// per content block, exactly as Claude does; the mode/file-history/attachment
// records stand in for the housekeeping volume that must not be copied, and
// future-thing stands in for an unknown type a later Claude version might add.
const claudeSplitMessageFixture = `{"type":"mode","mode":"normal","sessionId":"s","uuid":"h1","timestamp":"2026-09-08T10:00:00.000Z"}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:00:01.000Z","cwd":"/w","sessionId":"s","message":{"id":"msg_1","model":"claude-sonnet-4","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":3,"cache_creation_input_tokens":4,"output_tokens_details":{"thinking_tokens":7}},"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"hello"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-09-08T10:00:01.100Z","cwd":"/w","sessionId":"s","message":{"id":"msg_1","model":"claude-sonnet-4","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":3,"cache_creation_input_tokens":4,"output_tokens_details":{"thinking_tokens":7}},"content":[{"type":"text","text":"world"}]}}
{"type":"assistant","uuid":"a3","timestamp":"2026-09-08T10:00:01.200Z","cwd":"/w","sessionId":"s","message":{"id":"msg_1","model":"claude-sonnet-4","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":3,"cache_creation_input_tokens":4,"output_tokens_details":{"thinking_tokens":7}},"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","uuid":"u1","timestamp":"2026-09-08T10:00:02.000Z","cwd":"/w","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file.txt"}]}}
{"type":"user","uuid":"u2","timestamp":"2026-09-08T10:00:03.000Z","cwd":"/w","message":{"role":"user","content":"fix it"}}
{"type":"file-history-snapshot","uuid":"h2","timestamp":"2026-09-08T10:00:03.500Z","snapshot":{}}
{"type":"attachment","uuid":"h3","timestamp":"2026-09-08T10:00:03.600Z","attachment":{"type":"file"}}
{"type":"future-thing","uuid":"h4","timestamp":"2026-09-08T10:00:03.700Z","payload":{"x":1}}
{"type":"agent-name","uuid":"h5","timestamp":"2026-09-08T10:00:03.800Z","name":"mate"}
{"type":"custom-title","uuid":"h6","timestamp":"2026-09-08T10:00:03.900Z","title":"a title"}
{"type":"system","uuid":"s1","timestamp":"2026-09-08T10:00:04.000Z","subtype":"turn_duration","durationMs":5}
`

// parseFinalFixture parses a whole synthetic fixture. A fixture is a file at
// rest, so the whole-file tests use the final contract; the incremental
// contract withholds the trailing message group and is covered on its own.
func parseFinalFixture(t *testing.T, fixture string) TranscriptBatch {
	t.Helper()
	return Claude{}.ParseTranscriptFinal(TranscriptParseState{}, []byte(fixture))
}

func parseIncrementalFixture(t *testing.T, fixture string) TranscriptBatch {
	t.Helper()
	return Claude{}.ParseTranscript(TranscriptParseState{}, []byte(fixture))
}

// lineOffsets returns the byte offset of each newline-terminated line.
func lineOffsets(data []byte) []int64 {
	var offs []int64
	var off int64
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		offs = append(offs, off)
		off += int64(len(line))
	}
	return offs
}

func TestClaudeParseTranscriptGroupsSplitMessageIntoOneTurnWithOneUsage(t *testing.T) {
	batch := parseFinalFixture(t, claudeSplitMessageFixture)

	if batch.Source != TranscriptClaude {
		t.Fatalf("source = %q, want %q", batch.Source, TranscriptClaude)
	}
	if len(batch.Turns) != 1 {
		t.Fatalf("turns = %d, want 1: one API response must be one turn despite spanning %d records", len(batch.Turns), 4)
	}
	turn := batch.Turns[0]
	if turn.SourceRef != "msg_1" {
		t.Errorf("turn source ref = %q, want msg_1", turn.SourceRef)
	}
	// Usage is identical on all three records; counting it once is the point.
	wantUsage := TokenUsage{Input: 10, Output: 20, CacheRead: 3, CacheWrite: 4, Reasoning: 7}
	if turn.Usage != wantUsage {
		t.Errorf("usage = %+v, want %+v (must not be summed across the message's records)", turn.Usage, wantUsage)
	}
	if got := turn.Usage.ContextTokens(); got != 17 {
		t.Errorf("context tokens = %d, want 17 (input + cache read + cache write)", got)
	}
	if turn.Text != "hello\nworld" {
		t.Errorf("turn text = %q, want %q", turn.Text, "hello\nworld")
	}
	if turn.Model != "claude-sonnet-4" {
		t.Errorf("model = %q", turn.Model)
	}
	if turn.StopReason != "end_turn" {
		t.Errorf("stop reason = %q, want end_turn (only the record carrying it)", turn.StopReason)
	}
	if turn.Ordinal != 1 || turn.Offset != lineOffsets([]byte(claudeSplitMessageFixture))[1] {
		t.Errorf("turn position = (ordinal %d, offset %d), want (1, %d)",
			turn.Ordinal, turn.Offset, lineOffsets([]byte(claudeSplitMessageFixture))[1])
	}
	if !turn.OccurredAt.Equal(mustTime(t, "2026-09-08T10:00:01Z")) {
		t.Errorf("turn time = %s, want the first record's timestamp", turn.OccurredAt)
	}
	if turn.ContextReset {
		t.Error("context reset must stay false: how Claude marks a reset is unproven")
	}
	if turn.DurationMS != nil {
		t.Errorf("duration = %v, want nil (not derivable from a Claude transcript)", *turn.DurationMS)
	}

	if len(batch.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(batch.ToolCalls))
	}
	call := batch.ToolCalls[0]
	if call.SourceRef != "toolu_1" || call.TurnSourceRef != "msg_1" {
		t.Errorf("call refs = (%q, %q)", call.SourceRef, call.TurnSourceRef)
	}
	if call.ToolName != "Bash" || call.CommandClass != CommandShell {
		t.Errorf("call = (%q, %q)", call.ToolName, call.CommandClass)
	}
	if call.InputJSON != `{"command":"ls"}` {
		t.Errorf("call input = %s", call.InputJSON)
	}
	if !call.StartedAt.Equal(mustTime(t, "2026-09-08T10:00:01.2Z")) {
		t.Errorf("call started at = %s, want the timestamp of the record the block appeared in", call.StartedAt)
	}

	if len(batch.ToolResults) != 1 {
		t.Fatalf("tool results = %d, want 1", len(batch.ToolResults))
	}
	result := batch.ToolResults[0]
	if result.CallID != "toolu_1" || result.SourceRef != "u1" {
		t.Errorf("result refs = (call %q, source %q)", result.CallID, result.SourceRef)
	}
	if result.OutputText != "file.txt" || result.OutputBytes != 8 {
		t.Errorf("result output = (%q, %d bytes)", result.OutputText, result.OutputBytes)
	}
	if result.IsError {
		t.Error("is_error was absent and must mean false, not true")
	}
}

func TestClaudeParseTranscriptCopiesOnlyAgentRecordsAndCountsSkipped(t *testing.T) {
	batch := parseFinalFixture(t, claudeSplitMessageFixture)

	var sources []string
	for _, rec := range batch.Records {
		sources = append(sources, rec.SourceRef)
	}
	want := []string{"a1", "a2", "a3", "u1", "u2", "s1"}
	if strings.Join(sources, ",") != strings.Join(want, ",") {
		t.Errorf("record refs = %v, want %v", sources, want)
	}

	wantKinds := []TranscriptKind{
		TranscriptKindAssistant, TranscriptKindAssistant, TranscriptKindAssistant,
		TranscriptKindToolResult, TranscriptKindUser, TranscriptKindSystem,
	}
	for i, rec := range batch.Records {
		if rec.Kind != wantKinds[i] {
			t.Errorf("record %d (%s) kind = %q, want %q", i, rec.SourceRef, rec.Kind, wantKinds[i])
		}
	}
	// A user record carrying a tool_result is a tool_result record; a plain
	// user prompt stays user.
	if batch.Records[3].Kind != TranscriptKindToolResult || batch.Records[4].Kind != TranscriptKindUser {
		t.Errorf("user/tool_result classification wrong: %q, %q", batch.Records[3].Kind, batch.Records[4].Kind)
	}

	wantSkipped := map[string]int{
		"mode": 1, "file-history-snapshot": 1, "attachment": 1, "future-thing": 1,
		// Both were observed in a 330-session census of CLI 2.1.269 and were
		// missing from an earlier, smaller sample: a type the parser has not
		// seen must still be skipped and counted rather than guessed at.
		"agent-name": 1, "custom-title": 1,
	}
	if len(batch.Skipped) != len(wantSkipped) {
		t.Fatalf("skipped = %v, want %v", batch.Skipped, wantSkipped)
	}
	for typ, n := range wantSkipped {
		if batch.Skipped[typ] != n {
			t.Errorf("skipped[%q] = %d, want %d", typ, batch.Skipped[typ], n)
		}
	}
	if batch.Malformed != nil {
		t.Fatalf("malformed = %+v, want nil (housekeeping and unknown types are skipped, not fatal)", batch.Malformed)
	}
}

func TestClaudeParseTranscriptCopiesRecordsVerbatimWithAbsolutePosition(t *testing.T) {
	data := []byte(claudeSplitMessageFixture)
	batch := parseFinalFixture(t, claudeSplitMessageFixture)
	offsets := lineOffsets(data)

	if batch.ConsumedBytes != int64(len(data)) || batch.PendingBytes != 0 {
		t.Fatalf("consumed/pending = (%d, %d), want (%d, 0)", batch.ConsumedBytes, batch.PendingBytes, len(data))
	}
	if batch.TotalBytes != int64(len(data)) {
		t.Fatalf("total bytes = %d, want %d", batch.TotalBytes, len(data))
	}

	lines := strings.Split(strings.TrimSuffix(claudeSplitMessageFixture, "\n"), "\n")
	for _, rec := range batch.Records {
		if rec.RawJSON != lines[rec.Ordinal] {
			t.Errorf("record %s raw JSON is not the verbatim line %d", rec.SourceRef, rec.Ordinal)
		}
		if rec.Offset != offsets[rec.Ordinal] {
			t.Errorf("record %s offset = %d, want %d", rec.SourceRef, rec.Offset, offsets[rec.Ordinal])
		}
		if rec.OccurredAt.IsZero() {
			t.Errorf("record %s has no timestamp", rec.SourceRef)
		}
	}
}

func TestClaudeParseTranscriptHousekeepingOnlyYieldsNoFactsAndNoZeros(t *testing.T) {
	batch := parseFinalFixture(t, `{"type":"attachment","uuid":"h1"}
{"type":"file-history-delta","uuid":"h2","delta":{}}
`)

	if len(batch.Records) != 0 || len(batch.Turns) != 0 || len(batch.ToolCalls) != 0 || len(batch.ToolResults) != 0 {
		t.Fatalf("facts = (%d records, %d turns, %d calls, %d results), want all zero: an unreadable transcript must not become zeros",
			len(batch.Records), len(batch.Turns), len(batch.ToolCalls), len(batch.ToolResults))
	}
	if batch.Malformed != nil {
		t.Fatalf("malformed = %+v, want nil", batch.Malformed)
	}
	if batch.Skipped["attachment"] != 1 || batch.Skipped["file-history-delta"] != 1 {
		t.Errorf("skipped = %v", batch.Skipped)
	}
}

// claudeIncrementalFixture is one session tail in the order Claude actually
// writes it:
//
//	0  the user prompt
//	1  msg_1's first content block
//	2  an attachment between two records of the same message - measured:
//	   housekeeping is interleaved, so a group is not a contiguous run
//	3  msg_1's second block, carrying the message's tool_use
//	4  the tool_result answering that call
//	5  msg_2, the next turn, which is what closes msg_1
const claudeIncrementalFixture = `{"type":"user","uuid":"u1","timestamp":"2026-09-08T10:00:00.000Z","cwd":"/w","message":{"role":"user","content":"do the thing"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:00:01.000Z","cwd":"/w","message":{"id":"msg_1","model":"claude-sonnet-4","stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":3,"cache_creation_input_tokens":4,"output_tokens_details":{"thinking_tokens":7}},"content":[{"type":"text","text":"first half"}]}}
{"type":"attachment","uuid":"h1","timestamp":"2026-09-08T10:00:01.050Z","attachment":{"type":"file"}}
{"type":"assistant","uuid":"a2","timestamp":"2026-09-08T10:00:01.100Z","cwd":"/w","message":{"id":"msg_1","model":"claude-sonnet-4","stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":3,"cache_creation_input_tokens":4,"output_tokens_details":{"thinking_tokens":7}},"content":[{"type":"text","text":"second half"},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-09-08T10:00:02.000Z","cwd":"/w","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file.txt"}]}}
{"type":"assistant","uuid":"b1","timestamp":"2026-09-08T10:00:03.000Z","cwd":"/w","message":{"id":"msg_2","model":"claude-sonnet-4","stop_reason":"end_turn","usage":{"input_tokens":30,"output_tokens":40},"content":[{"type":"text","text":"next turn"}]}}
`

// TestClaudeParseTranscriptSplitMessageAcrossTwoCallsIsOneTurn is the
// regression for the defect that made a message split across two syncs produce
// a second turn for one message id: the parse contract, not a whole-file
// fixture, is what has to hold, because a caller resumes from
// transcript_cursor.byte_offset and hands the parser only the tail.
func TestClaudeParseTranscriptSplitMessageAcrossTwoCallsIsOneTurn(t *testing.T) {
	data := []byte(claudeIncrementalFixture)
	offsets := lineOffsets(data)
	msgOneUsage := TokenUsage{Input: 10, Output: 20, CacheRead: 3, CacheWrite: 4, Reasoning: 7}

	// First sync: the file ends in the middle of msg_1, after its first block.
	first := Claude{}.ParseTranscript(TranscriptParseState{}, data[:offsets[2]])
	if len(first.Turns) != 0 {
		t.Fatalf("first sync emitted %+v, want no turn: msg_1 may still be appended to", first.Turns)
	}
	if first.OpenSourceRef != "msg_1" || first.OpenOffset != offsets[1] {
		t.Fatalf("open group = (%q, %d), want (msg_1, %d)", first.OpenSourceRef, first.OpenOffset, offsets[1])
	}
	// Whatever the parse saw inside the group, the cursor must not pass its
	// first record: resuming from ConsumedBytes re-reads msg_1 whole.
	if first.ConsumedBytes != offsets[1] || first.ConsumedBytes >= offsets[2] {
		t.Fatalf("consumed = %d, want %d (the group's first record, before the bytes this sync read)", first.ConsumedBytes, offsets[1])
	}
	if len(first.Records) != 1 || first.Records[0].SourceRef != "u1" {
		t.Fatalf("records = %+v, want only the user prompt that precedes the group", first.Records)
	}

	// Second sync: the file grew past the end of msg_1 and into msg_2, which
	// is what closes it.
	cursor := first.ConsumedBytes
	second := Claude{}.ParseTranscript(TranscriptParseState{}, data[cursor:])
	if len(second.Turns) != 1 {
		t.Fatalf("turns after resuming = %+v, want exactly one turn for msg_1", second.Turns)
	}
	turn := second.Turns[0]
	if turn.SourceRef != "msg_1" {
		t.Fatalf("turn source ref = %q, want msg_1", turn.SourceRef)
	}
	if turn.Usage != msgOneUsage {
		t.Errorf("usage = %+v, want %+v: the message's records repeat one usage and it is counted once", turn.Usage, msgOneUsage)
	}
	if turn.Text != "first half\nsecond half" {
		t.Errorf("text = %q, want both blocks of the message", turn.Text)
	}
	if len(second.ToolCalls) != 1 || second.ToolCalls[0].SourceRef != "toolu_1" || second.ToolCalls[0].TurnSourceRef != "msg_1" {
		t.Errorf("tool calls = %+v, want msg_1's one call", second.ToolCalls)
	}
	if len(second.ToolResults) != 1 || second.ToolResults[0].CallID != "toolu_1" {
		t.Errorf("tool results = %+v, want the result of msg_1's call", second.ToolResults)
	}
	// Offsets and ordinals are relative to the bytes handed in: a tail read
	// rebases them, and the caller carries its own base.
	if turn.Offset != offsets[1]-cursor || turn.Ordinal != 0 {
		t.Errorf("turn position = (ordinal %d, offset %d), want (0, %d)", turn.Ordinal, turn.Offset, offsets[1]-cursor)
	}
	// msg_2 is now the only open group.
	if second.OpenSourceRef != "msg_2" || second.OpenOffset != offsets[5]-cursor || second.OpenRecords != 1 {
		t.Errorf("open group = (%q, %d, %d), want (msg_2, %d, 1)",
			second.OpenSourceRef, second.OpenOffset, second.OpenRecords, offsets[5]-cursor)
	}

	// A third sync before any new turn makes no progress and emits nothing
	// new: the open group is re-read, never half-consumed.
	rest := data[cursor+second.ConsumedBytes:]
	third := Claude{}.ParseTranscript(TranscriptParseState{}, rest)
	if len(third.Turns) != 0 || third.OpenSourceRef != "msg_2" || third.ConsumedBytes != 0 {
		t.Errorf("third sync = (%+v, open %q, consumed %d), want (no turns, msg_2, 0)",
			third.Turns, third.OpenSourceRef, third.ConsumedBytes)
	}
	final := Claude{}.ParseTranscriptFinal(TranscriptParseState{}, rest)
	if len(final.Turns) != 1 || final.Turns[0].SourceRef != "msg_2" {
		t.Fatalf("final parse turns = %+v, want exactly msg_2", final.Turns)
	}
	if final.OpenSourceRef != "" || final.ConsumedBytes != int64(len(rest)) || final.PendingBytes != 0 {
		t.Errorf("final parse = (open %q, consumed %d, pending %d), want nothing withheld and the whole range consumed",
			final.OpenSourceRef, final.ConsumedBytes, final.PendingBytes)
	}

	// The end state a caller stores: one turn per message id, one usage delta
	// each, with the text and calls of the split message complete.
	stored := map[string]int{}
	for _, batch := range []TranscriptBatch{first, second, third, final} {
		for _, tr := range batch.Turns {
			stored[tr.SourceRef]++
		}
	}
	if len(stored) != 2 || stored["msg_1"] != 1 || stored["msg_2"] != 1 {
		t.Errorf("stored turns = %v, want each message id exactly once", stored)
	}
}

func TestClaudeParseTranscriptEverySplitPositionEmitsEachMessageOnceComplete(t *testing.T) {
	// The cursor discipline the incremental contract rests on, driven as a
	// caller implements it: parse a range, advance only by ConsumedBytes,
	// re-read from there, and flush the tail once at rest. Doing that for
	// every byte position is what makes "no message is ever emitted twice,
	// dropped, or emitted half-written" a checked property rather than a
	// claim about the positions the fixture happens to break on: a cursor that
	// advanced into a withheld group, or a group emitted at a chunk boundary
	// and then again by the next chunk, shows up at some split position.
	data := []byte(claudeIncrementalFixture)
	complete := Claude{}.ParseTranscriptFinal(TranscriptParseState{}, data)
	if len(complete.Turns) != 2 {
		t.Fatalf("whole-file turns = %+v, want the two the fixture has", complete.Turns)
	}
	want := map[string]TranscriptTurn{}
	for _, tr := range complete.Turns {
		want[tr.SourceRef] = tr
	}

	collect := func(split int, seen map[string]TranscriptTurn, batch TranscriptBatch) {
		for _, tr := range batch.Turns {
			if prev, ok := seen[tr.SourceRef]; ok {
				t.Fatalf("split %d: turn %q emitted twice: %q then %q", split, tr.SourceRef, prev.Text, tr.Text)
			}
			seen[tr.SourceRef] = tr
		}
	}

	for split := 0; split <= len(data); split++ {
		seen := map[string]TranscriptTurn{}
		cursor := 0
		// Chunk boundaries: the artificial mid-write boundary, then the real
		// end of the file. A withheld group that straddles the first one is
		// simply re-read from the cursor by the second.
		for _, end := range []int{split, len(data)} {
			if end <= cursor {
				continue // an empty chunk: nothing to parse
			}
			batch := Claude{}.ParseTranscript(TranscriptParseState{}, data[cursor:end])
			collect(split, seen, batch)
			if batch.ConsumedBytes < 0 || cursor+int(batch.ConsumedBytes) > len(data) {
				t.Fatalf("split %d: cursor %d + consumed %d is past the %d bytes handed in", split, cursor, batch.ConsumedBytes, len(data))
			}
			cursor += int(batch.ConsumedBytes)
		}
		// The caller has established at rest (that is the precondition, not
		// something the parse discovers) and flushes what remains.
		final := Claude{}.ParseTranscriptFinal(TranscriptParseState{}, data[cursor:])
		collect(split, seen, final)
		if final.OpenSourceRef != "" || final.PendingBytes != 0 {
			t.Fatalf("split %d: at-rest flush left (%q, %d pending byte(s)); a real session ends on a line boundary",
				split, final.OpenSourceRef, final.PendingBytes)
		}

		if len(seen) != len(want) {
			t.Fatalf("split %d: stored %d turn(s) %v, want %d", split, len(seen), seen, len(want))
		}
		for ref, expected := range want {
			got, ok := seen[ref]
			if !ok {
				t.Fatalf("split %d: message %q was never emitted: it was dropped, not deferred", split, ref)
			}
			if got.Text != expected.Text || got.Usage != expected.Usage {
				t.Fatalf("split %d: message %q stored %q %+v, want %q %+v: a partially written message reached the caller",
					split, ref, got.Text, got.Usage, expected.Text, expected.Usage)
			}
		}
	}
}

func TestClaudeParseTranscriptWithholdsTheOpenGroupsOwnToolResult(t *testing.T) {
	// A user record carrying a tool_result can sit between two records of one
	// assistant message (measured). A sync ending after it must withhold the
	// result with the group: the call it answers is withheld too, and a result
	// whose call the caller cannot resolve is the orphaned-result failure the
	// ADR forbids.
	data := []byte(claudeIncrementalFixture)
	offsets := lineOffsets(data)
	batch := Claude{}.ParseTranscript(TranscriptParseState{}, data[:offsets[5]])

	if batch.OpenSourceRef != "msg_1" || batch.OpenOffset != offsets[1] || batch.ConsumedBytes != offsets[1] {
		t.Fatalf("open group = (%q, %d), consumed %d, want (msg_1, %d, %d)",
			batch.OpenSourceRef, batch.OpenOffset, batch.ConsumedBytes, offsets[1], offsets[1])
	}
	if len(batch.ToolResults) != 0 {
		t.Errorf("tool results = %+v, want none: their call is in the withheld group", batch.ToolResults)
	}
	if len(batch.Turns) != 0 || len(batch.ToolCalls) != 0 {
		t.Errorf("facts = (%d turns, %d calls), want none from the open group", len(batch.Turns), len(batch.ToolCalls))
	}
}

func TestClaudeParseTranscriptWithholdsTheTrailingMessageGroupAndFinalEmitsItOnce(t *testing.T) {
	data := []byte(claudeSplitMessageFixture)
	offsets := lineOffsets(data)
	batch := parseIncrementalFixture(t, claudeSplitMessageFixture)

	if batch.OpenSourceRef != "msg_1" || batch.OpenOffset != offsets[1] {
		t.Fatalf("open group = (%q, %d), want (msg_1, %d)", batch.OpenSourceRef, batch.OpenOffset, offsets[1])
	}
	if batch.ConsumedBytes != offsets[1] || batch.PendingBytes != int64(len(data))-offsets[1] {
		t.Errorf("consumed/pending = (%d, %d), want (%d, %d)",
			batch.ConsumedBytes, batch.PendingBytes, offsets[1], int64(len(data))-offsets[1])
	}
	if len(batch.Records) != 0 || len(batch.Turns) != 0 || len(batch.ToolCalls) != 0 || len(batch.ToolResults) != 0 {
		t.Fatalf("facts = (%d records, %d turns, %d calls, %d results), want none: every agent record here is in the open group",
			len(batch.Records), len(batch.Turns), len(batch.ToolCalls), len(batch.ToolResults))
	}
	if batch.OpenRecords != 6 {
		t.Errorf("open records = %d, want 6 (the message's three records and the three agent records after them)", batch.OpenRecords)
	}

	// The same bytes, handed over as an at-rest session, are the whole session:
	// the group is emitted once.
	final := parseFinalFixture(t, claudeSplitMessageFixture)
	if final.OpenSourceRef != "" || final.OpenOffset != 0 || final.OpenRecords != 0 {
		t.Errorf("final parse withheld (%q, %d, %d), want nothing", final.OpenSourceRef, final.OpenOffset, final.OpenRecords)
	}
	if len(final.Turns) != 1 || final.Turns[0].SourceRef != "msg_1" {
		t.Errorf("final turns = %+v, want msg_1 once", final.Turns)
	}
	if final.ConsumedBytes != int64(len(data)) || final.PendingBytes != 0 {
		t.Errorf("final consumed/pending = (%d, %d), want (%d, 0)", final.ConsumedBytes, final.PendingBytes, len(data))
	}
}

// TestClaudeParseTranscriptFinalWithholdsWhatBytesShowIsUnfinished pins the two
// ways a range is visibly unfinished, which even a final parse refuses to emit
// past: a partial trailing line (evidence a writer is still appending) and a
// malformed complete line (evidence the parse cannot see the whole message). In
// both, the trailing group is withheld exactly as ParseTranscript withholds it,
// because a turn emitted then could never be completed - and unlike the
// malformed-prefix rule, the group is dropped whole rather than half-emitted.
//
// These are the conditions bytes reveal. They are necessary, not sufficient:
// see TestClaudeParseTranscriptFinalCannotProveAtRestFromBytes for the boundary
// no byte-level check can see.
func TestClaudeParseTranscriptFinalWithholdsWhatBytesShowIsUnfinished(t *testing.T) {
	fixture := claudeIncrementalFixture
	data := []byte(fixture)
	offsets := lineOffsets(data)

	t.Run("partial trailing line", func(t *testing.T) {
		// The whole fixture, plus the first bytes of a b2 line: b1 closed
		// msg_1, so msg_2 is the group a writer may still be extending.
		cut := append([]byte(nil), data...)
		cut = append(cut, []byte(`{"type":"assistant","uuid":"b2","message":{"id":"msg_2"`)...)

		batch := Claude{}.ParseTranscriptFinal(TranscriptParseState{}, cut)
		if batch.OpenSourceRef != "msg_2" || batch.OpenOffset != offsets[5] {
			t.Fatalf("open group = (%q, %d), want (msg_2, %d)", batch.OpenSourceRef, batch.OpenOffset, offsets[5])
		}
		if batch.ConsumedBytes != offsets[5] {
			t.Errorf("consumed = %d, want %d: the cursor stays where the withheld group starts", batch.ConsumedBytes, offsets[5])
		}
		if len(batch.Turns) != 1 || batch.Turns[0].SourceRef != "msg_1" {
			t.Fatalf("turns = %+v, want msg_1 only: msg_2 is still being written", batch.Turns)
		}
	})

	t.Run("malformed line inside the open group", func(t *testing.T) {
		// msg_1's first record, then a complete line that is not JSON.
		cut := append([]byte(nil), data[:offsets[2]]...)
		cut = append(cut, []byte("not json\n")...)

		batch := Claude{}.ParseTranscriptFinal(TranscriptParseState{}, cut)
		if batch.Malformed == nil {
			t.Fatal("malformed = nil, want the bad line reported")
		}
		if batch.OpenSourceRef != "msg_1" || batch.OpenOffset != offsets[1] {
			t.Fatalf("open group = (%q, %d), want (msg_1, %d)", batch.OpenSourceRef, batch.OpenOffset, offsets[1])
		}
		if batch.ConsumedBytes != offsets[1] {
			t.Errorf("consumed = %d, want %d: below the bad line, so a later parse still sees the whole group",
				batch.ConsumedBytes, offsets[1])
		}
		if len(batch.Records) != 1 || batch.Records[0].SourceRef != "u1" {
			t.Fatalf("records = %+v, want the user record before the group only", batch.Records)
		}
		if len(batch.Turns) != 0 || len(batch.ToolCalls) != 0 || len(batch.ToolResults) != 0 {
			t.Errorf("facts = (%d turns, %d calls, %d results), want none from the open group",
				len(batch.Turns), len(batch.ToolCalls), len(batch.ToolResults))
		}
	})

	t.Run("unparseable line after an open group", func(t *testing.T) {
		first := `{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:00:01Z","message":{"id":"msg_open","model":"claude-sonnet-4","stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":20},"content":[{"type":"text","text":"first"}]}}` + "\n"
		cut := []byte(first + "not json\n")
		batch := Claude{}.ParseTranscript(TranscriptParseState{}, cut)
		if batch.Malformed == nil || batch.OpenSourceRef != "msg_open" {
			t.Fatalf("batch = %+v, want malformed line and open msg_open", batch)
		}
		if batch.ConsumedBytes != 0 || len(batch.Turns) != 0 {
			t.Fatalf("consumed/turns = (%d, %d), want 0/0: relationship to the open group is unknowable", batch.ConsumedBytes, len(batch.Turns))
		}
	})

	t.Run("parseable malformed line from another group", func(t *testing.T) {
		first := `{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:00:01Z","message":{"id":"msg_open","model":"claude-sonnet-4","stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":20},"content":[{"type":"text","text":"first"}]}}` + "\n"
		other := `{"type":"assistant","uuid":"a2","timestamp":"2026-09-08T10:00:02Z","message":{"id":"msg_other","model":"claude-sonnet-4","stop_reason":"end_turn","content":[{"type":"text","text":"bad"}]}}` + "\n"
		batch := Claude{}.ParseTranscript(TranscriptParseState{}, []byte(first+other))
		if batch.Malformed == nil || batch.OpenSourceRef != "" {
			t.Fatalf("batch = %+v, want malformed different-id line with no open group", batch)
		}
		if batch.ConsumedBytes != int64(len(first)) || len(batch.Turns) != 1 || batch.Turns[0].SourceRef != "msg_open" {
			t.Fatalf("consumed/turns = (%d, %+v), want prefix and msg_open", batch.ConsumedBytes, batch.Turns)
		}
	})
}

// TestClaudeParseTranscriptFinalCannotProveAtRestFromBytes is the executable
// statement of ParseTranscriptFinal's precondition (port docs in transcript.go,
// and the method's own doc): the parser cannot tell a writer that has stopped
// between two records from one that is finished, so a final call on a live range
// flushes a truncated turn. This case is the ordinary write boundary of a real
// session - a newline-terminated record whose message has further blocks to come
// - and nothing in the bytes distinguishes it from a complete message.
//
// The counterexample is kept as a test rather than handled in the parser,
// because the fix is not a better byte check: it is that the caller must
// establish at rest (the harness runtime positively stopped, so no writer can
// remain) - which no caller in this repository does, so the method is unused
// until G5-12 meets ADR 0016 item 6's three gates. A turn stored here can never be amended - the truncated
// turn already owns (binding_id, source, msg_1), and agent_turn is unique and
// never updated - so a later sync of the completed message can only fail, which
// is the damage the precondition exists to prevent.
func TestClaudeParseTranscriptFinalCannotProveAtRestFromBytes(t *testing.T) {
	data := []byte(claudeIncrementalFixture)
	offsets := lineOffsets(data)

	// A live session whose writer has just appended msg_1's first block. Every
	// byte handed over is a terminated, well-formed line.
	truncated := Claude{}.ParseTranscriptFinal(TranscriptParseState{}, data[:offsets[2]])
	if truncated.OpenSourceRef != "" || truncated.PendingBytes != 0 {
		t.Fatalf("final parse withheld (%q, %d pending byte(s)), want it to flush: nothing in the bytes says this message is unfinished",
			truncated.OpenSourceRef, truncated.PendingBytes)
	}
	if len(truncated.Turns) != 1 || truncated.Turns[0].SourceRef != "msg_1" {
		t.Fatalf("turns = %+v, want the one truncated msg_1 turn", truncated.Turns)
	}
	if got := truncated.Turns[0].Text; got != "first half" {
		t.Fatalf("truncated turn text = %q, want the half that was on disk", got)
	}
	if len(truncated.ToolCalls) != 0 {
		t.Errorf("tool calls = %+v, want none: the tool_use block had not been written yet", truncated.ToolCalls)
	}

	// The writer resumes and finishes the same message: same message id, more
	// blocks. A store that committed the first result cannot take this one.
	complete := Claude{}.ParseTranscriptFinal(TranscriptParseState{}, data[:offsets[5]])
	if len(complete.Turns) != 1 || complete.Turns[0].SourceRef != "msg_1" {
		t.Fatalf("turns = %+v, want msg_1 once", complete.Turns)
	}
	if got := complete.Turns[0].Text; got != "first half\nsecond half" {
		t.Fatalf("completed turn text = %q, want both halves", got)
	}
	if truncated.Turns[0].SourceRef != complete.Turns[0].SourceRef {
		t.Fatalf("source refs = (%q, %q), want the same message id: that identity is what makes the first result unamendable",
			truncated.Turns[0].SourceRef, complete.Turns[0].SourceRef)
	}
	if truncated.Turns[0].Text == complete.Turns[0].Text {
		t.Fatal("the two parses agree; the precondition would then be moot")
	}
}

// TestClaudeParseTranscriptSkipsEveryKnownHousekeepingType pins the inventory
// of harness top-level types that are skipped rather than copied. The list is
// the 330-session census of CLI 2.1.269 (ADR 0016 names the first eight); a
// type missing here would be copied or guessed at by accident, and a new one
// must be added deliberately and counted.
func TestClaudeParseTranscriptSkipsEveryKnownHousekeepingType(t *testing.T) {
	known := []string{
		"attachment", "file-history-snapshot", "file-history-delta", "ai-title",
		"atis-latch", "mode", "permission-mode", "last-prompt", "queue-operation",
		"pr-link", "cost-state", "agent-name", "custom-title",
	}
	for _, typ := range known {
		t.Run(typ, func(t *testing.T) {
			data := []byte(`{"type":"` + typ + `","uuid":"h1","timestamp":"2026-09-08T10:00:00.000Z"}` + "\n")
			batch := Claude{}.ParseTranscript(TranscriptParseState{}, data)
			if batch.Malformed != nil {
				t.Fatalf("malformed = %+v, want nil", batch.Malformed)
			}
			if len(batch.Records) != 0 || len(batch.Turns) != 0 || len(batch.ToolCalls) != 0 || len(batch.ToolResults) != 0 {
				t.Errorf("facts = (%d records, %d turns, %d calls, %d results), want none",
					len(batch.Records), len(batch.Turns), len(batch.ToolCalls), len(batch.ToolResults))
			}
			if batch.Skipped[typ] != 1 || len(batch.Skipped) != 1 {
				t.Errorf("skipped = %v, want exactly {%s: 1}", batch.Skipped, typ)
			}
			if batch.ConsumedBytes != int64(len(data)) {
				t.Errorf("consumed = %d, want %d: a skipped record is still consumed", batch.ConsumedBytes, len(data))
			}
			// A range of housekeeping records alone withholds nothing.
			if batch.OpenSourceRef != "" {
				t.Errorf("open group = %q, want none", batch.OpenSourceRef)
			}
		})
	}
}

func TestClaudeParseTranscriptTrailingFragmentIsNotConsumed(t *testing.T) {
	complete := `{"type":"user","uuid":"u1","timestamp":"2026-09-08T10:00:00.000Z","message":{"role":"user","content":"hi"}}` + "\n"
	fragment := `{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:`
	data := []byte(complete + fragment)

	batch := Claude{}.ParseTranscript(TranscriptParseState{}, data)
	if batch.Malformed != nil {
		t.Fatalf("malformed = %+v, want nil: an unterminated final line is an active-file boundary", batch.Malformed)
	}
	if batch.ConsumedBytes != int64(len(complete)) {
		t.Errorf("consumed = %d, want %d (the fragment must not be consumed)", batch.ConsumedBytes, len(complete))
	}
	if batch.PendingBytes != int64(len(fragment)) {
		t.Errorf("pending = %d, want %d", batch.PendingBytes, len(fragment))
	}
	if len(batch.Records) != 1 || batch.Records[0].SourceRef != "u1" {
		t.Fatalf("records = %+v, want just the complete line", batch.Records)
	}
}

func TestClaudeParseTranscriptMalformedCompleteLineStopsAndKeepsPrefix(t *testing.T) {
	const first = `{"type":"user","uuid":"u1","timestamp":"2026-09-08T10:00:00.000Z","message":{"role":"user","content":"hi"}}`
	const third = `{"type":"user","uuid":"u3","timestamp":"2026-09-08T10:00:03.000Z","message":{"role":"user","content":"later"}}`
	usage := `{"input_tokens":1,"output_tokens":1}`
	cases := []struct {
		name   string
		line   string
		reason string
	}{
		{"invalid_json", `{"type":"assistant",`, MalformedInvalidJSON},
		{"missing_type", `{"uuid":"x","timestamp":"2026-09-08T10:00:01.000Z"}`, MalformedMissingType},
		{"assistant_without_uuid", `{"type":"assistant","timestamp":"2026-09-08T10:00:01.000Z","message":{"id":"m","model":"c","usage":` + usage + `,"content":[]}}`, MalformedMissingRecordID},
		{"assistant_without_timestamp", `{"type":"assistant","uuid":"a","message":{"id":"m","model":"c","usage":` + usage + `,"content":[]}}`, MalformedMissingTimestamp},
		{"assistant_without_message_id", `{"type":"assistant","uuid":"a","timestamp":"2026-09-08T10:00:01.000Z","message":{"model":"c","usage":` + usage + `,"content":[]}}`, MalformedAssistantShape},
		{"assistant_without_usage", `{"type":"assistant","uuid":"a","timestamp":"2026-09-08T10:00:01.000Z","message":{"id":"m","model":"c","content":[]}}`, MalformedAssistantShape},
		{"tool_use_without_name", `{"type":"assistant","uuid":"a","timestamp":"2026-09-08T10:00:01.000Z","message":{"id":"m","model":"c","usage":` + usage + `,"content":[{"type":"tool_use","id":"toolu_x","input":{}}]}}`, MalformedToolCallShape},
		{"tool_result_without_id", `{"type":"user","uuid":"u","timestamp":"2026-09-08T10:00:01.000Z","message":{"role":"user","content":[{"type":"tool_result","content":"x"}]}}`, MalformedToolResultShape},
		{"user_content_neither_string_nor_array", `{"type":"user","uuid":"u","timestamp":"2026-09-08T10:00:01.000Z","message":{"role":"user","content":{"text":"x"}}}`, MalformedUserShape},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(first + "\n" + tc.line + "\n" + third + "\n")
			batch := Claude{}.ParseTranscript(TranscriptParseState{}, data)
			if batch.Malformed == nil {
				t.Fatalf("malformed = nil, want reason %q", tc.reason)
			}
			if batch.Malformed.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q (detail: %s)", batch.Malformed.Reason, tc.reason, batch.Malformed.Detail)
			}
			if batch.Malformed.Ordinal != 1 {
				t.Errorf("malformed ordinal = %d, want 1", batch.Malformed.Ordinal)
			}
			wantOffset := int64(len(first) + 1)
			if batch.Malformed.Offset != wantOffset || batch.ConsumedBytes != wantOffset {
				t.Errorf("malformed offset = %d, consumed = %d, want %d for both: the prefix is kept and the bad line is not passed",
					batch.Malformed.Offset, batch.ConsumedBytes, wantOffset)
			}
			if batch.OpenSourceRef != "" || batch.OpenOffset != 0 || batch.OpenRecords != 0 {
				t.Errorf("open group = (%q, %d, %d), want nothing withheld: the malformed line already stopped the parse before any assistant record",
					batch.OpenSourceRef, batch.OpenOffset, batch.OpenRecords)
			}
			if len(batch.Records) != 1 || batch.Records[0].SourceRef != "u1" {
				t.Errorf("records = %+v, want only the prefix record u1", batch.Records)
			}
			if batch.PendingBytes != int64(len(tc.line)+1+len(third)+1) {
				t.Errorf("pending = %d, want the bad line and everything after it", batch.PendingBytes)
			}
			wantEnd := wantOffset + int64(len([]byte(tc.line))) + 1
			if batch.Malformed.EndOffset != wantEnd {
				t.Errorf("malformed span = [%d,%d), want [%d,%d)", batch.Malformed.Offset, batch.Malformed.EndOffset, wantOffset, wantEnd)
			}
		})
	}
}

func TestClaudeParseTranscriptToolResultContentShapes(t *testing.T) {
	cases := []struct {
		name    string
		content string
		isError string
		want    string
		wantErr bool
	}{
		{name: "string", content: `"plain output"`, want: "plain output"},
		{name: "text_blocks_joined", content: `[{"type":"text","text":"one"},{"type":"text","text":"two"}]`, want: "one\ntwo"},
		{name: "non_text_blocks_kept_as_json", content: `[{"type":"image","source":{"data":"AAAA"}}]`, want: `[{"type":"image","source":{"data":"AAAA"}}]`},
		{name: "null_content", content: `null`, want: ""},
		{name: "error_flag", content: `"boom"`, isError: `,"is_error":true`, want: "boom", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := `{"type":"user","uuid":"u1","timestamp":"2026-09-08T10:00:00.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":` + tc.content + tc.isError + `}]}}`
			batch := Claude{}.ParseTranscript(TranscriptParseState{}, []byte(line+"\n"))
			if batch.Malformed != nil {
				t.Fatalf("malformed = %+v", batch.Malformed)
			}
			if len(batch.ToolResults) != 1 {
				t.Fatalf("results = %d, want 1", len(batch.ToolResults))
			}
			got := batch.ToolResults[0]
			if got.OutputText != tc.want {
				t.Errorf("output = %q, want %q", got.OutputText, tc.want)
			}
			if got.OutputBytes != int64(len(tc.want)) {
				t.Errorf("output bytes = %d, want %d", got.OutputBytes, len(tc.want))
			}
			if got.IsError != tc.wantErr {
				t.Errorf("is_error = %v, want %v", got.IsError, tc.wantErr)
			}
		})
	}
}

func TestClaudeParseTranscriptDisambiguatesMultipleResultsInOneRecord(t *testing.T) {
	// Claude writes one tool_result block per record today, so this shape is
	// not observed; the parser must still not collapse two results onto one
	// source_ref if a later version batches them.
	line := `{"type":"user","uuid":"u9","timestamp":"2026-09-08T10:00:00.000Z","message":{"role":"user","content":[` +
		`{"type":"tool_result","tool_use_id":"toolu_a","content":"a"},` +
		`{"type":"tool_result","tool_use_id":"toolu_b","content":"b"}]}}`
	batch := Claude{}.ParseTranscript(TranscriptParseState{}, []byte(line+"\n"))

	if len(batch.ToolResults) != 2 {
		t.Fatalf("results = %d, want 2", len(batch.ToolResults))
	}
	if batch.ToolResults[0].SourceRef != "u9#0" || batch.ToolResults[1].SourceRef != "u9#1" {
		t.Errorf("result refs = %q, %q, want u9#0, u9#1",
			batch.ToolResults[0].SourceRef, batch.ToolResults[1].SourceRef)
	}
	if batch.ToolResults[0].CallID != "toolu_a" || batch.ToolResults[1].CallID != "toolu_b" {
		t.Errorf("call ids = %q, %q", batch.ToolResults[0].CallID, batch.ToolResults[1].CallID)
	}
}

func TestClaudeParseTranscriptBlankLinesKeepOrdinalsAbsolute(t *testing.T) {
	line := `{"type":"user","uuid":"u1","timestamp":"2026-09-08T10:00:00.000Z","message":{"role":"user","content":"hi"}}`
	batch := Claude{}.ParseTranscript(TranscriptParseState{}, []byte("\n\n"+line+"\n"))

	if batch.Malformed != nil {
		t.Fatalf("malformed = %+v, want nil for blank lines", batch.Malformed)
	}
	if len(batch.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(batch.Records))
	}
	if batch.Records[0].Ordinal != 2 {
		t.Errorf("ordinal = %d, want 2: blank lines still occupy a line number", batch.Records[0].Ordinal)
	}
}

func TestClassifyClaudeTool(t *testing.T) {
	cases := map[string]CommandClass{
		"Bash":            CommandShell,
		"BashOutput":      CommandShell,
		"KillShell":       CommandShell,
		"KillBash":        CommandShell,
		"Read":            CommandRead,
		"NotebookRead":    CommandRead,
		"Edit":            CommandEdit,
		"MultiEdit":       CommandEdit,
		"Write":           CommandEdit,
		"NotebookEdit":    CommandEdit,
		"Grep":            CommandSearch,
		"Glob":            CommandSearch,
		"Task":            CommandAgent,
		"Agent":           CommandAgent,
		"ListAgents":      CommandAgent,
		"SendMessage":     CommandAgent,
		"SendFeedback":    CommandAgent,
		"mcp__github__pr": CommandMCP,
		"mcp__":           CommandMCP,
		"webfetch":        CommandOther, // unknown tool names are kept, as other
		"bash":            CommandOther, // the mapping is case-sensitive
		"":                CommandOther,
	}
	for name, want := range cases {
		if got := ClassifyClaudeTool(name); got != want {
			t.Errorf("ClassifyClaudeTool(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestClaudeProjectSlug(t *testing.T) {
	cases := []struct {
		name string
		cwd  string
		want string
	}{
		{"absolute_path", "/Users/anh/.treehouse/gomate-dd5648/4/gomate", "-Users-anh--treehouse-gomate-dd5648-4-gomate"},
		{"slash_only", "/", "-"},
		{"empty", "", ""},
		{"underscore_and_space", "/tmp/under_score space", "-tmp-under-score-space"},
		{"dot_and_dash", "/w/a.b", "-w-a-b"},
		// Measured live 2026-09-12 against claude-code 2.1.269: the slug runs
		// over UTF-16 code units, so an astral character is two dashes and a
		// BMP accented character is one.
		{"unicode_utf16_units", "/private/tmp/g5s6b-slug-probe-74669/acc\u00e9nt-\U0001F600-x", "-private-tmp-g5s6b-slug-probe-74669-acc-nt----x"},
		// Past 200 code units the CLI cuts the slug and appends a hash of the
		// whole cwd. Expected values come from running the functions copied
		// out of claude-code 2.1.286's bundle under node, not from this code.
		{"exactly_200_is_whole", "/" + strings.Repeat("a", 199), "-" + strings.Repeat("a", 199)},
		{"201_is_cut_and_hashed", "/" + strings.Repeat("a", 200), "-" + strings.Repeat("a", 199) + "-b6ymvl"},
		{"long_tmpdir", "/private/tmp/mate-long-tmpdir-" + strings.Repeat("a", 64) + "/" + strings.Repeat("b", 58) +
			"/TestMateClaudeIsLocatedFromItsSessionIDWhenTheHookHasNotWrittenT1521853395/001/.mate/projects/shop/mate",
			"-private-tmp-mate-long-tmpdir-" + strings.Repeat("a", 64) + "-" + strings.Repeat("b", 58) +
				"-TestMateClaudeIsLocatedFromItsSessionIDWhenThe-d83bm9"},
		{"cut_counts_utf16_units", "/w/" + strings.Repeat("x", 196) + "\U0001F600\u00e9/tail", "-w-" + strings.Repeat("x", 196) + "-" + "-8eh65c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClaudeProjectSlug(tc.cwd); got != tc.want {
				t.Errorf("ClaudeProjectSlug(%q) = %q, want %q", tc.cwd, got, tc.want)
			}
		})
	}

	// The encoding is collision-resistant, not injective: '.' and '-' share a
	// slug, which is exactly why a caller must still validate the file it
	// finds rather than trusting the name.
	if ClaudeProjectSlug("/w/a.b") != ClaudeProjectSlug("/w/a-b") {
		t.Fatal("expected distinct cwds to collide under the slug encoding")
	}
}

func TestClaudeTranscriptPath(t *testing.T) {
	got := ClaudeTranscriptPath("/home/me/.claude/projects", "/w/x.y", "1f3d-session")
	want := "/home/me/.claude/projects/-w-x-y/1f3d-session.jsonl"
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if ClaudeTranscriptFileName("abc") != "abc.jsonl" {
		t.Errorf("file name = %q", ClaudeTranscriptFileName("abc"))
	}
}

// TestLiveClaudeProjectSlugMatchesRealTranscriptCorpus re-verifies the slug rule
// against a real ~/.claude/projects tree rather than synthetic fixtures. It is
// opt-in because CI has no Claude installation; the evidence driver runs it
// when MATE_CLAUDE_PROJECTS_DIR points at one.
func TestLiveClaudeProjectSlugMatchesRealTranscriptCorpus(t *testing.T) {
	requireLive(t)
	root := os.Getenv("MATE_CLAUDE_PROJECTS_DIR")
	if root == "" {
		t.Skip("set MATE_CLAUDE_PROJECTS_DIR to <home>/.claude/projects to re-verify the slug rule against real sessions")
	}
	projectDirs, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	sessions := 0
	for _, project := range projectDirs {
		if !project.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, project.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", project.Name(), err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			cwd, ok, err := firstTranscriptCwd(filepath.Join(root, project.Name(), entry.Name()))
			if err != nil {
				t.Fatalf("%s: %v", entry.Name(), err)
			}
			if !ok {
				continue
			}
			if got := ClaudeProjectSlug(cwd); got != project.Name() {
				t.Errorf("%s: ClaudeProjectSlug(%q) = %q, want project directory %q",
					entry.Name(), cwd, got, project.Name())
			}
			sessions++
		}
	}
	t.Logf("checked %d session transcript(s) under %s", sessions, root)
	if sessions == 0 {
		t.Fatal("no session transcript with a cwd was found; this proved nothing")
	}
}

// firstTranscriptCwd returns the first cwd recorded in a session file, which is
// the session's launch cwd: later records can carry a changed cwd (after a
// `cd`) without the project slug changing.
func firstTranscriptCwd(path string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			var rec struct {
				Cwd string `json:"cwd"`
			}
			if json.Unmarshal(trimmed, &rec) == nil && rec.Cwd != "" {
				return rec.Cwd, true, nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", false, nil
			}
			return "", false, err
		}
	}
}

// TestLiveClaudeTranscriptCorpusIncrementalSyncEmitsEveryMessageOnce re-verifies
// the incremental contract against real sessions rather than synthetic
// fixtures: it cuts each session into eight growing prefixes, commits what each
// sync reports (never more - the cursor only moves by ConsumedBytes) and
// asserts that no message is ever emitted twice, that each emission is
// byte-identical in the facts a commitment uses to the whole-file parse of the
// same session, and that the chain loses no message. A message cut across two
// syncs is exactly the shape that produced a duplicate turn for one message id;
// eight arbitrary cut points per session hit it often. Opt-in for the same
// reason as the slug corpus test.
// TestLiveClaudeTranscriptCorpusIncrementalSyncEmitsEveryMessageOnce replays every
// real session as a growing file, committing each sync exactly as the ingest
// path would (only up to ConsumedBytes). It proves the two properties the
// deferral rests on, on real data: no message is emitted twice or with facts
// the finished message contradicts (which is also the no-ABA property - a
// message id recurring after a newer one is emitted again, because the group
// that closed it is already past the cursor), and the chain of syncs loses no
// message the whole-file parse has.
func TestLiveClaudeTranscriptCorpusIncrementalSyncEmitsEveryMessageOnce(t *testing.T) {
	requireLive(t)
	files, steps, emissions := 0, 0, 0
	walkTranscriptCorpus(t, func(path string, data []byte) {
		files++
		truth := map[string]TranscriptTurn{}
		whole := Claude{}.ParseTranscriptFinal(TranscriptParseState{}, data)
		if whole.Malformed != nil {
			t.Errorf("%s: line %d is malformed (%s): the parser must not stop part-way through a real session", path, whole.Malformed.Ordinal, whole.Malformed.Reason)
		}
		if whole.PendingBytes != 0 || whole.OpenSourceRef != "" {
			t.Errorf("%s: whole-file final parse left %d byte(s) pending and group %q open; a real session at rest ends on a line boundary with every message closed",
				path, whole.PendingBytes, whole.OpenSourceRef)
		}
		for _, turn := range whole.Turns {
			if _, dup := truth[turn.SourceRef]; dup {
				t.Errorf("%s: %s appears twice even in a whole-file parse", path, turn.SourceRef)
			}
			truth[turn.SourceRef] = turn
		}

		seen := map[string]int{}
		commit := func(turns []TranscriptTurn) {
			for _, turn := range turns {
				seen[turn.SourceRef]++
				emissions++
				if seen[turn.SourceRef] > 1 {
					t.Errorf("%s: message %s emitted more than once across syncs - the second receipt cannot be stored (agent_turn is unique and immutable)", path, turn.SourceRef)
				}
				want, ok := truth[turn.SourceRef]
				if !ok {
					t.Errorf("%s: sync emitted %s, which the whole-file parse does not have", path, turn.SourceRef)
					continue
				}
				if turn.Usage != want.Usage || turn.Text != want.Text || turn.Model != want.Model || turn.StopReason != want.StopReason {
					t.Errorf("%s: message %s emitted before it was complete: got (%+v, %q, %q), want (%+v, %q, %q)",
						path, turn.SourceRef, turn.Usage, turn.Text, turn.Model, want.Usage, want.Text, want.Model)
				}
				// The carried state must make a resumed parse indistinguishable
				// from an uninterrupted one. Without it every sync after the
				// first would lose the open harness turn, since no assistant
				// record carries one.
				if turn.HarnessTurnRef != want.HarnessTurnRef || turn.SubagentID != want.SubagentID {
					t.Errorf("%s: message %s resumed with harness turn %q/subagent %q, whole-file parse says %q/%q",
						path, turn.SourceRef, turn.HarnessTurnRef, turn.SubagentID, want.HarnessTurnRef, want.SubagentID)
				}
			}
		}

		cursor := int64(0)
		// The cursor and the state are one committed pair; the replay stores
		// and restores them exactly as the ingest path would.
		state := TranscriptParseState{}
		for step := 1; step <= 8; step++ {
			end := int64(len(data)) * int64(step) / 8
			if end <= cursor {
				continue
			}
			batch := Claude{}.ParseTranscript(state, data[cursor:end])
			steps++
			if batch.ConsumedBytes < 0 || cursor+batch.ConsumedBytes > end {
				t.Fatalf("%s: consumed %d bytes out of a %d-byte range", path, batch.ConsumedBytes, end-cursor)
			}
			commit(batch.Turns)
			cursor += batch.ConsumedBytes
			state = batch.NextState
		}
		final := Claude{}.ParseTranscriptFinal(state, data[cursor:])
		commit(final.Turns)
		if final.OpenSourceRef != "" {
			t.Errorf("%s: a final parse at rest still reports %s open", path, final.OpenSourceRef)
		}
		if len(seen) != len(truth) {
			t.Errorf("%s: %d of %d messages were emitted by the sync chain", path, len(seen), len(truth))
		}
	})
	t.Logf("cut %d session(s) into %d growing sync(s): %d turn emission(s), each message exactly once", files, steps, emissions)
	if files == 0 {
		t.Fatal("no session transcript was read; this proved nothing")
	}
}

// walkTranscriptCorpus hands fn the bytes of every session transcript under
// MATE_CLAUDE_PROJECTS_DIR, skipping the test when it is unset.
func walkTranscriptCorpus(t *testing.T, fn func(path string, data []byte)) {
	t.Helper()
	requireLive(t)
	root := os.Getenv("MATE_CLAUDE_PROJECTS_DIR")
	if root == "" {
		t.Skip("set MATE_CLAUDE_PROJECTS_DIR to <home>/.claude/projects to re-verify against real sessions")
	}
	projectDirs, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	_ = projectDirs
	// Walks the whole tree, not just each project directory's top level: a
	// subagent stream lives at <project>/<session-id>/subagents/agent-<id>.jsonl
	// and is a transcript like any other. It is also where the only
	// growing-usage messages in this corpus are, so a walk that stopped at the
	// top level would test the parser exactly where it was already correct.
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fn(path, data)
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func mustTime(t *testing.T, value string) (ts time.Time) {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("bad test timestamp %q: %v", value, err)
	}
	return ts
}

// claudeGrowingUsageFixture reproduces a shape measured on real subagent
// transcripts: one assistant message written as several records whose usage is
// a running snapshot, not a repeat. input_tokens and both cache buckets are
// stable while output_tokens and thinking_tokens grow, and the last record
// carries the completed message's real cost. The numbers are the ones
// msg_011CeuWsh1vBqRJaB4h2oZGf actually recorded (2, 4/4/341 output, 0/0/29
// thinking, 29046 cache creation).
const claudeGrowingUsageFixture = `{"type":"user","uuid":"u0","timestamp":"2026-09-08T10:00:00.000Z","promptId":"p1","message":{"role":"user","content":"do it"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:00:01.000Z","message":{"id":"msg_g","model":"m","usage":{"input_tokens":2,"output_tokens":4,"cache_read_input_tokens":0,"cache_creation_input_tokens":29046,"output_tokens_details":{"thinking_tokens":0}},"content":[{"type":"text","text":"one"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-09-08T10:00:02.000Z","message":{"id":"msg_g","model":"m","usage":{"input_tokens":2,"output_tokens":4,"cache_read_input_tokens":0,"cache_creation_input_tokens":29046,"output_tokens_details":{"thinking_tokens":0}},"content":[{"type":"text","text":"two"}]}}
{"type":"assistant","uuid":"a3","timestamp":"2026-09-08T10:00:03.000Z","message":{"id":"msg_g","model":"m","stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":341,"cache_read_input_tokens":0,"cache_creation_input_tokens":29046,"output_tokens_details":{"thinking_tokens":29}},"content":[{"type":"text","text":"three"}]}}
`

// A message's usage is a snapshot, and the last record's snapshot is the
// completed message's cost. Keeping the first record's numbers undercounts
// exactly the messages that grew - measured across the local corpus, all 342
// such groups are subagent turns, worth 412,965 output and 252,113 thinking
// tokens, the most expensive activity there is. Summing is equally wrong: the
// snapshots overlap.
func TestClaudeParseTranscriptKeepsTheLastUsageSnapshotOfAMessage(t *testing.T) {
	batch := parseFinalFixture(t, claudeGrowingUsageFixture)

	if len(batch.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(batch.Turns))
	}
	got := batch.Turns[0].Usage
	want := TokenUsage{Input: 2, Output: 341, CacheRead: 0, CacheWrite: 29046, Reasoning: 29}
	if got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
		if got.Output == 4 {
			t.Error("this is the first record's snapshot: a message whose records grow is undercounted")
		}
		if got.Output == 4+4+341 {
			t.Error("this is the sum of every snapshot: snapshots overlap and must never be added")
		}
	}
	// The turn still reports its own first record's position and time: the
	// usage is the last snapshot, the identity is the message's start.
	if batch.Turns[0].Ordinal != 1 {
		t.Errorf("ordinal = %d, want 1 (the message's first record)", batch.Turns[0].Ordinal)
	}
	if !batch.Turns[0].OccurredAt.Equal(mustTime(t, "2026-09-08T10:00:01Z")) {
		t.Errorf("occurred at = %s, want the first record's timestamp", batch.Turns[0].OccurredAt)
	}
	if batch.Turns[0].StopReason != "end_turn" {
		t.Errorf("stop reason = %q, want end_turn", batch.Turns[0].StopReason)
	}
}

// claudeHarnessTurnFixture is a main-stream transcript covering every opener
// shape the corpus contains: a plain-string prompt, a repeat of the same
// promptId (which must not start a second turn), a block-array prompt carrying
// an image (which must open one), and a prompt with no promptId at all (which
// ends the open turn and names no replacement). tool_result records repeat
// their turn's id, as real ones do.
const claudeHarnessTurnFixture = `{"type":"user","uuid":"u1","timestamp":"2026-09-08T10:00:00.000Z","promptId":"p1","message":{"role":"user","content":"first task"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:00:01.000Z","message":{"id":"msg_a","model":"m","usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"x"}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-09-08T10:00:02.000Z","promptId":"p1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_a","content":"data"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-09-08T10:00:03.000Z","message":{"id":"msg_b","model":"m","usage":{"input_tokens":1,"output_tokens":3,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[{"type":"text","text":"done"}]}}
{"type":"user","uuid":"u3","timestamp":"2026-09-08T10:00:04.000Z","promptId":"p1","message":{"role":"user","content":"same prompt id again"}}
{"type":"assistant","uuid":"a3","timestamp":"2026-09-08T10:00:05.000Z","message":{"id":"msg_c","model":"m","usage":{"input_tokens":1,"output_tokens":4,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[{"type":"text","text":"still p1"}]}}
{"type":"user","uuid":"u4","timestamp":"2026-09-08T10:00:06.000Z","promptId":"p2","message":{"role":"user","content":[{"type":"image","source":{"type":"base64"}},{"type":"text","text":"look at this"}]}}
{"type":"assistant","uuid":"a4","timestamp":"2026-09-08T10:00:07.000Z","message":{"id":"msg_d","model":"m","usage":{"input_tokens":1,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[{"type":"text","text":"p2 work"}]}}
{"type":"user","uuid":"u5","timestamp":"2026-09-08T10:00:08.000Z","message":{"role":"user","content":"no prompt id at all"}}
{"type":"assistant","uuid":"a5","timestamp":"2026-09-08T10:00:09.000Z","message":{"id":"msg_e","model":"m","usage":{"input_tokens":1,"output_tokens":6,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[{"type":"text","text":"still p2"}]}}
`

// claudeSubagentFixture is a sidechain stream, the shape Claude writes to
// <session-id>/subagents/agent-<id>.jsonl: every record carries isSidechain and
// agentId, only the user record carries promptId, and that promptId is one the
// parent session also used - which is what joins the child to its parent
// (measured: 19 of 19 subagent files, while 19 of 19 have a null parentUuid on
// their first record, so the record chain does not do that job).
const claudeSubagentFixture = `{"type":"user","uuid":"su1","timestamp":"2026-09-08T10:00:06.500Z","promptId":"p2","isSidechain":true,"agentId":"ag-7","parentUuid":null,"message":{"role":"user","content":"research this"}}
{"type":"assistant","uuid":"sa1","timestamp":"2026-09-08T10:00:07.000Z","isSidechain":true,"agentId":"ag-7","message":{"id":"msg_s","model":"m","usage":{"input_tokens":2,"output_tokens":4,"cache_read_input_tokens":0,"cache_creation_input_tokens":29046,"output_tokens_details":{"thinking_tokens":0}},"content":[{"type":"text","text":"partial"}]}}
{"type":"assistant","uuid":"sa2","timestamp":"2026-09-08T10:00:08.000Z","isSidechain":true,"agentId":"ag-7","message":{"id":"msg_s","model":"m","stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":341,"cache_read_input_tokens":0,"cache_creation_input_tokens":29046,"output_tokens_details":{"thinking_tokens":29}},"content":[{"type":"text","text":"complete"}]}}
`

func turnRefs(batch TranscriptBatch) map[string]string {
	refs := map[string]string{}
	for _, turn := range batch.Turns {
		refs[turn.SourceRef] = turn.HarnessTurnRef
	}
	return refs
}

func TestClaudeParseTranscriptCarriesTheHarnessTurnAcrossRecords(t *testing.T) {
	batch := parseFinalFixture(t, claudeHarnessTurnFixture)

	want := map[string]string{
		"msg_a": "p1", // opened by a plain-string prompt
		"msg_b": "p1", // after a tool_result that repeats the id
		"msg_c": "p1", // a repeated opener must not start a new turn
		"msg_d": "p2", // opened by a block-array prompt carrying an image
		"msg_e": "",   // a new prompt with no promptId ends p2 and names nothing
	}
	got := turnRefs(batch)
	if len(got) != len(want) {
		t.Fatalf("turns = %v, want %d", got, len(want))
	}
	for msg, ref := range want {
		if got[msg] != ref {
			t.Errorf("%s harness turn = %q, want %q", msg, got[msg], ref)
		}
	}
	for _, turn := range batch.Turns {
		if turn.SubagentID != "" {
			t.Errorf("%s subagent = %q, want empty on a main stream", turn.SourceRef, turn.SubagentID)
		}
	}
	if batch.NextState.HarnessTurnRef != "" {
		t.Errorf("next state = %q, want unknown: the range ends after a prompt that names no turn",
			batch.NextState.HarnessTurnRef)
	}
}

// A range that begins after its opener carries no harness turn of its own. The
// parser must report that as unknown rather than attaching the records to
// whatever turn it can see, and the caller's stored state is the only thing
// that can supply the answer.
func TestClaudeParseTranscriptResumingMidHarnessTurnNeedsCarriedState(t *testing.T) {
	data := []byte(claudeHarnessTurnFixture)
	offs := lineOffsets(data)
	// Resume at msg_b's record: inside turn p1, after its opener.
	tail := data[offs[3]:]

	blind := Claude{}.ParseTranscriptFinal(TranscriptParseState{}, tail)
	blindRefs := turnRefs(blind)
	// msg_b sits between the cursor and the next opener, so nothing in the
	// tail can say which turn it belongs to.
	if blindRefs["msg_b"] != "" {
		t.Errorf("msg_b harness turn = %q with no carried state, want unknown: an association must never be synthesised",
			blindRefs["msg_b"])
	}
	// msg_c is not unknown even blind: the tail contains u3, which carries p1
	// itself. Unknown is the absence of evidence, not a property of resuming.
	if blindRefs["msg_c"] != "p1" {
		t.Errorf("msg_c harness turn = %q, want p1 from the tail's own opener", blindRefs["msg_c"])
	}
	if blind.NextState.HarnessTurnRef != "" {
		t.Errorf("next state = %q, want unknown: the tail ends after a prompt that names no turn",
			blind.NextState.HarnessTurnRef)
	}

	resumed := Claude{}.ParseTranscriptFinal(TranscriptParseState{HarnessTurnRef: "p1"}, tail)
	got := turnRefs(resumed)
	for _, msg := range []string{"msg_b", "msg_c"} {
		if got[msg] != "p1" {
			t.Errorf("%s harness turn = %q resuming with state p1, want p1", msg, got[msg])
		}
	}
	// The carried state must not survive the tail's own opener, and must not
	// survive a prompt that names no turn either.
	if got["msg_d"] != "p2" {
		t.Errorf("after the tail's own opener: msg_d=%q, want p2", got["msg_d"])
	}
	if got["msg_e"] != "" {
		t.Errorf("after a prompt with no id: msg_e=%q, want unknown", got["msg_e"])
	}

	// Resuming is indistinguishable from never having stopped.
	whole := turnRefs(parseFinalFixture(t, claudeHarnessTurnFixture))
	for _, msg := range []string{"msg_b", "msg_c", "msg_d", "msg_e"} {
		if got[msg] != whole[msg] {
			t.Errorf("%s: resumed %q, whole-file %q", msg, got[msg], whole[msg])
		}
	}
}

// NextState pairs with ConsumedBytes, not with the end of the range. A caller
// commits both in one write, so a state that ran ahead of the cursor would
// attribute the re-read records to the wrong turn.
func TestClaudeParseTranscriptNextStateIsTheStateAtTheCursor(t *testing.T) {
	// A range whose trailing group is withheld, and whose withheld region
	// contains the opener of a later turn.
	fixture := claudeHarnessTurnFixture
	batch := parseIncrementalFixture(t, fixture)
	if batch.OpenSourceRef != "msg_e" {
		t.Fatalf("open group = %q, want msg_e", batch.OpenSourceRef)
	}
	if batch.NextState.HarnessTurnRef != "" {
		t.Errorf("next state = %q, want unknown: u5 names no turn and was consumed", batch.NextState.HarnessTurnRef)
	}

	// A range ending just after p2's opener still withholds the trailing
	// message group, and withholding moves the cursor back *before* that
	// opener. The reported state must move back with it: u4 has not been
	// consumed, so the turn open at the cursor is still p1. A state taken at
	// the end of the range instead would say p2 and misattribute msg_c when
	// the caller re-read it.
	data := []byte(claudeHarnessTurnFixture)
	offs := lineOffsets(data)
	head := data[:offs[7]] // lines u1..u4; u4 opens p2
	b2 := Claude{}.ParseTranscript(TranscriptParseState{}, head)
	if b2.OpenSourceRef != "msg_c" || b2.ConsumedBytes != offs[5] {
		t.Fatalf("open group = %q at cursor %d, want msg_c at %d", b2.OpenSourceRef, b2.ConsumedBytes, offs[5])
	}
	if b2.NextState.HarnessTurnRef != "p1" {
		t.Errorf("next state = %q, want p1: the cursor was moved back before u4, which is therefore unconsumed",
			b2.NextState.HarnessTurnRef)
	}

	// Re-parsing from that cursor with that state reproduces exactly the refs
	// of one uninterrupted parse - which is the whole point of pairing them.
	whole := turnRefs(parseFinalFixture(t, claudeHarnessTurnFixture))
	resumed := turnRefs(Claude{}.ParseTranscriptFinal(b2.NextState, data[b2.ConsumedBytes:]))
	if len(resumed) != 3 {
		t.Fatalf("resumed turns = %v, want msg_c, msg_d and msg_e", resumed)
	}
	for msg, ref := range resumed {
		if whole[msg] != ref {
			t.Errorf("%s: resumed %q, whole-file %q", msg, ref, whole[msg])
		}
	}
}

func TestClaudeParseTranscriptSubagentStreamCarriesItsAgentAndSharedTurn(t *testing.T) {
	batch := parseFinalFixture(t, claudeSubagentFixture)

	if len(batch.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(batch.Turns))
	}
	turn := batch.Turns[0]
	if turn.SubagentID != "ag-7" {
		t.Errorf("subagent = %q, want ag-7", turn.SubagentID)
	}
	// The join to the parent session is this shared id, and it is the parser's
	// whole contribution to it: building the parent/child graph is reconcile's.
	if turn.HarnessTurnRef != "p2" {
		t.Errorf("harness turn = %q, want p2 (the id the parent session also used)", turn.HarnessTurnRef)
	}
	// A subagent's usage exists only here; it is the growing-snapshot shape.
	want := TokenUsage{Input: 2, Output: 341, CacheWrite: 29046, Reasoning: 29}
	if turn.Usage != want {
		t.Errorf("usage = %+v, want %+v", turn.Usage, want)
	}
	if turn.SourceRef != "msg_s" {
		t.Errorf("source ref = %q", turn.SourceRef)
	}
}

func TestClaudeParseTranscriptSidechainRecordWithNoAgentIsRefused(t *testing.T) {
	const fixture = `{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:00:00.000Z","isSidechain":true,"message":{"id":"m1","model":"m","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"x"}]}}
`
	batch := parseFinalFixture(t, fixture)
	if batch.Malformed == nil {
		t.Fatalf("a sidechain record naming no agent was accepted; turns=%+v", batch.Turns)
	}
	if batch.Malformed.Reason != MalformedAssistantShape {
		t.Errorf("reason = %q, want %q", batch.Malformed.Reason, MalformedAssistantShape)
	}
	if len(batch.Turns) != 0 || batch.ConsumedBytes != 0 {
		t.Errorf("turns=%d consumed=%d, want nothing emitted and no advance", len(batch.Turns), batch.ConsumedBytes)
	}
}

// The last-snapshot rule rests on input and both cache buckets being fixed for
// a message while output and thinking only grow. A record that breaks that
// means the parser's model of the format is wrong, so it refuses rather than
// reporting a number it cannot stand behind.
func TestClaudeParseTranscriptRefusesAUsageSnapshotItCannotExplain(t *testing.T) {
	base := `{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:00:00.000Z","message":{"id":"m1","model":"m","usage":{"input_tokens":5,"output_tokens":10,"cache_read_input_tokens":1,"cache_creation_input_tokens":2,"output_tokens_details":{"thinking_tokens":3}},"content":[{"type":"text","text":"one"}]}}
`
	// A slice, not a map: this test is named in a committed evidence run, and
	// map iteration order would make that output differ between runs.
	cases := []struct{ name, usage string }{
		{"output went backwards", `{"input_tokens":5,"output_tokens":9,"cache_read_input_tokens":1,"cache_creation_input_tokens":2,"output_tokens_details":{"thinking_tokens":3}}`},
		{"thinking went backwards", `{"input_tokens":5,"output_tokens":11,"cache_read_input_tokens":1,"cache_creation_input_tokens":2,"output_tokens_details":{"thinking_tokens":2}}`},
		{"input changed", `{"input_tokens":6,"output_tokens":11,"cache_read_input_tokens":1,"cache_creation_input_tokens":2,"output_tokens_details":{"thinking_tokens":3}}`},
		{"cache read changed", `{"input_tokens":5,"output_tokens":11,"cache_read_input_tokens":9,"cache_creation_input_tokens":2,"output_tokens_details":{"thinking_tokens":3}}`},
		{"cache write changed", `{"input_tokens":5,"output_tokens":11,"cache_read_input_tokens":1,"cache_creation_input_tokens":9,"output_tokens_details":{"thinking_tokens":3}}`},
	}
	for _, tc := range cases {
		name, usage := tc.name, tc.usage
		t.Run(name, func(t *testing.T) {
			fixture := base + `{"type":"assistant","uuid":"a2","timestamp":"2026-09-08T10:00:01.000Z","message":{"id":"m1","model":"m","usage":` + usage + `,"content":[{"type":"text","text":"two"}]}}` + "\n"
			batch := parseFinalFixture(t, fixture)
			if batch.Malformed == nil {
				t.Fatalf("accepted an unexplainable snapshot; usage = %+v", batch.Turns[0].Usage)
			}
			if batch.Malformed.Reason != MalformedUsageSnapshot {
				t.Errorf("reason = %q, want %q", batch.Malformed.Reason, MalformedUsageSnapshot)
			}
			// The prefix stands: the first record's turn is still withheld
			// because the malformed line stopped the range inside its group.
			if batch.ConsumedBytes != 0 {
				t.Errorf("consumed = %d, want 0: the cursor must not advance through the bad line", batch.ConsumedBytes)
			}
		})
	}
}

// A message group that straddles two harness turns or two subagents cannot be
// attributed. Measured: no real group does either (23,013 groups), so this is
// fail-closed on a shape the parser's model does not contain.
func TestClaudeParseTranscriptRefusesAMessageThatSpansTwoTurns(t *testing.T) {
	const fixture = `{"type":"user","uuid":"u1","timestamp":"2026-09-08T10:00:00.000Z","promptId":"p1","message":{"role":"user","content":"go"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:00:01.000Z","message":{"id":"m1","model":"m","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"one"}]}}
{"type":"user","uuid":"u2","timestamp":"2026-09-08T10:00:02.000Z","promptId":"p2","message":{"role":"user","content":"interrupt"}}
{"type":"assistant","uuid":"a2","timestamp":"2026-09-08T10:00:03.000Z","message":{"id":"m1","model":"m","usage":{"input_tokens":1,"output_tokens":2},"content":[{"type":"text","text":"two"}]}}
`
	batch := parseFinalFixture(t, fixture)
	if batch.Malformed == nil {
		t.Fatalf("a message spanning two harness turns was accepted: %+v", batch.Turns)
	}
	if batch.Malformed.Reason != MalformedTurnIdentitySplit {
		t.Errorf("reason = %q, want %q", batch.Malformed.Reason, MalformedTurnIdentitySplit)
	}
}

// TestLiveClaudeTranscriptCorpusUsageMatchesAnIndependentOracle checks the parser's
// token totals against a second, deliberately naive implementation written in
// this test - a flat scan that knows only "group by message.id, keep the last
// usage". The whole-file corpus replay cannot catch a usage error on its own,
// because its notion of truth is produced by the same parser it is testing; an
// oracle that shares no code with the parser can.
//
// It also asserts the disagreement it exists to protect: where a message's
// records carry a growing snapshot, the parser must match the last-record
// oracle and must NOT match the first-record one. Without that second half the
// test would still pass if both implementations were changed to keep the first
// record.
func TestLiveClaudeTranscriptCorpusUsageMatchesAnIndependentOracle(t *testing.T) {
	requireLive(t)
	var parserTotal, lastTotal, firstTotal TokenUsage
	add := func(dst *TokenUsage, u TokenUsage) {
		dst.Input += u.Input
		dst.Output += u.Output
		dst.CacheRead += u.CacheRead
		dst.CacheWrite += u.CacheWrite
		dst.Reasoning += u.Reasoning
	}
	files, groups, growing := 0, 0, 0

	walkTranscriptCorpus(t, func(path string, data []byte) {
		files++
		for _, turn := range (Claude{}).ParseTranscriptFinal(TranscriptParseState{}, data).Turns {
			add(&parserTotal, turn.Usage)
		}

		// The oracle: no parser code, no shared types beyond the counters.
		type snap struct {
			first, last TokenUsage
			n           int
		}
		order := []string{}
		byID := map[string]*snap{}
		for _, line := range bytes.Split(data, []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var rec struct {
				Type    string `json:"type"`
				Message struct {
					ID    string `json:"id"`
					Usage *struct {
						Input               int64 `json:"input_tokens"`
						Output              int64 `json:"output_tokens"`
						CacheRead           int64 `json:"cache_read_input_tokens"`
						CacheWrite          int64 `json:"cache_creation_input_tokens"`
						OutputTokensDetails *struct {
							Thinking int64 `json:"thinking_tokens"`
						} `json:"output_tokens_details"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant" {
				continue
			}
			if rec.Message.ID == "" || rec.Message.Usage == nil {
				continue
			}
			u := TokenUsage{
				Input: rec.Message.Usage.Input, Output: rec.Message.Usage.Output,
				CacheRead: rec.Message.Usage.CacheRead, CacheWrite: rec.Message.Usage.CacheWrite,
			}
			if d := rec.Message.Usage.OutputTokensDetails; d != nil {
				u.Reasoning = d.Thinking
			}
			s, ok := byID[rec.Message.ID]
			if !ok {
				s = &snap{first: u}
				byID[rec.Message.ID] = s
				order = append(order, rec.Message.ID)
			}
			s.last = u
			s.n++
		}
		for _, id := range order {
			s := byID[id]
			groups++
			if s.first != s.last {
				growing++
			}
			add(&lastTotal, s.last)
			add(&firstTotal, s.first)
		}
	})

	if files == 0 {
		t.Fatal("no session transcript was read; this proved nothing")
	}
	if parserTotal != lastTotal {
		t.Errorf("parser total %+v != last-snapshot oracle %+v", parserTotal, lastTotal)
	}
	if growing == 0 {
		t.Fatal("no message in this corpus has a growing usage snapshot, so this run cannot tell the two rules apart")
	}
	if firstTotal == lastTotal {
		t.Fatal("the two oracles agree, so this run cannot tell the two rules apart")
	}
	if parserTotal == firstTotal {
		t.Errorf("parser total %+v matches the FIRST-record oracle: growing messages are undercounted", parserTotal)
	}
	t.Logf("%d file(s), %d message group(s), %d with a growing snapshot", files, groups, growing)
	t.Logf("parser/last-snapshot total: %+v", lastTotal)
	t.Logf("first-record total:         %+v", firstTotal)
	t.Logf("undercount avoided:         output %d, thinking %d",
		lastTotal.Output-firstTotal.Output, lastTotal.Reasoning-firstTotal.Reasoning)
}

// TestLiveClaudeTranscriptCorpusHarnessTurnRuleHoldsOnRealSessions checks the turn
// rule against the transcripts' own evidence rather than against the parser's
// output: a tool_result record carries the promptId of the turn it belongs to,
// so wherever one appears, the turn the parser assigned to the surrounding
// activity must be the id that record itself names. That is an independent
// check - the parser never reads a tool_result's promptId.
func TestLiveClaudeTranscriptCorpusHarnessTurnRuleHoldsOnRealSessions(t *testing.T) {
	requireLive(t)
	files, checked, unknown := 0, 0, 0
	walkTranscriptCorpus(t, func(path string, data []byte) {
		files++
		batch := Claude{}.ParseTranscriptFinal(TranscriptParseState{}, data)
		// The turn open at each byte offset, from the parser's own turns.
		refAt := func(off int64) (string, bool) {
			var ref string
			var ok bool
			for _, turn := range batch.Turns {
				if turn.Offset <= off {
					ref, ok = turn.HarnessTurnRef, true
					continue
				}
				break
			}
			return ref, ok
		}
		var off int64
		for _, line := range bytes.SplitAfter(data, []byte("\n")) {
			at := off
			off += int64(len(line))
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) == 0 {
				continue
			}
			var rec struct {
				Type     string `json:"type"`
				PromptID string `json:"promptId"`
				Message  struct {
					Content []struct {
						Type string `json:"type"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(trimmed, &rec) != nil || rec.Type != "user" || rec.PromptID == "" {
				continue
			}
			isResult := false
			for _, b := range rec.Message.Content {
				if b.Type == "tool_result" {
					isResult = true
				}
			}
			if !isResult {
				continue
			}
			ref, ok := refAt(at)
			if !ok {
				unknown++
				continue
			}
			checked++
			if ref != rec.PromptID {
				t.Errorf("%s at byte %d: the parser has turn %q open, but this tool_result names %q",
					path, at, ref, rec.PromptID)
			}
		}
	})
	if files == 0 {
		t.Fatal("no session transcript was read; this proved nothing")
	}
	if checked == 0 {
		t.Fatal("no tool_result carried a promptId; this proved nothing")
	}
	t.Logf("%d file(s): %d tool_result record(s) corroborated the open harness turn, %d before any turn was open",
		files, checked, unknown)
}

// A new prompt that carries no readable promptId ends the turn that was open:
// the following work belongs to *something*, but nothing in the transcript says
// what, and inheriting the previous turn would charge a Mate's work on a new
// prompt to the previous Task. agent_turn is immutable, so reconcile could
// never undo that. Unknown is the only honest answer.
//
// The corpus does not currently expose this - all 44 no-id prompts in it occur
// before any turn is open, where clearing and inheriting are the same thing -
// so the corpus oracle staying green is not evidence and this regression is
// deliberately synthetic.
func TestClaudeParseTranscriptPromptWithNoIdClearsTheHarnessTurn(t *testing.T) {
	const fixture = `{"type":"user","uuid":"u1","timestamp":"2026-09-08T10:00:00.000Z","promptId":"p1","message":{"role":"user","content":"first task"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:00:01.000Z","message":{"id":"msg_a","model":"m","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"p1 work"}]}}
{"type":"user","uuid":"u2","timestamp":"2026-09-08T10:00:02.000Z","message":{"role":"user","content":"a new prompt with no id"}}
{"type":"assistant","uuid":"a2","timestamp":"2026-09-08T10:00:03.000Z","message":{"id":"msg_b","model":"m","usage":{"input_tokens":1,"output_tokens":2},"content":[{"type":"text","text":"unattributable work"}]}}
{"type":"user","uuid":"u3","timestamp":"2026-09-08T10:00:04.000Z","promptId":null,"message":{"role":"user","content":[{"type":"image","source":{"type":"base64"}},{"type":"text","text":"an array prompt with a null id"}]}}
{"type":"assistant","uuid":"a3","timestamp":"2026-09-08T10:00:05.000Z","message":{"id":"msg_c","model":"m","usage":{"input_tokens":1,"output_tokens":3},"content":[{"type":"text","text":"also unattributable"}]}}
`
	got := turnRefs(parseFinalFixture(t, fixture))
	if got["msg_a"] != "p1" {
		t.Errorf("msg_a harness turn = %q, want p1", got["msg_a"])
	}
	if got["msg_b"] != "" {
		t.Errorf("msg_b harness turn = %q after a new prompt with no id, want unknown: inheriting %q charges new work to the previous turn",
			got["msg_b"], got["msg_b"])
	}
	if got["msg_c"] != "" {
		t.Errorf("msg_c harness turn = %q after an array prompt with a null id, want unknown", got["msg_c"])
	}
	// The cleared state must be what a resume is handed, too.
	batch := parseFinalFixture(t, fixture)
	if batch.NextState.HarnessTurnRef != "" {
		t.Errorf("next state = %q, want unknown", batch.NextState.HarnessTurnRef)
	}
}

// A tool_result is not a prompt. It reports the outcome of work already inside
// the open turn, so it neither opens one nor ends one - and it must not clear
// the ref just because it carries no promptId, which 188 records in the corpus
// do not. Its id, where present, is corroboration
// (TestLiveClaudeTranscriptCorpusHarnessTurnRuleHoldsOnRealSessions checks it) and
// never a transition: no tool_result in the corpus introduces an id of its own.
func TestClaudeParseTranscriptToolResultNeitherOpensNorClearsTheHarnessTurn(t *testing.T) {
	const fixture = `{"type":"user","uuid":"u1","timestamp":"2026-09-08T10:00:00.000Z","promptId":"p1","message":{"role":"user","content":"go"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:00:01.000Z","message":{"id":"msg_a","model":"m","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"tool_use","id":"toolu_a","name":"Read","input":{}}]}}
{"type":"user","uuid":"u2","timestamp":"2026-09-08T10:00:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_a","content":"out"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-09-08T10:00:03.000Z","message":{"id":"msg_b","model":"m","usage":{"input_tokens":1,"output_tokens":2},"content":[{"type":"text","text":"still p1"}]}}
`
	got := turnRefs(parseFinalFixture(t, fixture))
	if got["msg_b"] != "p1" {
		t.Errorf("msg_b harness turn = %q after a tool_result with no promptId, want p1: a tool result is not a prompt and must not end the turn",
			got["msg_b"])
	}

	// And a tool_result must not open one either: with no opener before it, the
	// turn stays unknown even though the record carries an id.
	const orphan = `{"type":"user","uuid":"u1","timestamp":"2026-09-08T10:00:00.000Z","promptId":"p9","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_x","content":"out"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-09-08T10:00:01.000Z","message":{"id":"msg_x","model":"m","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"x"}]}}
`
	orphanRefs := turnRefs(parseFinalFixture(t, orphan))
	if orphanRefs["msg_x"] != "" {
		t.Errorf("msg_x harness turn = %q, want unknown: a tool_result corroborates a turn, it does not open one", orphanRefs["msg_x"])
	}
}
