package console

import (
	"context"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// LoadFunc reads the current navigation tree. Called once at startup, again
// on every manual refresh ('r'), and every treeTickInterval in the
// background (treeTickCmd) so the crew list, statuses and Mate state do not
// go stale between keystrokes. The header says "live · HH:MM:SS" for the
// time of the last successful load, or "stale · HH:MM:SS · <error>" when
// the most recent background load failed - the picture on screen is always
// exactly what the last successful read said, never a guess at what
// changed since.
type LoadFunc func(context.Context) (query.Snapshot, error)

// Action identifies a Console action. The UI owns only the interaction and
// passes the request to the CLI/application bridge; it never imports either
// persistence or runtime.
type Action string

const (
	ActionStart  Action = "start"
	ActionStop   Action = "stop"
	ActionResume Action = "resume"
	ActionRepair Action = "repair"
	// ActionMode flips a Project's communication mode (mvp.md section 5).
	// Today it only moves the flag and the label: the daemon that acts on
	// auto mode is mvp.md task 19, which is why the key line says "Mode"
	// and not "Auto reply".
	ActionMode Action = "mode"
	// ActionOnboard adds a Project to the workspace, or creates and starts
	// a Project's Mate.
	ActionOnboard Action = "onboard"
	// The message-box actions (mvp.md task 15, section 5). They act on a box
	// entry rather than on a snapshot row, which is why ActionRequest
	// carries a Crew of its own: the entry names a crew, and the Project
	// frame's selected row usually does not.
	//
	// ActionResolve hands one inbox item to the Mate as a `resolve:` line,
	// typed into its composer behind the from-app sentinel: the crew's
	// question, the status file to read, and the `mate send` that answers
	// the crew. It is the console's `[assign]` button and `a` key - the word
	// on the button is the reader's ("give this away"), the word on the wire
	// is the Mate's manual's, and they are deliberately not the same.
	//
	// ActionReply and ActionPeek are the CLI's, not the console's: no
	// console surface issues either one since 2026-09-19 - a reader who
	// wants to talk to a crew or read its screen opens that crew's pane,
	// which is what a box row now does. They stay in the enum because
	// cmd/mate implements them and an ActionFunc is free to be asked.
	ActionResolve Action = "resolve"
	ActionReply   Action = "reply"
	ActionPeek    Action = "peek"
	// ActionNotice asks the optional classifier for an advisory only.
	ActionNotice Action = "explain_notice"
	// The two recovery actions. They exist because a Mate is a live
	// interactive agent sharing its composer with the reader: a key
	// sequence that went astray can leave junk half-typed in it, and an
	// agent can wedge outright. Both are entries in the Mate row's Actions
	// menu (actions.go), which the box zone opens with `o`: they are rare,
	// and a menu is where a reader looks for something they do not do every
	// day.
	//
	// ActionRestartMate stops the Mate and starts it again through the same
	// seams the action menu uses. ActionClearComposer presses Ctrl+U in the
	// Mate's pane - it types nothing and sends nothing, so it can never
	// become a message the Mate answers.
	ActionRestartMate   Action = "restart_mate"
	ActionClearComposer Action = "clear_composer"
	// ActionDiff is a Crew row's review surface (mvp.md task 21): what
	// `mate diff <project> <crew>` prints - the commits the crew's branch
	// carries beyond the project's default branch, then the patch. It writes
	// nothing, so it is the one action with no confirmation, and its result
	// is a screenful rather than a line: the Console puts it in the scrolling
	// overlay of diff.go instead of on the message line.
	ActionDiff Action = "diff"
	// ActionMerge lands a Crew's branch in the Project's default branch and
	// finishes the Crew (mvp.md task 22). It is offered on a Crew row only
	// while that Crew reads `wait-mate` - the one state in which the Crew
	// has said it is done and handed back - and it is absent, not disabled,
	// anywhere else: a menu entry that could never apply to this row is not
	// a refusal worth reading. It is dangerous, so the menu's own
	// confirmation stands in front of it, exactly as it does for
	// ActionRestartMate.
	//
	// ActionDiff is its read-only counterpart and deliberately has neither
	// restriction: a reader looks at a branch to decide whether it is worth
	// landing, which is before, not after, the Crew says `wait-mate`.
	ActionMerge Action = "merge"
	// ActionRestartCrew starts a crew's recorded harness again in the
	// worktree it already has, replacing an agent that died with its Herdr
	// pane (a Herdr server lost to a machine restart, a harness process
	// that exited). It is the console's counterpart of `mate crew relaunch`
	// and, unlike `crew spawn`, it does not need a new branch or worktree.
	// It is dangerous - a live crew would be stopped first - so the menu's
	// confirmation stands in front of it, like ActionRestartMate.
	ActionRestartCrew Action = "restart_crew"
	// TODO: v1 also had retry, discard and switch_harness. mate has no
	// retry (a Crew runs once), and no discard action: throwing work away
	// is `mate crew stop --discard`, on the captain's explicit word.
)

// ActionRequest is the identity selected from the snapshot. TargetKind is
// deliberately explicit: a Project id and a Crew id are both strings, and
// guessing which service to call from the string would assert more than the
// snapshot established.
type ActionRequest struct {
	Action     Action
	Target     string
	TargetKind string
	Input      string // project name for onboarding; the line to send for forward/reply
	// Repo is the repository path a workspace onboard registers, as typed
	// into the new-project form: absolute, or relative to the workspace
	// root. Empty on a workspace onboard means the Project starts with no
	// repo (docs/mvp.md M9), and it is empty on every other request.
	Repo string
	// Crew is the crew a box action names (mvp.md task 15). It is separate
	// from Target because those actions are addressed to a Project *and* one
	// of its crews, and folding the two into one string would make the
	// bridge guess which it had been given.
	Crew string
	// Key is the outbox dedup key of the box entry an ActionResolve hands
	// to the Mate (query.BoxEntry.AssignKey, mvp.md task 30), so a second
	// press on the same question is recognised rather than queued again.
	Key string
	// Harness is the agent chosen for this request: on an onboard it is the
	// harness the new Mate is created with. Empty everywhere else - an
	// existing Mate keeps its recorded harness.
	Harness query.HarnessKind
}

// ActionFunc is the application-service seam. A nil function keeps the
// Console read-only (useful for callers and tests that only need navigation).
type ActionFunc func(context.Context, ActionRequest) (string, error)

// phase is the state of the snapshot read itself: nothing loaded yet, a
// tree in hand, or a read that failed outright. It is a different axis from
// query.FieldState, which is per field: one unreadable field must never
// take the whole screen to an error page.
type phase int

const (
	phaseLoading phase = iota
	phaseReady
	phaseFailed
)

// frameKind is what one level of the navigation stack lists.
type frameKind int

const (
	frameWorkspace frameKind = iota // the Workspace: its Projects
	frameProject                    // one Project: its Mate row and its Crews
)

// TODO(task 21): v1 had a third level, frameTask, listing one Task's Crew
// attempts. mate has no Task and a Crew runs once, so the Project frame
// lists Crews directly.

func (k frameKind) String() string {
	switch k {
	case frameProject:
		return "project"
	default:
		return "workspace"
	}
}

// frame is one level of the navigation stack. It owns its own selection, so
// coming back up a level restores the row the reader left from without the
// parent having to remember anything.
//
// sel is the index into the frame's rows; selID is that row's identity.
// Both are kept: sel is what the keys move, selID is what survives a
// refresh in which rows were inserted or removed above the selection.
type frame struct {
	kind  frameKind
	id    string // the Project or Task this frame lists; "" for the Workspace
	sel   int
	selID string
	top   int // first visible row, the frame's scroll offset
}

// pane is which region the arrow keys move in.
type pane int

const (
	paneList pane = iota
	// paneDetail is the selected row's object (pane_detail.go).
	paneDetail
	// paneBox is the box (pane_box.go). It is only a legal focus while the
	// box is drawn; relayout sends focus back to the list as soon as it is
	// not, the same rule paneDetail follows.
	paneBox
)

// footerTone selects the message line's word-and-colour pairing. The tone
// never travels alone: error and attention are prefixed "! ", unknown "? ",
// so the line still reads correctly with colour stripped.
type footerTone int

const (
	toneNone footerTone = iota
	toneInfo
	toneOK
	toneWarn
	toneUnknown
	toneError
)

// footerMsg is the one-line message on line h-2. Empty when there is
// nothing to say.
type footerMsg struct {
	tone footerTone
	text string
}

func okMsg(text string) footerMsg      { return footerMsg{tone: toneOK, text: text} }
func warnMsg(text string) footerMsg    { return footerMsg{tone: toneWarn, text: text} }
func errMsg(text string) footerMsg     { return footerMsg{tone: toneError, text: text} }
func infoMsg(text string) footerMsg    { return footerMsg{tone: toneInfo, text: text} }
func unknownMsg(text string) footerMsg { return footerMsg{tone: toneUnknown, text: text} }

// Model is the Console's Bubble Tea model. Zero value is not usable; build
// one with New.
type Model struct {
	noticeClassifier bool

	load   LoadFunc
	action ActionFunc

	// stage is the host-pane attach (docs/mvp.md M10). Enter and a click on
	// a Mate/Crew row call it and stay on the tree. nil means no host pane.
	stage StageFunc
	// review is `e`: a tab beside the Console opens that crew's report.
	// nil means there is no host tab to open.
	review ReviewFunc
	// noHost says how to get a next pane while stage is nil; blank means
	// the default advice.
	noHost string
	// w and h come from tea.WindowSizeMsg and from nowhere else. Before the
	// first size message they are 0 and View renders nothing, rather than
	// guessing a size and drawing a frame the terminal never asked for.
	w, h int

	phase   phase
	loadErr error
	// tree.AsOf is the honest age of the picture on screen: when the read
	// completed, taken by the query layer's own clock (query.Snapshot.AsOf)
	// after its last field loaded - not a UI-owned clock, and not a database
	// column, since no row records "when did someone last look".
	tree query.Snapshot
	// hasLoaded distinguishes the first load (position starts at the top of
	// the tree) from every later refresh (position is re-found by selID and
	// then clamped - see reconcileSelection).
	hasLoaded bool
	// lastLoadErr is the error from the most recent load that followed the
	// first one (background tick or 'r'), or nil once one succeeds. It is
	// what the header's "stale" wording reads (frame.go): the tree itself
	// is never rolled back on a failed refresh (onTreeLoaded), so without a
	// field of its own the header would keep calling a known-bad picture
	// "live".
	lastLoadErr error
	// treeGen guards the auto-refresh tick chain the way sess.gen guards the
	// session poll chain (session_mode.go): a tick whose gen does not match
	// this one is a leftover from a chain that is no longer the current one
	// and is dropped rather than acted on. Bubble Tea gives tea.Tick no way
	// to be cancelled once scheduled, so this is the only way to retire a
	// chain.
	treeGen int
	// treeLoadInFlight is set the moment a tree load (tick, 'r', or the
	// failed-phase retry) is issued and cleared when its treeLoadedMsg
	// lands. A tick that fires while it is still true reschedules without
	// issuing a second concurrent load.
	treeLoadInFlight bool
	// treeTickIntervalOverride lets tests replace the real 2s auto-refresh
	// cadence (treeTickInterval below) so they do not have to block on it.
	// Zero (every production Console) means the real interval.
	treeTickIntervalOverride time.Duration
	// treeTickStarted is set the moment the auto-refresh chain is scheduled,
	// which happens exactly once per Console run, on the first treeLoadedMsg
	// Update sees (Init's own comment explains why there rather than in
	// Init itself). It exists solely to keep that one place from ever
	// scheduling a second chain.
	treeTickStarted bool

	stack  []frame
	focus  pane
	detail bool // < 100 cols: the inspector takes the whole main region
	// completedOpen records which Completed groups the reader has expanded,
	// keyed by the parent Project id. Presentation only: expanding does not
	// change the snapshot. A missing key is collapsed, which is the default
	// view.
	completedOpen map[string]bool
	// detailSel is the field under detail's cursor; every selection change
	// resets it.
	detailSel int
	// keysOpen is `?`: the key list, until the next key.
	keysOpen bool
	// notice is WithNotice's line; the first key clears it.
	notice string
	// staged is what the next pane shows, as far as this Console knows: the
	// last StageFunc call's target and outcome.
	staged stagedPane

	// diff is the open diff overlay (diff.go, mvp.md task 21): the text one
	// ActionDiff returned, the crew and branch it names, and where the
	// reader has scrolled to. Its zero value is closed.
	diff diffFlow

	// boxSel is the box selection; -1 follows the newest entry.
	boxSel int
	// boxAll is the `[all]` toggle: draw the whole merged log instead of the
	// inbox, on every box surface at once. It is a debugging view, it lives
	// for the Console's run only, and it defaults to off - the box exists to
	// show what somebody still has to decide on, and a filter a reader has to
	// re-apply on every start is a filter that is not the default.
	boxAll  bool
	actions bool
	// menu is the open actions sheet (menu.go).
	menu []menuEntry
	// actionRow is the row the menu was built for. It is kept because the
	// sheet can be opened for a row the list's cursor is not on (the box's
	// `o`), and the sheet's title names it.
	actionRow       row
	actionIndex     int
	confirm         *actionConfirmation
	actionInput     string
	actionInputMode bool
	// actionRepo is the new-project form's second field, the repository
	// path; actionField is which of the two fields the keyboard types into.
	actionRepo  string
	actionField onboardField
	// pendingChoice is the action the currently open sub-modal will run -
	// the name input or the harness picker. It is kept here rather than read
	// back off the menu cursor because 'n', 's' and 'h' all open a modal
	// with no menu behind it at all.
	pendingChoice actionChoice
	// harnessPick is the two-entry claude/codex chooser. It is its own flag
	// rather than a mode of the text input: one takes free text, the other a
	// bounded choice, and only the second can be confirmed afterwards.
	harnessPick  bool
	harnessIndex int
	actionBusy   bool
	// actionRunningDesc names the in-flight action ("stop crew_xyz") for the
	// one message that can still be shown if it is abandoned - see
	// actionAbandoned and onKey's actionBusy branch.
	actionRunningDesc string
	// actionCancel cancels the context the in-flight ActionFunc is running
	// under. Bubble Tea 1.2.4 cannot stop that goroutine itself (tea.go:
	// "we'll have to leak the goroutine until Cmd returns"), so this is the
	// one signal quitting mid-action can still send it.
	actionCancel context.CancelFunc
	// actionDone is closed when the in-flight ActionFunc returns, whether
	// it finished or was cancelled. A quit mid-action hands it to cmd/mate
	// (AbandonedActionDone) so the process waits for the action's own
	// cleanup instead of exiting under it.
	actionDone chan struct{}
	// clipboard is WithClipboard's writer; nil means y has nowhere to copy.
	clipboard ClipboardFunc
	// actionStartedAt is when the in-flight action began; the refresh tick
	// shows the time since then on the running line (runningLine).
	actionStartedAt time.Time
	// actionAbandoned is set when the operator quits while actionBusy: the
	// description of what was abandoned, surfaced to cmd/mate via
	// AbandonedAction so it can tell the operator plainly after the
	// terminal is restored, since the Console itself has nothing left to
	// draw by then.
	actionAbandoned string

	msg             footerMsg
	actionAfterRead *footerMsg
	g               glyphSet
	p               palette

	// TODO(task 18): the health tick chain and the workspace incident
	// overlay lived on the Model here. mvp.md defers the observer that
	// feeds them (internal/watch) to task 18.

	// ctx is the context the program itself was started with (cmd/mate's
	// handleConsole, via WithContext) - not context.Background(), so an
	// in-flight action's own context is a child of something the program
	// actually owns and can act on. nil in tests that build a Model
	// directly with New; baseCtx falls back to context.Background() then.
	ctx context.Context

	quitting bool
}

// WithContext attaches the context the program itself owns - cmd/mate's
// handleConsole passes the same context it gave tea.WithContext. Every
// in-flight ActionFunc call is a child of this context rather than of
// context.Background(), so quitting mid-action has something to cancel.
// Optional: a Model built without it (every existing test) falls back to
// context.Background() in baseCtx.
func (m Model) WithContext(ctx context.Context) Model {
	m.ctx = ctx
	return m
}

// ClipboardFunc writes one complete OSC 52 clipboard sequence to the real
// terminal the Console is drawn on.
type ClipboardFunc func(seq []byte)

// WithClipboard attaches the real terminal's clipboard, which y writes to.
func (m Model) WithClipboard(fn ClipboardFunc) Model {
	m.clipboard = fn
	return m
}

// WithNotice puts a line on the status line from the first frame: what
// happened before the Console started (a host split that failed), which
// stderr would lose under the alt screen.
func (m Model) WithNotice(text string) Model {
	m.notice = text
	return m
}

// WithStage installs the host-pane attach. A nil function means there is
// no host pane, and Enter says so rather than showing anything here.
func (m Model) WithStage(fn StageFunc) Model {
	m.stage = fn
	return m
}

// WithReview installs `e`. A nil function means there is no host tab, and
// `e` says so rather than opening a report here.
func (m Model) WithReview(fn ReviewFunc) Model {
	m.review = fn
	return m
}

// WithNoHostHint replaces the advice Enter gives while there is no host
// pane, such as what to do instead over ssh.
func (m Model) WithNoHostHint(text string) Model {
	m.noHost = text
	return m
}

// noHostHint is how to get a next pane, as Enter and the menu say it.
func (m Model) noHostHint() string {
	if m.noHost != "" {
		return m.noHost
	}
	return "run mate console inside WezTerm or Ghostty"
}

// baseCtx is ctx if WithContext set one, else context.Background().
func (m Model) baseCtx() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

// AbandonedAction reports what was abandoned if the operator quit while an
// action was in flight (empty, false otherwise). cmd/mate checks this after
// tea.Program.Run returns and tells the operator plainly - the Console's own
// screen is gone by then, so this is the one place left to say it.
func (m Model) AbandonedAction() (string, bool) {
	return m.actionAbandoned, m.actionAbandoned != ""
}

// AbandonedActionDone is closed once the abandoned action has returned -
// after its context was cancelled and it has undone what it had started.
// Nil when no action was abandoned.
func (m Model) AbandonedActionDone() <-chan struct{} {
	if m.actionAbandoned == "" {
		return nil
	}
	return m.actionDone
}

// New builds the initial Console model. load must be non-nil in
// production; tests pass fakes.
func New(load LoadFunc, action ...ActionFunc) Model {
	var run ActionFunc
	if len(action) > 0 {
		run = action[0]
	}
	return Model{
		load:          load,
		action:        run,
		phase:         phaseLoading,
		stack:         []frame{{kind: frameWorkspace}},
		focus:         paneList,
		completedOpen: map[string]bool{},
		// -1 is "follow the newest box entry".
		boxSel: -1,
		g:      glyphsFor(os.Getenv),
		p:      defaultPalette(),
		// Init issues the first load immediately; this marks it in flight
		// so a tick that fires before it resolves reschedules instead of
		// starting a second, redundant load.
		treeLoadInFlight: true,
	}
}

// Init kicks off the first tree load. Init returns a single Cmd rather than
// a tea.Batch that also starts the auto-refresh tick chain: Bubble Tea's own
// Program unpacks a tea.BatchMsg back into its Cmds via its event loop, but
// every test in this package drives Update directly with
// `m, _ = send(t, m, m.Init()())` - one Cmd invoked once, its one resulting
// Msg fed straight to Update - and a BatchMsg there would need every such
// call site taught to unpack it. The tick chain starts instead the moment
// the first treeLoadedMsg (success or failure) comes back from this load -
// see Update's treeLoadedMsg case and Model.treeTickStarted - which reaches
// exactly the same place at exactly the same moment without it.
func (m Model) Init() tea.Cmd {
	return loadCmd(m.load)
}

// Quitting reports whether the model asked Bubble Tea to quit, for callers
// that want to distinguish a clean exit from tea.Program.Run's own error.
func (m Model) Quitting() bool { return m.quitting }

type treeLoadedMsg struct {
	tree query.Snapshot
	err  error
}

func loadCmd(load LoadFunc) tea.Cmd {
	return func() tea.Msg {
		if load == nil {
			return treeLoadedMsg{}
		}
		tree, err := load(context.Background())
		return treeLoadedMsg{tree: tree, err: err}
	}
}

// treeTickInterval is the auto-refresh cadence: every 2s the Console loads
// the tree in the background, whatever frame is on screen (gallery,
// project, or a session view - onTreeLoaded's refresh path is the same one
// 'r' already takes, so a session view's crews table behind it is fresh by
// the time the reader leaves). The much faster 1s session-metadata and box
// ticks (session_mode.go) are unrelated and unchanged.
const defaultTreeTickInterval = 2 * time.Second

// treeTickInterval is defaultTreeTickInterval unless a test overrode it via
// Model.treeTickIntervalOverride, mirroring pollInterval's own override
// (session_mode.go) so a test can exercise the chain without a 2s sleep.
func (m Model) treeTickInterval() time.Duration {
	if m.treeTickIntervalOverride > 0 {
		return m.treeTickIntervalOverride
	}
	return defaultTreeTickInterval
}

// treeTickMsg requests the next background tree load. at is when it fired,
// which is also the clock the running-action line counts its seconds by.
type treeTickMsg struct {
	gen int
	at  time.Time
}

func treeTickCmd(interval time.Duration, gen int) tea.Cmd {
	return tea.Tick(interval, func(at time.Time) tea.Msg {
		return treeTickMsg{gen: gen, at: at}
	})
}

// ---------- navigation ----------

// cur is the frame the keys currently act on. The stack is never empty:
// every path that shortens it re-seeds the Workspace frame.
func (m Model) cur() frame {
	if len(m.stack) == 0 {
		return frame{kind: frameWorkspace}
	}
	return m.stack[len(m.stack)-1]
}

// setCur replaces the top frame.
func (m Model) setCur(f frame) Model {
	if len(m.stack) == 0 {
		m.stack = []frame{f}
		return m
	}
	stack := make([]frame, len(m.stack))
	copy(stack, m.stack)
	stack[len(stack)-1] = f
	m.stack = stack
	return m
}

func (m Model) push(f frame) Model {
	m.stack = append(append([]frame{}, m.stack...), f)
	return m
}

func (m Model) pop() Model {
	if len(m.stack) <= 1 {
		return m
	}
	m.stack = append([]frame{}, m.stack[:len(m.stack)-1]...)
	return m
}

// projectByID resolves a Project frame's id against the tree as it stands
// now. Frames name entities by id, not by index, so a refresh that
// reordered or removed rows cannot silently re-point a frame at a different
// Project.
func (m Model) projectByID(id string) (query.ProjectNode, bool) {
	for _, p := range m.tree.Projects {
		if p.ProjectID == id {
			return p, true
		}
	}
	return query.ProjectNode{}, false
}

// currentProject is the Project of the innermost Project frame.
func (m Model) currentProject() query.ProjectNode {
	for i := len(m.stack) - 1; i >= 0; i-- {
		if m.stack[i].kind == frameProject {
			p, _ := m.projectByID(m.stack[i].id)
			return p
		}
	}
	return query.ProjectNode{}
}

// rowsFor computes the rows one frame lists, against the tree as it stands
// now. Recomputed on every render rather than cached: Phase 1 workspace
// volume is small (LoadSnapshot's own tradeoff, internal/query/load.go)
// and a cache would be one more place selection and tree could drift apart.
func (m Model) rowsFor(i int) []row {
	if i < 0 || i >= len(m.stack) {
		return nil
	}
	f := m.stack[i]
	switch f.kind {
	case frameWorkspace:
		return projectRows(m.tree.Projects)
	case frameProject:
		p, ok := m.projectByID(f.id)
		if !ok {
			return nil
		}
		return projectDetailRows(p, m.completedOpen[p.ProjectID])
	default:
		return nil
	}
}

// currentRows are the rows of the frame the keys act on.
func (m Model) currentRows() []row { return m.rowsFor(len(m.stack) - 1) }

// jumpToCrew rebuilds the navigation stack to Workspace -> Project and
// selects the given Crew, reusing reconcileSelection's own by-identity
// positioning rather than hand-computing a row index. A finished Crew sits
// in the collapsed Completed group, which reconcileSelection expands for
// exactly this case (revealCompletedIfSelHidden).
func (m Model) jumpToCrew(crewID string) (Model, bool) {
	for _, p := range m.tree.Projects {
		for _, c := range p.Crews {
			if c.CrewID != crewID {
				continue
			}
			m.stack = []frame{
				{kind: frameWorkspace, selID: p.ProjectID},
				{kind: frameProject, id: p.ProjectID, selID: c.CrewID},
			}
			m = m.reconcileSelection()
			m.focus = paneList
			m.detail = false
			m.detailSel = 0
			m.msg = footerMsg{}
			return m, true
		}
	}
	return m, false
}

// selectedRow is the row the keys act on, or false when the frame is empty.
func (m Model) selectedRow() (row, bool) {
	rows := m.currentRows()
	f := m.cur()
	if f.sel < 0 || f.sel >= len(rows) {
		return row{}, false
	}
	return rows[f.sel], true
}

// ---------- selection maintenance ----------

// relayout re-fits the model to the current terminal size. Called on every
// tea.WindowSizeMsg, and after any change that can move the selection:
//
//   - Detail is closed when an inspector column exists, because Detail is
//     what stands in for the inspector below 100 columns. Leaving it set
//     would draw a full-width Detail body under a split rule, which is a
//     frame the chrome contract does not describe.
//   - Focus is reset to the list when the inspector column disappears,
//     which is the mirror of the Detail rule above: focus names a pane the
//     keys act on, and paneInspector is not a legal answer to "which pane"
//     once there is no inspector column to draw it in. Leaving it set
//     strands the arrow keys on a pane that is not on screen and leaves no
//     pane drawn as accent.
//   - The selection is clamped to the rows the frame actually has.
//   - The scroll offset is moved so the selection is inside the body.
//
// Nothing else about the navigation stack changes: shrinking the terminal
// below the minimum and growing it back returns to the same place, because
// the too-small screen is a rendering decision and not a state transition.
func (m Model) relayout() Model {
	p := m.plan()
	if _, ok := p.slot(slotDetail); !ok && m.focus == paneDetail && !m.detail {
		m.focus = paneList
	}
	if _, ok := p.slot(slotBox); !ok && m.focus == paneBox {
		// Focus names a pane the keys act on, and a pane that is not drawn
		// is not an answer to that.
		m.focus = paneList
	}
	if m.h >= stackRows {
		// Tab swaps detail in for the list only below 20 rows.
		m.detail = false
	}
	f := m.cur()
	rows := m.currentRows()
	f.sel = clampInt(f.sel, 0, len(rows)-1)
	if f.sel < len(rows) {
		f.selID = rows[f.sel].id
	} else {
		f.selID = ""
	}
	m = m.setCur(f)
	f.top = m.listTop(m.plan())
	return m.setCur(f)
}

// reconcileSelection re-anchors the whole stack onto a freshly read tree.
//
// Every frame is re-found by identity: its own entity by id, then its
// selection by selID. A selection whose row is gone falls back to the same
// index, clamped - the nearest surviving neighbour, which is where a reader
// expects to land. A frame whose own entity is gone (the Project was
// removed, the Task no longer exists) is dropped along with every frame
// below it, rather than being left pointing at nothing.
//
// The inspector's scroll offset is reset whenever this changes what the
// inspector shows (a different row's id, or a different frame on top of the
// stack): every other action that changes the selection - moveSelection,
// onTab, onBack, open - already resets inspTop to 0, on the same reasoning
// a reader who lands on a different row expects to read it from the top,
// not from wherever they had scrolled the previous row to. Without this, a
// refresh that drops the selected Crew and falls back to its nearest
// surviving neighbour left the stored offset pointing at the old row's
// scroll depth, so windowContent (seams.go) rendered the new row's tail -
// its Reason field - instead of its title, exactly as though the reader had
// already scrolled it there themselves.
func (m Model) reconcileSelection() Model {
	prevSelID := m.cur().selID
	kept := make([]frame, 0, len(m.stack))
	for i, f := range m.stack {
		if f.kind != frameWorkspace && !m.frameEntityExists(i) {
			break
		}
		rows := m.rowsFor(i)
		sel := f.sel
		if f.selID != "" {
			if found := indexOfRowID(rows, f.selID); found >= 0 {
				sel = found
			} else {
				m.revealCompletedIfSelHidden(i, f.selID)
				rows = m.rowsFor(i)
				if found := indexOfRowID(rows, f.selID); found >= 0 {
					sel = found
				}
			}
		}
		sel = clampInt(sel, 0, len(rows)-1)
		f.sel = sel
		f.selID = ""
		if sel < len(rows) {
			f.selID = rows[sel].id
		}
		kept = append(kept, f)
	}
	if len(kept) == 0 {
		kept = []frame{{kind: frameWorkspace}}
	}
	m.stack = kept
	if len(m.stack) == 0 || m.stack[len(m.stack)-1].selID != prevSelID {
		m.detailSel = 0
	}
	return m.relayout()
}

// frameEntityExists reports whether the entity a frame names is still in
// the tree.
func (m Model) frameEntityExists(i int) bool {
	f := m.stack[i]
	switch f.kind {
	case frameProject:
		_, ok := m.projectByID(f.id)
		return ok
	default:
		return true
	}
}

// revealCompletedIfSelHidden expands the Completed group when a refresh or
// jump is trying to land on a finished row the default view hides. Expanding
// is presentation only and does not mutate the snapshot.
func (m Model) revealCompletedIfSelHidden(i int, selID string) {
	if selID == "" || m.completedOpen == nil {
		return
	}
	f := m.stack[i]
	if m.completedOpen[f.id] {
		return
	}
	if f.kind != frameProject {
		return
	}
	p, ok := m.projectByID(f.id)
	if !ok {
		return
	}
	for _, c := range p.Crews {
		if c.CrewID == selID && c.Closed {
			m.completedOpen[f.id] = true
			return
		}
	}
}

func indexOfRowID(rows []row, id string) int {
	for i, r := range rows {
		if r.id == id {
			return i
		}
	}
	return -1
}

// moveSelection moves the selection by delta rows, clamping at both ends
// and keeping the new selection inside the body region.
func (m Model) moveSelection(delta int) Model {
	rows := m.currentRows()
	if len(rows) == 0 {
		return m
	}
	f := m.cur()
	f.sel = clampInt(f.sel+delta, 0, len(rows)-1)
	f.selID = rows[f.sel].id
	m = m.setCur(f)
	m.detailSel = 0
	m.msg = footerMsg{}
	return m.relayout()
}

// clampTop resolves the scroll offset: never past the end of the list, and
// always far enough that the selected row is on screen.
func clampTop(top, sel, total, h int) int {
	if h <= 0 || total <= h {
		return 0
	}
	top = clampInt(top, 0, total-h)
	if sel < top {
		top = sel
	}
	if sel > top+h-1 {
		top = sel - h + 1
	}
	return clampInt(top, 0, total-h)
}

// clampScrollTop is clampTop for a surface that has no selected row: an
// overlay showing total lines in a window of h. It exists because passing
// sel=0 to clampTop does not mean "there is no selection" - the rule "keep
// the selection on screen" then drags the offset back to the top on every
// keystroke, so such an overlay never scrolls at all.
//
// The bound is total-h+1, not total-h, and the extra line is windowContent's
// (seams.go): as soon as the offset leaves the top, one row of the window
// goes to the "↑ N more" indicator, so at total-h the last line of the
// content is still one row below the fold. An overlay that advertises more
// content below it and cannot reach it is worse than one that does not
// scroll.
func clampScrollTop(top, total, h int) int {
	if h <= 0 || total <= h {
		return 0
	}
	return clampInt(top, 0, total-h+1)
}

// window is the [start, end) row range a body of h lines shows at offset
// top. h <= 0 (no size message yet) shows everything: the caller's screen
// enforces the real line budget, and hiding the whole list would be worse
// than a list the frame then trims.
func window(total, top, h int) (int, int) {
	if h <= 0 || total <= h {
		return 0, total
	}
	top = clampInt(top, 0, total-h)
	return top, top + h
}

// clampInt confines v to [lo, hi]. An empty range (hi < lo, which is what
// an empty row list produces) collapses to lo, so a selection index is
// never negative.
func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
