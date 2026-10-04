package diagnostics

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

type Input struct {
	Calls          []Call
	Actions        []Action
	Facts          []telemetry.Fact
	Decisions      []Decision
	Profile        map[string]any
	LastIngestedAt string
	RecordingError string
}

type projection struct {
	in            Input
	opts          Options
	out           Performance
	call          map[string]Call
	callPrompt    map[string]string
	callSegment   map[string]int
	promptIndex   map[string]int
	promptEnded   map[string]bool
	responseCalls map[string]string
	actionCalls   map[string]string
	capabilities  map[string]bool
	missing       map[string]bool
	progress      []telemetry.Fact
	latestPrompt  map[string]string
	// parents are the wrappers a native execution names, or was linked to,
	// as its parent: what ran inside them is recorded by their children.
	parents         map[string]bool
	inferredParents int
	// callExecutions and callSkills are what each model call ran and loaded,
	// as the work observations saw it.
	callExecutions map[string]int
	callSkills     map[string][]string
}

// Project is a deterministic, read-only projection. Native usage records are
// used to link facts, never summed: every billed bucket comes from Calls.
func Project(in Input, opts Options) Performance {
	opts = opts.defaults()
	p := projection{in: in, opts: opts, call: map[string]Call{}, callPrompt: map[string]string{},
		callSegment: map[string]int{}, promptIndex: map[string]int{}, promptEnded: map[string]bool{},
		responseCalls: map[string]string{}, actionCalls: map[string]string{},
		capabilities: map[string]bool{}, missing: map[string]bool{}}
	p.latestPrompt = map[string]string{}
	p.parents, p.callExecutions, p.callSkills = map[string]bool{}, map[string]int{}, map[string][]string{}
	p.out = Performance{Version: Version, GeneratedAt: stamp(opts.Now),
		PromptTurns: []Prompt{}, Segments: []Segment{}, Findings: []Finding{},
		Executions: []Execution{}, Processes: []Process{}, TopSegmentIDs: []string{}, TopFindingIDs: []string{},
		ObservedInputs: []ContextInput{}, Progress: []Evidence{}, Skills: []SkillUse{},
		TopOutputSegmentIDs: []string{}, TopOutputCallIDs: []string{},
		RecentWindowMs: 300_000, Profile: in.Profile}
	if in.LastIngestedAt != "" {
		p.out.Freshness.LastIngestedAt = in.LastIngestedAt
		p.out.Freshness.LastObservedAt = in.LastIngestedAt
		p.capabilities["recording heartbeat"] = true
	}
	if in.RecordingError != "" {
		p.missing[in.RecordingError] = true
	}
	// Stable sort a private copy: callers may reuse their ledger snapshot.
	p.in.Calls = append([]Call(nil), in.Calls...)
	sort.SliceStable(p.in.Calls, func(i, j int) bool {
		a, b := p.in.Calls[i], p.in.Calls[j]
		if at, bt := parse(a.StartedAt), parse(b.StartedAt); !at.Equal(bt) {
			return at.Before(bt)
		}
		if a.Ordinal != b.Ordinal {
			return a.Ordinal < b.Ordinal
		}
		return a.ID < b.ID
	})
	p.readFacts()
	p.makePrompts()
	p.makeExecutions()
	p.makeSkills()
	p.makeSegments()
	p.makeOverviews()
	p.detect()
	p.finish()
	return p.out
}

func key(session, id string) string        { return session + "#" + id }
func promptKey(session, ref string) string { return session + "#prompt#" + ref }
func (p *projection) ensurePrompt(session, ref, fallback string) *Prompt {
	id := promptKey(session, ref)
	if ref == "" {
		id = "unknown#" + fallback
	}
	idx, ok := p.promptIndex[id]
	if !ok {
		idx = len(p.out.PromptTurns)
		p.promptIndex[id] = idx
		coverage := "prompt text and timing unavailable"
		if ref == "" {
			coverage = "prompt identity unavailable; this call is isolated"
		}
		p.out.PromptTurns = append(p.out.PromptTurns, Prompt{ID: id, SessionID: session,
			Prompt: "Prompt not recorded", SegmentIDs: []string{}, Coverage: coverage})
	}
	return &p.out.PromptTurns[idx]
}

