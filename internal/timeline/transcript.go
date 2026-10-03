package timeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// ingestTranscripts reads every agent's transcript into turns, tool calls,
// usage samples and the events that go with them.
//
// The harness's TranscriptSource reads the file - whole, or by appending
// what it grew by, which is the harness's call - and every pass records the
// whole batch it returns. Every fact carries a natural key, so nothing is
// recorded twice. `cursor` still records how far the parser trusted the
// file, which is what makes a withheld message group visible rather than
// silent.
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
	archives, err := p.ing.ws.MateSessionArchives(p.project)
	if err != nil {
		return err
	}
	current := p.mate.Meta
	for _, meta := range archives {
		if meta[MetaSessionID] == current[MetaSessionID] && meta["finalized"] != "true" {
			continue
		}
		p.mate.Meta = meta
		loc, reason := p.locateMate()
		p.mate.Meta = current
		loc.Finalized = meta["finalized"] == "true" && loc.Path == meta[MetaTranscript] && loc.Source == LocatorMeta
		if reason != "" {
			p.unresolved(p.mate.ActorID, string(loc.Kind), reason)
			continue
		}
		if err := p.ingestOneTranscript(ctx, loc); err != nil {
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
	src, reason := p.ing.transcriptSource(loc.Kind)
	if reason != "" {
		p.unresolved(loc.ActorID, string(loc.Kind), reason)
		return nil
	}
	tb, err := p.ing.cachedTranscript(src, loc)
	if err != nil {
		if os.IsNotExist(err) {
			p.unresolved(loc.ActorID, string(loc.Kind), unresolvedNoFile)
			return nil
		}
		return err
	}

	sessionID := loc.SessionID
	if sessionID == "" {
		sessionID = loc.Path
	}
	rowID := sessionRowID(loc.ActorID, sessionID)
	p.b.session(pendingSession{
		ID: rowID, ActorID: loc.ActorID, HarnessSession: loc.SessionID, TranscriptPath: loc.Path,
	})
	unchanged, err := p.transcriptPreviouslyRecorded(ctx, loc, tb)
	if err != nil {
		return err
	}
	since, err := p.legacyTranscriptOffset(ctx, loc)
	if err != nil {
		return err
	}
	p.b.cursor(loc.Path, tb.ConsumedBytes)
	if err := p.ingestTelemetry(ctx, src, loc, rowID, tb); err != nil {
		return err
	}
	if unchanged {
		results := resultsByCall(tb)
		for _, call := range tb.ToolCalls {
			p.rememberToolCommand(loc, call, results)
		}
		return nil
	}
	mark := transcriptFactMark{len(p.b.turns), len(p.b.actions), len(p.b.events), len(p.b.usage)}

	p.usageTurns(loc, rowID, tb)
	p.compactions(loc, rowID, tb)
	p.filterTranscriptFacts(loc, rowID, tb, since, mark)
	return nil
}

// usageTurns records one turn per priced model call the harness normalised:
// the turn row, the usage sample in the shape the harness wrote it, the two
// turn events, and an action for every tool call the call issued. One path
// serves every harness, because the harness has already made the numbers
// mean the same thing (harness.UsageTurn).
func (p *pass) usageTurns(loc Located, sessionRow string, tb harness.TranscriptBatch) {
	results := resultsByCall(tb)
	for ordinal, t := range tb.UsageTurns {
		turnID := turnRowID(sessionRow, t.SourceRef)
		p.b.turn(pendingTurn{
			ID: turnID, ActorID: loc.ActorID, SessionID: sessionRow, Ordinal: ordinal,
			StartedAt: t.StartedAt, EndedAt: t.EndedAt,
			Outcome: t.Outcome, Model: t.Model, HarnessTurnRef: t.HarnessTurnRef,
			Input: t.Usage.Input, CacheRead: t.Usage.CacheRead, CacheWrite: t.Usage.CacheWrite,
			Output: t.Usage.Output, Thinking: t.Usage.Reasoning,
			ContextAfter: t.ContextTokens, ToolCount: len(t.Calls),
			RefPath: loc.Path, RefOffset: t.Offset,
		})
		p.b.usageSample(pendingUsage{
			ID: usageRowID(sessionRow, t.Offset), SessionID: sessionRow, At: t.Sample.At,
			Cumulative: t.Sample.Cumulative,
			Input:      t.Sample.Usage.Input, CacheRead: t.Sample.Usage.CacheRead, CacheWrite: t.Sample.Usage.CacheWrite,
			Output: t.Sample.Usage.Output, Thinking: t.Sample.Usage.Reasoning,
			RefPath: loc.Path, RefOffset: t.Offset,
		})
		p.turnEvents(loc, turnID, t.StartedAt, t.EndedAt, map[string]any{
			"model":                t.Model,
			"harness_turn":         t.HarnessTurnRef,
			"outcome":              t.Outcome,
			"tool_count":           len(t.Calls),
			"input_tokens":         t.Usage.Input,
			"cache_read_tokens":    t.Usage.CacheRead,
			"cache_write_tokens":   t.Usage.CacheWrite,
			"output_tokens":        t.Usage.Output,
			"thinking_tokens":      t.Usage.Reasoning,
			"context_tokens_after": t.ContextTokens,
		}, t.Offset)
		for _, call := range t.Calls {
			p.toolAction(loc, sessionRow, turnID, call, results)
		}
	}
	// Tool calls no turn has priced yet are real work. They get an action
	// with no turn rather than being dropped: the live test asks that every
	// tool call in the transcript have one.
	for _, call := range tb.UnpricedCalls {
		p.toolAction(loc, sessionRow, "", call, results)
	}
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
	p.rememberToolCommand(loc, call, results)
}

func (p *pass) rememberToolCommand(loc Located, call harness.TranscriptToolCall, results map[string]harness.TranscriptToolResult) {
	_, summary, class := toolTarget(loc.Kind, call)

	// A crew's status lines have no timestamp of their own; the shell command
	// that wrote one does. Every shell command a crew ran is remembered here
	// so ingestStatus can date the line it echoed.
	if class == harness.CommandShell && summary != "" {
		p.statusClock[loc.ActorID] = append(p.statusClock[loc.ActorID],
			datedCommand{command: summary, at: call.StartedAt, path: loc.Path, offset: call.Offset})
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
// This exists because a crew's branch is short-lived. `mate merge` deletes
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
// that explains a context size falling instead of rising. The harness names
// the marker that showed it (harness.TranscriptCompaction).
func (p *pass) compactions(loc Located, sessionRow string, tb harness.TranscriptBatch) {
	for _, c := range tb.Compactions {
		p.b.event(pendingEvent{
			Dedup:   dedup(KindContextCompac, sessionRow, fmt.Sprint(c.Offset)),
			Project: p.project, At: c.OccurredAt, ActorID: loc.ActorID, Kind: KindContextCompac,
			TaskActor: p.taskActor(loc.ActorID),
			Payload:   map[string]any{"trigger": c.Trigger, "harness": string(loc.Kind)},
			RefPath:   loc.Path, RefOffset: c.Offset,
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
	// Claude's Skill tool is given the name of the skill it loads. The name
	// is what the call was about; the summary keeps the whole input.
	if skill := firstField(fields, "skill"); skill != "" {
		return truncateRunes(oneLine(skill), targetRunes), truncateRunes(oneLine(body), summaryRunes), class
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
