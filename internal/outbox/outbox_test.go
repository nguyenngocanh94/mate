package outbox_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The rules of docs/mvp.md task 30, each against runtime.Fake panes and a
// fake clock: busy then empty sends exactly once, a composer the captain is
// typing into never receives anything, a restart mid-queue resumes and still
// sends once, a duplicate is not queued again, and five minutes of waiting is
// a `wedged` incident on the Mate that the next delivery resolves.

const project = "shop"

var start = time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

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

type instant struct{}

func (instant) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

type fixture struct {
	t         *testing.T
	ws        *store.Workspace
	rt        *runtime.Fake
	clock     *fakeClock
	handle    runtime.AgentHandle
	handleErr error
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ws, err := store.Init(t.TempDir())
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
	return &fixture{t: t, ws: ws, rt: rt, clock: &fakeClock{now: start}, handle: handle}
}

func (f *fixture) deps() outbox.Deps {
	return outbox.Deps{
		Harnesses: catalog.Default(),
		Runtime:   f.rt,
		Handle: func(context.Context, string) (runtime.AgentHandle, harness.Kind, error) {
			if f.handleErr != nil {
				return runtime.AgentHandle{}, "", f.handleErr
			}
			return f.handle, harness.KindClaude, nil
		},
		Clock:   f.clock,
		Sleeper: instant{},
	}
}

// sender is a sender over its own workspace handle, the way each console
// component opens its own.
func (f *fixture) sender() *outbox.Sender {
	f.t.Helper()
	ws, err := store.Open(f.ws.Root())
	if err != nil {
		f.t.Fatalf("store.Open: %v", err)
	}
	return outbox.New(ws, f.deps())
}

func (f *fixture) assign(s *outbox.Sender, key, text string) outbox.Enqueued {
	f.t.Helper()
	got, err := s.Enqueue(project, outbox.Request{Source: store.OutboxSourceAssign, Key: key, Text: text})
	if err != nil {
		f.t.Fatalf("Enqueue: %v", err)
	}
	return got
}

func (f *fixture) drain(s *outbox.Sender) {
	f.t.Helper()
	if err := s.Drain(context.Background()); err != nil {
		f.t.Fatalf("Drain: %v", err)
	}
}

func (f *fixture) typed() []string {
	var out []string
	for _, s := range f.rt.SentText {
		out = append(out, s.Text)
	}
	return out
}

func (f *fixture) items() []store.OutboxItem {
	f.t.Helper()
	items, err := f.ws.ReadOutbox(project)
	if err != nil {
		f.t.Fatalf("ReadOutbox: %v", err)
	}
	return items
}

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

const rule = "─────────────────────────────────────────"

func claudeScreen(content string) string {
	return "some transcript\n" + rule + "\n❯ " + content + "\n" + rule + "\n  Sonnet 5 · medium\n"
}

func claudeBusyScreen() string {
	return "some transcript\n✶ Pollinating…\n" + rule + "\n❯ \n" + rule + "\n"
}

const resolveLine = `resolve: k3 asked: "pick A or B" — read /w/crews/k3.status, decide, and answer with mate send shop k3 "<one line>"`

