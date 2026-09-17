package console

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The seams. Five surfaces of the redesign belong to their own tasks; this
// file is where the frame calls into them, with a foundation implementation
// behind each one so `mate console` builds and runs today. Each is
// deliberately small - replacing one should mean rewriting a function, not
// unpicking the frame.
//
//	listLines       - the list pane's columns, selection and paging
//	inspectorLines  - the inspector's body, Detail and scrolling
//	loadingLines /
//	failedLines /
//	footerMessage /
//	keyHints        - the loading, empty and error screens, and the two
//	                  footer lines
//	attach.go       - the attach announce/refuse/hand-over/return flow,
//	                  which has taken its whole surface (including
//	                  attachRefusal) into a file of its own
//
// workspaceRoot/workspaceDisplayName (frame.go) already read real fields
// off query.Snapshot.Workspace. footerMessage renders Snapshot.Warnings (see
// warningsFooterMsg); per-row derived attention (*.Attention) is the
// list-pane task's own surface.
//
// The primitives they build on - the cell grid, the palette, the glyph
// sets, the truncation helpers and the three signal renderers - are the
// foundation's own and are not expected to change underneath them.

// workspaceRoot is the path shown in the header at 100 columns and wider,
// from the read layer's own resolution of the open workspace
// (StateStore.WorkspaceRoot via query.Snapshot.Workspace - never a
// caller-supplied path, ADR 0015). Empty when that field did not come back
// Known: an unresolved root prints nothing rather than inventing one.
func (m Model) workspaceRoot() string {
	if !m.tree.Workspace.IsKnown() {
		return ""
	}
	return m.tree.Workspace.Value.Root
}

// ---------- the list pane ----------
//
// listLines itself, its per-level column layouts and its empty-state text
// live in list.go - the list pane task's own surface. What is left here is
// everything else in this file: the inspector, the loading/error screens,
// the footer and the attach flow.

// ---------- the inspector ----------

// inspectorLines is the inspector pane: the title, the selected row's full
// field set grouped by a blank line (identity - status/binding -
// repo/branch/worktree - event/reason), and the scroll window over that
// content. h is the pane's full height, including the title: the title
// scrolls with everything else rather than pinning itself to the top, which
// is what lets a long Crew block scroll all the way through in Detail
// (design/mate-console-states.html, "Detail view").
//
// No value here is ever truncated - only wrapped, after a '/' for paths and
// branches - and every field whose read can fail routes through availSpans
// or one of the *Spans helpers below, so Known/Absent/Unknown are never
// confused and an Unknown field always says so with its reason.
func (m Model) inspectorLines(w, valueWidth, h int, focused bool) []*line {
	if valueWidth < 8 {
		valueWidth = 8
	}
	content := m.inspectorContent(valueWidth, focused)
	return windowContent(content, m.inspTop, h, m.g, m.p)
}

// inspectorContent builds the full, unwindowed field set for the selected
// row: the title line, a blank, then the row kind's own block. Nothing here
// knows about scrolling - that is inspectorLines' own job, over this slice.
func (m Model) inspectorContent(valueWidth int, focused bool) []*line {
	r, ok := m.selectedRow()
	if !ok {
		return []*line{
			newLine().pad(1).addSpan(paneTitleSpan(strings.ToUpper(m.cur().kind.String()), focused, m.p)),
			newLine(),
			newLine().pad(1).add("nothing selected", m.p.Dim),
		}
	}
	out := []*line{
		newLine().pad(1).addSpan(paneTitleSpan(m.inspectorTitle(r), focused, m.p)),
		newLine(),
	}
	switch r.kind {
	case rowProject:
		out = append(out, m.projectFields(r, valueWidth)...)
	case rowMate:
		out = append(out, m.mateFields(valueWidth)...)
	case rowCrew:
		out = append(out, m.crewFields(r, valueWidth)...)
	case rowCompletedGroup:
		out = append(out, m.completedGroupFields(valueWidth)...)
	}
	return out
}

func (m Model) inspectorTitle(r row) string {
	switch r.kind {
	case rowMate:
		return "MATE  " + m.currentProject().Name
	case rowCrew:
		if c, ok := m.crewByID(r.id); ok {
			return "CREW  " + c.CrewID
		}
		return "CREW"
	case rowCompletedGroup:
		return "COMPLETED"
	default:
		return "PROJECT"
	}
}

