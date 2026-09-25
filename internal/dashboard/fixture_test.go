package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// The fixture is the acceptance run task 25 captured: a Mate running Claude
// Code 2.1.278 and a crew running codex-cli 0.154 in project `shop`. The
// transcripts are internal/timeline's own testdata, reindexed here through
// timeline.Reindex, so every number these tests compare is a number the
// ingest produced from a real harness file rather than one a test invented.
const (
	claudeFixture = "../timeline/testdata/claude-2.1.278-transcript.jsonl"
	codexFixture  = "../timeline/testdata/codex-0.154-rollout.jsonl"

	fixtureProject = "shop"
	fixtureCrew    = "buybtn"
	fixtureBranch  = "mate/buybtn"
)

var (
	fixtureCrewStart = mustTime("2026-09-19T10:44:09Z")
	fixtureRequestAt = mustTime("2026-09-19T10:43:50Z")
	fixtureAssignAt  = mustTime("2026-09-19T10:44:40Z")
	fixtureAnswerAt  = mustTime("2026-09-19T10:46:00Z")
	fixtureReportAt  = mustTime("2026-09-19T10:46:40Z")
	fixtureNow       = mustTime("2026-09-19T10:47:00Z")
)

func mustTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		panic(err)
	}
	return t
}

type fixture struct {
	ws     *store.Workspace
	read   *db.DB
	server *Server
	http   *httptest.Server
}

// newFixture builds the workspace, reindexes it, and puts a dashboard on
// top of a read-only handle - the same handle `mate dashboard` opens
// while a console holds the writer.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	ws, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(root, fixtureProject)
	initRepo(t, repo)
	if err := ws.AddProject(fixtureProject, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if ws, err = store.Open(root); err != nil {
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
	for _, line := range []string{
		"working: verifying isolated worktree and task brief",
		"needs-decision: what is the checkout page URL for the Buy button?",
		"wait-mate: ready in branch mate/buybtn",
	} {
		if err := ws.AppendStatus(fixtureProject, fixtureCrew, line); err != nil {
			t.Fatalf("AppendStatus: %v", err)
		}
	}
	for _, entry := range []store.SentEntry{
		{Time: fixtureRequestAt, Source: store.SourceUser, Target: store.TargetMate,
			Text: "Add a Buy button to the end of README.md in project shop, linking to our checkout page. Use a crew."},
		{Time: fixtureAssignAt, Source: store.SourceApp, Target: store.TargetMate,
			Text: `resolve: buybtn asked: "what is the checkout page URL for the Buy button?"`},
		{Time: fixtureAnswerAt, Source: store.SourceMate, Target: store.CrewTarget(fixtureCrew),
			Text: "Use pages/checkout-express.html for the Buy button."},
		{Time: fixtureReportAt, Source: store.SourceMate, Target: store.SourceUser,
			Text: "buybtn is ready in branch mate/buybtn; say the word and I will land it."},
	} {
		if err := ws.AppendSent(fixtureProject, entry); err != nil {
			t.Fatalf("AppendSent: %v", err)
		}
	}

	writer, err := db.Open(ws)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	ing := timeline.New(ws, writer, timeline.Deps{
		Now:               func() time.Time { return fixtureNow },
		ClaudeProjectsDir: filepath.Join(root, "no-claude-projects"),
		CodexSessionsDir:  filepath.Join(root, "no-codex-sessions"),
	})
	if err := ing.Reindex(context.Background()); err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close the writer: %v", err)
	}

	read, err := db.OpenRead(ws)
	if err != nil {
		t.Fatalf("db.OpenRead: %v", err)
	}
	t.Cleanup(func() { _ = read.Close() })

	server, err := New(Options{
		Workspace: ws, DB: read,
		Deps: Deps{
			Now: func() time.Time { return fixtureNow },
			Diff: func(context.Context, string, string) (string, error) {
				return "0d2d20d docs: add Buy link\n", nil
			},
			BranchExists: func(_ context.Context, _, _, branch string) (bool, error) {
				return branch == fixtureBranch, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("dashboard.New: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)

	return &fixture{ws: ws, read: read, server: server, http: ts}
}

// get fetches one endpoint and decodes it, failing the test on any status
// other than the one asked for.
func (f *fixture) get(t *testing.T, path string, status int, into any) {
	t.Helper()
	resp, err := f.http.Client().Get(f.http.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s = %d, want %d\n%s", path, resp.StatusCode, status, body)
	}
	if into == nil {
		return
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

// appendEvent writes one more row to `event` through a writer handle, which
// is how these tests move `MAX(event.id)` without inventing a whole poll.
func (f *fixture) appendEvent(t *testing.T) int64 {
	t.Helper()
	writer, err := db.Open(f.ws)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer writer.Close()
	res, err := writer.SQL().Exec(
		`INSERT INTO event(dedup, project, at, actor_id, kind, payload)
		 VALUES (?, ?, ?, ?, 'status.appended', '{"verb":"working","text":"still here"}')`,
		"test-"+time.Now().Format(time.RFC3339Nano), fixtureProject,
		db.FormatTime(fixtureNow.Add(time.Minute)), timeline.CrewActorID(fixtureProject, fixtureCrew))
	if err != nil {
		t.Fatalf("append an event: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return id
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
