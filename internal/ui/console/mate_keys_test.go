package console

import (
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The Mate keys on a Project: s creates, starts or resumes the Mate - one
// key, and the sheet says which - and m flips the Project's mode. Creating
// a Mate asks which harness to use; an existing Mate keeps its own.

// actMateTree is one Project whose Mate is in the given state, with the
// capabilities query.mateActions would author for it.
func actMateTree(mate query.MateNode, actions []query.ActionAvailability) query.Snapshot {
	tree := sampleTree()
	tree.Projects = tree.Projects[:1]
	tree.Projects[0].Crews = nil
	mate.Actions = actions
	tree.Projects[0].Mate = mate
	return tree
}

func actKnownMate(status query.MateStatus, kind query.HarnessKind) query.MateNode {
	return query.MateNode{
		Designated: query.KnownField(query.MateIdentity{MateID: "mate_1", HarnessKind: kind, Status: status}),
		AgentName:  query.AbsentField[string]("no binding"),
		Binding:    query.AbsentField[query.BindingValue]("no binding is recorded"),
		LastEvent:  query.AbsentField[query.EventValue]("no event"),
		Error:      query.AbsentField[query.ErrorReason](notErrorState),
	}
}

func actMateCaps(onboard, start, resume bool) []query.ActionAvailability {
	return []query.ActionAvailability{
		{Action: "start", Available: start, Reason: "recorded status"},
		{Action: "stop", Available: false, Reason: "no active binding to stop"},
		{Action: "resume", Available: resume, Reason: "recorded status"},
		{Action: "onboard", Available: onboard, Reason: "create and start this Project's Mate"},
	}
}

func TestTheStartEntryFollowsTheRecordedMateStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		mate query.MateNode
		caps []query.ActionAvailability
		want string
	}{
		{"no mate", absentMate("this Project has no Mate"), actMateCaps(true, false, false), "Create mate…"},
		{"created", actKnownMate(query.MateCreated, query.HarnessKind("claude")), actMateCaps(false, true, false), "Start mate"},
		{"stopped", actKnownMate(query.MateStopped, query.HarnessKind("claude")), actMateCaps(false, false, true), "Resume mate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := projectFrame(t, actMateTree(tc.mate, tc.caps))
			m, _ = send(t, m, key("a"))
			if e := actEntry(t, m, "s"); !e.enabled || e.label != tc.want {
				t.Fatalf("start entry = %+v, want enabled %q", e, tc.want)
			}
			if !strings.Contains(renderFrame(t, m), tc.want) {
				t.Fatalf("the sheet does not draw %q:\n%s", tc.want, renderFrame(t, m))
			}
		})
	}
}

// A running Mate has nothing to start: the entry stays, with its reason.
func TestTheStartEntryOnARunningMateSaysWhyNot(t *testing.T) {
	m, got := actRunner(t, sampleTree(), "must not run", nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("a"))
	e := actEntry(t, m, "s")
	if e.enabled || !strings.Contains(e.reason, "running") {
		t.Fatalf("start on a running Mate = %+v, want disabled naming the recorded status", e)
	}
	m, cmd := send(t, m, key("s"))
	if cmd != nil || len(*got) != 0 {
		t.Fatalf("s on a running Mate ran: cmd=%v calls=%d", cmd != nil, len(*got))
	}
	_ = m
}

// Creating a Mate lets the captain choose codex or claude, never silently
// a configured default.
func TestCreatingAMateAsksWhichHarnessToUse(t *testing.T) {
	m, got := actRunner(t, actMateTree(absentMate("this Project has no Mate"), actMateCaps(true, false, false)), "Mate created", nil)
	m, _ = send(t, m, key("enter"))
	m, cmd := send(t, m, key("s"))
	if cmd != nil || !m.harnessPick {
		t.Fatalf("s dispatched before the harness was chosen: cmd=%v pick=%v", cmd != nil, m.harnessPick)
	}
	view := renderFrame(t, m)
	for _, want := range []string{"harness", "claude", "codex", "esc"} {
		if !strings.Contains(view, want) {
			t.Fatalf("harness picker missing %q:\n%s", want, view)
		}
	}
	m, _ = send(t, m, key("down")) // claude is first; take codex
	m, cmd = send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("choosing a harness did not dispatch the create")
	}
	_, _ = send(t, m, cmd())
	if len(*got) != 1 {
		t.Fatalf("runner calls = %d, want one", len(*got))
	}
	if req := (*got)[0]; req.Action != ActionOnboard || req.Harness != query.HarnessKind("codex") || req.TargetKind != "project-mate" {
		t.Fatalf("request = %+v, want a project-mate onboard carrying codex", req)
	}
}

