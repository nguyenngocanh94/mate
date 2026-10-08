package console

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// Enter on a Mate or Crew row shows that agent in the host's next pane
// (docs/mvp.md M10). The Console never draws an agent itself: it builds a
// StageTarget from the loaded snapshot, refuses what the snapshot already
// rules out, and hands the rest to the StageFunc cmd/mate injected.

// StageFunc shows the named agent in the host terminal's sibling pane. The
// Console never talks to WezTerm or Ghostty; cmd/mate builds this closure.
// A nil StageFunc means the host is not one mate can drive, and Enter says
// so on the status line.
type StageFunc func(context.Context, StageTarget) error

// ToolViewFunc opens the tool bound to key (query.Snapshot.Tools) on
// target, in a tab beside the Console: `e` on a crew opens its report.
// target is the crew for a crew-scoped key, and only ProjectID for a
// project-scoped one. A nil ToolViewFunc means there is no host tab, and
// the key says so on the status line.
type ToolViewFunc func(ctx context.Context, key string, target StageTarget) error

// StageTargetKind says whether a target is a Mate or a Crew.
type StageTargetKind string

const (
	StageMate StageTargetKind = "mate"
	StageCrew StageTargetKind = "crew"
)

// StageTarget identifies the Mate or Crew to show, built from an
// already-loaded query.Snapshot node: this package never resolves a target
// itself, and a raw Herdr pane or tab id is never a valid ID here (ADR 0010).
type StageTarget struct {
	Kind        StageTargetKind
	ID          string // Mate id or Crew id
	ProjectID   string
	HarnessKind query.HarnessKind
	// AgentName is the Herdr agent name the host pane attaches to.
	AgentName string
}

// stageTargetAvailable reports whether Enter on row r can ask the host to
// show it: the row names a resolvable target and the snapshot records
// something to show (stageRefusal). It is the one predicate the key line
// and Enter both consult, so they cannot disagree. Whether a host is wired
// is a separate question, answered by beginStage.
func (m Model) stageTargetAvailable(r row) (StageTarget, bool) {
	target, ok := m.stageTargetFor(r)
	if !ok {
		return StageTarget{}, false
	}
	if _, refused := m.stageRefusal(r); refused {
		return StageTarget{}, false
	}
	return target, true
}

// stageTargetFor builds the StageTarget a row names, straight from the
// loaded snapshot. It decides only what the row names; stageTargetAvailable
// applies the refusal.
func (m Model) stageTargetFor(r row) (StageTarget, bool) {
	switch r.kind {
	case rowMate:
		mate := m.currentProject().Mate
		if !mate.Designated.IsKnown() || mate.Designated.Value.MateID == "" {
			return StageTarget{}, false
		}
		agent := ""
		if mate.AgentName.IsKnown() {
			agent = mate.AgentName.Value
		}
		return StageTarget{
			Kind:        StageMate,
			ID:          mate.Designated.Value.MateID,
			ProjectID:   m.currentProject().ProjectID,
			HarnessKind: mate.Designated.Value.HarnessKind,
			AgentName:   agent,
		}, true
	case rowCrew:
		c, ok := m.crewByID(r.id)
		if !ok || c.CrewID == "" {
			return StageTarget{}, false
		}
		agent := ""
		if c.AgentName.IsKnown() {
			agent = c.AgentName.Value
		}
		return StageTarget{
			Kind:        StageCrew,
			ID:          c.CrewID,
			ProjectID:   c.ProjectID,
			HarnessKind: c.HarnessKind,
			AgentName:   agent,
		}, true
	default:
		return StageTarget{}, false
	}
}

func stageLabel(t StageTarget) string {
	if t.AgentName != "" {
		return t.AgentName
	}
	return t.ID
}

