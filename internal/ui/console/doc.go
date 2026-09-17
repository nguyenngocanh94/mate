// Package console is the G6 Console: a Bubble Tea model that renders the
// Workspace -> Project -> {Mate, Task -> Crew} navigation tree and opens the
// Agent View by default for resolvable live sessions. Stream mode is the
// primary path; the snapshot reader and then `matev2 attach` remain fallbacks.
//
// This package must never import internal/persistence or internal/runtime,
// and must never call Herdr. It only knows internal/query's read types and
// caller-supplied seams: LoadFunc (how to re-read the tree),
// AttachCmdFunc (how to build the `matev2 attach <target>` subprocess),
// ActionFunc (how to invoke application services for start/stop/resume/retry/
// repair/discard/onboard), and the three session-mode ports from ADR 0025
// (session.go) - SessionReader (snapshot fallback), SessionPrompt (send
// composer input), SessionClose (release the snapshot controller),
// SessionStreamFactory (open the primary PTY stream), and
// SessionMetadataReader (refresh status/runtime/inbox side channels). The
// CLI layer in cmd/matev2 is the only place those
// seams are built, which is where the persistence/runtime access actually
// happens (G6 gate, see
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
//	            MATEV2_ASCII - the only place this package reads the environment
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
// # The attach lifecycle
//
// attach.go owns Enter on a Mate or Crew row and everything that follows.
// Its two facts never render alike:
//
//	Attach refused: <what the snapshot says> - nothing started
//	Attach failed:  <taxonomy>, matev2 attach exit <n>
//
// A refusal is decided from the snapshot alone - a Mate's binding read
// exactly like a Crew's - so nothing is started for an attach that cannot
// happen. A failure means `matev2 attach` ran (or could not be started) and
// says what its exit establishes: the child's coded envelope when the
// hand-over's private result channel is available, or the code/class where
// only the exit remains (usage, runtime_unavailable, target_blocked,
// needs_repair; or shared exits 1, 10, 31). Neither ever implies the agent is
// alive or dead.
//
// The hand-over is announced, then handed over, then taken back:
//
//	Enter -> announce -> AttachHandedOverMsg ->
//	tea.Exec(handoverNotice{`matev2 attach <target>`}) -> AttachFinishedMsg ->
//	exactly one re-read -> the same task, the same row, re-found by id
//
// The announcement the reader is guaranteed to see is not a frame. In the
// pinned Bubble Tea 1.2.4 no frame can be: tea.Sequence orders messages and
// not renderer flushes, and ReleaseTerminal discards the alt screen every
// frame was drawn on. So the guarantee is a line handoverNotice writes to
// the terminal Bubble Tea has just released, immediately before the child
// starts - see handover.go for the mechanism and the evidence. The
// announcing frame is kept as the model state that ignores stray keys and
// as a gallery state; it is not the promise.
//
// While the terminal is leaving or gone the Console ignores keys (they were
// aimed at the agent session) except q, and its key line names the child's
// own detach - Ctrl+b then q, which does not stop the agent. That is
// Herdr's binding inside the subprocess (runtime.DetachKey), not a Console
// key: it is the only Ctrl+b left anywhere here, and it is named because
// the reader needs it to get out of somebody else's UI.
//
// # The session view's two focus zones
//
// The embedded session view has no prefix at all. It has two focus zones -
// the box (the left rail) and the terminal (the agent's PTY) - and exactly
// one owns the keyboard: the focused zone's border is drawn in the accent
// colour, the frame's single bottom hint line names only that zone's keys,
// a click moves focus, and F2 toggles it. Under terminal focus every key,
// including q, Esc and Ctrl+C, is encoded and written to the PTY; under box
// focus nothing reaches it. session_focus.go owns the model and the frame
// geometry, session_mouse.go the hit tests. The Ctrl+b prefix this view
// used to carry is gone: a prefix is a mode with no indicator, and a
// mis-typed one delivered the key after it into the Mate's own composer.
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
