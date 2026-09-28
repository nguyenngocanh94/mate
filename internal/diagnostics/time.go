package diagnostics

import (
	"sort"
	"time"
)

type interval struct{ start, end time.Time }

func unionMs(spans []interval) int64 {
	if len(spans) == 0 {
		return 0
	}
	spans = append([]interval(nil), spans...)
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	start, end := spans[0].start, spans[0].end
	var total time.Duration
	for _, s := range spans[1:] {
		if s.start.After(end) {
			total += end.Sub(start)
			start, end = s.start, s.end
		} else if s.end.After(end) {
			end = s.end
		}
	}
	return (total + end.Sub(start)).Milliseconds()
}

func (p *projection) executionInterval(e Execution) (interval, bool) {
	a, b := parse(e.StartedAt), parse(e.EndedAt)
	if a.IsZero() {
		return interval{}, false
	}
	if b.IsZero() && e.Status == "running" && p.promptLive(e.PromptID) {
		b = p.opts.Now
	}
	if b.IsZero() || b.Before(a) {
		return interval{}, false
	}
	return interval{a, b}, true
}

func (p *projection) summarizeTime() {
	prompts, tools, waits := []interval{}, []interval{}, []interval{}
	var invocation int64
	invocationKnown := false
	// Native commands and direct tools count as work. A wrapper duration is
	// the wait around nested work, not an additional invocation of the job.
	for _, e := range p.out.Executions {
		if e.IsWrapper || e.Poll {
			continue
		}
		if span, ok := p.executionInterval(e); ok {
			tools = append(tools, span)
			invocation += span.end.Sub(span.start).Milliseconds()
			invocationKnown = true
		} else if e.DurationMs != nil {
			invocation += *e.DurationMs
			invocationKnown = true
		}
	}
	for _, g := range p.out.PromptTurns {
		if g.PromptAt == "" {
			continue
		}
		end := parse(g.EndedAt)
		if p.promptLive(g.ID) {
			end = p.opts.Now
		}
		start := parse(g.PromptAt)
		if !start.IsZero() && !end.IsZero() && !end.Before(start) {
			prompts = append(prompts, interval{start, end})
		}
	}
	for _, q := range p.in.Decisions {
		start, end := parse(q.AskedAt), parse(q.AnsweredAt)
		if end.IsZero() && !p.opts.Closed {
			end = p.opts.Now
		}
		if !start.IsZero() && !end.IsZero() && !end.Before(start) {
			waits = append(waits, interval{start, end})
		}
	}
	if len(tools) > 0 {
		n := unionMs(tools)
		p.out.Time.ToolElapsedMs = &n
	}
	if invocationKnown {
		p.out.Time.InvocationMs = &invocation
	}
	if len(waits) > 0 {
		n := unionMs(waits)
		p.out.Time.DecisionWaitMs = &n
	}
	if len(prompts) > 0 {
		n := unionMs(prompts)
		p.out.Time.PromptElapsedMs = &n
		// Clip every explained lane to the measured prompt intervals before
		// subtracting. Parallel jobs and decision waits cannot double count.
		covered := []interval{}
		for _, lane := range append(tools, waits...) {
			for _, g := range prompts {
				start, end := lane.start, lane.end
				if start.Before(g.start) {
					start = g.start
				}
				if end.After(g.end) {
					end = g.end
				}
				if !end.Before(start) {
					covered = append(covered, interval{start, end})
				}
			}
		}
		unallocated := n - unionMs(covered)
		p.out.Time.UnallocatedMs = &unallocated
	}
}