// stageRefusal decides, from the snapshot alone, whether Enter can show
// this row, and says why not. A stale binding means mate could not confirm
// the agent stopped (ADR 0027) and `herdr agent attach` would reach nothing
// mate owns, so the host is never asked. A held but not active (reserved)
// binding is deliberately not a refusal; see the reserved case below.
//
// The Mate branch reads MateNode.Binding exactly like the Crew branch reads
// CrewNode.Binding: both are the query layer's own runtime_binding read.
func (m Model) stageRefusal(r row) (string, bool) {
	var binding query.Field[query.BindingValue]
	switch r.kind {
	case rowMate:
		mate := m.currentProject().Mate
		switch {
		case mate.Designated.State == query.Unknown:
			return "the Mate could not be read; r re-reads", true
		case !mate.Designated.IsKnown() || mate.Designated.Value.MateID == "":
			return "this Project has no Mate", true
		case !mate.Designated.Value.Status.OccupiesActiveSlot():
			// created, stopped: there is no session to attach to, and the
			// snapshot is enough to know it. OccupiesActiveSlot is the
			// domain's own predicate for "this Mate holds the Project's one
			// active slot" (internal/domain/status.go), which is exactly the
			// set of statuses a session can exist for.
			// The stopped state is not a failure. It is not reworded to add
			// "press s to start it" because the 80-column budget this line
			// is held to (TestEveryAttachMessageFitsAnEightyColumnFrame)
			// has no room for both that and "no session to attach", and the
			// key line directly beneath already offers 's Resume mate'.
			return "Mate recorded " + string(mate.Designated.Value.Status) + ", no session to attach", true
		}
		binding = mate.Binding
	case rowCrew:
		c, ok := m.crewByID(r.id)
		if !ok {
			return "this attempt is no longer in the snapshot", true
		}
		binding = c.Binding
	default:
		return "this row cannot be attached to", true
	}
	switch binding.State {
	case query.Absent:
		return "no runtime binding recorded", true
	case query.Unknown:
		return "the binding could not be read; r re-reads", true
	}
	switch binding.Value.Status {
	case query.BindingActive, query.BindingReserved:
		// reserved is not a refusal. application.ResolveAttachTarget (the
		// CLI's own resolution, ADR 0010) accepts any held binding that is
		// not stale - reserved included - and a reserved binding is
		// frequently a live agent: a SIGKILL mid-spawn leaves `preparing` +
		// reserved binding + live agent (ADR 0012). Refusing it here would
		// refuse an attach the CLI allows, and would hide the one session a
		// crash recovery most needs to reach. The Agent View is enterable
		// for the same reason (stageTargetAvailable, session_mode.go).
		return "", false
	case query.BindingStale:
		// Its own sentence, not "not active": stale is the one status that
		// means mate could not confirm the agent stopped, and a reader who
		// sees it lumped in with a generic "not active" would read it as
		// "not running".
		return "binding recorded stale, stop unconfirmed", true
	case "":
		// A Known binding whose status is the empty string: the read
		// succeeded and recorded nothing to compare against active, so it
		// is refused as unreadable rather than printed as "binding recorded
		// , not active".
		return "the binding records no status", true
	default:
		return fmt.Sprintf("binding recorded %s, not active", binding.Value.Status), true
	}
}

func unavailableSuffix(_ string, refused bool) string {
	if refused {
		return " (unavailable)"
	}
	return ""
}

// stageRow is Enter on a Mate or Crew row, from the list, the box or a
// click: refuse what the snapshot rules out, else ask the host.
func (m Model) stageRow(r row) (Model, tea.Cmd) {
	target, ok := m.stageTargetAvailable(r)
	if !ok {
		reason, refused := m.stageRefusal(r)
		if !refused {
			reason = "this row names no agent"
		}
		m.msg = errMsg("Show refused: " + reason + " " + m.g.Dot + " nothing was shown")
		return m, nil
	}
	return m.beginStage(target)
}

