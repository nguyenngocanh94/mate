package timeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
)

// ingestTranscripts reads every agent's transcript into turns, tool calls,
// usage samples and the events that go with them.
//
// A transcript is re-read whole on every pass rather than tailed from a
// cursor. That is deliberate: harness.ParseTranscript withholds the trailing
// message group because a later write may still extend it (see
// internal/harness/transcript.go), so a tail would have to carry the parser's
// own state across passes and a resume that got it wrong would charge one
// turn's tokens to another, permanently. Re-reading costs one JSON pass over
// a file that is hundreds of kilobytes at most, and every fact carries a
// natural key, so nothing is recorded twice. `cursor` still records how far
// the parser trusted the file, which is what makes a withheld group visible
// rather than silent.
func (p *pass) ingestTranscripts(ctx context.Context) error {
	p.statusClock = map[string][]datedCommand{}
	p.commitSightings = map[string][]commitSighting{}

	if p.mate.Present {
		loc, reason := p.locateMate()
		if reason != "" {
			p.unresolved(p.mate.ActorID, string(loc.Kind), reason)
		} else if err := p.ingestOneTranscript(ctx, loc); err != nil {
			return err
		}
	}
	for _, crew := range p.crews {
		loc, reason := p.locateCrew(ctx, crew)
		if reason != "" {
			p.unresolved(crew.ActorID, string(loc.Kind), reason)
			continue
		}
		if err := p.ingestOneTranscript(ctx, loc); err != nil {
			return err
		}
	}
	return nil
}

func (p *pass) ingestOneTranscript(ctx context.Context, loc Located) error {
	data, err := os.ReadFile(loc.Path)
	if err != nil {
		if os.IsNotExist(err) {
			p.unresolved(loc.ActorID, string(loc.Kind), unresolvedNoFile)
			return nil
		}
		return err
	}
	parser, err := transcriptParser(loc.Kind)
	if err != nil {
		p.unresolved(loc.ActorID, string(loc.Kind), unresolvedNoHarness)
		return nil
	}
	// A zero state and the whole file: the parser is pure over the bytes it
	// is handed, so this is the same parse a resume would have produced with
	// a correctly carried state, without the risk of carrying one wrongly.
	tb := parser.ParseTranscript(harness.TranscriptParseState{}, data)

	sessionID := loc.SessionID
	if sessionID == "" {
		sessionID = loc.Path
	}
	rowID := sessionRowID(loc.ActorID, sessionID)
	p.b.session(pendingSession{
		ID: rowID, ActorID: loc.ActorID, HarnessSession: loc.SessionID, TranscriptPath: loc.Path,
	})
	p.b.cursor(loc.Path, tb.ConsumedBytes)

	switch loc.Kind {
	case harness.KindClaude:
		p.claudeTurns(loc, rowID, tb)
	case harness.KindCodex:
		p.codexTurns(loc, rowID, tb)
	}
	p.compactions(loc, rowID, tb)
	return nil
}

func transcriptParser(kind harness.Kind) (harness.TranscriptParser, error) {
	switch kind {
	case harness.KindClaude:
		return harness.Claude{}, nil
	case harness.KindCodex:
		return harness.Codex{}, nil
	default:
		return nil, fmt.Errorf("timeline: no transcript parser for harness %q", kind)
	}
}

// ---------------------------------------------------------------- Claude

