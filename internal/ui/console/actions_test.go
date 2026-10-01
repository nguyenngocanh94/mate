package console

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The actions sheet (design F) and the confirm sheet (design G), as the
// ActionFunc sees them: dangerous actions never run without their own key
// pressed twice, refusals never reach the runner, and a failure reads
// differently from a refusal.

// actRunner builds a model on tree whose ActionFunc records every request
// and answers with reply and err.
func actRunner(t *testing.T, tree query.Snapshot, reply string, err error) (Model, *[]ActionRequest) {
	t.Helper()
	got := &[]ActionRequest{}
	m := loaded(t, tree, nil)
	m.action = func(_ context.Context, req ActionRequest) (string, error) {
		*got = append(*got, req)
		return reply, err
	}
	return m, got
}

// actEntry is the open sheet's entry with key k.
func actEntry(t *testing.T, m Model, k string) menuEntry {
	t.Helper()
	for _, e := range m.menu {
		if e.key == k {
			return e
		}
	}
	t.Fatalf("the actions sheet has no %q entry: %+v", k, m.menu)
	return menuEntry{}
}

// actFlat collapses a frame into one line of words, for "does it still
// say X" checks that must not care where a sentence wrapped.
func actFlat(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestDangerousActionAsksBeforeTheRunnerAndEnterCancels(t *testing.T) {
	m, got := actRunner(t, sampleTree(), "stop confirmed", nil)
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	if !m.actions || actEntry(t, m, "x").label != "Stop crew…" {
		t.Fatalf("a did not open the crew's sheet with its stop entry: %+v", m.menu)
	}
	m, cmd := send(t, m, key("x"))
	if cmd != nil || m.confirm == nil || len(*got) != 0 {
		t.Fatalf("x must open the confirmation without running: cmd=%v confirm=%v calls=%d", cmd != nil, m.confirm != nil, len(*got))
	}
	view := renderFrame(t, m)
	for _, want := range []string{"stop crew?", "object", "scope", "effect", "cancel", "x      stop crew", "worktree and branch"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation missing %q:\n%s", want, view)
		}
	}
	// Enter is the safe default: it cancels.
	m, cmd = send(t, m, key("enter"))
	if cmd != nil || m.confirm != nil || len(*got) != 0 {
		t.Fatalf("Enter on the confirmation ran or stayed: cmd=%v confirm=%v calls=%d", cmd != nil, m.confirm != nil, len(*got))
	}

	m, _ = send(t, m, key("a"))
	m, _ = send(t, m, key("x"))
	m, cmd = send(t, m, key("x"))
	if cmd == nil || len(*got) != 0 {
		t.Fatalf("the asking key should queue the runner: cmd=%v calls=%d", cmd != nil, len(*got))
	}
	m, _ = send(t, m, cmd())
	if len(*got) != 1 || (*got)[0].Action != ActionStop || (*got)[0].TargetKind != "crew" {
		t.Fatalf("runner requests = %+v, want one crew stop", *got)
	}
	// The crew is named within its Project, as `mate crew stop <project> <id>` names it.
	tree := sampleTree()
	if req := (*got)[0]; req.Target != tree.Projects[0].ProjectID || req.Crew != tree.Projects[0].Crews[1].CrewID {
		t.Fatalf("crew stop request = %+v, want Target %q and Crew %q", req, tree.Projects[0].ProjectID, tree.Projects[0].Crews[1].CrewID)
	}
	if m.msg.tone != toneOK || !strings.Contains(m.msg.text, "stop completed") {
		t.Fatalf("success message = %+v", m.msg)
	}
}

func TestOnlyTheAskingKeyConfirmsAndEscGoesBackToTheSheet(t *testing.T) {
	m, got := actRunner(t, sampleTree(), "", nil)
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	// Arrows and Enter reach the same entry the key does.
	m, _ = send(t, m, key("down"))
	if m.menu[m.actionIndex].key != "x" {
		t.Fatalf("second entry = %+v, want stop", m.menu[m.actionIndex])
	}
	m, _ = send(t, m, key("enter"))
	if m.confirm == nil || m.confirm.key != "x" {
		t.Fatalf("Enter on stop did not ask with x: %+v", m.confirm)
	}
	for _, k := range []string{"y", "p", "s", "M"} {
		var cmd tea.Cmd
		m, cmd = send(t, m, key(k))
		if cmd != nil || m.confirm == nil || len(*got) != 0 {
			t.Fatalf("%q confirmed a stop that x asked for: cmd=%v confirm=%v calls=%d", k, cmd != nil, m.confirm != nil, len(*got))
		}
	}
	m, cmd := send(t, m, key("esc"))
	if cmd != nil || m.confirm != nil || !m.actions || len(*got) != 0 {
		t.Fatalf("Esc must return to the sheet: cmd=%v confirm=%v sheet=%v calls=%d", cmd != nil, m.confirm != nil, m.actions, len(*got))
	}
}