// beginStage asks the host to show the agent in the sibling pane and stays
// on the tree. It never opens a PTY in this process (docs/mvp.md M10).
// Without a StageFunc there is no host pane to fill, which is said plainly
// rather than pretended.
func (m Model) beginStage(target StageTarget) (Model, tea.Cmd) {
	label := stageLabel(target)
	if m.stage == nil {
		m.msg = errMsg("no next pane: " + m.noHostHint())
		return m, nil
	}
	m.msg = infoMsg(m.g.Arrow + " next pane " + m.g.Dot + " opening " + label + m.g.Ellipsis)
	fn := m.stage
	ctx := m.baseCtx()
	return m, func() tea.Msg {
		return stageDoneMsg{err: fn(ctx, target), target: target}
	}
}

type stageDoneMsg struct {
	err    error
	target StageTarget
}

// stagedPane is what the next pane shows, as the status line says it.
type stagedPane struct {
	kind   StageTargetKind
	name   string
	target StageTarget
	// err is the last stage's failure, in a few words; r retries it.
	err string
}

// onStageDone records what the next pane shows now. A failure keeps the
// target, so r can try again.
func (m Model) onStageDone(msg stageDoneMsg) Model {
	m.msg = footerMsg{}
	if msg.err != nil {
		m.staged = stagedPane{kind: msg.target.Kind, target: msg.target, err: oneLine(msg.err.Error())}
		return m
	}
	m.staged = stagedPane{kind: msg.target.Kind, name: m.stagedName(msg.target), target: msg.target}
	return m
}

// stagedName is how the status line names a staged agent: the Mate by its
// Project, a Crew by its task.
func (m Model) stagedName(t StageTarget) string {
	p, ok := m.projectByID(t.ProjectID)
	if !ok {
		return stageLabel(t)
	}
	if t.Kind == StageMate {
		return p.Name
	}
	for _, c := range p.Crews {
		if c.CrewID == t.ID {
			if task := strings.TrimSpace(oneLine(c.Task)); task != "" {
				return task
			}
		}
	}
	return shortID(t.ID, m.g)
}

// retryStage is r while the last stage failed: the same target again.
func (m Model) retryStage() (Model, tea.Cmd) {
	return m.beginStage(m.staged.target)
}

// ownedKeys are the keys the Console handles itself on the tree, in any
// pane (onKey, onListKey, onDetailKey, onBoxKey). Every other key a tool
// binds is handed to that tool (beginToolView). The tool registry refuses a
// binding on one of these (tool.ConsoleKeys, which a test holds equal to
// this set and to the keys the handlers switch on).
var ownedKeys = map[string]bool{
	"?": true, "G": true, "a": true, "backspace": true, "ctrl+c": true,
	"down": true, "end": true, "enter": true, "esc": true, "g": true,
	"home": true, "j": true, "k": true, "l": true, "m": true, "n": true,
	"o": true, "pgdown": true, "pgup": true, "q": true, "r": true,
	"s": true, "shift+tab": true, "tab": true, "up": true, "y": true,
}

// toolHints are the key line's hints for the tool keys that act on every
// row, the project-scope bindings, with each tool's own label. A crew key
// means something only on a crew row; the key sheet lists it.
func (m Model) toolHints() []keyHint {
	var out []keyHint
	for _, b := range m.tree.Tools {
		if b.Scope == "project" && !ownedKeys[b.Key] {
			out = append(out, keyHint{b.Key, b.Label})
		}
	}
	return out
}

// keyProject is the project a project key names: the box item's when the
// box has focus, otherwise the one open or selected (modeTarget).
func (m Model) keyProject() string {
	if m.focus == paneBox {
		items := m.boxItems()
		if sel := m.boxSelection(items); sel >= 0 {
			return items[sel].project
		}
	}
	return m.modeTarget()
}

// toolBound reports whether the snapshot binds a tool to key on any row.
func (m Model) toolBound(key string) bool {
	for _, b := range m.tree.Tools {
		if b.Key == key {
			return true
		}
	}
	return false
}

