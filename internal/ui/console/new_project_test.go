package console

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// emptyWorkspaceTree is a workspace that has been initialized but carries no
// Project yet - what a reader sees on a fresh `mate <dir>` since ADR 0023
// dropped the default Project, and the one screen where creating a Project
// is the only thing left to do.
func emptyWorkspaceTree() query.Snapshot {
	return query.Snapshot{
		WorkspaceID: "ws_acme",
		Workspace: query.KnownField(query.WorkspaceValue{
			Name: "acme", Root: "/Users/dev/work/acme",
		}),
		Actions: []query.ActionAvailability{{Action: "onboard", Available: true, Reason: "add a Project to this workspace"}},
	}
}

// withRunner installs an action runner on a read-only fixture model and
// returns a pointer to the requests it received.
func withRunner(m Model, reply string, err error) (Model, *[]ActionRequest) {
	got := &[]ActionRequest{}
	m.action = func(_ context.Context, req ActionRequest) (string, error) {
		*got = append(*got, req)
		return reply, err
	}
	return m, got
}

// typeText sends each rune of text as its own key press.
func typeText(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m, _ = send(t, m, key(string(r)))
	}
	return m
}

// fillRepo moves from the typed name to the repo field and types path.
func fillRepo(t *testing.T, m Model, path string) Model {
	t.Helper()
	m, cmd := send(t, m, key("enter"))
	if cmd != nil {
		t.Fatalf("enter on the name dispatched before a repo was given: %v", cmd)
	}
	if m.actionField != fieldRepo {
		t.Fatalf("enter on the name left the cursor on field %d, want the repo", m.actionField)
	}
	return typeText(t, m, path)
}

func TestNewProjectKeyOpensTheNameInputDirectly(t *testing.T) {
	m := loaded(t, emptyWorkspaceTree(), nil)
	m, cmd := send(t, m, key("n"))
	if cmd != nil {
		t.Fatalf("n queued a command before any name was typed: %v", cmd)
	}
	if !m.actionInputMode {
		t.Fatal("n did not open the new-project name input")
	}
	if m.actions {
		t.Fatal("n opened the action menu; it is meant to skip it")
	}
	view := renderFrame(t, m)
	for _, want := range []string{"NEW PROJECT", "Name", "Repo", "Enter Next", "Tab Switch field", "Esc Cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("new-project input view missing %q:\n%s", want, view)
		}
	}
}

func TestNewProjectKeyTypesANameAndDispatchesOnboardOnce(t *testing.T) {
	m, got := withRunner(loaded(t, emptyWorkspaceTree(), nil), "created proj_1", nil)
	m, _ = send(t, m, key("n"))
	// The name contains an 'n': once the input is open, n is a character,
	// not the shortcut again.
	for _, r := range "payments-api" {
		m, _ = send(t, m, key(string(r)))
	}
	if m.actionInput != "payments-api" {
		t.Fatalf("typed name = %q, want payments-api", m.actionInput)
	}
	m = fillRepo(t, m, "services/payments")
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("enter on a typed name did not dispatch onboard")
	}
	m, _ = send(t, m, cmd())
	if len(*got) != 1 {
		t.Fatalf("runner calls = %d, want exactly one", len(*got))
	}
	req := (*got)[0]
	if req.Action != ActionOnboard || req.TargetKind != "workspace" || req.Input != "payments-api" || req.Repo != "services/payments" {
		t.Fatalf("onboard request = %+v, want a workspace onboard carrying the typed name and repo", req)
	}
	if m.msg.tone != toneOK || !strings.Contains(m.msg.text, "created proj_1") {
		t.Fatalf("result message = %+v, want the service's own reply", m.msg)
	}
}

func TestNewProjectKeyEscapeClosesWithoutOpeningTheMenu(t *testing.T) {
	m, got := withRunner(loaded(t, emptyWorkspaceTree(), nil), "must not run", nil)
	m, _ = send(t, m, key("n"))
	m, cmd := send(t, m, key("esc"))
	if cmd != nil || len(*got) != 0 {
		t.Fatalf("esc ran something: cmd=%v calls=%d", cmd, len(*got))
	}
	if m.actionInputMode {
		t.Fatal("esc left the name input open")
	}
	if m.actions {
		t.Fatal("esc from the n shortcut opened a menu the reader never asked for")
	}
	if m.actionInput != "" {
		t.Fatalf("esc kept the abandoned draft name %q", m.actionInput)
	}
}

func TestNewProjectKeyBelowTheWorkspaceIsARefusalThatStartsNothing(t *testing.T) {
	m, got := withRunner(loaded(t, sampleTree(), nil), "must not run", nil)
	m, _ = send(t, m, key("enter")) // into the first Project
	m, cmd := send(t, m, key("n"))
	if cmd != nil || len(*got) != 0 {
		t.Fatalf("n below the workspace ran something: cmd=%v calls=%d", cmd, len(*got))
	}
	if m.actionInputMode || m.actions {
		t.Fatalf("n below the workspace opened input=%v menu=%v", m.actionInputMode, m.actions)
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "nothing started") {
		t.Fatalf("refusal message = %+v, want an explicit refusal", m.msg)
	}
	if !strings.Contains(m.msg.text, "workspace") {
		t.Fatalf("refusal message = %q, want it to name where a Project is created", m.msg.text)
	}
}