// claudeTurns records one turn per assistant message group, which is one API
// response: the unit Claude restates usage on, and the unit
// harness.ParseTranscript already keys by message id.
//
// context_tokens_after is the input side of that response - fresh input plus
// both cache buckets - because that is what was carried into the call and
// therefore what the context held when it returned.
func (p *pass) claudeTurns(loc Located, sessionRow string, tb harness.TranscriptBatch) {
	results := resultsByCall(tb)
	callsByTurn := map[string][]harness.TranscriptToolCall{}
	for _, call := range tb.ToolCalls {
		callsByTurn[call.TurnSourceRef] = append(callsByTurn[call.TurnSourceRef], call)
	}

	for ordinal, t := range tb.Turns {
		turnID := turnRowID(sessionRow, t.SourceRef)
		calls := callsByTurn[t.SourceRef]
		ended := t.OccurredAt
		for _, call := range calls {
			if res, ok := results[call.SourceRef]; ok && res.CompletedAt.After(ended) {
				ended = res.CompletedAt
			} else if call.StartedAt.After(ended) {
				ended = call.StartedAt
			}
		}
		p.b.turn(pendingTurn{
			ID: turnID, ActorID: loc.ActorID, SessionID: sessionRow, Ordinal: ordinal,
			StartedAt: t.OccurredAt, EndedAt: ended,
			Outcome: t.StopReason, Model: t.Model, HarnessTurnRef: t.HarnessTurnRef,
			Input: t.Usage.Input, CacheRead: t.Usage.CacheRead, CacheWrite: t.Usage.CacheWrite,
			Output: t.Usage.Output, Thinking: t.Usage.Reasoning,
			ContextAfter: t.Usage.ContextTokens(), ToolCount: len(calls),
			RefPath: loc.Path, RefOffset: t.Offset,
		})
		p.b.usageSample(pendingUsage{
			ID: usageRowID(sessionRow, t.Offset), SessionID: sessionRow, At: t.OccurredAt,
			Cumulative: false,
			Input:      t.Usage.Input, CacheRead: t.Usage.CacheRead, CacheWrite: t.Usage.CacheWrite,
			Output: t.Usage.Output, Thinking: t.Usage.Reasoning,
			RefPath: loc.Path, RefOffset: t.Offset,
		})
		p.turnEvents(loc, turnID, t.OccurredAt, ended, map[string]any{
			"model":                t.Model,
			"harness_turn":         t.HarnessTurnRef,
			"outcome":              t.StopReason,
			"tool_count":           len(calls),
			"input_tokens":         t.Usage.Input,
			"cache_read_tokens":    t.Usage.CacheRead,
			"cache_write_tokens":   t.Usage.CacheWrite,
			"output_tokens":        t.Usage.Output,
			"thinking_tokens":      t.Usage.Reasoning,
			"context_tokens_after": t.Usage.ContextTokens(),
		}, t.Offset)
		for _, call := range calls {
			p.toolAction(loc, sessionRow, turnID, call, results)
		}
	}
}

// ---------------------------------------------------------------- Codex