// ---------- inspector: Project ----------

// projectFields: identity (ID, Name) - the designated Mate summarized on
// one field, the registered Repos and a note per repo, Crews with the
// Project's own attention count folded in - the Mate's own last event,
// which is the closest thing a Project has to "what last happened here".
func (m Model) projectFields(r row, valueWidth int) []*line {
	p, ok := m.projectByID(r.id)
	if !ok {
		return nil
	}
	var out []*line
	out = append(out, m.textField("ID", p.ProjectID, m.p.Fg, valueWidth)...)
	out = append(out, m.textField("Name", p.Name, m.p.Fg, valueWidth)...)
	out = append(out, newLine())
	out = append(out, m.field("Mate", mateSummarySpans(p.Mate, m.g, m.p), valueWidth)...)
	out = append(out, m.field("Repos", m.availSpans(p.Repos.State, fmt.Sprint(len(p.Repos.Value)), p.Repos.Reason, m.p.Fg), valueWidth)...)
	if p.Repos.State == query.Known {
		for _, repo := range p.Repos.Value {
			out = append(out, m.note([]span{
				{text: repo.DisplayName, style: m.p.Fg},
				{text: "  " + repo.Path + "  " + repo.DefaultBranch, style: m.p.Dim},
			}, valueWidth)...)
		}
	}
	out = append(out, m.field("Crews", crewsCountSpans(p, m.g, m.p), valueWidth)...)
	out = append(out, newLine())
	out = append(out, m.field("Last event", m.eventSpans(p.Mate.LastEvent), valueWidth)...)
	return out
}

// reasonSpan is a dim " · <reason>" continuation span, or nothing when
// reason is empty - the same guard availabilitySpans applies, so a custom
// field builder never emits a bare " · " next to a reason nobody set (an
// unset Field in a hand-built fixture, never a real query-layer read: every
// Absent/Unknown field query.LoadSnapshot produces carries a reason).
func reasonSpan(reason string, g glyphSet, p palette) []span {
	if reason == "" {
		return nil
	}
	return []span{{text: " " + g.Dot + " " + reason, style: p.Dim}}
}

// rereadsSpan is the design's own hint on an Unknown read ("gợi ý r"), as
// its own span so it never collides with a reason guarded independently by
// reasonSpan.
func rereadsSpan(g glyphSet, p palette) span {
	return span{text: " " + g.Dot + " r re-reads", style: p.Dim}
}

// mateSummarySpans is the Project block's one-line Mate summary: agent name,
// harness and recorded status when there is one, or the amber "none
// assigned" / "unknown" the design's nomate-120 and unknown states show.
func mateSummarySpans(mate query.MateNode, g glyphSet, p palette) []span {
	switch mate.Designated.State {
	case query.Known:
		agent := mate.AgentName.Value
		if !mate.AgentName.IsKnown() {
			agent = "(agent name unknown)"
		}
		return []span{
			{text: agent, style: p.Fg},
			{text: " " + g.Dot + " " + string(mate.Designated.Value.HarnessKind) + " " + g.Dot + " ", style: p.Dim},
			statusSpan(string(mate.Designated.Value.Status), p),
		}
	case query.Absent:
		return append([]span{{text: "none assigned", style: p.Amber}}, reasonSpan(mate.Designated.Reason, g, p)...)
	default:
		out := append([]span{{text: "unknown", style: p.Amber}}, reasonSpan(mate.Designated.Reason, g, p)...)
		return append(out, rereadsSpan(g, p))
	}
}

// crewsCountSpans is the Project block's Crews field: the count, plus the
// Project's own rollup of how many of them need attention - the one place
// ProjectAttention.CrewsNeedingAttention surfaces in the inspector, since
// each Crew's own inspector already answers "why" for itself.
func crewsCountSpans(p query.ProjectNode, g glyphSet, pal palette) []span {
	out := []span{{text: fmt.Sprint(len(p.Crews)), style: pal.Fg}}
	if p.Attention.State == query.Known && p.Attention.Value.CrewsNeedingAttention > 0 {
		out = append(out, span{
			text:  " " + g.Dot + " " + plural(p.Attention.Value.CrewsNeedingAttention, "crew", "crews") + " need attention",
			style: pal.Amber,
		})
	}
	return out
}

