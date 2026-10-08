package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// The tool goldens pin, byte for byte, what the two outside tools mate
// drives from this package look like today: the command the review tab
// runs Fresh with, and the Beads part of `mate recall`. They are the safety
// net for moving Beads and Fresh behind the tool registry
// (docs/plans/workspace-layout-and-tools-2026-10-08.md, PR 0). A diff here
// is a behaviour change; rerun with MATE_UPDATE_GOLDEN=1 only when that
// change is intended, and read the golden diff before committing it.

// TestReviewFreshArgvGolden drives `e` the way the Console does: the
// columns are planned by newConsoleColumns from a getenv, which finds
// Fresh only in the getenv's XDG_BIN_HOME (the process PATH has no fresh),
// and consoleReview then hands the review tab its command - Fresh on
// report.md when the crew wrote one, on the crew's folder when not. With no
// Fresh found, newConsoleColumns plans no review tab, and `e` says how to
// install it.
//
// /opt/homebrew/bin is one of findTool's fixed directories and is read
// from the real disk, so a fake getenv cannot hide a Fresh installed
// there: the not-installed case starts from the columns newConsoleColumns
// returns without Fresh (review == "") rather than from the lookup.
func TestReviewFreshArgvGolden(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	spawnFakeCrew(t, w, deps, "shop", "k3")
	target := console.StageTarget{Kind: console.StageCrew, ID: "k3", ProjectID: "shop"}

	xdg := t.TempDir()
	if err := os.WriteFile(filepath.Join(xdg, "fresh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	env := map[string]string{"PATH": "/usr/bin:/bin", "HOME": t.TempDir(), "XDG_BIN_HOME": xdg}
	getenv := func(k string) string { return env[k] }

	rec := newRecordingColumns(t)
	planned, err := newConsoleColumns(rec.h, getenv)
	if err != nil {
		t.Fatalf("newConsoleColumns: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(planned.dir) })
	rec.editor, rec.env = planned.editor, planned.env

	normalize := placeholders(map[string]string{w.Root(): "{{WORKSPACE}}", xdg: "{{XDG_BIN_HOME}}"})
	var got strings.Builder
	fmt.Fprintf(&got, "== fresh in XDG_BIN_HOME, not on PATH\neditor: %s\nreview tab: %v\n\n", normalize(planned.editor), planned.review != "")

	show := func(name string) {
		t.Helper()
		if err := consoleReview(w, rec.consoleColumns)(context.Background(), target); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		shown := rec.of(roleReview)
		cmd := shown[len(shown)-1]
		fmt.Fprintf(&got, "== %s\nargv: %s\ndir:  %s\nenv:  %q\n\n", name, normalize(fmt.Sprintf("%q", cmd.Argv)), normalize(cmd.Dir), cmd.Env)
	}
	show("no report.md")
	if err := os.WriteFile(w.CrewReport("shop", "k3"), []byte("# report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	show("report.md")

	rec.review = ""
	err = consoleReview(w, rec.consoleColumns)(context.Background(), target)
	fmt.Fprintf(&got, "== fresh not installed\nerror: %v\n", err)

	checkToolGolden(t, "review-fresh-argv.golden", got.String())
}

// TestRecallBeadsGolden pins the Beads part of `mate recall`
// (recallTaskPlan): two active issues and one ready one, and an
// unreadable tracker. recallTaskPlan opens its tracker with the default
// runner, which execs `bd`, so the fake is a bd on PATH, as in
// TestBeadsCLISeparatesMateAndUpstreamFlags. It answers only the exact
// argv Tracker.Work sends today; any other argv fails, and the golden
// shows it.
func TestRecallBeadsGolden(t *testing.T) {
	const listArgs = "list --status in_progress,blocked --limit 10 --sort priority --brief --json --readonly"
	const readyArgs = "ready --limit 10 --exclude-type epic --brief --json --readonly"
	active := `[{"id":"shop-a1","title":"Wire the checkout button to the payment form","status":"in_progress","issue_type":"task","priority":1},` +
		`{"id":"shop-b2","title":"` + strings.Repeat("Blocked on the payment provider's sandbox keys, ", 4) + `","status":"blocked","issue_type":"bug","priority":0}]`
	ready := `[{"id":"shop-c3","title":"Add a Buy button to README.md","status":"open","issue_type":"feature","priority":2}]`

	var got strings.Builder
	for _, tc := range []struct{ name, script string }{
		{"two active, one ready", "#!/bin/sh\ndir=$(dirname \"$0\")\ncase \"$*\" in\n" +
			"\"" + listArgs + "\") cat \"$dir/active.json\" ;;\n" +
			"\"" + readyArgs + "\") cat \"$dir/ready.json\" ;;\n" +
			"*) echo \"unexpected bd $*\" >&2; exit 17 ;;\nesac\n"},
		{"bd fails", "#!/bin/sh\necho 'database is locked' >&2\nexit 3\n"},
	} {
		w, _ := consoleFixture(t, "shop")
		beadsDir, err := w.BeadsDir("shop")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(beadsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		bin := t.TempDir()
		for name, data := range map[string]string{"bd": tc.script, "active.json": active, "ready.json": ready} {
			if err := os.WriteFile(filepath.Join(bin, name), []byte(data), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

		var out strings.Builder
		recallTaskPlan(w, "shop", &out)
		fmt.Fprintf(&got, "== %s\n%s\n", tc.name, out.String())
	}
	checkToolGolden(t, "recall-beads.golden", got.String())
}

// placeholders replaces each directory, in its given and symlink-resolved
// spelling, with its name. Longest first, so a nested directory keeps its
// own name.
func placeholders(places map[string]string) func(string) string {
	type pair struct{ old, new string }
	var pairs []pair
	for path, name := range places {
		pairs = append(pairs, pair{path, name})
		if r, err := filepath.EvalSymlinks(path); err == nil && r != path {
			pairs = append(pairs, pair{r, name})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].old) > len(pairs[j].old) })
	var args []string
	for _, p := range pairs {
		args = append(args, p.old, p.new)
	}
	return strings.NewReplacer(args...).Replace
}

func checkToolGolden(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if os.Getenv("MATE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (rerun with MATE_UPDATE_GOLDEN=1 to record it)", err)
	}
	if got != string(want) {
		t.Errorf("%s changed byte for byte.\ngot:\n%s\nwant:\n%s", golden, got, want)
	}
}
