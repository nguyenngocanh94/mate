package autopilot_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/box"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/store"
)

const project = "shop"

// start is the moment every fixture's clock begins at, so a digest line in a
// failure message is the same one on every run.
var start = time.Date(2026, 9, 18, 14, 30, 0, 0, time.UTC)

// fakeClock is the injected clock of mvp.md task 19: every threshold in the
// daemon - the digest window, the five minutes before `wedged` - is crossed
// by moving this rather than by waiting.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *fakeClock { return &fakeClock{now: start} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// instant is a Sleeper that never waits, so a test drives real durations
// through send.Send's settle and enter retries at memory speed. The hook, if
// set, runs on every sleep - which is how a test driving the running daemon
// knows a tick has finished without waiting ninety seconds for it.
type instant struct{ hook func() }

func (i instant) Sleep(ctx context.Context, _ time.Duration) error {
	if i.hook != nil {
		i.hook()
	}
	return ctx.Err()
}

type fixture struct {
	t      *testing.T
	ws     *store.Workspace
	rt     *runtime.Fake
	clock  *fakeClock
	handle runtime.AgentHandle
	pilot  *autopilot.Pilot
	// handleErr, when set, is what the Mate handle resolver returns instead
	// of a pane - the Mate is not running, or Herdr is not answering.
	handleErr error
	// onHandle, when set, runs while the handle is being resolved, which is
	// the window between the daemon reading the flag and typing the line.
	onHandle func()
}

// newFixture builds a workspace with one registered project, a fake Herdr
// holding one live Mate pane showing an empty Claude composer, and a daemon
// over both.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	ws, err := store.Init(root, store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(ws.Root(), project)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ws.AddProject(project, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	rt := runtime.NewFake()
	handle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: "fm-lab-test"},
		Name:    "mate-" + project,
		RawID:   project,
		Kind:    harness.KindClaude,
		Tab:     runtime.TabHandle{PaneID: "w1:p1"},
	}
	rt.PutAgent(handle, runtime.AgentIdle)
	rt.SetReadOutput(handle, claudeScreen(""))

	f := &fixture{t: t, ws: ws, rt: rt, clock: newClock(), handle: handle}
	f.pilot = autopilot.New(ws, f.deps(nil))
	return f
}

// deps builds the daemon's collaborators over this fixture: an outbox sender
// over the fake Herdr, and the daemon over that. onSleep, when given, runs at
// the end of every tick of a running daemon.
func (f *fixture) deps(onSleep func()) autopilot.Deps {
	return f.depsOver(f.ws, onSleep)
}

func (f *fixture) depsOver(ws *store.Workspace, onSleep func()) autopilot.Deps {
	return autopilot.Deps{
		Outbox:  outbox.New(ws, f.outboxDeps()),
		Clock:   f.clock,
		Sleeper: instant{hook: onSleep},
	}
}

func (f *fixture) outboxDeps() outbox.Deps {
	return outbox.Deps{
		Harnesses: catalog.Default(),
		Runtime:   f.rt,
		Handle: func(context.Context, string) (runtime.AgentHandle, harness.Kind, error) {
			if f.onHandle != nil {
				f.onHandle()
			}
			if f.handleErr != nil {
				return runtime.AgentHandle{}, "", f.handleErr
			}
			return f.handle, harness.KindClaude, nil
		},
		Clock:   f.clock,
		Sleeper: instant{},
	}
}

// drain is one pass of the console's outbox loop over this workspace.
func (f *fixture) drain() {
	f.t.Helper()
	ws, err := store.Open(f.ws.Root())
	if err != nil {
		f.t.Fatalf("store.Open: %v", err)
	}
	if err := outbox.New(ws, f.outboxDeps()).Drain(context.Background()); err != nil {
		f.t.Fatalf("Drain: %v", err)
	}
}

// outboxItems is the project's `mate/.outbox`.
func (f *fixture) outboxItems() []store.OutboxItem {
	f.t.Helper()
	items, err := f.ws.ReadOutbox(project)
	if err != nil {
		f.t.Fatalf("ReadOutbox: %v", err)
	}
	return items
}

