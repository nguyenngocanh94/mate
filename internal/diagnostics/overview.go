package diagnostics

import (
	"fmt"
	"sort"
	"strings"
)

const overviewRule = "prompt-work-v1/observed-operations"

// EmptyOverview is also used by message-only prompts whose harness activity
// has not reached the ledger yet.
func EmptyOverview() Overview {
	return Overview{Summary: "No activity recorded yet", Categories: []WorkCategory{}, Sequence: []WorkStep{}, Rule: overviewRule,
		Coverage: "No linked activity has been recorded for this prompt."}
}

var workLabels = map[string]string{
	"research": "Research / inspect", "write_code": "Write code / files", "edit_code": "Edit code / files",
	"review": "Review", "test": "Run tests / build", "coordination": "Coordinate", "wait": "Wait / poll",
	"instructions": "Read instructions", "mixed": "Mixed activity", "unknown": "Unclassified activity", "response": "Respond",
}

type workObservation struct {
	promptID, callID, executionID, at string
	kinds                             []string
	evidence                          Evidence
	isWrapper                         bool
	span                              *interval
}

type categoryBuilder struct {
	category                              WorkCategory
	calls, segments, executions, evidence map[string]bool
	spans                                 []interval
}

func newCategory(kind string) *categoryBuilder {
	return &categoryBuilder{category: WorkCategory{Kind: kind, Label: workLabels[kind], CallIDs: []string{}, SegmentIDs: []string{}, ExecutionIDs: []string{}, Evidence: []Evidence{}},
		calls: map[string]bool{}, segments: map[string]bool{}, executions: map[string]bool{}, evidence: map[string]bool{}}
}

func (p *projection) makeOverviews() {
	observations := p.workObservations()
	byPrompt := map[string][]workObservation{}
	byCall := map[string]map[string]bool{}
	for _, o := range observations {
		byPrompt[o.promptID] = append(byPrompt[o.promptID], o)
		if o.callID != "" {
			if byCall[o.callID] == nil {
				byCall[o.callID] = map[string]bool{}
			}
			for _, kind := range o.kinds {
				byCall[o.callID][kind] = true
			}
		}
	}
	// A response with visible assistant text can establish communication,
	// while a call with no operation or message evidence remains unknown.
	responseEvidence := map[string]Evidence{}
	for _, fact := range p.in.Facts {
		if fact.Kind != "response" || fact.Text == "" {
			continue
		}
		id := p.responseCalls[key(fact.SessionID, fact.ResponseID)]
		if id != "" {
			responseEvidence[id] = Evidence{ID: fact.ID, Kind: "response", Label: "Assistant response", At: stamp(fact.OccurredAt), SourceRef: Ref{fact.SourcePath, fact.SourceOffset}}
		}
	}
	for _, c := range p.in.Calls {
		if len(byCall[c.ID]) > 0 {
			continue
		}
		kind := "unknown"
		evidence := Evidence{ID: c.ID, Kind: "model_call", Label: "Model call; operation details unavailable", At: c.StartedAt, SourceRef: c.Ref}
		if response, ok := responseEvidence[c.ID]; ok {
			kind, evidence = "response", response
		}
		byCall[c.ID] = map[string]bool{kind: true}
		pid := p.callPrompt[c.ID]
		byPrompt[pid] = append(byPrompt[pid], workObservation{promptID: pid, callID: c.ID, at: c.StartedAt, kinds: []string{kind}, evidence: evidence})
	}
	for i := range p.out.PromptTurns {
		g := &p.out.PromptTurns[i]
		g.Overview = p.promptOverview(g.ID, byPrompt[g.ID], byCall)
	}
	// The Crew overview is the same categories over every known prompt, so the
	// two scopes cannot disagree. Prompts outside promptIndex have no
	// observations here, exactly as workObservations skips them.
	all := []workObservation{}
	for _, g := range p.out.PromptTurns {
		all = append(all, byPrompt[g.ID]...)
	}
	p.out.Overview = p.crewOverview(all, byCall)
}

func (p *projection) promptOverview(promptID string, observations []workObservation, callKinds map[string]map[string]bool) Overview {
	return p.buildOverview(observations, callKinds, func(id string) bool { return p.callPrompt[id] == promptID })
}

func (p *projection) crewOverview(observations []workObservation, callKinds map[string]map[string]bool) Overview {
	prompts := fmt.Sprintf(" across %d prompts.", len(p.out.PromptTurns))
	if len(observations) == 0 {
		out := EmptyOverview()
		out.Coverage = "No linked activity has been recorded" + prompts
		return out
	}
	out := p.buildOverview(observations, callKinds, func(string) bool { return true })
	// The per-prompt sequences already carry order. Splicing every prompt into
	// one Crew sequence would invent an order between unrelated prompts.
	out.Sequence = []WorkStep{}
	out.Coverage = strings.TrimSuffix(out.Coverage, ".") + prompts
	return out
}

