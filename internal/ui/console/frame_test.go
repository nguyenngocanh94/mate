package console

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// frameSizes are the sizes every frame test sweeps: one per breakpoint,
// their boundaries, and a too-small terminal.
var frameSizes = []struct {
	name string
	w, h int
}{
	{"160x48", 160, 48},
	{"140x40", 140, 40},
	{"139x36", 139, 36},
	{"120x36", 120, 36},
	{"100x30", 100, 30},
	{"99x24", 99, 24},
	{"80x24", 80, 24},
	{"60x16", 60, 16},
	{"56x14", 56, 14},
	{"200x17", 200, 17},
}

// hostileTree is a snapshot built to break a naive renderer: a title of
// two-cell CJK runes, a combining mark, an emoji-width rune, an id and a
// path far longer than any column, empty strings where a value is
// legitimately blank, and the five control-plane attacks the frame
// contract has to survive on its own: a literal newline (which would add a
// line to View if it ever reached the terminal), a tab, a raw SGR escape
// sequence, a variation-selector emoji immediately followed by more text in
// the same column (the shape that overflowed a 31-cell inspector value and
// silently dropped the word after it - see cutCells/wrapAfterSlash), and
// U+0085 (C1 NEL) - invisible to lipgloss.Width but read as its own
// line-break by a terminal that honours C1 controls, the same h-lines
// defect as the literal newline through the other half of the
// control-character space sanitizeText neutralises (cells.go).
func hostileTree() query.Snapshot {
	long := strings.Repeat("payments-api-", 12)
	return query.Snapshot{
		WorkspaceID: "ws_" + strings.Repeat("x", 40),
		// A Known Workspace so the header's >=100-column path route
		// (workspaceDisplayName/workspaceRoot, frame.go) actually renders
		// hostile text instead of falling back to the abbreviated id - a
		// zero-valued Workspace field never reaches that branch at all.
		Workspace: query.KnownField(query.WorkspaceValue{
			Name: "支払い\t‼️ " + long,
			Root: "/repos/evil\nworkspace/" + long,
		}),
		// A hostile standing warning so the message line (seams.go's
		// warningsFooterMsg, rendered by frame.go's messageLine) formats a
		// field/row/reason carrying CJK, an emoji, a tab and a raw SGR
		// escape - the chrome this PR's fix round changed and the hostile
		// sweep did not previously reach.
		Warnings: []query.FieldWarning{{
			Field: "worktree",
			Row: query.RowRef{
				Kind:  query.RowCrew,
				ID:    "crew_" + strings.Repeat("D", 40),
				Label: "修正\t" + long + " ‼️ \x1b[31m",
			},
			Reason: "lookup timed out\n(2s) \x1b[31mred\x1b[0m",
		}},
		Projects: []query.ProjectNode{
			{
				ProjectID: "proj_" + strings.Repeat("A", 60),
				Name:      "支払いのべき等性を修正する é " + long + "\nsecond line\u0085third line",
				Mate: query.MateNode{
					Designated: query.KnownField(query.MateIdentity{
						MateID: "mate_" + strings.Repeat("B", 40), HarnessKind: query.HarnessCodex, Status: query.MateUnknown,
					}),
					AgentName: query.KnownField("mate_" + strings.Repeat("B", 40)),
					Binding:   query.KnownField(query.BindingValue{Status: query.BindingActive, AgentName: "mate_" + strings.Repeat("B", 40)}),
					Error:     query.AbsentField[query.ErrorReason](notErrorState),
				},
				Crews: []query.CrewNode{
					{
						CrewID:    "crew_" + strings.Repeat("D", 40),
						Task:      "修正\t" + long + " ‼️ tail",
						Status:    query.CrewNeedsRepair,
						Worktree:  query.UnknownField[query.WorktreeValue]("worktree lookup failed"),
						AgentName: query.UnknownField[string]("binding lookup failed"),
						Binding:   query.UnknownField[query.BindingValue]("binding lookup failed"),
						Error:     query.KnownField(query.ErrorReason(strings.Repeat("因為 ", 40) + "\x1b[31mred\x1b[0m")),
					},
					{
						CrewID:    "",
						Status:    query.CrewBlocked,
						Worktree:  query.KnownField(query.WorktreeValue{Path: "", Branch: "", Status: ""}),
						AgentName: query.KnownField(""),
						Binding:   query.KnownField(query.BindingValue{Status: query.BindingReserved}),
						Error:     query.AbsentField[query.ErrorReason](notErrorState),
					},
					{
						CrewID: "crew_" + strings.Repeat("E", 40),
						Status: query.CrewFailed,
						Worktree: query.KnownField(query.WorktreeValue{
							Path:   "/repos/payments-api/.worktrees/crew_evil\nname/a1",
							Branch: "fix/branch",
						}),
						AgentName: query.KnownField(strings.Repeat("‼️", 8) + long),
						Binding:   query.KnownField(query.BindingValue{Status: query.BindingActive, AgentName: strings.Repeat("‼️", 8) + long}),
						Error:     query.AbsentField[query.ErrorReason](notErrorState),
					},
				},
			},
			{ProjectID: "", Name: "", Mate: unknownMate("designation lookup failed")},
		},
	}
}

