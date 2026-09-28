package diagnostics

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

var epoch = time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

func at(sec int) time.Time   { return epoch.Add(time.Duration(sec) * time.Second) }
func atp(sec int) *time.Time { v := at(sec); return &v }
func intp(n int) *int        { return &n }
func n64(n int64) *int64     { return &n }
func fixtureCall(i int, prompt string) Call {
	return Call{ID: fmt.Sprintf("s#turn#r%d", i), SessionID: "s", HarnessTurnRef: prompt, Ordinal: int64(i), StartedAt: stamp(at(i * 10)), EndedAt: stamp(at(i*10 + 5)),
		Tokens: Tokens{Input: int64(i + 1), CacheRead: 20, CacheWrite: 3, Output: 7, Thinking: 2}, Ref: Ref{"rollout.jsonl", int64(i * 100)}}
}
func promptFact(session, prompt string, sec int) telemetry.Fact {
	return telemetry.Fact{ID: prompt, Kind: "prompt", SessionID: session, HarnessTurnRef: prompt, Text: "Fix the tests", OccurredAt: at(sec)}
}
func execution(id string, start, end int) telemetry.Fact {
	return telemetry.Fact{ID: id, Kind: "execution", ExecutionID: id, SessionID: "s", HarnessTurnRef: "p", Tool: "exec_command", Command: "go test ./...", StartedAt: atp(start), CompletedAt: atp(end), Status: "completed", ExitCode: intp(0), SourceOffset: int64(start * 100), MeasurementKind: "native"}
}
func findingsOf(p Performance, kind string) []Finding {
	out := []Finding{}
	for _, f := range p.Findings {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

func TestLedgerBucketsBelongToOneContiguousSegmentAndPrompt(t *testing.T) {
	in := Input{}
	for i, tool := range []string{"Read", "Edit", "Bash", "Read"} {
		c := fixtureCall(i, "p")
		in.Calls = append(in.Calls, c)
		target := "src/main.go"
		if tool == "Bash" {
			target = "go test ./..."
		}
		in.Actions = append(in.Actions, Action{ID: fmt.Sprintf("a%d", i), CallID: c.ID, SessionID: "s", Tool: tool, Target: target, Summary: target, StartedAt: c.StartedAt, EndedAt: c.EndedAt})
	}
	in.Facts = []telemetry.Fact{promptFact("s", "p", 0), {ID: "huge", Kind: "response", SessionID: "s", ResponseID: "r0", InputTokens: 9999999}}
	p := Project(in, Options{Now: at(100), Closed: true})
	if len(p.PromptTurns) != 1 || len(p.Segments) != 4 {
		t.Fatalf("prompts %d segments %d; returning read phases must stay separate", len(p.PromptTurns), len(p.Segments))
	}
	var total Tokens
	seen := map[string]bool{}
	for _, s := range p.Segments {
		total.Add(s.Tokens)
		for _, id := range s.CallIDs {
			if seen[id] {
				t.Fatalf("double charge %s", id)
			}
			seen[id] = true
		}
	}
	if total != p.Tokens || p.PromptTurns[0].Tokens != p.Tokens {
		t.Fatalf("segments %+v prompts %+v ledger %+v", total, p.PromptTurns[0].Tokens, p.Tokens)
	}
	if p.Tokens.Total != 130 || p.Tokens.Thinking != 8 {
		t.Fatalf("usage %+v; native usage and thinking must not be added", p.Tokens)
	}
	if p.CurrentSegmentID != "" {
		t.Fatal("closed run advertised current work")
	}
}

func TestParallelTimeUsesUnionAndUnknownTimingStaysUnknown(t *testing.T) {
	a, b := execution("one", 0, 10), execution("two", 0, 10)
	b.SourceOffset = 2
	in := Input{Facts: []telemetry.Fact{promptFact("s", "p", 0), a, b, {ID: "end", Kind: "turn_completed", SessionID: "s", HarnessTurnRef: "p", OccurredAt: at(20)}}}
	in.Decisions = []Decision{{ID: "q", AskedAt: stamp(at(5)), AnsweredAt: stamp(at(15))}}
	p := Project(in, Options{Now: at(20)})
	if p.Time.ToolElapsedMs == nil || *p.Time.ToolElapsedMs != 10000 || *p.Time.InvocationMs != 20000 {
		t.Fatalf("tool time %+v", p.Time)
	}
	if *p.Time.PromptElapsedMs != 20000 || *p.Time.DecisionWaitMs != 10000 || *p.Time.UnallocatedMs != 5000 {
		t.Fatalf("overlapping lanes %+v", p.Time)
	}
	u := Project(Input{Calls: []Call{fixtureCall(0, "")}}, Options{Now: at(20)})
	if u.Time.PromptElapsedMs != nil || u.Time.ToolElapsedMs != nil {
		t.Fatalf("missing measurements became zero: %+v", u.Time)
	}
}

func TestLateResultCompletesOneExecutionAndExposesNestedFailure(t *testing.T) {
	start := execution("child", 0, 10)
	start.CompletedAt = nil
	start.ExitCode = nil
	start.Status = "running"
	finish := execution("child", 0, 10)
	finish.SourceOffset = 100
	finish.ExitCode = intp(2)
	finish.Status = "failed"
	c := fixtureCall(0, "p")
	ok := true
	in := Input{Calls: []Call{c}, Actions: []Action{{ID: "wrapper", CallID: c.ID, Tool: "functions.exec", OK: &ok}}, Facts: []telemetry.Fact{start, finish}}
	p := Project(in, Options{Now: at(20)})
	if len(p.Executions) != 2 {
		t.Fatalf("late result duplicated execution: %+v", p.Executions)
	}
	fs := findingsOf(p, "execution_failure")
	if len(fs) != 1 || fs[0].Count != 1 || fs[0].Tokens != nil {
		t.Fatalf("native failure not exposed or fabricated call attribution: %+v", fs)
	}
	if len(fs[0].Evidence) != 1 || !strings.Contains(fs[0].Evidence[0].Label, "exit 2") {
		t.Fatalf("missing exit evidence %+v", fs)
	}
	open := Project(Input{Facts: []telemetry.Fact{promptFact("s", "p", 0), start}}, Options{Now: at(20)})
	if open.Executions[0].ExitCode != nil || open.Executions[0].OutputBytes != nil || len(findingsOf(open, "execution_failure")) != 0 {
		t.Fatal("running execution invented result")
	}
	if open.Time.ToolElapsedMs == nil || *open.Time.ToolElapsedMs != 20000 {
		t.Fatalf("running tool time stopped %+v", open.Time)
	}
}

func TestPollingLinksOneJobAndDistinguishesNewOutput(t *testing.T) {
	job := execution("job", 0, 60)
	job.ProcessID = "42"
	in := Input{Facts: []telemetry.Fact{job}}
	for i := 0; i < 3; i++ {
		c := fixtureCall(i, "p")
		in.Calls = append(in.Calls, c)
		ref := fmt.Sprintf("poll%d", i)
		in.Actions = append(in.Actions, Action{ID: "s#tool#" + ref, CallID: c.ID, SessionID: "s", Tool: "functions.exec"})
		f := telemetry.Fact{ID: ref, Kind: "tool_call", SessionID: "s", SourceRef: ref, WrapperRef: ref, Tool: "functions.exec", Poll: true, ProcessID: "42", OccurredAt: at(i * 10), SourceOffset: int64(i), Output: &telemetry.Output{SHA256: "result", Bytes: 100}}
		if i == 0 {
			f.Output.NewBytes = n64(0)
		}
		if i == 1 {
			f.Output.NewBytes = n64(12)
		}
		in.Facts = append(in.Facts, f)
	}
	p := Project(in, Options{Now: at(100)})
	if len(p.Processes) != 1 {
		t.Fatalf("process chains %+v", p.Processes)
	}
	proc := p.Processes[0]
	if proc.Polls != 3 || proc.UnchangedPolls != 1 || proc.ProgressPolls != 1 || proc.UnknownPolls != 1 || len(proc.ExecutionIDs) != 1 {
		t.Fatalf("poll chain %+v", proc)
	}
	if proc.Tokens == nil || proc.Tokens.Total != p.Tokens.Total {
		t.Fatal("polling usage not from each ledger response once")
	}
	if len(findingsOf(p, "process_polling")) != 1 {
		t.Fatal("missing poll finding")
	}
}

func TestRepeatedReadsRequireSameContentAndNoRecordedChanges(t *testing.T) {
	read := func(id string, start int, hash, command string) telemetry.Fact {
		e := execution(id, start, start+1)
		e.Command = command
		e.Output = &telemetry.Output{SHA256: hash, Bytes: 10}
		return e
	}
	in := Input{Facts: []telemetry.Fact{read("a", 0, "same", "sed -n '1,10p' src.go"), read("b", 10, "same", "sed -n '1,10p' src.go"), read("c", 20, "same", "sed -n '1,10p' src.go")}}
	if len(findingsOf(Project(in, Options{Now: at(100)}), "repeated_read")) != 1 {
		t.Fatal("same reads not detected")
	}
	in.Facts = append(in.Facts, telemetry.Fact{ID: "edit", Kind: "progress", SessionID: "s", OccurredAt: at(15), Changes: []telemetry.Change{{Path: "src.go"}}})
	if len(findingsOf(Project(in, Options{Now: at(100)}), "repeated_read")) != 0 {
		t.Fatal("reading after edit treated as unchanged repetition")
	}
	in.Facts = []telemetry.Fact{read("a", 0, "same", "sed -n '1,10p' src.go"), read("b", 10, "same", "sed -n '20,30p' src.go"), read("c", 20, "same", "sed -n '40,50p' src.go")}
	if len(findingsOf(Project(in, Options{}), "repeated_read")) != 0 {
		t.Fatal("different ranges merged")
	}
	in.Facts = []telemetry.Fact{read("a", 0, "one", "cat src.go"), read("b", 10, "two", "cat src.go"), read("c", 20, "one", "cat src.go")}
	if len(findingsOf(Project(in, Options{ReadThreshold: 2}), "repeated_read")) != 0 {
		t.Fatal("content changes between reads ignored")
	}
}

func TestRetryAfterEditIsRepairSequenceAndMissingStateIsNotUnchanged(t *testing.T) {
	a, b := execution("a", 0, 1), execution("b", 10, 11)
	for _, e := range []*telemetry.Fact{&a, &b} {
		e.Status = "failed"
		e.ExitCode = intp(1)
		e.Output = &telemetry.Output{SHA256: "same-error", Bytes: 24}
	}
	in := Input{Facts: []telemetry.Fact{a, b}}
	fs := findingsOf(Project(in, Options{}), "repeated_error")
	if len(fs) != 1 || !strings.Contains(fs[0].Detail, "coverage is incomplete") {
		t.Fatalf("retry lacking state evidence %+v", fs)
	}
	in.Facts = append(in.Facts, telemetry.Fact{ID: "edit", Kind: "progress", SessionID: "s", OccurredAt: at(5), Changes: []telemetry.Change{{Path: "file.go"}}})
	p := Project(in, Options{})
	if len(findingsOf(p, "repeated_error")) != 0 || len(findingsOf(p, "repair_test_loop")) != 1 {
		t.Fatal("retry after edit mislabeled")
	}
	b.Command = "go test ./... -run Different"
	in.Facts = []telemetry.Fact{a, b}
	if len(findingsOf(Project(in, Options{}), "repeated_error")) != 0 {
		t.Fatal("different target merged")
	}
}

func TestUnknownPromptAndSameIDAcrossSessionsAreNotMerged(t *testing.T) {
	a, b, c, d := fixtureCall(0, "same"), fixtureCall(1, "same"), fixtureCall(2, ""), fixtureCall(3, "")
	b.SessionID = "other"
	b.ID = "other#turn#r1"
	p := Project(Input{Calls: []Call{a, b, c, d}}, Options{})
	if len(p.PromptTurns) != 4 {
		t.Fatalf("invented prompt correlation %+v", p.PromptTurns)
	}
}

func TestLateNativeResponseAliasDoesNotCreateMissingLedgerGap(t *testing.T) {
	c := fixtureCall(0, "p")
	c.ID = "s#turn#token_count0"
	f := telemetry.Fact{ID: "r", Kind: "response", SessionID: "s", ResponseID: "native", SourcePath: c.Ref.Path}
	late := f
	late.LedgerRefOffset = &c.Ref.Offset
	p := Project(Input{Calls: []Call{c}, Facts: []telemetry.Fact{f, late}}, Options{})
	for _, gap := range p.Freshness.Missing {
		if strings.Contains(gap, "no confirmed ledger match") {
			t.Fatal(gap)
		}
	}
}

func TestWrapperLabelsShowLiteralCommandAndBuildIntent(t *testing.T) {
	e := Execution{IsWrapper: true, Tool: "functions.exec", Command: `const r=await tools.exec_command({cmd:"xcodebuild -scheme Trip -only-testing:TripUITests | head -20"}); text(r)`}
	kind, target, label := classifyCall([]Execution{e}, "")
	if kind != "test" || target != "xcodebuild -scheme Trip -only-testing:TripUITests | head -20" || strings.Contains(label, "tools.exec") {
		t.Fatalf("kind=%s target=%q label=%q", kind, target, label)
	}
}

func TestLegacyThinkingIsNotAnExecution(t *testing.T) {
	c := fixtureCall(0, "p")
	p := Project(Input{Calls: []Call{c}, Actions: []Action{{ID: "fake", CallID: c.ID, Tool: "thinking", StartedAt: stamp(at(0)), EndedAt: stamp(at(10))}}}, Options{})
	if len(p.Executions) != 0 || p.Time.ToolElapsedMs != nil {
		t.Fatalf("inferred thinking became measured execution: %+v", p)
	}
}
