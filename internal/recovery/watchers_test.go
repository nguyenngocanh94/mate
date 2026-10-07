package recovery

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/prwatch"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// startsNothing records what recovery asked to start; the pid it hands back
// is the number of the start, so the test can tell watchers apart.
type recordingStarter struct{ specs []prwatch.Spec }

func (r *recordingStarter) Start(spec prwatch.Spec) (int, error) {
	r.specs = append(r.specs, spec)
	return 1000 + len(r.specs), nil
}

func useStarter(t *testing.T, st prwatch.Starter, alive func(int) bool) {
	t.Helper()
	old, oldAlive := WatcherStarter, prwatch.Alive
	WatcherStarter, prwatch.Alive = st, alive
	t.Cleanup(func() { WatcherStarter, prwatch.Alive = old, oldAlive })
}

// watcherWorkspace has crews in every situation a pull request watcher can
// be in after a restart of the machine.
func watcherWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w := crewWorkspace(t, filepath.Join(t.TempDir(), "ws"))
	// No pane anywhere: nothing for Herdr to bring back, only watchers.
	if err := w.WriteMateMeta("shop", map[string]string{spawn.MetaHarness: "claude", spawn.MetaSessionID: sessionA}); err != nil {
		t.Fatal(err)
	}
	write := func(id string, meta map[string]string) {
		t.Helper()
		meta["task"], meta["repo"] = "ship", "shop"
		if err := w.WriteCrewMeta("shop", id, meta); err != nil {
			t.Fatal(err)
		}
	}
	pr := func(n, state string) map[string]string {
		m := map[string]string{"state": "spawned", crewstate.MetaPRURL: "https://github.com/acme/shop/pull/" + n}
		if state != "" {
			m[crewstate.MetaPRState] = state
		}
		return m
	}
	write("k1", pr("1", crewstate.PRStateOpen))   // watcher died: restart
	write("k2", pr("2", ""))                      // pr_state never written: restart
	write("k3", pr("3", crewstate.PRStateMerged)) // over: leave
	write("k4", pr("4", crewstate.PRStateClosed)) // over: leave
	write("k5", pr("5", crewstate.PRStateOpen))   // watcher alive: leave
	if err := prwatch.WritePID(w, "shop", "k5", 777); err != nil {
		t.Fatal(err)
	}
	k6 := pr("6", crewstate.PRStateOpen) // the crew itself was closed: leave
	k6["state"] = "finished"
	write("k6", k6)
	write("k7", map[string]string{"state": "spawned"}) // no pull request at all
	return w
}

func TestRunRestartsADeadPullRequestWatcherAndOnlyThat(t *testing.T) {
	w := watcherWorkspace(t)
	st := &recordingStarter{}
	useStarter(t, st, func(pid int) bool { return pid == 777 })
	fake := runtime.NewFake()
	fake.SessionNotRunning = true
	rt := &herdrDown{Fake: fake}

	res := Run(context.Background(), w, deps(t, rt, fake.Names), nil)

	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	var got []string
	for _, wt := range res.Watchers {
		if wt.Err != nil {
			t.Fatalf("%s: %v", wt.Name(), wt.Err)
		}
		got = append(got, wt.Crew+":"+strconv.Itoa(wt.PID))
	}
	if len(got) != 2 || got[0] != "k1:1001" || got[1] != "k2:1002" {
		t.Fatalf("restarted %v, want k1 and k2 only (k3, k4 ended; k5 alive; k6 closed; k7 has no pull request)", got)
	}
	for i, id := range []string{"k1", "k2"} {
		want := "pr watch --run --workspace " + w.Root() + " shop " + id + " https://github.com/acme/shop/pull/" + strconv.Itoa(i+1)
		if joined := join(st.specs[i].Args); joined != want {
			t.Fatalf("started %q, want %q", joined, want)
		}
		if prwatch.ReadPID(w, "shop", id) != 1001+i {
			t.Fatalf("%s pid file = %d", id, prwatch.ReadPID(w, "shop", id))
		}
	}
	if prwatch.ReadPID(w, "shop", "k5") != 777 {
		t.Fatal("a live watcher's pid file was replaced")
	}
	if len(rt.order) != 0 {
		t.Fatalf("Herdr calls %v: restarting watchers must not start a server", rt.order)
	}
	if res.Idle() {
		t.Fatal("a pass that restarted watchers reports itself idle")
	}

	// A second pass finds the watchers alive and does nothing.
	useStarter(t, st, func(pid int) bool { return pid == 777 || pid == 1001 || pid == 1002 })
	if again := Run(context.Background(), w, deps(t, rt, fake.Names), nil); !again.Idle() || len(st.specs) != 2 {
		t.Fatalf("second pass = %+v with %d starts", again, len(st.specs))
	}
}

func join(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
