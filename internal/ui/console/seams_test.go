package console

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// fieldValueText renders inspectorLines' output at pane width w and returns
// each field's full value text (its head line plus any wrapped continuation
// lines, space-joined) keyed by field label, so a test can assert on one
// named field without hard-coding line indices that would drift as the
// inspector's field set grows, and without the assertion breaking the
// moment a value happens to wrap onto a second line. A blank line (label
// and value both empty) ends the current field, so a field's own value
// never absorbs an unrelated note two fields further down.
func fieldValueText(lines []*line, w int) map[string]string {
	const prefix = 1 + labelWidth + 1
	fields := map[string]string{}
	current := ""
	for _, ln := range lines {
		rendered := ln.render(w)
		if len(rendered) < prefix {
			current = ""
			continue
		}
		label := strings.TrimSpace(rendered[1 : 1+labelWidth])
		value := strings.TrimSpace(rendered[prefix:])
		if strings.TrimSpace(rendered) == "" {
			current = ""
			continue
		}
		if label != "" {
			current = label
			fields[current] = value
			continue
		}
		if current != "" {
			fields[current] = strings.TrimSpace(fields[current] + " " + value)
		}
	}
	return fields
}

// TestInspectorRendersAnUnknownMateAsUnknownNotAbsent is F1's regression
// case: mateRowID returns the same "mate:none" sentinel for a Project with
// no designated Mate (Absent) and for one whose designation read failed
// (Unknown) - it exists only to give the row a stable identity across a
// refresh, not to be shown as a fact. Before the fix, the inspector's ID
// field rendered that sentinel through the plain textField path regardless
// of state, and Harness read the zero HarnessKind off an unknown
// MateIdentity and printed nothing - both asserting things the read never
// established. ID and Harness now route through the same Designated state
// the Recorded status field already used, so all three read as unknown,
// with the reason, and never as the row-identity sentinel.
func TestInspectorRendersAnUnknownMateAsUnknownNotAbsent(t *testing.T) {
	const reason = "ListMates timed out"
	tree := query.Snapshot{
		WorkspaceID: "ws_1",
		Projects: []query.ProjectNode{
			{ProjectID: "proj_1", Name: "acme", Mate: unknownMate(reason)},
		},
	}
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter"))
	r, ok := m.selectedRow()
	if !ok || r.kind != rowMate {
		t.Fatalf("selected row = %+v, ok=%v, want the Mate row", r, ok)
	}
	l := layout(m.w, m.h)
	fields := fieldValueText(m.inspectorLines(l.Inspector, l.valueWidth(), l.Body, true), l.Inspector)
	for _, label := range []string{"ID", "Recorded status", "Harness"} {
		val, ok := fields[label]
		if !ok {
			t.Fatalf("no %q field rendered; fields = %v", label, fields)
		}
		if strings.Contains(val, "mate:none") {
			t.Fatalf("%q field renders the internal row-identity sentinel %q as fact: %q", label, "mate:none", val)
		}
		if !strings.HasPrefix(val, "unknown") {
			t.Fatalf("%q field = %q, want it to say unknown - the read failed, it never established there is no Mate", label, val)
		}
		if !strings.Contains(val, reason) {
			t.Fatalf("%q field = %q, want the read failure's own reason %q", label, val, reason)
		}
	}
}

// TestInspectorRendersAnAbsentMateAsNoneNotUnknown is the Unknown case's
// contrast: a Project that was read successfully and genuinely has no
// designated Mate must still say "none", not "unknown" - the fix for F1
// must not collapse the two states into one rendering.
func TestInspectorRendersAnAbsentMateAsNoneNotUnknown(t *testing.T) {
	const reason = "this project has no designated Mate"
	tree := query.Snapshot{
		WorkspaceID: "ws_1",
		Projects: []query.ProjectNode{
			{ProjectID: "proj_1", Name: "acme", Mate: absentMate(reason)},
		},
	}
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	m, _ = send(t, m, key("enter"))
	r, ok := m.selectedRow()
	if !ok || r.kind != rowMate {
		t.Fatalf("selected row = %+v, ok=%v, want the Mate row", r, ok)
	}
	l := layout(m.w, m.h)
	fields := fieldValueText(m.inspectorLines(l.Inspector, l.valueWidth(), l.Body, true), l.Inspector)
	for _, label := range []string{"ID", "Harness"} {
		val, ok := fields[label]
		if !ok {
			t.Fatalf("no %q field rendered; fields = %v", label, fields)
		}
		if !strings.HasPrefix(val, "none") {
			t.Fatalf("%q field = %q, want it to say none - the read succeeded and found no Mate", label, val)
		}
		if strings.HasPrefix(val, "unknown") {
			t.Fatalf("%q field = %q, an Absent Mate must not render as Unknown", label, val)
		}
	}
}