// TestNewProjectKeyHonoursTheSnapshotsOwnCapability pins that this shortcut
// reads the store-backed loader's workspace capability rather than assuming
// onboarding is always available - the same rule the action menu follows.
func TestNewProjectKeyHonoursTheSnapshotsOwnCapability(t *testing.T) {
	tree := emptyWorkspaceTree()
	tree.Actions = []query.ActionAvailability{{Action: "onboard", Available: false, Reason: "workspace record is unreadable"}}
	m, got := withRunner(loaded(t, tree, nil), "must not run", nil)
	m, cmd := send(t, m, key("n"))
	if cmd != nil || len(*got) != 0 || m.actionInputMode {
		t.Fatalf("n ran or opened input despite a refused capability: cmd=%v calls=%d input=%v", cmd, len(*got), m.actionInputMode)
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "workspace record is unreadable") {
		t.Fatalf("refusal message = %+v, want the snapshot's own reason", m.msg)
	}
}

func TestNewProjectKeyIsNotABindingInsideTheActionMenu(t *testing.T) {
	m := loaded(t, emptyWorkspaceTree(), nil)
	m, _ = send(t, m, key("a"))
	before := m.actionIndex
	m, cmd := send(t, m, key("n"))
	if cmd != nil {
		t.Fatalf("n inside the menu queued %v", cmd)
	}
	if !m.actions || m.actionInputMode || m.actionIndex != before {
		t.Fatalf("n inside the menu changed state: menu=%v input=%v index=%d", m.actions, m.actionInputMode, m.actionIndex)
	}
}

func TestWorkspaceKeyLineOffersNewProjectOnlyAtTheWorkspace(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	if view := renderFrame(t, m); !strings.Contains(view, "n New project") {
		t.Fatalf("workspace key line does not offer the new-project key:\n%s", view)
	}
	m, _ = send(t, m, key("enter")) // into the first Project
	if view := renderFrame(t, m); strings.Contains(view, "n New project") {
		t.Fatalf("Project frame offers a key that refuses there:\n%s", view)
	}
}

func TestEmptyWorkspacePointsAtTheNewProjectKey(t *testing.T) {
	m := loaded(t, emptyWorkspaceTree(), nil)
	view := renderFrame(t, m)
	if !strings.Contains(view, "Press n to add a Project") {
		t.Fatalf("empty-workspace hint does not name the n key:\n%s", view)
	}
}

// TestWorkspaceOnboardIsNotRefusedByASelectedProjectsOwnMate is the
// capability-source defect actionCapability closes: at the Workspace level
// the menu's onboard entry means "add a Project to this workspace", but
// availability was looked up on the selected row - so a Project that already
// has a Mate (query.projectActions: onboard false, "this Project already has
// a Mate") disabled it and explained the refusal with a fact about a
// different object. The 'n' key must not inherit that either.
func TestWorkspaceOnboardIsNotRefusedByASelectedProjectsOwnMate(t *testing.T) {
	tree := sampleTree()
	tree.Actions = []query.ActionAvailability{{Action: "onboard", Available: true, Reason: "add a Project to this workspace"}}
	for i := range tree.Projects {
		tree.Projects[i].Actions = []query.ActionAvailability{
			{Action: "start", Available: false, Reason: "Mate is recorded running"},
			{Action: "resume", Available: false, Reason: "Mate is recorded running"},
			{Action: "onboard", Available: false, Reason: "this Project already has a Mate"},
		}
	}
	m, got := withRunner(loaded(t, tree, nil), "created proj_new", nil)

	m, _ = send(t, m, key("a"))
	onboard := m.actionChoices[len(m.actionChoices)-1]
	if onboard.action != ActionOnboard {
		t.Fatalf("menu index 5 = %+v, want onboard", onboard)
	}
	if !onboard.enabled {
		t.Fatalf("workspace onboard = %+v, want it available regardless of the selected Project's Mate", onboard)
	}
	if strings.Contains(onboard.desc, "already has a Mate") {
		t.Fatalf("workspace onboard desc = %q, explains itself with another object's fact", onboard.desc)
	}
	m, _ = send(t, m, key("esc"))

	// The shortcut reads the same capability and reaches the same request.
	m, _ = send(t, m, key("n"))
	if !m.actionInputMode {
		t.Fatal("n was refused at a Workspace whose selected Project already has a Mate")
	}
	m = typeText(t, m, "ledger")
	m = fillRepo(t, m, "ledger")
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("enter did not dispatch the new Project")
	}
	m, _ = send(t, m, cmd())
	if len(*got) != 1 || (*got)[0].TargetKind != "workspace" || (*got)[0].Input != "ledger" {
		t.Fatalf("requests = %+v, want one workspace onboard for \"ledger\"", *got)
	}
}

