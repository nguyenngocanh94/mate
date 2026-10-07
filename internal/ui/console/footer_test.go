package console

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The status line (design I, "6"): what the next pane shows, or the last
// thing that happened when there is something to say.

func warning(field, rowLabel, reason string) query.FieldWarning {
	return query.FieldWarning{Field: field, Row: query.RowRef{Label: rowLabel}, Reason: reason}
}

// The standing unknown-field line is built only from query.FieldWarning:
// correct singular/plural, and the first warning's own field, row, reason.
func TestWarningsFooterMsgNamesCountFieldRowAndReason(t *testing.T) {
	cases := []struct {
		name     string
		warnings []query.FieldWarning
		want     footerMsg
	}{
		{name: "none", want: footerMsg{}},
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := warningsFooterMsg(tc.warnings); got != tc.want {
				t.Fatalf("warningsFooterMsg = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// An explicit message wins over the standing warning, and clearing it
// brings the warning back rather than leaving the line blank.
func TestFooterMessageExplicitMessageWinsOverWarnings(t *testing.T) {
	tree := sampleTree()
	tree.Warnings = []query.FieldWarning{warning("worktree", "attempt 2", "lookup timed out (2s)")}
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	if got := m.footerMessage(); got.tone != toneUnknown {
		t.Fatalf("with no explicit message the line must fall back to the warning, got %+v", got)
	}
	if !strings.Contains(renderFrame(t, m), "? 1 field unknown") {
		t.Fatalf("the warning is not on the status line with its ? mark:\n%s", renderFrame(t, m))
	}
	m.msg = errMsg("Show refused: binding stale")
	if got := m.footerMessage(); got != m.msg {
		t.Fatalf("an explicit message must win: got %+v", got)
	}
	m.msg = footerMsg{}
	if got := m.footerMessage(); got.tone != toneUnknown {
		t.Fatalf("after the message clears the warning must reappear, got %+v", got)
	}
}

// The observer's standing runtime notice is a warn line and outranks the
// loader's own unknown-field line: while Herdr cannot be reached, the field
// warnings are describing a picture nobody can refresh, and the reader's
// first need is to know that. It is absent from the ordinary snapshot, and
// the field warning comes back the moment it is cleared.
func TestFooterMessageShowsTheRuntimeNotice(t *testing.T) {
	tree := sampleTree()
	tree.Warnings = []query.FieldWarning{warning("worktree", "attempt 2", "lookup timed out (2s)")}
	tree.Runtime = query.RuntimeStatus{Notice: "herdr is not running; start or resume the Mate to bring it back"}
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)

	got := m.footerMessage()
	if got.tone != toneWarn || !strings.Contains(got.text, "herdr is not running") {
		t.Fatalf("footerMessage = %+v, want the runtime notice as a warning", got)
	}
	if !strings.Contains(renderFrame(t, m), "! herdr is not running") {
		t.Fatalf("the runtime notice is not on the status line with its ! mark:\n%s", renderFrame(t, m))
	}

	// Clearing it in the snapshot brings the field warning back, rather
	// than leaving the line blank.
	tree.Runtime = query.RuntimeStatus{}
	cleared := newFixture(t, tree, 120, 36, unicodeGlyphs)
	if clearedNotice := cleared.footerMessage(); clearedNotice.tone != toneUnknown || !strings.Contains(clearedNotice.text, "1 field unknown") {
		t.Fatalf("footerMessage after recovery = %+v, want the field warning back", clearedNotice)
	}
}

// With nothing to say, the status line says what the next pane shows.
func TestStatusLineSaysNothingIsShownBeforeTheFirstStage(t *testing.T) {
	m := newFixture(t, sampleTree(), 40, 36, unicodeGlyphs).WithStage(func(_ context.Context, _ StageTarget) error { return nil })
	if got := m.footerMessage(); got != (footerMsg{}) {
		t.Fatalf("with no message and no warnings the message must be blank, got %+v", got)
	}
	if !strings.Contains(renderFrame(t, m), "→ next pane · nothing shown") {
		t.Fatalf("status line before any stage:\n%s", renderFrame(t, m))
	}
}

// Without a host there is no next pane, and the line says so.
func TestStatusLineSaysThereIsNoHost(t *testing.T) {
	m := newFixture(t, sampleTree(), 40, 36, unicodeGlyphs)
	if !strings.Contains(renderFrame(t, m), "→ next pane · no host") {
		t.Fatalf("status line without a StageFunc:\n%s", renderFrame(t, m))
	}
}

// A successful stage names what the next pane now shows; a failure turns
// the line red with r to retry, and r stages the same target again.
func TestStatusLineFollowsTheStageAndRRetriesAFailure(t *testing.T) {
	calls := 0
	fail := true
	stage := func(_ context.Context, _ StageTarget) error {
		calls++
		if fail {
			return errors.New("no pane")
		}
		return nil
	}
	m := projectFrame(t, sampleTree()).WithStage(stage)
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter on the Mate did not ask the host")
	}
	m, _ = send(t, m, cmd())
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "→ next pane · failed: no pane") || !strings.Contains(frame, "r retry") {
		t.Fatalf("status line after a failed stage:\n%s", frame)
	}

	fail = false
	m, cmd = send(t, m, key("r"))
	if cmd == nil {
		t.Fatal("r after a failed stage must retry it")
	}
	m, _ = send(t, m, cmd())
	if calls != 2 {
		t.Fatalf("stage calls = %d, want 2", calls)
	}
	if !strings.Contains(renderFrame(t, m), "→ next pane  👨‍💻 payments-api") {
		t.Fatalf("status line after a successful retry:\n%s", renderFrame(t, m))
	}
	m, cmd = send(t, m, key("r"))
	if cmd == nil || !m.treeLoadInFlight {
		t.Fatal("once the stage succeeded, r is a refresh again")
	}
}

// A crew on stage is named by its task.
func TestStatusLineNamesAStagedCrewByItsTask(t *testing.T) {
	// query.Load sets every crew's ProjectID (load.go); sampleTree leaves
	// it out.
	tree := sampleTree()
	for i := range tree.Projects[0].Crews {
		tree.Projects[0].Crews[i].ProjectID = tree.Projects[0].ProjectID
	}
	m := toRunningAttempt(t, loaded(t, tree, nil)).WithStage(func(_ context.Context, _ StageTarget) error { return nil })
	m, cmd := send(t, m, key("enter"))
	m, _ = send(t, m, cmd())
	if !strings.Contains(renderFrame(t, m), "→ next pane  🤖 Add idempotency-key") {
		t.Fatalf("status line after staging the crew:\n%s", renderFrame(t, m))
	}
}

// A short frame ends its status line in "? keys", and ? shows the key list
// until the next key.
func TestShortFrameOffersTheKeyList(t *testing.T) {
	m := newFixture(t, sampleTree(), 40, 24, unicodeGlyphs)
	lines := strings.Split(renderFrame(t, m), "\n")
	if !strings.HasSuffix(strings.TrimRight(lines[len(lines)-1], " "), "? keys") {
		t.Fatalf("status line at 24 rows = %q, want it to end in ? keys", lines[len(lines)-1])
	}
	m, _ = send(t, m, key("?"))
	if !strings.Contains(renderFrame(t, m), "─ keys ") {
		t.Fatalf("? did not open the key list:\n%s", renderFrame(t, m))
	}
	m, _ = send(t, m, key("j"))
	if m.keysOpen || m.cur().sel != 0 {
		t.Fatalf("the next key must close the list and do nothing else: open=%v sel=%d", m.keysOpen, m.cur().sel)
	}
}

// A tall frame's key line names the keys of the pane that has the keyboard.
func TestKeyLineFollowsFocus(t *testing.T) {
	m := projectFrame(t, sampleTree())
	last := func(m Model) string {
		lines := strings.Split(renderFrame(t, m), "\n")
		return lines[len(lines)-1]
	}
	if l := last(m); !strings.Contains(l, "enter show") || !strings.Contains(l, "a act") {
		t.Fatalf("list key line = %q", l)
	}
	m, _ = send(t, m, key("tab"))
	if l := last(m); !strings.Contains(l, "y copy") || !strings.Contains(l, "esc list") {
		t.Fatalf("detail key line = %q", l)
	}
	m, _ = send(t, m, key("tab"))
	if l := last(m); !strings.Contains(l, "a assign") {
		t.Fatalf("box key line = %q", l)
	}
}

// Recovery's line (M17) is the status line while it runs and after it: the
// progress is info, a clean summary is green, one that names a failure is a
// warning. It outranks the runtime notice, which is the thing it is fixing,
// and an explicit message still wins over it.
func TestFooterMessageShowsRecovery(t *testing.T) {
	tree := sampleTree()
	tree.Runtime = query.RuntimeStatus{Notice: "herdr is not running; start or resume the Mate to bring it back"}
	cases := []struct {
		name string
		r    query.RecoveryStatus
		want footerMsg
	}{
		{"running", query.RecoveryStatus{Line: "recovering 1 of 3…", Active: true}, infoMsg("recovering 1 of 3…")},
		{"done", query.RecoveryStatus{Line: "recovered 3"}, okMsg("recovered 3")},
		{"failed", query.RecoveryStatus{Line: "recovered 2; 1 failed: crew shop/k4: gone", Failed: true}, warnMsg("recovered 2; 1 failed: crew shop/k4: gone")},
	}
	for _, tc := range cases {
		tree.Recovery = tc.r
		m := newFixture(t, tree, 120, 36, unicodeGlyphs)
		if got := m.footerMessage(); got != tc.want {
			t.Fatalf("%s: footerMessage = %+v, want %+v", tc.name, got, tc.want)
		}
	}
	tree.Recovery = query.RecoveryStatus{Line: "recovering 1 of 3…", Active: true}
	m := newFixture(t, tree, 120, 36, unicodeGlyphs)
	if !strings.Contains(renderFrame(t, m), "recovering 1 of 3…") {
		t.Fatalf("recovery is not on the status line:\n%s", renderFrame(t, m))
	}
	m.msg = errMsg("Show refused: binding stale")
	if got := m.footerMessage(); got != m.msg {
		t.Fatalf("an explicit message must win over recovery: got %+v", got)
	}
}
