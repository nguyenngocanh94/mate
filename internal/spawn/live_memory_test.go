package spawn_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Task 35's Claude measurements (docs/mvp.md section 7): auto-memory off
// (A1, B6), which SessionStart sources fire in a Herdr pane and whether their
// output reaches the model (A2), and whether --resume reloads a manual that
// changed while the Mate was stopped (A5).

// liveLab is one lab workspace with project shop registered, and the deps a
// live StartMate needs against the runner's provisioned Herdr session.
type liveLab struct {
	w          *store.Workspace
	rt         *runtime.Herdr
	deps       spawn.Deps
	session    string
	configHome string
	repo       string
}

func newLiveLab(t *testing.T) liveLab {
	t.Helper()
	requireLive(t)
	session, configHome := liveLabSession(t)
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	useLabSession(t, w, session)
	w, err = store.Open(root)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	liveGit(t, repo, "init", "-b", "main")
	liveGit(t, repo, "config", "user.email", "mate-test@example.com")
	liveGit(t, repo, "config", "user.name", "mate test")
	liveGit(t, repo, "commit", "--allow-empty", "-m", "init")
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
		Harnesses:            catalog.Default(),
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               binaryPath(t),
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 60 * time.Second,
		StartTimeout:         60 * time.Second,
	}
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })
	lab := liveLab{w: w, rt: rt, deps: deps, session: session, configHome: configHome, repo: repo}
	t.Cleanup(func() {
		// Whatever a test asserts, the lab must not be left with a Mate.
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = spawn.StopMate(ctx, w, deps, "shop")
	})
	return lab
}

func (l liveLab) handle(res spawn.StartResult) runtime.AgentHandle {
	return runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: l.session, ConfigHome: l.configHome},
		Name:    res.Agent,
		RawID:   "shop",
		Kind:    res.Harness,
		Tab:     runtime.TabHandle{PaneID: res.Pane, TabID: res.Tab, WorkspaceID: res.Workspace},
	}
}

// askClaudeMate prompts the Mate and returns the text of its Stop hook's
// sent.log entry for that turn, and the cursor after it.
func (l liveLab) askClaudeMate(t *testing.T, ctx context.Context, h runtime.AgentHandle, from int64, text string) (string, int64) {
	t.Helper()
	if err := l.rt.PromptAgent(ctx, h, text); err != nil {
		t.Fatalf("PromptAgent %q: %v", text, err)
	}
	var answer string
	next := waitForMateEntry(t, l.w, from, 3*time.Minute, func(e store.SentEntry) bool {
		if e.Source != store.SourceMate {
			return false
		}
		answer = e.Text
		return true
	})
	t.Logf("asked %q -> %q", text, answer)
	return answer, next
}

