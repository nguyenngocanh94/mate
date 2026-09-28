package diagnostics

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

func categoryByKind(t *testing.T, overview Overview, kind string) WorkCategory {
	t.Helper()
	for _, category := range overview.Categories {
		if category.Kind == kind {
			return category
		}
	}
	t.Fatalf("missing category %s in %+v", kind, overview.Categories)
	return WorkCategory{}
}

func TestOverviewPreservesTestEditTestAndConservesEveryBucket(t *testing.T) {
	in := Input{Facts: []telemetry.Fact{promptFact("s", "p", 0)}}
	for i, tool := range []string{"Bash", "Edit", "Bash"} {
		call := fixtureCall(i, "p")
		in.Calls = append(in.Calls, call)
		command := "go test ./..."
		if tool == "Edit" {
			command = "src.go"
		}
		in.Actions = append(in.Actions, Action{ID: call.ID + "#action", SessionID: "s", CallID: call.ID, Tool: tool, Summary: command, StartedAt: call.StartedAt, EndedAt: call.EndedAt})
	}
	p := Project(in, Options{Now: at(30), Closed: true})
	o := p.PromptTurns[0].Overview
	sequence := []string{}
	for _, step := range o.Sequence {
		sequence = append(sequence, step.Kind)
	}
	if !reflect.DeepEqual(sequence, []string{"test", "edit_code", "test"}) {
		t.Fatalf("sequence %v", sequence)
	}
	seen := map[string]bool{}
	var tokens Tokens
	calls := 0
	for _, c := range o.Categories {
		if c.Tokens != nil {
			tokens.Add(*c.Tokens)
		}
		calls += c.ModelCalls
		for _, id := range c.CallIDs {
			if seen[id] {
				t.Fatalf("call charged twice %s", id)
			}
			seen[id] = true
		}
	}
	if tokens != p.Tokens || calls != 3 {
		t.Fatalf("overview totals %+v/%d, ledger %+v", tokens, calls, p.Tokens)
	}
	if categoryByKind(t, o, "test").ModelCalls != 2 || categoryByKind(t, o, "edit_code").ModelCalls != 1 {
		t.Fatal("wrong work categories")
	}
	if strings.Contains(strings.ToLower(o.Summary), "fix") {
		t.Fatal("an edit was inferred to be a bug fix")
	}
}

func TestOverviewMixedCallHasTypesButChargesTokensOnce(t *testing.T) {
	call := fixtureCall(0, "p")
	in := Input{Calls: []Call{call}, Facts: []telemetry.Fact{promptFact("s", "p", 0)}, Actions: []Action{{ID: "wrapper", CallID: call.ID, SessionID: "s", Tool: "functions.exec", StartedAt: call.StartedAt,
		Summary: `await Promise.all([tools.exec_command({cmd:"cat src.go"}), tools.exec_command({cmd:"go test ./..."})])`}}}
	p := Project(in, Options{Closed: true})
	o := p.PromptTurns[0].Overview
	if len(o.Sequence) != 1 || o.Sequence[0].Kind != "mixed" {
		t.Fatalf("parallel wrapper invented serial steps: %+v", o.Sequence)
	}
	if categoryByKind(t, o, "research").Tokens != nil || categoryByKind(t, o, "test").Tokens != nil {
		t.Fatal("mixed call tokens split across tools")
	}
	mixed := categoryByKind(t, o, "mixed")
	if mixed.Tokens == nil || *mixed.Tokens != p.Tokens || mixed.ModelCalls != 1 {
		t.Fatalf("mixed accounting %+v", mixed)
	}
}

