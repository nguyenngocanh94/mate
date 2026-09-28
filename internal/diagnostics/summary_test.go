package diagnostics

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

// Two prompts, three calls: the crew overview must be the prompt overviews
// added together, produced by the same classifier, with no sequence.
func TestCrewOverviewSumsPromptCategoriesAndConservesTokens(t *testing.T) {
	in := Input{Facts: []telemetry.Fact{promptFact("s", "p1", 0), promptFact("s", "p2", 100)}}
	for i, tool := range []string{"Bash", "Edit", "Bash"} {
		prompt := "p1"
		if i == 2 {
			prompt = "p2"
		}
		c := fixtureCall(i, prompt)
		in.Calls = append(in.Calls, c)
		command := "go test ./..."
		if tool == "Edit" {
			command = "src.go"
		}
		in.Actions = append(in.Actions, Action{ID: c.ID + "#action", SessionID: "s", CallID: c.ID, Tool: tool, Summary: command, StartedAt: c.StartedAt, EndedAt: c.EndedAt})
	}
	p := Project(in, Options{Now: at(200), Closed: true})
	o := p.Overview
	if len(o.Sequence) != 0 || o.Rule == "" || o.Coverage == "" {
		t.Fatalf("crew overview shape %+v", o)
	}
	var tokens Tokens
	calls := 0
	for _, c := range o.Categories {
		if c.Tokens != nil {
			tokens.Add(*c.Tokens)
		}
		calls += c.ModelCalls
	}
	if tokens != p.Tokens || calls != 3 {
		t.Fatalf("crew categories %+v/%d, ledger %+v", tokens, calls, p.Tokens)
	}
	if categoryByKind(t, o, "test").ModelCalls != 2 || categoryByKind(t, o, "edit_code").ModelCalls != 1 {
		t.Fatalf("crew categories %+v", o.Categories)
	}
	// Per-prompt overviews are untouched by the crew-level one.
	if categoryByKind(t, p.PromptTurns[0].Overview, "test").ModelCalls != 1 || categoryByKind(t, p.PromptTurns[1].Overview, "test").ModelCalls != 1 {
		t.Fatal("prompt overviews changed")
	}
	if !strings.HasSuffix(o.Coverage, " across 2 prompts.") {
		t.Fatalf("crew coverage %q", o.Coverage)
	}
	// No activity at all is still a Crew-level statement, not a prompt's.
	empty := Project(Input{}, Options{Now: at(0), Closed: true}).Overview
	if len(empty.Categories) != 0 || len(empty.Sequence) != 0 || !strings.HasSuffix(empty.Coverage, " across 0 prompts.") {
		t.Fatalf("empty crew overview %+v", empty)
	}
}

func TestOutputRankingsPreferWhatTheModelWroteNotCacheReads(t *testing.T) {
	in := Input{Facts: []telemetry.Fact{promptFact("s", "p", 0)}}
	heavyCache, heavyOutput, small := fixtureCall(0, "p"), fixtureCall(1, "p"), fixtureCall(2, "p")
	heavyCache.Tokens = Tokens{Input: 10, CacheRead: 900000, Output: 5, Thinking: 1}
	heavyOutput.Tokens = Tokens{Input: 10, CacheRead: 100, Output: 3000, Thinking: 700}
	small.Tokens = Tokens{Input: 1, CacheRead: 1, Output: 1}
	in.Calls = []Call{heavyCache, heavyOutput, small}
	for i, c := range in.Calls {
		tool := []string{"Bash", "Edit", "Read"}[i]
		in.Actions = append(in.Actions, Action{ID: c.ID + "#a", SessionID: "s", CallID: c.ID, Tool: tool, Summary: "x", StartedAt: c.StartedAt, EndedAt: c.EndedAt})
	}
	p := Project(in, Options{Now: at(100), Closed: true})
	if len(p.TopSegmentIDs) == 0 || p.TopSegmentIDs[0] != "segment#"+heavyCache.ID {
		t.Fatalf("total ranking %v", p.TopSegmentIDs)
	}
	if len(p.TopOutputSegmentIDs) != 3 || p.TopOutputSegmentIDs[0] != "segment#"+heavyOutput.ID {
		t.Fatalf("output ranking %v", p.TopOutputSegmentIDs)
	}
	if len(p.TopOutputCallIDs) != 3 || p.TopOutputCallIDs[0] != heavyOutput.ID || p.TopOutputCallIDs[2] != small.ID {
		t.Fatalf("call ranking %v", p.TopOutputCallIDs)
	}
}

// Same fixture as TestPollingLinksOneJobAndDistinguishesNewOutput: one job,
// three polls in three calls of one prompt. The three polls share one wait
// segment, and the polling finding marks that segment with count-1 = 2
// repeats, so segment_repeats is 2.
func TestLoopsSummarizePollingOnceAndStayUnmeasuredWithoutNativeStatus(t *testing.T) {
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
	l := p.Loops
	if !l.Measured || l.Chains != 1 || l.Polls != 3 || l.UnchangedPolls != 1 || l.ProgressPolls != 1 || l.UnknownPolls != 1 || l.ProcessesPolled != 1 {
		t.Fatalf("loops %+v", l)
	}
	if l.PollCalls != 3 || l.PollTokens == nil || *l.PollTokens != p.Tokens || l.SegmentRepeats != 2 {
		t.Fatalf("poll usage %+v vs ledger %+v", l, p.Tokens)
	}
	claude := Project(Input{Calls: []Call{fixtureCall(0, "p")}}, Options{Now: at(100)})
	if claude.Loops.Measured || claude.Loops.Polls != 0 || claude.Loops.PollTokens != nil || !strings.Contains(claude.Loops.Coverage, "not measurable") {
		t.Fatalf("unmeasured harness invented polling: %+v", claude.Loops)
	}
}

// Rankings keep five ids, and an exact tie on output + thinking falls back
// to the id so equal usage ranks the same way on every projection.
func TestTopIDsKeepFiveAndBreakTiesByID(t *testing.T) {
	items := []rank{{"f", 1}, {"b", 7}, {"e", 3}, {"a", 7}, {"d", 5}, {"c", 6}}
	if got := topIDs(items, 5); fmt.Sprint(got) != "[a b c d e]" {
		t.Fatalf("ranking %v", got)
	}
	in := Input{}
	// r3 and r4 tie at 7 with different output/thinking splits; r1 is the
	// sixth and drops out.
	for i, tokens := range []Tokens{{Output: 10}, {Output: 1}, {Output: 8}, {Output: 5, Thinking: 2}, {Output: 7}, {Output: 9}} {
		c := fixtureCall(i, "p")
		c.Tokens = tokens
		in.Calls = append(in.Calls, c)
	}
	p := Project(in, Options{Now: at(100), Closed: true})
	if fmt.Sprint(p.TopOutputCallIDs) != "[s#turn#r0 s#turn#r5 s#turn#r2 s#turn#r3 s#turn#r4]" {
		t.Fatalf("call ranking %v", p.TopOutputCallIDs)
	}
}
