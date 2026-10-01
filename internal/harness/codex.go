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
	"time"

	"github.com/google/uuid"
	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/observability"
)

// Codex is the Profile of Codex CLI 0.151.0. Prepare names the generated
// context <cwd>/AGENTS.override.md (not tracked AGENTS.md), and Build binds
// that cwd and refuses to start when the effective
// instruction chain is missing the required file, empty, or would be
// silently truncated at project_doc_max_bytes.
type Codex struct {
	// MaxChainBytes is the instruction-file cap the launch meters against
	// and hands Codex. Zero means CodexDefaultMaxBytes.
	MaxChainBytes int
	LookPath      func(name string) (string, error) // unused in G2; reserved for G4 executable probe
	// Home is $CODEX_HOME for global instruction discovery. Empty means the
	// one the process environment resolves (LaunchCodexHome).
	Home string
	// FallbackFilenames are Codex project_doc_fallback_filenames. Empty
	// means only AGENTS.override.md / AGENTS.md, never an assumed
	// TEAM_GUIDE.md.
	FallbackFilenames []string
	// SessionsDir is where Codex writes its rollouts, which a Mate's resume
	// is checked against and its session id recovered from at stop (task
	// 35). Empty means CodexSessionsDir(Home); tests point it at a
	// directory of their own so none reads the operator's ~/.codex.
	SessionsDir string
}

// Kind implements Profile.
func (Codex) Kind() Kind { return KindCodex }

// Info implements Profile.
func (Codex) Info() Info {
	return Info{
		RuntimeKind:     string(KindCodex),
		ConfigDir:       ".codex",
		InstructionFile: CodexOverrideName,
		EnvKeys:         []string{config.EnvCodexHome},
		// Codex discovers no skills from a Mate's cwd; its manual sends it
		// to each skill's file by path, in the directory a Claude Mate's
		// layout puts them, so a project's skills live in one place
		// whichever harness its Mate runs on.
		SkillsDir: ".claude/skills",
	}
}

// Launcher implements Profile.
func (c Codex) Launcher() Launcher { return c }

// Screen implements Profile.
func (Codex) Screen() ScreenProfile { return codexScreen{} }

// Capabilities implements Profile.
func (c Codex) Capabilities() Capabilities {
	return Capabilities{
		GracefulStop: Cap[GracefulStopper]{
			Status: CapUnknown,
			Reason: "never measured: v1 G1 proved only Claude's /exit, so a Codex stop closes the pane",
		},
		Session: Cap[SessionIdentity]{
			Status: CapVerified,
			Impl:   codexSessions{c},
			Evidence: Evidence{
				Version:  "codex-cli 0.156.1",
				Measured: "2026-09-24 (docs/mvp.md section 7, task 35 A4 and B11)",
				Proof:    "live TestLiveSpawnMateResumeRemembersCodex: the id read at stop resumed with `codex resume <flags> <id>` and the Mate remembered",
			},
		},
		Hooks: Cap[HookInstaller]{
			Status: CapVerified,
			Impl:   codexHooks{},
			Evidence: Evidence{
				Version:  "codex-cli 0.156.1",
				Measured: "2026-09-24 (docs/mvp.md section 7, tasks 35 A3 and 37)",
				Proof:    "live TestLiveCodexSessionStartHook and TestLiveCodexMateRecallHook: the cwd's .codex/hooks.json SessionStart hook, trusted in the hook review, puts its stdout in context",
			},
		},
		TurnEnd: Cap[TurnEndEvidence]{
			Status: CapVerified,
			Impl:   codexTurnEnd{},
			Evidence: Evidence{
				Version:  "codex-cli 0.156.1",
				Measured: "2026-09-24 (docs/mvp.md task 38)",
				Proof:    "live TestLiveMemorySurvivesRestart codex: the stow ended on the rollout's task_complete, where the composer had read empty mid-turn",
			},
		},
		Transcript: Cap[TranscriptSource]{
			Status: CapVerified,
			Impl:   codexTranscripts{home: c.Home},
			Evidence: Evidence{
				Version:  "codex-cli 0.154",
				Measured: "2026-09-20 on the 2026-09-19 acceptance rollout (docs/evidence/m4-acceptance-2026-09-19.md)",
				Proof:    "internal/timeline/testdata/codex-0.154-rollout.jsonl through TestLedgerCharacterization and the catalog contract suite",
			},
		},
		Quota: Cap[QuotaProvider]{
			Status: CapVerified,
			// Native Codex is filed under the codex-home account (firstmate
			// bin/fm-quota-axi-lib.sh quota_lane).
			Impl: quotaRow{provider: "codex", lane: "codex-home"},
			Evidence: Evidence{
				Version:  "quota-axi 0.1.34",
				Measured: "2026-09-26",
				Proof:    "internal/quota/testdata/schema5-0.1.34.json through TestParseARealSchema5Snapshot and TestSchema6BindsTheHarnessAccount",
			},
		},
	}
}

