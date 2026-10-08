package prwatch_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/github"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/prwatch"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

const (
	project = "shop"
	crew    = "k3"
	prURL   = "https://github.com/acme/shop/pull/7"
)

var epoch = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// clock is the fake time of the watcher and the outbox: Sleep moves it, so
// minutes of polling cost nothing.
type clock struct {
	mu  sync.Mutex
	now time.Time
	// slept counts the pauses, which is how many polls a test saw.
	slept int
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.slept++
	return nil
}

// scriptedGH answers `gh pr view` from a queue: each call takes the next
// answer, and the last one repeats.
type scriptedGH struct {
	mu      sync.Mutex
	answers []github.Result
	calls   int
}

func (g *scriptedGH) Run(_ context.Context, cmd github.Command) (github.Result, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	i := g.calls - 1
	if i >= len(g.answers) {
		i = len(g.answers) - 1
	}
	return g.answers[i], nil
}

func prJSON(state, oid string) github.Result {
	merge := "null"
	if oid != "" {
		merge = `{"oid":"` + oid + `"}`
	}
	return github.Result{Stdout: `{"state":"` + state + `","mergeCommit":` + merge + `,"baseRefName":"main"}`}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

const rule = "─────────────────────────────────────────"

func claudeScreen(content string) string {
	return "some transcript\n" + rule + "\n❯ " + content + "\n" + rule + "\n  Sonnet 5 · medium\n"
}

// env is a workspace with one project over a real git checkout whose origin
// is a bare repository in a temp directory - no network - one crew, a fake
// Herdr holding the Mate's idle pane, and a fake gh.
type env struct {
	t      *testing.T
	ws     *store.Workspace
	repo   string
	origin string
	rt     *runtime.Fake
	handle runtime.AgentHandle
	clock  *clock
	gh     *scriptedGH
	log    bytes.Buffer
	// mergeSHA is the commit a second clone pushed to origin's main, the
	// one the "merged" pull request produced.
	mergeSHA string
}

func newEnv(t *testing.T, gh ...github.Result) *env {
	t.Helper()
	root := t.TempDir()
	ws, err := store.Init(root, store.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, ws: ws, repo: filepath.Join(root, "shop", "shop"), origin: filepath.Join(t.TempDir(), "origin.git")}
	run(t, root, "init", "--bare", "-b", "main", e.origin)
	if err := os.MkdirAll(e.repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, e.repo, "init", "-b", "main")
	run(t, e.repo, "commit", "--allow-empty", "-m", "init")
	run(t, e.repo, "remote", "add", "origin", e.origin)
	run(t, e.repo, "push", "-u", "origin", "main")
	// The pull request's merge lands on origin from somewhere else.
	other := filepath.Join(t.TempDir(), "other")
	run(t, root, "clone", e.origin, other)
	run(t, other, "commit", "--allow-empty", "-m", "squashed pull request")
	run(t, other, "push", "origin", "main")
	e.mergeSHA = run(t, other, "rev-parse", "HEAD")

	if err := ws.AddProject(project, store.ProjectConfig{Repos: []store.RepoConfig{{Path: "shop/shop", DefaultBranch: "main"}}}); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteCrewMeta(project, crew, map[string]string{"state": "spawned", "branch": "mate/k3", "task": "x"}); err != nil {
		t.Fatal(err)
	}
	if err := ws.AppendStatus(project, crew, "pr-open: "+prURL); err != nil {
		t.Fatal(err)
	}

	e.rt = runtime.NewFake()
	e.handle = runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: "fm-lab-test"},
		Name:    "mate-" + project,
		RawID:   project,
		Kind:    claude.KindClaude,
		Tab:     runtime.TabHandle{PaneID: "w1:p1"},
	}
	e.rt.PutAgent(e.handle, runtime.AgentIdle)
	e.rt.SetReadOutput(e.handle, claudeScreen(""))
	e.clock = &clock{now: epoch}
	e.gh = &scriptedGH{answers: gh}
	return e
}

func (e *env) outbox() *outbox.Sender {
	return outbox.New(e.ws, outbox.Deps{
		Harnesses: catalog.Default(),
		Runtime:   e.rt,
		Handle: func(context.Context, string) (runtime.AgentHandle, harness.Kind, error) {
			return e.handle, claude.KindClaude, nil
		},
		Clock:   e.clock,
		Sleeper: e.clock,
	})
}

func (e *env) deps() prwatch.Deps {
	return prwatch.Deps{
		WS:     e.ws,
		GH:     github.Client{Runner: e.gh},
		Git:    gitx.New(),
		Outbox: e.outbox(),
		Sleep:  e.clock.Sleep,
		Now:    e.clock.Now,
		Log:    &e.log,
	}
}

