package store_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

// The incidents.log contract is docs/mvp.md section 4b: append-only,
// tab-separated, `RFC3339 \t crew \t kind \t open|resolved \t text`, and an
// incident is open when the last line of its (crew, kind) pair says `open`.
// These tests are written from that paragraph, not from the reader.

func TestStoreAppendIncidentWritesTheContractLine(t *testing.T) {
	w := newProjectWorkspace(t)

	when := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	if err := w.AppendIncident("shop", store.IncidentEntry{
		Time:  when,
		Crew:  "k3",
		Kind:  "stale",
		State: store.IncidentOpen,
		Text:  "pane and status unchanged for 3m0s",
	}); err != nil {
		t.Fatalf("AppendIncident: %v", err)
	}

	raw, err := os.ReadFile(w.IncidentsLog("shop"))
	if err != nil {
		t.Fatalf("read incidents.log: %v", err)
	}
	want := strings.Join([]string{
		when.Format(time.RFC3339), "k3", "stale", "open", "pane and status unchanged for 3m0s",
	}, "\t") + "\n"
	if string(raw) != want {
		t.Fatalf("incidents.log = %q, want %q", raw, want)
	}
}

func TestStoreReadIncidentsResumesFromCursor(t *testing.T) {
	w := newProjectWorkspace(t)

	when := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	lines := []store.IncidentEntry{
		{Time: when, Crew: "k3", Kind: "stale", State: store.IncidentOpen, Text: "quiet"},
		{Time: when.Add(time.Minute), Crew: "k3", Kind: "stale", State: store.IncidentResolved, Text: "pane changed"},
		{Time: when.Add(2 * time.Minute), Crew: "k4", Kind: "runtime_lost", State: store.IncidentOpen, Text: "agent gone"},
	}
	for _, l := range lines {
		if err := w.AppendIncident("shop", l); err != nil {
			t.Fatalf("AppendIncident: %v", err)
		}
	}

	got, next, err := w.ReadIncidents("shop", 0)
	if err != nil {
		t.Fatalf("ReadIncidents: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	for i := range lines {
		if got[i].Crew != lines[i].Crew || got[i].Kind != lines[i].Kind ||
			got[i].State != lines[i].State || got[i].Text != lines[i].Text {
			t.Fatalf("entry %d = %+v, want %+v", i, got[i], lines[i])
		}
		if !got[i].Time.Equal(lines[i].Time) {
			t.Fatalf("entry %d time = %v, want %v", i, got[i].Time, lines[i].Time)
		}
	}
	if !got[0].Open() || got[1].Open() {
		t.Fatalf("Open() = %v, %v; want the first line open and the second not", got[0].Open(), got[1].Open())
	}

	resumed, after, err := w.ReadIncidents("shop", next)
	if err != nil {
		t.Fatalf("ReadIncidents from the end: %v", err)
	}
	if len(resumed) != 0 || after != next {
		t.Fatalf("read from the end returned %d entries, next = %d (was %d)", len(resumed), after, next)
	}

	tail, _, err := w.ReadIncidents("shop", got[2].Offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 || tail[0].Crew != "k4" {
		t.Fatalf("resumed read = %+v, want only the k4 line", tail)
	}
}

func TestStoreReadIncidentsOfAProjectThatHasNoneIsNotAnError(t *testing.T) {
	w := newProjectWorkspace(t)
	got, next, err := w.ReadIncidents("shop", 0)
	if err != nil {
		t.Fatalf("ReadIncidents before the file exists: %v", err)
	}
	if len(got) != 0 || next != 0 {
		t.Fatalf("empty read = %+v, next = %d", got, next)
	}
}

func TestStoreAppendIncidentFlattensAndFillsTheTime(t *testing.T) {
	w := newProjectWorkspace(t)
	if err := w.AppendIncident("shop", store.IncidentEntry{
		Crew:  "k3",
		Kind:  "stale",
		State: store.IncidentOpen,
		Text:  "first\nsecond\tthird",
	}); err != nil {
		t.Fatalf("AppendIncident: %v", err)
	}
	got, _, err := w.ReadIncidents("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "first second third" {
		t.Fatalf("entries = %+v, want one flattened line", got)
	}
	if got[0].Time.IsZero() {
		t.Fatal("AppendIncident did not fill in the time")
	}
}

func TestStoreReadIncidentsSkipsUnparseableLines(t *testing.T) {
	w := newProjectWorkspace(t)
	if err := w.AppendIncident("shop", store.IncidentEntry{
		Crew: "k3", Kind: "stale", State: store.IncidentOpen, Text: "good",
	}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(w.IncidentsLog("shop"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// A hand-edited line, and one with a state word the contract does not
	// allow: neither may hide the lines around it, and neither may be
	// reported as an incident state a reader would then have to guess at.
	if _, err := f.WriteString("junk hand-edited line\n" +
		time.Now().Format(time.RFC3339) + "\tk3\tstale\tmaybe\thalf-open\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := w.AppendIncident("shop", store.IncidentEntry{
		Crew: "k3", Kind: "stale", State: store.IncidentResolved, Text: "after",
	}); err != nil {
		t.Fatal(err)
	}

	got, _, err := w.ReadIncidents("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Text != "good" || got[1].Text != "after" {
		t.Fatalf("entries = %+v, want the two parseable lines", got)
	}
}

func TestStoreIncidentLogRejectsBadNamesAndStates(t *testing.T) {
	w := newProjectWorkspace(t)

	good := store.IncidentEntry{Crew: "k3", Kind: "stale", State: store.IncidentOpen, Text: "x"}
	if err := w.AppendIncident("../evil", good); err == nil {
		t.Fatal("AppendIncident with an invalid project name succeeded")
	}
	bad := good
	bad.Crew = "K3"
	if err := w.AppendIncident("shop", bad); err == nil {
		t.Fatal("AppendIncident with an invalid crew id succeeded")
	}
	bad = good
	bad.State = "maybe"
	if err := w.AppendIncident("shop", bad); err == nil {
		t.Fatal("AppendIncident with an invalid state succeeded")
	}
	bad = good
	bad.Kind = ""
	if err := w.AppendIncident("shop", bad); err == nil {
		t.Fatal("AppendIncident with no kind succeeded")
	}
	if _, _, err := w.ReadIncidents("Shop", 0); err == nil {
		t.Fatal("ReadIncidents with an invalid project name succeeded")
	}
}