func (p *projection) workObservations() []workObservation {
	out := []workObservation{}
	// Suppress a wrapper as a duplicate operation only when the child names
	// its parent explicitly. Equal commands or close timestamps do not link it.
	parents := map[string]bool{}
	for _, e := range p.out.Executions {
		if !e.IsWrapper && e.WrapperID != "" && e.WrapperID != e.ID {
			parents[e.WrapperID] = true
		}
	}
	for _, e := range p.out.Executions {
		if _, known := p.promptIndex[e.PromptID]; !known {
			continue
		}
		if e.IsWrapper && parents[e.ID] {
			continue
		}
		kinds := executionWork(e)
		evidence := Evidence{ID: e.ID, Kind: "execution", Label: executionLabel(e), At: e.StartedAt, SourceRef: e.SourceRef,
			Tool: e.Tool, Command: executionCommand(e), Status: e.Status, ExitCode: e.ExitCode, OutputExcerpt: short(e.OutputExcerpt, 500)}
		if e.ExitCode != nil {
			evidence.Label += fmt.Sprintf(" · exit %d", *e.ExitCode)
		}
		o := workObservation{promptID: e.PromptID, callID: e.CallID, executionID: e.ID, at: e.StartedAt, kinds: kinds, evidence: evidence, isWrapper: e.IsWrapper}
		if span, ok := p.executionInterval(e); ok && !e.IsWrapper {
			o.span = &span
		}
		out = append(out, o)
	}
	// FileChange start/completion observations share an ID. The latest
	// snapshot supplies the changed paths without becoming a second change.
	latest := map[string]int{}
	for i, f := range p.in.Facts {
		if f.Kind != "progress" || len(f.Changes) == 0 || f.HarnessTurnRef == "" {
			continue
		}
		id := key(f.SessionID, f.ID)
		if prev, ok := latest[id]; !ok || p.in.Facts[prev].SourceOffset <= f.SourceOffset {
			latest[id] = i
		}
	}
	for _, i := range latest {
		f := p.in.Facts[i]
		pid := promptKey(f.SessionID, f.HarnessTurnRef)
		if _, known := p.promptIndex[pid]; !known {
			continue
		}
		kinds, paths := []string{}, []string{}
		for _, change := range f.Changes {
			kind := "edit_code"
			switch strings.ToLower(change.Kind) {
			case "add", "added", "create", "created":
				kind = "write_code"
			}
			kinds = appendKind(kinds, kind)
			paths = append(paths, normalizeTarget(change.Path, p.opts.Worktree))
		}
		out = append(out, workObservation{promptID: pid, callID: p.responseCalls[key(f.SessionID, f.ResponseID)], at: stamp(f.OccurredAt), kinds: kinds,
			evidence: Evidence{ID: key(f.SessionID, f.ID), Kind: "file_change", Label: strings.Join(paths, ", "), At: stamp(f.OccurredAt), SourceRef: Ref{f.SourcePath, f.SourceOffset}, Status: f.Status}})
	}
	seenMessages := map[string]bool{}
	for _, f := range p.in.Facts {
		if f.Kind != "message" || f.Text == "" || f.HarnessTurnRef == "" || (f.Phase != "" && f.Phase != "final_answer" && f.Phase != "final") {
			continue
		}
		pid, id := promptKey(f.SessionID, f.HarnessTurnRef), key(f.SessionID, f.ID)
		if _, known := p.promptIndex[pid]; !known || seenMessages[id] {
			continue
		}
		seenMessages[id] = true
		out = append(out, workObservation{promptID: pid, callID: p.responseCalls[key(f.SessionID, f.ResponseID)], at: stamp(f.OccurredAt), kinds: []string{"response"},
			evidence: Evidence{ID: id, Kind: "response", Label: "Assistant response", At: stamp(f.OccurredAt), SourceRef: Ref{f.SourcePath, f.SourceOffset}}})
	}
	return out
}

