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

	v, err := box.Load(w, "shop")
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

	// Attention holds the needs-decision and done status lines, newest first.
	if len(v.Attention) != 2 {
		t.Fatalf("Attention = %d, want 2: %+v", len(v.Attention), v.Attention)
	}
	if v.Attention[0].Crew != "k9" || v.Attention[1].Crew != "k3" {
		t.Fatalf("Attention order = %+v, want k9 then k3 (newest first)", v.Attention)
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

	first, err := box.Load(w, "shop")
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

	second, err := box.LoadSince(w, "shop", first.Cursor)
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
	third, err := box.LoadSince(w, "shop", second.Cursor)
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

	if err := w.AppendIncident("shop", store.IncidentEntry{
		Time: time.Now(), Crew: "k3", Kind: string(box.IncidentStale),
		State: store.IncidentOpen, Text: "no status in 20m",
	}); err != nil {
		t.Fatal(err)
	}
	v, err := box.Load(w, "shop")
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

func TestParseStatusUnknownStateAndMalformed(t *testing.T) {
	cases := []struct {
		line      string
		wantState box.State
		wantText  string
	}{
		{"working: on it", box.StateWorking, "on it"},
		{"needs-decision: pick one", box.StateNeedsDecision, "pick one"},
		{"blocked: waiting on ci", box.StateBlocked, "waiting on ci"},
		{"done: shipped", box.StateDone, "shipped"},
		{"failed: tests red", box.StateFailed, "tests red"},
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

func TestAttentionVerbSet(t *testing.T) {
	yes := []box.State{box.StateNeedsDecision, box.StateBlocked, box.StateDone, box.StateFailed}
	for _, s := range yes {
		if !box.Attention(s) {
			t.Errorf("Attention(%s) = false, want true", s)
		}
	}
	no := []box.State{box.StateWorking, box.StateUnknown, box.State("something-else")}
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

	v, err := box.Load(w, "shop")
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
