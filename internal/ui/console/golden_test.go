package console

// The golden-frame harness. Four things, in the order you will want them:
//
//	newFixture(t, tree, w, h, g)   a loaded Model, deterministic and colourless
//	renderFrame(t, m)              its View, checked against the frame contract
//	assertGolden(t, name, frame)   diff it against testdata/golden/<name>.txt
//	goldenFrame(t, name, ...)      all three in one call, the usual entry point
//
// A whole test is normally one line:
//
//	func TestCrewListAt120(t *testing.T) {
//	    goldenFrame(t, "crew-list-120x36", sampleTree(), 120, 36, unicodeGlyphs)
//	}
//
// To accept a deliberate change, run the test with -update and read the
// diff in git:
//
//	go test ./internal/ui/console -run TestCrewListAt120 -update
//
// Fixtures are plain text: every fixture is rendered with plainPalette, so
// nothing in a frame is carried by colour alone - if a state is invisible
// in a fixture, it is invisible to a reader with a monochrome terminal too.
// Every line is space-padded to exactly w cells, so fixtures have trailing
// whitespace on purpose; do not strip it.
//
// renderFrame asserts the frame contract on every call - exactly h lines of
// exactly w display cells - so a golden test also covers geometry, and a
// pane that miscounts its width fails with the offending line and column
// rather than with a wall of shifted text.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden fixtures under testdata/golden")

// ansiPalette is defaultPalette on a renderer with a pinned 16-colour
// profile. A test that asserts colour must use it: lipgloss's default
// renderer resolves the profile from the process's own stdout, which under
// `go test` is a pipe, so every style would render as plain text and a
// colour assertion would pass or fail for the wrong reason.
func ansiPalette() palette {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI)
	return paletteFor(r)
}

// cellIndex is the display column a substring starts at - strings.Index
// gives a byte offset, which is not a column once a frame contains a
// two-cell rune or a box-drawing glyph.
func cellIndex(line, substr string) int {
	i := strings.Index(line, substr)
	if i < 0 {
		return -1
	}
	return cells(line[:i])
}

// cellAt is the rune at a display column.
func cellAt(line string, col int) string {
	used := 0
	for _, r := range line {
		if used == col {
			return string(r)
		}
		used += cells(string(r))
		if used > col {
			return string(r)
		}
	}
	return ""
}

// goldenAsOf is the read time every fixture's header shows. A frame carries
// "As of HH:MM:SS", so a fixture needs a clock that does not move.
var goldenAsOf = time.Date(2026, 9, 10, 14, 2, 11, 0, time.UTC)

// newFixture builds a Console loaded with tree, sized w x h, drawn with
// glyph set g and no colour. The size arrives the only way the Console
// accepts one - a tea.WindowSizeMsg - so a fixture exercises the same
// relayout path a real resize does.
//
// tree.AsOf is pinned to goldenAsOf here rather than left to the caller:
// the header's "As of" comes straight from Snapshot.AsOf (there is no
// UI-owned clock), so this is the one place every fixture's read time is
// fixed.
func newFixture(t *testing.T, tree query.Snapshot, w, h int, g glyphSet) Model {
	t.Helper()
	tree.AsOf = goldenAsOf
	m := New(
		func(context.Context) (query.Snapshot, error) { return tree, nil },
		func(string) *exec.Cmd { return exec.Command("true") },
	)
	m.g = g
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m, _ = send(t, m, m.Init()())
	return m
}

// newFailedFixture is newFixture for a read that fails outright: the model
// is sized, the first load returns err, and the frame is the error screen.
func newFailedFixture(t *testing.T, err error, w, h int, g glyphSet) Model {
	t.Helper()
	m := New(
		func(context.Context) (query.Snapshot, error) { return query.Snapshot{}, err },
		func(string) *exec.Cmd { return exec.Command("true") },
	)
	m.g = g
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m, _ = send(t, m, m.Init()())
	return m
}

// renderFrame renders m and asserts the frame contract before returning:
// exactly h lines, each exactly w display cells. Every test that renders
// should go through this rather than calling View directly.
func renderFrame(t *testing.T, m Model) string {
	t.Helper()
	frame := m.View()
	assertFrameShape(t, frame, m.w, m.h)
	return frame
}

