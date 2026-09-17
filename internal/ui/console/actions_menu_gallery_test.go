package console

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/domain"
	"github.com/nguyenngocanh94/matev2/internal/query"
)

// ---------- B5/B6: the ACTIONS menu must never claim more than the
// recorded state establishes ----------
//
// PR 51's counter-review found the ACTIONS menu offering retry where the
// service always refuses it (B5: the occupancy check missed
// needs_repair/needs_rebase/blocked, which persistence's activeCrewSQL,
// `status NOT IN ('succeeded', 'failed')`, does count as active) and
// reporting a failed binding read as the established fact "no stale
// binding is recorded" (B6). Both are this package's dominant failure mode
// - the Console stating something the recorded state did not establish -
// so both get the confirmationGallery convention PR 53 established: the
// golden fixture and the text it must carry live in one table, applied
// here to the ACTIONS menu itself rather than an open confirmation, since
// neither fix changes what a confirmation dialog renders (a refused action
// never reaches one).

type actionsMenuGalleryState struct {
	name    string
	build   func(t *testing.T) string
	says    []string
	notSays []string
}

// blockedRetryTree is sampleTree with its first Task's Crew attempts
// replaced: a failed target attempt with a needs_repair sibling, active by
// the real repo occupancy rule but invisible to the pre-fix hand-rolled
// reserved|preparing|running list.
func blockedRetryTree() query.Snapshot {
	tree := sampleTree()
	tree.Projects[0].Tasks[0].Crews = []query.CrewNode{
		{CrewID: "crew_target", Status: domain.CrewFailed},
		{CrewID: "crew_sibling", Status: domain.CrewNeedsRepair},
	}
	return tree
}

// unknownBindingRepairTree is sampleTree with a single needs_repair Crew
// whose binding read itself failed (Unknown, not Absent), so repair must
// say the read is unknown rather than assert the binding is not stale.
func unknownBindingRepairTree() query.Snapshot {
	tree := sampleTree()
	tree.Projects[0].Tasks[0].Crews = []query.CrewNode{
		{CrewID: "crew_target", Status: domain.CrewNeedsRepair, Binding: query.UnknownField[query.BindingValue]("binding lookup timed out (2s)")},
	}
	return tree
}

func openActionsMenu(t *testing.T, tree query.Snapshot) Model {
	t.Helper()
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter")) // the one Project
	m, _ = send(t, m, key("down"))  // its Task
	m, _ = send(t, m, key("enter")) // that Task's Crew attempts
	m, _ = send(t, m, key("a"))
	return m
}

func actionsMenuGallery() []actionsMenuGalleryState {
	return []actionsMenuGalleryState{{
		name: "actions-menu-retry-blocked-by-active-sibling",
		build: func(t *testing.T) string {
			m := loaded(t, blockedRetryTree(), nil)
			m, _ = send(t, m, key("enter")) // Project
			m, _ = send(t, m, key("down"))  // Task
			m, _ = send(t, m, key("enter")) // active sibling listed; failed target is in Completed
			m, _ = send(t, m, key("down"))  // Completed group
			m, _ = send(t, m, key("enter")) // expand
			m, _ = send(t, m, key("down"))  // failed target
			m, _ = send(t, m, key("a"))
			return renderFrame(t, m)
		},
		says:    []string{"retry", "unavailable · another attempt is active"},
		notSays: []string{"preparing or running", "Start a new attempt and keep this history"},
	}, {
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
			m := projectFrame(t, mateTree(absentMate("this Project has no Mate"), mateCaps(true, false, false, false)))
			m, _ = send(t, m, key("s"))
			return renderFrame(t, m)
		},
		says:    []string{"CHOOSE AN AGENT", "claude", "codex", "created and started", "Esc Cancel"},
		notSays: []string{"CHANGE HARNESS", "destroyed"},
	}, {
		// The 'h' key on a live Mate, after the agent is chosen: the modal
		// that warns before the old panel is destroyed.
		name: "harness-switch-confirm-live-mate",
		build: func(t *testing.T) string {
			tree := sampleTree()
			tree.Projects = tree.Projects[:1]
			tree.Projects[0].Tasks = nil
			tree.Projects[0].Mate.Actions = mateCaps(false, false, false, true)
			m := projectFrame(t, tree)
			m, _ = send(t, m, key("h"))
			m, _ = send(t, m, key("down")) // codex
			m, _ = send(t, m, key("enter"))
			return renderFrame(t, m)
		},
		says:    []string{"CONFIRM switch_harness?", "Object", "Scope", "Effect", "destroyed", "unsaved", "Esc Cancel"},
		notSays: []string{"No live panel is recorded"},
	}, {
		// The same switch with nothing live: it must not borrow the live
		// warning, or the warning that matters stops meaning anything.
		name: "harness-switch-confirm-stopped-mate",
		build: func(t *testing.T) string {
			m := projectFrame(t, mateTree(knownMate(domain.MateStopped, domain.HarnessClaude), mateCaps(false, false, true, true)))
			m, _ = send(t, m, key("h"))
			m, _ = send(t, m, key("down"))
			m, _ = send(t, m, key("enter"))
			return renderFrame(t, m)
		},
		says:    []string{"CONFIRM switch_harness?", "No live panel is recorded", "nothing is destroyed"},
		notSays: []string{"unsaved"},
	}, {
		name: "actions-menu-discard-open-merge",
		build: func(t *testing.T) string {
			m := loaded(t, openMergeDiscardTree(), nil)
			m, _ = send(t, m, key("enter")) // Project
			m, _ = send(t, m, key("down"))  // Task
			m, _ = send(t, m, key("enter")) // Completed group (the only listed row)
			m, _ = send(t, m, key("enter")) // expand
			m, _ = send(t, m, key("down"))  // the failed Crew
			m, _ = send(t, m, key("a"))
			return renderFrame(t, m)
		},
		says:    []string{"discard", "unavailable · crew has an open merge request"},
		notSays: []string{"Remove this Crew's worktree and branch"},
	}}
}

func openMergeDiscardTree() query.Snapshot {
	tree := sampleTree()
	tree.Projects[0].Tasks[0].Crews = []query.CrewNode{{
		CrewID: "crew_target", Status: domain.CrewFailed,
		Worktree: query.KnownField(query.WorktreeValue{
			Path: "/w", Branch: "crew/x", Status: query.WorktreeRecordedCreated,
		}),
		OpenMerge: query.KnownField(query.OpenMergeValue{
			RequestID: "mr_1", Status: domain.MergePendingConfirmation,
		}),
		Attention: query.KnownField(query.Attention{Kind: query.AttentionFailed, Why: "failed"}),
	}}
	return tree
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