// codexTurns records one turn per `token_count` record: the group of work
// between two of them is one model call, which is the same unit a Claude
// assistant message group is. Codex's own `task_started`/`task_complete`
// pair is the larger harness turn - a whole prompt and everything it caused -
// and is carried on `turn.harness_turn_ref` rather than made the turn, so
// both harnesses answer "what did one model call cost" the same way.
//
// Tokens come from the snapshot's delta, because Codex reports cumulative
// totals; `context_tokens_after` comes from the same record's
// `last_token_usage`, which is the prompt the call actually carried.
func (p *pass) codexTurns(loc Located, sessionRow string, tb harness.TranscriptBatch) {
	results := resultsByCall(tb)
	marks := scanCodexMarks(tb)

	// A rollout's own session start is where the first turn begins when
	// nothing earlier dates it.
	sessionStart := time.Time{}
	if len(tb.Records) > 0 {
		sessionStart = tb.Records[0].OccurredAt
	}

	prevEnd := sessionStart
	prevOffset := int64(-1)
	for ordinal, snap := range tb.CodexUsageSnapshots {
		started := prevEnd
		if mark, ok := marks.lastTaskStartedBefore(snap.Offset); ok && mark.at.After(started) {
			started = mark.at
		}
		if started.IsZero() || started.After(snap.OccurredAt) {
			started = snap.OccurredAt
		}
		turnID := turnRowID(sessionRow, snap.SourceRef)

		var calls []harness.TranscriptToolCall
		for _, call := range tb.ToolCalls {
			if call.Offset > prevOffset && call.Offset <= snap.Offset {
				calls = append(calls, call)
			}
		}
		outcome := "end_turn"
		if len(calls) > 0 {
			outcome = "tool_use"
		}
		if _, ok := marks.taskCompleteAt(snap.Offset); ok {
			outcome = "end_turn"
		}
		contextAfter := snap.Cumulative.Input + snap.Cumulative.CacheRead + snap.Cumulative.CacheWrite
		if last, ok := marks.lastTokenUsage[snap.Offset]; ok {
			contextAfter = last
		}
		p.b.turn(pendingTurn{
			ID: turnID, ActorID: loc.ActorID, SessionID: sessionRow, Ordinal: ordinal,
			StartedAt: started, EndedAt: snap.OccurredAt,
			Outcome: outcome, Model: snap.Model, HarnessTurnRef: snap.HarnessTurnRef,
			Input: snap.Delta.Input, CacheRead: snap.Delta.CacheRead, CacheWrite: snap.Delta.CacheWrite,
			Output: snap.Delta.Output, Thinking: snap.Delta.Reasoning,
			ContextAfter: contextAfter, ToolCount: len(calls),
			RefPath: loc.Path, RefOffset: snap.Offset,
		})
		p.b.usageSample(pendingUsage{
			ID: usageRowID(sessionRow, snap.Offset), SessionID: sessionRow, At: snap.OccurredAt,
			// Cumulative, because that is what the record holds: keeping the
			// raw shape is the reason usage_sample exists (M5's "lựa chọn có
			// chủ ý"), and the delta beside it is this parser's arithmetic.
			Cumulative: true,
			Input:      snap.Cumulative.Input, CacheRead: snap.Cumulative.CacheRead,
			CacheWrite: snap.Cumulative.CacheWrite, Output: snap.Cumulative.Output,
			Thinking: snap.Cumulative.Reasoning,
			RefPath:  loc.Path, RefOffset: snap.Offset,
		})
		p.turnEvents(loc, turnID, started, snap.OccurredAt, map[string]any{
			"model":                snap.Model,
			"harness_turn":         snap.HarnessTurnRef,
			"outcome":              outcome,
			"tool_count":           len(calls),
			"input_tokens":         snap.Delta.Input,
			"cache_read_tokens":    snap.Delta.CacheRead,
			"cache_write_tokens":   snap.Delta.CacheWrite,
			"output_tokens":        snap.Delta.Output,
			"thinking_tokens":      snap.Delta.Reasoning,
			"context_tokens_after": contextAfter,
		}, snap.Offset)
		for _, call := range calls {
			p.toolAction(loc, sessionRow, turnID, call, results)
		}
		prevEnd, prevOffset = snap.OccurredAt, snap.Offset
	}

	// Tool calls after the last snapshot are real work the rollout has not
	// yet priced. They get an action with no turn rather than being dropped:
	// the live test asks that every tool call in the transcript have one.
	for _, call := range tb.ToolCalls {
		if call.Offset > prevOffset {
			p.toolAction(loc, sessionRow, "", call, results)
		}
	}
}

// codexMark is one dated rollout record the turn boundaries need.
type codexMark struct {
	offset int64
	at     time.Time
	turnID string
}

