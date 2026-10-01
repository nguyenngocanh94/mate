package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// TestLiveDashboardMatchesUsage is docs/mvp.md M6 task 28's live proof: run
// the shop half of the acceptance with a real Mate and a real Crew, then
// serve the dashboard over the timeline that run produced and check that
// the page and the terminal say the same thing.
//
// Two comparisons, both against surfaces that existed before this task:
// every token on `/api/projects/shop` appears in `mate usage shop`'s own
// row for the same crew, humanised by the same function; and
// `/api/events?since=0` returns exactly the events `timeline.Story` holds,
// which is what `mate events shop` prints.
//
// A green unit suite is not evidence for this: the fixture database was
// built from captured transcripts, and the thing under test here is a
// dashboard reading a timeline that a live Herdr run wrote while it ran.
func TestLiveDashboardMatchesUsage(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	root := t.TempDir()
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	consoleUseLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# shop\n\nA tiny shop.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, repo, "add", "README.md")
	runGitOrFatal(t, repo, "commit", "-m", "add README")
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	names := runtime.NewMemoryNameRegistry()
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = names
	rt.StartServer = func(context.Context, string) error {
		t.Fatal("the lab session is provisioned by the runner; this test must not start a Herdr server")
		return nil
	}
	deps := spawn.Deps{
		Harnesses:            harnesses,
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               consoleBinaryPath(t),
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 60 * time.Second,
		StartTimeout:         60 * time.Second,
	}
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	started, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Harness: claude.KindClaude})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})
	mate := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    started.Agent,
		RawID:   "shop",
		Kind:    claude.KindClaude,
		Tab: runtime.TabHandle{
			Session:     runtime.SessionHandle{Name: session, ConfigHome: configHome},
			WorkspaceID: started.Workspace,
			TabID:       started.Tab,
			PaneID:      started.Pane,
			Label:       "mate",
		},
	}
	if _, err := send.Send(ctx, send.Deps{Harnesses: harnesses, Runtime: rt}, mate, claude.KindClaude, acceptanceRequest, send.Options{}); err != nil {
		t.Fatalf("send the captain's request: %v", err)
	}

	budget := 4 * time.Minute
	crew := waitForCrewRecord(t, ctx, w, "shop", budget, func() string { return acceptancePanes(ctx, rt, mate, w, "shop") })
	t.Logf("the mate spawned crew %q", crew)
	waitForCrewStatus(t, ctx, w, "shop", crew, "wait-mate:", budget, func() string { return acceptancePanes(ctx, rt, mate, w, "shop") })
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", crew, true)
	})

	// One reindex, then the dashboard reads the same file the CLI does.
	writer, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := timeline.New(w, writer, timeline.Deps{
		SessionRef: consoleSessionRef(w, deps),
		Harnesses:  harnesses,
	}).Reindex(ctx); err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close the writer: %v", err)
	}

	handle, err := db.OpenRead(w)
	if err != nil {
		t.Fatalf("db.OpenRead: %v", err)
	}
	defer handle.Close()
	server, err := dashboard.New(dashboard.Options{Workspace: w, DB: handle, Deps: dashboardDeps(w)})
	if err != nil {
		t.Fatalf("dashboard.New: %v", err)
	}
	ln, err := dashboard.Listen(dashboard.Options{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	serveCtx, stopServer := context.WithCancel(context.Background())
	defer stopServer()
	go func() { _ = server.Serve(serveCtx, ln) }()
	base := "http://" + ln.Addr().(*net.TCPAddr).String()
	t.Logf("dashboard on %s", base)

	var page struct {
		Tasks []struct {
			Crew   string `json:"crew"`
			Tokens struct {
				Input      int64 `json:"input"`
				CacheRead  int64 `json:"cache_read"`
				CacheWrite int64 `json:"cache_write"`
				Output     int64 `json:"output"`
				Total      int64 `json:"total"`
			} `json:"tokens"`
		} `json:"tasks"`
	}
	liveGetJSON(t, base+"/api/projects/shop", &page)

	var row struct {
		Crew   string
		Tokens struct {
			Input, CacheRead, CacheWrite, Output, Total int64
		}
	}
	found := false
	for _, task := range page.Tasks {
		if task.Crew == crew {
			row.Crew = task.Crew
			row.Tokens.Input = task.Tokens.Input
			row.Tokens.CacheRead = task.Tokens.CacheRead
			row.Tokens.CacheWrite = task.Tokens.CacheWrite
			row.Tokens.Output = task.Tokens.Output
			row.Tokens.Total = task.Tokens.Total
			found = true
		}
	}
	if !found {
		t.Fatalf("/api/projects/shop lists %+v, none of them crew %s", page.Tasks, crew)
	}
	if row.Tokens.Total == 0 {
		t.Fatalf("crew %s did real work; the API reports zero tokens for it", crew)
	}

	usageOut := runCLI(t, "usage", "shop", "--workspace", w.Root())
	crewLine := ""
	for _, line := range strings.Split(usageOut, "\n") {
		if strings.HasPrefix(line, crew+" ") || strings.HasPrefix(line, crew+"\t") {
			crewLine = line
		}
	}
	if crewLine == "" {
		t.Fatalf("`mate usage shop` printed no row for %s:\n%s", crew, usageOut)
	}
	for _, want := range []string{
		query.HumanizeTokens(row.Tokens.Input),
		query.HumanizeTokens(row.Tokens.CacheRead + row.Tokens.CacheWrite),
		query.HumanizeTokens(row.Tokens.Output),
		query.HumanizeTokens(row.Tokens.Total),
	} {
		if !strings.Contains(crewLine, want) {
			t.Errorf("`mate usage` row %q does not carry the API's %q", crewLine, want)
		}
	}
	t.Logf("crew %s: API total %d == usage row %q", crew, row.Tokens.Total, strings.TrimSpace(crewLine))

	var events struct {
		Events []json.RawMessage `json:"events"`
	}
	liveGetJSON(t, base+"/api/events?since=0&project=shop", &events)
	story, err := timeline.Story(ctx, handle.SQL(), timeline.StoryQuery{Project: "shop"})
	if err != nil {
		t.Fatalf("timeline.Story: %v", err)
	}
	if len(events.Events) != len(story) {
		t.Fatalf("/api/events?since=0 returned %d event(s), the story holds %d", len(events.Events), len(story))
	}
	if len(story) == 0 {
		t.Fatal("a live acceptance run produced no story at all")
	}
	t.Logf("/api/events?since=0 == the story: %d event(s)", len(story))
}

func liveGetJSON(t *testing.T, url string, into any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}
