package console

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// bxDetail is the detail pane's rendered lines, trimmed.
func bxDetail(t *testing.T, m Model) string {
	t.Helper()
	var b strings.Builder
	for _, l := range m.detailLines(m.plan()) {
		b.WriteString(strings.TrimRight(l.render(m.w, plainPalette()), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func bxWants(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s is missing %q:\n%s", what, w, got)
		}
	}
}

func bxDenies(t *testing.T, what, got string, denies ...string) {
	t.Helper()
	for _, w := range denies {
		if strings.Contains(got, w) {
			t.Errorf("%s carries %q:\n%s", what, w, got)
		}
	}
}

// The Mate's detail: its agent, harness as mark and word, status and age,
// repo, crews, tokens, mode, what waits on the captain, its last event.
func TestMateDetailShowsWhatTheSnapshotRecords(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	d := bxDetail(t, m)
	bxWants(t, "Mate detail", d,
		"agent    payments-api.mate", "harness  ✻ claude", "status   running · 38m",
		"repo     ~/src/payments-api", "crews    2 live · 1 completed", "tokens   182k",
		"mode     manual", "blocked  no · 1 crew waits on you", "last     status · 1m")
	// An active binding is not news; only a problem binding is shown.
	bxDenies(t, "Mate detail", d, "binding")
	if title, _ := m.detailTitle(); bxText(title) != "👨‍💻 payments-api" {
		t.Errorf("detail title = %q", bxText(title))
	}
}

// A stale, absent or unreadable binding is why Enter refuses, so detail
// says it.
func TestDetailShowsABindingOnlyWhenItIsAProblem(t *testing.T) {
	for _, tc := range []struct {
		name    string
		binding query.Field[query.BindingValue]
		want    string
	}{
		{"stale", query.KnownField(query.BindingValue{Status: query.BindingStale}), "binding  stale"},
		{"absent", query.AbsentField[query.BindingValue]("released"), "binding  none"},
		{"unknown", query.UnknownField[query.BindingValue]("read timed out"), "binding  unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := designTree()
			tree.Projects[7].Mate.Binding = tc.binding
			m := newFixture(t, tree, 40, 36, unicodeGlyphs)
			for i := 0; i < 7; i++ {
				m, _ = send(t, m, key("down"))
			}
			m, _ = send(t, m, key("enter"))
			bxWants(t, "Mate detail", bxDetail(t, m), tc.want)
		})
	}
}

// A Crew's detail: its id head…tail, harness, status and age, why it
// needs the captain, the Mate it answers to, its branch and repo.
func TestCrewDetailShowsWhyItWaits(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down")) // k3, waiting on a decision
	d := bxDetail(t, m)
	bxWants(t, "crew detail", d,
		"agent    k3", "harness  ✻ claude", "status   needs-decision · 33m",
		"blocked  asked a question and stopped", "mate     👨‍💻 payments-api",
		"branch   crew/k3", "repo     ~/src/payments-api")
	if title, _ := m.detailTitle(); bxText(title) != "🤖 Backfill ledger v2" {
		t.Errorf("detail title = %q", bxText(title))
	}
	// A crew that needs nothing has no blocked field.
	m, _ = send(t, m, key("up"))
	bxDenies(t, "working crew detail", bxDetail(t, m), "blocked")
}

// A failed crew's reason is red, and its error is written out.
func TestAFailedCrewSaysWhy(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("enter")) // open Completed
	m, _ = send(t, m, key("down"))  // k1, failed
	if r, _ := m.selectedRow(); r.id != "k1" {
		t.Fatalf("setup: selection %+v, want k1", r)
	}
	d := bxDetail(t, m)
	bxWants(t, "failed crew detail", d, "status   failed", "blocked  failed: exit 1", "error    exit 1")
	for _, l := range m.detailLines(m.plan()) {
		if strings.Contains(bxText(l), "error") {
			red := false
			for _, s := range l.segs {
				red = red || (s.t == tRed && strings.Contains(s.text, "exit 1"))
			}
			if !red {
				t.Errorf("the error value is not red: %+v", l.segs)
			}
		}
	}
}

// A Project, seen from the workspace: its Mate, crews by state, finished
// count, what waits, mode and repo.
func TestProjectPeekSummarisesTheProject(t *testing.T) {
	m := newFixture(t, designTree(), 40, 36, unicodeGlyphs)
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	d := bxDetail(t, m)
	bxWants(t, "project peek", d,
		"mate      ✻ running · 38m", "crews     1 working · 1 decide", "completed 1",
		"box       1 waiting on you", "mode      manual", "repo      ~/src/payments-api")
	_, meta := m.detailTitle()
	if meta != "project" {
		t.Errorf("peek rule meta = %q, want project", meta)
	}
}

