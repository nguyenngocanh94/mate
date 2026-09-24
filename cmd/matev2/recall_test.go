package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/db"
	"github.com/nguyenngocanh94/matev2/internal/gitx"
	"github.com/nguyenngocanh94/matev2/internal/memory"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// recallFixture is backlogFixtureWorkspace (six crews, one per state, k5
// closed, k6 blocked by an open stale incident) plus every file recall
// frames: a PROJECT.md with one anchored and one stale-anchored repo-state
// line, a backlog.md whose In flight names a crew that is not open and
// leaves out one that is, a memory.md with a stale lesson, WORKSPACE.md,
// and the project's CREW.md with a rule. The repository has two commits, so
// the anchor at the first one is behind main.
func recallFixture(t *testing.T) (*store.Workspace, time.Time, string) {
	t.Helper()
	w, now := backlogFixtureWorkspace(t)
	repo := filepath.Join(w.Root(), "shop")
	first := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "--short", "HEAD"))
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, repo, "add", "README.md")
	runGitOrFatal(t, repo, "commit", "-m", "readme")
	if err := os.MkdirAll(w.MateDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, w.ProjectDoc("shop"), "# shop\n\n## What this project is\n- A landing page for the ESP32 kit. (captain, 2026-09-18)\n\n## Layout and state\n- main has one README and no build. (crews/k1/report.md §Durable facts, main@"+first+", 2026-09-19)\n\n## How to work here\n\n## Research on file\n\n## Captain's decisions\n")
	writeFixture(t, w.BacklogFile("shop"), "# Backlog\n\n## In flight\n- k2 (ship, matev2/k2): fix the flaky test\n- k3: pick a colour\n- k4 ship the button\n- k6 (scout): crash\n- buyesp32 (ship): stopped at needs-decision\n\n## Held for the captain\n- k3, asked 2026-09-19: \"red or blue?\" Waiting on: the captain.\n\n## Queued\n- none\n\n## Done (10 most recent)\n- k5 (ship, 2026-09-18): merged\n- rpi35 (scout, 2026-09-17): crews/rpi35/report.md\n")
	writeFixture(t, w.MemoryFile("shop"), "# Memory\n"+memory.HeaderPointer+"\n\n## Captain\n- Speaks Vietnamese. (captain, 2026-09-17)\n\n## Lessons\n- Scouts commit their reports unless told not to. (sent.log to rpi35 2026-09-18) <!--a:2026-08-01-->\n- The canary lesson is HERON-7. (captain, 2026-09-19) <!--a:2026-09-19-->\n")
	writeFixture(t, w.WorkspaceDoc(), "# Workspace\n- Never merge on a Friday.\n")
	writeFixture(t, w.ProjectCrewDoc("shop"), "<!-- rules for every crew of shop -->\n- Run the tests before wait-mate.\n")
	return w, now, first
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func recallText(t *testing.T, w *store.Workspace, now time.Time, opts recallOptions) string {
	t.Helper()
	if opts.Binary == "" {
		opts.Binary = "/usr/local/bin/matev2"
	}
	out, err := recall(context.Background(), w, gitx.New(), "shop", now, opts)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	return out
}

// partHeadings returns the `== N. title ==` lines in the order printed.
func partHeadings(out string) []string {
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "== ") && strings.HasSuffix(l, " ==") {
			got = append(got, l)
		}
	}
	return got
}

// mustOrder fails unless every needle is in out, each after the one before.
func mustOrder(t *testing.T, out string, needles ...string) {
	t.Helper()
	at := 0
	for _, n := range needles {
		i := strings.Index(out[at:], n)
		if i < 0 {
			t.Fatalf("%q missing, or out of order after byte %d, in:\n%s", n, at, out)
		}
		at += i + len(n)
	}
}