func (p *projection) readFacts() {
	byOffset := map[string]string{}
	for _, c := range p.in.Calls {
		p.call[c.ID] = c
		byOffset[key(c.SessionID, fmt.Sprintf("%s:%d", c.Ref.Path, c.Ref.Offset))] = c.ID
		// Claude's message ID and future native response-ledger IDs are exact
		// source identities. Never correlate using nearby timestamps.
		if _, ref, ok := strings.Cut(c.ID, "#turn#"); ok {
			p.responseCalls[key(c.SessionID, ref)] = c.ID
		}
	}
	for _, a := range p.in.Actions {
		p.actionCalls[a.ID] = a.CallID
	}
	for _, f := range p.in.Facts {
		if f.Model != "" {
			p.out.Runtime.Model = f.Model
		}
		if f.Effort != "" {
			p.out.Runtime.Effort = f.Effort
		}
		if f.HarnessVersion != "" {
			p.out.Runtime.HarnessVersion = f.HarnessVersion
		}
		if !f.ObservedAt.IsZero() {
			p.out.Freshness.LastObservedAt = later(p.out.Freshness.LastObservedAt, stamp(f.ObservedAt))
		}
		for _, gap := range f.Gaps {
			p.missing[gapText(gap)] = true
		}
		switch f.Kind {
		case "response":
			p.capabilities["response identity"] = true
			var id string
			if f.LedgerRefOffset != nil {
				id = byOffset[key(f.SessionID, fmt.Sprintf("%s:%d", f.SourcePath, *f.LedgerRefOffset))]
			}
			if id == "" {
				id = p.responseCalls[key(f.SessionID, f.ResponseID)]
			}
			if id != "" {
				p.responseCalls[key(f.SessionID, f.ResponseID)] = id
			}
		case "progress":
			p.progress = append(p.progress, f)
			p.capabilities["file changes"] = true
			paths := []string{}
			for _, change := range f.Changes {
				paths = append(paths, normalizeTarget(change.Path, p.opts.Worktree))
			}
			p.out.Progress = append(p.out.Progress, Evidence{ID: f.ID, Kind: "file_change", Label: strings.Join(paths, ", "), At: stamp(f.OccurredAt), SourceRef: Ref{f.SourcePath, f.SourceOffset}})
		case "context":
			if f.Phase == "instruction_input" && f.Output != nil {
				path := ""
				if len(f.Targets) > 0 {
					path = f.Targets[0].Path
				}
				p.out.ObservedInputs = append(p.out.ObservedInputs, ContextInput{ID: f.ID, Kind: f.Text, Path: path, Bytes: f.Output.Bytes, SHA256: f.Output.SHA256, At: stamp(f.OccurredAt), SourceRef: Ref{f.SourcePath, f.SourceOffset}})
			}
		case "execution":
			p.capabilities["native execution status"] = true
		case "prompt":
			p.capabilities["prompt identity"] = true
		case "health":
			p.capabilities["recording heartbeat"] = true
			p.out.Freshness.LastIngestedAt = later(p.out.Freshness.LastIngestedAt, stamp(f.ObservedAt))
		}
	}
	// A response observation can precede its explicitly correlated ledger
	// observation. Assess coverage only after all aliases are available.
	for _, f := range p.in.Facts {
		if f.Kind == "response" && p.responseCalls[key(f.SessionID, f.ResponseID)] == "" {
			p.missing["Native response usage has no confirmed ledger match; it is not added to totals."] = true
		}
		if f.Kind != "response" || f.HarnessTurnRef == "" {
			continue
		}
		id := p.responseCalls[key(f.SessionID, f.ResponseID)]
		if id == "" {
			continue
		}
		for i := range p.in.Calls {
			c := &p.in.Calls[i]
			if c.ID != id {
				continue
			}
			if c.HarnessTurnRef == "" {
				c.HarnessTurnRef = f.HarnessTurnRef
				p.call[c.ID] = *c
			}
			if c.HarnessTurnRef != f.HarnessTurnRef {
				p.missing["A native response and ledger call disagree about their prompt identity."] = true
			}
			break
		}
	}
}