// ---------- inspector: Mate ----------

// mateFields: identity (ID, Agent, Harness - all three carry the same
// Designated read state, so an unresolvable designation says unknown on
// every one of them rather than rendering the Mate row's own "mate:none"
// row-identity sentinel as fact), Recorded status and the full Binding
// block, then the Mate's own last event and error reason.
func (m Model) mateFields(valueWidth int) []*line {
	mate := m.currentProject().Mate
	var out []*line
	out = append(out, m.field("ID",
		m.availSpans(mate.Designated.State, mate.Designated.Value.MateID, mate.Designated.Reason, m.p.Fg),
		valueWidth)...)
	out = append(out, m.field("Agent",
		m.availSpans(mate.AgentName.State, mate.AgentName.Value, mate.AgentName.Reason, m.p.Fg),
		valueWidth)...)
	out = append(out, m.field("Harness",
		m.availSpans(mate.Designated.State, string(mate.Designated.Value.HarnessKind), mate.Designated.Reason, m.p.Fg),
		valueWidth)...)
	out = append(out, newLine())
	status := string(mate.Designated.Value.Status)
	out = append(out, m.field("Recorded status",
		m.availSpans(mate.Designated.State, status, mate.Designated.Reason, statusStyle(status, m.p)),
		valueWidth)...)
	out = append(out, m.bindingBlock(mate.Binding, valueWidth)...)
	out = append(out, newLine())
	out = append(out, m.field("Last event", m.eventSpans(mate.LastEvent), valueWidth)...)
	out = append(out, m.field("Reason", errorReasonSpans(mate.Error, true, m.g, m.p), valueWidth)...)
	return out
}

// TODO(task 21): the Task inspector block lived here, between the Project
// and Crew blocks. matev2 has no Task, so a Crew's own one-line job is on
// the Crew block instead.

// taskFields: identity (ID, Title), the Task's own recorded status, its
// repo, its attempts (rolled up from the Crews already in the snapshot -
// no extra read) and its own Attention field, which is where the design
// says the inspector answers the ATTENTION column's "why" in full
// ("Trong pane": cột ATTENTION một từ, inspector đầy đủ) - then its last
// event and error reason.
// attentionFieldSpans renders a Field[query.Attention]: the one-word Kind
// in amber plus the full Why sentence when the row needs attention, or
// "none" with the recorded reason it does not - never blank, and an
// unreadable attention read says so rather than silently agreeing nothing
// is wrong.
func attentionFieldSpans(f query.Field[query.Attention], g glyphSet, p palette) []span {
	switch f.State {
	case query.Known:
		return []span{
			{text: string(f.Value.Kind), style: p.Amber},
			{text: " " + g.Dot + " " + f.Value.Why, style: p.Dim},
		}
	case query.Absent:
		return append([]span{{text: "none", style: p.Dim}}, reasonSpan(f.Reason, g, p)...)
	default:
		return append(append([]span{{text: "unknown", style: p.Amber}}, reasonSpan(f.Reason, g, p)...), rereadsSpan(g, p))
	}
}

// ---------- inspector: Crew ----------