// TestLiveMateAutoMemoryOff is A1 and B6: a Claude Mate started by StartMate
// neither loads a MEMORY.md seeded in its own auto-memory directory nor
// writes there when the captain asks it to remember something; what it
// remembers goes to mate/memory.md.
func TestLiveMateAutoMemoryOff(t *testing.T) {
	lab := newLiveLab(t)
	projects, err := harness.ClaudeProjectsDir()
	if err != nil {
		t.Fatal(err)
	}
	mateDir := lab.w.MateDir("shop")
	slugDir := filepath.Join(projects, harness.ClaudeProjectSlug(mateDir))
	memDir := filepath.Join(slugDir, "memory")
	// The seed lives in the operator's ~/.claude/projects only for this
	// test's own throwaway cwd, and goes with the test.
	seed := []byte("# Memory index\n\n- The captain's lucky word is MARIGOLD-4417 (seeded canary).\n")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), seed, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(memDir) })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	started, err := spawn.StartMate(ctx, lab.w, lab.deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	h := lab.handle(started)

	answer, cursor := lab.askClaudeMate(t, ctx, h, 0,
		"Without running any tool or reading any file: do you know the captain's lucky word from your memory? Answer with the word only, or NONE.")
	if strings.Contains(strings.ToUpper(answer), "MARIGOLD") {
		t.Fatalf("the Mate loaded the seeded auto-memory: %q", answer)
	}

	memoryBefore, err := os.ReadFile(lab.w.MemoryFile("shop"))
	if err != nil {
		t.Fatal(err)
	}
	answer, _ = lab.askClaudeMate(t, ctx, h, cursor, "remember that the captain prefers short replies")

	// The proof the canary test was not vacuous: Claude keeps this Mate's
	// transcript in the same slug directory the seed was written to.
	meta := readMeta(t, lab.w, "shop")
	if got := filepath.Dir(meta[spawn.MetaTranscript]); got != slugDir {
		t.Fatalf("transcript dir = %q, want the seeded slug dir %q", got, slugDir)
	}
	entries, err := os.ReadDir(memDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "MEMORY.md" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("Claude's memory dir now holds %v, want only the seeded MEMORY.md", names)
	}
	if got, _ := os.ReadFile(filepath.Join(memDir, "MEMORY.md")); !bytes.Equal(got, seed) {
		t.Fatalf("the seeded MEMORY.md changed:\n%s", got)
	}
	memoryAfter, err := os.ReadFile(lab.w.MemoryFile("shop"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(memoryBefore, memoryAfter) || !strings.Contains(strings.ToLower(string(memoryAfter)), "short") {
		t.Fatalf("mate/memory.md did not get the preference (answer %q):\n%s", answer, memoryAfter)
	}
	t.Logf("mate/memory.md after:\n%s", memoryAfter)
}

// sessionStartEntry is one SessionStart payload the lab hook logged.
type sessionStartEntry struct {
	Source         string `json:"source"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

// writeCanaryHook writes a SessionStart hook that appends its payload to
// logPath and prints a canary naming the source, and returns the command.
func writeCanaryHook(t *testing.T, dir, logPath, word string) string {
	t.Helper()
	script := filepath.Join(dir, "session-start.sh")
	body := "#!/bin/sh\n" +
		"payload=$(cat)\n" +
		"printf '%s\\n' \"$payload\" | tr -d '\\n' >> '" + logPath + "'\n" +
		"printf '\\n' >> '" + logPath + "'\n" +
		"src=$(printf %s \"$payload\" | sed -n 's/.*\"source\" *: *\"\\([a-z]*\\)\".*/\\1/p')\n" +
		"echo \"The session canary word is " + word + "-$src.\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return "sh '" + script + "'"
}

func readSessionStarts(t *testing.T, logPath string) []sessionStartEntry {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []sessionStartEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e sessionStartEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("hook log line does not parse: %v: %s", err, line)
		}
		out = append(out, e)
	}
	return out
}

func waitSessionStart(t *testing.T, logPath string, n int, timeout time.Duration) []sessionStartEntry {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := readSessionStarts(t, logPath)
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited %s for SessionStart #%d; log has %+v", timeout, n, got)
		}
		time.Sleep(time.Second)
	}
}

// TestLiveMateSessionStartHookSources is A2: in a Herdr pane, a Claude Mate's
// SessionStart hook fires for startup, clear (the slash command sent with
// send.Send), compact, and resume (StopMate then StartMate), each once, and
// the hook's stdout is in the model's context every time.
func TestLiveMateSessionStartHookSources(t *testing.T) {
	lab := newLiveLab(t)
	mateDir := lab.w.MateDir("shop")
	hookDir := t.TempDir()
	logPath := filepath.Join(hookDir, "session-start.log")

	// The Mate's own settings plus a lab SessionStart hook. StartMate keeps
	// an existing settings file, so this is what the Mate launches with.
	base, err := spawn.ClaudeSettings(lab.deps.Binary)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(base, &settings); err != nil {
		t.Fatal(err)
	}
	settings["hooks"].(map[string]any)["SessionStart"] = []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": writeCanaryHook(t, hookDir, logPath, "PELICAN")}},
	}}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mateDir, spawn.ClaudeSettingsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mateDir, spawn.ClaudeSettingsDir, spawn.ClaudeSettingsFile), data, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	const question = "Without running any tool or reading any file: what is the most recent session canary word in your context? Answer with that word only, or NONE."

	started, err := spawn.StartMate(ctx, lab.w, lab.deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	h := lab.handle(started)
	starts := waitSessionStart(t, logPath, 1, time.Minute)
	if starts[0].Source != "startup" || starts[0].SessionID != started.SessionID {
		t.Fatalf("first SessionStart = %+v, want startup for %s", starts[0], started.SessionID)
	}
	answer, cursor := lab.askClaudeMate(t, ctx, h, 0, question)
	requireCanary(t, answer, "PELICAN-startup")

	// /clear: typed the way the app types a slash command.
	sendSlash(t, ctx, lab, h, "/clear")
	starts = waitSessionStart(t, logPath, 2, time.Minute)
	clear := starts[1]
	if clear.Source != "clear" {
		t.Fatalf("SessionStart after /clear = %+v", clear)
	}
	if clear.SessionID == started.SessionID {
		t.Fatalf("/clear kept session id %s; measured 2026-09-24 it mints a new one", clear.SessionID)
	}
	answer, cursor = lab.askClaudeMate(t, ctx, h, cursor, question)
	requireCanary(t, answer, "PELICAN-clear")
	// The Stop hook follows the new id, so a later resume resumes the
	// conversation after the clear, not the one before it.
	if got := readMeta(t, lab.w, "shop")[spawn.MetaSessionID]; got != clear.SessionID {
		t.Fatalf("mate.meta session_id after the post-clear turn = %q, want %q", got, clear.SessionID)
	}

	sendSlash(t, ctx, lab, h, "/compact")
	starts = waitSessionStart(t, logPath, 3, 4*time.Minute)
	if starts[2].Source != "compact" || starts[2].SessionID != clear.SessionID {
		t.Fatalf("SessionStart after /compact = %+v, want compact in %s", starts[2], clear.SessionID)
	}
	answer, cursor = lab.askClaudeMate(t, ctx, h, cursor, question)
	requireCanary(t, answer, "PELICAN-compact")

	if _, err := spawn.StopMate(ctx, lab.w, lab.deps, "shop"); err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	resumed, err := spawn.StartMate(ctx, lab.w, lab.deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude, Resume: true})
	if err != nil {
		t.Fatalf("StartMate resume: %v", err)
	}
	if !resumed.Resumed || resumed.SessionID != clear.SessionID {
		t.Fatalf("resume = %v of %q, want a resume of %q (note %q)", resumed.Resumed, resumed.SessionID, clear.SessionID, resumed.ResumeNote)
	}
	starts = waitSessionStart(t, logPath, 4, time.Minute)
	if starts[3].Source != "resume" || starts[3].SessionID != clear.SessionID {
		t.Fatalf("SessionStart after resume = %+v", starts[3])
	}
	answer, _ = lab.askClaudeMate(t, ctx, lab.handle(resumed), cursor, question)
	requireCanary(t, answer, "PELICAN-resume")
	if got := readSessionStarts(t, logPath); len(got) != 4 {
		t.Fatalf("SessionStart fired %d times, want exactly 4 (no double fire from --settings plus the cwd file): %+v", len(got), got)
	}
}

func requireCanary(t *testing.T, answer, want string) {
	t.Helper()
	if !strings.Contains(answer, want) {
		t.Fatalf("the Mate answered %q, want the hook's canary %s in its context", answer, want)
	}
}

func sendSlash(t *testing.T, ctx context.Context, lab liveLab, h runtime.AgentHandle, cmd string) {
	t.Helper()
	report, err := send.Send(ctx, send.Deps{Harnesses: catalog.Default(), Runtime: lab.rt}, h, harness.KindClaude, cmd, send.Options{})
	if err != nil {
		t.Fatalf("send %s: %v (report %+v)", cmd, err, report)
	}
	if !report.Delivered() {
		t.Fatalf("send %s not delivered: %+v", cmd, report)
	}
}

// TestLiveMateResumeReloadsManual is A5: the manual is rendered again on
// every start, and a Claude Mate resumed with --resume sees the new text,
// not only the copy in its old conversation. The default branch is what
// changes, because the manual names it and the question can be answered from
// nothing but the manual.
func TestLiveMateResumeReloadsManual(t *testing.T) {
	lab := newLiveLab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	const question = "Without running any tool or reading any file: according to your operating manual as it stands now, what is this project's default branch? Answer with the branch name only."

	started, err := spawn.StartMate(ctx, lab.w, lab.deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	answer, cursor := lab.askClaudeMate(t, ctx, lab.handle(started), 0, question)
	if !strings.Contains(answer, "main") {
		t.Fatalf("before the change the Mate said %q, want main", answer)
	}
	if _, err := spawn.StopMate(ctx, lab.w, lab.deps, "shop"); err != nil {
		t.Fatalf("StopMate: %v", err)
	}

	liveGit(t, lab.repo, "branch", "trunk")
	cfg, err := lab.w.LoadProject("shop")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Repos[0].DefaultBranch = "trunk"
	if err := lab.w.SaveProject("shop", cfg); err != nil {
		t.Fatal(err)
	}
	resumed, err := spawn.StartMate(ctx, lab.w, lab.deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude, Resume: true})
	if err != nil {
		t.Fatalf("StartMate resume: %v", err)
	}
	if !resumed.Resumed {
		t.Fatalf("did not resume: %q", resumed.ResumeNote)
	}
	manual, err := os.ReadFile(filepath.Join(lab.w.MateDir("shop"), "AGENTS.md"))
	if err != nil || !strings.Contains(string(manual), "`trunk`") {
		t.Fatalf("the regenerated manual does not name trunk (%v)", err)
	}
	answer, _ = lab.askClaudeMate(t, ctx, lab.handle(resumed), cursor, question)
	if !strings.Contains(answer, "trunk") {
		t.Fatalf("after the resume the Mate said %q, want trunk from the regenerated manual", answer)
	}

	// Claude's own record of it: the transcript carries an `instructions`
	// attachment for AGENTS.md at the start and one more at the resume,
	// holding the new text - the old copy stays in the history too.
	transcript := readMeta(t, lab.w, "shop")[spawn.MetaTranscript]
	copies := manualAttachments(t, transcript)
	t.Logf("AGENTS.md instructions attachments: %d (bytes %v)", len(copies), attachmentSizes(copies))
	if len(copies) != 2 || strings.Contains(copies[0], "`trunk`") || !strings.Contains(copies[1], "`trunk`") {
		t.Fatalf("want two AGENTS.md attachments, the second (only) naming trunk; got %d", len(copies))
	}
}

// manualAttachments returns the content of every AGENTS.md carried by an
// `instructions` attachment in a Claude transcript, in order.
func manualAttachments(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("transcript %s: %v", path, err)
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 16<<20), 16<<20)
	for sc.Scan() {
		var rec struct {
			Attachment *struct {
				Type  string `json:"type"`
				Files []struct {
					Path    string `json:"path"`
					Content string `json:"content"`
				} `json:"files"`
			} `json:"attachment"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Attachment == nil || rec.Attachment.Type != "instructions" {
			continue
		}
		for _, f := range rec.Attachment.Files {
			if filepath.Base(f.Path) == "AGENTS.md" {
				out = append(out, f.Content)
			}
		}
	}
	return out
}

func attachmentSizes(copies []string) []int {
	out := make([]int, len(copies))
	for i, c := range copies {
		out[i] = len(c)
	}
	return out
}