// restart is a second daemon over the same workspace, as a reopened console
// builds: same files, no memory of the first one.
func (f *fixture) restart() *autopilot.Pilot {
	f.t.Helper()
	ws, err := store.Open(f.ws.Root())
	if err != nil {
		f.t.Fatalf("store.Open: %v", err)
	}
	return autopilot.New(ws, f.depsOver(ws, nil))
}

// addProject registers a second project, manual by default.
func (f *fixture) addProject(name string) {
	f.t.Helper()
	repo := filepath.Join(f.ws.Root(), name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := f.ws.AddProject(name, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		f.t.Fatalf("AddProject(%s): %v", name, err)
	}
}

func (f *fixture) auto(on bool) {
	f.t.Helper()
	if err := f.ws.SetAuto(project, on); err != nil {
		f.t.Fatalf("SetAuto(%v): %v", on, err)
	}
}

func (f *fixture) status(crew, line string) {
	f.t.Helper()
	if err := f.ws.AppendStatus(project, crew, line); err != nil {
		f.t.Fatalf("AppendStatus(%s, %q): %v", crew, line, err)
	}
}

func (f *fixture) incident(crew string, kind box.IncidentKind, state, text string) {
	f.t.Helper()
	if err := f.ws.AppendIncident(project, store.IncidentEntry{
		Time:  f.clock.Now(),
		Crew:  crew,
		Kind:  string(kind),
		State: state,
		Text:  text,
	}); err != nil {
		f.t.Fatalf("AppendIncident: %v", err)
	}
}

func (f *fixture) crewMeta(crew string, meta map[string]string) {
	f.t.Helper()
	if err := f.ws.WriteCrewMeta(project, crew, meta); err != nil {
		f.t.Fatalf("WriteCrewMeta: %v", err)
	}
}

func (f *fixture) tick() error {
	f.t.Helper()
	return f.pilot.Tick(context.Background())
}

func (f *fixture) mustTick() {
	f.t.Helper()
	if err := f.tick(); err != nil {
		f.t.Fatalf("Tick: %v", err)
	}
}

// typed is every line the daemon put into the Mate's composer, in order.
func (f *fixture) typed() []string {
	f.t.Helper()
	var out []string
	for _, sent := range f.rt.SentText {
		out = append(out, sent.Text)
	}
	return out
}

// sent is what `sent.log` records, which is the daemon's own claim about what
// the Mate received.
func (f *fixture) sent() []store.SentEntry {
	f.t.Helper()
	entries, _, err := f.ws.ReadSent(project, 0)
	if err != nil {
		f.t.Fatalf("ReadSent: %v", err)
	}
	return entries
}

func (f *fixture) incidents() []store.IncidentEntry {
	f.t.Helper()
	entries, _, err := f.ws.ReadIncidents(project, 0)
	if err != nil {
		f.t.Fatalf("ReadIncidents: %v", err)
	}
	return entries
}

func (f *fixture) view() box.View {
	f.t.Helper()
	v, err := box.Load(f.ws, project)
	if err != nil {
		f.t.Fatalf("box.Load: %v", err)
	}
	return v
}

func (f *fixture) cursor() map[string]int64 {
	f.t.Helper()
	c, err := f.ws.ReadAutoCursor(project)
	if err != nil {
		f.t.Fatalf("ReadAutoCursor: %v", err)
	}
	return c
}

func (f *fixture) daemonStatus() autopilot.Status {
	f.t.Helper()
	return f.pilot.Snapshot()[project]
}

// requireOneDigest asserts the daemon typed exactly one line and returns it.
func (f *fixture) requireOneDigest() string {
	f.t.Helper()
	typed := f.typed()
	if len(typed) != 1 {
		f.t.Fatalf("typed %d line(s), want exactly one digest: %#v", len(typed), typed)
	}
	if !strings.HasPrefix(typed[0], send.Marker) {
		f.t.Fatalf("the digest carries no from-app marker: %q", typed[0])
	}
	return typed[0]
}

const rule = "─────────────────────────────────────────"

// claudeScreen renders Claude's composer box holding content, the way
// internal/send's own fixtures and the captures under
// internal/harness/testdata/screens draw it.
func claudeScreen(content string) string {
	return "some transcript\n" + rule + "\n❯ " + content + "\n" + rule + "\n  Sonnet 5 · medium\n"
}

func claudeBusyScreen() string {
	return "some transcript\n✶ Pollinating…\n" + rule + "\n❯ \n" + rule + "\n"
}