// crewFields: identity (ID and the one-line Task it was spawned for), then
// Recorded status, Harness, Agent and the full Binding block, then Repo/Repo
// ID/Branch/Worktree/Worktree status (Branch and Worktree share Worktree's
// own Field, per query.WorktreeValue's own doc: they come from one read),
// then Last event and Reason.
func (m Model) crewFields(r row, valueWidth int) []*line {
	c, ok := m.crewByID(r.id)
	if !ok {
		return nil
	}
	var out []*line
	out = append(out, m.textField("ID", c.CrewID, m.p.Fg, valueWidth)...)
	out = append(out, m.textField("Task", c.Task, m.p.Fg, valueWidth)...)
	out = append(out, newLine())
	out = append(out, m.field("Recorded status", []span{statusSpan(string(c.Status), m.p)}, valueWidth)...)
	out = append(out, m.textField("Harness", string(c.HarnessKind), m.p.Fg, valueWidth)...)
	out = append(out, m.field("Agent", m.availSpans(c.AgentName.State, c.AgentName.Value, c.AgentName.Reason, m.p.Fg), valueWidth)...)
	out = append(out, m.bindingBlock(c.Binding, valueWidth)...)
	out = append(out, newLine())
	out = append(out, m.field("Repo", m.repoSpans(c.Repo, false), valueWidth)...)
	out = append(out, m.textField("Repo ID", c.RepoID, m.p.Fg, valueWidth)...)
	out = append(out, m.field("Branch", m.availSpans(c.Worktree.State, c.Worktree.Value.Branch, c.Worktree.Reason, m.p.Fg), valueWidth)...)
	out = append(out, m.field("Worktree", m.availSpans(c.Worktree.State, c.Worktree.Value.Path, c.Worktree.Reason, m.p.Fg), valueWidth)...)
	worktreeStatus := worktreeStatusWord(c.Worktree.Value.Status)
	out = append(out, m.field("Worktree status",
		m.availSpans(c.Worktree.State, worktreeStatus, c.Worktree.Reason, statusStyle(worktreeStatus, m.p)),
		valueWidth)...)
	out = append(out, newLine())
	out = append(out, m.field("Last event", m.eventSpans(c.LastEvent), valueWidth)...)
	out = append(out, m.field("Reason", errorReasonSpans(c.Error, c.Status == query.CrewFailed, m.g, m.p), valueWidth)...)
	// TODO(task 18): the Runtime health / last check / reason block went
	// here. It read the health observer, which mvp.md defers to task 18.
	return out
}

// worktreeStatusWord maps query's durable persistence vocabulary
// (query.WorktreeStatus) to the word the design specifies for a Known
// recorded removal: "missing", styled red by statusStyle - distinct from an
// Unknown failed read, which renders "unknown" via availSpans regardless of
// this word (design/mate-console-design-notes.html, mate-console-states.html
// "repair-120"). Every other recorded status passes through unchanged.
func worktreeStatusWord(status query.WorktreeStatus) string {
	if status == query.WorktreeRecordedRemoved {
		return "missing"
	}
	return string(status)
}

// ---------- inspector: shared field builders ----------

// availSpans is availabilitySpans plus the design's own hint for an
// Unknown read ("gợi ý r"): every field in the inspector whose read can
// fail says so and points at the one key that might fix it.
func (m Model) availSpans(state query.FieldState, value, reason string, style lipgloss.Style) []span {
	spans := availabilitySpans(state, value, reason, style, m.g, m.p)
	if state == query.Unknown {
		spans = append(spans, span{text: " " + m.g.Dot + " r re-reads", style: m.p.Dim})
	}
	return spans
}

// repoSpans renders a Field[query.RepoValue]: the display name, with its
// path folded in dim when withPath (the Task block shows it; the Crew
// block does not, since the Crew's own worktree path is more specific).
func (m Model) repoSpans(f query.Field[query.RepoValue], withPath bool) []span {
	if f.State != query.Known {
		return m.availSpans(f.State, "", f.Reason, m.p.Fg)
	}
	out := []span{{text: f.Value.DisplayName, style: m.p.Fg}}
	if withPath && f.Value.Path != "" {
		out = append(out, span{text: "  " + f.Value.Path, style: m.p.Dim})
	}
	return out
}

// eventSpans renders a Field[query.EventValue] as "HH:MM:SS  event.type" -
// there is deliberately no free-text detail (query.EventValue's own doc:
// event payloads have no common detail key, so a UI must not invent one).
func (m Model) eventSpans(f query.Field[query.EventValue]) []span {
	if f.State != query.Known {
		return m.availSpans(f.State, "", f.Reason, m.p.Fg)
	}
	return []span{
		{text: f.Value.OccurredAt.Format("15:04:05"), style: m.p.Fg},
		{text: "  " + f.Value.EventType, style: m.p.Fg},
	}
}