// codexSessions is Codex's SessionIdentity. Codex has no launch-time
// session id: it exists once the first prompt opens the rollout, and after
// a /clear it is a new one, so it is read at the one moment that knows it.
type codexSessions struct{ c Codex }

func (s codexSessions) dir() (string, error) {
	if s.c.SessionsDir != "" {
		return s.c.SessionsDir, nil
	}
	return CodexSessionsDir(s.c.Home)
}

// AtStop implements SessionIdentity (task 35, B11).
//
// The runtime's ref (Herdr's agent_session.value) is the first rule. It is
// exact, and on Herdr 0.8.2 it is filled by the Herdr integration's
// SessionStart hook in the operator's ~/.codex/hooks.json, which codex-cli
// 0.154.0 runs at the first prompt of a session, not at launch (measured
// 2026-09-24). When Herdr has none - the agent already gone, or no
// integration - the rollout is adopted by the timeline's own rule
// (AdoptCodexRollout: this cwd, a session_meta at or after launched, and
// exactly one of them). Empty means neither answered: a resumed session's
// rollout is older than its launch, so the adoption rule rightly finds
// nothing new for it.
func (s codexSessions) AtStop(cwd string, launched time.Time, runtimeRef string) string {
	if ref := strings.TrimSpace(runtimeRef); ref != "" {
		return ref
	}
	if launched.IsZero() {
		return ""
	}
	dir, err := s.dir()
	if err != nil {
		return ""
	}
	candidates, err := CodexRolloutCandidatesSince(dir, launched)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	adopted := AdoptCodexRollout(candidates, cwd, launched, "")
	if adopted.Status != CodexAdoptionKnown {
		return ""
	}
	return adopted.Candidate.Meta.SessionID
}

// Resumable implements SessionIdentity. codex-cli 0.154.0 answers `codex
// resume <unknown id>` with "No saved session found with ID ..." and drops
// to the shell, which Herdr reports only as an agent-start timeout a minute
// later (measured 2026-09-24, task 35); the rollout's file name says it at
// once.
func (s codexSessions) Resumable(id string) error {
	dir, err := s.dir()
	if err != nil {
		return fmt.Errorf("cannot look for the Codex session %s to resume (%v)", id, err)
	}
	if _, ok := CodexRolloutPath(dir, id); !ok {
		return &NoSessionError{Session: "the Codex session " + id, Missing: dir + " has no rollout for it"}
	}
	return nil
}

// codexHooks is Codex's HookInstaller: the SessionStart hook of the Mate's
// `.codex/hooks.json` (CodexHooks), which Codex asks the operator to trust
// in its hook review.
type codexHooks struct{}

// Own implements HookInstaller.
func (codexHooks) Own(binary, cwd string) []OwnHook {
	return []OwnHook{{
		Event:   "SessionStart",
		Source:  CodexHooksPath(cwd),
		Command: SessionHookCommand(binary, KindCodex),
	}}
}

// DigestMaxBytes implements HookInstaller.
func (codexHooks) DigestMaxBytes() int { return CodexSessionHookMaxBytes }

// codexTurnEnd is Codex's TurnEndEvidence: no Stop hook, but the rollout
// records `task_complete` when a turn ends (CodexTurnCompletedAfter).
type codexTurnEnd struct{}

// LogsAnswers implements TurnEndEvidence.
func (codexTurnEnd) LogsAnswers() bool { return false }

// EndsInTranscript implements TurnEndEvidence.
func (codexTurnEnd) EndsInTranscript() bool { return true }

// TranscriptTurnEnded implements TurnEndEvidence.
func (codexTurnEnd) TranscriptTurnEnded(rollout []byte, after time.Time) bool {
	return CodexTurnCompletedAfter(rollout, after)
}

