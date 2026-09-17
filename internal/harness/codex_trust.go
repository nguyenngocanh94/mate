package harness

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nguyenngocanh94/matev2/internal/observability"
)

// CodexTrustLevel is the recorded per-project decision in
// $CODEX_HOME/config.toml, as codex-cli 0.154.0 spells it.
type CodexTrustLevel string

const (
	// CodexTrustNone means no `projects` entry matched any lookup key: codex
	// will draw its directory-trust dialog on launch.
	CodexTrustNone CodexTrustLevel = ""
	// CodexTrustTrusted means the operator accepted the dialog once for this
	// repository (or its main checkout).
	CodexTrustTrusted CodexTrustLevel = "trusted"
	// CodexTrustUntrusted means the operator recorded an explicit no. Codex
	// draws no dialog and runs with project-local config disabled.
	CodexTrustUntrusted CodexTrustLevel = "untrusted"
)

// CodexTrustRequest names what a Codex agent is about to be launched into.
type CodexTrustRequest struct {
	// Cwd is the directory codex will start in. It need not exist yet: a
	// Crew's worktree is checked before `git worktree add` runs.
	Cwd string
	// RepoPath is the checkout Cwd belongs to, or will be added from. When
	// Cwd exists and is itself a checkout the two may be the same path.
	RepoPath string
	// CodexHome is the effective $CODEX_HOME (see EffectiveCodexHome).
	CodexHome string
}

// CodexTrustReport is what CheckCodexProjectTrust found. It is a reading of a
// file codex owns, taken before launch; the launch itself is still measured
// against the pane (ADR 0028), this only says what the file predicted.
type CodexTrustReport struct {
	ConfigPath string
	// LookupKeys are the locations codex would look up, in order: the cwd,
	// then the trust root. Each is compared to the file's keys as a
	// location (samePath), which covers codex's canonical-and-
	// as-spelled pair without a second canonicalisation site.
	LookupKeys []string
	// TrustRoot is the repository root codex would key the decision on -
	// a main checkout for a linked worktree. Empty when none resolves.
	TrustRoot string
	// TrustKey is the config key that matched, empty for CodexTrustNone.
	TrustKey string
	Level    CodexTrustLevel
}

// codexMaxGitMetadataBytes mirrors codex's MAX_GIT_METADATA_FILE_BYTES.
const codexMaxGitMetadataBytes = 64 * 1024

// CheckCodexProjectTrust reads $CODEX_HOME/config.toml and reports which
// `projects` decision codex-cli 0.154.0 would apply at the agent's cwd. The
// lookup mirrors codex's own (config/src/config_toml.rs get_active_project,
// git-utils/src/trust.rs resolve_root_git_project_for_trust, both read at tag
// rust-v0.154.0): the cwd first, then the repository root, where a linked
// worktree's root is its main checkout. No ancestor directory inherits; a
// trusted parent folder proves nothing for a child.
//
// This rides on vendor behaviour that is not a public contract. A missing
// config file is CodexTrustNone, not an error; a file that does not parse is
// an error, because "unreadable" must never be reported as "no decision".
func CheckCodexProjectTrust(req CodexTrustRequest) (CodexTrustReport, error) {
	cwd := filepath.Clean(strings.TrimSpace(req.Cwd))
	if cwd == "" || !filepath.IsAbs(cwd) {
		return CodexTrustReport{}, observability.WrapError(observability.CodeUsage, "codex trust check requires the absolute cwd codex will start in", ErrContextRequired)
	}
	repo := filepath.Clean(strings.TrimSpace(req.RepoPath))
	if repo == "" || !filepath.IsAbs(repo) {
		return CodexTrustReport{}, observability.WrapError(observability.CodeUsage, "codex trust check requires the absolute repository path", ErrContextRequired)
	}
	home := strings.TrimSpace(req.CodexHome)
	if home == "" || !filepath.IsAbs(home) {
		return CodexTrustReport{}, observability.WrapError(observability.CodeUsage, "CODEX_HOME must be absolute when set", ErrContextRequired)
	}
	report := CodexTrustReport{ConfigPath: filepath.Join(filepath.Clean(home), "config.toml")}

	report.LookupKeys = []string{cwd}
	root := ResolveCodexTrustRoot(cwd)
	if root == "" && !pathExists(cwd) {
		// The cwd does not exist yet (a worktree about to be added); the
		// repository it will be added from is what codex will resolve to.
		root = ResolveCodexTrustRoot(repo)
	}
	report.TrustRoot = root
	if root != "" && !samePath(root, cwd) {
		report.LookupKeys = append(report.LookupKeys, root)
	}

	levels, err := readCodexProjectTrust(report.ConfigPath)
	if err != nil {
		return CodexTrustReport{}, err
	}
	keys := make([]string, 0, len(levels))
	for key := range levels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, lookup := range report.LookupKeys {
		for _, key := range keys {
			if samePath(key, lookup) {
				report.TrustKey = key
				report.Level = levels[key]
				return report, nil
			}
		}
	}
	return report, nil
}

// pathExists is a plain presence probe - is there any entry at all - not a
// boundary resolution (internal/fsboundary owns those). It only picks which
// of two caller-supplied paths a git root is resolved from.
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

type codexProjectsToml struct {
	Projects map[string]struct {
		TrustLevel string `toml:"trust_level"`
	} `toml:"projects"`
}

