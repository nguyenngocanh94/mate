package diagnostics

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

// wrapperFacts is one Codex tool call as its telemetry records it: the call
// when the response names it and the result when it returns. It belongs to
// model call r<call>, and is open from start to end.
func wrapperFacts(ref string, call, start, end int, script string) []telemetry.Fact {
	base := telemetry.Fact{ID: ref, SessionID: "s", HarnessTurnRef: "p", ResponseID: fmt.Sprintf("r%d", call), SourceRef: ref, WrapperRef: ref,
		Tool: "exec", Command: script, StartedAt: atp(start), MeasurementKind: "native"}
	opened, closed := base, base
	opened.Kind, opened.OccurredAt, opened.SourceOffset = "tool_call", at(start), int64(start*100+1)
	closed.Kind, closed.OccurredAt, closed.SourceOffset, closed.CompletedAt = "tool_result", at(end), int64(end*100+2), atp(end)
	return []telemetry.Fact{opened, closed}
}

func executionByID(t *testing.T, p Performance, id string) Execution {
	t.Helper()
	for _, e := range p.Executions {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no execution %s in %+v", id, p.Executions)
	return Execution{}
}

func stepKinds(o Overview) []string {
	out := []string{}
	for _, s := range o.Steps {
		out = append(out, s.Kind)
	}
	return out
}

// Codex records a command without saying which tool call ran it. The command
// started while exactly one call was open, so that call is charged with it,
// the link says it was inferred, and the wrapper is not a second operation.
func TestNativeCommandIsChargedToTheOneToolCallOpenWhenItStarted(t *testing.T) {
	call := fixtureCall(0, "p")
	facts := []telemetry.Fact{promptFact("s", "p", 0), execution("n", 2, 3)}
	facts = append(facts, wrapperFacts("w0", 0, 1, 4, `const outputs = await Promise.all(jobs.map(([name, cmd]) => tools.exec_command({cmd})))`)...)
	p := Project(Input{Calls: []Call{call}, Facts: facts}, Options{Closed: true})

	native := executionByID(t, p, "s#execution#n")
	if native.CallID != call.ID || native.WrapperID != "s#tool#w0" || native.ParentLink != ParentLinkStart {
		t.Fatalf("native command not linked to the open call: %+v", native)
	}
	o := p.PromptTurns[0].Overview
	tests := categoryByKind(t, o, "test")
	if tests.ModelCalls != 1 || tests.ExecutionCount != 1 || tests.Tokens == nil || *tests.Tokens != p.Tokens {
		t.Fatalf("the call is not charged to the work its command did: %+v", tests)
	}
	if len(o.Categories) != 1 {
		t.Fatalf("the wrapper was counted as work of its own: %+v", o.Categories)
	}
	if !strings.Contains(o.Coverage, "1 command(s) in this recording are attributed to the one tool call that was open when they started") {
		t.Fatalf("the inference is not disclosed: %q", o.Coverage)
	}
	if !strings.Contains(p.Overview.Coverage, "the harness does not name their parent.") || !strings.HasSuffix(p.Overview.Coverage, ".") {
		t.Fatalf("the Crew coverage does not disclose it as a sentence: %q", p.Overview.Coverage)
	}
	for _, missing := range p.Freshness.Missing {
		if strings.Contains(missing, "no confirmed model-call parent") {
			t.Fatalf("a linked command is still reported unattributed: %q", missing)
		}
	}
}

// A command that was sent to the background ends long after the call that
// started it returned. Only the start places it.
func TestBackgroundedCommandKeepsTheCallThatStartedIt(t *testing.T) {
	facts := []telemetry.Fact{promptFact("s", "p", 0), execution("n", 2, 40)}
	facts = append(facts, wrapperFacts("w0", 0, 1, 3, `await tools.exec_command({cmd: build})`)...)
	p := Project(Input{Calls: []Call{fixtureCall(0, "p")}, Facts: facts}, Options{Closed: true})
	if native := executionByID(t, p, "s#execution#n"); native.WrapperID != "s#tool#w0" {
		t.Fatalf("backgrounded command lost its call: %+v", native)
	}
}

func TestNativeCommandStaysUnlinkedUnlessExactlyOneCallCouldHaveStartedIt(t *testing.T) {
	poll := wrapperFacts("poll", 0, 1, 4, `await tools.write_stdin({session_id: 7})`)
	poll[0].Poll, poll[1].Poll = true, true
	otherSession := wrapperFacts("w0", 0, 1, 4, `await tools.exec_command({cmd})`)
	otherSession[0].SessionID, otherSession[1].SessionID = "other", "other"
	otherPrompt := wrapperFacts("w0", 0, 1, 4, `await tools.exec_command({cmd})`)
	otherPrompt[0].HarnessTurnRef, otherPrompt[1].HarnessTurnRef = "q", "q"
	otherPrompt[0].ResponseID, otherPrompt[1].ResponseID = "", ""
	cases := []struct {
		name     string
		wrappers []telemetry.Fact
	}{
		{"two calls open", append(wrapperFacts("w0", 0, 1, 6, `await tools.exec_command({cmd})`), wrapperFacts("w1", 0, 2, 7, `await tools.exec_command({cmd})`)...)},
		{"started after the call returned", wrapperFacts("w0", 0, 1, 2, `await tools.exec_command({cmd})`)},
		{"started before the call opened", wrapperFacts("w0", 0, 4, 6, `await tools.exec_command({cmd})`)},
		{"only a poll was open", poll},
		{"the open call is another session's", otherSession},
		{"the open call is another prompt's", otherPrompt},
	}
	for _, c := range cases {
		facts := append([]telemetry.Fact{promptFact("s", "p", 0), promptFact("s", "q", 0), promptFact("other", "p", 0), execution("n", 3, 5)}, c.wrappers...)
		p := Project(Input{Calls: []Call{fixtureCall(0, "p")}, Facts: facts}, Options{Closed: true})
		if native := executionByID(t, p, "s#execution#n"); native.CallID != "" || native.WrapperID != "" || native.ParentLink != "" {
			t.Errorf("%s: linked anyway: %+v", c.name, native)
		}
	}
}

// A parent the harness names is a fact; timing never replaces it.
func TestNamedParentIsNeverReplacedByTiming(t *testing.T) {
	native := execution("n", 6, 7)
	native.WrapperRef = "w0"
	facts := []telemetry.Fact{promptFact("s", "p", 0), native}
	facts = append(facts, wrapperFacts("w0", 0, 1, 2, `await tools.exec_command({cmd})`)...)
	facts = append(facts, wrapperFacts("w1", 1, 5, 8, `await tools.exec_command({cmd})`)...)
	p := Project(Input{Calls: []Call{fixtureCall(0, "p"), fixtureCall(1, "p")}, Facts: facts}, Options{Closed: true})
	if got := executionByID(t, p, "s#execution#n"); got.WrapperID != "s#tool#w0" || got.ParentLink != "" {
		t.Fatalf("named parent replaced: %+v", got)
	}
}

// A wrapper that patched a file and ran a command keeps the patch when the
// command is recorded by its own native execution.
func TestWrapperWithNativeChildrenKeepsWhatItDidBesidesTheirCommands(t *testing.T) {
	script := `await tools.apply_patch("*** Begin Patch\n*** Update File: src.go\n@@\n-a\n+b\n*** End Patch\n"); await tools.exec_command({cmd:"go test ./..."})`
	facts := []telemetry.Fact{promptFact("s", "p", 0), execution("n", 2, 3)}
	facts = append(facts, wrapperFacts("w0", 0, 1, 4, script)...)
	p := Project(Input{Calls: []Call{fixtureCall(0, "p")}, Facts: facts}, Options{Closed: true})
	steps := p.PromptTurns[0].Overview.Steps
	if len(steps) != 1 || steps[0].Kind != "mixed" || !reflect.DeepEqual(steps[0].Parts, []string{"edit_code", "test"}) || steps[0].Executions != 2 {
		t.Fatalf("patch or command lost: %+v", steps)
	}
	if kind := p.Segments[0].Kind; kind != "mixed" {
		t.Fatalf("segment kind %s disagrees with the step", kind)
	}
}

func TestStepsFoldConsecutiveCallsOfOneTypeAndAddUpToThePrompt(t *testing.T) {
	in := Input{Facts: []telemetry.Fact{promptFact("s", "p", 0)}}
	for i, tool := range []string{"Bash", "Bash", "Edit", "Bash", ""} {
		call := fixtureCall(i, "p")
		if tool == "" {
			call.Outcome = "end_turn"
			in.Calls = append(in.Calls, call)
			continue
		}
		in.Calls = append(in.Calls, call)
		command := "go test ./..."
		if tool == "Edit" {
			command = "src.go"
		}
		in.Actions = append(in.Actions, Action{ID: call.ID + "#action", SessionID: "s", CallID: call.ID, Tool: tool, Summary: command, StartedAt: call.StartedAt, EndedAt: call.EndedAt})
	}
	p := Project(in, Options{Now: at(100), Closed: true})
	prompt := p.PromptTurns[0]
	steps := prompt.Overview.Steps
	if got := stepKinds(prompt.Overview); !reflect.DeepEqual(got, []string{"test", "edit_code", "test", "response"}) {
		t.Fatalf("steps %v", got)
	}
	var tokens Tokens
	calls := 0
	for _, s := range steps {
		tokens.Add(s.Tokens)
		calls += s.ModelCalls
		if s.ModelCalls != len(s.CallIDs) {
			t.Fatalf("step counts %d calls and lists %v", s.ModelCalls, s.CallIDs)
		}
		// A step and the category that charges its calls are one assignment.
		charged := categoryByKind(t, prompt.Overview, s.Kind).CallIDs
		for _, id := range s.CallIDs {
			if !strings.Contains(strings.Join(charged, "\n"), id) {
				t.Fatalf("step %s holds call %s that category does not charge", s.Kind, id)
			}
		}
		if s.Open {
			t.Fatalf("a closed task has an open step: %+v", s)
		}
	}
	if tokens != prompt.Tokens || calls != prompt.ModelCalls {
		t.Fatalf("steps add up to %+v in %d calls, prompt is %+v in %d", tokens, calls, prompt.Tokens, prompt.ModelCalls)
	}
	first := steps[0]
	if first.ModelCalls != 2 || first.Executions != 2 || first.ElapsedMs == nil || *first.ElapsedMs != 15000 || first.Label != "Run tests / build" {
		t.Fatalf("first step %+v", first)
	}
	if last := steps[3]; last.Executions != 0 || !reflect.DeepEqual(last.Parts, []string{"response"}) {
		t.Fatalf("reply step %+v", last)
	}
	if len(p.Overview.Steps) != 0 || p.Overview.Steps == nil {
		t.Fatalf("the Crew overview orders unrelated prompts: %+v", p.Overview.Steps)
	}
}

func TestOnlyTheLastStepOfARunningPromptIsOpen(t *testing.T) {
	in := Input{Facts: []telemetry.Fact{promptFact("s", "p", 0)}}
	for i, command := range []string{"go test ./...", "src.go"} {
		call := fixtureCall(i, "p")
		in.Calls = append(in.Calls, call)
		tool := "Bash"
		if i == 1 {
			tool = "Edit"
		}
		in.Actions = append(in.Actions, Action{ID: call.ID + "#action", SessionID: "s", CallID: call.ID, Tool: tool, Summary: command, StartedAt: call.StartedAt, EndedAt: call.EndedAt})
	}
	steps := Project(in, Options{Now: at(30)}).PromptTurns[0].Overview.Steps
	if len(steps) != 2 || steps[0].Open || !steps[1].Open {
		t.Fatalf("open flags %+v", steps)
	}
}

// The ledger's stop reason is evidence: a call that handed the turn back
// without running anything produced the reply. One that was cut short, or
// whose stop reason was not recorded, stays unclassified.
func TestCallThatEndedItsTurnWithoutAToolIsTheReply(t *testing.T) {
	for outcome, want := range map[string]string{"end_turn": "response", "stop": "response", "length": "unknown", "aborted": "unknown", "tool_use": "unknown", "": "unknown"} {
		call := fixtureCall(0, "p")
		call.Outcome = outcome
		p := Project(Input{Calls: []Call{call}, Facts: []telemetry.Fact{promptFact("s", "p", 0)}}, Options{Closed: true})
		if got := stepKinds(p.PromptTurns[0].Overview); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("outcome %q: steps %v, want %s", outcome, got, want)
		}
	}
}