// beginToolView is a tool's key (query.Snapshot.Tools): open the tool bound
// to key on the row the key acts on, in the host tab, and stay on the tree.
// A row the key means nothing on is said so, and nothing opens.
func (m Model) beginToolView(key string) (Model, tea.Cmd) {
	var bound []query.ToolBinding
	for _, b := range m.tree.Tools {
		if b.Key == key {
			bound = append(bound, b)
		}
	}
	if len(bound) == 0 {
		m.msg = errMsg("no tool bound to " + key)
		return m, nil
	}
	var (
		b      query.ToolBinding
		target StageTarget
		ok     bool
	)
	for _, b = range bound {
		if target, ok = m.toolTarget(b.Scope); ok {
			break
		}
	}
	if !ok {
		m.msg = errMsg(key + " opens a " + bound[0].Scope + "'s " + bound[0].Label)
		return m, nil
	}
	if m.toolView == nil {
		m.msg = errMsg("no next pane: " + m.noHostHint())
		return m, nil
	}
	m.msg = infoMsg(m.g.Arrow + " " + b.Label + " " + m.g.Dot + " opening " + toolTargetName(target) + m.g.Ellipsis)
	fn := m.toolView
	ctx := m.baseCtx()
	return m, func() tea.Msg {
		return toolViewDoneMsg{err: fn(ctx, key, target), label: b.Label, target: target}
	}
}

type toolViewDoneMsg struct {
	err    error
	label  string
	target StageTarget
}

func (m Model) onToolViewDone(msg toolViewDoneMsg) Model {
	if msg.err != nil {
		m.msg = errMsg(oneLine(msg.err.Error()))
		return m
	}
	m.msg = infoMsg(m.g.Arrow + " " + msg.label + " " + m.g.Dot + " " + toolTargetName(msg.target))
	return m
}

// toolTargetName is the crew a tool opened on, or its project.
func toolTargetName(t StageTarget) string {
	if t.ID != "" {
		return t.ID
	}
	return t.ProjectID
}

// toolTarget is what a key of scope acts on: on a crew row, that crew; on
// a project row, that project.
func (m Model) toolTarget(scope string) (StageTarget, bool) {
	switch scope {
	case "crew":
		return m.keyCrewTarget()
	case "project":
		if p := m.keyProject(); p != "" {
			return StageTarget{ProjectID: p}, true
		}
	}
	return StageTarget{}, false
}

// keyCrewTarget is the crew a crew key names: the box item under the cursor
// when the box has focus, otherwise the selected row when that row is a
// crew.
func (m Model) keyCrewTarget() (StageTarget, bool) {
	if m.focus == paneBox {
		if t, ok := m.boxCrewTarget(); ok {
			return t, true
		}
	}
	r, ok := m.selectedRow()
	if !ok || r.kind != rowCrew {
		return StageTarget{}, false
	}
	return m.stageTargetFor(r)
}

// boxCrewTarget is the crew a box row names, including one whose project
// is not the frame on screen.
func (m Model) boxCrewTarget() (StageTarget, bool) {
	items := m.boxItems()
	sel := m.boxSelection(items)
	if sel < 0 || items[sel].e.Crew == "" {
		return StageTarget{}, false
	}
	id, project := items[sel].e.Crew, items[sel].project
	for _, p := range m.tree.Projects {
		if p.ProjectID != project {
			continue
		}
		for _, c := range p.Crews {
			if c.CrewID != id {
				continue
			}
			agent := ""
			if c.AgentName.IsKnown() {
				agent = c.AgentName.Value
			}
			return StageTarget{
				Kind: StageCrew, ID: id, ProjectID: project,
				HarnessKind: c.HarnessKind, AgentName: agent,
			}, true
		}
	}
	return StageTarget{Kind: StageCrew, ID: id, ProjectID: project}, true
}

// clipboardSequence is OSC 52's clipboard write for text.
func clipboardSequence(text string) []byte {
	return []byte("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07")
}
