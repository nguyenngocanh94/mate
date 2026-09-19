package console

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// The `merge` entry of a Crew row's Actions menu (docs/mvp.md task 22).
//
// Two rules, and they are the whole of what this surface owes: the entry is
// offered on a Crew that reads `wait-mate` and is absent - not disabled - on
// every other row, and it is dangerous, so it runs only after the same
// confirmation ActionRestartMate goes through.

// mergeTree is sampleTree with the running Crew moved to `wait-mate`: the
// Crew has handed its work back, which is the one state a merge applies in.
func mergeTree() query.Snapshot {
	tree := sampleTree()
	tree.Projects[0].Crews[1].Status = query.CrewWaitMate
	return tree
}

// toMergeableCrew selects sampleTree's second Crew of the first Project.
func toMergeableCrew(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = send(t, m, key("enter")) // into the payments-api Project
	m, _ = send(t, m, key("down"))  // the open Crew; the closed one sits in Completed
	want := sampleTree().Projects[0].Crews[1].CrewID
	if r, ok := m.selectedRow(); !ok || r.kind != rowCrew || r.id != want {
		t.Fatalf("selected row = %+v (ok=%v), want crew %s", r, ok, want)
	}
	return m
}

func mergeEntry(choices []actionChoice) (actionChoice, bool) {
	for _, c := range choices {
		if c.action == ActionMerge {
			return c, true
		}
	}
	return actionChoice{}, false
}

func TestMergeIsOfferedOnlyOnAWaitMateCrewRow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status query.CrewStatus
		want   bool
	}{
		{"wait-mate", query.CrewWaitMate, true},
		{"working", query.CrewWorking, false},
		{"spawned", query.CrewSpawned, false},
		{"needs-decision", query.CrewNeedsDecision, false},
		{"blocked", query.CrewBlocked, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := sampleTree()
			tree.Projects[0].Crews[1].Status = tc.status
			m := loaded(t, tree, nil)
			m = toMergeableCrew(t, m)
			m, _ = send(t, m, key("a"))
			_, found := mergeEntry(m.actionChoices)
			if found != tc.want {
				var names []string
				for _, c := range m.actionChoices {
					names = append(names, string(c.action))
				}
				t.Fatalf("merge entry present = %v on a %s crew, want %v; menu was %v", found, tc.status, tc.want, names)
			}
		})
	}
}

func TestMergeIsAbsentFromTheMateAndProjectRows(t *testing.T) {
	m := loaded(t, mergeTree(), nil)
	// The Mate row of the Project frame.
	m, _ = send(t, m, key("enter"))
	if r, ok := m.selectedRow(); !ok || r.kind != rowMate {
		t.Fatalf("selected row = %+v (ok=%v), want the Mate row", r, ok)
	}
	m, _ = send(t, m, key("a"))
	if _, found := mergeEntry(m.actionChoices); found {
		t.Fatal("the Mate row offers merge; merge acts on a crew's branch")
	}
	m, _ = send(t, m, key("esc"))

	// A Project row on the workspace frame.
	m2 := loaded(t, mergeTree(), nil)
	m2, _ = send(t, m2, key("a"))
	if _, found := mergeEntry(m2.actionChoices); found {
		t.Fatal("a Project row offers merge; merge acts on one named crew")
	}
}

func TestMergeConfirmsWithTheBranchesItWillMoveBeforeRunning(t *testing.T) {
	calls := 0
	var got ActionRequest
	m := loaded(t, mergeTree(), nil)
	m.action = func(_ context.Context, req ActionRequest) (string, error) {
		calls++
		got = req
		return "payments-api/crew_01J9P6Q6W0E5V8XK2M4B8DT: merged 2 commit(s) into main (aaaaaaa..bbbbbbb); crew finished, worktree and branch removed", nil
	}
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m = toMergeableCrew(t, m)
	m, _ = send(t, m, key("a"))

	entry, found := mergeEntry(m.actionChoices)
	if !found || !entry.enabled || !entry.dangerous {
		t.Fatalf("merge entry = %+v (found=%v), want an enabled, dangerous entry", entry, found)
	}
	// Walk the cursor onto it rather than reaching into the slice: what is
	// being proved is that pressing Enter on the entry a reader can select
	// opens the confirmation.
	for i := 0; i < len(m.actionChoices); i++ {
		if m.actionChoices[m.actionIndex].action == ActionMerge {
			break
		}
		m, _ = send(t, m, key("down"))
	}
	if m.actionChoices[m.actionIndex].action != ActionMerge {
		t.Fatal("could not move the cursor onto the merge entry")
	}

	m, cmd := send(t, m, key("enter"))
	if cmd != nil || m.confirm == nil {
		t.Fatalf("merge ran without a confirmation: cmd=%v confirm=%+v", cmd, m.confirm)
	}
	if calls != 0 {
		t.Fatalf("the action ran before the confirmation: %d call(s)", calls)
	}

	crew := sampleTree().Projects[0].Crews[1]
	want := "Merge " + crew.Worktree.Value.Branch + " into " + crew.Repo.Value.DefaultBranch + " and finish crew " + crew.CrewID + "?"
	view := renderFrame(t, m)
	if !strings.Contains(view, want) {
		t.Fatalf("confirmation does not ask %q:\n%s", want, view)
	}
	for _, field := range []string{"Object", "Scope", "Effect", "Enter merge"} {
		if !strings.Contains(view, field) {
			t.Fatalf("confirmation is missing %q:\n%s", field, view)
		}
	}

	m, cmd = send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("the confirmed merge did not dispatch")
	}
	m, _ = send(t, m, cmd())
	if calls != 1 {
		t.Fatalf("runner called %d time(s), want 1", calls)
	}
	if got.Action != ActionMerge || got.TargetKind != "crew" ||
		got.Target != mergeTree().Projects[0].ProjectID || got.Crew != crew.CrewID {
		t.Fatalf("request = %+v, want the Project and the Crew named separately", got)
	}
	if m.msg.tone != toneOK || !strings.Contains(m.msg.text, "crew finished") {
		t.Fatalf("outcome line = %+v, want the command's own one line", m.msg)
	}
}

func TestMergeCancelledFromTheConfirmationRunsNothing(t *testing.T) {
	calls := 0
	m := loaded(t, mergeTree(), nil)
	m.action = func(context.Context, ActionRequest) (string, error) { calls++; return "", nil }
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m = toMergeableCrew(t, m)
	m, _ = send(t, m, key("a"))
	for i := 0; i < len(m.actionChoices); i++ {
		if m.actionChoices[m.actionIndex].action == ActionMerge {
			break
		}
		m, _ = send(t, m, key("down"))
	}
	m, _ = send(t, m, key("enter"))
	if m.confirm == nil {
		t.Fatal("no confirmation to cancel")
	}
	m, cmd := send(t, m, key("esc"))
	if cmd != nil || calls != 0 {
		t.Fatalf("Esc on the confirmation ran the merge: cmd=%v calls=%d", cmd, calls)
	}
	if m.confirm != nil {
		t.Fatal("Esc left the confirmation open")
	}
}
