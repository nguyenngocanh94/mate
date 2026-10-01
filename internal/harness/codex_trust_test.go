package harness

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// resolvedTempDir is t.TempDir with symlinks resolved, so a fixture's spelled
// path is also its canonical one (macOS /var -> /private/var).
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// mkGitDir makes <repo>/.git a repository the way codex recognises one: a
// directory holding HEAD. A bare .git directory is skipped by codex's walk.
func mkGitDir(t *testing.T, repo string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCodexConfig(t *testing.T, home, body string) string {
	t.Helper()
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func tomlKey(path string) string {
	return "\"" + strings.ReplaceAll(path, "\\", "\\\\") + "\""
}

// A repository root is trusted when its own path is a `projects` entry - the
// entry codex writes on "Yes, continue".
func TestCodexTrustTrustedByRepositoryRoot(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := resolvedTempDir(t)
	mkGitDir(t, repo)
	cfg := writeCodexConfig(t, home, "model = \"x\"\n\n[projects."+tomlKey(repo)+"]\ntrust_level = \"trusted\"\n")
	got, err := CheckCodexProjectTrust(CodexTrustRequest{Cwd: filepath.Join(repo, "sub", "dir"), RepoPath: repo, CodexHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if got.Level != CodexTrustTrusted || got.TrustKey != repo || got.ConfigPath != cfg || got.TrustRoot != repo {
		t.Fatalf("report = %+v", got)
	}
}

// codex 0.154.0 resolves a linked worktree to its main checkout before the
// lookup (git-utils/src/trust.rs, resolve_root_git_project_for_trust): the
// worktree's .git file names <main>/.git/worktrees/<name>, whose gitdir and
// commondir files must point back. The fixture below is built the way codex's
// own worktree_trust test builds it, so no git binary is needed.
func TestCodexTrustLinkedWorktreeInheritsTheMainCheckout(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	base := resolvedTempDir(t)
	main := filepath.Join(base, "main")
	wt := filepath.Join(base, "wt")
	admin := filepath.Join(main, ".git", "worktrees", "wt")
	for _, d := range []string{admin, wt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(main, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+admin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(admin, "gitdir"), []byte(filepath.Join(wt, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(admin, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCodexConfig(t, home, "[projects."+tomlKey(main)+"]\ntrust_level = \"trusted\"\n")

	// The worktree exists: codex would start there and resolve to main.
	got, err := CheckCodexProjectTrust(CodexTrustRequest{Cwd: wt, RepoPath: wt, CodexHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if got.Level != CodexTrustTrusted || got.TrustKey != main || got.TrustRoot != main {
		t.Fatalf("existing worktree report = %+v", got)
	}

	// Before `git worktree add` has run, the Crew's cwd does not exist yet;
	// the repository the worktree will be added from stands in for it.
	planned := filepath.Join(base, "not-yet", "crew")
	got, err = CheckCodexProjectTrust(CodexTrustRequest{Cwd: planned, RepoPath: main, CodexHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if got.Level != CodexTrustTrusted || got.TrustKey != main {
		t.Fatalf("planned worktree report = %+v", got)
	}

	// A forged pointer - the admin dir's gitdir names a different checkout -
	// resolves to nothing, exactly as codex refuses it.
	forged := filepath.Join(base, "forged")
	if err := os.MkdirAll(forged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(forged, ".git"), []byte("gitdir: "+admin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = CheckCodexProjectTrust(CodexTrustRequest{Cwd: forged, RepoPath: forged, CodexHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if got.Level != CodexTrustNone || got.TrustRoot != "" {
		t.Fatalf("forged worktree report = %+v, want no trust root", got)
	}
}

// A Crew's worktree does not exist before `git worktree add`, and the
// workspace it will be added under may itself be a checkout. Walking up from
// the missing path stopped at that enclosing checkout and reported its trust
// instead of the project repository's (found 2026-10-01 with TMPDIR inside a
// git checkout).
func TestCodexTrustPlannedWorktreeIgnoresAnEnclosingCheckout(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	workspace := resolvedTempDir(t)
	mkGitDir(t, workspace)
	repo := filepath.Join(workspace, "shop")
	mkGitDir(t, repo)
	writeCodexConfig(t, home, "[projects."+tomlKey(workspace)+"]\ntrust_level = \"untrusted\"\n"+
		"[projects."+tomlKey(repo)+"]\ntrust_level = \"trusted\"\n")

	planned := filepath.Join(workspace, ".worktrees", "shop-crew")
	got, err := CheckCodexProjectTrust(CodexTrustRequest{Cwd: planned, RepoPath: repo, CodexHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if got.TrustRoot != repo || got.Level != CodexTrustTrusted || got.TrustKey != repo {
		t.Fatalf("planned worktree report = %+v, want the project repository %s", got, repo)
	}
}

func TestCodexTrustReportsUntrustedNoneAndMissingConfig(t *testing.T) {
	t.Parallel()
	repo := resolvedTempDir(t)
	mkGitDir(t, repo)
	untrusted := t.TempDir()
	writeCodexConfig(t, untrusted, "[projects."+tomlKey(repo)+"]\ntrust_level = \"untrusted\"\n")
	got, err := CheckCodexProjectTrust(CodexTrustRequest{Cwd: repo, RepoPath: repo, CodexHome: untrusted})
	if err != nil || got.Level != CodexTrustUntrusted || got.TrustKey != repo {
		t.Fatalf("untrusted report = %+v err=%v", got, err)
	}

	other := t.TempDir()
	writeCodexConfig(t, other, "[projects.\"/somewhere/else\"]\ntrust_level = \"trusted\"\n")
	got, err = CheckCodexProjectTrust(CodexTrustRequest{Cwd: repo, RepoPath: repo, CodexHome: other})
	if err != nil || got.Level != CodexTrustNone || got.TrustKey != "" {
		t.Fatalf("no-entry report = %+v err=%v", got, err)
	}
	if len(got.LookupKeys) == 0 || got.LookupKeys[0] != repo {
		t.Fatalf("lookup keys must name what codex would look up: %+v", got.LookupKeys)
	}

	empty := t.TempDir()
	got, err = CheckCodexProjectTrust(CodexTrustRequest{Cwd: repo, RepoPath: repo, CodexHome: empty})
	if err != nil || got.Level != CodexTrustNone || got.ConfigPath != filepath.Join(empty, "config.toml") {
		t.Fatalf("missing config report = %+v err=%v", got, err)
	}
}

// The inline-table spelling is legal TOML for the same entry; a hand-rolled
// line matcher would miss it, which is why this goes through a parser.
func TestCodexTrustReadsInlineTableProjects(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := resolvedTempDir(t)
	mkGitDir(t, repo)
	writeCodexConfig(t, home, "[projects]\n"+tomlKey(repo)+" = { trust_level = \"trusted\" }\n")
	got, err := CheckCodexProjectTrust(CodexTrustRequest{Cwd: repo, RepoPath: repo, CodexHome: home})
	if err != nil || got.Level != CodexTrustTrusted {
		t.Fatalf("inline table report = %+v err=%v", got, err)
	}
}

func TestCodexTrustRefusesMalformedConfigAndRelativeHome(t *testing.T) {
	t.Parallel()
	repo := resolvedTempDir(t)
	home := t.TempDir()
	writeCodexConfig(t, home, "[projects\nbroken = \n")
	_, err := CheckCodexProjectTrust(CodexTrustRequest{Cwd: repo, RepoPath: repo, CodexHome: home})
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeStateConflict {
		t.Fatalf("malformed config err = %v, want state_conflict (never a silent 'no trust')", err)
	}
	_, err = CheckCodexProjectTrust(CodexTrustRequest{Cwd: repo, RepoPath: repo, CodexHome: "relative/home"})
	if err == nil {
		t.Fatal("relative CODEX_HOME must be refused")
	}
	_, err = CheckCodexProjectTrust(CodexTrustRequest{Cwd: "relative", RepoPath: repo, CodexHome: home})
	if err == nil {
		t.Fatal("relative cwd must be refused")
	}
}

// A symlinked spelling of a trusted path is still trusted: codex looks up both
// the canonical and the as-spelled key.
func TestCodexTrustMatchesTheCanonicalSpelling(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	base := resolvedTempDir(t)
	real := filepath.Join(base, "real")
	mkGitDir(t, real)
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	writeCodexConfig(t, home, "[projects."+tomlKey(real)+"]\ntrust_level = \"trusted\"\n")
	got, err := CheckCodexProjectTrust(CodexTrustRequest{Cwd: link, RepoPath: link, CodexHome: home})
	if err != nil || got.Level != CodexTrustTrusted || got.TrustKey != real {
		t.Fatalf("symlinked spelling report = %+v err=%v", got, err)
	}
}