func TestStatusLineBesideWorkIsNotAWorkTypeOfItsOwn(t *testing.T) {
	cases := []struct{ command, kind string }{
		{`echo "working: polishing layout" >> "$MATE_STATUS"`, "coordination"},
		{"printf '%s\\n' 'working: running the suite' >> \"$MATE_STATUS\"\ngo test ./...", "test"},
		{"sed -n '1,80p' main.go && echo \"working: reading\" >> \"$MATE_STATUS\"", "research"},
		// The query after a git write still reports that write.
		{"git commit -m x\necho \"done: ready\" >> \"$MATE_STATUS\"\ngit status --short", "coordination"},
	}
	for _, c := range cases {
		if kind := executionKind(Execution{Tool: "exec_command", Command: c.command}); kind != c.kind {
			t.Errorf("%q: kind %s, want %s", c.command, kind, c.kind)
		}
	}
}

func TestShellControlFlowAndCommentsAreNotWork(t *testing.T) {
	cases := []struct{ command, kind string }{
		{"if [ -f brief.md ]; then sed -n '1,40p' brief.md; fi", "research"},
		{"for f in a.go b.go; do cat \"$f\"; done", "research"},
		{"# check the suite first\ngo test ./...", "test"},
		{"command -v chromium || command -v google-chrome || true", "research"},
		{"if ! go test ./...; then echo failed; fi", "test"},
		// A guard alone establishes nothing, and a program is not the builtin.
		{"test -s report.html", "unknown"},
		{"./test --all", "unknown"},
		// Finding an instruction file is not reading it.
		{"find . -maxdepth 3 -name AGENTS.md", "research"},
		{"cat AGENTS.md", "instructions"},
	}
	for _, c := range cases {
		if kind := executionKind(Execution{Tool: "exec_command", Command: c.command}); kind != c.kind {
			t.Errorf("%q: kind %s, want %s", c.command, kind, c.kind)
		}
	}
}

func TestWrapperToolsAreClassifiedByTheirName(t *testing.T) {
	cases := []struct{ script, kind string }{
		{`const r = await tools.web__run({"search_query":[{"q":"esp32 datasheet"}]}); text(r)`, "research"},
		{`await tools.spawn_agent({task: "x"})`, "coordination"},
		{`await tools.web__run({"open":[{"ref_id":"a"}]}); await tools.exec_command({cmd:"go test ./..."})`, "mixed"},
		{`const r = await tools.frobnicate({})`, "unknown"},
	}
	for _, c := range cases {
		if kind := executionKind(Execution{Tool: "exec", IsWrapper: true, Command: c.script}); kind != c.kind {
			t.Errorf("%q: kind %s, want %s", c.script, kind, c.kind)
		}
	}
}
