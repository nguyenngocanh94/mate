package console

import (
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// Rows and detail must not draw two different recorded states the same way.

// mdlCrewFrame is the project frame with the given crew selected, at 40x36.
func mdlCrewFrame(t *testing.T, c query.CrewNode) Model {
	t.Helper()
	tree := sampleTree()
	tree.Projects[0].Crews = []query.CrewNode{c}
	m := loaded(t, tree, nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("down"))
	if r, ok := m.selectedRow(); !ok || r.id != c.CrewID {
		t.Fatalf("precondition: selected %+v, want crew %s", r, c.CrewID)
	}
	return m
}

func mdlLine(frame, needle string) string {
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return ""
}

// A crew whose status looks fine but whose binding is recorded stale needs
// the captain: its row carries the ! and detail says why.
func TestAStaleBindingIsDrawnApartFromALiveOne(t *testing.T) {
	live := sampleTree().Projects[0].Crews[1]
	stale := live
	stale.Binding.Value.Status = query.BindingStale
	stale.Attention = query.KnownField(query.Attention{Kind: query.AttentionStaleBinding,
		Why: "crew " + live.CrewID + " is recorded working but its runtime binding is stale, so attach is refused"})

	liveFrame := renderFrame(t, mdlCrewFrame(t, live))
	staleFrame := renderFrame(t, mdlCrewFrame(t, stale))
	liveRow, staleRow := mdlLine(liveFrame, live.Task), mdlLine(staleFrame, live.Task)
	if strings.Contains(liveRow, "!") || !strings.Contains(staleRow, "!") {
		t.Fatalf("rows: live %q, stale %q; want the ! only on the stale one", liveRow, staleRow)
	}
	if b := mdlLine(staleFrame, "binding"); !strings.Contains(b, "stale") {
		t.Fatalf("stale detail binding line = %q, want it to say stale:\n%s", b, staleFrame)
	}
	if strings.Contains(liveFrame, "binding") {
		t.Fatalf("a live binding grew a binding field:\n%s", liveFrame)
	}
}

// The observer's reading of a crew's pane is detail's "pane" field, in the
// words that fit, naming Jev when its answer is in the reading; an agent
// Herdr no longer has is unmistakable, and no observation draws no field at
// all.
func TestTheObserversHealthIsTheCrewsPaneField(t *testing.T) {
	base := sampleTree().Projects[0].Crews[1]
	cases := []struct {
		name   string
		health query.Field[query.CrewHealth]
		want   string
	}{
		{"gone", query.KnownField(query.CrewHealth{AgentPresent: false}), "agent gone"},
		{"busy", query.KnownField(query.CrewHealth{AgentPresent: true, Composer: query.ComposerBusy, ComposerFor: 12 * time.Second}), "busy 12s"},
		{"idle", query.KnownField(query.CrewHealth{AgentPresent: true, Composer: query.ComposerEmpty, QuietFor: 4 * time.Minute}), "idle 4m"},
		{"unclear", query.KnownField(query.CrewHealth{AgentPresent: true, Composer: query.ComposerUnknown}), "unclear"},
		{"read by jev", query.KnownField(query.CrewHealth{AgentPresent: true, Composer: query.ComposerBusy, ComposerFor: 12 * time.Second,
			Source: "jev"}), "busy 12s · via jev"},
		{"none", query.AbsentField[query.CrewHealth]("the observer has not seen this crew"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			c.Health = tc.health
			line := mdlLine(renderFrame(t, mdlCrewFrame(t, c)), "  pane ")
			if tc.want == "" {
				if line != "" {
					t.Fatalf("no observation drew a pane field: %q", line)
				}
				return
			}
			if !strings.Contains(line, tc.want) {
				t.Fatalf("pane field = %q, want %q", line, tc.want)
			}
			if tc.health.Value.Source != "jev" && strings.Contains(line, "via") {
				t.Fatalf("pane field = %q names the fixture's reading", line)
			}
		})
	}
}

// A Project read to have no Mate says "no mate"; one whose Mate could not
// be read says "unknown" - never the same words for both.
func TestWorkspaceRowDistinguishesAnAbsentMateFromAnUnknownOne(t *testing.T) {
	tree := sampleTree()
	tree.Projects[0].Mate = unknownMate("ListMates timed out")
	tree.Projects[0].Attention = query.UnknownField[query.ProjectAttention]("ListMates timed out")
	frame := renderFrame(t, loaded(t, tree, nil))
	lines := strings.Split(frame, "\n")
	var unknown, absent string
	for i, l := range lines {
		if unknown == "" && strings.Contains(l, "payments-api") && i+1 < len(lines) {
			unknown = lines[i+1]
		}
		if absent == "" && strings.Contains(l, "ledger-worker") && i+1 < len(lines) {
			absent = lines[i+1]
		}
	}
	if !strings.Contains(absent, "no mate") || strings.Contains(absent, "unknown") {
		t.Fatalf("absent Mate summary = %q, want no mate", absent)
	}
	if !strings.Contains(unknown, "unknown") || strings.Contains(unknown, "no mate") {
		t.Fatalf("unknown Mate summary = %q, want unknown", unknown)
	}
}
