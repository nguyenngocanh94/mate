package console

import (
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The crew sheet's merge entry (docs/mvp.md task 22). It runs only on a
// crew that reads wait-mate; on every other crew it keeps its place in the
// fixed order, disabled with its reason; it never appears on a Mate or a
// Project; and it is dangerous, so only M pressed twice runs it.

// mergeTree is sampleTree with the running crew at wait-mate: the crew has
// handed its work back, the one state a merge applies in.
func mergeTree() query.Snapshot {
	tree := sampleTree()
	tree.Projects[0].Crews[1].Status = query.CrewWaitMate
	return tree
}

// toMergeableCrew selects sampleTree's open crew of the first Project.
func toMergeableCrew(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = send(t, m, key("enter")) // payments-api
	m, _ = send(t, m, key("down"))  // the open crew; the closed one sits in Completed
	want := sampleTree().Projects[0].Crews[1].CrewID
	if r, ok := m.selectedRow(); !ok || r.kind != rowCrew || r.id != want {
		t.Fatalf("selected row = %+v (ok=%v), want crew %s", r, ok, want)
	}
	return m
}

func hasMerge(m Model) bool {
	for _, e := range m.menu {
		if e.key == "M" || e.choice.action == ActionMerge {
			return true
		}
	}
	return false
}

func TestMergeRunsOnlyOnAWaitMateCrew(t *testing.T) {
	for _, tc := range []struct {
		status query.CrewStatus
		want   bool
	}{
		{query.CrewWaitMate, true},
		{query.CrewWorking, false},
		{query.CrewSpawned, false},
		{query.CrewNeedsDecision, false},
		{query.CrewBlocked, false},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			tree := sampleTree()
			tree.Projects[0].Crews[1].Status = tc.status
			m := toMergeableCrew(t, loaded(t, tree, nil))
			m, _ = send(t, m, key("a"))
			e := actEntry(t, m, "M")
			if e.enabled != tc.want {
				t.Fatalf("merge entry on a %s crew = %+v, want enabled=%v", tc.status, e, tc.want)
			}
			if !tc.want && e.reason == "" {
				t.Fatalf("disabled merge on a %s crew carries no reason", tc.status)
			}
			if !tc.want {
				calls := 0
				m.action = func(context.Context, ActionRequest) (string, error) { calls++; return "", nil }
				m, cmd := send(t, m, key("M"))
				if cmd != nil || calls != 0 || m.confirm != nil {
					t.Fatalf("M on a %s crew acted: cmd=%v calls=%d confirm=%v", tc.status, cmd != nil, calls, m.confirm != nil)
				}
			}
		})
	}
}

func TestMergeIsAbsentFromTheMateAndProjectRows(t *testing.T) {
	m := loaded(t, mergeTree(), nil)
	m, _ = send(t, m, key("a")) // a Project row on the workspace
	if hasMerge(m) {
		t.Fatal("a Project row offers merge; merge acts on one named crew")
	}
	m, _ = send(t, m, key("esc"))
	m, _ = send(t, m, key("enter"))
	if r, ok := m.selectedRow(); !ok || r.kind != rowMate {
		t.Fatalf("selected row = %+v (ok=%v), want the Mate row", r, ok)
	}
	m, _ = send(t, m, key("a"))
	if hasMerge(m) {
		t.Fatal("the Mate row offers merge; merge acts on a crew's branch")
	}
}

func TestMergeNamesTheBranchesAndRunsOnlyOnItsOwnKey(t *testing.T) {
	m, got := actRunner(t, mergeTree(),
		"payments-api/crew_01J9P6Q6W0E5V8XK2M4B8DT: merged 2 commit(s) into main (aaaaaaa..bbbbbbb); crew finished, worktree and branch removed", nil)
	m = toMergeableCrew(t, m)
	m, _ = send(t, m, key("a"))
	e := actEntry(t, m, "M")
	if !e.enabled || !e.confirms() {
		t.Fatalf("merge entry = %+v, want enabled and confirming", e)
	}
	// Walk the cursor onto it: the reader sees what it moves before asking.
	for m.menu[m.actionIndex].key != "M" {
		m, _ = send(t, m, key("down"))
	}
	crew := sampleTree().Projects[0].Crews[1]
	flat := actFlat(renderFrame(t, m))
	for _, want := range []string{crew.Worktree.Value.Branch, crew.Repo.Value.DefaultBranch} {
		if !strings.Contains(flat, want) {
			t.Fatalf("the highlighted merge does not name %q:\n%s", want, renderFrame(t, m))
		}
	}

	m, cmd := send(t, m, key("enter"))
	if cmd != nil || m.confirm == nil || len(*got) != 0 {
		t.Fatalf("merge ran without a confirmation: cmd=%v confirm=%v calls=%d", cmd != nil, m.confirm != nil, len(*got))
	}
	view := renderFrame(t, m)
	for _, want := range []string{"merge?", "object", "scope", "effect", "M      merge"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation is missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(actFlat(view), "Fast-forward only") {
		t.Fatalf("confirmation does not say a merge is fast-forward only:\n%s", view)
	}

	m, cmd = send(t, m, key("M"))
	if cmd == nil {
		t.Fatal("the confirmed merge did not dispatch")
	}
	m, _ = send(t, m, cmd())
	if len(*got) != 1 {
		t.Fatalf("runner called %d time(s), want 1", len(*got))
	}
	req := (*got)[0]
	if req.Action != ActionMerge || req.TargetKind != "crew" ||
		req.Target != mergeTree().Projects[0].ProjectID || req.Crew != crew.CrewID {
		t.Fatalf("request = %+v, want the Project and the crew named separately", req)
	}
	if m.msg.tone != toneOK || !strings.Contains(m.msg.text, "crew finished") {
		t.Fatalf("outcome line = %+v, want the command's own one line", m.msg)
	}
}

func TestMergeCancelledFromTheConfirmationRunsNothing(t *testing.T) {
	for _, cancel := range []string{"enter", "esc"} {
		t.Run(cancel, func(t *testing.T) {
			m, got := actRunner(t, mergeTree(), "", nil)
			m = toMergeableCrew(t, m)
			m, _ = send(t, m, key("a"))
			m, _ = send(t, m, key("M"))
			if m.confirm == nil {
				t.Fatal("no confirmation to cancel")
			}
			m, cmd := send(t, m, key(cancel))
			if cmd != nil || len(*got) != 0 || m.confirm != nil {
				t.Fatalf("%s on the confirmation ran the merge or stayed: cmd=%v calls=%d confirm=%v", cancel, cmd != nil, len(*got), m.confirm != nil)
			}
		})
	}
}
