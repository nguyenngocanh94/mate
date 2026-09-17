package console

import (
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/domain"
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
	tree.Projects[0].Tasks = nil
	mate.Actions = actions
	tree.Projects[0].Mate = mate
	return tree
}

func knownMate(status domain.MateStatus, kind domain.HarnessKind) query.MateNode {
	return query.MateNode{
		Designated: query.KnownField(query.MateIdentity{MateID: "mate_1", HarnessKind: kind, Status: status}),
		AgentName:  query.AbsentField[string]("no binding"),
		Binding:    query.AbsentField[query.BindingValue]("no binding is recorded"),
		LastEvent:  query.AbsentField[query.EventValue]("no event"),
		Error:      query.AbsentField[query.ErrorReason](notErrorState),
	}
}

func mateCaps(onboard, start, resume, switchH bool) []query.ActionAvailability {
	return []query.ActionAvailability{
		{Action: "start", Available: start, Reason: "recorded status"},
		{Action: "stop", Available: false, Reason: "no active binding to stop"},
		{Action: "resume", Available: resume, Reason: "recorded status"},
		{Action: "onboard", Available: onboard, Reason: "create and start this Project's Mate"},
		{Action: "switch_harness", Available: switchH, Reason: "restart this Mate under a different harness"},
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
			caps: mateCaps(true, false, false, false),
			want: "s Create mate",
		},
		{
			name: "created",
			mate: knownMate(domain.MateCreated, domain.HarnessClaude),
			caps: mateCaps(false, true, false, true),
			want: "s Start mate",
		},
		{
			name: "stopped",
			mate: knownMate(domain.MateStopped, domain.HarnessClaude),
			caps: mateCaps(false, false, true, true),
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
			if !strings.Contains(view, "h Change harness") && tc.name != "no mate" {
				t.Fatalf("key line does not offer the harness key:\n%s", view)
			}
		})
	}
}

// TestCreatingAMateAsksWhichAgentToUse is the captain's requirement 2: the
// create path must let the user choose codex or claude, not silently take a
// configured default.
func TestCreatingAMateAsksWhichAgentToUse(t *testing.T) {
	t.Parallel()
	m, got := withRunner(projectFrame(t, mateTree(absentMate("this Project has no Mate"), mateCaps(true, false, false, false))), "Mate created", nil)
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
	if req.Action != ActionOnboard || req.Harness != domain.HarnessCodex {
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
		{"created", knownMate(domain.MateCreated, domain.HarnessClaude), mateCaps(false, true, false, true), ActionStart},
		{"stopped", knownMate(domain.MateStopped, domain.HarnessClaude), mateCaps(false, false, true, true), ActionResume},
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

// TestSwitchingHarnessAsksThenConfirmsBeforeDestroyingThePanel is the
// captain's modal: the user is told the old panel and its harness session
// are destroyed and unsaved work is lost, and nothing runs until they accept.
func TestSwitchingHarnessAsksThenConfirmsBeforeDestroyingThePanel(t *testing.T) {
	t.Parallel()
	tree := sampleTree()
	tree.Projects = tree.Projects[:1]
	tree.Projects[0].Tasks = nil
	tree.Projects[0].Mate.Actions = mateCaps(false, false, false, true)
	m, got := withRunner(projectFrame(t, tree), "switched", nil)

	m, cmd := send(t, m, key("h"))
	if cmd != nil {
		t.Fatalf("h dispatched before anything was chosen: %v", cmd)
	}
	m, _ = send(t, m, key("down")) // codex
	m, cmd = send(t, m, key("enter"))
	if cmd != nil || len(*got) != 0 {
		t.Fatalf("choosing an agent ran the switch without a confirmation: cmd=%v calls=%d", cmd, len(*got))
	}
	view := renderFrame(t, m)
	for _, want := range []string{"CONFIRM", "Object", "Scope", "Effect", "destroyed", "unsaved"} {
		if !strings.Contains(flattenWrap(view), want) {
			t.Fatalf("confirmation missing %q:\n%s", want, view)
		}
	}
	m, cmd = send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("the confirmation did not dispatch the switch")
	}
	m, _ = send(t, m, cmd())
	if len(*got) != 1 {
		t.Fatalf("runner calls = %d, want one", len(*got))
	}
	if (*got)[0].Action != ActionSwitchHarness || (*got)[0].Harness != domain.HarnessCodex {
		t.Fatalf("request = %+v, want a switch_harness carrying codex", (*got)[0])
	}
}

// TestSwitchingHarnessOnAStoppedMateDoesNotClaimAPanelWillBeDestroyed is the
// package's standing rule: never assert more than the recorded state says.
func TestSwitchingHarnessOnAStoppedMateDoesNotClaimAPanelWillBeDestroyed(t *testing.T) {
	t.Parallel()
	m := projectFrame(t, mateTree(knownMate(domain.MateStopped, domain.HarnessClaude), mateCaps(false, false, true, true)))
	m, _ = send(t, m, key("h"))
	m, _ = send(t, m, key("enter")) // claude, same kind: still a restart
	flat := flattenWrap(renderFrame(t, m))
	if !strings.Contains(flat, "No live panel is recorded") {
		t.Fatalf("confirmation does not say there is no live panel:\n%s", flat)
	}
	if strings.Contains(flat, "unsaved") {
		t.Fatalf("confirmation warns about unsaved work with nothing live:\n%s", flat)
	}
}

// TestTheHarnessKeyIsRefusedWithNoMate: h has nothing to restart, and the
// refusal must point at the key that does create one.
func TestTheHarnessKeyIsRefusedWithNoMate(t *testing.T) {
	t.Parallel()
	m, got := withRunner(projectFrame(t, mateTree(absentMate("this Project has no Mate"), mateCaps(true, false, false, false))), "must not run", nil)
	m, cmd := send(t, m, key("h"))
	if cmd != nil || len(*got) != 0 {
		t.Fatalf("h ran something with no Mate: cmd=%v calls=%d", cmd, len(*got))
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "nothing started") {
		t.Fatalf("refusal = %+v", m.msg)
	}
	if !strings.Contains(m.msg.text, "s ") {
		t.Fatalf("refusal %q does not point at the key that creates a Mate", m.msg.text)
	}
}

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
	m, got := withRunner(projectFrame(t, mateTree(absentMate("this Project has no Mate"), mateCaps(true, false, false, false))), "must not run", nil)
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