func TestOverviewNativeUnlinkedReviewAndFileChangesCarryEvidenceWithoutCost(t *testing.T) {
	exec := execution("review", 1, 4)
	exec.Command = "git diff -- src.go"
	exec.ExitCode = intp(1)
	exec.Status = "failed"
	in := Input{Facts: []telemetry.Fact{promptFact("s", "p", 0), exec,
		{ID: "patch", Kind: "progress", SessionID: "s", HarnessTurnRef: "p", OccurredAt: at(5), SourceOffset: 20, Changes: []telemetry.Change{{Path: "new.go", Kind: "add"}, {Path: "src.go", Kind: "update"}}},
		{ID: "patch", Kind: "progress", SessionID: "s", HarnessTurnRef: "p", OccurredAt: at(6), SourceOffset: 30, Changes: []telemetry.Change{{Path: "new.go", Kind: "add"}, {Path: "src.go", Kind: "update"}}}}}
	p := Project(in, Options{Closed: true})
	o := p.PromptTurns[0].Overview
	review := categoryByKind(t, o, "review")
	if review.Tokens != nil || review.ModelCalls != 0 || review.ExecutionCount != 1 || review.ElapsedMs == nil || *review.ElapsedMs != 3000 {
		t.Fatalf("unlinked review %+v", review)
	}
	evidence := review.Evidence[0]
	if evidence.Command != exec.Command || evidence.ExitCode == nil || *evidence.ExitCode != 1 || evidence.Status != "failed" {
		t.Fatalf("Mate evidence not self-contained %+v", evidence)
	}
	for _, kind := range []string{"write_code", "edit_code"} {
		category := categoryByKind(t, o, kind)
		if category.Tokens != nil || category.ExecutionCount != 0 || len(category.Evidence) != 1 {
			t.Fatalf("FileChange duplicated or billed: %+v", category)
		}
	}
	if len(o.Sequence) != 2 || o.Sequence[1].Kind != "mixed" {
		t.Fatalf("file paths fabricated edit order: %+v", o.Sequence)
	}
}

func TestOverviewPromptRequestsDoNotCreateActivityAndSessionsStayIsolated(t *testing.T) {
	prompt := promptFact("s", "p", 0)
	prompt.Text = "Research the bug, write code, review it and run all tests"
	other := promptFact("other", "p", 0)
	exec := execution("test", 1, 4)
	exec.SessionID = "other"
	p := Project(Input{Facts: []telemetry.Fact{prompt, other, exec}}, Options{Closed: true})
	for _, g := range p.PromptTurns {
		if g.SessionID == "s" && len(g.Overview.Categories) != 0 {
			t.Fatalf("request became measured activity %+v", g.Overview)
		}
		if g.SessionID == "other" {
			categoryByKind(t, g.Overview, "test")
		}
	}
	call := fixtureCall(0, "p")
	p = Project(Input{Calls: []Call{call}, Facts: []telemetry.Fact{prompt}}, Options{Closed: true})
	if len(p.PromptTurns[0].Overview.Categories) != 1 || p.PromptTurns[0].Overview.Categories[0].Kind != "unknown" {
		t.Fatal("unobserved work inferred from request")
	}
}

func TestOverviewParallelCategoryTimeIsUnionAndWrapperIsNotAnotherExecution(t *testing.T) {
	call := fixtureCall(0, "p")
	a, b := execution("test-a", 0, 10), execution("test-b", 0, 10)
	a.WrapperRef, b.WrapperRef = "wrapper", "wrapper"
	in := Input{Calls: []Call{call}, Facts: []telemetry.Fact{promptFact("s", "p", 0), a, b}, Actions: []Action{{ID: "s#tool#wrapper", SessionID: "s", CallID: call.ID, Tool: "functions.exec", Summary: "opaque wrapper", StartedAt: stamp(at(0)), EndedAt: stamp(at(10))}}}
	p := Project(in, Options{Closed: true})
	o := p.PromptTurns[0].Overview
	category := categoryByKind(t, o, "test")
	if category.ElapsedMs == nil || *category.ElapsedMs != 10000 || category.ExecutionCount != 2 || category.ModelCalls != 1 || *category.Tokens != p.Tokens {
		t.Fatalf("parallel category %+v", category)
	}
	if len(o.Categories) != 1 {
		t.Fatal("linked wrapper counted as separate unknown operation")
	}
}