// errorReasonSpans renders a Field[query.ErrorReason]: red when the row's
// own status is itself the failure state (statusIsError), amber for a
// non-failed error state such as needs_rebase, and the three-state
// Known/Absent/Unknown rule otherwise - including the Known-but-empty case
// query.ErrorReason's own doc calls out: the event read succeeded and
// recorded no reason, which is a fact, not a blank.
func errorReasonSpans(e query.Field[query.ErrorReason], statusIsError bool, g glyphSet, p palette) []span {
	switch e.State {
	case query.Known:
		if e.Value == "" {
			return []span{{text: "(no reason recorded)", style: p.Dim}}
		}
		style := p.Amber
		if statusIsError {
			style = p.Red
		}
		return []span{{text: string(e.Value), style: style}}
	case query.Absent:
		return append([]span{{text: "none", style: p.Dim}}, reasonSpan(e.Reason, g, p)...)
	default:
		return append(append([]span{{text: "unknown", style: p.Amber}}, reasonSpan(e.Reason, g, p)...), rereadsSpan(g, p))
	}
}

// bindingBlock renders the Binding field plus, only when the binding is
// Known, the caveat the query layer authored for its own status (see
// query.bindingField: "active does not prove the agent is alive", "attach
// is refused while stale", "the slot is held but no agent has started") as
// a separate unlabeled note line, and the Runtime (session/tab/pane) and
// Bound since fields. An Absent or Unknown binding renders as one field -
// there is nothing to run Runtime/Bound since off.
func (m Model) bindingBlock(b query.Field[query.BindingValue], valueWidth int) []*line {
	if b.State != query.Known {
		return m.field("Binding", m.availSpans(b.State, "", b.Reason, m.p.Fg), valueWidth)
	}
	v := b.Value
	var out []*line
	out = append(out, m.field("Binding", []span{statusSpan(string(v.Status), m.p)}, valueWidth)...)
	if b.Reason != "" {
		out = append(out, m.note([]span{{text: b.Reason, style: m.p.Dim}}, valueWidth)...)
	}
	out = append(out, m.field("Runtime", []span{
		{text: v.Runtime + " " + m.g.Dot + " " + v.Session, style: m.p.Fg},
		{text: " " + m.g.Dot + " tab " + v.Tab + " " + m.g.Dot + " pane " + v.Pane, style: m.p.Dim},
	}, valueWidth)...)
	out = append(out, m.textField("Bound since", v.BoundSince.Format("15:04:05"), m.p.Fg, valueWidth)...)
	return out
}

// note is a continuation-only line: no label, indented past the label
// column like a field's own wrapped continuation, for a caveat that
// belongs to the field just above it (a binding's note, a Project's
// per-repo line) rather than being its own labeled field.
func (m Model) note(value []span, valueWidth int) []*line {
	if len(value) == 0 {
		return nil
	}
	wrapped := wrapSpans(value, valueWidth)
	out := make([]*line, 0, len(wrapped))
	for _, ln := range wrapped {
		out = append(out, newLine().pad(1+labelWidth+1).addSpans(ln...))
	}
	return out
}

// windowContent scrolls content in place: it mirrors listLines' own
// two-pass reservation of "N more" indicator lines (model.go's window),
// but over the inspector's whole content - including its own title line,
// which is why the title scrolls away with everything else in Detail
// (design/mate-console-states.html, "Detail view — inspector takes the
// pane, scrolls in place") rather than pinning itself above the scroll.
func windowContent(content []*line, top, h int, g glyphSet, p palette) []*line {
	if h <= 0 {
		return nil
	}
	total := len(content)
	rowLines := h
	var start, end int
	for i := 0; i < 2; i++ {
		start, end = window(total, top, rowLines)
		need := 0
		if start > 0 {
			need++
		}
		if end < total {
			need++
		}
		if h-need == rowLines {
			break
		}
		rowLines = h - need
		if rowLines < 1 {
			rowLines = 1
		}
	}
	out := make([]*line, 0, h)
	if start > 0 {
		out = append(out, newLine().pad(2).add(fmt.Sprintf("%s %d more", g.Up, start), p.Dim))
	}
	for i := start; i < end; i++ {
		out = append(out, content[i])
	}
	if end < total {
		out = append(out, newLine().pad(2).add(fmt.Sprintf("%s %d more", g.Down, total-end), p.Dim))
	}
	return out
}

