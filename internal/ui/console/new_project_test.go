package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The new-project sheet (design K): n on the workspace opens it straight,
// with a name and an optional repo. It registers a Project; no Mate is
// started.

// actEmptyWorkspace is an initialized workspace with no Project yet - what
// a fresh `mate <dir>` shows since ADR 0023 dropped the default Project.
func actEmptyWorkspace() query.Snapshot {
	return query.Snapshot{
		WorkspaceID: "ws_acme",
		Workspace:   query.KnownField(query.WorkspaceValue{Name: "acme", Root: "/Users/dev/work/acme"}),
		Actions:     []query.ActionAvailability{{Action: "onboard", Available: true, Reason: "add a Project to this workspace"}},
	}
}

// actType sends each rune of text as its own key press.
func actType(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m, _ = send(t, m, key(string(r)))
	}
	return m
}

// actFillRepo moves from the typed name to the repo field and types path.
func actFillRepo(t *testing.T, m Model, path string) Model {
	t.Helper()
	m, cmd := send(t, m, key("enter"))
	if cmd != nil {
		t.Fatal("enter on the name dispatched before a repo was asked")
	}
	if m.actionField != fieldRepo {
		t.Fatalf("enter on the name left the cursor on field %d, want the repo", m.actionField)
	}
	return actType(t, m, path)
}

func TestNewProjectKeyOpensTheSheetDirectly(t *testing.T) {
	for _, tree := range []query.Snapshot{actEmptyWorkspace(), sampleTree()} {
		m := loaded(t, tree, nil)
		m, cmd := send(t, m, key("n"))
		if cmd != nil || !m.actionInputMode || m.actions {
			t.Fatalf("n: cmd=%v input=%v sheet=%v, want the new-project sheet and no actions sheet", cmd != nil, m.actionInputMode, m.actions)
		}
		view := renderFrame(t, m)
		for _, want := range []string{"new project", "name", "repo · optional", "leave blank to add later", "no mate is started", "esc"} {
			if !strings.Contains(view, want) {
				t.Fatalf("new-project sheet missing %q:\n%s", want, view)
			}
		}
	}
}

func TestNewProjectTypesANameAndDispatchesOnboardOnce(t *testing.T) {
	m, got := actRunner(t, actEmptyWorkspace(), "created proj_1", nil)
	m, _ = send(t, m, key("n"))
	// The name has an n in it: once the sheet is open, n is a character.
	m = actType(t, m, "payments-api")
	if m.actionInput != "payments-api" {
		t.Fatalf("typed name = %q, want payments-api", m.actionInput)
	}
	if !strings.Contains(renderFrame(t, m), "create payments-api") {
		t.Fatalf("the sheet does not say what Enter creates:\n%s", renderFrame(t, m))
	}
	m = actFillRepo(t, m, "services/payments")
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("enter did not dispatch onboard")
	}
	m, _ = send(t, m, cmd())
	if len(*got) != 1 {
		t.Fatalf("runner calls = %d, want exactly one", len(*got))
	}
	if req := (*got)[0]; req.Action != ActionOnboard || req.TargetKind != "workspace" || req.Input != "payments-api" || req.Repo != "services/payments" {
		t.Fatalf("onboard request = %+v, want a workspace onboard with the typed name and repo", req)
	}
	if m.msg.tone != toneOK || !strings.Contains(m.msg.text, "created proj_1") {
		t.Fatalf("result message = %+v, want the service's own reply", m.msg)
	}
}

func TestNewProjectEscapeClosesWithoutOpeningTheSheet(t *testing.T) {
	m, got := actRunner(t, actEmptyWorkspace(), "must not run", nil)
	m, _ = send(t, m, key("n"))
	m = actType(t, m, "draft")
	m, cmd := send(t, m, key("esc"))
	if cmd != nil || len(*got) != 0 {
		t.Fatalf("esc ran something: cmd=%v calls=%d", cmd != nil, len(*got))
	}
	if m.actionInputMode || m.actions {
		t.Fatalf("esc left input=%v sheet=%v open", m.actionInputMode, m.actions)
	}
	if m.actionInput != "" {
		t.Fatalf("esc kept the abandoned draft %q", m.actionInput)
	}
}

