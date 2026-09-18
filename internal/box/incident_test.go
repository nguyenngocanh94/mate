package box_test

import (
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// The incident half of the merge, read from `incidents.log` (mvp.md section
// 4b): an incident is open when the last line of its (crew, kind) pair says
// `open`, a resolved one is history rather than an inbox item, and the file
// is the only place box learns about any of it.

func appendIncident(t *testing.T, w *store.Workspace, at time.Time, crew, kind, state, text string) {
	t.Helper()
	if err := w.AppendIncident("shop", store.IncidentEntry{
		Time: at, Crew: crew, Kind: kind, State: state, Text: text,
	}); err != nil {
		t.Fatalf("AppendIncident: %v", err)
	}
}

func TestLoadReadsIncidentsLogWithoutBeingHandedAny(t *testing.T) {
	w := newFixtureWorkspace(t)
	at := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	appendIncident(t, w, at, "k3", string(box.IncidentStale), store.IncidentOpen, "pane quiet for 3m0s")

	v, err := box.Load(w, "shop")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	open := box.OpenIncidents(v, "k3")
	if len(open) != 1 {
		t.Fatalf("OpenIncidents(k3) = %+v, want one", open)
	}
	got := open[0]
	if got.Kind != box.IncidentStale || got.Text != "pane quiet for 3m0s" || got.Resolved {
		t.Fatalf("incident = %+v, want an unresolved stale one", got)
	}
	if !got.At.Equal(at) {
		t.Fatalf("incident time = %v, want the log's own %v", got.At, at)
	}
	entry := v.ByCrew["k3"][0]
	if entry.Ref.File != w.IncidentsLog("shop") {
		t.Fatalf("incident Ref.File = %q, want %q", entry.Ref.File, w.IncidentsLog("shop"))
	}
}

func TestLoadMarksAnIncidentResolvedWhenTheObserverClosedIt(t *testing.T) {
	w := newFixtureWorkspace(t)
	at := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	appendIncident(t, w, at, "k3", string(box.IncidentStale), store.IncidentOpen, "pane quiet for 3m0s")
	appendIncident(t, w, at.Add(time.Minute), "k3", string(box.IncidentStale), store.IncidentResolved, "pane changed")

	v, err := box.Load(w, "shop")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if open := box.OpenIncidents(v, "k3"); len(open) != 0 {
		t.Fatalf("OpenIncidents(k3) = %+v, want none after the resolve", open)
	}
	// The record keeps it: one entry, marked resolved. A `resolved` line is
	// the end of a finding, not a second finding, so it draws no entry of
	// its own.
	if len(v.ByCrew["k3"]) != 1 {
		t.Fatalf("ByCrew[k3] = %+v, want the one opened incident", v.ByCrew["k3"])
	}
	if inc := v.ByCrew["k3"][0].Incident; inc == nil || !inc.Resolved {
		t.Fatalf("incident = %+v, want Resolved", inc)
	}
	if items := box.Inbox(v); len(items) != 0 {
		t.Fatalf("inbox = %+v, want empty: a resolved incident is history", items)
	}
}

func TestLoadPairsRepeatedIncidentsOfTheSameKind(t *testing.T) {
	w := newFixtureWorkspace(t)
	at := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	// Went stale, recovered, went stale again: the second open is the one
	// still waiting, and the first must not be re-opened by it.
	appendIncident(t, w, at, "k3", string(box.IncidentStale), store.IncidentOpen, "first")
	appendIncident(t, w, at.Add(time.Minute), "k3", string(box.IncidentStale), store.IncidentResolved, "pane changed")
	appendIncident(t, w, at.Add(2*time.Minute), "k3", string(box.IncidentStale), store.IncidentOpen, "second")

	v, err := box.Load(w, "shop")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	open := box.OpenIncidents(v, "k3")
	if len(open) != 1 || open[0].Text != "second" {
		t.Fatalf("OpenIncidents(k3) = %+v, want only the second one", open)
	}
	if len(v.ByCrew["k3"]) != 2 {
		t.Fatalf("ByCrew[k3] = %+v, want both opened incidents in the history", v.ByCrew["k3"])
	}
}

func TestLoadKeepsIncidentKindsApart(t *testing.T) {
	w := newFixtureWorkspace(t)
	at := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	appendIncident(t, w, at, "k3", string(box.IncidentStale), store.IncidentOpen, "quiet")
	appendIncident(t, w, at.Add(time.Minute), "k3", string(box.IncidentRuntimeLost), store.IncidentOpen, "agent gone")
	// Resolving one kind must not resolve the other.
	appendIncident(t, w, at.Add(2*time.Minute), "k3", string(box.IncidentRuntimeLost), store.IncidentResolved, "agent back")

	v, err := box.Load(w, "shop")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	open := box.OpenIncidents(v, "k3")
	if len(open) != 1 || open[0].Kind != box.IncidentStale {
		t.Fatalf("OpenIncidents(k3) = %+v, want the stale one only", open)
	}
}

func TestLoadSinceDoesNotReplayAnIncidentItAlreadyReported(t *testing.T) {
	w := newFixtureWorkspace(t)
	at := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	appendIncident(t, w, at, "k3", string(box.IncidentStale), store.IncidentOpen, "first")

	first, err := box.Load(w, "shop")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(first.Entries) != 1 {
		t.Fatalf("first Entries = %+v, want the one incident", first.Entries)
	}

	appendIncident(t, w, at.Add(time.Minute), "k4", string(box.IncidentRuntimeLost), store.IncidentOpen, "agent gone")
	second, err := box.LoadSince(w, "shop", first.Cursor)
	if err != nil {
		t.Fatalf("LoadSince: %v", err)
	}
	if len(second.Entries) != 1 || second.Entries[0].Crew != "k4" {
		t.Fatalf("second Entries = %+v, want only the k4 incident", second.Entries)
	}
}