// Busy, then empty: the line waits, is typed once on the first empty
// composer, and is recorded in sent.log exactly as the old synchronous
// [assign] recorded it.
func TestABusyMateGetsTheLineOnceItsComposerEmpties(t *testing.T) {
	f := newFixture(t)
	f.rt.SetReadOutput(f.handle, claudeBusyScreen())
	s := f.sender()
	f.assign(s, "crews/k3.status@0", resolveLine)

	for i := 0; i < 3; i++ {
		f.drain(s)
		f.clock.Advance(outbox.DefaultInterval)
	}
	if typed := f.typed(); len(typed) != 0 {
		t.Fatalf("typed %#v into a Mate mid-turn", typed)
	}
	items := f.items()
	if len(items) != 1 || !items[0].Queued() || items[0].Attempts != 3 {
		t.Fatalf("outbox = %+v, want one queued item tried three times", items)
	}
	if !strings.Contains(items[0].LastRefusal, "mid-turn") {
		t.Fatalf("last refusal = %q, want internal/send's own busy observation", items[0].LastRefusal)
	}
	if sent := f.sent(); len(sent) != 0 {
		t.Fatalf("sent.log = %+v before anything was delivered", sent)
	}

	f.rt.SetReadOutput(f.handle, claudeScreen(""))
	for i := 0; i < 3; i++ {
		f.drain(s)
		f.clock.Advance(outbox.DefaultInterval)
	}

	typed := f.typed()
	if len(typed) != 1 || typed[0] != send.Marker+resolveLine {
		t.Fatalf("typed %#v, want the marked line exactly once", typed)
	}
	items = f.items()
	if items[0].State != store.OutboxSent || !items[0].SentAt.Equal(start.Add(3*outbox.DefaultInterval)) {
		t.Fatalf("outbox = %+v, want it sent on the first empty pass", items)
	}
	sent := f.sent()
	if len(sent) != 1 || sent[0].Source != store.SourceApp || sent[0].Target != store.TargetMate || sent[0].Text != resolveLine {
		t.Fatalf("sent.log = %+v, want one app -> mate line without the marker", sent)
	}
	for _, call := range f.rt.Calls {
		if call == "PromptAgent" {
			t.Fatalf("the outbox used herdr agent prompt; calls = %v", f.rt.Calls)
		}
	}
}

// The captain typing into the Mate's composer is Pending, and Pending is
// theirs: however long it lasts, nothing is typed over it.
func TestAComposerTheCaptainIsTypingIntoNeverReceivesTheLine(t *testing.T) {
	f := newFixture(t)
	f.rt.SetReadOutput(f.handle, claudeScreen("let me think about th"))
	s := f.sender()
	f.assign(s, "crews/k3.status@0", resolveLine)

	for i := 0; i < 10; i++ {
		f.drain(s)
		f.clock.Advance(outbox.DefaultInterval)
	}
	if typed := f.typed(); len(typed) != 0 {
		t.Fatalf("typed %#v over the captain's own text", typed)
	}
	if keys := f.rt.SentKeys; len(keys) != 0 {
		t.Fatalf("pressed %+v into a composer holding the captain's text", keys)
	}
	items := f.items()
	if !items[0].Queued() || !strings.Contains(items[0].LastRefusal, "unsubmitted text") {
		t.Fatalf("outbox = %+v, want it queued with the pending refusal", items)
	}
}

// A console that restarts mid-queue: the new sender reads the file and
// delivers what the old one could not, once, and the old one - were it still
// running - finds nothing left to send.
func TestARestartMidQueueResumesAndSendsOnce(t *testing.T) {
	f := newFixture(t)
	f.rt.SetReadOutput(f.handle, claudeBusyScreen())
	before := f.sender()
	f.assign(before, "crews/k3.status@0", resolveLine)
	f.drain(before)

	f.rt.SetReadOutput(f.handle, claudeScreen(""))
	after := f.sender()
	f.drain(after)
	f.drain(after)
	f.drain(before)

	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v across a restart, want exactly one line", typed)
	}
	if sent := f.sent(); len(sent) != 1 {
		t.Fatalf("sent.log = %+v, want one line", sent)
	}
}

// Two senders racing for the same item - the console's loop and an
// [assign]'s immediate attempt - still type it once: the attempt runs under
// the outbox's writers' lock.
func TestTwoSendersRacingTypeOneItemOnce(t *testing.T) {
	f := newFixture(t)
	f.assign(f.sender(), "crews/k3.status@0", resolveLine)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		s := f.sender()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Attempt(context.Background(), project); err != nil {
				t.Errorf("Attempt: %v", err)
			}
		}()
	}
	wg.Wait()
	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v from four racing senders, want one line", typed)
	}
}

// The crash window: sent.log is written right after a verified send and the
// outbox is rewritten after that. A sender that dies between the two leaves
// the item queued with its line already in sent.log; the next sender marks it
// sent from that record instead of typing it again.
func TestALineAlreadyInSentLogIsNotTypedAgain(t *testing.T) {
	f := newFixture(t)
	s := f.sender()
	f.rt.SetReadOutput(f.handle, claudeBusyScreen())
	f.assign(s, "crews/k3.status@0", resolveLine)
	if err := f.ws.AppendSent(project, store.SentEntry{
		Time: start, Source: store.SourceApp, Target: store.TargetMate, Text: resolveLine,
	}); err != nil {
		t.Fatalf("AppendSent: %v", err)
	}

	f.rt.SetReadOutput(f.handle, claudeScreen(""))
	got, err := s.Attempt(context.Background(), project)
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	if !got.Delivered {
		t.Fatalf("attempt = %+v, want the item found delivered", got)
	}
	if typed := f.typed(); len(typed) != 0 {
		t.Fatalf("typed %#v although sent.log already holds the line", typed)
	}
	if items := f.items(); items[0].State != store.OutboxSent {
		t.Fatalf("outbox = %+v, want the item marked sent", items)
	}
}