// TestEveryFrameIsExactlyHLinesOfWCells is the invariant the whole series
// rests on. It sweeps both glyph sets, every breakpoint, every phase, both
// focus states, Detail, and content designed to overflow every column - and
// asserts the shape after each step rather than only at the end, so the
// failure names the state that broke it.
func TestEveryFrameIsExactlyHLinesOfWCells(t *testing.T) {
	trees := map[string]query.Snapshot{
		"sample":  sampleTree(),
		"hostile": hostileTree(),
		"empty":   {WorkspaceID: "ws_1"},
	}
	for _, g := range []glyphSet{unicodeGlyphs, asciiGlyphs} {
		for treeName, tree := range trees {
			for _, size := range frameSizes {
				t.Run(g.Name+"/"+treeName+"/"+size.name, func(t *testing.T) {
					m := newFixture(t, tree, size.w, size.h, g)
					// Walk the surfaces: drill into the *first* Project,
					// down its Crews to the hostile one in the Completed
					// group, page, focus the inspector, open Detail,
					// refresh, fail. hostileTree's hostile payload - the
					// tab/VS16 task line, the CJK error, the VS16 agent
					// name, the newline-in-a-path worktree - lives entirely
					// under Project 0; a sweep that moves "down" before its
					// first "enter" selects Project 1 (deliberately empty)
					// instead and never renders any of it, which is exactly
					// how this guard went blind to the grapheme regression
					// the direct text_test.go cases caught (see
					// assertHostileCrewAgentSurvives below). Project 1 is
					// still visited, at the end, once the hostile branch has
					// been rendered.
					steps := []string{"enter", "down", "down", "down", "enter", "down", "tab", "pgdn", "pgup", "tab", "esc", "esc", "down", "enter"}
					const hostileCrewStep = 6 // the "tab" that opens the inspector/Detail on the selected Crew
					assertFrameShape(t, m.View(), size.w, size.h)
					if treeName == "hostile" {
						assertNoRawControlChars(t, m.View())
					}
					for i, k := range steps {
						m, _ = send(t, m, key(k))
						assertFrameShape(t, m.View(), size.w, size.h)
						if treeName == "hostile" {
							assertNoRawControlChars(t, m.View())
						}
						if treeName == "hostile" && i == hostileCrewStep {
							assertHostileCrewAgentSurvives(t, m, layout(size.w, size.h))
						}
					}
					// A failed refresh, then a loading one, are frames too. m
					// has already completed a first load above
					// (hasLoaded=true), so this treeLoadedMsg{err} takes
					// onTreeLoaded's refresh-failed branch and stays in
					// phaseReady - it never reaches failedLines. The hostile
					// tree's failure carries the same class of hostile payload
					// as its snapshot data (CJK, an emoji, a raw SGR escape and
					// a C1 NEL) so this branch's rendering of the error text is
					// swept as well, not only a plain ASCII path string. The
					// first-load failure (failedLines itself) is swept
					// separately below, from a fixture that never loaded.
					failMsg := "read failed at " + strings.Repeat("/deep", 40)
					if treeName == "hostile" {
						failMsg = "読み込み失敗 ‼️ \x1b[31mred\x1b[0mtail " + strings.Repeat("/deep", 40)
					}
					failed, _ := send(t, m, treeLoadedMsg{err: errFake(failMsg)})
					if failed.phase != phaseReady {
						t.Fatalf("a refresh failure after a successful load left phase %v, want phaseReady", failed.phase)
					}
					assertFrameShape(t, failed.View(), size.w, size.h)
					if treeName == "hostile" {
						assertNoRawControlChars(t, failed.View())
					}
					fresh := newFixture(t, tree, size.w, size.h, g)
					fresh.phase = phaseLoading
					assertFrameShape(t, fresh.View(), size.w, size.h)
					if treeName == "hostile" {
						assertNoRawControlChars(t, fresh.View())
					}
					// The first-load failure is a different branch entirely:
					// onTreeLoaded only enters phaseFailed - the only phase
					// failedLines renders - when hasLoaded is still false,
					// which requires a fixture that never completed a load.
					// newFailedFixture builds exactly that, so this is the one
					// case in the sweep that actually reaches failedLines with
					// hostile content (a second counter-review found the
					// treeLoadedMsg{err} send above, sent to an
					// already-loaded m, never reached this branch despite the
					// comment above once claiming it did).
					if treeName == "hostile" {
						firstFailed := newFailedFixture(t, errFake(failMsg), size.w, size.h, g)
						if firstFailed.phase != phaseFailed {
							t.Fatalf("a first-load failure left phase %v, want phaseFailed", firstFailed.phase)
						}
						assertFrameShape(t, firstFailed.View(), size.w, size.h)
						assertNoRawControlChars(t, firstFailed.View())
					}
				})
			}
		}
	}
}

