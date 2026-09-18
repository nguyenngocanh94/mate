package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/matev2/internal/config"
	"github.com/nguyenngocanh94/matev2/internal/observability"
)

// Codex is the HarnessAdapter for Codex CLI 0.151.0. Generated context is
// written by later gates to <cwd>/AGENTS.override.md (not tracked AGENTS.md).
// BuildLaunchSpec binds that cwd and refuses to start when the effective
// instruction chain is missing the required file, empty, or would be
// silently truncated at project_doc_max_bytes.
type Codex struct {
	MaxChainBytes int
	LookPath      func(name string) (string, error) // unused in G2; reserved for G4 executable probe
}

// Validate reports Codex capabilities. Config.toml fallback filenames are
// not read here (unproven as a G2 file-parse); they are an explicit Config
// field so callers cannot silently assume TEAM_GUIDE.md.
func (c Codex) Validate(_ context.Context, cfg Config) (CapabilitySet, error) {
	if cfg.Kind != "" && cfg.Kind != KindCodex {
		return CapabilitySet{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("codex adapter got kind %q", cfg.Kind))
	}
	max := c.maxBytes(cfg)
	return CapabilitySet{
		Kind:               KindCodex,
		Deliveries:         []Delivery{DeliveryInstructionFile},
		DefaultDelivery:    DeliveryInstructionFile,
		ProjectDocMaxBytes: max,
		GracefulStop: GracefulStop{
			Available: false,
			Method:    "",
			ProvenOn:  "",
		},
		Assumptions: []Assumption{
			{
				ID:      AssumptionCodexConfigFallbacks,
				Status:  "unproven",
				Summary: "G2 does not parse ~/.codex/config.toml. Fallback filenames must be passed on Config; TEAM_GUIDE.md is not assumed.",
			},
			{
				ID:      "codex_graceful_stop",
				Status:  "unproven",
				Summary: "G1 proved Claude /exit; Codex graceful stop was not measured. StopAgent must not hardcode send-keys.",
			},
		},
	}, nil
}

func (c Codex) maxBytes(cfg Config) int {
	if cfg.ProjectDocMaxBytes > 0 {
		return cfg.ProjectDocMaxBytes
	}
	if c.MaxChainBytes > 0 {
		return c.MaxChainBytes
	}
	return CodexDefaultMaxBytes
}

