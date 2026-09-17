package console

import (
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// projectFrame loads a tree and drills into its first Project, leaving the
// Mate row (always row 0 of a Project frame) selected.
func projectFrame(t *testing.T, tree query.Snapshot) Model {
	t.Helper()
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	if r, ok := m.selectedRow(); !ok || r.kind != rowMate {
		t.Fatalf("expected the Mate row selected, got %+v ok=%v", r, ok)
	}
	return m
}

// mateTree is one Project whose Mate is in the given state, with the
// capability DTO query.mateActions would author for it.
func mateTree(mate query.MateNode, actions []query.ActionAvailability) query.Snapshot {
	tree := sampleTree()
	tree.Projects = tree.Projects[:1]
	tree.Projects[0].Crews = nil
	mate.Actions = actions
	tree.Projects[0].Mate = mate
	return tree
}

func knownMate(status query.MateStatus, kind query.HarnessKind) query.MateNode {
	return query.MateNode{
		Designated: query.KnownField(query.MateIdentity{MateID: "mate_1", HarnessKind: kind, Status: status}),
		AgentName:  query.AbsentField[string]("no binding"),
		Binding:    query.AbsentField[query.BindingValue]("no binding is recorded"),
		LastEvent:  query.AbsentField[query.EventValue]("no event"),
		Error:      query.AbsentField[query.ErrorReason](notErrorState),
	}
}

func mateCaps(onboard, start, resume bool) []query.ActionAvailability {
	return []query.ActionAvailability{
		{Action: "start", Available: start, Reason: "recorded status"},
		{Action: "stop", Available: false, Reason: "no active binding to stop"},
		{Action: "resume", Available: resume, Reason: "recorded status"},
		{Action: "onboard", Available: onboard, Reason: "create and start this Project's Mate"},
	}
}

// TestTheStartKeyLabelFollowsTheRecordedMateStatus is requirement 1+2+3: the
// one key does create / start / resume, and the key line says which.
func TestTheStartKeyLabelFollowsTheRecordedMateStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mate query.MateNode
		caps []query.ActionAvailability
		want string
	}{
		{
			name: "no mate",
			mate: absentMate("this Project has no Mate"),
			caps: mateCaps(true, false, false),
			want: "s Create mate",
		},
		{
			name: "created",
			mate: knownMate(query.MateCreated, query.HarnessClaude),
			caps: mateCaps(false, true, false),
			want: "s Start mate",
		},
		{
			name: "stopped",
			mate: knownMate(query.MateStopped, query.HarnessClaude),
			caps: mateCaps(false, false, true),
			want: "s Resume mate",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := projectFrame(t, mateTree(tc.mate, tc.caps))
			view := renderFrame(t, m)
			if !strings.Contains(view, tc.want) {
				t.Fatalf("key line does not offer %q:\n%s", tc.want, view)
			}
		})
	}
}

// TestCreatingAMateAsksWhichAgentToUse is the captain's requirement 2: the
// create path must let the user choose codex or claude, not silently take a
// configured default.
func TestCreatingAMateAsksWhichAgentToUse(t *testing.T) {
	t.Parallel()
	m, got := withRunner(projectFrame(t, mateTree(absentMate("this Project has no Mate"), mateCaps(true, false, false))), "Mate created", nil)
	m, cmd := send(t, m, key("s"))
	if cmd != nil {
		t.Fatalf("s dispatched before the agent was chosen: %v", cmd)
	}
	view := renderFrame(t, m)
	for _, want := range []string{"claude", "codex", "Esc"} {
		if !strings.Contains(view, want) {
			t.Fatalf("harness picker missing %q:\n%s", want, view)
		}
	}
	// claude is first; move to codex and take it.
	m, _ = send(t, m, key("down"))
	m, cmd = send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("choosing an agent did not dispatch the create")
	}
	m, _ = send(t, m, cmd())
	if len(*got) != 1 {
		t.Fatalf("runner calls = %d, want one", len(*got))
	}
	req := (*got)[0]
	if req.Action != ActionOnboard || req.Harness != query.HarnessCodex {
		t.Fatalf("request = %+v, want an onboard carrying codex", req)
	}
	if req.TargetKind != "project-mate" {
		t.Fatalf("request target kind = %q, want project-mate", req.TargetKind)
	}
}

// TestStartAndResumeDoNotAskForAnAgent: an existing Mate has a recorded
// harness. Only a create chooses one, and only a switch changes one.
func TestStartAndResumeDoNotAskForAnAgent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mate   query.MateNode
		caps   []query.ActionAvailability
		action Action
	}{
		{"created", knownMate(query.MateCreated, query.HarnessClaude), mateCaps(false, true, false), ActionStart},
		{"stopped", knownMate(query.MateStopped, query.HarnessClaude), mateCaps(false, false, true), ActionResume},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, got := withRunner(projectFrame(t, mateTree(tc.mate, tc.caps)), "ok", nil)
			m, cmd := send(t, m, key("s"))
			if cmd == nil {
				t.Fatalf("s did not dispatch for a %s Mate", tc.name)
			}
			m, _ = send(t, m, cmd())
			if len(*got) != 1 || (*got)[0].Action != tc.action {
				t.Fatalf("requests = %+v, want one %s", *got, tc.action)
			}
			if (*got)[0].Harness != "" {
				t.Fatalf("a %s carried a harness: %+v", tc.action, (*got)[0])
			}
		})
	}
}

// TODO(task 10): three tests lived here for the 'h' key - the
// harness-switch confirm on a live Mate, the same on a stopped one, and
// its refusal with no Mate. Restarting a Mate under another harness is
// mvp.md's task 10.

// TestMateKeysAreNotOfferedAwayFromAProjectScreen: the captain asked for
// these on a Project screen. A key line must never name a key that refuses.
func TestMateKeysAreNotOfferedAwayFromAProjectScreen(t *testing.T) {
	t.Parallel()
	m := loaded(t, sampleTree(), nil) // workspace frame
	view := renderFrame(t, m)
	for _, banned := range []string{"s Start mate", "s Create mate", "h Change harness"} {
		if strings.Contains(view, banned) {
			t.Fatalf("workspace key line offers %q:\n%s", banned, view)
		}
	}
}

// TestEscapeLeavesTheHarnessPickerWithoutRunningAnything.
func TestEscapeLeavesTheHarnessPickerWithoutRunningAnything(t *testing.T) {
	t.Parallel()
	m, got := withRunner(projectFrame(t, mateTree(absentMate("this Project has no Mate"), mateCaps(true, false, false))), "must not run", nil)
	m, _ = send(t, m, key("s"))
	m, cmd := send(t, m, key("esc"))
	if cmd != nil || len(*got) != 0 {
		t.Fatalf("esc ran something: cmd=%v calls=%d", cmd, len(*got))
	}
	if m.harnessPick || m.actions || m.confirm != nil {
		t.Fatalf("esc left a modal open: pick=%v menu=%v confirm=%v", m.harnessPick, m.actions, m.confirm != nil)
	}
}

var _ = context.Background