// A Project without a Mate says so, and one whose Mate could not be read
// says unknown - never a guess.
func TestKnownAbsentAndUnknownAreThreeDifferentWords(t *testing.T) {
	tree := designTree()
	tree.Projects[1].Repos = query.UnknownField[[]query.RepoValue]("project.yaml unreadable")
	tree.Projects[2].Repos = query.KnownField([]query.RepoValue{})
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	pick := func(i int) string {
		mm := m
		for j := 0; j < i; j++ {
			mm, _ = send(t, mm, key("down"))
		}
		return bxDetail(t, mm)
	}
	// None of these has finished crews, so the label column is nine wide.
	bxWants(t, "no-mate peek", pick(3), "mate     no mate")
	bxWants(t, "unknown-mate peek", pick(8), "mate     ✻ unknown")
	bxWants(t, "unreadable repos", pick(1), "repo     unknown", "project.yaml unreadable")
	bxWants(t, "no repos", pick(2), "repo     none yet", "mate project repo add")
}

// The Completed group's detail counts what finished and what failed.
func TestCompletedDetailCountsTheGroup(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	for i := 0; i < 3; i++ {
		m, _ = send(t, m, key("down"))
	}
	if r, _ := m.selectedRow(); r.kind != rowCompletedGroup {
		t.Fatalf("setup: selection %+v, want Completed", r)
	}
	bxWants(t, "Completed detail", bxDetail(t, m), "crews    0 finished", "failed   1")
}

// Rows draw the harness as its mark, never its name; detail keeps the word
// beside the mark.
func TestRowsDrawTheHarnessAsItsMark(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	lines := bxLines(t, m)
	list, _ := m.plan().slot(slotList)
	for _, l := range lines[list.top : list.top+list.h] {
		if strings.Contains(l, "claude") || strings.Contains(l, "codex") {
			t.Errorf("a list row names its harness: %q", l)
		}
	}
	if !strings.Contains(renderFrame(t, m), "     ✻ running") || !strings.Contains(renderFrame(t, m), "     ⌬ working") {
		t.Fatalf("rows do not carry the harness marks:\n%s", renderFrame(t, m))
	}
}

// Every row keeps the design's columns: marker in col 0, kind in cols 2-3,
// the name from col 5, line 2 under the name, ! at col 36 at 40 columns.
func TestRowAnatomyKeepsItsColumns(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 36)
	l := bxLineWith(t, m, "Backfill ledger v2  ")
	if got := cellIndex(l, "🤖"); got != 2 {
		t.Errorf("kind at col %d, want 2: %q", got, l)
	}
	if got := cellIndex(l, "Backfill"); got != 5 {
		t.Errorf("name at col %d, want 5: %q", got, l)
	}
	if got := cellIndex(l, "!"); got != 36 {
		t.Errorf("! at col %d, want 36: %q", got, l)
	}
	if got := cellIndex(bxLineWith(t, m, "needs-decision"), "✻"); got != 5 {
		t.Errorf("line 2 starts at col %d, want 5", got)
	}
}

// Under 30 rows a row is one line: name, the ! column at 31 and a
// seven-cell status word at 33-39 (design E).
func TestOneLineRowsAtShortHeights(t *testing.T) {
	m, _, _ := bxPayments(t, 40, 24)
	l := bxLineWith(t, m, "Backfill ledger v2")
	if got := cellIndex(l, "!"); got != 31 {
		t.Errorf("! at col %d, want 31: %q", got, l)
	}
	if got := cellIndex(l, "decide"); got != 33 {
		t.Errorf("status at col %d, want 33: %q", got, l)
	}
	if strings.Contains(renderFrame(t, m), "✻ needs-decision") {
		t.Error("a short frame still draws two-line rows")
	}
}

// Long names end in the ellipsis and never push the right column.
func TestLongNamesAreCutNotWrapped(t *testing.T) {
	m := newFixture(t, designTree(), 40, 36, unicodeGlyphs)
	l := bxLineWith(t, m, "customer-notifications")
	if !strings.Contains(l, "…") || cells(l) != 40 {
		t.Fatalf("long workspace name = %q (%d cells), want it cut to fit", l, cells(l))
	}
}

// A crew spawned with a profile shows it in detail, beside its harness;
// one spawned without shows neither field rather than an empty one.
func TestCrewDetailShowsTheLaunchProfile(t *testing.T) {
	tree := designTree()
	for i := range tree.Projects {
		if tree.Projects[i].Name != "payments-api" {
			continue
		}
		tree.Projects[i].Crews[0].Model = "gpt-5.5"
		tree.Projects[i].Crews[0].Effort = "high"
	}
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	m, _ = send(t, m, key("enter")) // payments-api
	m, _ = send(t, m, key("down"))  // the codex crew
	frame := renderFrame(t, m)
	for _, want := range []string{"model    gpt-5.5", "effort   high"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("detail lacks %q:\n%s", want, frame)
		}
	}
	m, _ = send(t, m, key("down")) // the claude crew, no profile
	if frame := renderFrame(t, m); strings.Contains(frame, "model ") || strings.Contains(frame, "effort ") {
		t.Fatalf("a crew with no profile shows model/effort fields:\n%s", frame)
	}
}
