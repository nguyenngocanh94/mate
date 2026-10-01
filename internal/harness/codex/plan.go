package codex

import (
	"fmt"
	"path/filepath"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
)

// CodexOverridePlan is what one cwd's Mate-written AGENTS.override.md has to
// fit into, computed before the file is written. It exists because Codex
// 0.151.0 fails open: an override that overflows project_doc_max_bytes is
// silently truncated and the agent starts anyway (ADR 0004). Composing the
// override first and discovering afterwards that it does not fit would leave
// a truncated file on disk at the agent's cwd; the plan lets the caller size
// the content, or refuse, before anything is written.
//
// The arithmetic is the one ADR 0004 probed live against codex-cli 0.151.0
// (re-checked on 0.152.1): project_doc_max_bytes meters only the raw content
// bytes of project files, summed in load order. The global $CODEX_HOME
// document, the `--- project-doc ---` marker and the per-file `\n\n` joiners
// are rendered afterwards and are exempt, so none of them appears in Budget.
type CodexOverridePlan struct {
	// Cwd is the absolute directory the agent process will run in.
	Cwd string
	// Path is where the override must be written: <cwd>/AGENTS.override.md.
	Path string
	// Global is the $CODEX_HOME document, reported so a caller can see what
	// else the agent will read. It is never charged against Budget.
	Global *CodexFile
	// Others are the metered project files strictly above cwd, from the git
	// root down. Codex renders them before the override, and their raw bytes
	// are charged against the same cap, so they reduce Budget.
	Others []CodexFile
	// Shadowed is the project document that would load at cwd if Mate wrote
	// no override there - in the Phase 1 Crew layout, the worktree's own
	// tracked AGENTS.md. Codex drops it entirely once the override exists
	// (ADR 0004, "Crew layout"), so a caller that wants the repository's
	// instructions to reach the agent has to embed these bytes in the
	// override it composes, inside Budget. Nil when there is none.
	Shadowed *CodexFile
	// MaxBytes is the effective project_doc_max_bytes.
	MaxBytes int
	// Budget is the exact number of raw content bytes the override may have.
	// It can be zero or negative: files above cwd can fill the cap on their
	// own, and then no override fits at all.
	Budget int
	// GitRoot is the directory whose .git marker stopped discovery, empty
	// when there is none.
	GitRoot string
	// NoGit reports that no .git marker was found, so Codex reads only cwd.
	NoGit bool
}

// PlanCodexOverride reports what the override at req.Cwd may contain. It
// reads the filesystem the way Codex 0.151.0 does and writes nothing.
//
// An override already at cwd is deliberately not charged: it is the previous
// start's file, which this launch replaces. Charging it would refuse a
// launch that fits, and ADR 0004 requires the override to be regenerated on
// every start anyway.
func PlanCodexOverride(req DiscoverRequest, maxBytes int) (CodexOverridePlan, error) {
	if maxBytes <= 0 {
		maxBytes = CodexDefaultMaxBytes
	}
	chain, err := DiscoverCodexChain(req)
	if err != nil {
		return CodexOverridePlan{}, err
	}
	cwd := filepath.Clean(req.Cwd)
	plan := CodexOverridePlan{
		Cwd:      cwd,
		Path:     CodexInstructionPath(cwd),
		Global:   chain.Global,
		MaxBytes: maxBytes,
		GitRoot:  chain.GitRoot,
		NoGit:    chain.NoGit,
	}
	used := 0
	for _, f := range chain.Project {
		// Everything Codex loads at cwd is replaced by the override this
		// plan is for: the stale override itself, or the tracked document
		// the override will shadow. Only strict ancestors survive alongside
		// it, so only they are charged.
		if filepath.Dir(filepath.Clean(f.Path)) == cwd {
			continue
		}
		plan.Others = append(plan.Others, f)
		used += len(f.Bytes)
	}
	shadowed, ok, err := pickDirDocExcludingOverride(cwd, req.FallbackFilenames)
	if err != nil {
		return CodexOverridePlan{}, err
	}
	if ok {
		plan.Shadowed = &shadowed
	}
	plan.Budget = maxBytes - used
	return plan, nil
}

// Fits reports whether content may be written to Path. An empty override is
// refused for the same reason a missing one is: Codex skips it without
// complaint and the agent starts with no Mate contract at all.
func (p CodexOverridePlan) Fits(content []byte) error {
	if len(content) == 0 {
		return observability.WrapError(
			observability.CodeUsage,
			fmt.Sprintf("refusing to write an empty %s: Codex skips an empty project file and would start the agent with no Mate context", p.Path),
			harness.ErrContextRequired,
		)
	}
	if p.Budget <= 0 {
		return observability.WrapError(
			observability.CodeUsage,
			fmt.Sprintf("no room for %s: the %d project instruction bytes Codex already loads above %s fill project_doc_max_bytes (%d) on their own, so any Mate context would be silently truncated away",
				p.Path, p.MaxBytes-p.Budget, p.Cwd, p.MaxBytes),
			harness.ErrContextTooLarge,
		)
	}
	if len(content) > p.Budget {
		return observability.WrapError(
			observability.CodeUsage,
			fmt.Sprintf("%s would be %d bytes but only %d fit: project_doc_max_bytes is %d and Codex already meters %d bytes of project instructions above %s, so Codex would silently truncate the tail instead of failing",
				p.Path, len(content), p.Budget, p.MaxBytes, p.MaxBytes-p.Budget, p.Cwd),
			harness.ErrContextTooLarge,
		)
	}
	return nil
}

// pickDirDocExcludingOverride is pickDirDoc with AGENTS.override.md removed
// from the candidate list: it answers "what would Codex load at this
// directory if Mate had not written an override here", which is exactly the
// document the override displaces.
func pickDirDocExcludingOverride(dir string, fallback []string) (CodexFile, bool, error) {
	return pickDirDocFrom(dirDocCandidates(dir, fallback)[1:])
}