// TestRecallPrintsSection12Point5InOrder is B1 on a workspace with every
// file present: seven parts, live state first and memory last, each file
// framed, and the three findings recall adds of its own - the stale anchor,
// the In flight mismatch both ways, the stale lesson.
func TestRecallPrintsSection12Point5InOrder(t *testing.T) {
	w, now, first := recallFixture(t)
	out := recallText(t, w, now, recallOptions{})
	t.Logf("\n%s", out)

	want := []string{
		"== 1. Live state ==",
		"== 2. Project facts (matev2 project facts shop) ==",
		"== 3. PROJECT.md ==",
		"== 4. backlog.md, without Done ==",
		"== 5. memory.md ==",
		"== 6. WORKSPACE.md ==",
		"== 7. Memory check (matev2 memory check shop) ==",
	}
	if got := partHeadings(out); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("parts = %q, want %q (no part 8 without matev2.db)", got, want)
	}
	if !strings.HasPrefix(out, "# matev2 recall shop · ") || !strings.Contains(strings.SplitN(out, "\n", 3)[1], "Contract: read this once; it is your state") {
		t.Fatalf("the digest does not open with its title and contract line:\n%s", out)
	}
	mustOrder(t, out,
		// 1: mode, the backlog table, inbox, outbox.
		"mode: manual · yolo off", "crews (matev2 backlog shop):", "ID  STATE", "k1  spawned", "5 open, 1 closed",
		"inbox: 2 unresolved", `stuck, quiet too long: "no status for 15m"`, `k3 needs an answer: "choose red or blue for the button" (crews/k3.status)`,
		"outbox: nothing queued",
		// 2: facts with head.
		"commits: 2 on main", "head: ",
		// 3: PROJECT.md framed, then the stale anchor.
		"----- begin PROJECT.md (", "- A landing page for the ESP32 kit.", "----- end PROJECT.md -----",
		"warning: PROJECT.md line 7 is anchored at main@"+first+", and main has 1 newer commit(s)",
		// 4: backlog without Done, then the mismatches.
		"----- begin backlog.md (", "## Held for the captain", `- k3, asked 2026-09-19: "red or blue?"`,
		"----- end backlog.md (## Done: 2 entries not shown) -----",
		"In flight in backlog.md but not an open crew in part 1: buyesp32",
		"Open crews in part 1 with no `- <id>` line under In flight: k1",
		// 5, 6.
		"----- begin memory.md (", "The canary lesson is HERON-7.", "----- end memory.md -----",
		"----- begin WORKSPACE.md (", "Never merge on a Friday.", "----- end WORKSPACE.md -----",
		"(no rules yet)", "(has rules)",
		// 7.
		"budget: ", "memory.md:8: aging entry last reinforced 2026-08-01", "1 problem(s): load skill stow",
	)
	for _, gone := range []string{"rpi35 (scout, 2026-09-17)", "k5 (ship, 2026-09-18): merged", "## Done"} {
		if strings.Contains(strings.Split(out, "----- end backlog.md")[0], gone) {
			t.Errorf("part 4 printed %q from ## Done", gone)
		}
	}
	if strings.Contains(out, ": ABSENT (") || strings.Contains(out, ": (empty) (") {
		t.Errorf("every file is present and non-empty, yet the digest says ABSENT or (empty)")
	}
}

// TestRecallTellsAbsentFromEmpty: a missing file and an empty one mean
// different things and never print alike; a seeded file that holds only its
// headings says so, which is what the onboarding-scout rule reads.
func TestRecallTellsAbsentFromEmpty(t *testing.T) {
	w, now, _ := recallFixture(t)
	if err := os.Remove(w.WorkspaceDoc()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(w.BacklogFile("shop")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, w.MemoryFile("shop"), "")
	writeFixture(t, w.ProjectDoc("shop"), memory.ProjectTemplate("shop"))
	if err := os.Remove(w.ProjectCrewDoc("shop")); err != nil {
		t.Fatal(err)
	}
	out := recallText(t, w, now, recallOptions{})
	t.Logf("\n%s", out)
	mustOrder(t, out,
		"----- end PROJECT.md -----", "note: PROJECT.md holds its headings and nothing else yet",
		"----- backlog.md: ABSENT (", w.BacklogFile("shop"),
		"----- memory.md: (empty) (", w.MemoryFile("shop"),
		"----- WORKSPACE.md: ABSENT (",
		w.ProjectCrewDoc("shop")+" (ABSENT)",
	)
	if strings.Contains(out, "In flight in backlog.md") {
		t.Error("an absent backlog.md cannot disagree with the table")
	}
}

// TestRecallLiveIsPartOneOnly is the resume digest: the crews may have
// moved while the Mate was stopped, the files are still in its context.
func TestRecallLiveIsPartOneOnly(t *testing.T) {
	w, now, _ := recallFixture(t)
	out := recallText(t, w, now, recallOptions{LiveOnly: true})
	if got := partHeadings(out); len(got) != 1 || got[0] != "== 1. Live state ==" {
		t.Fatalf("--live printed parts %q", got)
	}
	mustOrder(t, out, "Contract: live state only.", "k3  needs-decision", "inbox: 2 unresolved", "outbox: nothing queued")
	for _, gone := range []string{"PROJECT.md", "memory.md", "budget:"} {
		if strings.Contains(out, gone) {
			t.Errorf("--live printed %q", gone)
		}
	}
}

// TestRecallMaxBytesCutsFromTheBottom: the point of the order is that a cut
// digest keeps the state and loses the memory, and says what it lost.
func TestRecallMaxBytesCutsFromTheBottom(t *testing.T) {
	w, now, _ := recallFixture(t)
	full := recallText(t, w, now, recallOptions{})
	live := recallText(t, w, now, recallOptions{LiveOnly: true})
	for _, max := range []int{len(full) / 2, len(full) - 200, 10} {
		out := recallText(t, w, now, recallOptions{MaxBytes: max})
		livePart := strings.SplitN(live, "\n== 1. Live state ==\n", 2)[1]
		if !strings.Contains(out, livePart) {
			t.Fatalf("--max-bytes %d lost part of part 1:\n%s", max, out)
		}
		if max > len(live)+500 && len(out) > max {
			t.Fatalf("--max-bytes %d printed %d bytes", max, len(out))
		}
		last := strings.TrimSpace(out[strings.LastIndex(strings.TrimRight(out, "\n"), "\n")+1:])
		if !strings.HasPrefix(last, fmt.Sprintf("recall cut to fit %d bytes; not shown: ", max)) ||
			!strings.Contains(last, "part 7 (Memory check") || !strings.HasSuffix(last, "Run `/usr/local/bin/matev2 recall shop` once before acting on anything those parts cover.") {
			t.Fatalf("--max-bytes %d: last line = %q", max, last)
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "== ") && strings.HasSuffix(l, " ==") {
				idx := strings.Index(out, l)
				rest := strings.TrimLeft(out[idx+len(l):], "\n")
				if strings.HasPrefix(rest, "recall cut") {
					t.Fatalf("--max-bytes %d left the heading %q with no body", max, l)
				}
			}
		}
	}
	if got := recallText(t, w, now, recallOptions{MaxBytes: len(full) + 1000}); got != full {
		t.Fatal("a limit the digest fits under changed it")
	}
}