// BuildLaunchSpec constructs a startable Codex spec or returns an error.
func (c Codex) BuildLaunchSpec(_ context.Context, spec AgentSpec) (LaunchSpec, error) {
	if spec.Kind != "" && spec.Kind != KindCodex {
		return LaunchSpec{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("codex adapter got kind %q", spec.Kind))
	}
	if spec.ResumeSessionID != "" {
		// codex-cli 0.154.0's `resume` subcommand is documented as "picker
		// by default": mate has no proven, non-interactive way to drive it
		// through Herdr the way Claude's --resume flag works, so a resume
		// request for Codex is refused rather than silently guessed at.
		return LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			"resume not supported for codex: `codex resume` opens an interactive session picker", ErrResumeUnsupported)
	}
	cwd := spec.Cwd
	if cwd == "" || !filepath.IsAbs(cwd) {
		return LaunchSpec{}, observability.WrapError(observability.CodeUsage, "codex launch requires the absolute cwd the agent will run in", ErrContextRequired)
	}
	want := CodexInstructionPath(cwd)
	path := spec.ContextPath
	if path == "" {
		path = want
	}
	if filepath.Clean(path) != filepath.Clean(want) {
		return LaunchSpec{}, observability.WrapError(
			observability.CodeUsage,
			fmt.Sprintf("codex context path must be %s (got %s); tracked AGENTS.md is not the Mate discovery file", want, path),
			ErrContextRequired,
		)
	}
	max := c.maxBytes(spec.Config)
	codexHome, err := EffectiveCodexHome(spec.Config.CodexHome)
	if err != nil {
		return LaunchSpec{}, observability.WrapError(observability.CodeUsage, "codex home", err)
	}
	chain, err := DiscoverCodexChain(DiscoverRequest{
		Cwd:               cwd,
		CodexHome:         codexHome,
		FallbackFilenames: spec.Config.FallbackFilenames,
	})
	if err != nil {
		return LaunchSpec{}, err
	}
	if err := chain.RefuseIfRequiredMissingOrTruncated(want, max); err != nil {
		return LaunchSpec{}, err
	}
	envInput := append([]EnvVar(nil), spec.Env...)
	envInput = append(envInput, EnvVar{Key: config.EnvCodexHome, Value: codexHome})
	env, err := filterLaunchEnv(envInput)
	if err != nil {
		return LaunchSpec{}, err
	}
	out := LaunchSpec{
		kind:            string(KindCodex),
		args:            []string{"--dangerously-bypass-approvals-and-sandbox", "-c", CodexDisableUpdateCheck},
		cwd:             cwd,
		env:             env,
		contextFiles:    []GeneratedFile{{Path: want, Role: "codex_override"}},
		delivery:        DeliveryInstructionFile,
		contextPath:     want,
		contextRequired: true,
		maxFileBytes:    max,
		taskPrompt:      spec.TaskPrompt,
		model:           spec.Model,
		notes: []string{
			"the captain ruled on 2026-09-14 that Mate and Crew launches carry the harness permission bypass; there is no external sandbox of any kind, because a Crew runs on the operator's own machine on real project code",
			"the captain first chose the narrower --ask-for-approval never --sandbox workspace-write pair, then reversed that ruling the same day: a linked Crew worktree's .git is a pointer file into the primary repo's git dir, outside workspace-write's writable root, so a Codex Crew under the narrow pair could do work but could never git commit it (measured both directions) - and mate never commits on a Crew's behalf, it only fast-forwards the Crew branch, so uncommitted work can never be delivered",
			"this is not merely a prompt bypass: under the combined flag a Codex Crew can write outside its own worktree - the workspace database (.matev2/mate.db), other Crews' worktrees, and the operator's home directory are all reachable; the captain accepted that cost knowingly to keep Crews able to commit",
			"this puts gomate in the same posture firstmate's own tooling already runs Codex in",
			"this does not clear Codex's per-absolute-path directory-trust confirmation; that dialog is a separate wall no flag removes, measured 2026-09-14",
			"the launch also carries -c check_for_update_on_startup=false: codex-cli draws a three-option release-update prompt ahead of everything else while a newer version is published, which blocks the composer (measured 2026-09-18 with 0.154.0 installed and 0.155.0 out); the key is documented and the override is per-launch, so the operator's own config.toml is never written",
			"the bypass is unconditional: there is no config key or CLI flag to opt out, a deliberate choice consistent with unattended Crew/Mate operation",
			"Codex 0.151.0 loads AGENTS.override.md in preference to AGENTS.md and silently truncates the project chain's content bytes at project_doc_max_bytes (global doc, marker and joiners are rendered unmetered)",
			"missing or empty project files do not fail Codex; mate refuses to start instead",
		},
		codexHome:     codexHome,
		codexFallback: append([]string(nil), spec.Config.FallbackFilenames...),
	}
	return finalize(out)
}

// CodexDisableUpdateCheck is the `-c key=value` override that turns off
// codex-cli's startup release check for one launch.
// `check_for_update_on_startup` is a documented boolean in the Codex
// configuration reference
// ("Check for Codex updates on startup (set to false only when updates are
// centrally managed)"), and codex-cli 0.154.0 accepts it under
// --strict-config, which errors on keys this build does not know.
//
// Without it a launch made while a newer release is published stops on a
// three-option update prompt before the directory-trust dialog, and a Crew
// spawn never reaches the composer (measured 2026-09-18, 0.154.0 installed
// with 0.155.0 published). It is passed on the command line rather than
// written into the operator's config.toml: mate never edits files codex
// owns, and the operator keeps their own update notices everywhere else.
// The prompt is still recognised and answered by the startup settle, because
// a flag is a prediction and the pane is the measurement.
const CodexDisableUpdateCheck = "check_for_update_on_startup=false"

// DiscoverRequest is the cwd-relative view Codex will use at start.
type DiscoverRequest struct {
	Cwd               string
	CodexHome         string
	FallbackFilenames []string
}

// CodexFile is one instruction file in load order.
type CodexFile struct {
	Path  string
	Bytes []byte
}

