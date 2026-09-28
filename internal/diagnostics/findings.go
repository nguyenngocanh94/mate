package diagnostics

import (
	"fmt"
	"sort"
	"strings"
)

func (p *projection) detect() {
	p.detectProcesses()
	p.detectReads()
	p.detectRetries()
	p.detectOutputsAndSlowTools()
	p.detectContext()
	failed := []Execution{}
	for _, e := range p.out.Executions {
		if !e.IsWrapper && !e.Poll && (e.Status == "failed" || (e.ExitCode != nil && *e.ExitCode != 0)) {
			failed = append(failed, e)
		}
	}
	if len(failed) > 0 {
		f := p.finding("execution_failure", failed)
		f.Title = fmt.Sprintf("%d command execution(s) failed", len(failed))
		f.Detail = "The recorded command status or exit code reports failure. Open the evidence to inspect the actual command and output."
		f.Severity = "warning"
		f.Review = "Review the failing test/build command, prerequisites and repo setup instructions."
		p.out.Findings = append(p.out.Findings, f)
	}
	for _, q := range p.in.Decisions {
		end := q.AnsweredAt
		if end == "" && !p.opts.Closed {
			end = stamp(p.opts.Now)
		}
		wait := duration(q.AskedAt, end)
		if wait == nil {
			continue
		}
		f := Finding{ID: "decision#" + q.ID, Kind: "decision_wait", Title: "Waiting for a decision", Detail: q.Text,
			Severity: "info", Confidence: "recorded", Ongoing: q.AnsweredAt == "" && !p.opts.Closed,
			StartedAt: q.AskedAt, EndedAt: q.AnsweredAt, Count: 1, SegmentIDs: []string{}, CallIDs: []string{}, ExecutionIDs: []string{},
			Evidence: []Evidence{{ID: fmt.Sprintf("event#%d", q.EventID), Kind: "question", Label: q.Text, At: q.AskedAt}},
			Review:   "Review the brief, decision authority and path for answering Crew questions.", Rule: Version + "/decision"}
		if !f.Ongoing {
			f.Title = "Decision wait completed"
		}
		f.Detail = fmt.Sprintf("%s · %s observed waiting; this is not model or CPU time.", q.Text, elapsedWord(*wait))
		p.out.Findings = append(p.out.Findings, f)
	}
}

func (p *projection) finding(kind string, executions []Execution) Finding {
	f := Finding{Kind: kind, Severity: "info", Confidence: "recorded", Count: len(executions), Rule: Version + "/" + kind,
		SegmentIDs: []string{}, CallIDs: []string{}, ExecutionIDs: []string{}, Evidence: []Evidence{}}
	calls, segments := map[string]bool{}, map[string]bool{}
	for _, e := range executions {
		if f.ID == "" {
			f.ID = kind + "#" + e.ID
		}
		f.StartedAt = earlier(f.StartedAt, e.StartedAt)
		f.EndedAt = later(f.EndedAt, e.EndedAt)
		f.Ongoing = f.Ongoing || (!p.opts.Closed && e.Status == "running")
		f.ExecutionIDs = append(f.ExecutionIDs, e.ID)
		label := e.Command
		if label == "" {
			label = e.Target
		}
		if label == "" {
			label = e.Tool
		}
		if e.ExitCode != nil {
			label += fmt.Sprintf(" · exit %d", *e.ExitCode)
		}
		f.Evidence = append(f.Evidence, Evidence{ID: e.ID, Kind: "execution", Label: short(label, 240), At: e.StartedAt, SourceRef: e.SourceRef})
		if e.CallID != "" {
			calls[e.CallID] = true
		}
	}
	for _, id := range sortedKeys(calls) {
		if c, ok := p.call[id]; ok {
			if f.Tokens == nil {
				f.Tokens = &Tokens{}
			}
			f.Tokens.Add(c.Tokens)
			f.CallIDs = append(f.CallIDs, id)
			if idx, ok := p.callSegment[id]; ok {
				segments[p.out.Segments[idx].ID] = true
			}
		}
	}
	f.SegmentIDs = sortedKeys(segments)
	return f
}