func (c Codex) maxBytes() int {
	if c.MaxChainBytes > 0 {
		return c.MaxChainBytes
	}
	return CodexDefaultMaxBytes
}

// Prepare implements Launcher. Codex reads its instructions from
// AGENTS.override.md at its cwd and nothing else, so the agent's
// instructions are copied there: for a Crew that is a file in its
// worktree, excluded so it is never committed, which is why the crew is
// told to read the brief by absolute path rather than to trust whatever it
// found in its cwd. A Codex Mate also gets its SessionStart hook in its
// cwd's `.codex/hooks.json`.
//
// A Codex Mate's directory also carries a Claude Mate's `CLAUDE.md` and
// `.claude/settings.json`, as it always has: nothing in Codex reads them,
// and leaving them out is a change to what a start writes, for a change of
// its own. Codex has no launch-time session id; it exists only once the
// first prompt opens the rollout, so a fresh launch names none.
func (c Codex) Prepare(_ context.Context, req PrepareRequest) (Prepared, error) {
	if err := req.check(KindCodex); err != nil {
		return Prepared{}, err
	}
	manual, err := os.ReadFile(req.ContextPath)
	if err != nil {
		return Prepared{}, err
	}
	override := CodexInstructionPath(req.Cwd)
	files := []LaunchFile{{Path: override, Data: manual, Exclude: true}}
	if req.Role == RoleMate {
		claude, err := claudeMateFiles(req)
		if err != nil {
			return Prepared{}, err
		}
		files = append(files, LaunchFile{Path: CodexHooksPath(req.Cwd), Data: CodexHooks(req.Binary)})
		files = append(files, claude...)
	}
	return Prepared{Files: files, SessionID: req.ResumeSessionID, ContextPath: override}, nil
}