func (m Model) completedGroupFields(valueWidth int) []*line {
	if m.cur().kind != frameProject {
		return nil
	}
	var out []*line
	p := m.currentProject()
	finished := finishedCrews(p)
	out = append(out, m.textField("Finished", fmt.Sprintf("%d of %d crews", len(finished), len(p.Crews)), m.p.Fg, valueWidth)...)
	out = append(out, m.note([]span{{
		text:  "Finished crews stay in the snapshot. Enter shows or hides them in this list.",
		style: m.p.Dim,
	}}, valueWidth)...)
	if s := m.completedCrewNoteSpans(finished, valueWidth); len(s) > 0 {
		out = append(out, newLine())
		out = append(out, m.field("Needs attention", s, valueWidth)...)
	}
	return out
}

// crewByID resolves a Crew row against the Project frame it belongs to.
func (m Model) crewByID(id string) (query.CrewNode, bool) {
	for _, c := range m.currentProject().Crews {
		if c.CrewID == id {
			return c, true
		}
	}
	return query.CrewNode{}, false
}

// field renders one inspector field: a 16-cell dim label, one blank cell,
// then the value. The value is never truncated - the inspector is where a
// full path, branch or title lives - so it wraps after a '/' with
// continuation lines indented past the label, keeping a "/a1" suffix or an
// embedded crew ID intact.
func (m Model) field(label string, value []span, valueWidth int) []*line {
	head := newLine().pad(1).add(padRight(label, labelWidth), m.p.Dim).pad(1)
	if len(value) == 0 {
		return []*line{head}
	}
	wrapped := wrapSpans(value, valueWidth)
	out := []*line{head.addSpans(wrapped[0]...)}
	for _, cont := range wrapped[1:] {
		out = append(out, newLine().pad(1+labelWidth+1).addSpans(cont...))
	}
	return out
}

// text is one plain-value field, the common case.
func (m Model) textField(label, value string, style lipgloss.Style, valueWidth int) []*line {
	if value == "" {
		return m.field(label, nil, valueWidth)
	}
	return m.field(label, []span{{text: value, style: style}}, valueWidth)
}