// A duplicate key is not queued again while the first is queued, nor after it
// was sent; the earlier item comes back so the caller can say when.
func TestADuplicateAssignIsNotQueuedAgain(t *testing.T) {
	f := newFixture(t)
	f.rt.SetReadOutput(f.handle, claudeBusyScreen())
	s := f.sender()
	first := f.assign(s, "crews/k3.status@0", resolveLine)
	if first.Duplicate {
		t.Fatalf("first enqueue = %+v, reported as a duplicate", first)
	}

	f.clock.Advance(time.Minute)
	again := f.assign(s, "crews/k3.status@0", resolveLine)
	if !again.Duplicate || again.Item.ID != first.Item.ID || !again.Item.At.Equal(start) {
		t.Fatalf("second enqueue = %+v, want the first item back as a duplicate", again)
	}

	f.rt.SetReadOutput(f.handle, claudeScreen(""))
	f.drain(s)
	third := f.assign(s, "crews/k3.status@0", resolveLine)
	if !third.Duplicate || third.Item.State != store.OutboxSent {
		t.Fatalf("enqueue after the send = %+v, want the sent item back as a duplicate", third)
	}
	f.drain(s)
	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v, want the one line", typed)
	}
	if items := f.items(); len(items) != 1 {
		t.Fatalf("outbox = %+v, want one item", items)
	}

	// A different entry of the same crew - its next question - is new.
	if next := f.assign(s, "crews/k3.status@40", resolveLine); next.Duplicate {
		t.Fatalf("a new question was reported as a duplicate: %+v", next)
	}
}

// Five minutes queued is `wedged` on crew `mate`, opened once, and the next
// delivery resolves it.
func TestFiveMinutesQueuedWedgesTheMateAndTheNextSendResolvesIt(t *testing.T) {
	f := newFixture(t)
	f.rt.SetReadOutput(f.handle, claudeBusyScreen())
	s := f.sender()
	f.assign(s, "crews/k3.status@0", resolveLine)

	f.drain(s)
	f.clock.Advance(outbox.DefaultWedgedAfter - time.Second)
	f.drain(s)
	if got := f.incidents(); len(got) != 0 {
		t.Fatalf("incidents = %+v before five minutes", got)
	}
	f.clock.Advance(time.Second)
	f.drain(s)
	f.clock.Advance(outbox.DefaultInterval)
	f.drain(s)
	// A restarted console does not open a second one: the file is the state.
	f.drain(f.sender())

	opened := f.incidents()
	if len(opened) != 1 || opened[0].Crew != outbox.MateCrew || opened[0].Kind != outbox.WedgedKind ||
		opened[0].State != store.IncidentOpen {
		t.Fatalf("incidents = %+v, want one open wedged incident on the Mate", opened)
	}
	if !strings.Contains(opened[0].Text, "5m0s") || !strings.Contains(opened[0].Text, "mid-turn") {
		t.Fatalf("incident text = %q, want how long and why", opened[0].Text)
	}

	f.rt.SetReadOutput(f.handle, claudeScreen(""))
	f.drain(s)
	resolved := f.incidents()
	if len(resolved) != 2 || resolved[1].State != store.IncidentResolved ||
		resolved[1].Crew != outbox.MateCrew || resolved[1].Kind != outbox.WedgedKind {
		t.Fatalf("incidents = %+v, want the wedge resolved by the delivery", resolved)
	}
	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v, want the one line", typed)
	}
}