// assertNoRawControlChars is F3's guard that a rendered frame never carries
// a raw C0/C1 control character, DEL or ANSI escape through to the
// terminal. TestLineAddNeutralisesControlCharactersAndANSI pins that the one
// place text becomes span content (line.add) sanitises it; this pins that
// every chrome path the fix round touched - the header's workspace name and
// root, the message line's standing warning, and the failed-read screen's
// error text - still routes hostile content through that primitive rather
// than, say, a raw fmt.Sprintf that bypasses it.
func assertNoRawControlChars(t *testing.T, frame string) {
	t.Helper()
	for i, l := range strings.Split(frame, "\n") {
		if strings.ContainsAny(l, "\t\r\x1b\x07\x7f") {
			t.Fatalf("rendered line %d carries a raw control character or escape: %q", i, l)
		}
		for _, r := range l {
			if r >= 0x80 && r <= 0x9f {
				t.Fatalf("rendered line %d carries a raw C1 control U+%04X: %q", i, r, l)
			}
		}
	}
}

// assertHostileCrewAgentSurvives is F3's content-level guard: the frame
// contract (assertFrameShape) only checks that every line is exactly w
// cells, and line.render enforces that by hard-truncating an overlong line
// at the pane edge with no marker - so a wrap that mismeasures a grapheme
// cluster and overflows its value column passes the shape check while
// silently dropping the end of a value the design says the inspector never
// truncates. This reconstructs the selected Crew's "Agent" field from the
// actual rendered lines, at the actual pane width production uses
// (frame.go's pushBody renders each inspector line at l.Inspector or
// l.Cols, never at the narrower valueWidth alone), and requires the
// reconstruction to equal the source value exactly.
//
// It only fires for a Crew whose AgentName has no space or slash
// (hostileTree's second Crew: "‼️" plus a long hyphenated run): wrapAfterSlash
// only ever drops a character at a space break, so a value with neither
// makes any shortfall between source and reconstruction unambiguous - it
// can only be the render-time truncation this guard exists to catch, never
// an intentional wrap-time drop.
func assertHostileCrewAgentSurvives(t *testing.T, m Model, l frameLayout) {
	t.Helper()
	if l.TooSmall {
		return
	}
	r, ok := m.selectedRow()
	if !ok || r.kind != rowCrew {
		return
	}
	c, ok := m.crewByID(r.id)
	if !ok || c.AgentName.State != query.Known || c.AgentName.Value == "" || strings.ContainsAny(c.AgentName.Value, " /") {
		return
	}
	valueWidth, paneWidth := l.valueWidth(), l.Inspector
	if m.detail {
		valueWidth, paneWidth = l.detailValueWidth(), l.Cols
	}
	fieldLines := m.field("Agent",
		availabilitySpans(c.AgentName.State, c.AgentName.Value, c.AgentName.Reason, m.p.Fg, m.g, m.p),
		valueWidth)
	const prefix = 1 + labelWidth + 1 // pad(1) + the label column + pad(1), the same on every line field() emits
	var got strings.Builder
	for _, fl := range fieldLines {
		rendered := fl.render(paneWidth)
		runes := []rune(rendered)
		if len(runes) < prefix {
			t.Fatalf("rendered Agent field line shorter than its own label prefix: %q", rendered)
		}
		got.WriteString(strings.TrimRight(string(runes[prefix:]), " "))
	}
	if s := got.String(); s != c.AgentName.Value {
		t.Fatalf("Agent field reconstructed as %q at pane width %d (value width %d), want the full value %q - part of it was silently dropped by the inspector's render truncation",
			s, paneWidth, valueWidth, c.AgentName.Value)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

// TestFrameIsThreeLinesOfChromeAroundExactlyHMinusSixLines: the main region
// is h-6, and the six chrome lines are where the contract says they are.
func TestFrameIsThreeLinesOfChromeAroundExactlyHMinusSixLines(t *testing.T) {
	for _, size := range frameSizes {
		l := layout(size.w, size.h)
		if l.TooSmall {
			continue
		}
		t.Run(size.name, func(t *testing.T) {
			m := newFixture(t, sampleTree(), size.w, size.h, unicodeGlyphs)
			lines := strings.Split(renderFrame(t, m), "\n")
			if !strings.HasPrefix(lines[0], " matev2 console") {
				t.Errorf("line 0 = %q, want the header", lines[0])
			}
			if !strings.Contains(lines[1], "ws_acme") {
				t.Errorf("line 1 = %q, want the breadcrumb", lines[1])
			}
			if !isRule(lines[2], m.g) {
				t.Errorf("line 2 = %q, want a horizontal rule", lines[2])
			}
			if !isRule(lines[size.h-3], m.g) {
				t.Errorf("line h-3 = %q, want a horizontal rule", lines[size.h-3])
			}
			if body := size.h - chromeRows; body != l.Body {
				t.Errorf("body = %d, want %d", l.Body, body)
			}
			// Line h-2 is the message line: blank here, since nothing has
			// happened yet. Line h-1 always offers a way out.
			if strings.TrimSpace(lines[size.h-2]) != "" {
				t.Errorf("line h-2 = %q, want a blank message line on a fresh load", lines[size.h-2])
			}
			if !strings.Contains(lines[size.h-1], "q Quit") {
				t.Errorf("line h-1 = %q, want the key line", lines[size.h-1])
			}
		})
	}
}

func isRule(l string, g glyphSet) bool {
	trimmed := strings.NewReplacer(g.HRule, "", g.TeeDown, "", g.TeeUp, "").Replace(l)
	return trimmed == "" && l != ""
}

// TestSplitRulesTeeIntoTheInspectorDivider: the tee on the rules and the
// divider in the body must be the same column, or the frame looks like two
// unrelated boxes.
func TestSplitRulesTeeIntoTheInspectorDivider(t *testing.T) {
	for _, size := range []struct{ w, h int }{{160, 48}, {140, 40}, {120, 36}, {100, 30}} {
		l := layout(size.w, size.h)
		for _, g := range []glyphSet{unicodeGlyphs, asciiGlyphs} {
			m := newFixture(t, sampleTree(), size.w, size.h, g)
			lines := strings.Split(renderFrame(t, m), "\n")
			topTee := cellIndex(lines[2], g.TeeDown)
			bottomTee := cellIndex(lines[size.h-3], g.TeeUp)
			if topTee != l.List || bottomTee != l.List {
				t.Fatalf("%s at %dx%d: tees at %d and %d, want both at column %d", g.Name, size.w, size.h, topTee, bottomTee, l.List)
			}
			for i := 3; i < size.h-3; i++ {
				if got := cellAt(lines[i], l.List); got != g.VRule {
					t.Fatalf("%s at %dx%d body line %d: column %d is %q, want the divider %q", g.Name, size.w, size.h, i, l.List, got, g.VRule)
				}
			}
		}
	}
}

// TestNarrowFrameHasNoDividerAndTabOpensDetail: below 100 columns there is
// one main region, and the rules run edge to edge.
func TestNarrowFrameHasNoDividerAndTabOpensDetail(t *testing.T) {
	m := newFixture(t, sampleTree(), 80, 24, unicodeGlyphs)
	lines := strings.Split(renderFrame(t, m), "\n")
	if strings.Contains(lines[2], unicodeGlyphs.TeeDown) || strings.Contains(lines[21], unicodeGlyphs.TeeUp) {
		t.Fatalf("a single-region frame drew a tee:\n%s\n%s", lines[2], lines[21])
	}
	for i := 3; i < 21; i++ {
		if strings.Contains(lines[i], unicodeGlyphs.VRule) {
			t.Fatalf("body line %d drew a divider in a single-region frame: %q", i, lines[i])
		}
	}
	m, _ = send(t, m, key("tab"))
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "Detail view") {
		t.Fatalf("Tab at 80 columns did not open Detail:\n%s", frame)
	}
	if strings.Contains(frame, unicodeGlyphs.VRule) {
		t.Fatalf("Detail drew a divider:\n%s", frame)
	}
}

// TestTooSmallScreenNamesTheSizeAndOnlyQResponds is the fourth breakpoint.
// The size is in the message because "too small" without a number leaves
// the reader guessing how much to drag.
func TestTooSmallScreenNamesTheSizeAndOnlyQResponds(t *testing.T) {
	m := newFixture(t, sampleTree(), 56, 14, unicodeGlyphs)
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "Terminal too small: 56x14") {
		t.Fatalf("too-small frame does not name the size:\n%s", frame)
	}
	if !strings.Contains(frame, "at least 60x16") {
		t.Fatalf("too-small frame does not name the minimum:\n%s", frame)
	}
	if !strings.Contains(frame, "press q to quit") {
		t.Fatalf("too-small frame does not offer the one key that works:\n%s", frame)
	}

	before := m
	for _, k := range []string{"down", "up", "enter", "esc", "tab", "r", "pgdn", "pgup", "backspace", "j", "k"} {
		next, cmd := send(t, m, key(k))
		if cmd != nil {
			t.Fatalf("%q returned a Cmd on the too-small screen", k)
		}
		if next.cur() != before.cur() || next.focus != before.focus || next.detail != before.detail ||
			next.phase != before.phase || next.msg != before.msg {
			t.Fatalf("%q changed the model on the too-small screen: %+v -> %+v", k, before, next)
		}
	}
	quit, cmd := send(t, m, key("q"))
	if !quit.Quitting() || cmd == nil {
		t.Fatalf("q must quit from the too-small screen")
	}
}

