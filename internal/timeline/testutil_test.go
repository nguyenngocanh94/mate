package timeline_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// The fixture is one acceptance run, captured live on 2026-09-19 and trimmed:
// a Mate running Claude Code 2.1.278 and a Crew running codex-cli 0.154, in
// the `shop` project of docs/evidence/m4-acceptance-2026-09-19.md. Only the
// bulk was cut (long developer instructions, attachment bodies, tool output);
// every timestamp, ordinal, token count, tool call and command survives, which
// is what the ingest reads.
const (
	claudeFixture = "testdata/claude-2.1.278-transcript.jsonl"
	codexFixture  = "testdata/codex-0.154-rollout.jsonl"

	fixtureCrew    = "buybtn"
	fixtureProject = "shop"
	fixtureBranch  = "mate/buybtn"

	// The three status lines the crew echoed, verbatim. Each appears inside
	// the `exec` command that wrote it, which is how the ingest dates them.
	statusWorking  = "working: verifying isolated worktree and task brief"
	statusQuestion = "needs-decision: what is the checkout page URL for the Buy button?"
	statusHandback = "wait-mate: ready in branch mate/buybtn"

	// What the captain and the Mate said, in the order the real run said it.
	captainRequest = "Add a Buy button to the end of README.md in project shop, linking to our checkout page. Use a crew."
	assignLine     = `resolve: buybtn asked: "what is the checkout page URL for the Buy button?" — read /w/.mate/projects/shop/crews/buybtn.status, decide, and answer with mate send shop buybtn "<one line>"`
	mateAnswer     = "Use pages/checkout-express.html for the Buy button."
	mateReport     = "buybtn is ready in branch mate/buybtn; say the word and I will land it."
)

// The clock of the captured run, so every assertion is against real moments
// rather than against whatever the test machine's clock says today.
var (
	fixtureCrewStart = mustTime("2026-09-19T10:44:09Z")
	fixtureRequestAt = mustTime("2026-09-19T10:43:50Z")
	fixtureAssignAt  = mustTime("2026-09-19T10:44:40Z")
	fixtureAnswerAt  = mustTime("2026-09-19T10:46:00Z")
	fixtureReportAt  = mustTime("2026-09-19T10:46:40Z")
	fixtureNow       = mustTime("2026-09-19T10:47:00Z")
)

func mustTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

type fixture struct {
	ws   *store.Workspace
	db   *db.DB
	ing  *timeline.Ingester
	root string
}

// newFixture builds a workspace holding the captured run: one project, one
// Mate whose `.meta` names the Claude transcript, one crew whose `.meta` names
// the Codex rollout, and the three flat logs with the lines the run produced.
//
// The transcripts are named directly in `.meta`, which is the locator's first
// rule; the other rules have their own tests.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	ws, err := store.Init(root, store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(ws.ProjectHome(fixtureProject), fixtureProject)
	initRepo(t, repo)
	if err := ws.AddProject(fixtureProject, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	ws, err = store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	if err := ws.WriteMateMeta(fixtureProject, map[string]string{
		"harness":    "claude",
		"agent":      "mate-shop",
		"session_id": "8414030c-5d90-4925-94cc-c94e12aae4a9",
		"transcript": abs(t, claudeFixture),
		"started_at": mustTime("2026-09-19T10:43:30Z").Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("WriteMateMeta: %v", err)
	}
	if err := ws.WriteCrewMeta(fixtureProject, fixtureCrew, map[string]string{
		"harness":    "codex",
		"agent":      "crew-buybtn",
		"task":       "Add a Buy button to README.md linking to the checkout page",
		"branch":     fixtureBranch,
		"worktree":   ".worktrees/shop-buybtn",
		"session_id": "01a0b944-33bb-7503-9db7-cd51ff61855a",
		"transcript": abs(t, codexFixture),
		"started_at": fixtureCrewStart.Format(time.RFC3339),
		"state":      "spawned",
	}); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}

	for _, line := range []string{statusWorking, statusQuestion, statusHandback} {
		if err := ws.AppendStatus(fixtureProject, fixtureCrew, line); err != nil {
			t.Fatalf("AppendStatus: %v", err)
		}
	}
	for _, entry := range []store.SentEntry{
		{Time: fixtureRequestAt, Source: store.SourceUser, Target: store.TargetMate, Text: captainRequest},
		{Time: fixtureAssignAt, Source: store.SourceApp, Target: store.TargetMate, Text: assignLine},
		{Time: fixtureAnswerAt, Source: store.SourceMate, Target: store.CrewTarget(fixtureCrew), Text: mateAnswer},
		{Time: fixtureReportAt, Source: store.SourceMate, Target: store.SourceUser, Text: mateReport},
	} {
		if err := ws.AppendSent(fixtureProject, entry); err != nil {
			t.Fatalf("AppendSent: %v", err)
		}
	}

	handle, err := db.Open(ws)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })

	f := &fixture{ws: ws, db: handle, root: root}
	f.ing = timeline.New(ws, handle, timeline.Deps{
		Now:             func() time.Time { return fixtureNow },
		Harnesses:       catalog.Default(),
		TranscriptRoots: transcriptRoots(filepath.Join(root, "no-claude-projects"), filepath.Join(root, "no-codex-sessions")),
	})
	return f
}

func (f *fixture) ingest(t *testing.T) {
	t.Helper()
	if err := f.ing.Ingest(context.Background()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
}

func (f *fixture) story(t *testing.T) []timeline.StoryEvent {
	t.Helper()
	events, err := timeline.Story(context.Background(), f.db.SQL(), timeline.StoryQuery{Project: fixtureProject})
	if err != nil {
		t.Fatalf("Story: %v", err)
	}
	return events
}

// narrate is the whole story as the golden spells it.
func (f *fixture) narrate(t *testing.T) string {
	t.Helper()
	var lines []string
	for _, e := range f.story(t) {
		lines = append(lines, timeline.NarrateIn(e, time.UTC))
	}
	return strings.Join(lines, "\n") + "\n"
}

func (f *fixture) crewActor() string { return timeline.CrewActorID(fixtureProject, fixtureCrew) }
func (f *fixture) mateActor() string { return timeline.MateActorID(fixtureProject) }
func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.SQL().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func abs(t *testing.T, rel string) string {
	t.Helper()
	path, err := filepath.Abs(rel)
	if err != nil {
		t.Fatalf("abs %s: %v", rel, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture %s: %v", path, err)
	}
	return path
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# shop\n"), 0o644); err != nil {
		t.Fatalf("seed README: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "fixture@example.com"},
		{"config", "user.name", "fixture"},
		{"add", "."},
		{"commit", "-m", "seed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// transcriptRoots points the catalog's harnesses at fixture directories:
// Claude's projects root and Codex's sessions root.
func transcriptRoots(claudeProjects, codexSessions string) map[harness.Kind]string {
	return map[harness.Kind]string{claude.KindClaude: claudeProjects, codex.KindCodex: codexSessions}
}
