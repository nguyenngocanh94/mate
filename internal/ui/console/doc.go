// Package console is the G6 Console: a Bubble Tea model that renders the
// Workspace -> Project -> {Mate, Crew} navigation tree and acts on it. It is
// a controller only. An agent's own terminal is never drawn here: Enter on a
// Mate or Crew row asks the host terminal (Ghostty, WezTerm) to show
// `herdr agent attach` in the next pane (docs/mvp.md M10), and the Console
// stays on the tree.
//
// This package must never import internal/persistence, internal/runtime or
// internal/host, and must never call Herdr. It only knows internal/query's
// read types and caller-supplied seams: LoadFunc (how to re-read the tree),
// ActionFunc (how to invoke application services for start/stop/resume/
// retry/repair/discard/onboard), StageFunc (stage.go: show an agent in the
// next pane), ReviewFunc (`e`: the crew's report in a tab beside the
// Console) and ClipboardFunc (OSC 52 for y). The CLI layer in cmd/mate is
// the only place those seams are built, which is where the persistence,
// runtime and host access actually happens (G6 gate, see
// docs/phase1/roadmap.md's G6 section and internal/query/types.go).
// boundary_test.go enforces that rather than trusting this paragraph.
//
// # The character grid
//
// The Console is a character grid, not a document. Its one structural
// guarantee:
//
//	View always returns exactly h lines of exactly w display cells.
//
// Display cells, measured by grapheme cluster - not bytes, not runes, since
// ids, paths and titles come from the database and are arbitrary text.
// Every line is a gline rendered through grid.go, and nothing else writes a
// frame line, so the guarantee is checkable in one place.
//
// It is drawn for the left ~20% of the captain's terminal, 32 to 48
// columns, beside the pane the host shows agents in (docs/console-design.md,
// the "20% terminal butler" boards A-K). Sizes come from tea.WindowSizeMsg
// and nowhere else. layout.go's plan places one stack of panes - list,
// detail, box, or a bottom sheet in the box's place - above a status line
// and, at 30+ rows, a key line; the renderer and the mouse both read it.
//
//	grid.go      gline and tok: text in design tokens, the frame
//	palette.go   tokens as ANSI styles, and the lift rule for selected rows
//	glyphs.go    the Unicode and ASCII alphabets, chosen by locale or
//	             MATE_ASCII - the only place this package reads the
//	             environment; kinds.go and icons.go the Mate/Crew marks and
//	             harness icons cmd/mate settles on before the TUI starts
//	words.go     status words, their one-line forms and tints, !N
//	pane_*.go    the list, detail and box panes
//	sheets.go    actions (menu.go), confirm, new project, harness, diff, keys
//	chrome.go    pane rules, the status line, the key line
//	states.go    too small, loading, a failed read, an empty workspace
//
// # Actions
//
// Press `a` for the selection's actions sheet. The order never changes
// between objects of a kind; an action that cannot run stays in its place
// with · in the key column and the reason on the right; each entry has its
// own key. Destructive actions open the confirm sheet - object, scope,
// effect - where Enter cancels and only the key that asked runs it. The
// action runner is asynchronous, and its result triggers one snapshot
// re-read. Refusals and service failures are worded differently: a refusal
// says nothing started, a failure says the service ran and why it failed.
//
// # Show in next pane
//
// stage.go owns Enter on a Mate or Crew row, from the list, the box or a
// click. A refusal is decided from the snapshot alone - a Mate's binding
// read exactly like a Crew's - so the host is never asked to show what
// cannot be shown. A failure means the StageFunc ran and says what the host
// answered. Without a StageFunc there is no next pane, and Enter says so.
//
// # The golden-frame harness
//
// golden_test.go carries the harness the Console tests share, and
// design_golden_test.go draws every design board from designTree() (the
// boards' own acme workspace) at the board's own size:
//
//	{"design-b-project-40x36", func(t *testing.T) Model { return designProject(t, 40, 36, unicodeGlyphs) }},
//
// Each builds a deterministic model (fixed AsOf, no colour), renders it,
// asserts the frame contract, and diffs the result against
// testdata/golden/<name>.txt - reporting the differing lines with their
// cell widths and a caret under the first divergent column. To accept a
// deliberate change:
//
//	go test ./internal/ui/console -run TestDesignBoards -update
//
// The pieces are usable separately: newFixture builds the model,
// renderFrame renders and checks the contract, assertGolden does the diff,
// and assertFrameShape checks a frame built some other way. Fixtures are
// rendered with plainPalette, so a state that is only visible in colour is
// invisible in a fixture - which is the point: no signal in this package may
// be carried by colour alone. Fixture lines are space-padded to the full
// width, so they end in trailing whitespace on purpose.
package console