// Build implements Launcher: a startable Codex spec, or an error.
func (c Codex) Build(_ context.Context, spec AgentSpec) (LaunchSpec, error) {
	if spec.Kind != "" && spec.Kind != KindCodex {
		return LaunchSpec{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("codex adapter got kind %q", spec.Kind))
	}
	if spec.Launch != nil {
		return LaunchSpec{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("codex adapter got launch data %T that its Prepare did not make", spec.Launch))
	}
	resumeID := strings.TrimSpace(spec.ResumeSessionID)
	if spec.ResumeSessionID != "" {
		// `codex resume [OPTIONS] [SESSION_ID]` opens the picker only when
		// no id is given (codex-cli 0.154.0 --help). With an id it resumes
		// that session straight to the composer: measured 2026-09-24 in a
		// Herdr 0.8.2 pane (docs/mvp.md section 7, task 35). The id is only
		// ever one Herdr's agent_session or a rollout's session_meta
		// recorded, so anything that is not a UUID is refused rather than
		// passed on as an argument Codex might read as a flag or a name.
		if _, err := uuid.Parse(resumeID); err != nil {
			return LaunchSpec{}, observability.WrapError(observability.CodeUsage,
				fmt.Sprintf("codex resume id %q is not a session UUID", spec.ResumeSessionID), ErrContextRequired)
		}
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
	max := c.maxBytes()
	codexHome, err := LaunchCodexHome(c.Home)
	if err != nil {
		return LaunchSpec{}, observability.WrapError(observability.CodeUsage, "codex home", err)
	}
	chain, err := DiscoverCodexChain(DiscoverRequest{
		Cwd:               cwd,
		CodexHome:         codexHome,
		FallbackFilenames: c.FallbackFilenames,
	})
	if err != nil {
		return LaunchSpec{}, err
	}
	if err := chain.RefuseIfRequiredMissingOrTruncated(want, max); err != nil {
		return LaunchSpec{}, err
	}
	envInput := append([]EnvVar(nil), spec.Env...)
	envInput = append(envInput, EnvVar{Key: config.EnvCodexHome, Value: codexHome})
	args := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", CodexDisableUpdateCheck, "-c", CodexProjectDocMaxBytesOverride}
	profile, effortOmitted := profileArgs(KindCodex, spec.Model, spec.Effort)
	args = append(args, profile...)
	if resumeID != "" {
		// The subcommand takes the same flags as a fresh launch (0.154.0
		// `codex resume --help`), and the id goes last, after every flag.
		// Measured 2026-09-24: without CodexDisableUpdateCheck a resume
		// draws the same release-update prompt as a fresh launch; with it,
		// a resume in an already-trusted directory goes straight to the
		// composer with the old conversation replayed above it.
		args = append(append([]string{"resume"}, args...), resumeID)
	}
	fallback := append([]string(nil), c.FallbackFilenames...)
	return NewLaunchSpec(LaunchPlan{
		RuntimeKind:     c.Info().RuntimeKind,
		Screen:          c.Screen(),
		Args:            args,
		Cwd:             cwd,
		Env:             envInput,
		ContextFiles:    []GeneratedFile{{Path: want, Role: "codex_override"}},
		Delivery:        DeliveryInstructionFile,
		ContextPath:     want,
		ContextRequired: true,
		MaxFileBytes:    max,
		TaskPrompt:      spec.TaskPrompt,
		Model:           spec.Model,
		Effort:          spec.Effort,
		EffortOmitted:   effortOmitted,
		Notes: []string{
			"the captain ruled on 2026-09-14 that Mate and Crew launches carry the harness permission bypass; there is no external sandbox of any kind, because a Crew runs on the operator's own machine on real project code",
			"the captain first chose the narrower --ask-for-approval never --sandbox workspace-write pair, then reversed that ruling the same day: a linked Crew worktree's .git is a pointer file into the primary repo's git dir, outside workspace-write's writable root, so a Codex Crew under the narrow pair could do work but could never git commit it (measured both directions) - and mate never commits on a Crew's behalf, it only fast-forwards the Crew branch, so uncommitted work can never be delivered",
			"this is not merely a prompt bypass: under the combined flag a Codex Crew can write outside its own worktree - the workspace database (.mate/mate.db), other Crews' worktrees, and the operator's home directory are all reachable; the captain accepted that cost knowingly to keep Crews able to commit",
			"this puts gomate in the same posture firstmate's own tooling already runs Codex in",
			"this does not clear Codex's per-absolute-path directory-trust confirmation; that dialog is a separate wall no flag removes, measured 2026-09-14",
			"the launch also carries -c check_for_update_on_startup=false: codex-cli draws a three-option release-update prompt ahead of everything else while a newer version is published, which blocks the composer (measured 2026-09-18 with 0.154.0 installed and 0.155.0 out); the key is documented and the override is per-launch, so the operator's own config.toml is never written",
			"the bypass is unconditional: there is no config key or CLI flag to opt out, a deliberate choice consistent with unattended Crew/Mate operation",
			"Codex 0.151.0 loads AGENTS.override.md in preference to AGENTS.md and silently truncates the project chain's content bytes at project_doc_max_bytes (global doc, marker and joiners are rendered unmetered); the launch raises that cap to CodexDefaultMaxBytes with -c project_doc_max_bytes= and mate meters against the same number",
			"missing or empty project files do not fail Codex; mate refuses to start instead",
		},
		CodexHome: codexHome,
		// The chain is discovered again every time the spec is validated, so
		// the runtime boundary refuses a launch whose override went missing
		// or grew past the cap after Build.
		CheckContext: func(s LaunchSpec) error {
			want := CodexInstructionPath(s.Cwd())
			if filepath.Clean(s.ContextPath()) != filepath.Clean(want) {
				return s.codedRequired(fmt.Sprintf("context path %s is not the Codex discovery file for cwd %s (%s)", s.ContextPath(), s.Cwd(), want))
			}
			chain, err := DiscoverCodexChain(DiscoverRequest{Cwd: s.Cwd(), CodexHome: codexHome, FallbackFilenames: fallback})
			if err != nil {
				return err
			}
			max := s.MaxFileBytes()
			if max <= 0 {
				max = CodexDefaultMaxBytes
			}
			return chain.RefuseIfRequiredMissingOrTruncated(want, max)
		},
	})
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

// CodexProjectDocMaxBytesOverride raises Codex's instruction-file cap to
// CodexDefaultMaxBytes for one launch. `project_doc_max_bytes` is the
// documented key ("Maximum number of bytes to read from AGENTS.md files"),
// and codex-cli 0.154.0 accepts it under --strict-config (measured
// 2026-09-18; an unknown key in the same position is refused with "unknown
// configuration field"). Passed per launch, never written to config.toml,
// for the same reason as CodexDisableUpdateCheck.
var CodexProjectDocMaxBytesOverride = fmt.Sprintf("project_doc_max_bytes=%d", CodexDefaultMaxBytes)

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
