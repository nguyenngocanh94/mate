package spawn_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

const codexSessionA = "01a0d260-cd47-77d2-bee7-46d98aa0461a"

// codexDeps is fakeDeps with Codex's rollouts in a directory of the test's
// own, which it returns, so no test reads the operator's ~/.codex.
func codexDeps(t *testing.T, rt *runtime.Fake) (spawn.Deps, string) {
	t.Helper()
	deps := fakeDeps(t, rt)
	sessions := t.TempDir()
	deps.Harnesses = codexSessionsIn(t, sessions)
	return deps, sessions
}

// writeCodexRollout writes a rollout file of codex-cli 0.154.0's naming and
// first-record shape.
func writeCodexRollout(t *testing.T, sessions, id, cwd string, at time.Time) {
	t.Helper()
	local := at.Local()
	dir := filepath.Join(sessions, local.Format("2006"), local.Format("01"), local.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("rollout-%s-%s.jsonl", local.Format("2006-01-02T15-04-05"), id)
	line := fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"session_id":%q,"id":%q,"timestamp":%q,"cwd":%q,"cli_version":"0.154.0","model_provider":"openai"}}`+"\n",
		at.UTC().Format(time.RFC3339Nano), id, id, at.UTC().Format(time.RFC3339Nano), cwd)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setSessionRef(t *testing.T, rt *runtime.Fake, w *store.Workspace, agent, ref string) {
	t.Helper()
	ag, ok := rt.Agents[w.Session()+"/"+agent]
	if !ok {
		t.Fatalf("no live fake agent %s", agent)
	}
	ag.SessionRef = ref
}

func lastArgv(t *testing.T, rt *runtime.Fake) []string {
	t.Helper()
	if len(rt.StartArgv) == 0 {
		t.Fatal("nothing was started")
	}
	return rt.StartArgv[len(rt.StartArgv)-1]
}

// TestStopMateRecordsTheCodexSessionAndStartResumesIt is B11 end to end on
// the fake: Herdr's agent_session is read at stop, kept in mate.meta, and the
// next start launches `codex resume <flags> <id>`.
func TestStopMateRecordsTheCodexSessionAndStartResumesIt(t *testing.T) {
	w := newWorkspace(t, "blog")
	rt := runtime.NewFake()
	deps, sessions := codexDeps(t, rt)
	ctx := context.Background()

	first, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "blog", Harness: harness.KindCodex, Resume: true})
	if err != nil {
		t.Fatalf("first StartMate: %v", err)
	}
	setSessionRef(t, rt, w, first.Agent, codexSessionA)
	writeCodexRollout(t, sessions, codexSessionA, w.MateDir("blog"), deps.Now())

	stopped, err := spawn.StopMate(ctx, w, deps, "blog")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if stopped.SessionID != codexSessionA {
		t.Fatalf("stopped session id = %q, want Herdr's %q", stopped.SessionID, codexSessionA)
	}
	if got := readMeta(t, w, "blog")[spawn.MetaSessionID]; got != codexSessionA {
		t.Fatalf("meta session_id after stop = %q, want %q", got, codexSessionA)
	}

	second, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "blog", Harness: harness.KindCodex, Resume: true})
	if err != nil {
		t.Fatalf("resuming StartMate: %v", err)
	}
	if !second.Resumed || second.ResumedFrom != codexSessionA || second.SessionID != codexSessionA {
		t.Fatalf("second start = resumed %v from %q id %q, want a resume of %q (note %q)",
			second.Resumed, second.ResumedFrom, second.SessionID, codexSessionA, second.ResumeNote)
	}
	argv := lastArgv(t, rt)
	sep := slices.Index(argv, "--")
	if sep < 0 || argv[sep+1] != "resume" || argv[len(argv)-1] != codexSessionA {
		t.Fatalf("resumed argv %v, want `-- resume <flags> %s`", argv, codexSessionA)
	}
	meta := readMeta(t, w, "blog")
	if meta[spawn.MetaResumed] != "true" || meta[spawn.MetaResumedFrom] != codexSessionA {
		t.Fatalf("meta resumed/resumed_from = %q/%q", meta[spawn.MetaResumed], meta[spawn.MetaResumedFrom])
	}

	// A resumed session that is stopped before its first prompt has no
	// agent_session in Herdr yet, and its rollout is older than this launch:
	// the id it resumed is kept, not blanked.
	again, err := spawn.StopMate(ctx, w, deps, "blog")
	if err != nil {
		t.Fatalf("second StopMate: %v", err)
	}
	if again.SessionID != codexSessionA || readMeta(t, w, "blog")[spawn.MetaSessionID] != codexSessionA {
		t.Fatalf("session id after a quiet resumed stop = %q / meta %q, want %q kept",
			again.SessionID, readMeta(t, w, "blog")[spawn.MetaSessionID], codexSessionA)
	}
}

// Without Herdr's agent_session (the agent already gone, or no Herdr Codex
// integration), the rollout Codex wrote in the Mate's cwd after launch names
// the session.
func TestStopMateAdoptsTheCodexRolloutWhenHerdrHasNoSession(t *testing.T) {
	w := newWorkspace(t, "blog")
	rt := runtime.NewFake()
	deps, sessions := codexDeps(t, rt)
	ctx := context.Background()

	if _, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "blog", Harness: harness.KindCodex}); err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	cwd, err := filepath.EvalSymlinks(w.MateDir("blog"))
	if err != nil {
		t.Fatal(err)
	}
	writeCodexRollout(t, sessions, codexSessionA, cwd, deps.Now().Add(2*time.Second))
	// Another directory's session, and one of this directory's from before
	// the launch: neither is this Mate's.
	writeCodexRollout(t, sessions, "01a0d260-0000-7000-8000-000000000001", "/elsewhere", deps.Now().Add(time.Second))
	writeCodexRollout(t, sessions, "01a0d260-0000-7000-8000-000000000002", cwd, deps.Now().Add(-time.Hour))

	stopped, err := spawn.StopMate(ctx, w, deps, "blog")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if stopped.SessionID != codexSessionA {
		t.Fatalf("stopped session id = %q, want the adopted %q", stopped.SessionID, codexSessionA)
	}
}

// codex-cli 0.154.0 answers `codex resume <id>` for an id with no rollout by
// exiting to the shell; the start must not launch it at all.
func TestStartMateGoesFreshWhenTheCodexRolloutIsGone(t *testing.T) {
	w := newWorkspace(t, "blog")
	rt := runtime.NewFake()
	deps, sessions := codexDeps(t, rt)
	ctx := context.Background()

	first, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "blog", Harness: harness.KindCodex})
	if err != nil {
		t.Fatal(err)
	}
	setSessionRef(t, rt, w, first.Agent, codexSessionA)
	if _, err := spawn.StopMate(ctx, w, deps, "blog"); err != nil {
		t.Fatal(err)
	}
	// No rollout was ever written for codexSessionA.
	second, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "blog", Harness: harness.KindCodex, Resume: true})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if second.Resumed {
		t.Fatal("a session with no rollout must not be resumed")
	}
	// The note is the one mate has always printed for a Codex Mate.
	want := "mate.meta recorded the Codex session " + codexSessionA + " but " + sessions + " has no rollout for it; starting a fresh session instead"
	if second.ResumeNote != want {
		t.Fatalf("ResumeNote = %q, want %q", second.ResumeNote, want)
	}
	if slices.Contains(lastArgv(t, rt), "resume") {
		t.Fatalf("fresh argv %v must not carry resume", lastArgv(t, rt))
	}
}

// A resumed launch that does not come up is retried once as a fresh session
// in a new tab, and the result says why; the project is not left without a
// Mate because a conversation could not be restored.
func TestStartMateFallsBackToFreshWhenTheResumedLaunchFails(t *testing.T) {
	w := newWorkspace(t, "blog")
	rt := runtime.NewFake()
	deps, sessions := codexDeps(t, rt)
	ctx := context.Background()

	first, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "blog", Harness: harness.KindCodex})
	if err != nil {
		t.Fatal(err)
	}
	setSessionRef(t, rt, w, first.Agent, codexSessionA)
	writeCodexRollout(t, sessions, codexSessionA, w.MateDir("blog"), deps.Now())
	if _, err := spawn.StopMate(ctx, w, deps, "blog"); err != nil {
		t.Fatal(err)
	}

	// The resumed pane shows a screen the settle cannot name (the fake
	// shows NextStartupScreen once, then the composer again).
	rt.NextStartupScreen = "ERROR: something codex has never printed before\n"
	second, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "blog", Harness: harness.KindCodex, Resume: true})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if second.Resumed {
		t.Fatal("the fallback start is fresh, not resumed")
	}
	if !strings.Contains(second.ResumeNote, "resuming the codex session "+codexSessionA+" failed") {
		t.Fatalf("ResumeNote = %q, want it to say the resume failed", second.ResumeNote)
	}
	if n := len(rt.StartArgv); n != 3 {
		t.Fatalf("StartAgent calls = %d, want 3 (first, failed resume, fresh)", n)
	}
	if !slices.Contains(rt.StartArgv[1], "resume") || slices.Contains(rt.StartArgv[2], "resume") {
		t.Fatalf("argv: resume attempt %v, fallback %v", rt.StartArgv[1], rt.StartArgv[2])
	}
	meta := readMeta(t, w, "blog")
	if meta[spawn.MetaAgent] != second.Agent || meta[spawn.MetaResumed] != "" {
		t.Fatalf("meta after fallback = %v", meta)
	}
}
