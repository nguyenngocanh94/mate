package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// codexTranscripts is Codex's TranscriptSource: its rollouts under
// $CODEX_HOME/sessions, read by appending each new tail to the batch
// before, and its cumulative usage turned into per-call deltas.
type codexTranscripts struct {
	// home is the profile's CODEX_HOME; empty resolves it as a launch does.
	home string
}

// The rules a Codex rollout is located by.
const (
	// codexRuleHerdrSession: Herdr's `agent_session.value`, which for Codex
	// is the rollout's session uuid. Measured 2026-09-20 on Herdr 0.8.2: it
	// is present and correct for every Codex agent and null for every
	// Claude one, so it is a Codex rule and only a Codex rule.
	codexRuleHerdrSession = "herdr.agent_session"
	// codexRuleAdopt: AdoptCodexRollout over the rollout directory, by
	// canonical cwd and launch time. The fallback when Herdr has no session
	// for the agent - a crew whose pane is gone, or a rebuild after the
	// session was deleted.
	codexRuleAdopt = "codex.adopt"
	// codexAdoptPending is the reason no rollout could be adopted yet.
	codexAdoptPending = "rollout_not_adopted"
)

// Locate implements TranscriptSource. A Codex rollout id is not in any file
// mate writes, so it comes from the runtime first, then from the id the
// meta records, and last from adopting a rollout by cwd and launch time.
func (s codexTranscripts) Locate(req harness.TranscriptLocateRequest) (harness.TranscriptLocation, string) {
	root := req.Root
	if root == "" {
		var err error
		if root, err = CodexSessionsDir(s.home); err != nil {
			return harness.TranscriptLocation{}, codexAdoptPending
		}
	}
	if req.RuntimeSession != nil {
		if ref := req.RuntimeSession(); ref != "" {
			if path, ok := codexRolloutNamed(root, ref); ok {
				return harness.TranscriptLocation{Path: path, SessionID: ref, Rule: codexRuleHerdrSession}, ""
			}
		}
	}
	if path, ok := codexRolloutNamed(root, req.SessionID); ok {
		return harness.TranscriptLocation{Path: path, Rule: codexRuleHerdrSession}, ""
	}
	return codexAdopt(root, req)
}

// codexRolloutNamed finds the rollout whose file name carries a session
// id. Codex names a rollout `rollout-<timestamp>-<session-id>.jsonl`, so the
// id is in the name and no file has to be opened to find the right one.
func codexRolloutNamed(root, sessionID string) (string, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", false
	}
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return nil //nolint:nilerr // an unreadable branch is not an answer
		}
		name := d.Name()
		if strings.HasSuffix(name, ".jsonl") && strings.Contains(name, sessionID) {
			found = path
		}
		return nil
	})
	return found, found != ""
}

// codexAdopt is AdoptCodexRollout over every rollout whose header parses,
// with the agent's canonical cwd and its recorded launch time. The rule is
// the v1 one and is deliberately conservative: an ambiguous match is no
// match, because two crews launched in the same worktree really are
// indistinguishable from the rollout alone.
func codexAdopt(root string, req harness.TranscriptLocateRequest) (harness.TranscriptLocation, string) {
	candidates := codexAdoptionCandidates(root)
	if len(candidates) == 0 {
		return harness.TranscriptLocation{}, codexAdoptPending
	}
	canonical := req.Cwd
	if resolved, err := filepath.EvalSymlinks(req.Cwd); err == nil {
		canonical = resolved
	}
	adoption := AdoptCodexRollout(candidates, canonical, req.LaunchedAt, req.SessionID)
	if adoption.Status != CodexAdoptionKnown {
		return harness.TranscriptLocation{}, codexAdoptPending
	}
	return harness.TranscriptLocation{Path: adoption.Candidate.Path, SessionID: adoption.Candidate.Meta.SessionID, Rule: codexRuleAdopt}, ""
}

// codexAdoptionCandidates is every rollout whose first record is a
// session_meta this build understands. It is read fresh each time it is
// needed rather than cached: a crew spawned during a console's run writes
// its rollout after the console started.
func codexAdoptionCandidates(root string) []CodexRolloutCandidate {
	var out []CodexRolloutCandidate
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil //nolint:nilerr // an unreadable branch is skipped, not fatal
		}
		head, err := readFileHead(path, 64*1024)
		if err != nil {
			return nil //nolint:nilerr
		}
		meta, err := ParseCodexSessionMeta(head)
		if err != nil {
			return nil //nolint:nilerr
		}
		out = append(out, CodexRolloutCandidate{Path: path, Meta: meta})
		return nil
	})
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out
}

