package prwatch_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/prwatch"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestLatestURLReadsHandBackLinesOnly(t *testing.T) {
	const a, b = "https://github.com/acme/shop/pull/4", "https://github.com/acme/shop/pull/5"
	for name, tc := range map[string]struct {
		lines []string
		want  string
	}{
		"pr-open":                   {[]string{"working: x", "pr-open: " + a}, a},
		"a wait-mate naming one":    {[]string{"wait-mate: PR open " + b + " (not merged)"}, b},
		"the newest wins":           {[]string{"pr-open: " + a, "working: fixing review", "wait-mate: pushed again, " + b}, b},
		"a working line is not one": {[]string{"working: reading " + a}, ""},
		"none":                      {[]string{"wait-mate: ready in branch mate/k3"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			var entries []store.StatusEntry
			for _, l := range tc.lines {
				entries = append(entries, store.StatusEntry{Line: l})
			}
			if got := prwatch.LatestURL(entries); got != tc.want {
				t.Fatalf("LatestURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// writeStatus replaces the crew's status file with lines.
func writeStatus(t *testing.T, ws *store.Workspace, c string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(ws.CrewStatus(project, c), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The hellovietnam case (docs/mvp.md M19): a crew told to open a pull request
// in so many words writes `wait-mate: PR open <url>`, never runs `mate pr
// watch`, and the pull request is merged on GitHub. Sweep records the pull
// request and starts its watcher; the watcher wakes the Mate exactly once.
func TestSweepWakesTheMateForAPullRequestOnlyTheStatusNames(t *testing.T) {
	e := newEnv(t, prJSON("MERGED", ""))
	e.gh.answers[0] = prJSON("MERGED", e.mergeSHA)
	writeStatus(t, e.ws, crew, "working: pushing branch and opening PR", "wait-mate: PR open "+prURL+" (not merged)")
	setAlive(t, func(pid int) bool { return false })
	st := &fakeStarter{pid: 4242}

	swept := prwatch.Sweep(e.ws, st)
	if len(swept) != 1 || swept[0].Crew != crew || swept[0].URL != prURL || swept[0].Err != nil || len(st.specs) != 1 {
		t.Fatalf("Sweep = %+v with %d starts, want one watcher for %s", swept, len(st.specs), prURL)
	}
	if m := e.meta(); m[crewstate.MetaPRURL] != prURL || m[crewstate.MetaPRState] != crewstate.PRStateOpen {
		t.Fatalf("meta = %v, want the pull request recorded open", m)
	}

	// What the started watcher does.
	if err := prwatch.Watch(context.Background(), e.deps(), project, crew, prURL); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	typed := e.typed()
	if len(typed) != 1 || !strings.Contains(typed[0], "pr-merged: k3 "+prURL) {
		t.Fatalf("typed %q, want the one pr-merged line", typed)
	}

	// The next sweep finds it merged and starts nothing.
	if again := prwatch.Sweep(e.ws, st); len(again) != 0 || len(st.specs) != 1 {
		t.Fatalf("second Sweep = %+v, %d starts; want nothing", again, len(st.specs))
	}
}

func TestSweepStartsNothingTwice(t *testing.T) {
	e := newEnv(t, prJSON("OPEN", ""))
	st := &fakeStarter{pid: 4242}
	setAlive(t, func(pid int) bool { return pid == 4242 })
	if swept := prwatch.Sweep(e.ws, st); len(swept) != 1 || len(st.specs) != 1 {
		t.Fatalf("first Sweep = %+v, %d starts", swept, len(st.specs))
	}
	if swept := prwatch.Sweep(e.ws, st); len(swept) != 0 || len(st.specs) != 1 {
		t.Fatalf("Sweep with a live watcher = %+v, %d starts; want nothing", swept, len(st.specs))
	}
}

func TestSweepLeavesEndedPullRequestsAndClosedCrewsAlone(t *testing.T) {
	for name, meta := range map[string]map[string]string{
		"merged":      {crewstate.MetaPRURL: prURL, crewstate.MetaPRState: crewstate.PRStateMerged},
		"closed":      {crewstate.MetaPRURL: prURL, crewstate.MetaPRState: crewstate.PRStateClosed},
		"crew closed": {"state": "finished"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, prJSON("OPEN", ""))
			if err := e.ws.UpdateCrewMeta(project, crew, meta); err != nil {
				t.Fatal(err)
			}
			setAlive(t, func(int) bool { return false })
			st := &fakeStarter{pid: 1}
			if swept := prwatch.Sweep(e.ws, st); len(swept) != 0 || len(st.specs) != 0 {
				t.Fatalf("Sweep = %+v, %d starts; want nothing", swept, len(st.specs))
			}
		})
	}
}

// A crew that opens a second pull request after its first was merged gets
// the new one watched.
func TestSweepFollowsANewerPullRequest(t *testing.T) {
	const next = "https://github.com/acme/shop/pull/8"
	e := newEnv(t, prJSON("OPEN", ""))
	if err := e.ws.UpdateCrewMeta(project, crew, map[string]string{crewstate.MetaPRURL: prURL, crewstate.MetaPRState: crewstate.PRStateMerged}); err != nil {
		t.Fatal(err)
	}
	writeStatus(t, e.ws, crew, "pr-open: "+prURL, "pr-merged: "+prURL, "working: follow-up", "pr-open: "+next)
	setAlive(t, func(int) bool { return false })
	st := &fakeStarter{pid: 3}
	swept := prwatch.Sweep(e.ws, st)
	if len(swept) != 1 || swept[0].URL != next {
		t.Fatalf("Sweep = %+v, want the newer pull request", swept)
	}
	if m := e.meta(); m[crewstate.MetaPRURL] != next || m[crewstate.MetaPRState] != crewstate.PRStateOpen {
		t.Fatalf("meta = %v", m)
	}
}