func TestNewProjectBelowTheWorkspaceIsARefusalThatStartsNothing(t *testing.T) {
	m, got := actRunner(t, sampleTree(), "must not run", nil)
	m, _ = send(t, m, key("enter")) // into the first Project
	m, cmd := send(t, m, key("n"))
	if cmd != nil || len(*got) != 0 || m.actionInputMode || m.actions {
		t.Fatalf("n below the workspace acted: cmd=%v calls=%d input=%v sheet=%v", cmd != nil, len(*got), m.actionInputMode, m.actions)
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "nothing started") || !strings.Contains(m.msg.text, "workspace") {
		t.Fatalf("refusal = %+v, want a refusal that says where a Project is created", m.msg)
	}
}

// The shortcut reads the store-backed workspace capability rather than
// assuming onboarding is always available.
func TestNewProjectHonoursTheSnapshotsOwnCapability(t *testing.T) {
	tree := actEmptyWorkspace()
	tree.Actions = []query.ActionAvailability{{Action: "onboard", Available: false, Reason: "workspace record is unreadable"}}
	m, got := actRunner(t, tree, "must not run", nil)
	m, cmd := send(t, m, key("n"))
	if cmd != nil || len(*got) != 0 || m.actionInputMode {
		t.Fatalf("n opened or ran despite a refused capability: cmd=%v calls=%d input=%v", cmd != nil, len(*got), m.actionInputMode)
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "workspace record is unreadable") {
		t.Fatalf("refusal = %+v, want the snapshot's own reason", m.msg)
	}
}

// At the workspace level "new project" means "add a Project here"; a
// selected Project that already has a Mate must not refuse it with a fact
// about itself.
func TestWorkspaceNewProjectIsNotRefusedByASelectedProjectsOwnMate(t *testing.T) {
	tree := sampleTree()
	tree.Actions = []query.ActionAvailability{{Action: "onboard", Available: true, Reason: "add a Project to this workspace"}}
	for i := range tree.Projects {
		tree.Projects[i].Actions = []query.ActionAvailability{
			{Action: "start", Available: false, Reason: "Mate is recorded running"},
			{Action: "onboard", Available: false, Reason: "this Project already has a Mate"},
		}
	}
	m, got := actRunner(t, tree, "created proj_new", nil)
	m, _ = send(t, m, key("a"))
	e := actEntry(t, m, "n")
	if !e.enabled || strings.Contains(e.reason+e.about, "already has a Mate") {
		t.Fatalf("workspace new project = %+v, want it available and not explained by another object", e)
	}
	m, _ = send(t, m, key("esc"))
	m, _ = send(t, m, key("n"))
	if !m.actionInputMode {
		t.Fatal("n was refused at a workspace whose selected Project already has a Mate")
	}
	m = actType(t, m, "ledger")
	m = actFillRepo(t, m, "ledger")
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("enter did not dispatch")
	}
	_, _ = send(t, m, cmd())
	if len(*got) != 1 || (*got)[0].TargetKind != "workspace" || (*got)[0].Input != "ledger" {
		t.Fatalf("requests = %+v, want one workspace onboard for ledger", *got)
	}
}

func TestNewProjectRefusesAnEmptyNameWithoutCallingTheService(t *testing.T) {
	m, got := actRunner(t, actEmptyWorkspace(), "must not run", nil)
	m, _ = send(t, m, key("n"))
	m, _ = send(t, m, key(" "))
	m, cmd := send(t, m, key("enter"))
	if cmd != nil || len(*got) != 0 {
		t.Fatalf("a blank name dispatched: cmd=%v calls=%d", cmd != nil, len(*got))
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "project name is required") {
		t.Fatalf("refusal = %+v", m.msg)
	}
	if !m.actionInputMode {
		t.Fatal("the refusal closed the sheet; there is nowhere left to type the name")
	}
}

// q quits everywhere else, but in a text field it is a character:
// otherwise "queue-service" cannot be typed. Ctrl+C still leaves.
func TestNewProjectNameCanContainQ(t *testing.T) {
	m, got := actRunner(t, actEmptyWorkspace(), "created proj_q", nil)
	m, _ = send(t, m, key("n"))
	for _, r := range "queue-sqlite" {
		next, cmd := send(t, m, key(string(r)))
		if cmd != nil || next.quitting {
			t.Fatalf("typing %q quit or queued: cmd=%v quitting=%v", r, cmd != nil, next.quitting)
		}
		m = next
	}
	if m.actionInput != "queue-sqlite" {
		t.Fatalf("typed name = %q, want queue-sqlite", m.actionInput)
	}
	if strings.Contains(renderFrame(t, m), "q quit") {
		t.Fatalf("the frame offers q as quit while q types a character:\n%s", renderFrame(t, m))
	}
	m = actFillRepo(t, m, "queue")
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("enter did not dispatch")
	}
	_, _ = send(t, m, cmd())
	if len(*got) != 1 || (*got)[0].Input != "queue-sqlite" {
		t.Fatalf("requests = %+v, want the whole typed name", *got)
	}
}

