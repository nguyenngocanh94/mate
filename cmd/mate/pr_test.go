package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/prwatch"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// recordingStarter is the detach seam of `mate pr watch`: it starts nothing.
type recordingStarter struct {
	specs []prwatch.Spec
	pid   int
}

func (r *recordingStarter) Start(spec prwatch.Spec) (int, error) {
	r.specs = append(r.specs, spec)
	return r.pid, nil
}

func usePRStarter(t *testing.T, st prwatch.Starter) {
	t.Helper()
	old := prStarter
	prStarter = st
	t.Cleanup(func() { prStarter = old })
	oldAlive := prwatch.Alive
	prwatch.Alive = func(pid int) bool { return pid == 4242 }
	t.Cleanup(func() { prwatch.Alive = oldAlive })
}

const testPR = "https://github.com/acme/shop/pull/7"

func prWorkspace(t *testing.T) (string, *store.Workspace) {
	t.Helper()
	ws, _ := projectWithOrigin(t, "git@github.com:acme/shop.git")
	w, err := store.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{"state": "spawned", "branch": "mate/k3"}); err != nil {
		t.Fatal(err)
	}
	return ws, w
}

func TestPRWatchDetachesRecordsAndReturns(t *testing.T) {
	ws, w := prWorkspace(t)
	st := &recordingStarter{pid: 4242}
	usePRStarter(t, st)
	var out, errw bytes.Buffer

	if err := cmdPRWatch([]string{"--workspace", ws, "shop", "k3", testPR}, &out, &errw); err != nil {
		t.Fatalf("pr watch: %v", err)
	}
	if len(st.specs) != 1 {
		t.Fatalf("started %d watchers, want 1", len(st.specs))
	}
	if got, want := strings.Join(st.specs[0].Args, " "), "pr watch --run --workspace "+w.Root()+" shop k3 "+testPR; got != want {
		t.Fatalf("child args = %q, want %q", got, want)
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[crewstate.MetaPRURL] != testPR || meta[crewstate.MetaPRState] != crewstate.PRStateOpen || meta["branch"] != "mate/k3" {
		t.Fatalf("meta = %v", meta)
	}
	data, err := os.ReadFile(filepath.Join(w.CrewsDir("shop"), "k3.prwatch"))
	if err != nil || strings.TrimSpace(string(data)) != "4242" {
		t.Fatalf(".prwatch = %q, %v", data, err)
	}
	if !strings.Contains(out.String(), "watching "+testPR) {
		t.Fatalf("output = %q", out.String())
	}

	// A second call while the watcher lives starts nothing.
	out.Reset()
	if err := cmdPRWatch([]string{"--workspace", ws, "shop", "k3", testPR}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if len(st.specs) != 1 || !strings.Contains(out.String(), "already watching") {
		t.Fatalf("starts = %d, output = %q", len(st.specs), out.String())
	}
}

func TestPRWatchRefusesWhatItCannotWatch(t *testing.T) {
	ws, w := prWorkspace(t)
	st := &recordingStarter{pid: 4242}
	usePRStarter(t, st)
	var out, errw bytes.Buffer

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"shop", "k3", "https://example.com/not-a-pr"}, "not a pull request URL"},
		{[]string{"shop", "nosuch", testPR}, "no crew nosuch"},
		{[]string{"nosuch", "k3", testPR}, "no such project"},
		{[]string{"shop", "k3"}, "want exactly 3 arguments"},
	} {
		err := cmdPRWatch(append([]string{"--workspace", ws}, tc.args...), &out, &errw)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}

	if err := w.UpdateCrewMeta("shop", "k3", map[string]string{"state": "finished"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdPRWatch([]string{"--workspace", ws, "shop", "k3", testPR}, &out, &errw); err == nil || !strings.Contains(err.Error(), "is closed") {
		t.Fatalf("closed crew: err = %v", err)
	}
	if len(st.specs) != 0 {
		t.Fatalf("started %d watchers for refused calls", len(st.specs))
	}
}

func TestPRWatchOfAnEndedPullRequestStartsNothing(t *testing.T) {
	ws, w := prWorkspace(t)
	st := &recordingStarter{pid: 4242}
	usePRStarter(t, st)
	if err := w.UpdateCrewMeta("shop", "k3", map[string]string{crewstate.MetaPRURL: testPR, crewstate.MetaPRState: crewstate.PRStateMerged}); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if err := cmdPRWatch([]string{"--workspace", ws, "shop", "k3", testPR}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if len(st.specs) != 0 || !strings.Contains(out.String(), "already merged") {
		t.Fatalf("starts = %d, output = %q", len(st.specs), out.String())
	}
}

// A closed pull request is watched again: it may have been reopened
// (docs/mvp.md M19), and only a merge ends a pull request for good.
func TestPRWatchOfAClosedPullRequestWatchesAgain(t *testing.T) {
	ws, w := prWorkspace(t)
	st := &recordingStarter{pid: 4242}
	usePRStarter(t, st)
	if err := w.UpdateCrewMeta("shop", "k3", map[string]string{crewstate.MetaPRURL: testPR, crewstate.MetaPRState: crewstate.PRStateClosed}); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if err := cmdPRWatch([]string{"--workspace", ws, "shop", "k3", testPR}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if len(st.specs) != 1 || !strings.Contains(out.String(), "watching "+testPR) {
		t.Fatalf("starts = %d, output = %q", len(st.specs), out.String())
	}
	if meta, _ := w.ReadCrewMeta("shop", "k3"); meta[crewstate.MetaPRState] != crewstate.PRStateOpen {
		t.Fatalf("pr_state = %q, want open again", meta[crewstate.MetaPRState])
	}
}