func TestOverviewRecognizesResponseOnlyWithRecordedMessage(t *testing.T) {
	call := fixtureCall(0, "p")
	response := telemetry.Fact{ID: "r0", Kind: "response", SessionID: "s", ResponseID: "r0", Text: "The answer is recorded", OccurredAt: at(5)}
	p := Project(Input{Calls: []Call{call}, Facts: []telemetry.Fact{promptFact("s", "p", 0), response}}, Options{Closed: true})
	categoryByKind(t, p.PromptTurns[0].Overview, "response")
	if len(p.PromptTurns[0].Overview.Categories) != 1 {
		t.Fatal("response generated unrelated work type")
	}
}

func TestWorkClassifierUsesOperationsNotQuotedTextOrHeredocBrief(t *testing.T) {
	tests := []struct {
		name      string
		execution Execution
		want      []string
	}{
		{"review", Execution{Tool: "Bash", Command: "git -C /repo --no-pager diff -- src.go"}, []string{"review"}},
		{"mate_review", Execution{Tool: "Bash", Command: "/tmp/mate review shop crew 2>&1"}, []string{"review"}},
		{"mate_poll", Execution{Tool: "Bash", Command: "/tmp/mate peek shop crew 2>&1 | tail -25"}, []string{"coordination"}},
		{"test_filter", Execution{Tool: "Bash", Command: "go test ./... 2>&1 | head -20"}, []string{"test"}},
		{"quoted_test", Execution{Tool: "Bash", Command: `echo "go test ./...; git diff; mate review"`}, []string{"unknown"}},
		{"quoted_cmd", Execution{Tool: "exec_command", Command: `echo 'cmd:"go test ./..."'`}, []string{"unknown"}},
		{"search_cmd", Execution{Tool: "exec_command", Command: `rg 'cmd:"go test ./..."' src.go`}, []string{"research"}},
		{"structured_cmd", Execution{Tool: "Bash", Command: `{"command":"go test ./..."}`}, []string{"test"}},
		{"quoted_patch", Execution{Tool: "Bash", Command: `echo '*** Begin Patch\n*** Update File: src.go'`}, []string{"unknown"}},
		{"brief_patch", Execution{Tool: "Bash", Command: "cat > brief.md <<'EOF'\n*** Begin Patch\n*** Update File: src.go\nEOF"}, []string{"write_code"}},
		{"wrapper_patch", Execution{Tool: "functions.exec", IsWrapper: true, Command: `await tools.apply_patch("*** Begin Patch\n*** Add File: src.go\n+package main\n*** End Patch")`}, []string{"write_code"}},
		{"quoted_wrapper_patch", Execution{Tool: "functions.exec", IsWrapper: true, Command: "text('tools.apply_patch(\"*** Begin Patch\\n*** Update File: src.go\")')"}, []string{"unknown"}},
		{"shell_patch", Execution{Tool: "Bash", Command: "apply_patch <<'PATCH'\n*** Begin Patch\n*** Add File: new.go\n+package main\n*** End Patch\nPATCH"}, []string{"write_code"}},
		{"mixed_poll_test", Execution{Tool: "functions.exec", IsWrapper: true, Poll: true, Command: `await tools.write_stdin({session_id:42}); await tools.exec_command({cmd:"go test ./..."})`}, []string{"wait", "test"}},
		{"brief", Execution{Tool: "Bash", Command: "cat > brief.md <<'EOF'\nRun go test ./...\ngit diff\nEOF\nmate crew spawn shop crew --brief brief.md"}, []string{"write_code", "coordination"}},
		{"write", Execution{Tool: "Write", Command: `{"file_path":"new.go","content":"go test ./..."}`}, []string{"write_code"}},
		{"edit", Execution{Tool: "Edit", Command: `{"file_path":"src.go","new_string":"fix bug"}`}, []string{"edit_code"}},
		{"instructions", Execution{Tool: "Read", Target: "/repo/AGENTS.md"}, []string{"instructions"}},
		{"web", Execution{Tool: "web.run", Command: `{"search_query":[{"q":"how to test Go"}]}`}, []string{"research"}},
		{"native_poll", Execution{Tool: "exec", Poll: true, ProcessID: "42"}, []string{"wait"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := executionWork(test.execution); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("kinds %v want %v", got, test.want)
			}
		})
	}
	quoted := Execution{Tool: "exec_command", Command: `rg 'cmd:"go test ./..."' src.go`}
	if executionCommand(quoted) != quoted.Command || !strings.HasPrefix(executionLabel(quoted), "rg ") {
		t.Fatal("quoted example replaced actual command in evidence")
	}
}