// TestNavigationSurvivesAResizeThroughTooSmall: the too-small screen is a
// rendering decision, not a state transition. Shrinking below the minimum
// and growing back returns to the same place, so a reader who resizes their
// window does not lose their position in the tree.
func TestNavigationSurvivesAResizeThroughTooSmall(t *testing.T) {
	m := newFixture(t, sampleTree(), 160, 48, unicodeGlyphs)
	m, _ = send(t, m, key("enter")) // Project
	m, _ = send(t, m, key("down"))  // Task
	m, _ = send(t, m, key("enter")) // attempts
	m, _ = send(t, m, key("down"))  // second attempt
	want := m.stack
	wantSelID := m.cur().selID
	if wantSelID == "" {
		t.Fatalf("precondition: no selection to preserve")
	}

	m, _ = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})
	if l := layout(m.w, m.h); !l.TooSmall {
		t.Fatalf("precondition: 40x10 is not too small")
	}
	assertFrameShape(t, m.View(), 40, 10)
	if len(m.stack) != len(want) {
		t.Fatalf("the too-small screen unwound the stack: %+v", m.stack)
	}

	m, _ = send(t, m, tea.WindowSizeMsg{Width: 160, Height: 48})
	if len(m.stack) != len(want) || m.cur().selID != wantSelID {
		t.Fatalf("stack after growing back = %+v (selID %q), want %+v (selID %q)", m.stack, m.cur().selID, want, wantSelID)
	}
	if !strings.Contains(renderFrame(t, m), "CREW") {
		t.Fatalf("the inspector did not come back with the frame")
	}
}