type codexMarks struct {
	taskStarted  []codexMark
	taskComplete map[int64]codexMark
	// lastTokenUsage is the prompt the priced call carried, from the
	// `last_token_usage` block of a `token_count` record. harness's
	// CodexUsageSnapshot carries only the cumulative total and the delta, so
	// this is read from the raw record rather than by changing the parser.
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

func (m codexMarks) taskCompleteAt(offset int64) (codexMark, bool) {
	mark, ok := m.taskComplete[offset]
	return mark, ok
}

func scanCodexMarks(tb harness.TranscriptBatch) codexMarks {
	marks := codexMarks{taskComplete: map[int64]codexMark{}, lastTokenUsage: map[int64]int64{}}
	for _, rec := range tb.Records {
		var env struct {
			Type    string `json:"type"`
			Payload struct {
				Type   string `json:"type"`
				TurnID string `json:"turn_id"`
				Info   struct {
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
			marks.taskStarted = append(marks.taskStarted,
				codexMark{offset: rec.Offset, at: rec.OccurredAt, turnID: env.Payload.TurnID})
		case "task_complete":
			marks.taskComplete[rec.Offset] = codexMark{offset: rec.Offset, at: rec.OccurredAt, turnID: env.Payload.TurnID}
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

// ---------------------------------------------------------------- shared

func resultsByCall(tb harness.TranscriptBatch) map[string]harness.TranscriptToolResult {
	out := make(map[string]harness.TranscriptToolResult, len(tb.ToolResults))
	for _, res := range tb.ToolResults {
		if _, seen := out[res.CallID]; seen {
			continue
		}
		out[res.CallID] = res
	}
	return out
}

func (p *pass) turnEvents(loc Located, turnID string, started, ended time.Time, payload map[string]any, offset int64) {
	p.b.event(pendingEvent{
		Dedup:   dedup(KindTurnStarted, turnID),
		Project: p.project, At: started, ActorID: loc.ActorID, Kind: KindTurnStarted,
		TurnID: turnID, TaskActor: p.taskActor(loc.ActorID), Payload: payload,
		RefPath: loc.Path, RefOffset: offset,
	})
	done := map[string]any{}
	for k, v := range payload {
		done[k] = v
	}
	done["duration_ms"] = ended.Sub(started).Milliseconds()
	p.b.event(pendingEvent{
		Dedup:   dedup(KindTurnEnded, turnID),
		Project: p.project, At: ended, ActorID: loc.ActorID, Kind: KindTurnEnded,
		TurnID: turnID, TaskActor: p.taskActor(loc.ActorID), Payload: done,
		RefPath: loc.Path, RefOffset: offset,
	})
}

// toolAction records one tool call as an `action` row plus the two events of
// the vocabulary: `tool.called` when it was issued and `tool.finished` when
// the transcript carries its result. A call with no result yet is in-flight,
// not failed, so it gets the row and the first event and nothing else.
func (p *pass) toolAction(loc Located, sessionRow, turnID string,
	call harness.TranscriptToolCall, results map[string]harness.TranscriptToolResult) {

	target, summary, class := toolTarget(loc.Kind, call)
	actionID := actionRowID(sessionRow, call.SourceRef)
	calledDedup := dedup(KindToolCalled, actionID)

	action := pendingAction{
		ID: actionID, TurnID: turnID, EventDedup: calledDedup, ActorID: loc.ActorID,
		At: call.StartedAt, Tool: call.ToolName, Target: target, Summary: summary,
	}
	payload := map[string]any{
		"tool":   call.ToolName,
		"class":  string(class),
		"target": target,
	}
	p.b.event(pendingEvent{
		Dedup:   calledDedup,
		Project: p.project, At: call.StartedAt, ActorID: loc.ActorID, Kind: KindToolCalled,
		TurnID: turnID, TaskActor: p.taskActor(loc.ActorID), Payload: payload,
		RefPath: loc.Path, RefOffset: call.Offset,
	})
	if res, ok := results[call.SourceRef]; ok {
		duration := res.CompletedAt.Sub(call.StartedAt).Milliseconds()
		okFlag := !res.IsError
		action.EndedAt = res.CompletedAt
		action.DurationMS = &duration
		action.OK = &okFlag
		p.b.event(pendingEvent{
			Dedup:   dedup(KindToolFinished, actionID),
			Project: p.project, At: res.CompletedAt, ActorID: loc.ActorID, Kind: KindToolFinished,
			TurnID: turnID, TaskActor: p.taskActor(loc.ActorID),
			Payload: map[string]any{
				"tool": call.ToolName, "target": target,
				"ok": okFlag, "duration_ms": duration, "output_bytes": res.OutputBytes,
			},
			RefPath: loc.Path, RefOffset: res.Offset,
		})
	}
	p.b.action(action)

	// A crew's status lines have no timestamp of their own; the shell command
	// that wrote one does. Every shell command a crew ran is remembered here
	// so ingestStatus can date the line it echoed.
	if class == harness.CommandShell && summary != "" {
		p.statusClock[loc.ActorID] = append(p.statusClock[loc.ActorID],
			datedCommand{command: summary, at: call.StartedAt})
		if res, ok := results[call.SourceRef]; ok && !res.IsError {
			if sha, found := commitSha(summary, res.OutputText); found {
				p.commitSightings[loc.ActorID] = append(p.commitSightings[loc.ActorID],
					commitSighting{sha: sha, at: res.CompletedAt})
			}
		}
	}
}

// gitCommitEcho is the line `git commit` prints back: `[<branch> <sha>] <subject>`.
// It is git's own format and has not changed in a decade, which is why this
// reads it rather than asking git again - by the time anything could ask, the
// branch may be gone.
var gitCommitEcho = regexp.MustCompile(`\[[^\]\s]+ (?:\(root-commit\) )?([0-9a-f]{7,40})\]`)

// commitSha is the commit a shell action made, when its command ran
// `git commit` and git echoed a sha back.
//
// This exists because a crew's branch is short-lived. `matev2 merge` deletes
// it on its way out, so a crew that commits a few seconds before somebody
// merges leaves no window in which a five-second poll could read
// `git log <default>..<branch>` - measured 2026-09-20, six seconds between the
// commit and the merge, and the run that depended on catching it recorded no
// commit at all. The transcript keeps the sighting for ever.
func commitSha(command, output string) (string, bool) {
	if !strings.Contains(command, "git commit") && !strings.Contains(command, "git -C") {
		return "", false
	}
	match := gitCommitEcho.FindStringSubmatch(output)
	if match == nil {
		return "", false
	}
	return match[1], true
}

// compactions records `context.compacted`: the moment a harness threw the
// conversation away and replaced it with a summary, which is the one event
// that explains a context size falling instead of rising.
//
// Claude marks it on the summary record itself (`isCompactSummary`) or with a
// `compact_boundary` system record; Codex writes a top-level `compacted`
// record. Both are read from the raw record, because
// harness.TranscriptBatch normalises them into ordinary records and the
// marker is the only thing that distinguishes them.
func (p *pass) compactions(loc Located, sessionRow string, tb harness.TranscriptBatch) {
	for _, rec := range tb.Records {
		var probe struct {
			Type             string          `json:"type"`
			Subtype          string          `json:"subtype"`
			IsCompactSummary bool            `json:"isCompactSummary"`
			Payload          json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(rec.RawJSON), &probe) != nil {
			continue
		}
		trigger := ""
		switch {
		case probe.IsCompactSummary:
			trigger = "claude.compact_summary"
		case probe.Type == "system" && probe.Subtype == "compact_boundary":
			trigger = "claude.compact_boundary"
		case probe.Type == "compacted":
			trigger = "codex.compacted"
		}
		if trigger == "" {
			continue
		}
		p.b.event(pendingEvent{
			Dedup:   dedup(KindContextCompac, sessionRow, fmt.Sprint(rec.Offset)),
			Project: p.project, At: rec.OccurredAt, ActorID: loc.ActorID, Kind: KindContextCompac,
			TaskActor: p.taskActor(loc.ActorID),
			Payload:   map[string]any{"trigger": trigger, "harness": string(loc.Kind)},
			RefPath:   loc.Path, RefOffset: rec.Offset,
		})
	}
}

// taskActor is the task an actor's work belongs to: a crew's own, and none
// for the Mate, whose turns belong to the project rather than to one task.
func (p *pass) taskActor(actorID string) string {
	if strings.HasPrefix(actorID, ActorCrew+":") {
		return actorID
	}
	return ""
}

func turnRowID(sessionRow, sourceRef string) string   { return sessionRow + "#turn#" + sourceRef }
func actionRowID(sessionRow, sourceRef string) string { return sessionRow + "#tool#" + sourceRef }
func usageRowID(sessionRow string, offset int64) string {
	return fmt.Sprintf("%s#usage#%d", sessionRow, offset)
}

// targetRunes is how much of a shell command becomes `action.target`. It is
// the M5 rule: a file path in full, and the first 80 runes of a command,
// which is enough to recognise `git commit -m …` in a list and short enough
// that the column is readable.
const targetRunes = 80

// summaryRunes bounds `action.summary`. It is generous because a crew's
// status lines are dated by finding them inside the command that echoed them
// (see ingestStatus), and a truncated command loses that.
const summaryRunes = 4000

// toolTarget turns one tool call into the two strings a reader wants: what it
// touched, and enough of how to recognise it.
func toolTarget(kind harness.Kind, call harness.TranscriptToolCall) (target, summary string, class harness.CommandClass) {
	body := toolBody(call.InputJSON)
	fields := decodeFields(call.InputJSON)
	class = call.CommandClass
	// Codex runs an edit through the same `exec` tool as a shell command, by
	// handing it a script that builds a patch (measured 2026-09-20,
	// codex-cli 0.154). The patch is the interesting half, so the file it
	// names is the target even though the tool's class says shell.
	if patchFile, ok := patchTarget(body); ok {
		return patchFile, truncateRunes(body, summaryRunes), harness.CommandEdit
	}

	switch call.CommandClass {
	case harness.CommandShell:
		command := shellCommand(kind, body, fields)
		return truncateRunes(oneLine(command), targetRunes), truncateRunes(command, summaryRunes), class
	case harness.CommandRead, harness.CommandEdit:
		if path := firstField(fields, "file_path", "path", "notebook_path", "filePath"); path != "" {
			return shortPath(path), truncateRunes(oneLine(body), summaryRunes), class
		}
		if patch := firstField(fields, "input", "patch"); patch != "" {
			return truncateRunes(oneLine(applyPatchFile(patch)), targetRunes), truncateRunes(patch, summaryRunes), class
		}
	case harness.CommandSearch:
		pattern := firstField(fields, "pattern", "query", "q")
		where := firstField(fields, "path", "glob")
		if pattern != "" && where != "" {
			return truncateRunes(pattern+" in "+where, targetRunes), truncateRunes(oneLine(body), summaryRunes), class
		}
		if pattern != "" {
			return truncateRunes(pattern, targetRunes), truncateRunes(oneLine(body), summaryRunes), class
		}
	}
	if path := firstField(fields, "file_path", "path", "notebook_path"); path != "" {
		return shortPath(path), truncateRunes(oneLine(body), summaryRunes), class
	}
	if command := firstField(fields, "command", "cmd", "description", "prompt"); command != "" {
		return truncateRunes(oneLine(command), targetRunes), truncateRunes(command, summaryRunes), class
	}
	return "", truncateRunes(oneLine(body), summaryRunes), class
}

// shellCommand digs the command out of whatever shape the harness wrapped it
// in. Measured 2026-09-20 on codex-cli 0.154: its `exec` tool's input is not
// JSON at all but a JavaScript snippet,
// `const r = await tools.exec_command({"cmd":"…","workdir":"…"});`, so a
// reader that only knew the `{"command":[...]}` shape would record every
// Codex shell call with an empty target.
func shellCommand(kind harness.Kind, body string, fields map[string]string) string {
	if command := firstField(fields, "command", "cmd"); command != "" {
		return command
	}
	if list := decodeCommandList(body); list != "" {
		return list
	}
	if command := codexExecCommand(body); command != "" {
		return command
	}
	return oneLine(body)
}

// toolBody is a tool input's text. Claude hands its tools a JSON object, so
// the body is that object; Codex hands `exec` a JavaScript program, which the
// rollout stores as a JSON *string*, so it has to be unquoted before anything
// can be read out of it. A reader that skipped this step records every Codex
// shell call with a target of `"const r = await tools.exec_command({\"cmd\"…`,
// which is the escaping, not the command.
func toolBody(inputJSON string) string {
	trimmed := strings.TrimSpace(inputJSON)
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if json.Unmarshal([]byte(trimmed), &text) == nil {
			return text
		}
	}
	return inputJSON
}

// patchTarget names the file a Codex patch touches, when the body is one.
func patchTarget(body string) (string, bool) {
	if !strings.Contains(body, "*** Begin Patch") {
		return "", false
	}
	return shortPath(applyPatchFile(body)), true
}

// shortPath keeps the end of a path rather than the start when it will not
// fit. A crew works in an absolute worktree path whose first eighty runes are
// the temporary directory it was created in, so truncating from the front
// gives every file of every crew the same target and names none of them.
func shortPath(path string) string {
	if len([]rune(path)) <= targetRunes {
		return path
	}
	segments := strings.Split(path, string(filepath.Separator))
	for i := len(segments) - 1; i >= 0; i-- {
		tail := strings.Join(segments[i:], string(filepath.Separator))
		if len([]rune(tail)) > targetRunes-2 {
			tail = strings.Join(segments[i+1:], string(filepath.Separator))
			if tail == "" {
				break
			}
			return "…/" + truncateRunes(tail, targetRunes-2)
		}
	}
	return truncateRunes(path, targetRunes)
}

// codexExecCommand pulls the `cmd` out of a `tools.exec_command({...})` call
// in the JavaScript body Codex hands its `exec` tool.
func codexExecCommand(input string) string {
	i := strings.Index(input, "exec_command(")
	if i < 0 {
		return ""
	}
	rest := input[i+len("exec_command("):]
	brace := strings.IndexByte(rest, '{')
	if brace < 0 {
		return ""
	}
	dec := json.NewDecoder(strings.NewReader(rest[brace:]))
	var args map[string]any
	if err := dec.Decode(&args); err != nil {
		return ""
	}
	if cmd, ok := args["cmd"].(string); ok {
		return cmd
	}
	if cmd, ok := args["command"].(string); ok {
		return cmd
	}
	return ""
}

// decodeCommandList renders the `{"command":["bash","-lc","…"]}` shape as the
// line a reader would have typed.
func decodeCommandList(raw string) string {
	var probe struct {
		Command []string `json:"command"`
	}
	if json.Unmarshal([]byte(raw), &probe) != nil || len(probe.Command) == 0 {
		return ""
	}
	if len(probe.Command) >= 3 && (probe.Command[0] == "bash" || probe.Command[0] == "sh") {
		return probe.Command[len(probe.Command)-1]
	}
	return strings.Join(probe.Command, " ")
}

// applyPatchFile names the file a patch touches, from its first
// `*** Update File:` / `*** Add File:` header.
//
// The header is not searched line by line, because in a Codex `exec` body the
// patch is a JavaScript string literal and its newlines are the two
// characters `\` and `n`, not line breaks (measured 2026-09-20, codex-cli
// 0.154). A line-oriented reader finds no header at all there and every Codex
// edit lands in the timeline as an unnamed "patch".
func applyPatchFile(patch string) string {
	for _, prefix := range []string{"*** Update File:", "*** Add File:", "*** Delete File:", "*** Move to:"} {
		at := strings.Index(patch, prefix)
		if at < 0 {
			continue
		}
		rest := patch[at+len(prefix):]
		end := len(rest)
		for _, terminator := range []string{"\n", `\n`, `"`} {
			if i := strings.Index(rest, terminator); i >= 0 && i < end {
				end = i
			}
		}
		if name := strings.TrimSpace(rest[:end]); name != "" {
			return name
		}
	}
	return "patch"
}

// decodeFields flattens a tool input's top-level string fields. A non-object
// input (Codex hands `exec` a JavaScript string) yields nothing, and the
// caller falls through to the shapes that are not JSON.
func decodeFields(raw string) map[string]string {
	out := map[string]string{}
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &values) != nil {
		return out
	}
	for key, value := range values {
		var s string
		if json.Unmarshal(value, &s) == nil {
			out[key] = s
		}
	}
	return out
}

func firstField(fields map[string]string, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(fields[key]); v != "" {
			return v
		}
	}
	return ""
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)), " ")
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ParserFor is the transcript parser of one harness. It is exported so a test
// can re-parse the very file the ingest read and check that nothing was lost
// between the parser and the database.
func ParserFor(kind harness.Kind) (harness.TranscriptParser, error) { return transcriptParser(kind) }