// CodexProjectDocMarker is the exact byte sequence codex-cli 0.151.0 renders
// between the global $CODEX_HOME document and the project chain (live probe,
// ADR 0004). It is rendered, not metered: project_doc_max_bytes never
// charges it.
const CodexProjectDocMarker = "\n\n--- project-doc ---\n\n"

// CodexChainJoiner is the exact joiner codex-cli 0.151.0 renders between
// concatenated project files (live probe, ADR 0004). Rendered, not metered.
const CodexChainJoiner = "\n\n"

// CodexChain is the effective instruction chain codex-cli 0.151.0 would load.
// The global document is structurally separate from the project chain because
// the two are budgeted differently: project_doc_max_bytes meters only the
// project files (live probe, ADR 0004), so a global byte cannot be charged
// by construction.
type CodexChain struct {
	// Global is the $CODEX_HOME document (override-else-base), rendered
	// before the project chain and never metered: a 1-byte
	// project_doc_max_bytes still delivers it in full.
	Global *CodexFile
	// Project is the git-root-down-to-cwd chain in load order. Only these
	// files' raw content bytes are metered by project_doc_max_bytes.
	Project []CodexFile
	GitRoot string
	NoGit   bool
}

// DiscoverCodexChain walks the filesystem the way Codex 0.151.0 does:
// global $CODEX_HOME override-else-base, then git-root-down-to-cwd with
// override precedence, skipping empty files. No git repo means only cwd
// (plus global), not parent directories.
func DiscoverCodexChain(req DiscoverRequest) (CodexChain, error) {
	cwd := filepath.Clean(req.Cwd)
	if cwd == "" || !filepath.IsAbs(cwd) {
		return CodexChain{}, observability.WrapError(observability.CodeUsage, "codex chain requires absolute cwd", ErrContextRequired)
	}
	var chain CodexChain
	if home := strings.TrimSpace(req.CodexHome); home != "" {
		if !filepath.IsAbs(home) {
			return CodexChain{}, observability.WrapError(observability.CodeUsage, "CODEX_HOME must be absolute when set", ErrContextRequired)
		}
		f, ok, err := pickDirDoc(home, nil)
		if err != nil {
			return CodexChain{}, err
		}
		if ok {
			chain.Global = &f
		}
	}
	root, hasGit := findGitRoot(cwd)
	chain.GitRoot = root
	chain.NoGit = !hasGit
	var dirs []string
	if hasGit {
		dirs = dirsFromRootToCwd(root, cwd)
	} else {
		dirs = []string{cwd}
	}
	for _, dir := range dirs {
		f, ok, err := pickDirDoc(dir, req.FallbackFilenames)
		if err != nil {
			return CodexChain{}, err
		}
		if ok {
			chain.Project = append(chain.Project, f)
		}
	}
	return chain, nil
}

// meteredExtent is one project file's [Start, End) interval within the
// metered stream.
type meteredExtent struct {
	Start, End int
}

// meter performs the one concatenation project_doc_max_bytes is charged
// against — every project file's raw content bytes, in load order, with
// nothing between them — and records each file's extent within it. Every
// budget decision derives from this single construction, so a metered byte
// cannot be uncounted and an exempt byte cannot be charged.
func (c CodexChain) meter() ([]byte, []meteredExtent) {
	var buf bytes.Buffer
	extents := make([]meteredExtent, len(c.Project))
	for i, f := range c.Project {
		start := buf.Len()
		buf.Write(f.Bytes)
		extents[i] = meteredExtent{Start: start, End: buf.Len()}
	}
	return buf.Bytes(), extents
}

// MeteredProjectBytes is the exact stream codex-cli 0.151.0 meters against
// project_doc_max_bytes (live probe, ADR 0004): raw project-file content
// only. The global doc, the project-doc marker, and the per-file joiners are
// rendered after metering and are exempt; overflow silently truncates the
// tail of this stream.
func (c CodexChain) MeteredProjectBytes() []byte {
	metered, _ := c.meter()
	return metered
}

