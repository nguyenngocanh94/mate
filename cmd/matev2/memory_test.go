package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/gitx"
	"github.com/nguyenngocanh94/matev2/internal/memory"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

var memoryDay = time.Date(2026, 9, 24, 10, 0, 0, 0, time.Local)

// TestRememberThreeCallsBuildTheFile is the report's example: a captain
// preference, an aging lesson and a perishable lesson, written into the
// 9-byte memory.md every Mate had before task 36.
func TestRememberThreeCallsBuildTheFile(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	if err := os.MkdirAll(w.MateDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.MemoryFile("shop"), []byte("# Memory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := []struct{ section, text, source, expiry string }{
		{memory.LessonsSection, "Scout Crews commit their report files unless the brief says to keep every file under crews/<id>/ and commit nothing; proposed to the captain for CREW.md.", "sent.log to rpi35 2026-09-18, power12 2026-09-19", ""},
		{memory.CaptainSection, "Speaks Vietnamese; answer and escalate in Vietnamese.", "captain, 2026-09-17", ""},
		{memory.LessonsSection, "Crew spawn fails with no pane or meta; stop after two tries and tell the captain.", "backlog Failed rpi34", "the spawn fix lands"},
	}
	for _, c := range calls {
		if _, err := remember(w, "shop", c.section, c.text, c.source, c.expiry, memoryDay); err != nil {
			t.Fatalf("remember %q: %v", c.text, err)
		}
	}
	got, err := os.ReadFile(w.MemoryFile("shop"))
	if err != nil {
		t.Fatal(err)
	}
	want := `# Memory

## Captain
- Speaks Vietnamese; answer and escalate in Vietnamese. (captain, 2026-09-17)

## Lessons
- Scout Crews commit their report files unless the brief says to keep every file under crews/<id>/ and commit nothing; proposed to the captain for CREW.md. (sent.log to rpi35 2026-09-18, power12 2026-09-19) <!--a:2026-09-24-->
- Crew spawn fails with no pane or meta; stop after two tries and tell the captain. (expires: the spawn fix lands) (backlog Failed rpi34) <!--p:2026-09-24-->
`
	if string(got) != want {
		t.Fatalf("memory.md:\n%s\nwant:\n%s", got, want)
	}
	rep, err := memoryCheck(context.Background(), w, gitx.New(), "shop", memoryDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Problems) != 0 || rep.Captain != 1 || rep.Lessons != 2 {
		t.Fatalf("memory check on remember's own file: %+v", rep)
	}
}

func rememberCLI(t *testing.T, w *store.Workspace, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := mainRun(append([]string{"remember", "--workspace", w.Root()}, args...), &out, &errw)
	return code, out.String(), errw.String()
}

func TestRememberCLIWritesAndPrintsTheEntry(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	code, out, errw := rememberCLI(t, w, "shop", "--lesson", "--source", "crews/k1/report.md §Durable facts", "Hugo builds into public/.")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	today := time.Now().Format(memory.DateLayout)
	line := "- Hugo builds into public/. (crews/k1/report.md §Durable facts) <!--a:" + today + "-->"
	if want := "remembered in " + w.MemoryFile("shop") + " under ## Lessons: " + line + "\n"; out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}
	data, _ := os.ReadFile(w.MemoryFile("shop"))
	if !strings.Contains(string(data), "\n## Lessons\n"+line+"\n") {
		t.Fatalf("memory.md:\n%s", data)
	}
}