// buildOverview is the one classifier behind both scopes: include decides
// which ledger calls this overview charges, so a prompt and the whole Crew
// are the same categories over different call sets.
func (p *projection) buildOverview(observations []workObservation, callKinds map[string]map[string]bool, include func(callID string) bool) Overview {
	out := EmptyOverview()
	if len(observations) == 0 {
		return out
	}
	sort.SliceStable(observations, func(i, j int) bool {
		a, b := observations[i], observations[j]
		if a.at != b.at {
			if a.at == "" {
				return false
			}
			if b.at == "" {
				return true
			}
			return parse(a.at).Before(parse(b.at))
		}
		if a.evidence.SourceRef.Path == b.evidence.SourceRef.Path && a.evidence.SourceRef.Offset != b.evidence.SourceRef.Offset {
			return a.evidence.SourceRef.Offset < b.evidence.SourceRef.Offset
		}
		return a.evidence.ID < b.evidence.ID
	})
	categories := map[string]*categoryBuilder{}
	order := []string{}
	category := func(kind string) *categoryBuilder {
		if categories[kind] == nil {
			categories[kind] = newCategory(kind)
			order = append(order, kind)
		}
		return categories[kind]
	}
	unlinked := 0
	for _, o := range observations {
		if o.callID == "" && (o.executionID != "" || o.evidence.Kind == "file_change") {
			unlinked++
		}
		segments := p.observationSegments(o)
		for _, kind := range o.kinds {
			c := category(kind)
			if !c.evidence[o.evidence.ID] {
				c.category.Evidence = append(c.category.Evidence, o.evidence)
				c.evidence[o.evidence.ID] = true
			}
			if o.executionID != "" && !c.executions[o.executionID] {
				c.category.ExecutionIDs = append(c.category.ExecutionIDs, o.executionID)
				c.executions[o.executionID] = true
				if !o.isWrapper {
					c.category.ExecutionCount++
				}
			}
			for _, id := range segments {
				if !c.segments[id] {
					c.category.SegmentIDs = append(c.category.SegmentIDs, id)
					c.segments[id] = true
				}
			}
			// The timing of a mixed operation cannot be split across kinds.
			if o.span != nil && len(uniqueKinds(o.kinds)) == 1 {
				c.spans = append(c.spans, *o.span)
			}
		}
		// One wrapper/FileChange observation can contain several operation
		// kinds with no internal timing. Do not manufacture a serial order.
		kind := "mixed"
		if len(uniqueKinds(o.kinds)) == 1 {
			kind = o.kinds[0]
		}
		n := len(out.Sequence) - 1
		if n < 0 || out.Sequence[n].Kind != kind {
			out.Sequence = append(out.Sequence, WorkStep{Kind: kind, Label: workLabels[kind], StartedAt: o.at, SegmentIDs: []string{}, ExecutionIDs: []string{}})
			n++
		}
		step := &out.Sequence[n]
		step.SegmentIDs = appendUnique(step.SegmentIDs, segments...)
		if o.executionID != "" {
			step.ExecutionIDs = appendUnique(step.ExecutionIDs, o.executionID)
		}
	}
	seenCalls := map[string]bool{}
	for _, call := range p.in.Calls {
		if !include(call.ID) || seenCalls[call.ID] {
			continue
		}
		seenCalls[call.ID] = true
		kinds := callKinds[call.ID]
		kind := "unknown"
		if len(kinds) == 1 {
			kind = sortedKeys(kinds)[0]
		} else if len(kinds) > 1 {
			kind = "mixed"
		}
		c := category(kind)
		c.category.ModelCalls++
		c.category.CallIDs = append(c.category.CallIDs, call.ID)
		if c.category.Tokens == nil {
			c.category.Tokens = &Tokens{}
		}
		c.category.Tokens.Add(call.Tokens)
		if idx, ok := p.callSegment[call.ID]; ok {
			id := p.out.Segments[idx].ID
			if !c.segments[id] {
				c.category.SegmentIDs = append(c.category.SegmentIDs, id)
				c.segments[id] = true
			}
		}
		if kind == "mixed" {
			c.category.Evidence = append(c.category.Evidence, Evidence{ID: call.ID, Kind: "model_call", Label: "Model call with multiple recorded work types; its tokens are counted once here", At: call.StartedAt, SourceRef: call.Ref})
		}
	}
	for _, kind := range order {
		c := categories[kind]
		if len(c.spans) > 0 {
			ms := unionMs(c.spans)
			c.category.ElapsedMs = &ms
		}
		out.Categories = append(out.Categories, c.category)
	}
	parts := []string{}
	for i, c := range out.Categories {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("+%d other work types", len(out.Categories)-i))
			break
		}
		label := c.Label
		if c.ModelCalls > 0 {
			label += fmt.Sprintf(" (%d calls)", c.ModelCalls)
		} else if c.ExecutionCount > 0 {
			label += fmt.Sprintf(" (%d executions)", c.ExecutionCount)
		}
		parts = append(parts, label)
	}
	out.Summary = strings.Join(parts, " · ")
	out.Coverage = "Types inferred from recorded operations, not prompt wording or successful completion. Each call's tokens belong to one category; execution evidence and time can overlap."
	if unlinked > 0 {
		out.Coverage += fmt.Sprintf(" %d operation observation(s) lack call attribution.", unlinked)
	}
	return out
}

func (p *projection) observationSegments(o workObservation) []string {
	if idx, ok := p.callSegment[o.callID]; ok {
		return []string{p.out.Segments[idx].ID}
	}
	out := []string{}
	if o.executionID != "" {
		for _, s := range p.out.Segments {
			for _, id := range s.ExecutionIDs {
				if id == o.executionID {
					out = append(out, s.ID)
					break
				}
			}
		}
	}
	return out
}

func appendUnique(values []string, more ...string) []string {
	for _, v := range more {
		found := false
		for _, old := range values {
			if old == v {
				found = true
				break
			}
		}
		if !found {
			values = append(values, v)
		}
	}
	return values
}
func uniqueKinds(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}
func appendKind(kinds []string, kind string) []string {
	if len(kinds) == 0 || kinds[len(kinds)-1] != kind {
		return append(kinds, kind)
	}
	return kinds
}
