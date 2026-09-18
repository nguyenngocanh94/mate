package box_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// newFixtureWorkspace builds a workspace with one project "shop" and two
// crews, k3 and k9, through store, the way a real workspace would be
// populated: AddProject creates the directories, AppendStatus and
// AppendSent do the writing. Nothing here reaches around store.
func newFixtureWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := store.Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{Repo: "shop"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

func TestLoadMergesStatusAndSentByTime(t *testing.T) {
	w := newFixtureWorkspace(t)

	if err := w.AppendSent("shop", store.SentEntry{
		Time:   time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
		Source: store.SourceUser, Target: store.TargetMate, Text: "spawn k3",
	}); err != nil {
		t.Fatalf("AppendSent: %v", err)
	}
	if err := w.AppendStatus("shop", "k3", "working: reading the ticket"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	if err := w.AppendSent("shop", store.SentEntry{
		Time:   time.Date(2026, 9, 17, 10, 5, 0, 0, time.UTC),
		Source: store.SourceMate, Target: store.CrewTarget("k3"), Text: "use SQLite",
	}); err != nil {
		t.Fatalf("AppendSent: %v", err)
	}
	if err := w.AppendStatus("shop", "k3", "needs-decision: use SQLite or Postgres?"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	if err := w.AppendStatus("shop", "k9", "done: PR ready"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}

	v, err := box.Load(w, "shop", nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(v.Entries) != 5 {
		t.Fatalf("Entries = %d, want 5: %+v", len(v.Entries), v.Entries)
	}

	// The sent.log entries carry real timestamps, so the 10:00 user line
	// must come before the 10:05 mate line, regardless of where the two
	// status lines (stamped with file mtime) land around them.
	var sawUser, sawMate bool
	for _, e := range v.Entries {
		if e.Kind != box.KindMessage {
			continue
		}
		if e.Source == box.SourceUser {
			sawUser = true
			if sawMate {
				t.Fatalf("user entry (10:00) sorted after mate entry (10:05)")
			}
		}
		if e.Source == box.SourceMate {
			sawMate = true
		}
	}
	if !sawUser || !sawMate {
		t.Fatalf("missing message entries: user=%v mate=%v", sawUser, sawMate)
	}

	// ByCrew groups by crew and drops the mate-targeted message.
	if len(v.ByCrew["k3"]) != 3 { // working, mate->k3, needs-decision
		t.Fatalf("ByCrew[k3] = %d, want 3: %+v", len(v.ByCrew["k3"]), v.ByCrew["k3"])
	}
	if len(v.ByCrew["k9"]) != 1 {
		t.Fatalf("ByCrew[k9] = %d, want 1", len(v.ByCrew["k9"]))
	}

	// Attention holds the needs-decision line and nothing else: k9's
	// `done:` is a wait-mate report, which the STATE column carries and
	// attention deliberately does not (mvp.md section 4b).
	if len(v.Attention) != 1 {
		t.Fatalf("Attention = %d, want 1: %+v", len(v.Attention), v.Attention)
	}
	if v.Attention[0].Crew != "k3" {
		t.Fatalf("Attention = %+v, want only k3's needs-decision line", v.Attention)
	}
}

func TestLoadSinceReturnsOnlyAppendedLines(t *testing.T) {
	w := newFixtureWorkspace(t)

	if err := w.AppendStatus("shop", "k3", "working: step one"); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendSent("shop", store.SentEntry{Source: store.SourceUser, Target: store.TargetMate, Text: "go"}); err != nil {
		t.Fatal(err)
	}

	first, err := box.Load(w, "shop", nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(first.Entries) != 2 {
		t.Fatalf("first Entries = %d, want 2", len(first.Entries))
	}

	if err := w.AppendStatus("shop", "k3", "blocked: need a decision"); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendSent("shop", store.SentEntry{Source: store.SourceMate, Target: store.CrewTarget("k3"), Text: "wait"}); err != nil {
		t.Fatal(err)
	}
	// A brand new crew appearing between polls must also show up.
	if err := w.AppendStatus("shop", "k9", "done: scouted"); err != nil {
		t.Fatal(err)
	}

	second, err := box.LoadSince(w, "shop", nil, first.Cursor)
	if err != nil {
		t.Fatalf("LoadSince: %v", err)
	}
	if len(second.Entries) != 3 {
		t.Fatalf("second Entries = %d, want 3 (the two appends plus the new crew): %+v", len(second.Entries), second.Entries)
	}
	for _, e := range second.Entries {
		if e.Text == "working: step one" || e.Text == "go" {
			t.Fatalf("LoadSince replayed an already-seen entry: %+v", e)
		}
	}

	// A further poll with no new writes returns nothing.
	third, err := box.LoadSince(w, "shop", nil, second.Cursor)
	if err != nil {
		t.Fatalf("LoadSince: %v", err)
	}
	if len(third.Entries) != 0 {
		t.Fatalf("third Entries = %d, want 0: %+v", len(third.Entries), third.Entries)
	}
}

func TestLoadMergesIncidents(t *testing.T) {
	w := newFixtureWorkspace(t)
	if err := w.AppendStatus("shop", "k3", "working: step one"); err != nil {
		t.Fatal(err)
	}

	incidents := []box.Incident{
		{At: time.Now(), Crew: "k3", Kind: box.IncidentStale, Text: "no status in 20m"},
	}
	v, err := box.Load(w, "shop", incidents)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(v.Attention) != 1 || v.Attention[0].Kind != box.KindIncident {
		t.Fatalf("Attention = %+v, want one incident", v.Attention)
	}
	kind, text := box.ParseIncidentText(v.Attention[0].Text)
	if kind != box.IncidentStale || text != "no status in 20m" {
		t.Fatalf("ParseIncidentText = %q, %q", kind, text)
	}
	if len(v.ByCrew["k3"]) != 2 { // the working status line plus the incident
		t.Fatalf("ByCrew[k3] = %d, want 2: %+v", len(v.ByCrew["k3"]), v.ByCrew["k3"])
	}
}

// TestParseStatusVocabulary pins mvp.md section 4b: the three verbs a crew
// may write, the three pre-4b verbs a status file may still carry and what
// each maps to, and the two shapes that are not a verb at all.
func TestParseStatusVocabulary(t *testing.T) {
	cases := []struct {
		line      string
		wantState box.State
		wantText  string
	}{
		{"working: on it", box.StateWorking, "on it"},
		{"needs-decision: pick one", box.StateNeedsDecision, "pick one"},
		{"wait-mate: ready in branch matev2/k3", box.StateWaitMate, "ready in branch matev2/k3"},
		// Legacy. `done:` is the old spelling of `wait-mate:`.
		{"done: shipped", box.StateWaitMate, "shipped"},
		// A crew that said `blocked:` could still speak, which is a
		// needs-decision now - and its own word stays in the text so
		// nothing it said is lost to the mapping.
		{"blocked: waiting on ci", box.StateNeedsDecision, "blocked: waiting on ci"},
		// Same for `failed:`: the crew handing work back is wait-mate, and
		// whether the task failed is the Mate's call, not the crew's.
		{"failed: tests red", box.StateWaitMate, "failed: tests red"},
		{"pr-ready: opened #4", box.StateUnknown, "pr-ready: opened #4"},
		{"no colon at all here", box.StateUnknown, "no colon at all here"},
		{"", box.StateUnknown, ""},
	}
	for _, tc := range cases {
		got := box.ParseStatus(tc.line)
		if got.State != tc.wantState || got.Text != tc.wantText {
			t.Errorf("ParseStatus(%q) = %+v, want {%s %q}", tc.line, got, tc.wantState, tc.wantText)
		}
	}
}

// TestLastVerbSkipsLinesThatCarryNoVerb: a crew that echoed something
// malformed after reporting has not changed state, and `unknown` is not a
// state (mvp.md section 4b).
func TestLastVerbSkipsLinesThatCarryNoVerb(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  box.State
	}{
		{"nothing written", nil, box.StateUnknown},
		{"only blank lines", []string{"", "   "}, box.StateUnknown},
		{"only unrecognised lines", []string{"hello there"}, box.StateUnknown},
		{"last verb wins", []string{"working: a", "needs-decision: b"}, box.StateNeedsDecision},
		{"a malformed tail does not erase the last verb", []string{"working: a", "oops"}, box.StateWorking},
		{"a legacy done tail reads as wait-mate", []string{"working: a", "done: b"}, box.StateWaitMate},
	}
	for _, tc := range cases {
		if got := box.LastVerb(tc.lines); got != tc.want {
			t.Errorf("%s: LastVerb(%q) = %q, want %q", tc.name, tc.lines, got, tc.want)
		}
	}
}

// TestAttentionVerbSet: among the crew's own verbs only needs-decision is
// attention (mvp.md section 4b). wait-mate is a report the STATE column
// already carries, and the decision of 2026-09-18 keeps it out of the
// inbox on purpose.
func TestAttentionVerbSet(t *testing.T) {
	if !box.Attention(box.StateNeedsDecision) {
		t.Errorf("Attention(%s) = false, want true", box.StateNeedsDecision)
	}
	no := []box.State{box.StateWorking, box.StateWaitMate, box.StateUnknown, box.State("something-else")}
	for _, s := range no {
		if box.Attention(s) {
			t.Errorf("Attention(%s) = true, want false", s)
		}
	}
}

func TestLatestStatusAndSummarize(t *testing.T) {
	w := newFixtureWorkspace(t)
	if err := w.AppendStatus("shop", "k3", "working: step one"); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "needs-decision: pick a db"); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k9", "working: still going"); err != nil {
		t.Fatal(err)
	}

	v, err := box.Load(w, "shop", nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	st, ok := box.LatestStatus(v, "k3")
	if !ok || st.State != box.StateNeedsDecision || st.Text != "pick a db" {
		t.Fatalf("LatestStatus(k3) = %+v, %v", st, ok)
	}
	if _, ok := box.LatestStatus(v, "ghost"); ok {
		t.Fatalf("LatestStatus(ghost) = ok, want not found")
	}

	sum := box.Summarize(v)
	if sum.Crews != 2 {
		t.Fatalf("Summary.Crews = %d, want 2", sum.Crews)
	}
	if sum.Awaiting != 1 {
		t.Fatalf("Summary.Awaiting = %d, want 1 (only k3 is needs-decision)", sum.Awaiting)
	}
	if sum.LastAt.IsZero() {
		t.Fatalf("Summary.LastAt is zero")
	}
}

func TestLineTruncatesWideRunes(t *testing.T) {
	e := box.Entry{
		At:   time.Date(2026, 9, 17, 10, 32, 0, 0, time.UTC),
		Kind: box.KindStatus,
		Crew: "k3",
		Text: "needs-decision: 使用 SQLite 還是 Postgres 資料庫比較好呢",
	}

	full := box.Line(e, 200)
	if got := "10:32  crew k3  needs-decision  使用 SQLite 還是 Postgres 資料庫比較好呢"; full != got {
		t.Fatalf("Line(wide) = %q, want %q", full, got)
	}

	short := box.Line(e, 20)
	if short == full {
		t.Fatalf("Line did not truncate at width 20")
	}
	if len([]rune(short)) >= len([]rune(full)) {
		t.Fatalf("Line truncated = %q, not shorter than full %q", short, full)
	}
	if !hasSuffixRune(short, '…') {
		t.Fatalf("Line truncated = %q, want an ellipsis", short)
	}

	if got := box.Line(e, 0); got != "" {
		t.Fatalf("Line(width=0) = %q, want empty", got)
	}
}

func TestLineRendersMessageEntry(t *testing.T) {
	e := box.Entry{
		At:     time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
		Kind:   box.KindMessage,
		Source: box.SourceUser,
		Target: store.TargetMate,
		Text:   "spawn k3",
	}
	got := box.Line(e, 100)
	want := "10:00  user->mate  spawn k3"
	if got != want {
		t.Fatalf("Line(message) = %q, want %q", got, want)
	}
}

// hasSuffixRune is a tiny local helper so the test does not need to reach
// into box's unexported width helpers.
func hasSuffixRune(s string, r rune) bool {
	rs := []rune(s)
	return len(rs) > 0 && rs[len(rs)-1] == r
}