func TestProjectRowStartDispatchesForEmptyAndPopulatedProjects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		repos query.Field[[]query.RepoValue]
		crews []query.CrewNode
	}{
		{name: "empty", repos: query.AbsentField[[]query.RepoValue]("no repo is registered in this project")},
		{name: "with-repo-and-worktree", repos: query.KnownField([]query.RepoValue{{RepoID: "repo_1", DisplayName: "repo", Path: "/repo", DefaultBranch: "main"}}), crews: []query.CrewNode{{CrewID: "crew_1", Task: "task", Worktree: query.KnownField(query.WorktreeValue{Path: "/worktree", Branch: "crew", Status: query.WorktreeRecordedCreated})}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := sampleTree()
			tree.Projects = tree.Projects[:1]
			tree.Projects[0].Repos = tc.repos
			tree.Projects[0].Crews = tc.crews
			tree.Projects[0].Mate.Designated = query.KnownField(query.MateIdentity{
				MateID: "mate_created", HarnessKind: query.HarnessKind("claude"), Status: query.MateCreated, IsDefault: true,
			})
			tree.Projects[0].Actions = []query.ActionAvailability{
				{Action: "start", Available: true, Reason: "Mate is recorded created"},
				{Action: "resume", Available: false, Reason: "Mate is recorded created"},
				{Action: "onboard", Available: false, Reason: "this Project already has a Mate"},
			}
			m, got := actRunner(t, tree, "started", nil)
			m, _ = send(t, m, key("a"))
			if e := actEntry(t, m, "s"); !e.enabled || e.label != "Start mate" {
				t.Fatalf("Project-row start entry = %+v, want an enabled Start mate", e)
			}
			m, cmd := send(t, m, key("s"))
			if cmd == nil {
				t.Fatal("enabled Project-row start did not dispatch")
			}
			m, _ = send(t, m, cmd())
			if len(*got) != 1 || (*got)[0].Action != ActionStart || (*got)[0].Target != tree.Projects[0].ProjectID || (*got)[0].TargetKind != "mate" {
				t.Fatalf("start requests = %+v, want one Mate start for the Project", *got)
			}
		})
	}
}

// The workspace-level new project is the one door off an empty workspace
// (ADR 0023). Selecting an existing Project must not turn it into "start
// this Project's Mate": the sheet's n entry keys off the frame, not the row.
func TestWorkspaceNewProjectAddsAProjectWhenEmptyAndWhenNotEmpty(t *testing.T) {
	for _, tc := range []struct {
		name     string
		projects []query.ProjectNode
		open     []string
	}{
		{name: "empty-workspace", open: []string{"n"}},
		{name: "existing-project-selected", projects: []query.ProjectNode{{
			ProjectID: "prj_existing", Name: "existing",
			Mate: absentMate("this project has no designated Mate"),
		}}, open: []string{"a", "n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := query.Snapshot{
				WorkspaceID: "ws_acme",
				Harnesses:   testHarnesses,
				Workspace:   query.KnownField(query.WorkspaceValue{Name: "acme", Root: "/work/acme"}),
				Projects:    tc.projects,
			}
			m, got := actRunner(t, tree, "created prj_new", nil)
			var cmd tea.Cmd
			for _, k := range tc.open {
				m, cmd = send(t, m, key(k))
			}
			if cmd != nil || !m.actionInputMode {
				t.Fatalf("new project must open the name field, not dispatch: cmd=%v input=%v", cmd != nil, m.actionInputMode)
			}
			m = actType(t, m, "new-project")
			m, _ = send(t, m, key("enter")) // on to the repo field
			m = actType(t, m, "new-project")
			m, cmd = send(t, m, key("enter"))
			if cmd == nil {
				t.Fatal("submitting the project did not dispatch the runner")
			}
			m, _ = send(t, m, cmd())
			if len(*got) != 1 || (*got)[0].Action != ActionOnboard || (*got)[0].TargetKind != "workspace" ||
				(*got)[0].Input != "new-project" || (*got)[0].Repo != "new-project" {
				t.Fatalf("onboard requests = %+v, want one workspace-level create with the typed name", *got)
			}
		})
	}
}