// RenderedInstructions is the instruction text codex-cli 0.151.0 renders
// inside its <INSTRUCTIONS> block: the global doc, then CodexProjectDocMarker
// when both sides exist, then the project files joined with CodexChainJoiner
// (live probe, ADR 0004). This is what the agent sees; MeteredProjectBytes is
// what the cap charges. Budget decisions must never use this stream.
func (c CodexChain) RenderedInstructions() []byte {
	var buf bytes.Buffer
	if c.Global != nil {
		buf.Write(c.Global.Bytes)
		if len(c.Project) > 0 {
			buf.WriteString(CodexProjectDocMarker)
		}
	}
	for i, f := range c.Project {
		if i > 0 {
			buf.WriteString(CodexChainJoiner)
		}
		buf.Write(f.Bytes)
	}
	return buf.Bytes()
}

// RefuseIfRequiredMissingOrTruncated fails closed: the required Mate/Crew
// override must be present in the project chain, non-empty, and end inside
// the metered budget (root-down, so cwd is last and most at risk). Any other
// project file spilling over the cap also refuses — Codex would silently
// drop its tail.
func (c CodexChain) RefuseIfRequiredMissingOrTruncated(requiredPath string, maxBytes int) error {
	if maxBytes <= 0 {
		maxBytes = CodexDefaultMaxBytes
	}
	requiredPath = filepath.Clean(requiredPath)
	metered, extents := c.meter()
	required := -1
	for i := range c.Project {
		if filepath.Clean(c.Project[i].Path) == requiredPath {
			required = i
			break
		}
	}
	if required < 0 || extents[required].Start == extents[required].End {
		return observability.WrapError(
			observability.CodeUsage,
			fmt.Sprintf("required Codex file %s is missing from the effective chain or empty; Codex would start anyway", requiredPath),
			ErrContextRequired,
		)
	}
	if end := extents[required].End; end > maxBytes {
		return observability.WrapError(
			observability.CodeUsage,
			fmt.Sprintf("required Codex file %s would be silently truncated: it ends at metered byte %d of the project chain and project_doc_max_bytes is %d", requiredPath, end, maxBytes),
			ErrContextTooLarge,
		)
	}
	if len(metered) > maxBytes {
		return observability.WrapError(
			observability.CodeUsage,
			fmt.Sprintf("Codex project instruction chain meters %d bytes and would be silently truncated at %d", len(metered), maxBytes),
			ErrContextTooLarge,
		)
	}
	return nil
}

// dirDocCandidates is the ordered candidate list Codex tries in one
// directory: the Mate-written override first, then the tracked project file,
// then any configured fallback filename. Index 0 is always the override, so
// a caller asking what the override displaces can skip it (see
// pickDirDocExcludingOverride).
func dirDocCandidates(dir string, fallback []string) []string {
	candidates := []string{
		filepath.Join(dir, CodexOverrideName),
		filepath.Join(dir, CodexBaseName),
	}
	for _, name := range fallback {
		name = strings.TrimSpace(name)
		if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
			continue
		}
		candidates = append(candidates, filepath.Join(dir, name))
	}
	return candidates
}

func pickDirDoc(dir string, fallback []string) (CodexFile, bool, error) {
	return pickDirDocFrom(dirDocCandidates(dir, fallback))
}

// pickDirDocFrom returns the first candidate that exists and is non-empty.
func pickDirDocFrom(candidates []string) (CodexFile, bool, error) {
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
				continue
			}
			return CodexFile{}, false, observability.WrapError(observability.CodeUsage, "read "+p, fmt.Errorf("%w: %w", ErrContextRequired, err))
		}
		if len(data) == 0 {
			// Empty override does not fall back to AGENTS.md (G1). An empty
			// file is skipped in the chain; required-file checks catch an
			// empty Mate override separately.
			if filepath.Base(p) == CodexOverrideName {
				return CodexFile{}, false, nil
			}
			continue
		}
		return CodexFile{Path: p, Bytes: data}, true, nil
	}
	return CodexFile{}, false, nil
}

func findGitRoot(cwd string) (string, bool) {
	dir := cwd
	for {
		info, err := os.Stat(filepath.Join(dir, ".git"))
		if err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func dirsFromRootToCwd(root, cwd string) []string {
	rel, err := filepath.Rel(root, cwd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []string{cwd}
	}
	out := []string{root}
	if rel == "." {
		return out
	}
	parts := strings.Split(rel, string(filepath.Separator))
	cur := root
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		cur = filepath.Join(cur, p)
		out = append(out, cur)
	}
	return out
}
