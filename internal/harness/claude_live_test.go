package harness

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLiveClaudeProjectSlugResolution measures how a real claude-code CLI names
// its project directory instead of trusting a rule inferred from a transcript
// corpus. Opt in with MATE_LIVE=1: it runs the CLI twice with a one-word
// prompt, which needs an authenticated installation and writes two transcript
// files into the user's real Claude config directory. Both files, and the
// project directory if this test created it, are removed again on the way out.
// Nothing from the session is copied into the repository.
//
// It establishes on the machine that runs it:
//
//   - the project directory is the slug of the filesystem-resolved cwd, not of
//     the spelling the process was given and not of $PWD: the second run starts
//     in a symlinked spelling of the same directory with PWD pointing at the
//     symlink, and still lands in the resolved path's project;
//   - the transcript's own recorded cwd is that same resolved path, which is
//     what a caller that must validate a candidate file has to compare against;
//   - the directory name itself, so the encoding rule (UTF-16 code units,
//     non-injective) is checked against a real directory rather than a fixture
//   - the fixture cases for the encoding live in TestClaudeProjectSlug.
func TestLiveClaudeProjectSlugResolution(t *testing.T) {
	requireLive(t)
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("MATE_LIVE=1 but no claude CLI on PATH: %v", err)
	}
	projectsDir := claudeProjectsDir(t)

	// One name holding every character class the rule treats separately: a dot
	// and a dash (both slug to '-'), a space, and an astral character (two
	// UTF-16 code units, so two dashes).
	base := t.TempDir()
	phys := filepath.Join(base, "probe.dot-dash-\U0001F600 x")
	if err := os.MkdirAll(phys, 0o755); err != nil {
		t.Fatalf("create %s: %v", phys, err)
	}
	resolved, err := filepath.EvalSymlinks(phys)
	if err != nil {
		t.Fatalf("resolve %s: %v", phys, err)
	}
	link := filepath.Join(base, "link-spelling")
	if err := os.Symlink(phys, link); err != nil {
		t.Fatalf("symlink %s: %v", link, err)
	}
	if ClaudeProjectSlug(resolved) == ClaudeProjectSlug(phys) {
		t.Skipf("%s resolves to no divergent spelling on this filesystem, so it cannot show which one is slugged", phys)
	}
	wantSlug := ClaudeProjectSlug(resolved)
	wantDir := filepath.Join(projectsDir, wantSlug)
	_, statErr := os.Stat(wantDir)
	dirExisted := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		t.Fatalf("stat %s: %v", wantDir, statErr)
	}
	sessionResolved, sessionSymlink := newClaudeSessionID(t), newClaudeSessionID(t)

	// Run one: the resolved spelling as the process cwd, with PWD pointing
	// somewhere else entirely.
	runClaudeProbe(t, claudePath, resolved, "/", sessionResolved)
	// Run two: a symlink to the same directory, with PWD naming the symlink.
	runClaudeProbe(t, claudePath, link, link, sessionSymlink)

	for _, tc := range []struct{ name, session string }{
		{"resolved cwd with a divergent PWD", sessionResolved},
		{"symlink spelling", sessionSymlink},
	} {
		path := waitForClaudeTranscript(t, projectsDir, tc.session)
		dir := filepath.Dir(path)
		if dir != wantDir {
			t.Errorf("%s: transcript landed in %s, want %s", tc.name, dir, wantDir)
		}
		cwd, ok, err := firstTranscriptCwd(path)
		if err != nil {
			t.Fatalf("%s: read %s: %v", tc.name, path, err)
		}
		if !ok || cwd != resolved {
			t.Errorf("%s: transcript cwd = %q (found %v), want the resolved path %q", tc.name, cwd, ok, resolved)
		}
		t.Logf("%s: session %s -> %s (recorded cwd %s)", tc.name, tc.session, dir, cwd)
	}
	t.Logf("claude %s slug %s as %s, from the given spelling %s",
		claudeVersion(t, claudePath), resolved, wantSlug, phys)

	// The CLI writes <session>.jsonl plus a per-session directory and, for a
	// project directory it had to create, its own project state. Only the two
	// session ids this test generated are touched, and the project directory
	// only when this test created it.
	t.Cleanup(func() {
		for _, session := range []string{sessionResolved, sessionSymlink} {
			for _, path := range []string{
				filepath.Join(wantDir, session+".jsonl"),
				filepath.Join(wantDir, session),
			} {
				if err := os.RemoveAll(path); err != nil {
					t.Errorf("cleanup: remove %s: %v", path, err)
				}
			}
		}
		if !dirExisted {
			if err := os.RemoveAll(wantDir); err != nil {
				t.Logf("cleanup: left %s in place: %v", wantDir, err)
			}
		}
	})
}

