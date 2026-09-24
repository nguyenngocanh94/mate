package console

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// warning is a small constructor for the table below, keeping each case to
// one line.
func warning(field, rowLabel, reason string) query.FieldWarning {
	return query.FieldWarning{Field: field, Row: query.RowRef{Label: rowLabel}, Reason: reason}
}

// TestWarningsFooterMsgNamesCountFieldRowAndReason is the standing
// unknown-field line, built only from query.FieldWarning: never invented
// wording, correct singular/plural, and the first warning's own field, row
// and reason when there is more than one.
func TestWarningsFooterMsgNamesCountFieldRowAndReason(t *testing.T) {
	cases := []struct {
		name     string
		warnings []query.FieldWarning
		want     footerMsg
	}{
		{name: "none", warnings: nil, want: footerMsg{}},
		{
			name:     "one",
			warnings: []query.FieldWarning{warning("worktree", "attempt 2", "lookup timed out (2s)")},
			want:     unknownMsg("1 field unknown: worktree of attempt 2 (lookup timed out (2s))"),
		},
		{
			name: "two, names the first",
			warnings: []query.FieldWarning{
				warning("worktree", "attempt 2", "lookup timed out (2s)"),
				warning("last event", "attempt 1", "store closed"),
			},
			want: unknownMsg("2 fields unknown; first: worktree of attempt 2 (lookup timed out (2s))"),
		},
		{
			name: "three",
			warnings: []query.FieldWarning{
				warning("binding", "mate of payments-api", "timeout"),
				warning("worktree", "attempt 2", "lookup timed out (2s)"),
				warning("last event", "attempt 1", "store closed"),
			},
			want: unknownMsg("3 fields unknown; first: binding of mate of payments-api (timeout)"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := warningsFooterMsg(tc.warnings); got != tc.want {
				t.Fatalf("warningsFooterMsg(%+v) = %+v, want %+v", tc.warnings, got, tc.want)
			}
		})
	}
}

// TestFooterMessageExplicitMessageWinsOverWarnings: an action's own message
// (attach refused, retry, detach) always takes the line over the standing
// warning - the design's "cho tới khi có message khác" (until there is
// another message).
func TestFooterMessageExplicitMessageWinsOverWarnings(t *testing.T) {
	tree := sampleTree()
	tree.Warnings = []query.FieldWarning{warning("worktree", "attempt 2", "lookup timed out (2s)")}
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)

	if got := m.footerMessage(); got.tone != toneUnknown {
		t.Fatalf("with no explicit message the footer must fall back to the warning, got %+v", got)
	}

	m.msg = errMsg("Attach refused: binding stale")
	if got := m.footerMessage(); got != m.msg {
		t.Fatalf("an explicit message must win over the standing warning: got %+v, want %+v", got, m.msg)
	}

	// Clearing the explicit message (as every navigation key does, see
	// update.go) must bring the warning back rather than leaving the line
	// blank - the field is still Unknown until a refresh resolves it.
	m.msg = footerMsg{}
	if got := m.footerMessage(); got.tone != toneUnknown {
		t.Fatalf("after the explicit message is cleared the warning must reappear, got %+v", got)
	}
}

// TestFooterMessageIgnoresWarningsBeforeTheFirstLoad: Warnings belongs to a
// completed read (query.Snapshot). Before phaseReady there is no snapshot to
// have warned about, whatever the zero-valued tree happens to carry.
func TestFooterMessageIgnoresWarningsBeforeTheFirstLoad(t *testing.T) {
	tree := sampleTree()
	tree.Warnings = []query.FieldWarning{warning("worktree", "attempt 2", "lookup timed out (2s)")}
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	m.phase, m.hasLoaded = phaseLoading, false
	if got := m.footerMessage(); got != (footerMsg{}) {
		t.Fatalf("a loading screen must not show a warning line, got %+v", got)
	}
}

// TestFooterMessageBlankWithNoWarnings: the common case, asserted directly
// rather than only implied by the other tests above.
func TestFooterMessageBlankWithNoWarnings(t *testing.T) {
	m := newFixture(t, sampleTree(), 120, 36, unicodeGlyphs)
	if got := m.footerMessage(); got != (footerMsg{}) {
		t.Fatalf("with no explicit message and no warnings the footer must be blank, got %+v", got)
	}
}