func (p *projection) makePrompts() {
	for _, f := range p.in.Facts {
		if f.Kind != "prompt" && f.Kind != "turn_started" && f.Kind != "turn_completed" {
			continue
		}
		if f.HarnessTurnRef == "" {
			p.missing["A prompt event has no harness turn identity."] = true
			continue
		}
		g := p.ensurePrompt(f.SessionID, f.HarnessTurnRef, f.ID)
		at := stamp(f.OccurredAt)
		switch f.Kind {
		case "prompt":
			g.Prompt, g.PromptAt, g.SourceRef = f.Text, at, Ref{f.SourcePath, f.SourceOffset}
			g.StartedAt = earlier(g.StartedAt, at)
			g.Coverage = "native prompt"
		case "turn_started":
			g.StartedAt = earlier(g.StartedAt, at)
		case "turn_completed":
			g.EndedAt, g.Outcome = at, f.Status
			p.promptEnded[g.ID] = true
		}
	}
	seen := map[string]bool{}
	for _, c := range p.in.Calls {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		g := p.ensurePrompt(c.SessionID, c.HarnessTurnRef, c.ID)
		p.callPrompt[c.ID] = g.ID
		g.ModelCalls++
		g.Tokens.Add(c.Tokens)
		if g.StartedAt == "" {
			g.StartedAt = c.StartedAt
		}
		if !p.promptEnded[g.ID] {
			g.EndedAt = later(g.EndedAt, c.EndedAt)
		}
		p.out.Tokens.Add(c.Tokens)
		p.out.ModelCalls++
		p.out.Freshness.LastUsageAt = later(p.out.Freshness.LastUsageAt, c.EndedAt)
		at := parse(c.EndedAt)
		if !at.IsZero() && !at.Before(p.opts.Now.Add(-time.Duration(p.out.RecentWindowMs)*time.Millisecond)) && !at.After(p.opts.Now) {
			p.out.RecentTokens.Add(c.Tokens)
		}
	}
	for i := range p.out.PromptTurns {
		g := p.out.PromptTurns[i]
		if g.PromptAt == "" {
			continue
		}
		prior := p.latestPrompt[g.SessionID]
		if prior == "" || parse(g.PromptAt).After(parse(p.out.PromptTurns[p.promptIndex[prior]].PromptAt)) {
			p.latestPrompt[g.SessionID] = g.ID
		}
	}
	for i := range p.out.PromptTurns {
		g := &p.out.PromptTurns[i]
		if g.PromptAt != "" {
			end := g.EndedAt
			if p.promptLive(g.ID) {
				end = stamp(p.opts.Now)
			}
			g.ElapsedMs = duration(g.PromptAt, end)
		}
	}
}

func (p *projection) promptLive(id string) bool {
	idx, ok := p.promptIndex[id]
	return ok && !p.opts.Closed && !p.promptEnded[id] && p.latestPrompt[p.out.PromptTurns[idx].SessionID] == id
}