// TestLiveClaudeProjectSlugOfALongCwd measures the CLI's rule for a cwd whose
// slug passes 200 characters: cut, then '-' and a hash of the whole cwd. The
// rule was read out of the CLI's bundle on 2026-10-01 after a long TMPDIR put
// a fixture's slug past the filesystem's name limit. Opt in with MATE_LIVE=1;
// it runs the CLI once and removes what it wrote.
func TestLiveClaudeProjectSlugOfALongCwd(t *testing.T) {
	requireLive(t)
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("MATE_LIVE=1 but no claude CLI on PATH: %v", err)
	}
	projectsDir := claudeProjectsDir(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(base, strings.Repeat("a", 80), strings.Repeat("b", 80), "slug.\U0001F600probe")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("create %s: %v", deep, err)
	}
	wantSlug := ClaudeProjectSlug(deep)
	if len(wantSlug) <= claudeSlugMax {
		t.Fatalf("slug %q is not past the cut; lengthen the fixture", wantSlug)
	}
	wantDir := filepath.Join(projectsDir, wantSlug)
	session := newClaudeSessionID(t)
	// The project directory is this test's own: its name carries the random
	// TempDir name, so nothing else can have run there. The CLI also leaves
	// project state (memory/) in it, so the whole directory goes.
	t.Cleanup(func() {
		if err := os.RemoveAll(wantDir); err != nil {
			t.Errorf("cleanup: remove %s: %v", wantDir, err)
		}
	})
	runClaudeProbe(t, claudePath, deep, deep, session)
	path := waitForClaudeTranscript(t, projectsDir, session)
	if dir := filepath.Dir(path); dir != wantDir {
		t.Fatalf("transcript landed in %s, want %s", dir, wantDir)
	}
	t.Logf("claude %s slugs a %d-byte cwd as %s", claudeVersion(t, claudePath), len(deep), wantSlug)
}

// claudeProjectsDir resolves the directory the CLI writes its transcripts
// under, honouring the same override the CLI does.
func claudeProjectsDir(t *testing.T) string {
	t.Helper()
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("locate the home directory: %v", err)
		}
		configDir = filepath.Join(home, ".claude")
	}
	projects := filepath.Join(configDir, "projects")
	if info, err := os.Stat(projects); err != nil || !info.IsDir() {
		t.Fatalf("%s is not an existing directory: run claude once, or set CLAUDE_CONFIG_DIR", projects)
	}
	return projects
}

// runClaudeProbe runs one one-word prompt in dir with pwd as $PWD. A failure is
// reported with the CLI's own output: an unauthenticated or unavailable
// installation must say so rather than look like a rule mismatch.
func runClaudeProbe(t *testing.T, claudePath, dir, pwd, session string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, claudePath, "-p", "--session-id", session, "reply with the single word ok")
	cmd.Dir = dir
	cmd.Env = append(envWithout(os.Environ(), "PWD"), "PWD="+pwd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude -p in %s (PWD=%s) failed (%v, ctx %v):\n%s", dir, pwd, err, ctx.Err(), out)
	}
	t.Logf("claude -p in %s (PWD=%s) exited 0: %s", dir, pwd, firstOutputLine(string(out)))
}

// waitForClaudeTranscript finds where the CLI actually put a session's
// transcript: the search is an observation across every project directory, so a
// rule this test got wrong shows up as a mismatch rather than as a false pass.
func waitForClaudeTranscript(t *testing.T, projectsDir, session string) string {
	t.Helper()
	pattern := filepath.Join(projectsDir, "*", session+".jsonl")
	deadline := time.Now().Add(15 * time.Second)
	for {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		if len(matches) == 1 {
			return matches[0]
		}
		if len(matches) > 1 {
			t.Fatalf("session %s exists in %d project directories: %v", session, len(matches), matches)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no transcript %s was written under %s within 15s", session, projectsDir)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func claudeVersion(t *testing.T, claudePath string) string {
	t.Helper()
	out, err := exec.Command(claudePath, "--version").Output()
	if err != nil {
		return "(version unknown)"
	}
	return firstOutputLine(string(out))
}

// newClaudeSessionID returns a random UUIDv4: --session-id requires one.
func newClaudeSessionID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		t.Fatalf("random session id: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// envWithout drops the named variables from an environment slice, so a probe
// can set them deliberately rather than inherit them.
func envWithout(env []string, names ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, want := range names {
			if name == want {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// firstOutputLine bounds CLI output for a log line; a model reply is arbitrary
// text and must not become an unbounded expectation.
func firstOutputLine(out string) string {
	if line, _, found := strings.Cut(strings.TrimSpace(out), "\n"); found {
		out = line
	}
	out = strings.TrimSpace(out)
	if len(out) > 120 {
		out = out[:120] + "…"
	}
	return out
}