// TestKeyLineDropsOptionalHintsThenAbbreviatesUnavailable is the design's
// overflow rule for line h-1: drop the optional hint (Tab) first, then
// shorten "(unavailable)" to "(n/a)". A word is never cut in half.
func TestKeyLineDropsOptionalHintsThenAbbreviatesUnavailable(t *testing.T) {
	m := toFailedAttempt(t, newFixture(t, sampleTree(), 120, 36, unicodeGlyphs))

	wide := m.keysLine(layout(120, 36)).render(120)
	if !strings.Contains(wide, "Tab ") || !strings.Contains(wide, "(unavailable)") {
		t.Fatalf("at 120 cols the key line should carry both the Tab hint and the full wording: %q", wide)
	}

	// A narrow-but-usable frame: the Tab hint goes first.
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 60, Height: 16})
	narrow := m.keysLine(layout(60, 16))
	rendered := narrow.render(60)
	if strings.Contains(rendered, "Tab") {
		t.Fatalf("the optional Tab hint survived an overflow: %q", rendered)
	}
	if !strings.Contains(rendered, "(n/a)") {
		t.Fatalf("the key line did not abbreviate the unavailable marker: %q", rendered)
	}
	if strings.Contains(rendered, "(unavail") {
		t.Fatalf("the key line cut a word in half: %q", rendered)
	}
	if cells(rendered) != 60 {
		t.Fatalf("key line is %d cells, want 60", cells(rendered))
	}
	// Whatever is dropped, the way out is never dropped.
	if !strings.Contains(rendered, "q Quit") {
		t.Fatalf("the key line dropped the quit hint: %q", rendered)
	}
}

// TestHeaderAlwaysSaysLiveAndHowOld: the tree now auto-refreshes
// (model.go's treeTickInterval), so "live · HH:MM:SS" is an honest claim
// rather than the redesign's former "Recorded snapshot ... As of", which
// this test used to require to keep the screen from reading as live. It
// must always say the time of the last successful read, at every width.
func TestHeaderAlwaysSaysLiveAndHowOld(t *testing.T) {
	for _, size := range frameSizes {
		if layout(size.w, size.h).TooSmall {
			continue
		}
		m := newFixture(t, sampleTree(), size.w, size.h, unicodeGlyphs)
		header := strings.Split(renderFrame(t, m), "\n")[0]
		if !strings.Contains(header, "live "+m.g.Dot+" "+goldenAsOf.Format("15:04:05")) {
			t.Fatalf("at %s the header does not say the read is live and how old it is: %q", size.name, header)
		}
	}
}

// TestCellLineRendersExactlyTheRequestedWidth covers the primitive itself,
// including a truncation that lands inside a two-cell rune - which yields a
// line one cell short unless the padding makes it up.
func TestCellLineRendersExactlyTheRequestedWidth(t *testing.T) {
	p := plainPalette()
	cases := []struct {
		name string
		line *line
		w    int
	}{
		{name: "empty", line: newLine(), w: 20},
		{name: "short", line: newLine().add("hi", p.Fg), w: 20},
		{name: "exact", line: newLine().add("12345", p.Fg), w: 5},
		{name: "overflow", line: newLine().add(strings.Repeat("x", 40), p.Fg), w: 10},
		{name: "wide runes", line: newLine().add("支払いのべき等性", p.Fg), w: 9},
		{name: "wide runes odd cut", line: newLine().add("支払いのべき等性", p.Fg), w: 7},
		{name: "many spans", line: newLine().add("a", p.Fg).add("支", p.Amber).add("bcd", p.Dim).add("因", p.Red), w: 6},
		{name: "combining mark", line: newLine().add("éclair", p.Fg), w: 4},
		{name: "selected row fill", line: selectRow(newLine(), true, p).add("row", p.Fg), w: 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cells(tc.line.render(tc.w)); got != tc.w {
				t.Fatalf("render(%d) produced %d cells: %q", tc.w, got, tc.line.render(tc.w))
			}
		})
	}
	if got := newLine().add("x", p.Fg).render(0); got != "" {
		t.Fatalf("render(0) = %q, want empty", got)
	}
}

