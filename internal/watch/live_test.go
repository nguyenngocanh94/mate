package watch_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/codexlab"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/watch"
)

// requireLive gates this package's live proof the way every other package
// does: a provisioned Herdr lab session and the harness binaries are machine
// facts, so MATE_LIVE=1 is the single opt-in.
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("MATE_LIVE") != "1" {
		t.Skip("set MATE_LIVE=1 to run live Herdr proofs")
	}
}

func liveLabSession(t *testing.T) (session, configHome string) {
	t.Helper()
	session = strings.TrimSpace(os.Getenv("MATE_HERDR_LIVE_SESSION"))
	if session == "" {
		t.Skip("set MATE_HERDR_LIVE_SESSION to a provisioned fm-lab-* Herdr session")
	}
	if session == "default" || session == "firstmate" {
		t.Fatal("refusing to run a live observer against the default or firstmate session")
	}
	if !strings.HasPrefix(session, "fm-lab-") {
		t.Fatalf("live test requires an fm-lab- session, got %q", session)
	}
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		t.Fatal("HOME is required to resolve the Herdr socket")
	}
	// Every live test that reaches a lab session runs Codex in a lab
	// CODEX_HOME, never the operator's ~/.codex (internal/harness/codexlab).
	codexlab.Home(t)
	return session, filepath.Join(home, ".config")
}

// TestLiveWatchOpensAndResolvesIncidentsOnARealCrew is task 18's proof, and
// the only evidence that the rules in watch.go describe a real Codex pane
// rather than the fake one the unit tests script.
//
// It walks the whole contract of mvp.md section 4b on one crew:
//
//  1. a real Codex crew whose brief ends its turn without a status line goes
//     `stale` once the (shortened) threshold passes;
//  2. a line typed into its pane through internal/send - the same path
//     `mate send` takes - resolves it;
//  3. stopping the agent behind mate's back opens `runtime_lost`.
//
// Measured 2026-09-18, codex-cli 0.154.0, Herdr 0.8.2 (the numbers this test
// was written against):
//
//   - The brief is "reply with the single word ok and do nothing else"
//     rather than the `sleep 400` mvp.md's task row suggests. Codex draws
//     "• Working (Ns • esc to interrupt)" for the whole of a shell command,
//     and internal/send classifies that as Busy - correctly, the harness is
//     mid-turn - so a sleeping crew is never stale. A crew that finished its
//     turn and is waiting with an empty composer is the state the rule is
//     actually about.
//   - The spinner line is the only thing that moves on an idle Codex pane;
//     once the turn ends, the 40-line snapshot is byte-identical between
//     polls, which is what makes the pane hash a usable quiet signal.
func TestLiveWatchOpensAndResolvesIncidentsOnARealCrew(t *testing.T) {
	requireLive(t)
	session, configHome := liveLabSession(t)

	w := liveWorkspace(t, session)
	rt, deps := liveDeps(t, configHome)
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	res, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project:   "shop",
		Crew:      "k3",
		Harness:   harness.KindCodex,
		BriefText: brieftest.Ship("Reply with the single word ok and do nothing else. Do not run any command and do not write to any file."),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
	})
	t.Logf("spawned crew %s in pane %s of session %s", res.Agent, res.Pane, res.Session)

	handle := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session, ConfigHome: configHome},
		Name:    res.Agent,
		RawID:   "k3",
		Kind:    harness.KindCodex,
		Tab: runtime.TabHandle{
			Session:     runtime.SessionHandle{Name: session, ConfigHome: configHome},
			WorkspaceID: res.Workspace,
			TabID:       res.Tab,
			PaneID:      res.Pane,
		},
	}

	// A 20-second threshold, polled by hand: the production defaults (5s and
	// 3m) are the same code with different numbers, and waiting three
	// minutes per transition would make this proof cost ten.
	watcher := watch.New(w, watch.Deps{
		Runtime:    rt,
		Handle:     liveHandleFunc(w, deps),
		StaleAfter: 20 * time.Second,
	})

	// 1. The crew answers and stops. Its pane then holds still, and nothing
	// lands in `.status`, which is exactly the shape `stale` is about.
	waitForComposer(t, ctx, rt, handle, send.StateEmpty, 3*time.Minute)
	waitForIncident(t, ctx, watcher, w, "k3", "stale", store.IncidentOpen, 2*time.Minute)
	t.Logf("incidents after the quiet period:\n%s", incidentDump(t, w))

	// 2. A line into the pane is movement: the composer holds it, then the
	// turn starts. Either way the screen changed, and the incident closes.
	if _, err := send.Send(ctx, send.Deps{Runtime: rt}, handle, harness.KindCodex,
		"reply with the single word ack", send.Options{}); err != nil {
		t.Fatalf("send into the crew pane: %v", err)
	}
	waitForIncident(t, ctx, watcher, w, "k3", "stale", store.IncidentResolved, 2*time.Minute)

	// 3. The agent is stopped behind mate's back - a crash, or a human
	// closing the pane. The meta still records it, so the observer asks
	// Herdr and is told the agent is gone.
	if err := rt.StopAgent(ctx, handle, runtime.StopForce); err != nil {
		t.Fatalf("StopAgent: %v", err)
	}
	waitForIncident(t, ctx, watcher, w, "k3", "runtime_lost", store.IncidentOpen, 2*time.Minute)
	t.Logf("incidents at the end:\n%s", incidentDump(t, w))

	if h, ok := watcher.Health("shop", "k3"); !ok || h.AgentPresent {
		t.Fatalf("health = %+v (ok %v), want an observation saying the agent is gone", h, ok)
	}
}