func padRight(s string, w int) string {
	if n := cells(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// ---------- loading and error screens ----------

// loadingLines is the main region before the first read completes. Nothing
// is shown until there is something to show: no skeleton rows, because a
// skeleton row is indistinguishable from a real one that happens to be
// empty.
func (m Model) loadingLines(frameLayout) []*line {
	return []*line{
		newLine(),
		newLine().pad(1).add("Loading snapshot", m.p.Fg),
		newLine(),
		newLine().pad(1).add("Nothing is shown until the read completes.", m.p.Dim),
	}
}

// failedLines is the main region when the whole read failed. Only a total
// read failure gets here; one unreadable field is query.Unknown on that
// field and leaves the rest of the screen usable.
func (m Model) failedLines(l frameLayout) []*line {
	reason := "unknown error"
	if m.loadErr != nil {
		reason = m.loadErr.Error()
	}
	out := []*line{
		newLine(),
		newLine().pad(1).add("Could not read snapshot", m.p.Bold),
		newLine(),
	}
	for _, cont := range wrapAfterSlash(reason, maxInt(8, l.Cols-2)) {
		out = append(out, newLine().pad(1).add(cont, m.p.Red))
	}
	return append(out,
		newLine(),
		newLine().pad(1).add("No earlier snapshot is loaded, so nothing else is shown.", m.p.Dim),
		newLine().pad(1).add("r retries the read; q quits.", m.p.Dim),
	)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------- the footer ----------

// footerMessage is what line h-2 says. An explicit message - set by the
// last action (attach refusal, retry, detach) - always wins; every
// navigation and refresh clears m.msg (see update.go), and once it is clear
// the line falls back to the standing unknown-field warning built from the
// read layer's own Snapshot.Warnings, so an Unknown field is never silently
// dropped just because the reader moved the selection or changed screens.
func (m Model) footerMessage() footerMsg {
	if m.msg.tone != toneNone || m.msg.text != "" {
		return m.msg
	}
	if m.phase != phaseReady {
		return footerMsg{}
	}
	return warningsFooterMsg(m.tree.Warnings)
}

// warningsFooterMsg is the standing "N field(s) unknown" line, built only
// from the read layer's own warning list (query.FieldWarning: field, row,
// reason) - never invented here. A single warning names itself in full;
// more than one names the count and the first, since the message line is
// one line and cannot enumerate them all.
func warningsFooterMsg(warnings []query.FieldWarning) footerMsg {
	if len(warnings) == 0 {
		return footerMsg{}
	}
	first := warnings[0]
	if len(warnings) == 1 {
		return unknownMsg(fmt.Sprintf("1 field unknown: %s of %s (%s)", first.Field, first.Row.Label, first.Reason))
	}
	return unknownMsg(fmt.Sprintf("%d fields unknown; first: %s of %s (%s)", len(warnings), first.Field, first.Row.Label, first.Reason))
}

// keyHints is what line h-1 offers. Enter's label follows the selected row,
// and an attach that would be refused says so here rather than only after
// the keystroke.
func (m Model) keyHints(l frameLayout) []keyHint {
	// While the terminal is leaving or gone, the keys the reader has are the
	// child's, not the Console's (attach.go).
	if hints, ok := m.attachKeyHints(); ok {
		return hints
	}
	// The failure detail overlay owns the keyboard while it is open: its keys
	// are scroll and close, and q still quits (handled before any of this, in
	// onKey). Nothing here advertises a key that the overlay would swallow.
	if m.failureDetail {
		return []keyHint{
			{key: m.g.UpDown, desc: "Scroll", sacrifice: keyMovement},
			{key: "Esc", desc: "Close", sacrifice: keyBack},
			{key: "q", desc: "Quit", sacrifice: keyQuit},
		}
	}
	if m.actionInputMode {
		// q is a character here, not the quit key (see onKey), so the line
		// must not offer it as one: ctrl+c is the way out from inside a
		// name input.
		return []keyHint{{key: "Enter", desc: "Create project", sacrifice: keyAction}, {key: "Esc", desc: "Cancel", sacrifice: keyBack}, {key: "Ctrl+C", desc: "Quit", sacrifice: keyQuit}}
	}
	if m.harnessPick {
		return []keyHint{
			{key: m.g.UpDown, desc: "Move", sacrifice: keyMovement},
			{key: "Enter", desc: "Use " + string(harnessOrder[m.harnessIndex]), sacrifice: keyAction},
			{key: "Esc", desc: "Cancel", sacrifice: keyBack},
			{key: "q", desc: "Quit", sacrifice: keyQuit},
		}
	}
	if m.confirm != nil {
		return []keyHint{{key: "Enter", desc: string(m.confirm.choice.action) + " " + actionObject(m.confirm.choice), sacrifice: keyAction}, {key: "Esc", desc: "Cancel", sacrifice: keyBack}, {key: "q", desc: "Quit", sacrifice: keyQuit}}
	}
	if m.actions {
		return []keyHint{{key: m.g.UpDown, desc: "Move", sacrifice: keyMovement}, {key: "Enter", desc: "Run", sacrifice: keyAction}, {key: "Esc", desc: "Close", sacrifice: keyBack}, {key: "q", desc: "Quit", sacrifice: keyQuit}}
	}
	if m.actionBusy {
		// q still quits - it always does - but it abandons the running
		// action rather than waiting for it, so the key line says so up
		// front instead of only after the keystroke (see onBusyQuit).
		return []keyHint{{key: "q", desc: "Quit (abandons " + m.actionRunningDesc + ")", sacrifice: keyQuit}}
	}
	if l.TooSmall || m.phase == phaseLoading {
		return []keyHint{{key: "q", desc: "Quit"}}
	}
	if m.phase == phaseFailed {
		return []keyHint{{key: "r", desc: "Retry"}, {key: "q", desc: "Quit"}}
	}
	if m.detail {
		return append([]keyHint{{key: m.g.UpDown, desc: "Scroll", sacrifice: keyMovement}}, m.actionHints()...)
	}
	if m.focus == paneInspector {
		return []keyHint{
			{key: m.g.UpDown, desc: "Scroll", sacrifice: keyMovement},
			{key: "Tab", desc: "List", optional: true},
			{key: "Esc", desc: "List", sacrifice: keyBack},
			{key: "r", desc: "Refresh", sacrifice: keyRefresh},
			{key: "q", desc: "Quit", sacrifice: keyQuit},
		}
	}
	hints := make([]keyHint, 0, 6)
	if _, ok := m.selectedRow(); ok {
		hints = append(hints, keyHint{key: m.g.UpDown, desc: "Move", sacrifice: keyMovement})
	}
	hints = append(hints, m.actionHints()...)
	return hints
}

// actionHints are the Enter action for the selected row plus the exits.
func (m Model) actionHints() []keyHint {
	out := make([]keyHint, 0, 8)
	out = append(out, keyHint{key: "a", desc: "Actions", optional: true, sacrifice: keyAction})
	// 'e' opens the re-openable failure detail (session_failure.go) after a
	// session failed to open. It is offered early and dropped late: the cause
	// of a failed open is the most useful thing on the line when there is one,
	// and its absence (no failures recorded) is the only case it is not
	// offered at all. Optional, so a narrow frame still yields it before the
	// keys a reader needs to move and get out.
	if len(m.openFailures) > 0 {
		out = append(out, keyHint{key: "e", desc: "Details", optional: true, sacrifice: keyAction})
	}
	// 'n' is offered only where it works. At the Workspace it is the one
	// action available on a workspace with no Projects, so it is not
	// optional: dropping it would leave that reader with no key that does
	// anything. Below the Workspace the key refuses (see newProjectChoice),
	// and a key line must not name a trap.
	if m.cur().kind == frameWorkspace {
		out = append(out, keyHint{key: "n", desc: "New project", sacrifice: keyAction})
	}
	// The Mate keys are the captain's Project-screen affordances. They are
	// offered only on a Project frame's Mate row, because a key line must
	// never name a key that refuses where it is shown.
	if r, ok := m.selectedRow(); ok && m.cur().kind == frameProject && r.kind == rowMate {
		// Only when usable: unlike Enter, which is always bound and so says
		// "(unavailable)" rather than vanishing, these keys exist to offer an
		// action. Naming one that refuses is the trap the `n` key already
		// avoids, and it costs the width Esc Back needs at 80 columns.
		if label, ok := m.mateStartLabel(r); ok {
			out = append(out, keyHint{key: "s", desc: label, sacrifice: keyAction})
		}
		// TODO(task 10): the 'h' Change harness hint went here. Restarting
		// a Mate under another harness is task 10's surface.
	}
	if r, ok := m.selectedRow(); ok {
		out = append(out, keyHint{key: "Enter", desc: m.enterLabel(r), sacrifice: keyAction})
	}
	if m.detail {
		out = append(out, keyHint{key: "Esc", desc: "Back to list", sacrifice: keyBack})
	} else {
		if len(m.stack) > 1 {
			out = append(out, keyHint{key: "Esc", desc: "Back", sacrifice: keyBack})
		}
		if _, ok := m.selectedRow(); ok {
			desc := "Detail"
			if l := layout(m.w, m.h); l.Inspector > 0 {
				desc = "Inspector"
			}
			out = append(out, keyHint{key: "Tab", desc: desc, optional: true})
		}
	}
	return append(out,
		keyHint{key: "r", desc: "Refresh", sacrifice: keyRefresh},
		keyHint{key: "q", desc: "Quit", sacrifice: keyQuit})
}

func (m Model) enterLabel(r row) string {
	switch r.kind {
	case rowProject:
		return "Open project"
	case rowCompletedGroup:
		if m.completedOpen[m.cur().id] {
			return "Hide completed"
		}
		return "Show completed"
	}
	// A Mate/Crew row. Enter opens the embedded Agent View whenever it is
	// available - the same sessionAvailableFor predicate onEnter uses - and
	// only then falls back to the classic hand-off, with the snapshot's
	// refusal suffix, when it is not. Saying "Attach" unconditionally would
	// advertise the hand-off panel the Agent View replaced, which is exactly
	// the confusion issue #60 was.
	if _, ok := m.sessionAvailableFor(r); ok {
		return "Open agent view"
	}
	if r.kind == rowMate {
		return "Attach mate" + unavailableSuffix(m.attachRefusal(r))
	}
	return "Attach crew" + unavailableSuffix(m.attachRefusal(r))
}

// ---------- attach ----------
//
// The attach lifecycle is attach.go's: attachRefusal (the refusal decided
// from the snapshot), beginAttach, the hand-over and the return, and the
// failure taxonomy. keyHints above calls attachKeyHints for the key line
// while the terminal is handed over, and enterLabel calls sessionAvailableFor
// (falling back to attachRefusal for its suffix) so Enter is not a trap.
// Nothing else in this file knows about attach.