// TestLineAddNeutralisesControlCharactersAndANSI: a newline that reached
// render() would add a real line break to the terminal - lipgloss.Width
// measures the widest line of a multi-line string, not its line count, so
// nothing downstream of add would ever notice. add is the one place text
// becomes span content, so it is the one place this is fixed.
func TestLineAddNeutralisesControlCharactersAndANSI(t *testing.T) {
	p := plainPalette()
	cases := []struct {
		name string
		in   string
	}{
		{"newline", "before\nafter"},
		{"carriage return", "before\rafter"},
		{"tab", "before\tafter"},
		{"other C0 control", "before\x07after"},
		{"del", "before\x7fafter"},
		{"ansi escape", "before\x1b[31mafter\x1b[0m"},
		// C1 sits one byte above C0 and is invisible to a check that only
		// looks below 0x20. U+0085 NEL is the case a real terminal is
		// likeliest to treat as its own line-break function while
		// lipgloss.Width still measures the string as one line - the same
		// h-lines defect this test exists to close, reopened through a
		// control range one byte higher.
		{"C1 NEL", "beforeafter"},
		{"C1 range lower bound", "beforeafter"},
		{"C1 range upper bound", "beforeafter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rendered := newLine().add(tc.in, p.Fg).render(40)
			if strings.ContainsAny(rendered, "\n\r\t\x07\x7f\x1b") {
				t.Fatalf("render(%q) = %q, still carries a raw control character or escape", tc.in, rendered)
			}
			for r := rune(0x80); r <= 0x9f; r++ {
				if strings.ContainsRune(rendered, r) {
					t.Fatalf("render(%q) = %q, still carries the C1 control U+%04X", tc.in, rendered, r)
				}
			}
			if n := len(strings.Split(rendered, "\n")); n != 1 {
				t.Fatalf("render(%q) produced %d lines, want exactly 1", tc.in, n)
			}
			if got := cells(rendered); got != 40 {
				t.Fatalf("render(%q) is %d cells, want 40", tc.in, got)
			}
			// The bytes actually sent to the terminal, not just the Go
			// string: every case here sanitises to pure ASCII (a literal
			// space replaces the one offending character), so its byte
			// length must equal its cell count exactly. A survived
			// multi-byte C1 sequence would inflate the byte count while a
			// rune- or cell-only check could still read close to 40.
			if n := len(rendered); n != 40 {
				t.Fatalf("render(%q) is %d raw bytes, want exactly 40 - a sanitised, pure-ASCII line should have byte length equal to its cell count", tc.in, n)
			}
		})
	}
}

// TestScreenEnforcesTheLineBudget: a pane that pushes too many or too few
// lines is absorbed here rather than shifting the footer.
func TestScreenEnforcesTheLineBudget(t *testing.T) {
	s := newScreen(10, 3)
	frame := s.String()
	if lines := strings.Split(frame, "\n"); len(lines) != 3 {
		t.Fatalf("an empty screen rendered %d lines, want 3", len(lines))
	}
	s = newScreen(10, 2)
	for i := 0; i < 5; i++ {
		s.push(newLine().add("line", plainPalette().Fg))
	}
	if lines := strings.Split(s.String(), "\n"); len(lines) != 2 {
		t.Fatalf("an over-filled screen rendered %d lines, want 2", len(lines))
	}
	assertFrameShape(t, s.String(), 10, 2)
}

// TestStyledLinesMeasureTheSameAsPlainOnes: the palette must not be able to
// change the geometry. Width is display cells; ANSI escapes are not cells.
func TestStyledLinesMeasureTheSameAsPlainOnes(t *testing.T) {
	for _, size := range frameSizes {
		plain := newFixture(t, sampleTree(), size.w, size.h, unicodeGlyphs)
		coloured := plain
		coloured.p = ansiPalette()
		assertFrameShape(t, plain.View(), size.w, size.h)
		assertFrameShape(t, coloured.View(), size.w, size.h)
		if plain.View() == coloured.View() && size.w >= minCols {
			t.Fatalf("at %s the coloured frame is byte-identical to the plain one; the palette is not being applied", size.name)
		}
	}
}

// TestMessageLineCarriesEveryToneAsWordsNotOnlyColour: the message line is
// the one place an error, an attention and an unknown all land, so each
// must be readable without colour. Error and attention are prefixed "! ",
// unknown "? " - the same two labels the rows use.
func TestMessageLineCarriesEveryToneAsWordsNotOnlyColour(t *testing.T) {
	l := layout(120, 36)
	base := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	cases := []struct {
		name       string
		msg        footerMsg
		wantPrefix string
	}{
		{name: "none", msg: footerMsg{}, wantPrefix: ""},
		{name: "info", msg: infoMsg("Retrying snapshot read"), wantPrefix: " Retrying"},
		{name: "ok", msg: okMsg("Detached; agent not stopped"), wantPrefix: " Detached"},
		{name: "warn", msg: footerMsg{tone: toneWarn, text: "binding stale"}, wantPrefix: " ! binding stale"},
		{name: "unknown", msg: unknownMsg("1 field unknown: worktree of crew_01J9P4"), wantPrefix: " ? 1 field unknown"},
		{name: "error", msg: errMsg("Attach refused: binding stale"), wantPrefix: " ! Attach refused"},
	}
	seen := map[string]string{}
	for _, tc := range cases {
		m := base
		m.msg = tc.msg
		got := m.messageLine(l).render(l.Cols)
		if cells(got) != l.Cols {
			t.Errorf("%s: message line is %d cells, want %d", tc.name, cells(got), l.Cols)
		}
		if tc.wantPrefix == "" {
			if strings.TrimSpace(got) != "" {
				t.Errorf("%s: message line = %q, want blank", tc.name, got)
			}
			continue
		}
		if !strings.HasPrefix(got, tc.wantPrefix) {
			t.Errorf("%s: message line = %q, want it to start with %q", tc.name, got, tc.wantPrefix)
		}
		if prev, dup := seen[strings.TrimSpace(got)]; dup {
			t.Errorf("%s and %s render the same text; the tone would be carried by colour alone", prev, tc.name)
		}
		seen[strings.TrimSpace(got)] = tc.name
	}
}