// An entry that cannot run stays in its place, says why on its own row,
// and pressing it does nothing - the runner is never reached.
func TestUnavailableEntryStaysInPlaceAndDoesNotRun(t *testing.T) {
	m, got := actRunner(t, sampleTree(), "", errors.New("must not run"))
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	repair := actEntry(t, m, "p")
	if repair.enabled || repair.reason == "" {
		t.Fatalf("repair on an active binding = %+v, want disabled with a reason", repair)
	}
	// The third crew entry is repair: drawn with · in the key column and
	// its reason on the right.
	view := renderFrame(t, m)
	if !strings.Contains(view, "·      Repair binding…") || !strings.Contains(view, "no recorded") {
		t.Fatalf("the disabled entry is not drawn in place with · and its reason:\n%s", view)
	}
	for _, k := range []string{"p"} {
		var cmd tea.Cmd
		m, cmd = send(t, m, key(k))
		if cmd != nil || len(*got) != 0 || m.confirm != nil {
			t.Fatalf("unavailable %s ran: cmd=%v calls=%d confirm=%v", k, cmd != nil, len(*got), m.confirm != nil)
		}
	}
	// Enter on it does nothing either.
	for m.menu[m.actionIndex].key != "p" {
		m, _ = send(t, m, key("down"))
	}
	m, cmd := send(t, m, key("enter"))
	if cmd != nil || len(*got) != 0 || m.confirm != nil || !m.actions {
		t.Fatalf("Enter on a disabled entry acted: cmd=%v calls=%d confirm=%v sheet=%v", cmd != nil, len(*got), m.confirm != nil, m.actions)
	}
}

// An Unknown binding read must not be reported as the established fact "no
// stale binding is recorded": the read failed, and the reason says so.
func TestRepairEntryWithUnknownBindingSaysTheReadIsUnknown(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Crews = []query.CrewNode{
		{CrewID: "crew_target", Status: query.CrewWorking, Binding: query.UnknownField[query.BindingValue]("binding lookup timed out (2s)")},
	}
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter")) // the Project
	m, _ = send(t, m, key("down"))  // its one Crew
	m, _ = send(t, m, key("a"))
	repair := actEntry(t, m, "p")
	if repair.enabled {
		t.Fatalf("repair with an unknown binding = %+v, want refused", repair)
	}
	for _, s := range []string{repair.reason, repair.about} {
		if strings.Contains(s, "no stale binding is recorded") {
			t.Fatalf("repair says %q, a fact the failed read never established", s)
		}
	}
	if !strings.Contains(repair.reason, "unknown") {
		t.Fatalf("repair reason = %q, want it to say the binding read is unknown", repair.reason)
	}
	// The right column cuts a long reason; its head says what went wrong.
	if view := renderFrame(t, m); !strings.Contains(view, "·      Repair binding…  binding is u") {
		t.Fatalf("the sheet does not show why repair is refused:\n%s", view)
	}
}

func TestActionFailureIsNotReportedAsRefusal(t *testing.T) {
	m, _ := actRunner(t, sampleTree(), "application service diagnostics: refused by runtime", errors.New("runtime unavailable"))
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	m, _ = send(t, m, key("x"))
	m, cmd := send(t, m, key("x"))
	if cmd == nil {
		t.Fatal("confirmed action did not queue the service")
	}
	m, _ = send(t, m, cmd())
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "Action failed") || strings.Contains(m.msg.text, "nothing started") {
		t.Fatalf("action failure = %+v, want an attempted failure distinct from a refusal", m.msg)
	}
	if view := renderFrame(t, m); !strings.Contains(view, "! Action failed") {
		t.Fatalf("the status line does not carry the failure:\n%s", view)
	}
}

// Restarting a Mate and clearing its composer live on the Mate row's sheet;
// the restart keeps the confirmation every dangerous action gets.
func TestTheMateRowSheetCarriesTheRecoveryActions(t *testing.T) {
	m, got := actRunner(t, sampleTree(), "restarted", nil)
	m, _ = send(t, m, key("enter")) // the Project; the Mate row is first
	if r, ok := m.selectedRow(); !ok || r.kind != rowMate {
		t.Fatalf("setup: selected row = %+v, want the Mate row", r)
	}
	m, _ = send(t, m, key("a"))
	view := renderFrame(t, m)
	for _, want := range []string{"Restart mate…", "Clear composer"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the Mate row's sheet does not offer %q:\n%s", want, view)
		}
	}
	if e := actEntry(t, m, "R"); !e.enabled || !e.confirms() {
		t.Fatalf("restart entry = %+v, want enabled and confirming", e)
	}
	m, cmd := send(t, m, key("R"))
	if cmd != nil || m.confirm == nil {
		t.Fatalf("restart ran without its confirmation (cmd=%v confirm=%v)", cmd != nil, m.confirm != nil)
	}
	if !strings.Contains(renderFrame(t, m), "restart mate?") {
		t.Fatalf("the confirmation does not ask to restart:\n%s", renderFrame(t, m))
	}
	m, cmd = send(t, m, key("R"))
	if cmd == nil {
		t.Fatal("the answered confirmation ran nothing")
	}
	m, _ = send(t, m, cmd())
	if len(*got) != 1 || (*got)[0].Action != ActionRestartMate || (*got)[0].Target != m.currentProject().ProjectID {
		t.Fatalf("requests = %+v, want one restart of the open project", *got)
	}
}

