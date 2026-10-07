package recover

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

const sessionA = "11111111-2222-3333-4444-555555555555"

// herdrDown is a Fake whose server is gone until EnsureSession brings it back,
// and which notes what was true when each call arrived.
type herdrDown struct {
	*runtime.Fake
	mu     sync.Mutex
	order  []string
	onCall func(op string)
}

func (h *herdrDown) note(op string) {
	h.mu.Lock()
	h.order = append(h.order, op)
	h.mu.Unlock()
	if h.onCall != nil {
		h.onCall(op)
	}
}

func (h *herdrDown) EnsureSession(ctx context.Context, spec runtime.SessionSpec) (runtime.SessionHandle, error) {
	h.note("EnsureSession")
	h.Fake.SessionNotRunning = false
	return h.Fake.EnsureSession(ctx, spec)
}

func (h *herdrDown) StartAgent(ctx context.Context, spec runtime.AgentStartSpec) (runtime.AgentHandle, error) {
	h.note("StartAgent")
	return h.Fake.StartAgent(ctx, spec)
}

func deps(t *testing.T, rt runtime.Adapter, names runtime.LiveNameRegistry) spawn.Deps {
	t.Helper()
	return spawn.Deps{
		Harnesses:            catalog.Default(),
		Runtime:              rt,
		Names:                names,
		ConfigHome:           t.TempDir(),
		Binary:               filepath.Join(t.TempDir(), "mate"),
		ReadinessTimeout:     time.Second,
		StartupPromptTimeout: 50 * time.Millisecond,
		StartTimeout:         10 * time.Second,
		Sleep:                func(context.Context, time.Duration) error { return nil },
		Now:                  func() time.Time { return time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC) },
		NewSessionID:         func() string { return "99999999-8888-7777-6666-555555555555" },
	}
}

// lostWorkspace is a workspace after a machine restart: the meta of a Mate and
// four crews still names panes that no longer exist. k1 is the one that was
// working; k2 is over; k3 was stopped on purpose (no pane); k4 never had a
// worktree to go back to.
func lostWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w := crewWorkspace(t, filepath.Join(t.TempDir(), "ws"))
	if err := w.WriteMateMeta("shop", map[string]string{
		spawn.MetaHarness: "claude", spawn.MetaAgent: "mate-shop", spawn.MetaPane: "w1:p1", spawn.MetaSession: "gone",
		spawn.MetaSessionID: sessionA,
	}); err != nil {
		t.Fatal(err)
	}
	k1, err := w.ReadCrewMeta("shop", "k1")
	if err != nil {
		t.Fatal(err)
	}
	k1[spawn.MetaHarness], k1[spawn.MetaAgent], k1[spawn.MetaPane], k1[spawn.MetaSessionID] = "claude", "crew-k1", "w1:p2", sessionA
	write := func(id string, meta map[string]string) {
		t.Helper()
		if err := w.WriteCrewMeta("shop", id, meta); err != nil {
			t.Fatal(err)
		}
	}
	write("k1", k1)
	write("k2", map[string]string{"task": "done", "state": "finished", "agent": "crew-k2", "pane": "w1:p3", "repo": "shop"})
	write("k3", map[string]string{"task": "paused", "state": "spawned", "agent": "crew-k3", "repo": "shop"})
	write("k4", map[string]string{"task": "no tree", "state": "spawned", "agent": "crew-k4", "pane": "w1:p4", "repo": "shop"})
	return w
}

func TestRunRestartsWhatTheMachineLostAndLeavesTheRest(t *testing.T) {
	w, _ := moved(t, lostWorkspace(t))
	fake := runtime.NewFake()
	fake.SessionNotRunning = true
	rt := &herdrDown{Fake: fake}
	attachedAtEnsure := false
	rt.onCall = func(op string) {
		if op == "EnsureSession" {
			attachedAtEnsure, _ = gitx.New().WorktreeAttached(context.Background(), w.WorktreeDir("shop", "k1"))
		}
	}
	var seen []Progress
	res := Run(context.Background(), w, deps(t, rt, fake.Names), func(p Progress) { seen = append(seen, p) })

	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if !attachedAtEnsure {
		t.Fatal("the worktree was still detached when Herdr was started: the links must be repaired first")
	}
	if len(rt.order) == 0 || rt.order[0] != "EnsureSession" {
		t.Fatalf("calls = %v, want the server started before any agent", rt.order)
	}
	var names []string
	for _, it := range res.Items {
		names = append(names, it.Name())
	}
	if got := strings.Join(names, ","); got != "mate shop,crew shop/k1,crew shop/k4" {
		t.Fatalf("restarted = %s, want the Mate and the two crews with a pane (k2 is over, k3 stopped on purpose)", got)
	}
	for _, it := range res.Items[:2] {
		if it.Err != nil || !it.Resumed {
			t.Fatalf("%s = err %v resumed %v, want it back with its conversation", it.Name(), it.Err, it.Resumed)
		}
	}
	if k4 := res.Items[2]; k4.Err == nil || !strings.Contains(k4.Err.Error(), "worktree") {
		t.Fatalf("k4 = %+v, want the missing worktree named; it must not stop the others", k4)
	}
	for id, pane := range map[string]string{"k2": "w1:p3", "k3": ""} {
		meta, _ := w.ReadCrewMeta("shop", id)
		if meta[spawn.MetaPane] != pane {
			t.Fatalf("crew %s was touched: %v", id, meta)
		}
	}
	if len(seen) == 0 || seen[len(seen)-1] != (Progress{Done: 3, Total: 3}) {
		t.Fatalf("progress = %v, want it to end at 3 of 3", seen)
	}
	mate, _ := w.ReadMateMeta("shop")
	if mate[spawn.MetaSession] == "gone" || mate[spawn.MetaResumed] != "true" {
		t.Fatalf("mate meta = %v, want a new pane and resumed", mate)
	}
}