// TestInspectorNeverTruncatesAPathAndKeepsItsSuffix is the design's
// inspector rule, asserted on the rendered frame rather than only on the
// wrap helper: a worktree path is the thing a reader copies into a shell,
// so an ellipsis in it is worse than three lines of it.
func TestInspectorNeverTruncatesAPathAndKeepsItsSuffix(t *testing.T) {
	for _, size := range []struct{ w, h int }{{160, 48}, {140, 40}, {120, 36}, {100, 30}} {
		m := newFixture(t, sampleTree(), size.w, size.h, unicodeGlyphs)
		m = toFailedAttempt(t, m)
		// The inspector's richer field set does not fit every breakpoint's
		// body height without scrolling - that is the design's own Detail/
		// scroll behaviour, not truncation. Focus the inspector and scroll
		// to the bottom so Worktree and Branch, near the end of the Crew
		// block, are on screen regardless of how tall the pane is.
		m, _ = send(t, m, key("tab"))
		for i := 0; i < 40; i++ {
			m, _ = send(t, m, key("down"))
		}
		frame := renderFrame(t, m)
		want := sampleTree().Projects[0].Crews[0]
		inspector := inspectorColumn(t, frame, layout(size.w, size.h))
		if strings.Contains(inspector, unicodeGlyphs.Ellipsis) {
			t.Fatalf("at %dx%d the inspector truncated something:\n%s", size.w, size.h, inspector)
		}
		// The path wraps, so the piece that matters must land on one line
		// whole: the crew id, which is what a reader pastes into a command.
		lines := strings.Split(inspector, "\n")
		for _, whole := range []string{want.CrewID} {
			if !containsWhole(lines, whole) {
				t.Fatalf("at %dx%d the inspector split %q:\n%s", size.w, size.h, whole, inspector)
			}
		}
		// And the path reassembles to exactly what was recorded. "Worktree"
		// and "Worktree status" both start with "Worktree", so the label is
		// read from the field grid's own fixed label column (one leading
		// pad cell, then labelWidth cells) rather than matched by prefix,
		// which is what tells the field row from its own continuation lines
		// and from the next field down.
		var joined string
	reassemble:
		for _, line := range lines {
			runes := []rune(line)
			label := ""
			if len(runes) >= 1+labelWidth {
				label = strings.TrimSpace(string(runes[1 : 1+labelWidth]))
			}
			switch {
			case label == "Worktree":
				joined = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Worktree"))
			case joined != "" && label != "":
				break reassemble
			case joined != "":
				joined += strings.TrimSpace(line)
			}
		}
		if joined != want.Worktree.Value.Path {
			t.Fatalf("at %dx%d the wrapped worktree path reassembles to %q, want %q", size.w, size.h, joined, want.Worktree.Value.Path)
		}
		if !strings.Contains(inspector, want.Worktree.Value.Branch) {
			t.Fatalf("at %dx%d the inspector does not carry the whole branch %q:\n%s", size.w, size.h, want.Worktree.Value.Branch, inspector)
		}
	}
}

// inspectorColumn slices the inspector pane out of a rendered frame,
// stripping the leading and trailing padding of each line so a value that
// wrapped can be reassembled.
func inspectorColumn(t *testing.T, frame string, l frameLayout) string {
	t.Helper()
	if l.Inspector <= 0 {
		t.Fatalf("no inspector column at %dx%d", l.Cols, l.Rows)
	}
	var b strings.Builder
	for i, line := range strings.Split(frame, "\n") {
		if i < 3 || i >= l.Rows-3 {
			continue
		}
		runes := []rune(line)
		if len(runes) <= l.List+1 {
			continue
		}
		b.WriteString(strings.TrimRight(string(runes[l.List+1:]), " "))
		b.WriteString("\n")
	}
	return b.String()
}