// Those two entries are the Mate's; on a crew they would offer to restart
// something the row does not name. The crew row's own `R` is
// ActionRestartCrew, a different action on a different object.
func TestACrewRowSheetHasNoMateRecoveryActions(t *testing.T) {
	m := toRunningAttempt(t, loaded(t, sampleTree(), nil))
	m, _ = send(t, m, key("a"))
	sawCrewRestart := false
	for _, e := range m.menu {
		if e.choice.action == ActionRestartMate || e.choice.action == ActionClearComposer {
			t.Fatalf("a crew row's sheet offers %+v", e)
		}
		if e.key == "R" {
			if e.choice.action != ActionRestartCrew {
				t.Fatalf("a crew row's R entry = %+v, want ActionRestartCrew", e)
			}
			sawCrewRestart = true
		}
	}
	if !sawCrewRestart {
		t.Fatal("a crew row's sheet does not offer Restart crew…")
	}
}

// The crew row's Restart crew… entry is dangerous and runs
// ActionRestartCrew against the Project and the Crew the row names.
func TestTheCrewRowSheetRestartsTheCrew(t *testing.T) {
	m, got := actRunner(t, sampleTree(), "crew is running again", nil)
	m = toRunningAttempt(t, m)
	crewID := sampleTree().Projects[0].Crews[1].CrewID
	projectID := sampleTree().Projects[0].ProjectID
	m, _ = send(t, m, key("a"))
	e := actEntry(t, m, "R")
	if !e.enabled || !e.confirms() {
		t.Fatalf("restart crew entry = %+v, want enabled and confirming", e)
	}
	m, cmd := send(t, m, key("R"))
	if cmd != nil || m.confirm == nil {
		t.Fatalf("restart crew ran without its confirmation (cmd=%v confirm=%v)", cmd != nil, m.confirm != nil)
	}
	m, cmd = send(t, m, key("R"))
	if cmd == nil {
		t.Fatal("the answered confirmation ran nothing")
	}
	m, _ = send(t, m, cmd())
	if len(*got) != 1 {
		t.Fatalf("requests = %+v, want exactly one", *got)
	}
	req := (*got)[0]
	if req.Action != ActionRestartCrew || req.Target != projectID || req.Crew != crewID {
		t.Fatalf("request = %+v, want restart_crew of %s/%s", req, projectID, crewID)
	}
}

// A closed crew's task is over: the entry is withheld rather than offered
// and then refused by `crew relaunch` after the captain confirmed it.
func TestRestartCrewIsUnavailableForAClosedCrew(t *testing.T) {
	tree := sampleTree()
	crew := &tree.Projects[0].Crews[1]
	crew.Status, crew.Closed = query.CrewFinished, true
	if !crew.Worktree.IsKnown() || crew.Worktree.Value.Branch == "" {
		t.Fatalf("setup: the crew must record a branch, or the refusal proves nothing: %+v", crew.Worktree)
	}
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	c := m.restartCrewChoice(row{kind: rowCrew, id: crew.CrewID})
	if c.enabled {
		t.Fatalf("restart crew on a closed crew = %+v, want unavailable", c)
	}
	if !strings.Contains(c.desc, "closed") {
		t.Fatalf("restart crew reason = %q, want it to say the crew is closed", c.desc)
	}
}

// Each kind has one fixed order, so muscle memory holds across objects.
func TestTheSheetOrderIsFixedPerKind(t *testing.T) {
	keys := func(m Model) string {
		var ks []string
		for _, e := range m.menu {
			ks = append(ks, e.key)
		}
		return strings.Join(ks, " ")
	}
	m := loaded(t, sampleTree(), nil)
	m, _ = send(t, m, key("a"))
	if got := keys(m); got != "enter s m n" {
		t.Fatalf("project row sheet = %q", got)
	}
	m, _ = send(t, m, key("esc"))
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("a"))
	if got := keys(m); got != "enter s x R m C y" {
		t.Fatalf("mate row sheet = %q", got)
	}
	m, _ = send(t, m, key("esc"))
	m, _ = send(t, m, key("down"))
	m, _ = send(t, m, key("a"))
	if got := keys(m); got != "enter x p R M d y" {
		t.Fatalf("crew row sheet = %q", got)
	}
}