// Read implements TranscriptSource. A rollout record is complete on its
// own line and its ordinal is its identity, so a file that has only grown
// is read from where the prior batch stopped, with the prior's parser
// state, and the tail appended. Any other change reads the file whole.
// AtRest changes nothing: Codex has no multi-record message to withhold.
func (s codexTranscripts) Read(req harness.TranscriptReadRequest) (harness.TranscriptBatch, error) {
	if req.Grown && req.Prior.Malformed == nil {
		info, err := os.Stat(req.Path)
		if err != nil {
			return harness.TranscriptBatch{}, err
		}
		if base := req.Prior.ConsumedBytes; base <= info.Size() {
			data, err := readFileFrom(req.Path, base, info.Size())
			if err != nil {
				return harness.TranscriptBatch{}, err
			}
			b := appendCodexBatch(req.Prior, (Codex{}).ParseTranscript(req.Prior.NextState, data), base)
			normaliseCodex(&b)
			return b, nil
		}
	}
	data, err := os.ReadFile(req.Path)
	if err != nil {
		return harness.TranscriptBatch{}, err
	}
	b := (Codex{}).ParseTranscript(harness.TranscriptParseState{}, data)
	normaliseCodex(&b)
	return b, nil
}

// codexTranscriptState is the Codex half of a batch that only Codex reads:
// every cumulative usage snapshot of the file so far, which the usage turns
// are derived from and a tail read appends to.
type codexTranscriptState struct {
	snapshots []CodexUsageSnapshot
}

// codexSnapshots are the usage snapshots a Codex parse left in its batch.
func codexSnapshots(b harness.TranscriptBatch) []CodexUsageSnapshot {
	st, _ := b.HarnessState.(codexTranscriptState)
	return st.snapshots
}

// appendCodexBatch appends a tail parsed from base to the batch before it.
// Codex source refs and ordinals are native, absolute record identities;
// only byte offsets need rebasing. The parser already guarantees every
// emitted fact precedes ConsumedBytes.
func appendCodexBatch(prior, tail harness.TranscriptBatch, base int64) harness.TranscriptBatch {
	for n := range tail.Records {
		tail.Records[n].Offset += base
	}
	for n := range tail.Turns {
		tail.Turns[n].Offset += base
	}
	for n := range tail.ToolCalls {
		tail.ToolCalls[n].Offset += base
	}
	for n := range tail.ToolResults {
		tail.ToolResults[n].Offset += base
	}
	snapshots := codexSnapshots(tail)
	for n := range snapshots {
		snapshots[n].Offset += base
	}
	for n := range tail.UsageFailures {
		tail.UsageFailures[n].Offset += base
	}
	prior.Records = append(prior.Records, tail.Records...)
	prior.Turns = append(prior.Turns, tail.Turns...)
	prior.ToolCalls = append(prior.ToolCalls, tail.ToolCalls...)
	prior.ToolResults = append(prior.ToolResults, tail.ToolResults...)
	prior.HarnessState = codexTranscriptState{snapshots: append(codexSnapshots(prior), snapshots...)}
	prior.UsageFailures = append(prior.UsageFailures, tail.UsageFailures...)
	prior.ConsumedBytes, prior.TotalBytes, prior.PendingBytes = base+tail.ConsumedBytes, base+tail.TotalBytes, tail.PendingBytes
	prior.NextState, prior.Malformed = tail.NextState, tail.Malformed
	if prior.Malformed != nil {
		prior.Malformed.Offset += base
		prior.Malformed.EndOffset += base
	}
	for kind, count := range tail.Skipped {
		prior.Skipped[kind] += count
	}
	return prior
}