func (p *projection) makeSegments() {
	byCall := map[string][]Execution{}
	for _, e := range p.out.Executions {
		if e.CallID != "" {
			byCall[e.CallID] = append(byCall[e.CallID], e)
		}
	}
	seen := map[string]bool{}
	for _, c := range p.in.Calls {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		kind, target, label := classifyCallWith(byCall[c.ID], p.opts.Worktree, p.parents)
		pid := p.callPrompt[c.ID]
		idx := len(p.out.Segments) - 1
		if idx < 0 || p.out.Segments[idx].PromptID != pid || p.out.Segments[idx].Kind != kind || p.out.Segments[idx].Target != target {
			idx++
			p.out.Segments = append(p.out.Segments, Segment{ID: "segment#" + c.ID, PromptID: pid, Kind: kind, Target: target, Label: label,
				CallIDs: []string{}, ExecutionIDs: []string{}, Rule: Version + "/activity", Outcome: "unknown"})
			g := &p.out.PromptTurns[p.promptIndex[pid]]
			g.SegmentIDs = append(g.SegmentIDs, p.out.Segments[idx].ID)
		}
		s := &p.out.Segments[idx]
		p.callSegment[c.ID] = idx
		s.CallIDs = append(s.CallIDs, c.ID)
		s.ModelCalls++
		s.Tokens.Add(c.Tokens)
		start := c.StartedAt
		g := p.out.PromptTurns[p.promptIndex[pid]]
		if g.PromptAt != "" && start < g.PromptAt {
			start = g.PromptAt
		}
		s.StartedAt, s.EndedAt = earlier(s.StartedAt, start), later(s.EndedAt, c.EndedAt)
		for _, e := range byCall[c.ID] {
			s.ExecutionIDs = append(s.ExecutionIDs, e.ID)
			if e.Status == "failed" || (e.ExitCode != nil && *e.ExitCode != 0) {
				s.Outcome = "failed"
			} else if s.Outcome != "failed" && e.Status == "running" {
				s.Outcome = "running"
			}
		}
	}
	for i := range p.out.Segments {
		s := &p.out.Segments[i]
		s.ElapsedMs = duration(s.StartedAt, s.EndedAt)
		spans := []interval{}
		var sum int64
		known := false
		for _, id := range s.ExecutionIDs {
			for _, e := range p.out.Executions {
				if e.ID == id {
					if iv, ok := p.executionInterval(e); ok {
						spans = append(spans, iv)
						sum += iv.end.Sub(iv.start).Milliseconds()
						known = true
					}
					break
				}
			}
		}
		if known {
			n := unionMs(spans)
			s.ToolElapsedMs, s.InvocationMs = &n, &sum
		}
	}
	// Only an explicitly open prompt qualifies as current. A historical last
	// call is not evidence that the Crew is still doing that work.
	if !p.opts.Closed && len(p.out.Segments) > 0 {
		s := &p.out.Segments[len(p.out.Segments)-1]
		g := p.out.PromptTurns[p.promptIndex[s.PromptID]]
		if g.PromptAt != "" && p.promptLive(g.ID) {
			p.out.CurrentSegmentID = s.ID
		}
	}
	// Native executions can be visible before the model response supplies
	// usage. Show their live activity without manufacturing a charged call.
	for _, e := range p.out.Executions {
		if e.IsWrapper || e.Status != "running" || !p.promptLive(e.PromptID) || e.CallID != "" {
			continue
		}
		kind, target, label := classifyCall([]Execution{e}, p.opts.Worktree)
		elapsed := duration(e.StartedAt, stamp(p.opts.Now))
		s := Segment{ID: "segment#" + e.ID, PromptID: e.PromptID, Kind: kind, Label: label, Target: target, StartedAt: e.StartedAt,
			ElapsedMs: elapsed, ToolElapsedMs: elapsed, InvocationMs: elapsed, CallIDs: []string{}, ExecutionIDs: []string{e.ID}, Outcome: "running", Rule: Version + "/native-running"}
		p.out.Segments = append(p.out.Segments, s)
		g := &p.out.PromptTurns[p.promptIndex[e.PromptID]]
		g.SegmentIDs = append(g.SegmentIDs, s.ID)
		p.out.CurrentSegmentID = s.ID
	}
}