func TestRememberCLIRefusals(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"neither kind", []string{"shop", "--source", "captain", "x"}, 2, "exactly one of --captain and --lesson"},
		{"both kinds", []string{"shop", "--captain", "--lesson", "--source", "captain", "x"}, 2, "exactly one of --captain and --lesson"},
		{"no source", []string{"shop", "--lesson", "x"}, 2, "--source is empty"},
		{"no text", []string{"shop", "--lesson", "--source", "captain"}, 2, "want exactly 2 arguments"},
		{"perishable captain", []string{"shop", "--captain", "--perishable", "soon", "--source", "captain", "x"}, 2, "--perishable goes with --lesson"},
		{"absolute source", []string{"shop", "--lesson", "--source", filepath.Join(w.ProjectDir("shop"), "crews", "k1", "report.md"), "x"}, 2, "write it relative to the project directory: crews/k1/report.md"},
		{"temp source", []string{"shop", "--lesson", "--source", "/private/tmp/scratch/report.md", "x"}, 2, "absolute path outside the workspace"},
		{"outside source", []string{"shop", "--lesson", "--source", "../../../../../x.md", "x"}, 2, "climbs out of the workspace"},
		{"two lines", []string{"shop", "--lesson", "--source", "captain", "a\nb"}, 2, "the entry must be one line"},
		{"source parens", []string{"shop", "--lesson", "--source", "report (s2)", "x"}, 2, "may not contain parentheses"},
		{"unknown project", []string{"blog", "--lesson", "--source", "captain", "x"}, 1, "blog"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _, errw := rememberCLI(t, w, c.args...)
			if code != c.code || !strings.Contains(errw, c.want) {
				t.Fatalf("exit %d, stderr %q; want exit %d and %q", code, errw, c.code, c.want)
			}
			if _, err := os.Stat(w.MemoryFile("shop")); !os.IsNotExist(err) {
				t.Fatalf("a refused remember wrote memory.md (%v)", err)
			}
		})
	}
}

func TestRememberSaysWhenOverBudget(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	if err := os.WriteFile(w.WorkspaceDoc(), bytes.Repeat([]byte("x"), 3*memory.BudgetTokens), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errw := rememberCLI(t, w, "shop", "--captain", "--source", "captain, 2026-09-24", "Short replies.")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "budget: ") || !strings.Contains(lines[1], "over by") || !strings.Contains(lines[1], "skill stow") {
		t.Fatalf("stdout = %q, want the entry and an over-budget line", out)
	}
}

func memoryCheckCLI(t *testing.T, w *store.Workspace) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := mainRun([]string{"memory", "check", "shop", "--workspace", w.Root()}, &out, &errw)
	return code, out.String(), errw.String()
}

