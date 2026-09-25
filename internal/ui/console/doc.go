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
// next pane) and ClipboardFunc (OSC 52 for y). The CLI layer in cmd/mate is
// the only place those seams are built, which is where the persistence,
// runtime and host access actually happens (G6 gate, see
// docs/phase1/roadmap.md's G6 section and internal/query/types.go).
// boundary_test.go enforces that rather than trusting this paragraph.
//
// # The character grid
//
// The Console is a character grid, not a document. Its one structural
// guarantee, which every screen in the redesign is built on:
//
//	View always returns exactly h lines of exactly w display cells.
//
// Display cells, measured with lipgloss.Width - not bytes, not runes, since
// ids, paths and titles come from the database and are arbitrary text. Every
// line is emitted through the primitives in cells.go (line and screen), and
// nothing else in the package writes a rendered line by hand, so the
// guarantee is checkable in one place.
//
// Sizes come from tea.WindowSizeMsg and from nowhere else. layout(w, h)
// resolves the design's four breakpoints (see design/mate-console-design-
// notes.html, "Lưới ký tự và spacing") into the inspector width, the list
// width, a main region of exactly h-6 lines, and a too-small flag. frame.go
// owns the fixed six-line chrome around that region.
//
// The rest of the foundation:
//
//	glyphs.go   the Unicode and ASCII drawing alphabets, chosen by locale or
//	            MATE_ASCII - the only place this package reads the environment
//	palette.go  the ten design tokens as ANSI-indexed lipgloss styles
//	signals.go  selection, focus and status as three separate primitives that
//	            never share a drawing method, and each of which reads
//	            correctly with colour stripped
//	text.go     id abbreviation, title truncation, path wrapping, and
//	            whole-item drop-priority
//	seams.go    the surfaces that belong to the other Console tasks
//
// # Actions
//
// Press `a` to open the complete action menu for the selected snapshot row.
// Unavailable actions stay visible with their reason and Enter reports a
// refusal without invoking ActionFunc. Start, resume and workspace/project
// onboarding are non-destructive; stop, retry, repair and discard always open a
// confirmation containing Object, Scope and Effect before ActionFunc runs.
// The action runner is asynchronous, and its result triggers one snapshot
// re-read. Refusals and service failures use different message wording: a
// refusal says that nothing started, while a failure says the service was
// attempted and includes its diagnostics.
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
// golden_test.go carries the harness the Console tests share. The usual
// shape of a test is one line:
//
//	func TestCrewListAt120(t *testing.T) {
//	    goldenFrame(t, "crew-list-120x36", sampleTree(), 120, 36, unicodeGlyphs)
//	}
//
// which builds a deterministic model (fixed AsOf, no colour), renders it,
// asserts the frame contract, and diffs the result against
// testdata/golden/crew-list-120x36.txt - reporting the differing lines with
// their cell widths and a caret under the first divergent column. To accept
// a deliberate change:
//
//	go test ./internal/ui/console -run TestCrewListAt120 -update
//
// The pieces are usable separately: newFixture builds the model,
// renderFrame renders and checks the contract, assertGolden does the diff,
// and assertFrameShape checks a frame built some other way. Fixtures are
// rendered with plainPalette, so a state that is only visible in colour is
// invisible in a fixture - which is the point: no signal in this package may
// be carried by colour alone. Fixture lines are space-padded to the full
// width, so they end in trailing whitespace on purpose.
package console