func (p *projection) finish() {
	if !p.capabilities["native execution status"] {
		p.missing["Native execution status is unavailable; wrapper success does not establish command success."] = true
	}
	if !p.capabilities["prompt identity"] {
		p.missing["Prompt text and native prompt timing are unavailable."] = true
	}
	if !p.capabilities["recording heartbeat"] {
		p.missing["Observer heartbeat is unavailable; last observed source data is not a live heartbeat."] = true
	}
	if p.in.Profile == nil {
		p.missing["Launch profile was not recorded; current configuration cannot reconstruct it."] = true
	}
	if p.out.Freshness.LastObservedAt != "" {
		p.out.Freshness.AgeMs = duration(p.out.Freshness.LastObservedAt, stamp(p.opts.Now))
		if p.out.Freshness.AgeMs != nil {
			p.out.Freshness.Stale = *p.out.Freshness.AgeMs > p.opts.StaleAfter.Milliseconds() && !p.opts.Closed
		}
	}
	p.out.Freshness.Capabilities = sortedKeys(p.capabilities)
	p.out.Freshness.Missing = sortedKeys(p.missing)
	p.summarizeTime()
	ranked := append([]Segment(nil), p.out.Segments...)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Tokens.Total > ranked[j].Tokens.Total })
	for i, s := range ranked {
		if i == 5 {
			break
		}
		p.out.TopSegmentIDs = append(p.out.TopSegmentIDs, s.ID)
	}
	// What the model itself produced, per segment and per call. Thinking is
	// inside output for billing, so the sum ranks the step, never the ledger.
	produced := func(t Tokens) int64 { return t.Output + t.Thinking }
	segments := []rank{}
	for _, s := range p.out.Segments {
		segments = append(segments, rank{s.ID, produced(s.Tokens)})
	}
	p.out.TopOutputSegmentIDs = topIDs(segments, 5)
	calls, seenCalls := []rank{}, map[string]bool{}
	for _, c := range p.in.Calls {
		if !seenCalls[c.ID] {
			seenCalls[c.ID] = true
			calls = append(calls, rank{c.ID, produced(c.Tokens)})
		}
	}
	p.out.TopOutputCallIDs = topIDs(calls, 5)
	sort.SliceStable(p.out.Findings, func(i, j int) bool {
		a, b := p.out.Findings[i], p.out.Findings[j]
		if a.Severity != b.Severity {
			return a.Severity == "warning"
		}
		var at, bt int64
		if a.Tokens != nil {
			at = a.Tokens.Total
		}
		if b.Tokens != nil {
			bt = b.Tokens.Total
		}
		if at != bt {
			return at > bt
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.ID < b.ID
	})
	for i, f := range p.out.Findings {
		if i == 3 {
			break
		}
		p.out.TopFindingIDs = append(p.out.TopFindingIDs, f.ID)
	}
	sort.SliceStable(p.out.PromptTurns, func(i, j int) bool {
		return parse(p.out.PromptTurns[i].StartedAt).Before(parse(p.out.PromptTurns[j].StartedAt))
	})
	p.summarizeLoops()
}

type rank struct {
	id  string
	key int64
}

// topIDs keeps the n highest keys; ties fall back to the id so equal usage
// still ranks the same way on every projection.
func topIDs(items []rank, n int) []string {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].key != items[j].key {
			return items[i].key > items[j].key
		}
		return items[i].id < items[j].id
	})
	out := []string{}
	for i, item := range items {
		if i == n {
			break
		}
		out = append(out, item.id)
	}
	return out
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func parse(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func duration(start, end string) *int64 {
	a, b := parse(start), parse(end)
	if a.IsZero() || b.IsZero() || b.Before(a) {
		return nil
	}
	n := b.Sub(a).Milliseconds()
	return &n
}
func earlier(a, b string) string {
	if a == "" || (b != "" && parse(b).Before(parse(a))) {
		return b
	}
	return a
}
func later(a, b string) string {
	if b != "" && (a == "" || parse(b).After(parse(a))) {
		return b
	}
	return a
}
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func gapText(code string) string {
	if text := map[string]string{
		"child_usage_unavailable":             "Child-agent usage is unavailable from this transcript.",
		"wrapper_parent_unavailable":          "Native commands do not carry confirmed parent-wrapper identities.",
		"native_execution_unavailable":        "The adapter cannot observe native command executions for this harness.",
		"native_execution_not_observed":       "No native execution records were observed in this source.",
		"native_process_unavailable":          "Native process identities are unavailable for this harness.",
		"native_timing_unavailable":           "Native execution timing is unavailable for this harness.",
		"instruction_token_count_unavailable": "Observed instruction inputs have measured bytes; their token count is unknown.",
	}[code]; text != "" {
		return text
	}
	return code
}
