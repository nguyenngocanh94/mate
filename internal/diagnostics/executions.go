package diagnostics

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

func (p *projection) makeExecutions() {
	byID := map[string]Execution{}
	for _, a := range p.in.Actions {
		if a.Tool == "thinking" {
			continue
		}
		status := "unknown"
		if a.OK != nil {
			if *a.OK {
				status = "completed"
			} else {
				status = "failed"
			}
		}
		if a.EndedAt == "" {
			status = "running"
		}
		byID[a.ID] = Execution{ID: a.ID, SessionID: a.SessionID, CallID: a.CallID, PromptID: p.callPrompt[a.CallID],
			Tool: a.Tool, Target: normalizeTarget(a.Target, p.opts.Worktree), Command: a.Summary, StartedAt: a.StartedAt, EndedAt: a.EndedAt,
			DurationMs: a.DurationMs, Status: status, IsWrapper: isWrapper(a.Tool), SourceRef: a.Ref, MeasurementKind: "ledger"}
	}
	// Observations are ordered by source position so a late result completes
	// its existing execution. Equal commands never establish identity.
	facts := append([]telemetry.Fact(nil), p.in.Facts...)
	sort.SliceStable(facts, func(i, j int) bool {
		if facts[i].SessionID != facts[j].SessionID {
			return facts[i].SessionID < facts[j].SessionID
		}
		return facts[i].SourceOffset < facts[j].SourceOffset
	})
	for _, f := range facts {
		if f.Kind != "execution" && f.Kind != "tool_call" && f.Kind != "tool_result" {
			continue
		}
		id := key(f.SessionID, "execution#"+f.ExecutionID)
		if f.ExecutionID == "" {
			id = key(f.SessionID, "execution#"+f.ID)
		}
		wrapper := f.Kind != "execution"
		if wrapper {
			ref := f.WrapperRef
			if ref == "" {
				ref = f.SourceRef
			}
			id = f.SessionID + "#tool#" + ref
		}
		e := byID[id]
		e.ID, e.SessionID = id, f.SessionID
		if f.Tool != "" {
			e.IsWrapper = wrapper && isWrapper(f.Tool)
		}
		if f.Kind == "execution" {
			e.IsWrapper = false
		}
		if f.HarnessTurnRef != "" {
			e.PromptID = promptKey(f.SessionID, f.HarnessTurnRef)
		}
		if f.ResponseID != "" {
			e.CallID = p.responseCalls[key(f.SessionID, f.ResponseID)]
		}
		if f.WrapperRef != "" {
			e.WrapperID = f.SessionID + "#tool#" + f.WrapperRef
			if e.CallID == "" {
				e.CallID = p.actionCalls[e.WrapperID]
			}
		}
		if e.CallID == "" {
			e.CallID = p.actionCalls[id]
		}
		if e.CallID != "" {
			e.PromptID = p.callPrompt[e.CallID]
		}
		if f.Tool != "" {
			e.Tool = f.Tool
		}
		if f.Command != "" {
			e.Command = f.Command
		}
		if f.CWD != "" {
			e.Cwd = normalizeTarget(f.CWD, p.opts.Worktree)
		}
		if len(f.Targets) > 0 {
			parts := []string{}
			for _, t := range f.Targets {
				s := normalizeTarget(t.Path, p.opts.Worktree)
				if t.Range != "" {
					s += " " + t.Range
				}
				if t.Query != "" {
					s += " query=" + t.Query
				}
				parts = append(parts, strings.TrimSpace(s))
			}
			e.Target = strings.Join(parts, ", ")
		}
		if f.ProcessID != "" {
			e.ProcessID = f.ProcessID
		}
		e.Poll = e.Poll || f.Poll
		if f.StartedAt != nil {
			e.StartedAt = stamp(*f.StartedAt)
		} else if f.Kind == "tool_call" {
			e.StartedAt = stamp(f.OccurredAt)
		}
		if f.CompletedAt != nil {
			e.EndedAt = stamp(*f.CompletedAt)
		} else if f.Kind == "tool_result" {
			e.EndedAt = stamp(f.OccurredAt)
		}
		if f.DurationMS != nil {
			e.DurationMs = f.DurationMS
		}
		if f.ExitCode != nil {
			e.ExitCode = f.ExitCode
		}
		if f.Status != "" {
			e.Status = f.Status
		}
		if e.ExitCode != nil && *e.ExitCode != 0 {
			e.Status = "failed"
		}
		if e.Status == "" {
			e.Status = "unknown"
		}
		if f.Output != nil {
			n := f.Output.Bytes
			e.OutputBytes = &n
			e.OutputHash, e.NewOutputBytes, e.Truncated = f.Output.SHA256, f.Output.NewBytes, f.Output.Truncated
			e.OutputExcerpt = f.Output.Preview
		}
		if e.Status == "failed" {
			e.ErrorSignature = errorSignature(e)
		}
		e.SourceRef = Ref{f.SourcePath, f.SourceOffset}
		e.MeasurementKind = f.MeasurementKind
		if e.MeasurementKind == "" {
			e.MeasurementKind = "normalized"
		}
		if e.DurationMs == nil {
			e.DurationMs = duration(e.StartedAt, e.EndedAt)
		}
		byID[id] = e
	}
	for _, e := range byID {
		if e.Poll && e.ProcessID == "" {
			p.missing["A process poll could not be linked to its originating job."] = true
		}
		if e.CallID == "" && !e.IsWrapper {
			p.missing["Some native executions have no confirmed model-call parent; their token attribution is unknown."] = true
		}
		p.out.Executions = append(p.out.Executions, e)
	}
	sort.Slice(p.out.Executions, func(i, j int) bool {
		a, b := p.out.Executions[i], p.out.Executions[j]
		if at, bt := parse(a.StartedAt), parse(b.StartedAt); !at.Equal(bt) {
			return at.Before(bt)
		}
		return a.ID < b.ID
	})
}

