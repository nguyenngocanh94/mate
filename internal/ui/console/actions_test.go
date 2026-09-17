package console

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/domain"
	"github.com/nguyenngocanh94/matev2/internal/query"
)

func TestDangerousActionRequiresConfirmationBeforeRunner(t *testing.T) {
	calls := 0
	var got ActionRequest
	m := New(func(context.Context) (query.Snapshot, error) { return sampleTree(), nil }, nil,
		func(_ context.Context, req ActionRequest) (string, error) {
			calls++
			got = req
			return "stop confirmed", nil
		})
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m, _ = send(t, m, m.Init()())
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	m, _ = send(t, m, key("down")) // stop is the second menu item
	if m.actionIndex != 1 {
		t.Fatalf("action index = %d, want stop entry", m.actionIndex)
	}
	m, cmd := send(t, m, key("enter"))
	if cmd != nil || m.confirm == nil {
		t.Fatalf("dangerous action must open confirmation without running: cmd=%v confirm=%+v", cmd, m.confirm)
	}
	if calls != 0 {
		t.Fatalf("runner called before confirmation: %d", calls)
	}
	view := renderFrame(t, m)
	for _, want := range []string{"CONFIRM stop?", "Object", "Scope", "Effect", "Enter stop"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation view missing %q:\n%s", want, view)
		}
	}
	m, cmd = send(t, m, key("enter"))
	if cmd == nil || calls != 0 {
		t.Fatalf("confirmation should queue runner: cmd=%v calls=%d", cmd, calls)
	}
	m, _ = send(t, m, cmd())
	if calls != 1 || got.Action != ActionStop || got.TargetKind != "crew" {
		t.Fatalf("runner request = %+v calls=%d", got, calls)
	}
	if m.msg.tone != toneOK || !strings.Contains(m.msg.text, "stop completed") {
		t.Fatalf("success message = %+v", m.msg)
	}
}