func TestCtrlCStillQuitsFromTheNewProjectSheet(t *testing.T) {
	m := loaded(t, actEmptyWorkspace(), nil)
	m, _ = send(t, m, key("n"))
	m, cmd := send(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil || !m.quitting {
		t.Fatalf("ctrl+c from the sheet did not quit: cmd=%v quitting=%v", cmd != nil, m.quitting)
	}
}

// The repo is optional (docs/mvp.md M9): name, Enter, Enter creates the
// Project with none.
func TestNewProjectSendsNoRepoWhenTheRepoIsLeftEmpty(t *testing.T) {
	m, got := actRunner(t, actEmptyWorkspace(), "Project ledger added with no repo yet", nil)
	m, _ = send(t, m, key("n"))
	m = actType(t, m, "ledger")
	m = actFillRepo(t, m, "  ")
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("enter on an empty repo did not dispatch")
	}
	m, _ = send(t, m, cmd())
	if len(*got) != 1 {
		t.Fatalf("runner calls = %d, want exactly one", len(*got))
	}
	if req := (*got)[0]; req.Action != ActionOnboard || req.TargetKind != "workspace" || req.Input != "ledger" || req.Repo != "" {
		t.Fatalf("onboard request = %+v, want ledger with no repo", req)
	}
	if m.msg.tone != toneOK || !strings.Contains(m.msg.text, "no repo yet") {
		t.Fatalf("result = %+v, want the service's own reply", m.msg)
	}
}

// Tab switches fields; typing and Backspace touch only the focused one;
// Backspace on an empty repo steps back into the name instead of closing.
func TestNewProjectFieldsAreEditedIndependently(t *testing.T) {
	m := loaded(t, actEmptyWorkspace(), nil)
	m, _ = send(t, m, key("n"))
	m = actType(t, m, "led")
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = actType(t, m, "ab")
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.actionInput != "led" || m.actionRepo != "a" {
		t.Fatalf("fields = name %q repo %q, want led and a", m.actionInput, m.actionRepo)
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if !m.actionInputMode || m.actionField != fieldName || m.actionInput != "led" {
		t.Fatalf("backspace on an empty repo: input=%v field=%d name=%q, want back on the kept name",
			m.actionInputMode, m.actionField, m.actionInput)
	}
	// The field with the cursor is the one drawn with it.
	view := renderFrame(t, m)
	if !strings.Contains(view, "▌ led█") {
		t.Fatalf("the name field does not carry the cursor:\n%s", view)
	}
}

// The rule under the name is the store's own (internal/names), and a name
// the store or the snapshot would refuse swaps it for the reason.
func TestNewProjectNameRuleSaysWhyANameWouldBeRefused(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"ledger-audit", "unique"},
		{"Ledger_Audit", "not a project name"},
		{"payments-api", "payments-api already exists"},
	} {
		m := loaded(t, sampleTree(), nil)
		m, _ = send(t, m, key("n"))
		m = actType(t, m, tc.name)
		if flat := actFlat(renderFrame(t, m)); !strings.Contains(flat, tc.want) {
			t.Fatalf("typed %q: the sheet does not say %q:\n%s", tc.name, tc.want, renderFrame(t, m))
		}
	}
}

func TestNewProjectNameStopsAtItsLimit(t *testing.T) {
	m := loaded(t, actEmptyWorkspace(), nil)
	m, _ = send(t, m, key("n"))
	m = actType(t, m, strings.Repeat("a", onboardNameLimit+3))
	if n := len([]rune(m.actionInput)); n != onboardNameLimit {
		t.Fatalf("typed name length = %d, want the %d limit", n, onboardNameLimit)
	}
	if !strings.Contains(m.msg.text, "limit reached") {
		t.Fatalf("message = %+v, want a word that further input is ignored", m.msg)
	}
}