// normalizeTarget shows a path inside the worktree relative to it. An
// absolute worktree must be a prefix of the path. The task record may hold
// the worktree relative to the workspace instead; an absolute path is then
// inside it when it passes through that directory, taken at its last
// occurrence so a nested copy of the name cannot hide the real one.
func normalizeTarget(path, worktree string) string {
	if worktree == "" || !filepath.IsAbs(path) {
		return path
	}
	if filepath.IsAbs(worktree) {
		rel, err := filepath.Rel(worktree, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
		return path
	}
	dir := "/" + strings.Trim(filepath.ToSlash(filepath.Clean(worktree)), "/")
	if strings.HasSuffix(path, dir) {
		return "."
	}
	if i := strings.LastIndex(path, dir+"/"); i >= 0 {
		return path[i+len(dir)+1:]
	}
	return path
}

// relativeToWorktree shortens every absolute path under the worktree inside
// free text such as a shell command, the way normalizeTarget does for one
// path. A path is a whole token that starts the text or follows whitespace,
// a quote, "=", ":" or "("; a longer path that merely contains the
// worktree's name is left alone, so operationKey never conflates unrelated
// commands.
func relativeToWorktree(text, worktree string) string {
	if worktree == "" || text == "" {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '/' || (i > 0 && !tokenBoundary(text[i-1])) {
			out.WriteByte(text[i])
			i++
			continue
		}
		end := i + 1
		for end < len(text) && isPathByte(text[end]) {
			end++
		}
		out.WriteString(normalizeTarget(text[i:end], worktree))
		i = end
	}
	return out.String()
}

func tokenBoundary(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\'' || c == '"' || c == '`' || c == '=' || c == ':' || c == '('
}

func isPathByte(c byte) bool {
	return c == '/' || c == '.' || c == '_' || c == '-' || c == '~' || c == '@' || c == '+' || c == '%' ||
		c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func isWrapper(tool string) bool {
	t := strings.ToLower(tool)
	return t == "exec" || t == "functions.exec" || t == "wait" || t == "functions.wait" || t == "parallel" || t == "multi_tool_use.parallel"
}

// classifyCall names a ledger call's segment from the operations it owns.
// Kinds come from executionWork, so segments and the work overview share one
// vocabulary; targets are the concrete things operated on. Several kinds
// make the call mixed and several targets are counted, never guessed.
func classifyCall(executions []Execution, worktree string) (string, string, string) {
	kinds, targets := map[string]bool{}, map[string]bool{}
	for _, e := range executions {
		for _, kind := range executionWork(e) {
			kinds[kind] = true
		}
		for _, target := range classifyTargets(e, worktree) {
			targets[target] = true
		}
	}
	if len(kinds) == 0 {
		return "unknown", "", workLabels["unknown"] + " · no operations recorded"
	}
	kind := "mixed"
	if len(kinds) == 1 {
		kind = sortedKeys(kinds)[0]
	}
	target := ""
	if len(targets) == 1 {
		target = sortedKeys(targets)[0]
	} else if len(targets) > 1 {
		target = fmt.Sprintf("%d targets", len(targets))
	}
	label := workLabels[kind]
	if target != "" {
		label += " · " + short(target, 100)
	}
	return kind, target, label
}

// executionKind is the one work kind of an execution, or mixed when it
// recorded several. Findings that need a pure read use it.
func executionKind(e Execution) string {
	kinds := uniqueKinds(executionWork(e))
	if len(kinds) == 1 {
		return sortedKeys(kinds)[0]
	}
	return "mixed"
}

// classifyTargets names what one execution operated on: the process it
// polled, the files its patch touched, the literal commands it ran, or the
// path a tool was given. Wrapper JavaScript is never a target; an opaque
// wrapper is named by the tools it invokes.
func classifyTargets(e Execution, worktree string) []string {
	tool := strings.ToLower(e.Tool)
	if e.Poll || waitTool(tool) || (e.IsWrapper && wrapperInvokes(e.Command, "write_stdin")) {
		if e.ProcessID != "" {
			return []string{"process " + e.ProcessID}
		}
		return []string{"unlinked process"}
	}
	if !e.IsWrapper {
		if strings.Contains(tool, "apply_patch") || strings.HasPrefix(strings.TrimSpace(e.Command), "apply_patch") {
			if files := patchTargets(e.Command, worktree); len(files) > 0 {
				return []string{strings.Join(files, ", ")}
			}
		}
		target := normalizeTarget(e.Target, worktree)
		if target == "" {
			target = executionCommand(e)
		}
		if target = relativeToWorktree(target, worktree); target == "" {
			return nil
		}
		return []string{target}
	}
	if wrapperInvokes(e.Command, "apply_patch") {
		if files := patchTargets(e.Command, worktree); len(files) > 0 {
			// The patch body is not scanned for commands: a line shaped like
			// command: "…" inside the patched file is not a second target.
			return []string{strings.Join(files, ", ")}
		}
	}
	targets := []string{}
	for _, command := range literalCommands(e.Command) {
		targets = append(targets, relativeToWorktree(command, worktree))
	}
	if len(targets) > 0 {
		return targets
	}
	if tools := invokedTools(e.Command); len(tools) > 0 {
		return []string{strings.Join(tools, ", ")}
	}
	return []string{"command details unavailable"}
}

var commandLiteral = regexp.MustCompile(`(?:\bcmd|\bcommand)["']?\s*:\s*("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')`)

// Extract literal command values for display only. This is not a JavaScript
// interpreter and never guesses the value of variables or executes code.
func displayCommand(raw string) string {
	commands := literalCommands(raw)
	if len(commands) > 0 {
		return strings.Join(commands, " ; ")
	}
	return raw
}

func literalCommands(raw string) []string {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &object) == nil {
		for _, name := range []string{"cmd", "command"} {
			var command string
			if json.Unmarshal(object[name], &command) == nil && command != "" {
				if name == "command" && strings.Contains(command, "tools.") {
					return literalCommands(command)
				}
				return []string{command}
			}
		}
	}
	matches := commandLiteral.FindAllStringSubmatch(raw, -1)
	commands := []string{}
	for _, m := range matches {
		if strings.HasPrefix(m[1], `"`) {
			// JavaScript allows escapes Go rejects, such as \' in a
			// double-quoted literal; those are decoded by hand, never dropped.
			if command, err := strconv.Unquote(m[1]); err == nil {
				commands = append(commands, command)
			} else {
				commands = append(commands, unescapeJS(strings.TrimSuffix(strings.TrimPrefix(m[1], `"`), `"`)))
			}
		} else {
			// A single-quoted literal escapes its own quotes (\'); left in
			// place they would split a quoted jq or rg program into commands.
			commands = append(commands, unescapeJS(strings.TrimSuffix(strings.TrimPrefix(m[1], "'"), "'")))
		}
	}
	return commands
}

func executionLabel(e Execution) string {
	command := executionCommand(e)
	if e.IsWrapper && (strings.Contains(command, "tools.") || strings.HasPrefix(strings.TrimSpace(command), "{")) {
		// A patch wrapper is named by the files it touches. The label has no
		// worktree to shorten against, so file names stand in for the paths
		// the evidence command keeps in full.
		if files := patchTargets(e.Command, ""); wrapperInvokes(e.Command, "apply_patch") && len(files) > 0 {
			names := []string{}
			for _, file := range files {
				names = appendUnique(names, filepath.Base(file))
			}
			return short("apply_patch · "+strings.Join(names, ", "), 88)
		}
		return "Tool wrapper " + e.Tool
	}
	if command == "" {
		command = e.Target
	}
	if command == "" {
		command = e.Tool
	}
	return short(command, 88)
}

func executionCommand(e Execution) string {
	if e.IsWrapper {
		return displayCommand(e.Command)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(e.Command), &object) == nil {
		return displayCommand(e.Command)
	}
	return e.Command
}

func short(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

// Keep commands exact (including flags, test targets, ranges and queries).
// Only the known worktree root is normalized for a comparable repo path.
func (p *projection) operationKey(e Execution) string {
	command := relativeToWorktree(e.Command, p.opts.Worktree)
	return key(e.SessionID, e.Tool+"\x00"+e.Cwd+"\x00"+command+"\x00"+e.Target)
}

func errorSignature(e Execution) string {
	if e.OutputHash != "" && e.OutputBytes != nil && *e.OutputBytes > 0 {
		if e.ExitCode != nil {
			return fmt.Sprintf("%d:%s", *e.ExitCode, e.OutputHash)
		}
		return e.OutputHash
	}
	// Exit status alone does not demonstrate the same error.
	return ""
}