// readCodexProjectTrust returns every `projects` entry carrying a trust_level.
// Codex writes `[projects."<path>"]` tables, but the inline-table and dotted
// spellings are the same TOML document, so this goes through a parser.
func readCodexProjectTrust(path string) (map[string]CodexTrustLevel, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]CodexTrustLevel{}, nil
	}
	if err != nil {
		return nil, observability.WrapError(observability.CodeStateConflict, "read codex config "+path, err)
	}
	var doc codexProjectsToml
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		return nil, observability.WrapError(observability.CodeStateConflict, fmt.Sprintf("codex config %s does not parse; refusing to guess its trust decisions", path), err)
	}
	out := make(map[string]CodexTrustLevel, len(doc.Projects))
	for key, project := range doc.Projects {
		switch level := strings.TrimSpace(project.TrustLevel); level {
		case string(CodexTrustTrusted):
			out[key] = CodexTrustTrusted
		case string(CodexTrustUntrusted):
			out[key] = CodexTrustUntrusted
		case "":
			// An entry with no trust_level carries no decision.
		default:
			return nil, observability.NewError(observability.CodeStateConflict,
				fmt.Sprintf("codex config %s records trust_level %q for %s, which this build does not know", path, level, key))
		}
	}
	return out, nil
}

// ResolveCodexTrustRoot is a filesystem-only port of codex 0.154.0's
// resolve_root_git_project_for_trust: walk up from cwd to the nearest .git;
// a .git directory (with a HEAD) is the root; a .git pointer file is followed
// into <main>/.git/worktrees/<name>, whose gitdir must name this checkout's
// .git and whose commondir must name that main .git, and then the main
// checkout is the root. Anything that does not prove that shape - a .git that
// is not a plain directory or regular file, an oversized metadata file, a
// pointer into someone else's admin directory - resolves to nothing, exactly
// as codex refuses it. Empty means codex would find no repository root and
// key trust on the cwd alone.
//
// Location identity is decided by samePath, the one symlink-aware
// comparison mate allows outside internal/fsboundary.
func ResolveCodexTrustRoot(cwd string) string {
	base := filepath.Clean(cwd)
	if info, err := os.Stat(base); err != nil || !info.IsDir() {
		base = filepath.Dir(base)
	}
	repoRoot, ok := codexRepoRoot(base)
	if !ok {
		return ""
	}
	dotGit := filepath.Join(repoRoot, ".git")
	info, err := os.Lstat(dotGit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return repoRoot
	}
	if !info.Mode().IsRegular() || info.Size() > codexMaxGitMetadataBytes {
		return ""
	}
	gitDir, ok := readGitdirPointer(dotGit)
	if !ok {
		return ""
	}
	gitDirInfo, err := os.Lstat(gitDir)
	if err != nil || !gitDirInfo.IsDir() {
		return ""
	}
	worktreesDir := filepath.Dir(gitDir)
	if filepath.Base(worktreesDir) != "worktrees" {
		return ""
	}
	commonDir := filepath.Dir(worktreesDir)
	registered, ok := readMetadataFile(filepath.Join(gitDir, "gitdir"))
	if !ok || filepath.Base(registered) != ".git" {
		return ""
	}
	commondirRel, ok := readMetadataFile(filepath.Join(gitDir, "commondir"))
	if !ok {
		return ""
	}
	// Compare checkout directories, not the .git entries, and require the
	// admin directory's commondir to land on the common directory the
	// pointer implied.
	registeredCheckout := filepath.Dir(resolveAgainst(gitDir, registered))
	if !pathExists(registeredCheckout) || !samePath(registeredCheckout, repoRoot) {
		return ""
	}
	linkedCommon := resolveAgainst(gitDir, commondirRel)
	if !pathExists(linkedCommon) || !samePath(linkedCommon, commonDir) {
		return ""
	}
	// Keep codex's trust key - the main root as the pointer spells it - and
	// prove that root's own .git is the common directory.
	mainRoot := filepath.Dir(commonDir)
	mainDotGit := filepath.Join(mainRoot, ".git")
	mainInfo, err := os.Lstat(mainDotGit)
	if err != nil {
		return ""
	}
	mainGitDir := mainDotGit
	if !mainInfo.IsDir() {
		if !mainInfo.Mode().IsRegular() {
			return ""
		}
		mainGitDir, ok = readGitdirPointer(mainDotGit)
		if !ok {
			return ""
		}
	}
	if !pathExists(mainGitDir) || !samePath(mainGitDir, commonDir) {
		return ""
	}
	return mainRoot
}

// codexRepoRoot walks up from dir to the nearest ancestor whose .git entry is
// either a non-directory (a pointer file) or a directory holding HEAD, the
// way codex's loop skips a bare `.git/` directory with nothing in it.
func codexRepoRoot(dir string) (string, bool) {
	for {
		candidate, ok := nearestDotGit(dir)
		if !ok {
			return "", false
		}
		dotGit := filepath.Join(candidate, ".git")
		info, err := os.Lstat(dotGit)
		if err != nil {
			return "", false
		}
		if !info.IsDir() {
			return candidate, true
		}
		if _, err := os.Stat(filepath.Join(dotGit, "HEAD")); err == nil {
			return candidate, true
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", false
		}
		dir = parent
	}
}

// nearestDotGit finds the closest ancestor of dir (inclusive) holding a .git
// entry of any kind.
func nearestDotGit(dir string) (string, bool) {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func readGitdirPointer(dotGit string) (string, bool) {
	contents, ok := readMetadataFile(dotGit)
	if !ok {
		return "", false
	}
	target, found := strings.CutPrefix(contents, "gitdir:")
	if !found {
		return "", false
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}
	return resolveAgainst(filepath.Dir(dotGit), target), true
}

// readMetadataFile reads a small regular file (a symlink is not regular under
// Lstat and is refused) and returns its trimmed contents.
func readMetadataFile(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > codexMaxGitMetadataBytes {
		return "", false
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > codexMaxGitMetadataBytes {
		return "", false
	}
	return string(bytes.TrimSpace(raw)), true
}

func resolveAgainst(base, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(base, target))
}
