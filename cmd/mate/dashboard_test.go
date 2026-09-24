package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// dashboardServer puts a dashboard on a workspace, wired the way
// cmdDashboard wires it, and returns an httptest server for it.
func dashboardServer(t *testing.T, w *store.Workspace) *httptest.Server {
	t.Helper()
	handle, err := db.OpenRead(w)
	if err != nil {
		t.Fatalf("db.OpenRead: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	server, err := dashboard.New(dashboard.Options{Workspace: w, DB: handle, Deps: dashboardDeps(w)})
	if err != nil {
		t.Fatalf("dashboard.New: %v", err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func dashboardGet(t *testing.T, ts *httptest.Server, path string, into any) {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

// TestDashboardReportsTheSameNumbersAsUsage is docs/mvp.md M6 task 28's
// "mọi endpoint trả đúng số": the API and `mate usage` are two renderings
// of one ledger, so every token the page shows must be a token the terminal
// shows, humanised by the same function (query.HumanizeTokens).
//
// It compares against the CLI's printed table rather than against a number
// written here: a test that agreed with a constant would still pass if both
// surfaces drifted together away from `v_task_ledger`.
func TestDashboardReportsTheSameNumbersAsUsage(t *testing.T) {
	w := usageWorkspace(t)
	ts := dashboardServer(t, w)

	var page struct {
		Mate struct {
			Turns  int64 `json:"turns"`
			Tokens struct {
				Total int64 `json:"total"`
			} `json:"tokens"`
		} `json:"mate"`
		Tasks []struct {
			Crew   string `json:"crew"`
			Turns  int64  `json:"turns"`
			Tokens struct {
				Input      int64 `json:"input"`
				CacheRead  int64 `json:"cache_read"`
				CacheWrite int64 `json:"cache_write"`
				Output     int64 `json:"output"`
				Total      int64 `json:"total"`
			} `json:"tokens"`
			Cost *float64 `json:"cost"`
		} `json:"tasks"`
	}
	dashboardGet(t, ts, "/api/projects/shop", &page)
	if len(page.Tasks) != 1 || page.Tasks[0].Crew != "k3" {
		t.Fatalf("tasks = %+v, want the workspace's one crew k3", page.Tasks)
	}
	crew := page.Tasks[0]

	out := runCLI(t, "usage", "shop", "--workspace", w.Root())
	mateLine, crewLine := "", ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "mate"):
			mateLine = line
		case strings.HasPrefix(line, "k3"):
			crewLine = line
		}
	}
	if mateLine == "" || crewLine == "" {
		t.Fatalf("usage printed no mate or k3 row:\n%s", out)
	}

	// Every column the two surfaces share, in the CLI's own words.
	for _, want := range []string{
		query.HumanizeTokens(crew.Tokens.Input),
		query.HumanizeTokens(crew.Tokens.CacheRead + crew.Tokens.CacheWrite),
		query.HumanizeTokens(crew.Tokens.Output),
		query.HumanizeTokens(crew.Tokens.Total),
	} {
		if !strings.Contains(crewLine, want) {
			t.Errorf("usage row %q does not carry the API's %q", crewLine, want)
		}
	}
	if !strings.Contains(mateLine, query.HumanizeTokens(page.Mate.Tokens.Total)) {
		t.Errorf("usage mate row %q does not carry the API's total %q",
			mateLine, query.HumanizeTokens(page.Mate.Tokens.Total))
	}
	if crew.Cost != nil {
		t.Errorf("cost = %v, want null while the model is unpriced - the CLI prints ?", *crew.Cost)
	}
	if !strings.Contains(crewLine, "?") {
		t.Errorf("usage row %q should print ? for an unpriced cost", crewLine)
	}

	// And the per-crew form: one row per turn, the same count the API lists.
	var task struct {
		Turns []struct {
			ID string `json:"id"`
		} `json:"turns"`
	}
	dashboardGet(t, ts, "/api/projects/shop/tasks/k3", &task)
	if int64(len(task.Turns)) != crew.Turns {
		t.Fatalf("task page lists %d turn(s), the ledger says %d", len(task.Turns), crew.Turns)
	}
	turnRows := 0
	for _, line := range strings.Split(runCLI(t, "usage", "shop", "k3", "--workspace", w.Root()), "\n") {
		if strings.HasPrefix(line, "1") || strings.HasPrefix(line, "2") || strings.HasPrefix(line, "3") {
			turnRows++
		}
	}
	if turnRows != len(task.Turns) {
		t.Errorf("`usage shop k3` printed %d turn row(s), the API lists %d", turnRows, len(task.Turns))
	}
}

// TestDashboardEventsMatchTheEventsCommand: `/api/events?since=0` and
// `mate events <project>` read the same story through the same function,
// so they must return the same events in the same order.
func TestDashboardEventsMatchTheEventsCommand(t *testing.T) {
	w := eventsWorkspace(t)
	runCLI(t, "reindex", w.Root())
	ts := dashboardServer(t, w)

	var page struct {
		Events []struct {
			ID   int64  `json:"id"`
			Kind string `json:"kind"`
		} `json:"events"`
	}
	dashboardGet(t, ts, "/api/events?since=0&project=shop", &page)

	var want []struct {
		ID   int64  `json:"id"`
		Kind string `json:"kind"`
	}
	for _, line := range strings.Split(runCLI(t, "events", "shop", "--workspace", w.Root()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row struct {
			ID   int64  `json:"id"`
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("`mate events` printed a line that is not JSON: %q", line)
		}
		want = append(want, row)
	}
	if len(want) == 0 {
		t.Fatal("`mate events` printed nothing; there is nothing to compare")
	}
	if len(page.Events) != len(want) {
		t.Fatalf("/api/events returned %d event(s), `mate events` printed %d", len(page.Events), len(want))
	}
	for i := range want {
		if page.Events[i].ID != want[i].ID || page.Events[i].Kind != want[i].Kind {
			t.Fatalf("event %d: API %d/%s, CLI %d/%s", i,
				page.Events[i].ID, page.Events[i].Kind, want[i].ID, want[i].Kind)
		}
	}
}

// TestDashboardCommandRefusesARemoteBind: the flag is the whole protection,
// so the command must refuse before it opens a socket.
func TestDashboardCommandRefusesARemoteBind(t *testing.T) {
	w := usageWorkspace(t)
	var stdout, stderr bytes.Buffer
	err := run([]string{"dashboard", w.Root(), "--addr", "0.0.0.0:0"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("mate dashboard bound every interface without --allow-remote")
	}
	if !strings.Contains(err.Error(), "--allow-remote") {
		t.Errorf("refusal %q does not say how to ask for it on purpose", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("a refused bind printed a URL: %q", stdout.String())
	}
}

// TestDashboardCommandRejectsTwoWorkspaces keeps the argument and the flag
// from silently disagreeing.
func TestDashboardCommandRejectsTwoWorkspaces(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"dashboard", "/a", "--workspace", "/b"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("mate dashboard accepted two workspaces")
	}
	var usageErr *usageError
	if !errors.As(err, &usageErr) {
		t.Errorf("error %v is not a usage error; the CLI would exit 1 instead of 2", err)
	}
}

// TestDashboardDiffIsTheCLIsOwnText: one function behind both, so a reader
// comparing the page with the terminal sees the same bytes.
func TestDashboardDiffIsTheCLIsOwnText(t *testing.T) {
	w := usageWorkspace(t)
	ts := dashboardServer(t, w)

	var got struct {
		Exists bool   `json:"exists"`
		Text   string `json:"text"`
		Reason string `json:"reason"`
	}
	dashboardGet(t, ts, "/api/projects/shop/tasks/k3/diff", &got)

	// The fixture crew records no branch, which the CLI's own answer is
	// also a refusal about - the page says so instead of failing.
	if got.Exists || got.Text != "" || got.Reason == "" {
		t.Fatalf("diff = %+v, want an empty answer with a reason", got)
	}
	if _, err := dashboardDeps(w).BranchExists(context.Background(), "shop", "nosuch"); err != nil {
		t.Fatalf("BranchExists on a real repo: %v", err)
	}
}