// normaliseCodex derives a batch's usage turns from its cumulative
// snapshots, one per `token_count` record: the group of work between two of
// them is one model call, which is the same unit a Claude assistant
// message group is. Codex's own `task_started`/`task_complete` pair is the
// larger harness turn - a whole prompt and everything it caused - and is
// carried on HarnessTurnRef rather than made the turn, so both harnesses
// answer "what did one model call cost" the same way.
//
// Tokens come from the snapshot's delta, because Codex reports cumulative
// totals; ContextTokens comes from the same record's `last_token_usage`,
// which is the prompt the call actually carried.
//
// Usage.Input is stored net of the cache-read delta, not the raw delta
// Codex reports. Measured 2026-09-20 on the M5 acceptance fixture: the
// rollout's last `total_token_usage` is
// `{"input_tokens":232424,"cached_input_tokens":209152,...,"output_tokens":1544,"total_tokens":233968}`,
// and 232424+1544 = 233968 exactly - Codex's own `input_tokens` already
// counts every cached token, and `total_tokens` is simply input+output. A
// Claude turn is the opposite: its `input_tokens` excludes both cache
// buckets, so `input+cache_read+cache_write+output` is that call's real
// cost with no overlap (docs/timeline.md's own turn.started example: 32
// fresh + 57690 cache-read + 739 cache-write = 58461 = context_tokens_after).
// Subtracting the cache-read delta here makes Input mean the same thing for
// both harnesses - "billed at the input rate, not a cache rate" - so every
// sum across the four buckets (`v_task_ledger`, `v_now.tokens_today`, the
// budget check, `mate usage`) is correct without asking which harness a turn
// came from. Cache-write is left alone: Codex's own ContextTokens
// derivation (`marks.lastTokenUsage`, below) already treats it as
// additional rather than a subset of input, and the fixture's cache-write is
// always 0, so there is nothing to measure it against.
func normaliseCodex(b *harness.TranscriptBatch) {
	marks := scanCodexMarks(*b)

	// A rollout's own session start is where the first turn begins when
	// nothing earlier dates it.
	sessionStart := time.Time{}
	if len(b.Records) > 0 {
		sessionStart = b.Records[0].OccurredAt
	}

	b.UsageTurns, b.UnpricedCalls, b.Compactions = nil, nil, nil
	prevEnd := sessionStart
	prevOffset := int64(-1)
	for _, snap := range codexSnapshots(*b) {
		started := prevEnd
		if mark, ok := marks.lastTaskStartedBefore(snap.Offset); ok && mark.at.After(started) {
			started = mark.at
		}
		if started.IsZero() || started.After(snap.OccurredAt) {
			started = snap.OccurredAt
		}

		var calls []harness.TranscriptToolCall
		for _, call := range b.ToolCalls {
			if call.Offset > prevOffset && call.Offset <= snap.Offset {
				calls = append(calls, call)
			}
		}
		outcome := "end_turn"
		if len(calls) > 0 {
			outcome = "tool_use"
		}
		if _, ok := marks.taskComplete[snap.Offset]; ok {
			outcome = "end_turn"
		}
		// Cumulative.Input is cache-inclusive (see the function doc), so the
		// fallback context size adds only the cache-write bucket - the same
		// combination the primary `last_token_usage` rule below uses.
		contextAfter := snap.Cumulative.Input + snap.Cumulative.CacheWrite
		if last, ok := marks.lastTokenUsage[snap.Offset]; ok {
			contextAfter = last
		}
		// freshInput is this call's input delta net of its cache-read delta:
		// the portion Codex billed at the input rate rather than the cache
		// rate. Clamped at zero defensively - Codex's own invariant is
		// cached_input_tokens <= input_tokens at every cumulative snapshot,
		// so a negative result here would mean that invariant broke, not
		// that the crew somehow un-cached tokens.
		freshInput := snap.Delta.Input - snap.Delta.CacheRead
		if freshInput < 0 {
			freshInput = 0
		}
		usage := snap.Delta
		usage.Input = freshInput
		b.UsageTurns = append(b.UsageTurns, harness.UsageTurn{
			SourceRef: snap.SourceRef, Offset: snap.Offset,
			StartedAt: started, EndedAt: snap.OccurredAt,
			Outcome: outcome, Model: snap.Model, HarnessTurnRef: snap.HarnessTurnRef,
			Usage: usage, ContextTokens: contextAfter, Calls: calls,
			// Cumulative, because that is what the record holds: keeping the
			// raw shape is the reason usage_sample exists (M5's "lựa chọn có
			// chủ ý"), and the delta beside it is this parser's arithmetic.
			Sample: harness.UsageSample{At: snap.OccurredAt, Cumulative: true, Usage: harness.TokenUsage{
				Input: snap.Cumulative.Input, CacheRead: snap.Cumulative.CacheRead,
				CacheWrite: snap.Cumulative.CacheWrite, Output: snap.Cumulative.Output,
				Reasoning: snap.Cumulative.Reasoning,
			}},
		})
		prevEnd, prevOffset = snap.OccurredAt, snap.Offset
	}

	// Tool calls after the last snapshot are real work the rollout has not
	// yet priced.
	for _, call := range b.ToolCalls {
		if call.Offset > prevOffset {
			b.UnpricedCalls = append(b.UnpricedCalls, call)
		}
	}

	// Codex marks a compaction with a top-level `compacted` record.
	for _, rec := range b.Records {
		if !strings.Contains(rec.RawJSON, `"compacted"`) {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(rec.RawJSON), &probe) == nil && probe.Type == "compacted" {
			b.Compactions = append(b.Compactions, harness.TranscriptCompaction{Offset: rec.Offset, OccurredAt: rec.OccurredAt, Trigger: "codex.compacted"})
		}
	}
}