// waitForComposer polls the real pane until it classifies as want. It is the
// measurement the test's own comment records: if a live Codex crew never
// reaches an empty composer, the stale rule has nothing to stand on and this
// fails here rather than three minutes later.
func waitForComposer(t *testing.T, ctx context.Context, rt runtime.Adapter,
	handle runtime.AgentHandle, want send.ComposerState, budget time.Duration) {
	t.Helper()
	deadline := time.Now().Add(budget)
	var last send.Classification
	for {
		screen, err := rt.ReadAgent(ctx, handle, send.DefaultLines)
		if err != nil {
			t.Fatalf("ReadAgent: %v", err)
		}
		class, err := send.ClassifyComposer(harness.KindCodex, screen)
		if err != nil {
			t.Fatalf("ClassifyComposer: %v", err)
		}
		last = class
		if class.State == want {
			t.Logf("pane reached composer %s (%q)", class.State, class.Evidence)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never reached composer %s within %s; last was %s (%q)\n%s",
				want, budget, last.State, last.Evidence, send.ScreenTail(screen, 20))
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled waiting for composer %s", want)
		case <-time.After(2 * time.Second):
		}
	}
}

// waitForIncident polls the observer for real until `incidents.log` holds the
// transition, then checks it is the last line for that (crew, kind) - which
// is what "the incident is open" means (mvp.md section 4b).
func waitForIncident(t *testing.T, ctx context.Context, watcher *watch.Watcher,
	w *store.Workspace, crew, kind, state string, budget time.Duration) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for {
		if err := watcher.Poll(ctx); err != nil {
			t.Logf("poll: %v", err)
		}
		entries, _, err := w.ReadIncidents("shop", 0)
		if err != nil {
			t.Fatalf("ReadIncidents: %v", err)
		}
		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			if e.Crew != crew || e.Kind != kind {
				continue
			}
			if e.State == state {
				t.Logf("%s %s %s: %s", crew, kind, state, e.Text)
				return
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the observer never wrote %s %s %s within %s\n%s",
				crew, kind, state, budget, incidentDump(t, w))
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled waiting for %s %s %s", crew, kind, state)
		case <-time.After(3 * time.Second):
		}
	}
}

func incidentDump(t *testing.T, w *store.Workspace) string {
	t.Helper()
	entries, _, err := w.ReadIncidents("shop", 0)
	if err != nil {
		return "incidents.log unreadable: " + err.Error()
	}
	if len(entries) == 0 {
		return "incidents.log is empty"
	}
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "  %s %s %s %s %s\n", e.Time.Format(time.RFC3339), e.Crew, e.Kind, e.State, e.Text)
	}
	return b.String()
}

func liveHandleFunc(w *store.Workspace, deps spawn.Deps) watch.HandleFunc {
	return func(ctx context.Context, project, crew string) (runtime.AgentHandle, harness.Kind, error) {
		return spawn.CrewHandle(ctx, w, deps, project, crew)
	}
}

// liveWorkspace is a workspace on the lab session with one real git repo
// registered as project "shop".
func liveWorkspace(t *testing.T, session string) *store.Workspace {
	t.Helper()
	// TMPDIR must not go through a symlink: Herdr reports a pane cwd with
	// symlinks resolved and the launch guard compares the two (mvp.md
	// section 7).
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	useLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	liveGit(t, repo, "init", "-b", "main")
	liveGit(t, repo, "config", "user.email", "mate-test@example.com")
	liveGit(t, repo, "config", "user.name", "mate test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	liveGit(t, repo, "add", "README.md")
	liveGit(t, repo, "commit", "-m", "init")
	if err := w.AddProject("shop", store.ProjectConfig{Repo: repo, DefaultBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

func liveDeps(t *testing.T, configHome string) (*runtime.Herdr, spawn.Deps) {
	t.Helper()
	names := runtime.NewMemoryNameRegistry()
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = names
	rt.StartServer = func(context.Context, string) error {
		t.Fatal("the lab session is provisioned by the runner; this test must not start a Herdr server")
		return nil
	}
	return rt, spawn.Deps{
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               binaryPath(t),
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 60 * time.Second,
		StartTimeout:         60 * time.Second,
	}
}

// useLabSession rewrites workspace.yaml's session name to the provisioned
// lab: store.Init derives it from the workspace path, which a lab run must
// not use.
func useLabSession(t *testing.T, w *store.Workspace, session string) {
	t.Helper()
	path := w.WorkspaceFile()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	scanner := bufio.NewScanner(f)
	replaced := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "session:") {
			line = "session: " + session
			replaced = true
		}
		out = append(out, line)
	}
	f.Close()
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !replaced {
		t.Fatalf("%s carries no session key", path)
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// binaryPath builds the mate binary the crew's brief points at.
func binaryPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "mate")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/nguyenngocanh94/mate/cmd/mate")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build mate: %v\n%s", err, out)
	}
	return bin
}

func liveGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