// A Mate that is not running is the other way a line fails to arrive, and it
// waits on the same clock and wedges the same way, naming the cause.
func TestAMissingMateKeepsTheLineQueuedAndWedges(t *testing.T) {
	f := newFixture(t)
	f.handleErr = errors.New("the Mate of shop is not running")
	s := f.sender()
	f.assign(s, "crews/k3.status@0", resolveLine)
	f.drain(s)
	f.clock.Advance(outbox.DefaultWedgedAfter)
	f.drain(s)

	if items := f.items(); !items[0].Queued() || !strings.Contains(items[0].LastRefusal, "not running") {
		t.Fatalf("outbox = %+v, want the line still queued with the cause", items)
	}
	got := f.incidents()
	if len(got) != 1 || !strings.Contains(got[0].Text, "not running") {
		t.Fatalf("incidents = %+v, want a wedged incident naming the cause", got)
	}

	f.handleErr = nil
	f.drain(s)
	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v once the Mate was back, want the line", typed)
	}
}

// Only the oldest queued item is tried per pass: the Mate that just received
// a line is starting a turn, and the next one waits for it.
func TestOnePassTriesOnlyTheOldestItem(t *testing.T) {
	f := newFixture(t)
	s := f.sender()
	f.assign(s, "crews/k3.status@0", "resolve: first")
	f.assign(s, "crews/k9.status@0", "resolve: second")

	f.drain(s)
	if typed := f.typed(); len(typed) != 1 || typed[0] != send.Marker+"resolve: first" {
		t.Fatalf("typed %#v, want the oldest line alone", typed)
	}
	f.drain(s)
	if typed := f.typed(); len(typed) != 2 || typed[1] != send.Marker+"resolve: second" {
		t.Fatalf("typed %#v, want the second line on the next pass", typed)
	}
}

// An assign is the captain's keystroke and goes out in manual mode, which is
// where [assign] lives; only a digest is withdrawn when `.auto` is absent.
func TestAnAssignIsDeliveredInManualMode(t *testing.T) {
	f := newFixture(t)
	if f.ws.Auto(project) {
		t.Fatal("fixture starts in auto mode")
	}
	s := f.sender()
	f.assign(s, "crews/k3.status@0", resolveLine)
	f.drain(s)
	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v, want the assign delivered in manual mode", typed)
	}
}

// A multi-line or empty line is refused at the door, not queued to fail
// forever.
func TestEnqueueRefusesWhatCouldNeverBeSent(t *testing.T) {
	f := newFixture(t)
	s := f.sender()
	for _, req := range []outbox.Request{
		{Source: store.OutboxSourceAssign, Key: "k", Text: "two\nlines"},
		{Source: store.OutboxSourceAssign, Key: "k", Text: "  "},
		{Source: store.OutboxSourceAssign, Key: "", Text: "resolve: x"},
		{Source: "mystery", Key: "k", Text: "resolve: x"},
	} {
		if _, err := s.Enqueue(project, req); err == nil {
			t.Errorf("Enqueue(%+v) accepted it", req)
		}
	}
	if items := f.items(); len(items) != 0 {
		t.Fatalf("outbox = %+v, want nothing queued", items)
	}
}

// Start and Stop are the console's own lifecycle: the loop delivers on its
// own goroutine and stops with the workspace.
func TestStartDeliversAndStopEndsIt(t *testing.T) {
	f := newFixture(t)
	f.assign(f.sender(), "crews/k3.status@0", resolveLine)
	s := f.sender()
	s.Start(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if items := f.items(); len(items) == 1 && items[0].State == store.OutboxSent {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.Stop()
	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v, want the running loop to deliver once", typed)
	}
}

func TestMaintenanceHoldsQueuedDeliveryUntilRefreshEnds(t *testing.T) {
	f := newFixture(t)
	release, ok, err := f.ws.TryMateMaintenance(project, true)
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	defer release()
	sender := outbox.New(f.ws, f.deps())
	_, err = sender.Enqueue(project, outbox.Request{Source: store.OutboxSourceAssign, Key: "during-refresh", Text: "resolve: work"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := sender.Attempt(context.Background(), project)
	if err != nil || got.Delivered || len(f.rt.SentText) != 0 {
		t.Fatalf("sent during refresh: %+v %v", got, err)
	}
	release()
	got, err = sender.Attempt(context.Background(), project)
	if err != nil || !got.Delivered {
		t.Fatalf("lost queue after refresh: %+v %v", got, err)
	}
}
