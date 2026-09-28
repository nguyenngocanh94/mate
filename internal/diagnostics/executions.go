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

func normalizeTarget(path, worktree string) string {
	if worktree == "" || !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(worktree, path)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return path
}

func isWrapper(tool string) bool {
	t := strings.ToLower(tool)
	return t == "exec" || t == "functions.exec" || t == "wait" || t == "functions.wait" || t == "parallel" || t == "multi_tool_use.parallel"
}

func classifyCall(executions []Execution, worktree string) (string, string, string) {
	kinds, targets := map[string]bool{}, map[string]bool{}
	for _, e := range executions {
		if e.IsWrapper {
			commands := literalCommands(e.Command)
			if len(commands) > 1 {
				for _, command := range commands {
					inner := e
					inner.Command, inner.Target, inner.Tool, inner.IsWrapper = command, "", "exec_command", false
					kind, target := classify(inner, worktree)
					kinds[kind] = true
					if target != "" {
						targets[target] = true
					}
				}
				continue
			}
		}
		kind, target := classify(e, worktree)
		kinds[kind] = true
		if target != "" {
			targets[target] = true
		}
	}
	if len(kinds) == 0 {
		return "unknown", "", "Activity not recorded"
	}
	if len(kinds) > 1 {
		return "mixed", "", "Mixed activity"
	}
	kind := sortedKeys(kinds)[0]
	target := ""
	if len(targets) == 1 {
		target = sortedKeys(targets)[0]
	} else if len(targets) > 1 {
		target = fmt.Sprintf("%d targets", len(targets))
	}
	label := map[string]string{"instructions": "Load instructions", "read": "Read / search", "edit": "Edit files", "test": "Build / test", "debug": "Debug", "browser": "Browser", "git": "Git / handback", "communication": "Communicate", "poll": "Check process", "unknown": "Activity not classified", "wrapper": "Tool wrapper"}[kind]
	if target != "" {
		label += " · " + short(target, 100)
	}
	return kind, target, label
}

var readCommand = regexp.MustCompile(`(?i)(^|[;\s"'])(cat|sed|head|tail|rg|grep|find|ls|read_file)(\s|$)`)

func classify(e Execution, worktree string) (string, string) {
	t := strings.ToLower(e.Tool)
	command := displayCommand(e.Command)
	c := strings.ToLower(command)
	target := normalizeTarget(e.Target, worktree)
	if target == "" || e.IsWrapper {
		target = command
	}
	if worktree != "" {
		target = strings.ReplaceAll(target, worktree+"/", "")
	}
	if e.Poll || strings.Contains(t, "write_stdin") || t == "wait" || t == "functions.wait" {
		if e.ProcessID != "" {
			return "poll", "process " + e.ProcessID
		}
		return "poll", "unlinked process"
	}
	if strings.Contains(c, "tools.write_stdin") {
		return "poll", "unlinked process"
	}
	if e.IsWrapper && (strings.Contains(command, "tools.") || strings.HasPrefix(strings.TrimSpace(command), "{")) {
		return "wrapper", "command details unavailable"
	}
	if strings.Contains(t, "browser") || strings.Contains(t, "playwright") || strings.Contains(t, "screenshot") {
		return "browser", target
	}
	if strings.Contains(t, "edit") || strings.Contains(t, "write") || strings.Contains(t, "patch") || strings.Contains(c, "apply_patch") {
		return "edit", target
	}
	// Test/build commands often pipe their logs through head or sed. The
	// log filter does not turn the whole execution into a file read.
	if buildCommand.MatchString(c) {
		return "test", target
	}
	if strings.Contains(t, "read") || strings.Contains(t, "grep") || strings.Contains(t, "glob") || readCommand.MatchString(c) {
		if strings.Contains(c+" "+strings.ToLower(target), "agents.md") || strings.Contains(c+" "+strings.ToLower(target), "skill.md") || strings.Contains(c+" "+strings.ToLower(target), "claude.md") {
			return "instructions", target
		}
		return "read", target
	}
	if strings.Contains(c, "git ") {
		return "git", target
	}
	if strings.Contains(t, "send_message") || strings.Contains(c, "mate send ") || strings.Contains(c, "mate handback") {
		return "communication", target
	}
	if strings.Contains(c, "lldb") || strings.Contains(c, "gdb ") {
		return "debug", target
	}
	return "unknown", target
}

var buildCommand = regexp.MustCompile(`(?i)(^|[;\s"'])(go\s+(test|build)|pytest|xcodebuild|npm\s+(run\s+)?(test|build)|cargo\s+(test|build)|make\s+check|gradle)(\s|$)`)
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
			if command, err := strconv.Unquote(m[1]); err == nil {
				commands = append(commands, command)
			}
		} else {
			commands = append(commands, strings.TrimSuffix(strings.TrimPrefix(m[1], "'"), "'"))
		}
	}
	return commands
}

func executionLabel(e Execution) string {
	command := executionCommand(e)
	if e.IsWrapper && (strings.Contains(command, "tools.") || strings.HasPrefix(strings.TrimSpace(command), "{")) {
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
	command := e.Command
	if p.opts.Worktree != "" {
		command = strings.ReplaceAll(command, p.opts.Worktree+"/", "")
	}
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