// TestNewPicksTheGlyphSetFromTheEnvironment: the model's glyph set is
// resolved once, at construction, from the environment - so a terminal that
// cannot draw box glyphs gets the ASCII frame without anyone passing a flag.
func TestNewPicksTheGlyphSetFromTheEnvironment(t *testing.T) {
	build := func() Model {
		return New(func(context.Context) (query.Snapshot, error) { return sampleTree(), nil }, nil)
	}
	t.Setenv("LC_ALL", "en_US.UTF-8")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("MATEV2_ASCII", "")
	if got := build().g.Name; got != "unicode" {
		t.Fatalf("with a UTF-8 locale New picked %s, want unicode", got)
	}
	t.Setenv("MATEV2_ASCII", "1")
	if got := build().g.Name; got != "ascii" {
		t.Fatalf("with MATEV2_ASCII=1 New picked %s, want ascii", got)
	}
	t.Setenv("MATEV2_ASCII", "")
	t.Setenv("LC_ALL", "C")
	t.Setenv("LANG", "C")
	if got := build().g.Name; got != "ascii" {
		t.Fatalf("with a C locale New picked %s, want ascii", got)
	}

	// And both sets draw a whole frame, not just different glyphs in the
	// same one: the ASCII frame has no non-ASCII rune anywhere in its
	// chrome.
	m := newFixture(t, sampleTree(), 120, 36, asciiGlyphs)
	frame := renderFrame(t, m)
	for i, line := range strings.Split(frame, "\n") {
		if i >= 3 && i < 33 {
			continue // the body carries data, which may legitimately be any text
		}
		for _, r := range line {
			if r > 127 {
				t.Fatalf("the ascii frame's chrome line %d contains %q: %s", i, r, line)
			}
		}
	}
}

// TestHeaderKeepsTheRecordedClaimAgainstARealWorkspaceID is a regression
// from driving the real binary: a workspace id is "ws_" plus 64 hex
// characters, and at 120 columns that left two cells for the right-hand
// side of the header. The old rule dropped the right side on a collision,
// so the frame lost "Recorded snapshot" and "As of" - the two pieces of
// chrome that stop the screen reading as live - on every real workspace.
// The current wording ("live · HH:MM:SS") is short enough that it no longer
// needs to be dropped at any width this suite exercises, so this guards the
// read time and the id abbreviation rather than a claim that gets dropped.
func TestHeaderKeepsTheRecordedClaimAgainstARealWorkspaceID(t *testing.T) {
	tree := sampleTree()
	tree.WorkspaceID = "ws_cb29a22edd5f283052238d69ee94bbc95bcd19b56854333e02be8c81800ddd25"
	// Unknown, not Known: the abbreviation this test guards is exactly what
	// the header falls back to when Snapshot.Workspace did not read.
	// workspaceDisplayName prefers the real name once that field is Known,
	// which sampleTree's own Known Workspace field already exercises in the
	// other header tests.
	tree.Workspace = query.UnknownField[query.WorkspaceValue]("workspace root lookup failed")
	for _, size := range frameSizes {
		if layout(size.w, size.h).TooSmall {
			continue
		}
		m := newFixture(t, tree, size.w, size.h, unicodeGlyphs)
		lines := strings.Split(renderFrame(t, m), "\n")
		if !strings.Contains(lines[0], "live "+m.g.Dot+" "+goldenAsOf.Format("15:04:05")) {
			t.Errorf("at %s a 67-character workspace id pushed the live claim off the header:\n%q", size.name, lines[0])
		}
		if !strings.Contains(lines[1], "project") {
			t.Errorf("at %s the breadcrumb lost its level count:\n%q", size.name, lines[1])
		}
		// The id is abbreviated head-and-tail rather than cut, so it is still
		// recognisable and still distinguishes two workspaces.
		if !strings.Contains(lines[0], "ws_cb29a2") || !strings.Contains(lines[0], "dd25") {
			t.Errorf("at %s the workspace id is neither whole nor abbreviated head-and-tail:\n%q", size.name, lines[0])
		}
	}
}

// TestCutMarksTheCutAndKeepsTheStyleOfWhatItReplaced covers the primitive
// leftRight now leans on.
func TestCutMarksTheCutAndKeepsTheStyleOfWhatItReplaced(t *testing.T) {
	p := ansiPalette()
	l := newLine().add("head ", p.Dim).add("tail-that-overflows", p.Amber)
	got := l.cut(12, unicodeGlyphs)
	if n := got.width(); n != 12 {
		t.Fatalf("cut(12) is %d cells, want 12", n)
	}
	rendered := got.render(12)
	if !strings.Contains(rendered, unicodeGlyphs.Ellipsis) {
		t.Fatalf("cut did not mark the cut: %q", rendered)
	}
	if !strings.Contains(rendered, p.Amber.Render(unicodeGlyphs.Ellipsis)) {
		t.Fatalf("the ellipsis did not inherit the style of the span it replaced: %q", rendered)
	}
	// A line that already fits is returned untouched, marker and all.
	short := newLine().add("fits", p.Fg)
	if got := short.cut(12, unicodeGlyphs).render(12); strings.Contains(got, unicodeGlyphs.Ellipsis) {
		t.Fatalf("cut marked a line that did not need cutting: %q", got)
	}
	if got := newLine().add("x", p.Fg).cut(0, unicodeGlyphs).render(4); got != "    " {
		t.Fatalf("cut(0) rendered %q, want blanks", got)
	}
}