// assertFrameShape is the frame contract, on its own, for callers that
// build a frame some other way.
func assertFrameShape(t *testing.T, frame string, w, h int) {
	t.Helper()
	if w <= 0 || h <= 0 {
		if frame != "" {
			t.Fatalf("frame at %dx%d = %q, want empty before the first size message", w, h, frame)
		}
		return
	}
	lines := strings.Split(frame, "\n")
	if len(lines) != h {
		t.Fatalf("frame at %dx%d has %d lines, want exactly %d:\n%s", w, h, len(lines), h, numbered(frame))
	}
	for i, l := range lines {
		if n := cells(l); n != w {
			t.Fatalf("frame at %dx%d line %d is %d cells, want exactly %d:\n|%s|", w, h, i, n, w, l)
		}
	}
}

// goldenFrame is the one-call entry point: build, render, compare.
func goldenFrame(t *testing.T, name string, tree query.Snapshot, w, h int, g glyphSet) Model {
	t.Helper()
	m := newFixture(t, tree, w, h, g)
	assertGolden(t, name, renderFrame(t, m))
	return m
}

// assertGolden compares a rendered frame against testdata/golden/<name>.txt.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".txt")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("wrote golden %s", path)
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\n\naccept the current output with:\n  go test ./internal/ui/console -run '%s' -update",
			path, err, t.Name())
	}
	want := strings.TrimSuffix(string(raw), "\n")
	if want == got {
		return
	}
	t.Fatalf("golden %s mismatch\n%s\naccept the current output with:\n  go test ./internal/ui/console -run '%s' -update",
		path, frameDiff(want, got), t.Name())
}

// frameDiff prints only the lines that differ, each with its cell width and
// a caret under the first differing column - the two things that are
// invisible in a raw string comparison of a space-padded frame.
func frameDiff(want, got string) string {
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "lines: want %d, got %d\n", len(wl), len(gl))
	n := len(wl)
	if len(gl) > n {
		n = len(gl)
	}
	shown := 0
	for i := 0; i < n; i++ {
		w, g := lineAt(wl, i), lineAt(gl, i)
		if w == g {
			continue
		}
		shown++
		if shown > 12 {
			fmt.Fprintf(&b, "... and more differing lines\n")
			break
		}
		fmt.Fprintf(&b, "line %2d\n  want (%3d cells) |%s|\n  got  (%3d cells) |%s|\n  %s\n",
			i, cells(w), w, cells(g), g, caretUnder(w, g))
	}
	return b.String()
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<missing>"
}

// caretUnder marks the first display column where the two lines diverge -
// display, not rune, so the caret lines up under the frame printed above it
// even when the line contains two-cell runes.
func caretUnder(want, got string) string {
	wr, gr := []rune(want), []rune(got)
	col, i := 0, 0
	for i < len(wr) && i < len(gr) && wr[i] == gr[i] {
		col += cells(string(wr[i]))
		i++
	}
	indent := strings.Repeat(" ", len("  got  (000 cells) |")-2)
	return indent + strings.Repeat(" ", col) + "^ first difference at column " + fmt.Sprint(col)
}

func numbered(frame string) string {
	var b strings.Builder
	for i, l := range strings.Split(frame, "\n") {
		fmt.Fprintf(&b, "%2d |%s|\n", i, l)
	}
	return b.String()
}

// send applies one message and asserts the model type back, which is what
// every Bubble Tea test in this package does between assertions.
func send(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	return got, cmd
}

