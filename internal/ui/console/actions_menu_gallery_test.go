package console

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// ---------- the ACTIONS menu must never claim more than the recorded
// state establishes ----------
//
// The Console's dominant failure mode is stating something the recorded
// state did not establish - here, reporting a failed binding read as the
// established fact "no stale binding is recorded". Each state gets the
// confirmationGallery convention: the golden fixture and the text it must
// carry live in one table, applied to the ACTIONS menu itself rather than
// an open confirmation, since a refused action never reaches one.

type actionsMenuGalleryState struct {
	name    string
	build   func(t *testing.T) string
	says    []string
	notSays []string
}

// unknownBindingRepairTree is sampleTree with a single needs_repair Crew
// whose binding read itself failed (Unknown, not Absent), so repair must
// say the read is unknown rather than assert the binding is not stale.
func unknownBindingRepairTree() query.Snapshot {
	tree := sampleTree()
	tree.Projects[0].Crews = []query.CrewNode{
		{CrewID: "crew_target", Status: query.CrewNeedsRepair, Binding: query.UnknownField[query.BindingValue]("binding lookup timed out (2s)")},
	}
	return tree
}

func openActionsMenu(t *testing.T, tree query.Snapshot) Model {
	t.Helper()
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter")) // the one Project
	m, _ = send(t, m, key("down"))  // its first Crew
	m, _ = send(t, m, key("a"))
	return m
}

func actionsMenuGallery() []actionsMenuGalleryState {
	return []actionsMenuGalleryState{{
		name: "actions-menu-repair-unknown-binding",
		build: func(t *testing.T) string {
			return renderFrame(t, openActionsMenu(t, unknownBindingRepairTree()))
		},
		says:    []string{"repair", "unavailable · binding is unknown; refresh before repair"},
		notSays: []string{"no stale binding is recorded"},
	}, {
		// The 'n' key's own surface: the name input reached without the
		// menu. It must never offer q as the quit key, because q types a
		// character here (see TestNewProjectNameCanContainQ).
		name: "new-project-input-empty-workspace",
		build: func(t *testing.T) string {
			m := loaded(t, emptyWorkspaceTree(), nil)
			m, _ = send(t, m, key("n"))
			return renderFrame(t, m)
		},
		says:    []string{"NEW PROJECT", "Name", "no runtime agent is started", "Enter Create project", "Esc Cancel", "Ctrl+C Quit"},
		notSays: []string{"q Quit", "ACTIONS"},
	}, {
		name: "new-project-input-typed-name",
		build: func(t *testing.T) string {
			m := loaded(t, sampleTree(), nil)
			m, _ = send(t, m, key("n"))
			for _, r := range "sqlite-queue" {
				m, _ = send(t, m, key(string(r)))
			}
			return renderFrame(t, m)
		},
		says:    []string{"NEW PROJECT", "sqlite-queue"},
		notSays: []string{"q Quit"},
	}, {
		// Below the Workspace the key refuses, and the refusal says where a
		// Project is created instead of failing silently.
		name: "new-project-refused-below-workspace",
		build: func(t *testing.T) string {
			m := loaded(t, sampleTree(), nil)
			m, _ = send(t, m, key("enter")) // into the first Project
			m, _ = send(t, m, key("n"))
			return renderFrame(t, m)
		},
		says:    []string{"Action refused", "workspace level", "nothing started"},
		notSays: []string{"NEW PROJECT"},
	}, {
		// The 's' key on a Project with no Mate: the captain's "chọn dùng
		// agent nào" before anything is created.
		name: "harness-pick-create-mate",
		build: func(t *testing.T) string {
			m := projectFrame(t, mateTree(absentMate("this Project has no Mate"), mateCaps(true, false, false)))
			m, _ = send(t, m, key("s"))
			return renderFrame(t, m)
		},
		says:    []string{"CHOOSE AN AGENT", "claude", "codex", "created and started", "Esc Cancel"},
		notSays: []string{"CHANGE HARNESS", "destroyed"},
		// TODO(task 10): the 'h' harness-switch confirm states lived after
		// this one. Restarting a Mate under another harness is task 10.
	}}
}

// TestGoldenActionsMenuFrames pins the ACTIONS menu gallery byte for byte.
// Accept a deliberate change with:
//
//	go test ./internal/ui/console -run TestGoldenActionsMenuFrames -update
func TestGoldenActionsMenuFrames(t *testing.T) {
	for _, st := range actionsMenuGallery() {
		t.Run(st.name, func(t *testing.T) {
			assertGolden(t, st.name, st.build(t))
		})
	}
}

// TestEveryActionsMenuGalleryStateSaysWhatItMeans is the same states
// asserted as text, independent of the golden bytes.
func TestEveryActionsMenuGalleryStateSaysWhatItMeans(t *testing.T) {
	for _, st := range actionsMenuGallery() {
		t.Run(st.name, func(t *testing.T) {
			frame := st.build(t)
			flat := flattenWrap(frame)
			for _, want := range st.says {
				if !strings.Contains(flat, want) {
					t.Errorf("%s does not say %q:\n%s", st.name, want, frame)
				}
			}
			for _, banned := range st.notSays {
				if strings.Contains(flat, banned) {
					t.Errorf("%s says %q, which it must not:\n%s", st.name, banned, frame)
				}
			}
		})
	}
}