func writeMemoryFiles(t *testing.T, w *store.Workspace, mem, project string) {
	t.Helper()
	if err := os.MkdirAll(w.MateDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.MemoryFile("shop"), []byte(mem), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ProjectDoc("shop"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryCheckCLIPassesAndPrintsTheBudget(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	head := strings.TrimSpace(gitOut(t, w.RepoDir("shop"), "rev-parse", "--short", "main"))
	today := time.Now().Format(memory.DateLayout)
	mem := "# Memory\n" + memory.HeaderPointer + "\n\n## Captain\n- p (captain, 2026-09-17)\n\n## Lessons\n- l (captain) <!--a:" + today + "-->\n"
	doc := "# shop\n\n## Layout and state\n- One commit. (matev2 project facts, main@" + head + ", " + today + ")\n"
	writeMemoryFiles(t, w, mem, doc)
	if err := os.WriteFile(w.WorkspaceDoc(), []byte("rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errw := memoryCheckCLI(t, w)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errw)
	}
	wantBudget := "budget: " + itoa(memory.EstimateTokens(len(mem))+memory.EstimateTokens(len(doc))+memory.EstimateTokens(len("rules\n"))) + " of 4000 estimated tokens (memory.md " +
		itoa(memory.EstimateTokens(len(mem))) + ", PROJECT.md " + itoa(memory.EstimateTokens(len(doc))) + ", WORKSPACE.md 2)\n"
	if want := wantBudget + "memory ok: 1 captain entry, 1 lesson(s), 1 anchored PROJECT.md line(s)\n"; out != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", out, want)
	}
	if errw != "" {
		t.Fatalf("stderr = %q, want nothing (the anchor is main's head)", errw)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// TestMemoryCheckCLIReportsEveryRule: one file breaking every rule, and the
// CLI prints one line per problem with file and line, and exits 1.
func TestMemoryCheckCLIReportsEveryRule(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	now := time.Now()
	old := now.AddDate(0, 0, -30).Format(memory.DateLayout)
	week := now.AddDate(0, 0, -7).Format(memory.DateLayout)
	mem := strings.Join([]string{
		"# Memory",                             // 1
		"## Captain",                           // 2
		"- no source here",                     // 3
		"## Lessons",                           // 4
		"- no marker (captain)",                // 5
		"- old (captain) <!--a:" + old + "-->", // 6
		"- week (expires: r2) (captain) <!--p:" + week + "-->",                             // 7
		"- temp (/private/tmp/x/report.md) <!--a:" + now.Format(memory.DateLayout) + "-->", // 8
		"",
	}, "\n")
	doc := "# shop\n\n## Layout and state\n- An app. (crews/k1/report.md)\n"
	writeMemoryFiles(t, w, mem, doc)
	if err := os.WriteFile(w.WorkspaceDoc(), bytes.Repeat([]byte("x"), 3*memory.BudgetTokens), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errw := memoryCheckCLI(t, w)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s%s", code, out, errw)
	}
	if !strings.HasPrefix(out, "budget: ") || strings.Contains(out, "memory ok") {
		t.Fatalf("stdout = %q", out)
	}
	lines := strings.Split(strings.TrimRight(errw, "\n"), "\n")
	wantPrefixes := []string{
		"memory.md:3: entry has no source",
		"memory.md:5: lesson has no tier marker",
		"memory.md:6: aging entry last reinforced " + old + ", 30 days ago",
		"memory.md:7: perishable entry last reinforced " + week + ", 7 days ago",
		`memory.md:8: source "/private/tmp/x/report.md" is an absolute path outside the workspace`,
		"PROJECT.md:4: `## Layout and state` line has no main@<sha> anchor",
		"budget: ",
		"matev2: memory check: 7 problem(s) in shop's memory",
	}
	if len(lines) != len(wantPrefixes) {
		t.Fatalf("stderr:\n%s", errw)
	}
	for i, p := range wantPrefixes {
		if !strings.HasPrefix(lines[i], p) {
			t.Errorf("stderr line %d = %q, want prefix %q", i+1, lines[i], p)
		}
	}
	if !strings.Contains(lines[6], "over by") {
		t.Errorf("budget problem = %q", lines[6])
	}
}

// TestMemoryCheckWarnsOnAnchorsOlderThanHead: an old anchor is a warning,
// never a problem; the head's own anchor is silent.
func TestMemoryCheckWarnsOnAnchorsOlderThanHead(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	repo := w.RepoDir("shop")
	first := strings.TrimSpace(gitOut(t, repo, "rev-parse", "--short", "main"))
	runGitOrFatal(t, repo, "commit", "--allow-empty", "-m", "two")
	runGitOrFatal(t, repo, "commit", "--allow-empty", "-m", "three")
	head := strings.TrimSpace(gitOut(t, repo, "rev-parse", "--short", "main"))
	doc := strings.Join([]string{
		"# shop",
		"## Layout and state",
		"- A. (crews/k1/report.md, main@" + first + ")",
		"- B. (crews/k1/report.md, main@" + head + ")",
		"## How to work here",
		"- C. (crews/k1/report.md, main@" + first + ")",
		"- D. (project facts, main@none)",
		"- E. (crews/k1/report.md, main@abcdef0)",
		"",
	}, "\n")
	writeMemoryFiles(t, w, memory.Header, doc)
	code, out, errw := memoryCheckCLI(t, w)
	if code != 0 {
		t.Fatalf("an old anchor failed the check: exit %d\n%s%s", code, out, errw)
	}
	lines := strings.Split(strings.TrimRight(errw, "\n"), "\n")
	want := []string{
		"warning: PROJECT.md lines 3, 6 are anchored at main@" + first + ", and main has 2 newer commit(s); treat them as a hint until a report or hand-back confirms it",
		"warning: PROJECT.md line 8 is anchored at main@abcdef0, and main's history does not contain abcdef0; treat it as a hint until a report or hand-back confirms it",
		"warning: PROJECT.md line 7 is anchored at main@none, and main has 3 commit(s) since; treat it as a hint until a report or hand-back confirms it",
	}
	got := map[string]bool{}
	for _, l := range lines {
		got[l] = true
	}
	for _, l := range want {
		if !got[l] {
			t.Errorf("missing warning %q\nstderr:\n%s", l, errw)
		}
	}
	if len(lines) != len(want) {
		t.Errorf("stderr has %d lines, want %d:\n%s", len(lines), len(want), errw)
	}
	if !strings.Contains(out, "memory ok: 0 captain entries, 0 lesson(s), 5 anchored PROJECT.md line(s)") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestMemoryCheckAbsentFilesAreAbsent(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	_ = os.Remove(w.ProjectDoc("shop"))
	_ = os.Remove(w.WorkspaceDoc())
	code, out, errw := memoryCheckCLI(t, w)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errw)
	}
	if !strings.HasPrefix(out, "budget: 0 of 4000 estimated tokens (memory.md absent, PROJECT.md absent, WORKSPACE.md absent)\n") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestMemoryUsage(t *testing.T) {
	var out, errw bytes.Buffer
	for _, args := range [][]string{{"memory"}, {"memory", "bogus"}, {"memory", "check"}, {"memory", "check", "a", "b"}} {
		if code := mainRun(args, &out, &errw); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

// The two memory nudges (B9), in both caller modes.

func TestCrewStopNudgeOnlyForTheMate(t *testing.T) {
	res := spawn.StopResult{Agent: "crew-k3", TabClosed: true, Branch: "matev2/k3", Teardown: spawn.TeardownClean, State: "finished"}
	outcome := crewStopReport("shop", "k3", res) + "\n"

	var mate bytes.Buffer
	writeCrewStopReport(&mate, "shop", "k3", res, spawn.CallerMate)
	if got, want := mate.String(), outcome+"record: update backlog.md Done; if this task taught you anything durable, route it (skill stow)\n"; got != want {
		t.Fatalf("mate: %q, want %q", got, want)
	}
	for _, caller := range []string{spawn.CallerUser, spawn.CallerCrew} {
		var other bytes.Buffer
		writeCrewStopReport(&other, "shop", "k3", res, caller)
		if other.String() != outcome {
			t.Fatalf("%s: %q, want the outcome alone", caller, other.String())
		}
	}
	closed := spawn.StopResult{AlreadyClosed: true, State: "finished"}
	var again bytes.Buffer
	writeCrewStopReport(&again, "shop", "k3", closed, spawn.CallerMate)
	if strings.Contains(again.String(), "record:") {
		t.Fatalf("a stop that changed nothing nudged: %q", again.String())
	}
}

func TestCrewStopReadsTheCallerFromTheEnvironment(t *testing.T) {
	t.Setenv("MATEV2_CALLER", "mate")
	if spawn.CallerFromEnv() != spawn.CallerMate {
		t.Fatal("MATEV2_CALLER=mate is not the Mate")
	}
	t.Setenv("MATEV2_CALLER", "")
	if spawn.CallerFromEnv() != spawn.CallerUser {
		t.Fatal("no MATEV2_CALLER is not the user")
	}
}

func TestSendCorrectionNudgeOnlyForTheMateAndABriefedCrew(t *testing.T) {
	w, _ := autoTurnFixture(t)
	var mate, user, unbriefed bytes.Buffer
	printSendCorrectionNudge(&mate, w, "shop", "k3", store.SourceMate)
	if mate.String() != "if this correction applies to future crews, route it: memory.md Lessons or propose it for CREW.md\n" {
		t.Fatalf("mate: %q", mate.String())
	}
	printSendCorrectionNudge(&user, w, "shop", "k3", store.SourceUser)
	if user.String() != "" {
		t.Fatalf("user: %q, want nothing", user.String())
	}
	printSendCorrectionNudge(&unbriefed, w, "shop", "k9", store.SourceMate)
	if unbriefed.String() != "" {
		t.Fatalf("crew without a brief: %q, want nothing", unbriefed.String())
	}
}
