package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/timeline/scene"
)

// `matev2 events <project> --scene` answers "who is doing what, right now"
// (docs/mvp.md M5 question 1) before it answers anything else: one JSON line
// per actor, out of `v_now`.
func TestEventsSceneStartsWithWhereEverybodyIs(t *testing.T) {
	w := eventsWorkspace(t)
	runCLI(t, "reindex", w.Root())

	out := runCLI(t, "events", "shop", "--scene", "--workspace", w.Root())
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("--scene printed %d line(s):\n%s", len(lines), out)
	}
	states := map[string]string{}
	for i, line := range lines {
		var row struct {
			Actor     string `json:"actor"`
			ActorKind string `json:"actor_kind"`
			Project   string `json:"project"`
			State     string `json:"state"`
			Since     string `json:"since"`
			Detail    string `json:"detail"`
			Target    string `json:"target"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("line %d is not one JSON object: %v\n%s", i, err, line)
		}
		if !strings.HasPrefix(line, `{"actor":`) {
			t.Fatalf("line %d does not start with the actor field; the field order is part of the contract:\n%s", i, line)
		}
		if row.Project != "shop" {
			t.Fatalf("line %d is about project %q", i, row.Project)
		}
		states[row.Actor] = row.State
	}
	// The crew asked and was answered, so it is back at its desk; the Mate
	// answered it and is alone again.
	if got := states["k3"]; got != string(scene.AtDeskWorking) {
		t.Fatalf("k3 is %q in the snapshot, want %s", got, scene.AtDeskWorking)
	}
	if got := states["mate"]; got != string(scene.Idle) {
		t.Fatalf("the Mate is %q in the snapshot, want %s", got, scene.Idle)
	}
}

// `--scene --narrate` is the same snapshot in the office's own words, and
// `--since` adds the moves that led to it.
func TestEventsSceneNarratesTheOffice(t *testing.T) {
	w := eventsWorkspace(t)
	runCLI(t, "reindex", w.Root())

	out := runCLI(t, "events", "shop", "--scene", "--narrate", "--since", "0", "--workspace", w.Root())
	for _, want := range []string{
		"k3 walks to the CEO's office with a question",
		"k3 waits at the CEO's door",
		"the Mate answers k3",
		"k3 goes back to its desk",
		"now      k3 is at its desk",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the narrated scene does not say %q:\n%s", want, out)
		}
	}
}

// The transitions are JSON lines too, in story order, each naming the event
// it came from so a doubter can read the fact behind the move.
func TestEventsSceneTransitionsNameTheirEvent(t *testing.T) {
	w := eventsWorkspace(t)
	runCLI(t, "reindex", w.Root())

	out := runCLI(t, "events", "shop", "--scene", "--since", "0", "--workspace", w.Root())
	moves := 0
	lastID := ""
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !strings.HasPrefix(line, `{"id":`) {
			continue // the snapshot lines start with the actor
		}
		var row struct {
			ID    string `json:"id"`
			To    string `json:"to"`
			Event int64  `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("a transition line is not one JSON object: %v\n%s", err, line)
		}
		if row.Event == 0 {
			t.Fatalf("a transition names no event:\n%s", line)
		}
		if row.ID <= lastID {
			t.Fatalf("transition %q is printed after %q; the ids sort into story order", row.ID, lastID)
		}
		lastID = row.ID
		moves++
	}
	if moves < 4 {
		t.Fatalf("--scene --since printed %d move(s):\n%s", moves, out)
	}
}