func (p *projection) detectProcesses() {
	groups := map[string][]Execution{}
	for _, e := range p.out.Executions {
		if e.ProcessID != "" {
			k := key(e.SessionID, e.ProcessID)
			groups[k] = append(groups[k], e)
		}
	}
	for _, k := range sortedGroupKeys(groups) {
		es := groups[k]
		proc := Process{ID: es[0].ProcessID, SessionID: es[0].SessionID, ExecutionIDs: []string{}, PollIDs: []string{}}
		polls := []Execution{}
		spans := []interval{}
		for _, e := range es {
			if !e.Poll {
				proc.ExecutionIDs = append(proc.ExecutionIDs, e.ID)
				if !e.IsWrapper && e.Command != "" {
					proc.Command = e.Command
				}
				if span, ok := p.executionInterval(e); ok && !e.IsWrapper {
					spans = append(spans, span)
				}
				continue
			}
			polls = append(polls, e)
			proc.PollIDs = append(proc.PollIDs, e.ID)
			proc.Polls++
			if e.NewOutputBytes == nil {
				proc.UnknownPolls++
			} else if *e.NewOutputBytes == 0 {
				proc.UnchangedPolls++
			} else {
				proc.ProgressPolls++
			}
		}
		if len(spans) > 0 {
			n := unionMs(spans)
			proc.ElapsedMs = &n
		}
		f := p.finding("process_polling", polls)
		proc.Tokens = f.Tokens
		p.out.Processes = append(p.out.Processes, proc)
		if proc.Polls < p.opts.PollThreshold {
			continue
		}
		job := proc.Command
		if job == "" {
			job = "process " + proc.ID
		}
		f.Title = fmt.Sprintf("%s was polled %d times", short(job, 88), proc.Polls)
		f.Detail = fmt.Sprintf("%d poll(s) had no new output, %d had new output, %d have unknown output changes. Polling calls are counted once; polls are not additional job executions.", proc.UnchangedPolls, proc.ProgressPolls, proc.UnknownPolls)
		f.Review = "Review wait duration and completion notification support in the repo harness. Repeated polling alone does not show a stalled job."
		p.markRepeats(f)
		p.out.Findings = append(p.out.Findings, f)
	}
}

func (p *projection) detectReads() {
	groups := map[string][]Execution{}
	flush := func(k string) {
		es := groups[k]
		if len(es) < p.opts.ReadThreshold {
			return
		}
		f := p.finding("repeated_read", es)
		target := es[0].Target
		if target == "" {
			target = es[0].Command
		}
		f.Title = fmt.Sprintf("%s returned unchanged content %d times", short(target, 88), len(es))
		f.Detail = "The same exact query or file/range returned the same recorded output hash. No recorded file change separates these reads; external edits are not measured."
		f.Review = "Review the repo map, file-finding instructions and retention of already read results."
		p.markRepeats(f)
		p.out.Findings = append(p.out.Findings, f)
	}
	for _, e := range p.out.Executions {
		kind := executionKind(e)
		if (kind != "research" && kind != "instructions") || e.Poll || e.OutputHash == "" || e.OutputBytes == nil {
			continue
		}
		k := p.operationKey(e)
		prev := groups[k]
		if len(prev) > 0 && (prev[len(prev)-1].OutputHash != e.OutputHash || p.changesBetween(prev[len(prev)-1], e) > 0) {
			flush(k)
			groups[k] = nil
		}
		groups[k] = append(groups[k], e)
	}
	for _, k := range sortedGroupKeys(groups) {
		flush(k)
	}
}

func (p *projection) detectRetries() {
	groups := map[string][]Execution{}
	flush := func(k string) {
		es := groups[k]
		if len(es) < p.opts.RetryThreshold {
			return
		}
		changes := 0
		for i := 1; i < len(es); i++ {
			changes += p.changesBetween(es[i-1], es[i])
		}
		kind := "repeated_error"
		if changes > 0 {
			kind = "repair_test_loop"
		}
		f := p.finding(kind, es)
		f.Title = fmt.Sprintf("%s returned the same error %d times", short(es[0].Command, 88), len(es))
		if changes > 0 {
			f.Detail = fmt.Sprintf("The same command, working directory and output hash recur across %d recorded file-change event(s). This is a repair/retest sequence, not evidence of retrying without changes.", changes)
			for _, evidence := range p.out.Progress {
				if !parse(evidence.At).Before(parse(f.StartedAt)) && !parse(evidence.At).After(parse(f.EndedAt)) {
					f.Evidence = append(f.Evidence, evidence)
				}
			}
		} else {
			f.Detail = "The same command, working directory and failed output hash recur. No file change was recorded between attempts; repository state coverage is incomplete, so unchanged code is not established."
		}
		f.Severity = "warning"
		f.Review = "Review the failing test target, prerequisites and troubleshooting instructions for this repo."
		p.markRepeats(f)
		p.out.Findings = append(p.out.Findings, f)
	}
	for _, e := range p.out.Executions {
		if e.IsWrapper || e.Poll {
			continue
		}
		k := p.operationKey(e)
		prev := groups[k]
		if e.Status != "failed" || e.ErrorSignature == "" {
			if len(prev) > 0 {
				flush(k)
				groups[k] = nil
			}
			continue
		}
		if len(prev) > 0 && prev[len(prev)-1].ErrorSignature != e.ErrorSignature {
			flush(k)
			groups[k] = nil
		}
		groups[k] = append(groups[k], e)
	}
	for _, k := range sortedGroupKeys(groups) {
		flush(k)
	}
}