// key builds the tea.KeyMsg for a key name as onKey sees it.
func key(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdn":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// sampleTree is the shared fixture snapshot: one workspace, two Projects,
// and a Task with two Crew attempts whose fields cover Known, Absent and
// Unknown, a stale binding, and real ULID-shaped ids and worktree paths -
// the values that make truncation and wrapping visible in a fixture.
// notErrorState is the Reason a Field[ErrorReason] carries when a status is
// not an error state at all - Absent, not "no reason recorded" (see
// query.ErrorReason's own doc comment).
const notErrorState = "not an error state"

// absentMate is a Project with no designated Mate, shaped the way
// the store-backed loader shapes one: every dependent field carries the
// designation's Absent state forward rather than being left unset.
func absentMate(why string) query.MateNode {
	return query.MateNode{
		Designated: query.AbsentField[query.MateIdentity](why),
		AgentName:  query.AbsentField[string](why),
		Binding:    query.AbsentField[query.BindingValue](why),
		LastEvent:  query.AbsentField[query.EventValue](why),
		Error:      query.AbsentField[query.ErrorReason](notErrorState),
	}
}

// unknownMate is absentMate's Unknown twin: the designation read itself
// failed, so every dependent field carries Unknown forward rather than
// guessing Absent for a Mate that might exist.
func unknownMate(reason string) query.MateNode {
	return query.MateNode{
		Designated: query.UnknownField[query.MateIdentity](reason),
		AgentName:  query.UnknownField[string](reason),
		Binding:    query.UnknownField[query.BindingValue](reason),
		LastEvent:  query.UnknownField[query.EventValue](reason),
		Error:      query.UnknownField[query.ErrorReason](reason),
	}
}

func sampleTree() query.Snapshot {
	return query.Snapshot{
		WorkspaceID: "ws_acme",
		Workspace: query.KnownField(query.WorkspaceValue{
			Name: "acme", Root: "/Users/dev/work/acme",
		}),
		Projects: []query.ProjectNode{
			{
				ProjectID: "proj_01J9M1F8K2Q7C4H6N0R3V5T8YZ",
				Mode:      query.ModeManual,
				Name:      "payments-api",
				Mate: query.MateNode{
					Designated: query.KnownField(query.MateIdentity{
						MateID: "mate_01J9M2G9N3X8D5J0B4H7V2T1WK", HarnessKind: query.HarnessClaude,
						Status: query.MateRunning, IsDefault: true,
					}),
					AgentName: query.KnownField("mate-payments-api"),
					Binding: query.KnownNote(query.BindingValue{
						Status: query.BindingActive, AgentName: "mate-payments-api",
						Runtime: "herdr", Session: "mate-acme", Tab: "mate/payments-api", Pane: "p-3f2a",
						BoundSince: time.Date(2026, 9, 10, 9, 14, 2, 0, time.UTC), BoundSinceKind: query.BoundSinceActivated,
					}, "recorded active; this does not prove the agent is alive"),
					LastEvent: query.KnownField(query.EventValue{
						EventType: "crew.started", OccurredAt: time.Date(2026, 9, 10, 13, 41, 5, 0, time.UTC),
					}),
					Error: query.AbsentField[query.ErrorReason](notErrorState),
				},
				Repos: query.KnownField([]query.RepoValue{
					{RepoID: "repo_01J9M1E7K1V6B3G8Z2F5S0R9TH", DisplayName: "payments-api", Path: "/Users/dev/work/acme/repos/payments-api", DefaultBranch: "main"},
				}),
				Crews: []query.CrewNode{
					{
						CrewID: "crew_01J9P4Q5R6S7T8U9V0W1X2A7CS",
						RepoID: "repo_01J9M1E7K1V6B3G8Z2F5S0R9TH",
						Task:   "Fix webhook idempotency so retried Stripe deliveries do not double-charge",
						Status: query.CrewFailed,
						// `failed` is terminal, so this row lives in the
						// Completed group (mvp.md section 4b).
						Closed:      true,
						HarnessKind: query.HarnessCodex,
						Repo: query.KnownField(query.RepoValue{
							RepoID: "repo_01J9M1E7K1V6B3G8Z2F5S0R9TH", DisplayName: "payments-api",
							Path: "/Users/dev/work/acme/repos/payments-api", DefaultBranch: "main",
						}),
						Worktree: query.KnownField(query.WorktreeValue{
							Path:   "/Users/dev/work/acme/.worktrees/payments-api-crew_01J9P4Q5R6S7T8U9V0W1X2A7CS",
							Branch: "matev2/01J9P4Q5",
							Status: query.WorktreeRecordedCreated,
						}),
						AgentName: query.KnownField("crew-payments-api-1"),
						Binding: query.KnownNote(query.BindingValue{
							Status: query.BindingStale, AgentName: "crew-payments-api-1",
							Runtime: "herdr", Session: "mate-acme", Tab: "crew/01J9P4P5", Pane: "p-2b91",
							BoundSince: time.Date(2026, 9, 10, 10, 2, 14, 0, time.UTC), BoundSinceKind: query.BoundSinceActivated,
						}, "recorded stale: matev2 could not confirm the agent stopped, and attach is refused while stale"),
						LastEvent: query.KnownField(query.EventValue{
							EventType: "crew.failed", OccurredAt: time.Date(2026, 9, 10, 13, 21, 37, 0, time.UTC),
						}),
						Error: query.KnownField(query.ErrorReason("harness exited 1 before reporting")),
						Attention: query.KnownField(query.Attention{
							Kind: query.AttentionFailed,
							Why:  "crew crew_01J9P4Q5R6S7T8U9V0W1X2A7CS failed: harness exited 1 before reporting",
						}),
					},
					{
						CrewID:      "crew_01J9P6Q6W0E5V8XK2M4B8DT",
						RepoID:      "repo_01J9M1E7K1V6B3G8Z2F5S0R9TH",
						Task:        "Add idempotency-key index",
						Status:      query.CrewWorking,
						HarnessKind: query.HarnessClaude,
						Repo: query.KnownField(query.RepoValue{
							RepoID: "repo_01J9M1E7K1V6B3G8Z2F5S0R9TH", DisplayName: "payments-api",
							Path: "/Users/dev/work/acme/repos/payments-api", DefaultBranch: "main",
						}),
						Worktree: query.KnownField(query.WorktreeValue{
							Path:   "/Users/dev/work/acme/.worktrees/payments-api-crew_01J9P6Q6W0E5V8XK2M4B8DT",
							Branch: "matev2/01J9P6Q6",
							Status: query.WorktreeRecordedCreated,
						}),
						AgentName: query.KnownField("crew-payments-api-2"),
						Binding: query.KnownNote(query.BindingValue{
							Status: query.BindingActive, AgentName: "crew-payments-api-2",
							Runtime: "herdr", Session: "mate-acme", Tab: "crew/01J9P6Q6", Pane: "p-7c1e",
							BoundSince: time.Date(2026, 9, 10, 13, 41, 5, 0, time.UTC), BoundSinceKind: query.BoundSinceActivated,
						}, "recorded active; this does not prove the agent is alive"),
						LastEvent: query.KnownField(query.EventValue{
							EventType: "agent.tool_call", OccurredAt: time.Date(2026, 9, 10, 14, 1, 58, 0, time.UTC),
						}),
						Error:     query.AbsentField[query.ErrorReason](notErrorState),
						Attention: query.AbsentField[query.Attention]("crew crew_01J9P6Q6W0E5V8XK2M4B8DT is recorded running and nothing about it needs attention"),
					},
				},
				Attention: query.KnownField(query.ProjectAttention{CrewsNeedingAttention: 1}),
				Box:       sampleBox(),
			},
			{
				ProjectID: "proj_01J9M4H7K9L1M3N5P7Q9R1S3TU",
				Mode:      query.ModeAuto,
				Name:      "ledger-worker",
				Mate:      absentMate("this project has no designated Mate"),
				// A project whose crews directory is empty still reads
				// successfully: box.Load returns an empty view, which is Known
				// and not Absent - "nothing has been written yet" is a fact,
				// not a failed read.
				Box: query.KnownField(query.BoxView{}),
				Attention: query.KnownField(query.ProjectAttention{
					Kind: query.AttentionNoMate, Why: "the project has no designated Mate, so no crew can be spawned",
				}),
			},
		},
	}
}

// sampleBox is the fixture project's message box (mvp.md task 15): two crew
// status lines - one of them the needs-decision the rail must highlight -
// and two messages, one of which is the app's own forwarded signal. It is
// the same vocabulary internal/box produces, already flattened by
// internal/query, so a fixture cannot render a combination the merge cannot.
func sampleBox() query.Field[query.BoxView] {
	at := func(h, m int) time.Time { return time.Date(2026, 9, 10, h, m, 0, 0, time.UTC) }
	return query.KnownField(query.BoxView{
		Entries: []query.BoxEntry{
			{
				Seq: 0, At: at(13, 41), Kind: query.BoxStatus, Source: "crew", Target: "crew:k3",
				Crew: "k3", Verb: "working", Text: "reading the ticket",
				Resolve: testResolveLine("k3", "reading the ticket"),
			},
			{
				Seq: 1, At: at(13, 52), Kind: query.BoxMessage, Source: "user", Target: "mate",
				Text: "spawn a crew for the webhook fix",
			},
			{
				Seq: 2, At: at(14, 1), Kind: query.BoxStatus, Source: "crew", Target: "crew:k3",
				Crew: "k3", Verb: "needs-decision",
				Text:      "migration for idempotency_keys, or key off stripe_events?",
				Attention: true, Resolve: testResolveLine("k3", "migration for idempotency_keys, or key off stripe_events?"),
			},
			{
				Seq: 3, At: at(14, 2), Kind: query.BoxMessage, Source: "app", Target: "mate",
				Text: testResolveLine("k3", "migration for idempotency_keys, or key off stripe_events?"),
			},
		},
		Inbox: []query.BoxEntry{{
			Seq: 2, At: at(14, 1), Kind: query.BoxStatus, Source: "crew", Target: "crew:k3",
			Crew: "k3", Verb: "needs-decision", Text: "migration for idempotency_keys, or key off stripe_events?",
			Attention: true, Resolve: testResolveLine("k3", "migration for idempotency_keys, or key off stripe_events?"),
		}},
		Crews: 1, Awaiting: 1, LastAt: at(14, 2),
	})
}