// TestRecallTimelineHints is part 8: only with matev2.db, and only hints -
// the lines the Mate sent crews after their spawn, the questions crews
// asked, and the Mate's context.
func TestRecallTimelineHints(t *testing.T) {
	w, now, _ := recallFixture(t)
	d, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	at := func(ago time.Duration) string { return db.FormatTime(now.Add(-ago)) }
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO actor(id, project, kind, name) VALUES ('shop/mate','shop','mate','mate'), ('shop/k2','shop','crew','k2'), ('shop/k9','shop','crew','k9')`, nil},
		{`INSERT INTO task(crew_actor_id, project, spawned_at) VALUES ('shop/k2','shop',?), ('shop/k9','shop',?)`, []any{at(3 * time.Hour), at(20 * 24 * time.Hour)}},
		{`INSERT INTO event(id, dedup, project, at, actor_id, kind) VALUES
			(1,'a','shop',?,'shop/mate','message.sent'),
			(2,'b','shop',?,'shop/mate','message.sent'),
			(3,'c','shop',?,'shop/mate','message.sent'),
			(4,'d','shop',?,'shop/k2','question.asked')`, []any{at(4 * time.Hour), at(2 * time.Hour), at(16 * 24 * time.Hour), at(90 * time.Minute)}},
		{`INSERT INTO message(event_id, from_actor_id, to_actor_id, channel, text) VALUES
			(1,'shop/mate','shop/k2','pane','the brief, before spawn'),
			(2,'shop/mate','shop/k2','pane','use the staging bucket, not prod'),
			(3,'shop/mate','shop/k9','pane','too old to matter')`, nil},
		{`INSERT INTO question(id, asked_event_id, crew_actor_id, text, asked_at) VALUES ('q1',4,'shop/k2','which bucket?',?)`, []any{at(90 * time.Minute)}},
		{`INSERT INTO pricing(model, context_window) VALUES ('m1', 200000)`, nil},
		{`INSERT INTO session(id, actor_id) VALUES ('s1','shop/mate')`, nil},
		{`INSERT INTO turn(id, actor_id, session_id, started_at, model, context_tokens_after) VALUES ('t1','shop/mate','s1',?,'m1',106000)`, []any{at(time.Hour)}},
	}
	for _, s := range stmts {
		if _, err := d.SQL().Exec(s.q, s.args...); err != nil {
			t.Fatalf("%s: %v", s.q, err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	out := recallText(t, w, now, recallOptions{})
	t.Logf("\n%s", out)
	heads := partHeadings(out)
	if heads[len(heads)-1] != "== 8. Timeline hints (matev2.db: hints only, never memory) ==" {
		t.Fatalf("parts = %q, want part 8 last", heads)
	}
	part8 := out[strings.Index(out, "== 8."):]
	mustOrder(t, part8,
		"Mate context: 53% of the window after your latest turn",
		"lines you sent crews after their spawn, last 14 days: 1", `to k2: "use the staging bucket, not prod"`,
		"questions crews asked, last 14 days: 1", `k2 (unanswered): "which bucket?"`,
		"Nothing above is memory",
	)
	for _, gone := range []string{"the brief, before spawn", "too old to matter"} {
		if strings.Contains(part8, gone) {
			t.Errorf("part 8 printed %q", gone)
		}
	}
}

func TestRecallCLI(t *testing.T) {
	w, _, _ := recallFixture(t)
	var out, errw strings.Builder
	if code := mainRun([]string{"recall", "shop", "--live", "--workspace", w.Root()}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "== 1. Live state ==") || strings.Contains(out.String(), "== 2.") {
		t.Fatalf("recall --live printed:\n%s", out.String())
	}
	for _, args := range [][]string{{"recall"}, {"recall", "shop", "extra"}, {"recall", "shop", "--max-bytes", "-1"}} {
		if code := mainRun(append(args, "--workspace", w.Root()), &out, &errw); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code := mainRun([]string{"recall", "nosuch", "--workspace", w.Root()}, &out, &errw); code != 1 {
		t.Errorf("an unknown project: exit %d, want 1", code)
	}
}