// An existing Mate has a recorded harness: start and resume never ask.
func TestStartAndResumeDoNotAskForAHarness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mate   query.MateNode
		caps   []query.ActionAvailability
		action Action
	}{
		{"created", actKnownMate(query.MateCreated, query.HarnessKind("claude")), actMateCaps(false, true, false), ActionStart},
		{"stopped", actKnownMate(query.MateStopped, query.HarnessKind("claude")), actMateCaps(false, false, true), ActionResume},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, got := actRunner(t, actMateTree(tc.mate, tc.caps), "ok", nil)
			m, _ = send(t, m, key("enter"))
			m, cmd := send(t, m, key("s"))
			if cmd == nil || m.harnessPick {
				t.Fatalf("s did not dispatch straight for a %s Mate: cmd=%v pick=%v", tc.name, cmd != nil, m.harnessPick)
			}
			_, _ = send(t, m, cmd())
			if len(*got) != 1 || (*got)[0].Action != tc.action || (*got)[0].Harness != "" {
				t.Fatalf("requests = %+v, want one %s with no harness", *got, tc.action)
			}
		})
	}
}

func TestEscapeLeavesTheHarnessPickerWithoutRunningAnything(t *testing.T) {
	m, got := actRunner(t, actMateTree(absentMate("this Project has no Mate"), actMateCaps(true, false, false)), "must not run", nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("s"))
	m, cmd := send(t, m, key("esc"))
	if cmd != nil || len(*got) != 0 {
		t.Fatalf("esc ran something: cmd=%v calls=%d", cmd != nil, len(*got))
	}
	if m.harnessPick || m.actions || m.confirm != nil {
		t.Fatalf("esc left a sheet open: pick=%v sheet=%v confirm=%v", m.harnessPick, m.actions, m.confirm != nil)
	}
}

// m flips the open Project's mode, and on the workspace the selected one's.
func TestTheModeKeyFlipsTheProjectsMode(t *testing.T) {
	for _, tc := range []struct {
		name  string
		enter bool
	}{{"project", true}, {"workspace", false}} {
		t.Run(tc.name, func(t *testing.T) {
			m, got := actRunner(t, sampleTree(), "mode auto", nil)
			if tc.enter {
				m, _ = send(t, m, key("enter"))
			}
			m, cmd := send(t, m, key("m"))
			if cmd == nil {
				t.Fatal("m did not dispatch")
			}
			_, _ = send(t, m, cmd())
			want := sampleTree().Projects[0].ProjectID
			if len(*got) != 1 || (*got)[0].Action != ActionMode || (*got)[0].Target != want || (*got)[0].TargetKind != "project" {
				t.Fatalf("requests = %+v, want one mode flip of %s", *got, want)
			}
		})
	}
}

// The mode entry says where it flips to.
func TestTheModeEntryNamesTheModeItFlipsTo(t *testing.T) {
	m, _ := actRunner(t, sampleTree(), "", nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("a"))
	if e := actEntry(t, m, "m"); e.label != "Mode → auto" || !e.enabled {
		t.Fatalf("mode entry on a manual Project = %+v", e)
	}
}

// The picker starts from the workspace's own default Mate harness
// (workspace.yaml's `mate_harness`, carried on the snapshot): it is listed
// first and the cursor starts on it, so Enter alone creates the Mate the
// workspace is configured for. A workspace that names no known kind keeps
// the built-in order, and so does one that names a harness that cannot run
// a Mate: the catalog draws it, the picker never offers it.
func TestTheHarnessPickerPutsTheWorkspaceDefaultFirst(t *testing.T) {
	for _, tc := range []struct {
		name   string
		def    query.HarnessKind
		golden string
		want   []query.HarnessKind
	}{
		{"claude", query.HarnessKind("claude"), "harness-picker-claude-default-40x36", []query.HarnessKind{query.HarnessKind("claude"), query.HarnessKind("codex")}},
		{"codex", query.HarnessKind("codex"), "harness-picker-codex-default-40x36", []query.HarnessKind{query.HarnessKind("codex"), query.HarnessKind("claude")}},
		{"none", "", "", []query.HarnessKind{query.HarnessKind("claude"), query.HarnessKind("codex")}},
		{"crew-only", query.HarnessKind("pi"), "", []query.HarnessKind{query.HarnessKind("claude"), query.HarnessKind("codex")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := actMateTree(absentMate("this Project has no Mate"), actMateCaps(true, false, false))
			tree.Workspace.Value.MateHarness = tc.def
			m, got := actRunner(t, tree, "Mate created", nil)
			m, _ = send(t, m, key("enter"))
			m, _ = send(t, m, key("s"))
			if !m.harnessPick {
				t.Fatal("setup: s did not open the harness picker")
			}
			if order := m.harnessOrder(); !slices.Equal(order, tc.want) {
				t.Fatalf("picker order = %v, want %v", order, tc.want)
			}
			view := renderFrame(t, m)
			if first, second := strings.Index(view, string(tc.want[0])), strings.Index(view, string(tc.want[1])); first < 0 || second < 0 || first > second {
				t.Fatalf("the sheet does not draw %s above %s:\n%s", tc.want[0], tc.want[1], view)
			}
			if tc.golden != "" {
				assertGolden(t, tc.golden, view)
			}
			m, cmd := send(t, m, key("enter"))
			if cmd == nil {
				t.Fatal("Enter on the picker dispatched nothing")
			}
			_, _ = send(t, m, cmd())
			if len(*got) != 1 || (*got)[0].Harness != tc.want[0] {
				t.Fatalf("requests = %+v, want one create carrying %s", *got, tc.want[0])
			}
		})
	}
}
