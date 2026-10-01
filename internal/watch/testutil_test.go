package watch_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/watch"
)

// The fixtures every rule test in this package runs on: a real workspace
// through internal/store, a fake Herdr, and a clock the test moves by hand.
// Nothing here fakes internal/watch itself - the tests drive Poll and then
// read `incidents.log` back through store, which is the contract mvp.md
// section 4b states.

// The two Codex screens the classifier measures (internal/harness/composer.go):
// an idle composer, and a turn in flight. The observer only ever asks
// "busy or not" and "did this change", so two are enough.
const (
	codexIdleScreen = "› Ask Codex to do anything\n\n  model · cwd\n"
	codexBusyScreen = "• Working (2s • esc to interrupt)\n› Ask Codex to do anything\n\n  model · cwd\n"
)

// fakeClock is the injected clock. The observer reads it once per round, so
// a test advances it between Polls to cross a threshold without waiting.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fixture is one workspace with one project and one open crew whose pane the
// test scripts.
type fixture struct {
	t      *testing.T
	ws     *store.Workspace
	rt     *runtime.Fake
	clock  *fakeClock
	w      *watch.Watcher
	handle runtime.AgentHandle
	// screens is the ScreenProfile handleFunc answers with: Codex's, unless
	// a test swaps it.
	screens harness.ScreenProfile
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, rt: runtime.NewFake(), clock: newClock(), screens: harness.Codex{}.Screen()}
	f.ws = newWorkspace(t)
	f.handle = runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: "mate-lab"},
		Name:    "crew-k3",
		RawID:   "k3",
		Kind:    harness.KindCodex,
		Tab:     runtime.TabHandle{PaneID: "pane-k3"},
	}
	f.recordCrew("k3")
	f.rt.PutAgent(f.handle, runtime.AgentIdle)
	f.rt.SetReadOutput(f.handle, codexIdleScreen)
	f.w = watch.New(f.ws, f.deps())
	return f
}

func (f *fixture) deps() watch.Deps {
	return watch.Deps{
		Runtime:      f.rt,
		Handle:       f.handleFunc(),
		Clock:        f.clock,
		StaleAfter:   3 * time.Minute,
		PollInterval: 5 * time.Second,
	}
}

// handleFunc is the seam cmd/mate fills with spawn.CrewHandle: it answers
// for a crew whose meta records an agent, and refuses for one that does not.
func (f *fixture) handleFunc() watch.HandleFunc {
	return func(_ context.Context, project, crew string) (runtime.AgentHandle, harness.ScreenProfile, error) {
		meta, err := f.ws.ReadCrewMeta(project, crew)
		if err != nil {
			return runtime.AgentHandle{}, nil, err
		}
		if meta["agent"] == "" {
			return runtime.AgentHandle{}, nil, os.ErrNotExist
		}
		handle := f.handle
		handle.Name = meta["agent"]
		handle.RawID = crew
		return handle, f.screens, nil
	}
}

func newWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := store.Init(dir, store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

// recordCrew writes the meta a spawned crew has: an agent and a pane, and no
// word that would close it.
func (f *fixture) recordCrew(crew string) {
	f.t.Helper()
	f.writeMeta(crew, map[string]string{
		"harness": "codex",
		"agent":   "crew-" + crew,
		"pane":    "pane-" + crew,
		"state":   "spawned",
	})
}

func (f *fixture) writeMeta(crew string, meta map[string]string) {
	f.t.Helper()
	if err := f.ws.WriteCrewMeta("shop", crew, meta); err != nil {
		f.t.Fatalf("WriteCrewMeta(%s): %v", crew, err)
	}
}

func (f *fixture) appendStatus(crew, line string) {
	f.t.Helper()
	if err := f.ws.AppendStatus("shop", crew, line); err != nil {
		f.t.Fatalf("AppendStatus(%s, %q): %v", crew, line, err)
	}
}

func (f *fixture) setScreen(screen string) {
	f.t.Helper()
	f.rt.SetReadOutput(f.handle, screen)
}

func (f *fixture) poll() {
	f.t.Helper()
	if err := f.w.Poll(context.Background()); err != nil {
		f.t.Fatalf("Poll: %v", err)
	}
}

// pollIgnoringErrors is for the rounds where a read is meant to fail: the
// round still has to finish, and the assertion is about what it did not
// write.
func (f *fixture) pollIgnoringErrors() {
	f.t.Helper()
	_ = f.w.Poll(context.Background())
}

func (f *fixture) incidents() []store.IncidentEntry {
	f.t.Helper()
	entries, _, err := f.ws.ReadIncidents("shop", 0)
	if err != nil {
		f.t.Fatalf("ReadIncidents: %v", err)
	}
	return entries
}

// assertIncidents compares the whole log against "<crew> <kind> <state>"
// lines, in order: an incident's history is the sequence, so a test that
// checked only the last line would pass on a log that opened the same
// incident three times.
func (f *fixture) assertIncidents(want ...string) {
	f.t.Helper()
	got := make([]string, 0, len(want))
	for _, e := range f.incidents() {
		got = append(got, e.Crew+" "+e.Kind+" "+e.State)
	}
	if len(got) != len(want) {
		f.t.Fatalf("incidents.log =\n  %v\nwant\n  %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			f.t.Fatalf("incidents.log =\n  %v\nwant\n  %v", got, want)
		}
	}
}

func (f *fixture) health(crew string) (watch.Health, bool) {
	f.t.Helper()
	return f.w.Health("shop", crew)
}