func (e *env) watch() error {
	e.t.Helper()
	return prwatch.Watch(context.Background(), e.deps(), project, crew, prURL)
}

func (e *env) meta() map[string]string {
	e.t.Helper()
	m, err := e.ws.ReadCrewMeta(project, crew)
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) statusLines() []string {
	e.t.Helper()
	entries, _, err := e.ws.ReadStatus(project, crew, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, en := range entries {
		out = append(out, en.Line)
	}
	return out
}

func (e *env) typed() []string {
	var out []string
	for _, s := range e.rt.SentText {
		out = append(out, s.Text)
	}
	return out
}

func (e *env) prItems() []store.OutboxItem {
	e.t.Helper()
	items, err := e.ws.ReadOutbox(project)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []store.OutboxItem
	for _, it := range items {
		if it.Source == store.OutboxSourcePR {
			out = append(out, it)
		}
	}
	return out
}

func (e *env) head(branch string) string {
	return run(e.t, e.repo, "rev-parse", branch)
}

// A merged pull request: the checkout is fast-forwarded, the meta, the status
// file and the Mate all hear of it, once - and the project is in manual mode,
// where a digest would not have been sent.
func TestMergedPullRequestFastForwardsRecordsAndWakesTheMate(t *testing.T) {
	e := newEnv(t, prJSON("OPEN", ""), prJSON("OPEN", ""), prJSON("MERGED", "ignored"))
	e.gh.answers[2] = prJSON("MERGED", e.mergeSHA)
	if e.ws.Auto(project) {
		t.Fatal("the fixture is not in manual mode")
	}
	before := e.head("main")

	if err := e.watch(); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	if e.clock.slept < 2 || e.gh.calls != 3 {
		t.Fatalf("polled %d times with %d pauses, want 3 and at least 2", e.gh.calls, e.clock.slept)
	}
	if got := e.head("main"); got != e.mergeSHA || got == before {
		t.Fatalf("main = %s, want fast-forwarded to %s (was %s)", got, e.mergeSHA, before)
	}
	m := e.meta()
	if m[crewstate.MetaPRState] != crewstate.PRStateMerged || m[crewstate.MetaMergeCommit] != e.mergeSHA || m[crewstate.MetaPRURL] != prURL || m[crewstate.MetaPRSync] != "fast-forwarded main" {
		t.Fatalf("meta = %v", m)
	}
	lines := e.statusLines()
	if last := lines[len(lines)-1]; last != "pr-merged: "+prURL {
		t.Fatalf("status = %q, want a closing pr-merged line", lines)
	}
	items := e.prItems()
	if len(items) != 1 || items[0].State != store.OutboxSent {
		t.Fatalf("pr outbox items = %+v, want exactly one, sent", items)
	}
	typed := e.typed()
	if len(typed) != 1 || !strings.Contains(typed[0], "pr-merged: k3 "+prURL) || !strings.Contains(typed[0], "fast-forwarded main") || !strings.Contains(typed[0], "mate crew stop shop k3") {
		t.Fatalf("typed %q, want the one pr-merged line", typed)
	}

	// Watching it again - recovery restarting a watcher that died after the
	// merge was seen - adds no second line, no second typing.
	if err := e.watch(); err != nil {
		t.Fatalf("second Watch: %v", err)
	}
	if n := len(e.statusLines()); n != len(lines) {
		t.Fatalf("status lines %d -> %d on a repeat", len(lines), n)
	}
	if len(e.prItems()) != 1 || len(e.typed()) != 1 {
		t.Fatalf("outbox items %d, typed %d on a repeat, want 1 and 1", len(e.prItems()), len(e.typed()))
	}
}

func TestClosedPullRequestIsReportedAndNothingIsMoved(t *testing.T) {
	e := newEnv(t, prJSON("CLOSED", ""))
	before := e.head("main")
	if err := e.watch(); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if e.head("main") != before {
		t.Fatal("a closed pull request moved the default branch")
	}
	m := e.meta()
	if m[crewstate.MetaPRState] != crewstate.PRStateClosed || m[crewstate.MetaMergeCommit] != "" {
		t.Fatalf("meta = %v", m)
	}
	lines := e.statusLines()
	if last := lines[len(lines)-1]; last != "pr-closed: "+prURL {
		t.Fatalf("status = %q", lines)
	}
	typed := e.typed()
	if len(typed) != 1 || !strings.Contains(typed[0], "pr-closed: k3 "+prURL) {
		t.Fatalf("typed %q, want one pr-closed line", typed)
	}
}

func TestDirtyCheckoutSkipsTheFastForwardAndSaysWhy(t *testing.T) {
	e := newEnv(t, prJSON("MERGED", ""))
	e.gh.answers[0] = prJSON("MERGED", e.mergeSHA)
	if err := os.WriteFile(filepath.Join(e.repo, "wip.txt"), []byte("work in progress"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := e.head("main")
	if err := e.watch(); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if e.head("main") != before {
		t.Fatal("the watcher moved a dirty checkout")
	}
	m := e.meta()
	if !strings.HasPrefix(m[crewstate.MetaPRSync], "skipped: the primary checkout has 1 uncommitted file") || m[crewstate.MetaPRState] != crewstate.PRStateMerged {
		t.Fatalf("meta = %v", m)
	}
	if typed := e.typed(); len(typed) != 1 || !strings.Contains(typed[0], "skipped: the primary checkout has 1 uncommitted file") {
		t.Fatalf("typed %q, want the reason in the line the Mate gets", typed)
	}
	if !strings.Contains(e.log.String(), "skipped") {
		t.Fatalf("log = %q, want the reason", e.log.String())
	}
}

func TestACheckoutNotOnTheDefaultBranchIsLeftAlone(t *testing.T) {
	e := newEnv(t, prJSON("MERGED", ""))
	e.gh.answers[0] = prJSON("MERGED", e.mergeSHA)
	run(t, e.repo, "checkout", "-b", "feature")
	if err := e.watch(); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if got := e.meta()[crewstate.MetaPRSync]; got != "skipped: the primary checkout is on feature, not main" {
		t.Fatalf("pr_sync = %q", got)
	}
	if got := run(t, e.repo, "rev-parse", "main"); got == e.mergeSHA {
		t.Fatal("main moved while another branch was checked out")
	}
}

// A failing gh is not the end of the watch.
func TestAFailedPollIsLoggedAndTriedAgain(t *testing.T) {
	e := newEnv(t, github.Result{ExitCode: 1, Stderr: "HTTP 502\n"}, prJSON("CLOSED", ""))
	if err := e.watch(); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if e.gh.calls != 2 || !strings.Contains(e.log.String(), "HTTP 502") {
		t.Fatalf("calls = %d, log = %q", e.gh.calls, e.log.String())
	}
	if e.meta()[crewstate.MetaPRState] != crewstate.PRStateClosed {
		t.Fatalf("meta = %v", e.meta())
	}
}

func TestAClosedCrewEndsTheWatchWithoutTellingAnyone(t *testing.T) {
	e := newEnv(t, prJSON("OPEN", ""))
	if err := e.ws.UpdateCrewMeta(project, crew, map[string]string{"state": "finished"}); err != nil {
		t.Fatal(err)
	}
	if err := e.watch(); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if e.gh.calls != 0 || len(e.typed()) != 0 || len(e.prItems()) != 0 {
		t.Fatalf("calls %d, typed %d, items %d for a closed crew", e.gh.calls, len(e.typed()), len(e.prItems()))
	}
}

func TestCancellingStopsAnOpenWatch(t *testing.T) {
	e := newEnv(t, prJSON("OPEN", ""))
	ctx, cancel := context.WithCancel(context.Background())
	d := e.deps()
	d.Sleep = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
	if err := prwatch.Watch(ctx, d, project, crew, prURL); err == nil {
		t.Fatal("a cancelled watch returned nil")
	}
	if e.meta()[crewstate.MetaPRState] != "" {
		t.Fatalf("meta = %v", e.meta())
	}
}

// The Mate mid-turn: the watcher keeps trying for DeliverFor and then leaves
// the line queued; the console's sender (Drain) delivers it later, once.
func TestABusyMateLeavesTheLineQueuedForTheConsole(t *testing.T) {
	e := newEnv(t, prJSON("CLOSED", ""))
	e.rt.SetReadOutput(e.handle, "some transcript\n✶ Pollinating…\n"+rule+"\n❯ \n"+rule+"\n")
	d := e.deps()
	d.DeliverFor = 30 * time.Second
	if err := prwatch.Watch(context.Background(), d, project, crew, prURL); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	items := e.prItems()
	if len(items) != 1 || !items[0].Queued() || items[0].Attempts == 0 {
		t.Fatalf("pr items = %+v, want one queued item that was tried", items)
	}
	if len(e.typed()) != 0 {
		t.Fatalf("typed %q into a busy Mate", e.typed())
	}

	e.rt.SetReadOutput(e.handle, claudeScreen(""))
	if err := e.outbox().Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if items := e.prItems(); len(items) != 1 || items[0].State != store.OutboxSent || len(e.typed()) != 1 {
		t.Fatalf("after the console's pass: items %+v, typed %q", items, e.typed())
	}
}