// TestNewProjectInputRefusesAnEmptyNameWithoutCallingTheService keeps the
// direct-input path's refusal identical to the menu path's: a name is
// required, and nothing is started without one.
func TestNewProjectInputRefusesAnEmptyNameWithoutCallingTheService(t *testing.T) {
	m, got := withRunner(loaded(t, emptyWorkspaceTree(), nil), "must not run", nil)
	m, _ = send(t, m, key("n"))
	m, _ = send(t, m, key(" "))
	m, cmd := send(t, m, key("enter"))
	if cmd != nil || len(*got) != 0 {
		t.Fatalf("a blank name dispatched: cmd=%v calls=%d", cmd, len(*got))
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "project name is required") {
		t.Fatalf("refusal message = %+v", m.msg)
	}
	if !m.actionInputMode {
		t.Fatal("the refusal closed the input; the reader has nowhere to type the name")
	}
}

// TestNewProjectNameCanContainQ: q quits the Console everywhere else, but a
// name input is modal - a printable key typed into it is a character, not a
// command. Without this, "queue-service" or "sqlite-tools" cannot be typed
// at all: the Console exits on the first letter. Ctrl+C, the terminal's own
// interrupt rather than a character, still leaves.
func TestNewProjectNameCanContainQ(t *testing.T) {
	m, got := withRunner(loaded(t, emptyWorkspaceTree(), nil), "created proj_q", nil)
	m, _ = send(t, m, key("n"))
	for _, r := range "queue-sqlite" {
		next, cmd := send(t, m, key(string(r)))
		if cmd != nil {
			t.Fatalf("typing %q into the name input queued %v", r, cmd)
		}
		if next.quitting {
			t.Fatalf("typing %q into the name input quit the Console", r)
		}
		m = next
	}
	if m.actionInput != "queue-sqlite" {
		t.Fatalf("typed name = %q, want queue-sqlite", m.actionInput)
	}
	view := renderFrame(t, m)
	if strings.Contains(view, "q Quit") {
		t.Fatalf("key line still offers q as quit while q types a character:\n%s", view)
	}
	if !strings.Contains(view, "Ctrl+C Quit") {
		t.Fatalf("key line does not name a way out of the name input:\n%s", view)
	}
	m = fillRepo(t, m, "queue")
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("enter did not dispatch")
	}
	m, _ = send(t, m, cmd())
	if len(*got) != 1 || (*got)[0].Input != "queue-sqlite" {
		t.Fatalf("requests = %+v, want the whole typed name", *got)
	}
}

func TestCtrlCStillQuitsFromTheNewProjectInput(t *testing.T) {
	m := loaded(t, emptyWorkspaceTree(), nil)
	m, _ = send(t, m, key("n"))
	m, cmd := send(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil || !m.quitting {
		t.Fatalf("ctrl+c from the name input did not quit: cmd=%v quitting=%v", cmd, m.quitting)
	}
}

// TestNewProjectFormRefusesAnEmptyRepoWithoutCallingTheService: the bridge
// cannot register a repo it has not been given, so the form never sends one
// without it - the refusal is here, before anything runs, not after.
func TestNewProjectFormRefusesAnEmptyRepoWithoutCallingTheService(t *testing.T) {
	m, got := withRunner(loaded(t, emptyWorkspaceTree(), nil), "must not run", nil)
	m, _ = send(t, m, key("n"))
	m = typeText(t, m, "ledger")
	m = fillRepo(t, m, "  ")
	m, cmd := send(t, m, key("enter"))
	if cmd != nil || len(*got) != 0 {
		t.Fatalf("a blank repo dispatched: cmd=%v calls=%d", cmd, len(*got))
	}
	if m.msg.tone != toneError || !strings.Contains(m.msg.text, "repo path is required") {
		t.Fatalf("refusal message = %+v", m.msg)
	}
	if !m.actionInputMode || m.actionField != fieldRepo {
		t.Fatalf("the refusal moved the reader away from the repo field: input=%v field=%d", m.actionInputMode, m.actionField)
	}
}

// TestNewProjectFormFieldsAreEditedIndependently pins the two-field editing:
// Tab switches, typing and Backspace touch only the focused field, and
// Backspace on an empty repo steps back into the name instead of closing.
func TestNewProjectFormFieldsAreEditedIndependently(t *testing.T) {
	m := loaded(t, emptyWorkspaceTree(), nil)
	m, _ = send(t, m, key("n"))
	m = typeText(t, m, "led")
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = typeText(t, m, "ab")
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
	if view := renderFrame(t, m); !strings.Contains(view, "relative to /Users/dev/work/acme") {
		t.Fatalf("form view does not name the root a relative repo resolves against:\n%s", view)
	}
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if view := renderFrame(t, m); !strings.Contains(view, "Enter Create project") {
		t.Fatalf("repo field view does not offer the create key:\n%s", view)
	}
}