func TestOverviewMixedPollAndTestChargesOneMixedCall(t *testing.T) {
	call := fixtureCall(0, "p")
	in := Input{Calls: []Call{call}, Facts: []telemetry.Fact{promptFact("s", "p", 0), {
		ID: "wrapper", Kind: "tool_call", SessionID: "s", HarnessTurnRef: "p", ResponseID: "r0", WrapperRef: "wrapper",
		Tool: "functions.exec", Poll: true, ProcessID: "42", OccurredAt: at(1),
		Command: `await tools.write_stdin({session_id:42}); await tools.exec_command({cmd:"go test ./..."})`,
	}}}
	p := Project(in, Options{Closed: true})
	o := p.PromptTurns[0].Overview
	for _, kind := range []string{"wait", "test"} {
		if c := categoryByKind(t, o, kind); c.Tokens != nil || c.ModelCalls != 0 {
			t.Fatalf("mixed operation charged to %s: %+v", kind, c)
		}
	}
	mixed := categoryByKind(t, o, "mixed")
	if mixed.Tokens == nil || *mixed.Tokens != p.Tokens || mixed.ModelCalls != 1 || len(o.Sequence) != 1 || o.Sequence[0].Kind != "mixed" {
		t.Fatalf("mixed poll accounting %+v", o)
	}
}

func TestOverviewHistoricalUnfinishedExecutionDoesNotAccrueLiveTime(t *testing.T) {
	old := execution("old", 1, 3)
	old.CompletedAt, old.ExitCode, old.Status = nil, nil, "running"
	current := execution("current", 11, 15)
	current.CompletedAt, current.ExitCode, current.Status, current.HarnessTurnRef = nil, nil, "running", "next"
	in := Input{Facts: []telemetry.Fact{promptFact("s", "p", 0), old,
		{ID: "end", Kind: "turn_completed", SessionID: "s", HarnessTurnRef: "p", OccurredAt: at(5)},
		promptFact("s", "next", 10), current}}
	p := Project(in, Options{Now: at(20)})
	if p.Time.ToolElapsedMs == nil || *p.Time.ToolElapsedMs != 9000 || p.Time.InvocationMs == nil || *p.Time.InvocationMs != 9000 {
		t.Fatalf("historical unfinished execution grew elapsed: %+v", p.Time)
	}
	if c := categoryByKind(t, p.PromptTurns[0].Overview, "test"); c.ElapsedMs != nil {
		t.Fatalf("closed prompt category timing invented: %+v", c)
	}
	if c := categoryByKind(t, p.PromptTurns[1].Overview, "test"); c.ElapsedMs == nil || *c.ElapsedMs != 9000 {
		t.Fatalf("live prompt timing lost: %+v", c)
	}
}

func TestFractionalTimesOrderPromptsCallsAndExecutions(t *testing.T) {
	earlierPrompt, laterPrompt := promptFact("s", "p", 0), promptFact("s", "next", 0)
	laterPrompt.OccurredAt = at(0).Add(500 * time.Millisecond)
	a, b := fixtureCall(0, "p"), fixtureCall(1, "next")
	b.StartedAt = stamp(laterPrompt.OccurredAt)
	first, second := execution("first", 0, 1), execution("second", 0, 1)
	second.StartedAt, second.HarnessTurnRef = &laterPrompt.OccurredAt, "next"
	p := Project(Input{Calls: []Call{b, a}, Facts: []telemetry.Fact{laterPrompt, earlierPrompt, second, first}}, Options{Closed: true})
	if p.PromptTurns[0].ID != "s#prompt#p" || p.Executions[0].ID != "s#execution#first" || p.Segments[0].CallIDs[0] != a.ID {
		t.Fatalf("RFC3339Nano lexical order reversed same-second activity: %+v", p.PromptTurns)
	}
}