// TestKeyLineNeverAdvertisesAnInertKey: an empty list has no selected row, so
// Enter (open/attach) and Tab (inspector/Detail toggle for the selected row)
// must both be gone from the key line rather than promising an action Enter
// or Tab cannot perform. q, r and (once there is somewhere to go back to)
// Esc remain, since those never depend on a selection.
func TestKeyLineNeverAdvertisesAnInertKey(t *testing.T) {
	m := newFixture(t, query.Snapshot{WorkspaceID: "ws_acme"}, 120, 36, unicodeGlyphs)
	if _, ok := m.selectedRow(); ok {
		t.Fatalf("precondition: an empty workspace must have no selected row")
	}
	line := m.keysLine(layout(120, 36)).render(120)
	for _, dead := range []string{"Enter", "Tab"} {
		if strings.Contains(line, dead) {
			t.Fatalf("key line %q advertises %q with nothing selected to act on", line, dead)
		}
	}
	for _, live := range []string{"r Refresh", "q Quit"} {
		if !strings.Contains(line, live) {
			t.Fatalf("key line %q dropped the always-live hint %q", line, live)
		}
	}

	// Once something is selected, Enter comes back; Tab only comes back
	// once there is somewhere for it to go (an inspector column, or - below
	// 100 columns - Detail), neither of which this empty tree has past the
	// Workspace level, so it is not asserted "on" here.
	tree := sampleTree()
	withRow := newFixture(t, tree, 120, 36, unicodeGlyphs)
	if _, ok := withRow.selectedRow(); !ok {
		t.Fatalf("precondition: sampleTree's Workspace level must have a selected row")
	}
	if line := withRow.keysLine(layout(120, 36)).render(120); !strings.Contains(line, "Enter") {
		t.Fatalf("key line %q dropped Enter with a row selected", line)
	}
}

// TestKeyLineDropsOnlyAsManyOptionalHintsAsItMustIsThePolicy: the key line
// is the only place some keys are ever named, so narrowing it must cost the
// reader the least it can. Dropping every optional hint as a group loses
// hints the line still had room for - which is what adding "n New project"
// exposed: an 80-column workspace lost "a Actions", the gateway to every
// other action, with 7 cells to spare.
func TestKeyLineDropsOnlyAsManyOptionalHintsAsItMustIsThePolicy(t *testing.T) {
	m := newFixture(t, sampleTree(), 80, 24, unicodeGlyphs)
	line := m.keysLine(layout(80, 24))
	rendered := line.render(80)
	if line.width() > 80 {
		t.Fatalf("key line is %d cells at 80 columns:\n%s", line.width(), rendered)
	}
	for _, want := range []string{"a Actions", "n New project", "Enter Open project", "r Refresh", "q Quit"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("80-column key line dropped %q with room to spare:\n%s", want, rendered)
		}
	}
	// Tab is the one that had to go: it is optional and last.
	if strings.Contains(rendered, "Tab ") {
		t.Fatalf("80-column key line kept every hint; the fixture no longer exercises dropping:\n%s", rendered)
	}
}

func TestDropLastOptionalHintRemovesOneAtATimeFromTheEnd(t *testing.T) {
	hints := []keyHint{
		{key: "a", desc: "Actions", optional: true},
		{key: "Enter", desc: "Open project"},
		{key: "Tab", desc: "Detail", optional: true},
		{key: "q", desc: "Quit"},
	}
	next, ok := dropLastOptional(hints)
	if !ok {
		t.Fatal("dropLastOptional found no optional hint to drop")
	}
	if got := keysOf(next); got != "a,Enter,q" {
		t.Fatalf("after one drop = %s, want the last optional gone", got)
	}
	next, ok = dropLastOptional(next)
	if !ok || keysOf(next) != "Enter,q" {
		t.Fatalf("after two drops = %s ok=%v, want only the required hints", keysOf(next), ok)
	}
	if _, ok := dropLastOptional(next); ok {
		t.Fatal("dropLastOptional reported a drop with no optional hints left")
	}
}

func keysOf(hints []keyHint) string {
	out := make([]string, 0, len(hints))
	for _, h := range hints {
		out = append(out, h.key)
	}
	return strings.Join(out, ",")
}