func (p *projection) changesBetween(a, b Execution) int {
	count := 0
	for _, f := range p.progress {
		at := stamp(f.OccurredAt)
		if f.SessionID == a.SessionID && at != "" && parse(at).After(parse(a.StartedAt)) && parse(at).Before(parse(b.StartedAt)) {
			count++
		}
	}
	return count
}

func (p *projection) markRepeats(f Finding) {
	for _, id := range f.SegmentIDs {
		for i := range p.out.Segments {
			if p.out.Segments[i].ID == id {
				p.out.Segments[i].RepeatCount += max(0, f.Count-1)
			}
		}
	}
}

func (p *projection) detectOutputsAndSlowTools() {
	for _, e := range p.out.Executions {
		if !e.IsWrapper && !e.Poll {
			elapsed := e.DurationMs
			if span, ok := p.executionInterval(e); ok {
				n := span.end.Sub(span.start).Milliseconds()
				elapsed = &n
			}
			if elapsed != nil && *elapsed >= p.opts.SlowToolMs {
				f := p.finding("long_execution", []Execution{e})
				f.Title = fmt.Sprintf("%s has %s of recorded execution time", executionLabel(e), elapsedWord(*elapsed))
				f.Detail = "Measured from recorded command timing. There is no comparable-run baseline yet, so this is a duration threshold, not a claim of abnormal latency."
				f.Review = "Review the test/build scope, environment startup and build cache."
				p.out.Findings = append(p.out.Findings, f)
			}
		}
		if e.OutputBytes == nil || (*e.OutputBytes < p.opts.LargeOutputBytes && (e.Truncated == nil || !*e.Truncated)) {
			continue
		}
		f := p.finding("large_output", []Execution{e})
		f.Title = fmt.Sprintf("%s returned %d bytes of output", executionLabel(e), *e.OutputBytes)
		f.Detail = "Output bytes are measured; their exact contribution to billed input tokens is not measured."
		if delta, ok := p.nextContextDelta(e); ok {
			f.Detail += fmt.Sprintf(" The next linked model call's recorded context changed by %+d tokens.", delta)
		}
		if e.Truncated != nil && *e.Truncated {
			f.Detail += " The harness marked this output truncated."
		}
		f.Review = "Review log limits, targeted file ranges and output filtering."
		p.out.Findings = append(p.out.Findings, f)
	}
}

func (p *projection) nextContextDelta(e Execution) (int64, bool) {
	if e.CallID == "" {
		return 0, false
	}
	for i, c := range p.in.Calls {
		if c.ID != e.CallID || i+1 >= len(p.in.Calls) {
			continue
		}
		next := p.in.Calls[i+1]
		if c.SessionID != next.SessionID || p.callPrompt[c.ID] != p.callPrompt[next.ID] || c.ContextAfter == 0 || next.ContextAfter == 0 {
			return 0, false
		}
		if e.EndedAt == "" || parse(next.EndedAt).Before(parse(e.EndedAt)) {
			return 0, false
		}
		return next.ContextAfter - c.ContextAfter, true
	}
	return 0, false
}

func (p *projection) detectContext() {
	for _, fact := range p.in.Facts {
		if fact.Kind != "context" {
			continue
		}
		phase := strings.ToLower(fact.Phase + " " + fact.Text)
		if !strings.Contains(phase, "compact") && !strings.Contains(phase, "reset") {
			continue
		}
		f := Finding{ID: "context#" + fact.ID, Kind: "context_reset", Title: "Harness recorded a context reset or compaction",
			Detail:   "A native context marker was observed. Repeated input is measured separately; compaction alone does not establish wasted tokens.",
			Severity: "info", Confidence: "recorded", StartedAt: stamp(fact.OccurredAt), Count: 1,
			SegmentIDs: []string{}, CallIDs: []string{}, ExecutionIDs: []string{},
			Evidence: []Evidence{{ID: fact.ID, Kind: "context", Label: fact.Phase, At: stamp(fact.OccurredAt), SourceRef: Ref{fact.SourcePath, fact.SourceOffset}}},
			Review:   "Review instruction size, checkpoints and how the task is split.", Rule: Version + "/context"}
		p.out.Findings = append(p.out.Findings, f)
	}
}

func sortedGroupKeys(groups map[string][]Execution) []string {
	out := make([]string, 0, len(groups))
	for k := range groups {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func elapsedWord(ms int64) string {
	if ms >= 60_000 {
		return fmt.Sprintf("%.1f min", float64(ms)/60_000)
	}
	return fmt.Sprintf("%.1f s", float64(ms)/1000)
}