func TestRunStartsNothingForAWorkspaceWithNothingLost(t *testing.T) {
	w := crewWorkspace(t, filepath.Join(t.TempDir(), "ws"))
	// Both stopped on purpose: no pane in either meta.
	if err := w.WriteMateMeta("shop", map[string]string{spawn.MetaHarness: "claude", spawn.MetaSessionID: sessionA}); err != nil {
		t.Fatal(err)
	}
	fake := runtime.NewFake()
	fake.SessionNotRunning = true
	rt := &herdrDown{Fake: fake}
	res := Run(context.Background(), w, deps(t, rt, fake.Names), nil)
	if !res.Idle() {
		t.Fatalf("result = %+v, want a pass with nothing to do", res)
	}
	if len(rt.order) != 0 {
		t.Fatalf("calls = %v; a workspace with nothing lost must not start a Herdr server", rt.order)
	}
}

func TestRunLeavesLiveAgentsAlone(t *testing.T) {
	w := lostWorkspace(t)
	fake := runtime.NewFake()
	rt := &herdrDown{Fake: fake}
	session, err := fake.EnsureSession(context.Background(), runtime.SessionSpec{
		WorkspaceID: runtime.WorkspaceID(store.SessionName(w.Root())), Name: w.Session(), ConfigHome: t.TempDir(), Names: fake.Names})
	if err != nil {
		t.Fatal(err)
	}
	for name, pane := range map[string]string{"mate-shop": "w1:p1", "crew-k1": "w1:p2", "crew-k4": "w1:p4"} {
		fake.PutAgent(runtime.AgentHandle{Session: session, Name: name, Tab: runtime.TabHandle{Session: session, PaneID: pane}}, runtime.AgentIdle)
	}
	res := Run(context.Background(), w, deps(t, rt, fake.Names), nil)
	if !res.Idle() || len(rt.order) != 0 {
		t.Fatalf("result = %+v calls = %v, want nothing touched when every agent is alive", res, rt.order)
	}
}

func TestRunTakesTurnsOnTheRecoveryLock(t *testing.T) {
	w := lostWorkspace(t)
	if err := os.RemoveAll(w.WorktreeDir("shop", "k1")); err != nil {
		t.Fatal(err)
	}
	unlock, err := w.LockRecover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fake := runtime.NewFake()
	fake.SessionNotRunning = true
	rt := &herdrDown{Fake: fake}
	done := make(chan Result, 1)
	go func() { done <- Run(context.Background(), w, deps(t, rt, fake.Names), nil) }()
	select {
	case <-done:
		t.Fatal("Run finished while another console held the recovery lock")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case res := <-done:
		if res.Err != nil {
			t.Fatalf("Run after the lock was released: %v", res.Err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not resume once the lock was released")
	}
}

func TestTwoConsolesRestartEachAgentOnce(t *testing.T) {
	w := lostWorkspace(t)
	fake := runtime.NewFake()
	fake.SessionNotRunning = true
	rt := &herdrDown{Fake: fake}
	var wg sync.WaitGroup
	results := make([]Result, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = Run(context.Background(), w, deps(t, rt, fake.Names), nil)
		}()
	}
	wg.Wait()
	// k4 fails every time (no worktree) and is the one thing both attempt;
	// the Mate and k1 must have been started exactly once between them.
	starts := 0
	for _, op := range rt.order {
		if op == "StartAgent" {
			starts++
		}
	}
	if starts != 2 {
		t.Fatalf("StartAgent calls = %d (%v), want 2: the Mate and k1 once each", starts, rt.order)
	}
	if len(results[0].Items)+len(results[1].Items) != 3+1 {
		t.Fatalf("items = %d + %d, want the first pass's three and the second's k4 only", len(results[0].Items), len(results[1].Items))
	}
}