func TestProjectRowStartDispatchesForEmptyAndPopulatedProjects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		repos query.Field[[]query.RepoValue]
		tasks []query.TaskNode
	}{
		{name: "empty", repos: query.AbsentField[[]query.RepoValue]("no repo is registered in this project")},
		{name: "with-repo-and-worktree", repos: query.KnownField([]query.RepoValue{{RepoID: "repo_1", DisplayName: "repo", Path: "/repo", DefaultBranch: "main"}}), tasks: []query.TaskNode{{TaskID: "task_1", Title: "task", Crews: []query.CrewNode{{CrewID: "crew_1", Worktree: query.KnownField(query.WorktreeValue{Path: "/worktree", Branch: "crew", Status: query.WorktreeRecordedCreated})}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := sampleTree()
			tree.Projects = tree.Projects[:1]
			tree.Projects[0].Repos = tc.repos
			tree.Projects[0].Tasks = tc.tasks
			tree.Projects[0].Mate.Designated = query.KnownField(query.MateIdentity{
				MateID: "mate_created", HarnessKind: domain.HarnessClaude, Status: domain.MateCreated, IsDefault: true,
			})
			tree.Projects[0].Actions = []query.ActionAvailability{
				{Action: "start", Available: true, Reason: "Mate is recorded created"},
				{Action: "resume", Available: false, Reason: "Mate is recorded created"},
				{Action: "onboard", Available: false, Reason: "this Project already has a Mate"},
			}

			calls := 0
			var got ActionRequest
			m := loaded(t, tree, nil)
			// loaded() uses a read-only model; install the action runner on it
			// so this follows the same workspace -> Project-row path as the
			// real Console.
			m.action = func(_ context.Context, req ActionRequest) (string, error) {
				calls++
				got = req
				return "started", nil
			}
			m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
			m, _ = send(t, m, key("a"))
			start := m.actionChoices[0]
			if !start.enabled || start.action != ActionStart || start.req.TargetKind != "mate" {
				t.Fatalf("Project-row start choice = %+v, want enabled Mate target", start)
			}
			m, cmd := send(t, m, key("enter"))
			if cmd == nil {
				t.Fatal("enabled Project-row start did not dispatch")
			}
			m, _ = send(t, m, cmd())
			if calls != 1 || got.Action != ActionStart || got.Target != tree.Projects[0].ProjectID || got.TargetKind != "mate" {
				t.Fatalf("start request = %+v calls=%d, want one Mate start for the Project", got, calls)
			}
		})
	}
}

// TestWorkspaceOnboardAddsAProjectWhenEmptyAndWhenNotEmpty pins the one path
// off an empty workspace (ADR 0023 dropped the default Project, so this is
// now the only door): "a" -> onboard -> type a name -> enter must create a
// Project via ActionRequest{Action: ActionOnboard, TargetKind: "workspace"},
// both when the workspace has no Projects at all (no row is selected, so
// selectedRow returns false) and when it already has one and a Project row
// is selected (onboardChoice keys off the current *frame*, frameWorkspace,
// not off which row is selected - selecting an existing Project must not
// hijack "onboard" into "start this Project's Mate").
func TestWorkspaceOnboardAddsAProjectWhenEmptyAndWhenNotEmpty(t *testing.T) {
	for _, tc := range []struct {
		name     string
		projects []query.ProjectNode
	}{
		{name: "empty-workspace"},
		{name: "existing-project-selected", projects: []query.ProjectNode{{
			ProjectID: "prj_existing", Name: "existing",
			Mate: absentMate("this project has no designated Mate"),
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := query.Snapshot{
				WorkspaceID: "ws_acme",
				Workspace:   query.KnownField(query.WorkspaceValue{Name: "acme", Root: "/work/acme"}),
				Projects:    tc.projects,
			}
			calls := 0
			var got ActionRequest
			m := New(func(context.Context) (query.Snapshot, error) { return tree, nil }, nil,
				func(_ context.Context, req ActionRequest) (string, error) {
					calls++
					got = req
					return "created prj_new", nil
				})
			m.p = plainPalette()
			m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
			m, _ = send(t, m, m.Init()())

			m, _ = send(t, m, key("a"))
			onboard := m.actionChoices[len(m.actionChoices)-1]
			if !onboard.enabled || onboard.action != ActionOnboard || onboard.req.TargetKind != "workspace" {
				t.Fatalf("onboard choice = %+v, want an enabled workspace-level onboard", onboard)
			}
			m.actionIndex = len(m.actionChoices) - 1
			m, cmd := send(t, m, key("enter"))
			if cmd != nil || !m.actionInputMode {
				t.Fatalf("onboard at workspace level must open the name prompt, not dispatch immediately: cmd=%v inputMode=%v", cmd, m.actionInputMode)
			}
			for _, r := range "new-project" {
				m, _ = send(t, m, key(string(r)))
			}
			m, cmd = send(t, m, key("enter"))
			if cmd == nil {
				t.Fatal("submitting the project name did not dispatch the runner")
			}
			m, _ = send(t, m, cmd())
			if calls != 1 || got.Action != ActionOnboard || got.TargetKind != "workspace" || got.Input != "new-project" {
				t.Fatalf("onboard request = %+v calls=%d, want a workspace-level create with the typed name", got, calls)
			}
		})
	}
}

func TestUnavailableActionIsRefusalAndDoesNotRun(t *testing.T) {
	calls := 0
	m := New(func(context.Context) (query.Snapshot, error) { return sampleTree(), nil }, nil,
		func(context.Context, ActionRequest) (string, error) {
			calls++
			return "", errors.New("must not run")
		})
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m, _ = send(t, m, m.Init()())
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	// Repair is unavailable for the active Crew, so move to it.
	for i := 0; i < 4; i++ {
		m, _ = send(t, m, key("down"))
	}
	m, cmd := send(t, m, key("enter"))
	if cmd != nil || calls != 0 {
		t.Fatalf("unavailable action ran: cmd=%v calls=%d", cmd, calls)
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "nothing started") {
		t.Fatalf("refusal message = %+v", m.msg)
	}
}

func TestConfirmationFrameSanitizesHostileRecordedIdentity(t *testing.T) {
	tree := hostileTree()
	tree.Projects[0].Tasks[0].Crews[0].CrewID = "crew_\n修正\x1b[31m\x85"
	tree.Projects[0].Tasks[0].Crews[0].Binding = query.KnownField(query.BindingValue{Status: query.BindingStale})
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter")) // Project
	m, _ = send(t, m, key("down"))  // first Task
	m, _ = send(t, m, key("enter")) // Crew attempts
	m, _ = send(t, m, key("a"))
	m.actionIndex = 4 // repair; first hostile Crew is needs_repair
	m, _ = send(t, m, key("enter"))
	if m.confirm == nil {
		t.Fatalf("repair for hostile needs_repair Crew did not ask for confirmation")
	}
	for _, size := range [][2]int{{120, 36}, {80, 24}, {60, 16}} {
		m, _ = send(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		frame := renderFrame(t, m)
		assertFrameShape(t, frame, size[0], size[1])
		assertNoRawControlChars(t, frame)
		if !strings.Contains(frame, "CONFIRM repair?") || !strings.Contains(frame, "Object") {
			t.Fatalf("confirmation payload was not rendered at %dx%d:\n%s", size[0], size[1], frame)
		}
		if strings.Contains(frame, "\nsecond") || strings.Contains(frame, "\x1b") {
			t.Fatalf("confirmation leaked hostile control text:\n%s", frame)
		}
	}
}

// TestRetryMenuEntryIsRefusedByNonTerminalSiblingBeyondPreparingOrRunning is
// B5's console-side regression: retryChoice's own sibling scan (not just
// query's) must count needs_repair/needs_rebase/blocked as active, matching
// persistence's activeCrewSQL (`status NOT IN ('succeeded', 'failed')`).
// These fixtures carry no query-computed Actions, so this exercises
// retryChoice's local decision directly - the same path
// TestUnavailableActionIsRefusalAndDoesNotRun and
// TestConfirmationFrameSanitizesHostileRecordedIdentity already rely on.
func TestRetryMenuEntryIsRefusedByNonTerminalSiblingBeyondPreparingOrRunning(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Tasks[0].Crews = []query.CrewNode{
		{CrewID: "crew_target", Status: domain.CrewFailed},
		{CrewID: "crew_sibling", Status: domain.CrewNeedsRepair},
	}
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter")) // the one Project
	m, _ = send(t, m, key("down"))  // its Task
	m, _ = send(t, m, key("enter")) // active sibling is listed; the failed target is in Completed
	m, _ = send(t, m, key("down"))  // Completed group
	m, _ = send(t, m, key("enter")) // expand
	m, _ = send(t, m, key("down"))  // the failed target
	m, _ = send(t, m, key("a"))
	retry := m.actionChoices[3]
	if retry.action != ActionRetry {
		t.Fatalf("menu index 3 = %+v, want retry", retry)
	}
	if retry.enabled {
		t.Fatalf("retry with a needs_repair sibling = %+v, want refused", retry)
	}
	if !strings.Contains(retry.desc, "another attempt is active") {
		t.Fatalf("retry desc = %q, want the truthful active-attempt refusal", retry.desc)
	}
}

// TestRepairMenuEntryWithUnknownBindingIsNotReportedAsNoStaleBinding is B6's
// console-side regression: an Unknown binding read must not be reported as
// the established fact "no stale binding is recorded" - that fact was
// never read, only the read itself failed.
func TestRepairMenuEntryWithUnknownBindingIsNotReportedAsNoStaleBinding(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Tasks[0].Crews = []query.CrewNode{
		{CrewID: "crew_target", Status: domain.CrewNeedsRepair, Binding: query.UnknownField[query.BindingValue]("binding lookup timed out (2s)")},
	}
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter")) // the one Project
	m, _ = send(t, m, key("down"))  // its Task
	m, _ = send(t, m, key("enter")) // that Task's Crew attempts
	m, _ = send(t, m, key("a"))
	repair := m.actionChoices[4]
	if repair.action != ActionRepair {
		t.Fatalf("menu index 4 = %+v, want repair", repair)
	}
	if repair.enabled {
		t.Fatalf("repair with an unknown binding = %+v, want refused", repair)
	}
	if strings.Contains(repair.desc, "no stale binding is recorded") {
		t.Fatalf("repair desc = %q, asserts a fact the failed read never established", repair.desc)
	}
	if !strings.Contains(repair.desc, "unknown") {
		t.Fatalf("repair desc = %q, want it to say the binding read is unknown", repair.desc)
	}
}

func TestActionFailureIsNotReportedAsRefusal(t *testing.T) {
	m := New(func(context.Context) (query.Snapshot, error) { return sampleTree(), nil }, nil,
		func(context.Context, ActionRequest) (string, error) {
			return "application service diagnostics: refused by runtime", errors.New("runtime unavailable")
		})
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 36})
	m, _ = send(t, m, m.Init()())
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	m.actionIndex = 1 // active Crew stop; confirmation is required first
	m, _ = send(t, m, key("enter"))
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("confirmed action did not queue service")
	}
	m, _ = send(t, m, cmd())
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "Action failed") || strings.Contains(m.msg.text, "nothing started") {
		t.Fatalf("action failure = %+v, want attempted failure distinct from refusal", m.msg)
	}
}