// codexMark is one dated rollout record the turn boundaries need.
type codexMark struct {
	offset int64
	at     time.Time
}

type codexMarks struct {
	taskStarted  []codexMark
	taskComplete map[int64]codexMark
	// lastTokenUsage is the prompt the priced call carried, from the
	// `last_token_usage` block of a `token_count` record. CodexUsageSnapshot
	// carries only the cumulative total and the delta, so this is read from
	// the raw record.
	lastTokenUsage map[int64]int64
}

func (m codexMarks) lastTaskStartedBefore(offset int64) (codexMark, bool) {
	var out codexMark
	found := false
	for _, mark := range m.taskStarted {
		if mark.offset <= offset {
			out, found = mark, true
			continue
		}
		break
	}
	return out, found
}

func scanCodexMarks(b harness.TranscriptBatch) codexMarks {
	marks := codexMarks{taskComplete: map[int64]codexMark{}, lastTokenUsage: map[int64]int64{}}
	for _, rec := range b.Records {
		if !strings.Contains(rec.RawJSON, `"task_started"`) && !strings.Contains(rec.RawJSON, `"task_complete"`) && !strings.Contains(rec.RawJSON, `"token_count"`) {
			continue
		}
		var env struct {
			Type    string `json:"type"`
			Payload struct {
				Type string `json:"type"`
				Info struct {
					Last *struct {
						Input      int64 `json:"input_tokens"`
						CacheWrite int64 `json:"cache_write_input_tokens"`
					} `json:"last_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(rec.RawJSON), &env) != nil || env.Type != "event_msg" {
			continue
		}
		switch env.Payload.Type {
		case "task_started":
			marks.taskStarted = append(marks.taskStarted, codexMark{offset: rec.Offset, at: rec.OccurredAt})
		case "task_complete":
			marks.taskComplete[rec.Offset] = codexMark{offset: rec.Offset, at: rec.OccurredAt}
		case "token_count":
			if last := env.Payload.Info.Last; last != nil {
				// Codex's input_tokens already includes the cached part, so
				// it is the prompt size; the cache-write bucket is the part
				// of it the call paid to write.
				marks.lastTokenUsage[rec.Offset] = last.Input + last.CacheWrite
			}
		}
	}
	sort.Slice(marks.taskStarted, func(i, j int) bool { return marks.taskStarted[i].offset < marks.taskStarted[j].offset })
	return marks
}

// Telemetry implements TranscriptSource. Codex's rollout carries native
// observations - exec wrappers, response timing - that the token ledger
// does not, so they are parsed on their own from the telemetry cursor on.
func (s codexTranscripts) Telemetry(req harness.TelemetryRequest) (harness.TelemetryUpdate, error) {
	up := harness.TelemetryUpdate{Offset: req.Offset, State: req.State, Gaps: []string{"child_usage_unavailable", "wrapper_parent_unavailable"}}
	if req.Offset >= req.Size {
		return up, nil
	}
	data, err := readFileFrom(req.Path, req.Offset, req.Size)
	if err != nil {
		return harness.TelemetryUpdate{}, err
	}
	b := ParseCodexTelemetry(req.State, data, req.Offset)
	up.State, up.Offset, up.Facts, up.Error = b.State, req.Offset+b.Consumed, b.Facts, b.Error
	return up, nil
}

// readFileFrom reads a file from offset to size, the size a caller has
// just measured. It returns what it could read when the file shrank under
// it; only a read that found nothing at all is an error.
func readFileFrom(path string, offset, size int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data := make([]byte, size-offset)
	n, err := f.ReadAt(data, offset)
	if err != nil && n == 0 {
		return nil, err
	}
	return data[:n], nil
}
