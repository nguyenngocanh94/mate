package console

import (
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The detail pane (design B, C, D). It sits under the list, never beside
// it, and shows the selected row's object: a Project's summary on the
// workspace level, the Mate, a Crew or the Completed group on a Project.
// Focused, ↑↓ walk its fields and y copies the one under the cursor.

// detailField is one labelled value. lines are the value's own lines (a
// wrapped path, a reason); copy is what y puts on the clipboard, "" for a
// field with nothing worth copying.
type detailField struct {
	label string
	lines []gline
	copy  string
}

// detailLabelWidth is the lead before a value: two blanks, then a label
// column of nine cells, or the longest label and one blank when that is
// wider ("completed" on the workspace level).
func detailLabelWidth(fields []detailField) int {
	w := 9
	for _, f := range fields {
		w = maxInt(w, cells(f.label)+1)
	}
	return 2 + w
}

// detailLines draws the detail body at the plan's width.
func (m Model) detailLines(p framePlan) []gline {
	fields := m.detailFields(p.w)
	lw := detailLabelWidth(fields)
	focused := m.focus == paneDetail
	sel := clampInt(m.detailSel, 0, len(fields)-1)
	var out []gline
	for i, f := range fields {
		on := focused && i == sel
		for j, v := range f.lines {
			lead := gl().pad(lw)
			if j == 0 {
				lead = gl().pad(2).add(padRight(f.label, lw-2), tDim)
				if on {
					lead = gl().add(m.g.Selected, tAcc).pad(1).add(padRight(f.label, lw-2), tDim)
				}
			}
			out = append(out, lead.join(v).padTo(p.w, m.g).selected(on))
		}
	}
	return out
}

// detailTitle is the detail pane's rule title and meta.
func (m Model) detailTitle() (title gline, meta string) {
	r, ok := m.selectedRow()
	if !ok {
		return gl(), ""
	}
	switch r.kind {
	case rowProject:
		if proj, ok := m.projectByID(r.id); ok {
			return gl().add(proj.Name, tFg), "project"
		}
	case rowMate:
		return kindName(m.g.Mate, m.currentProject().Name), ""
	case rowCrew:
		if c, ok := m.crewByID(r.id); ok {
			title := strings.TrimSpace(oneLine(c.Task))
			if title == "" {
				title = shortID(c.CrewID, m.g)
			}
			return kindName(m.g.Crew, title), ""
		}
	case rowCompletedGroup:
		return gl().add("Completed", tFg), ""
	}
	return gl().add(r.id, tFg), ""
}

// detailFields is the selected row's fields. Only what the snapshot
// records is shown; a field the backend does not carry is left out rather
// than drawn empty.
func (m Model) detailFields(w int) []detailField {
	r, ok := m.selectedRow()
	if !ok {
		return nil
	}
	vw := maxInt(8, w-12)
	switch r.kind {
	case rowProject:
		if proj, ok := m.projectByID(r.id); ok {
			return m.projectPeekFields(proj, vw)
		}
	case rowMate:
		return m.mateDetailFields(vw)
	case rowCrew:
		if c, ok := m.crewByID(r.id); ok {
			return m.crewDetailFields(c, vw)
		}
	case rowCompletedGroup:
		return m.completedDetailFields()
	}
	return nil
}

func one(l gline) []gline { return []gline{l} }

func text(s string, t tok) []gline { return one(gl().add(s, t)) }

// wrapped is a long value on as many lines as it takes.
func wrapped(s string, t tok, w int) []gline {
	var out []gline
	for _, part := range wrapWords(s, w) {
		out = append(out, gl().add(part, t))
	}
	if len(out) == 0 {
		out = text("", t)
	}
	return out
}

// fieldState draws an Absent or Unknown field in its own words: "none" dim,
// "unknown" amber, with the reason under it when there is one.
func fieldState[T any](f query.Field[T], w int) []gline {
	if f.State == query.Unknown {
		out := text("unknown", tAmber)
		if f.Reason != "" {
			out = append(out, wrapped(f.Reason, tDim, w)...)
		}
		return out
	}
	return text("none", tDim)
}

// ---------- a Project, from the workspace ----------

func (m Model) projectPeekFields(proj query.ProjectNode, vw int) []detailField {
	var out []detailField
	mate := proj.Mate
	word := mateWord(mate)
	mateLine := gl()
	if h := mateHarness(mate); h != "" {
		mateLine = mateLine.add(m.harnessIcon(h), tFg).pad(1)
	}
	mateLine = mateLine.add(word, statusTokFg(word))
	if since := m.since(mate.Binding.Value.BoundSince); since != "" && mate.Binding.IsKnown() && word == string(query.MateRunning) {
		mateLine = mateLine.add(" "+m.g.Dot+" "+since, tDim)
	}
	out = append(out, detailField{label: "mate", lines: one(mateLine), copy: m.mateAgent(mate)})
	if l := m.crewCountsLine(proj); len(l.segs) > 0 {
		out = append(out, detailField{label: "crews", lines: one(l)})
	}
	if closed := proj.ClosedCrews; closed > 0 {
		out = append(out, detailField{label: "completed", lines: text(fmt.Sprint(closed), tFg)})
	}
	if n := boxWaiting(proj); n > 0 {
		out = append(out, detailField{label: "box", lines: text(fmt.Sprintf("%d waiting on you", n), tAmber)})
	}
	out = append(out, detailField{label: "mode", lines: one(m.modeLine(proj))})
	out = append(out, m.repoFields(proj, vw)...)
	if f, ok := m.lastEventField(mate.LastEvent); ok {
		out = append(out, f)
	}
	return out
}

// statusTokFg is statusTok for a value column, where a plain status is the
// foreground rather than dim: detail values are what the reader came for.
func statusTokFg(word string) tok {
	if t := statusTok(word); t != tDim {
		return t
	}
	return tFg
}

// crewCountsLine counts a Project's open crews by state, in a fixed order,
// each tinted by its own state: "1 working · 1 review".
func (m Model) crewCountsLine(proj query.ProjectNode) gline {
	order := []query.CrewStatus{query.CrewSpawned, query.CrewWorking, query.CrewNeedsDecision,
		query.CrewWaitMate, query.CrewBlocked, query.CrewFailed}
	counts := map[query.CrewStatus]int{}
	for _, c := range proj.Crews {
		if !c.Closed {
			counts[c.Status]++
		}
	}
	l := gl()
	for _, st := range order {
		n := counts[st]
		if n == 0 {
			continue
		}
		if len(l.segs) > 0 {
			l = l.add(" "+m.g.Dot+" ", tDim)
		}
		l = l.add(fmt.Sprintf("%d %s", n, shortStatus(string(st))), statusTokFg(string(st)))
	}
	if len(l.segs) == 0 {
		l = l.add("none", tDim)
	}
	return l
}

// boxWaiting is how many items in a Project's box wait on somebody.
func boxWaiting(proj query.ProjectNode) int {
	if !proj.Box.IsKnown() {
		return 0
	}
	return len(proj.Box.Value.Inbox)
}

// modeLine is the Project's communication mode: manual, or auto and when
// the daemon last sent.
func (m Model) modeLine(proj query.ProjectNode) gline {
	switch proj.Mode {
	case "":
		return gl().add("unknown", tAmber)
	case query.ModeAuto:
		l := gl().add("auto", tFg)
		if proj.Daemon.Sent() && !proj.Daemon.LastSentAt.IsZero() {
			l = l.add(" "+m.g.Dot+" sent "+m.since(proj.Daemon.LastSentAt)+" ago", tDim)
		}
		return l
	}
	return gl().add(string(proj.Mode), tFg)
}

// repoFields are a Project's repos: one path per line, the default branch
// beside it when there is room.
func (m Model) repoFields(proj query.ProjectNode, vw int) []detailField {
	if proj.Repos.State != query.Known {
		return []detailField{{label: "repo", lines: fieldState(proj.Repos, vw)}}
	}
	if len(proj.Repos.Value) == 0 {
		return []detailField{{label: "repo", lines: []gline{
			gl().add("none yet", tAmber),
			gl().add("mate project repo add", tDim),
		}}}
	}
	var out []detailField
	for i, repo := range proj.Repos.Value {
		label := ""
		if i == 0 {
			label = "repo"
		}
		out = append(out, detailField{label: label, lines: wrapped(repo.Path, tFg, vw), copy: repo.Path})
	}
	return out
}

// lastEventField is the most recent recorded event and its age.
func (m Model) lastEventField(ev query.Field[query.EventValue]) (detailField, bool) {
	if !ev.IsKnown() || ev.Value.EventType == "" {
		return detailField{}, false
	}
	l := gl().add(ev.Value.EventType, tFg)
	if since := m.since(ev.Value.OccurredAt); since != "" {
		l = l.add(" "+m.g.Dot+" "+since, tDim)
	}
	return detailField{label: "last", lines: one(l)}, true
}

// ---------- the Mate ----------

func (m Model) mateDetailFields(vw int) []detailField {
	proj := m.currentProject()
	mate := proj.Mate
	var out []detailField
	if agent := m.mateAgent(mate); agent != "" {
		out = append(out, detailField{label: "agent", lines: text(agent, tFg), copy: agent})
	}
	if h := mateHarness(mate); h != "" {
		out = append(out, detailField{label: "harness", lines: one(gl().add(m.harnessIcon(h), tFg).pad(1).add(h, tFg))})
	}
	word := mateWord(mate)
	status := gl().add(word, statusTokFg(word))
	if mate.Binding.IsKnown() && word == string(query.MateRunning) {
		if since := m.since(mate.Binding.Value.BoundSince); since != "" {
			status = status.add(" "+m.g.Dot+" "+since, tDim)
		}
	}
	out = append(out, detailField{label: "status", lines: one(status)})
	if word == "no mate" {
		// Nothing runs, so nothing is bound, spent or blocked: the Project's
		// own facts, and the key that makes a Mate.
		out = append(out, m.repoFields(proj, vw)...)
		out = append(out, detailField{label: "mode", lines: one(m.modeLine(proj))})
		return append(out, detailField{label: "start", lines: text("s creates one", tDim)})
	}
	if f, ok := m.bindingField(mate.Binding, vw); ok {
		out = append(out, f)
	}
	out = append(out, m.repoFields(proj, vw)...)
	live := 0
	for _, c := range proj.Crews {
		if !c.Closed {
			live++
		}
	}
	crews := gl().add(fmt.Sprintf("%d live", live), tFg)
	if n := len(m.finishedCrews()); n > 0 {
		crews = crews.add(" "+m.g.Dot+" ", tDim).add(fmt.Sprintf("%d completed", n), tFg)
	}
	out = append(out, detailField{label: "crews", lines: one(crews)})
	if mate.Tokens.IsKnown() {
		out = append(out, detailField{label: "tokens", lines: text(tokensWord(mate.Tokens.Value.Total), tFg)})
		if n := mate.Tokens.Value.ContextTokens; n != nil {
			out = append(out, detailField{label: "context", lines: text(tokensWord(*n), tFg)})
		}
	}
	out = append(out, detailField{label: "mode", lines: one(m.modeLine(proj))})
	blocked := gl().add("no", tFg)
	if n := boxWaiting(proj); n > 0 {
		word := "crews wait"
		if n == 1 {
			word = "crew waits"
		}
		blocked = blocked.add(" "+m.g.Dot+" ", tDim).add(fmt.Sprintf("%d %s on you", n, word), tAmber)
	}
	out = append(out, detailField{label: "blocked", lines: one(blocked)})
	if f, ok := m.lastEventField(mate.LastEvent); ok {
		out = append(out, f)
	}
	if mate.Error.IsKnown() && mate.Error.Value != "" {
		out = append(out, detailField{label: "error", lines: wrapped(string(mate.Error.Value), tRed, vw)})
	}
	return out
}

// bindingField is shown only when the binding is not simply active: a
// stale, absent or unreadable binding is why Enter refuses.
func (m Model) bindingField(b query.Field[query.BindingValue], vw int) (detailField, bool) {
	switch {
	case b.State != query.Known:
		return detailField{label: "binding", lines: fieldState(b, vw)}, true
	case b.Value.Status == query.BindingActive || b.Value.Status == query.BindingReserved:
		return detailField{}, false
	}
	return detailField{label: "binding", lines: text(string(b.Value.Status), statusTokFg(string(b.Value.Status)))}, true
}

// ---------- a Crew ----------

func (m Model) crewDetailFields(c query.CrewNode, vw int) []detailField {
	var out []detailField
	out = append(out, detailField{label: "agent", lines: text(shortID(c.CrewID, m.g), tFg), copy: c.CrewID})
	if h := string(c.HarnessKind); h != "" {
		out = append(out, detailField{label: "harness", lines: one(gl().add(m.harnessIcon(h), tFg).pad(1).add(h, tFg))})
	}
	// The launch profile, as spawned; absent means the harness's own.
	if c.Model != "" {
		out = append(out, detailField{label: "model", lines: text(c.Model, tFg), copy: c.Model})
	}
	if c.Effort != "" {
		out = append(out, detailField{label: "effort", lines: text(c.Effort, tFg)})
	}
	word := crewWord(c)
	status := gl().add(word, statusTokFg(word))
	if since := m.since(c.CreatedAt); since != "" {
		status = status.add(" "+m.g.Dot+" "+since, tDim)
	}
	out = append(out, detailField{label: "status", lines: one(status)})
	if c.Attention.IsKnown() {
		t := tAmber
		if crewFailed(c) {
			t = tRed
		}
		out = append(out, detailField{label: "blocked", lines: wrapped(attentionWhy(c), t, vw)})
	}
	if f, ok := m.bindingField(c.Binding, vw); ok {
		out = append(out, f)
	}
	if f, ok := m.lastEventField(c.LastEvent); ok {
		out = append(out, f)
	}
	if c.Health.IsKnown() {
		out = append(out, detailField{label: "pane", lines: one(m.healthLine(c.Health.Value))})
	}
	out = append(out, detailField{label: "mate", lines: one(kindName(m.g.Mate, m.currentProject().Name))})
	if c.Worktree.IsKnown() && c.Worktree.Value.Branch != "" {
		out = append(out, detailField{label: "branch", lines: wrapped(c.Worktree.Value.Branch, tFg, vw), copy: c.Worktree.Value.Branch})
	}
	switch {
	case c.Worktree.State == query.Unknown:
		out = append(out, detailField{label: "worktree", lines: fieldState(c.Worktree, vw)})
	case c.Worktree.IsKnown() && c.Worktree.Value.Status == query.WorktreeRecordedRemoved:
		// Teardown already removed it: a crew with no worktree must not
		// read like one that has its checkout.
		out = append(out, detailField{label: "worktree", lines: text("missing", tRed)})
	}
	if c.Repo.IsKnown() && c.Repo.Value.Path != "" {
		out = append(out, detailField{label: "repo", lines: wrapped(c.Repo.Value.Path, tFg, vw), copy: c.Repo.Value.Path})
	}
	if c.Tokens.IsKnown() {
		out = append(out, detailField{label: "tokens", lines: text(tokensWord(c.Tokens.Value.Total), tFg)})
	}
	if c.Error.IsKnown() && c.Error.Value != "" {
		out = append(out, detailField{label: "error", lines: wrapped(string(c.Error.Value), tRed, vw)})
	}
	return out
}

// attentionWhy is the reason a Crew needs the captain, in the query
// layer's own sentence, without the crew id it repeats.
func attentionWhy(c query.CrewNode) string {
	why := c.Attention.Value.Why
	if why == "" {
		return string(c.Attention.Value.Kind)
	}
	return strings.TrimPrefix(why, "crew "+c.CrewID+" ")
}

// healthLine is what the observer last saw in the crew's pane.
func (m Model) healthLine(h query.CrewHealth) gline {
	if !h.AgentPresent {
		return gl().add("agent gone", tRed)
	}
	switch h.Composer {
	case query.ComposerBusy:
		return gl().add("busy "+age(h.ComposerFor), tFg)
	case query.ComposerUnknown:
		return gl().add("unclear", tDim)
	}
	return gl().add("idle "+age(h.QuietFor), tFg)
}

// ---------- the Completed group ----------

func (m Model) completedDetailFields() []detailField {
	finished := m.finishedCrews()
	failed := 0
	for _, c := range finished {
		if crewFailed(c) {
			failed++
		}
	}
	done := gl().add(fmt.Sprintf("%d finished", len(finished)-failed), tFg)
	out := []detailField{{label: "crews", lines: one(done)}}
	if failed > 0 {
		out = append(out, detailField{label: "failed", lines: text(fmt.Sprint(failed), tRed)})
	}
	return out
}

// detailCopy is the value y copies from the field under the cursor.
func (m Model) detailCopy() (string, bool) {
	fields := m.detailFields(m.w)
	if len(fields) == 0 {
		return "", false
	}
	f := fields[clampInt(m.detailSel, 0, len(fields)-1)]
	if f.copy != "" {
		return f.copy, true
	}
	var b strings.Builder
	for _, l := range f.lines {
		for _, s := range l.segs {
			b.WriteString(s.text)
		}
	}
	v := strings.TrimSpace(b.String())
	return v, v != ""
}
