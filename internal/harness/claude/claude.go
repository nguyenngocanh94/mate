// Package claude is Claude Code as a mate harness: its Profile, launch,
// screens, hooks and transcripts. Everything that knows Claude Code lives
// here; the contract it implements is internal/harness.
package claude

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
)

// Claude is the Profile of Claude Code. Default delivery is
// --append-system-prompt-file. Inline --append-system-prompt is a bounded
// fallback only: it carries file contents (Claude 2.1.251 does not resolve
// @path tokens) and exposes the entire context in the process table.
type Claude struct {
	MaxInlineBytes int
	// InlineFallback takes the bounded --append-system-prompt path for a
	// launch that delivers a context file. Build still fails closed if the
	// file is missing, empty, relative, or over budget.
	InlineFallback bool
	// ConfigDir is the configured CLAUDE_CONFIG_DIR. Empty means use the
	// provider default for transcript discovery and leave the launch
	// variable unset so Claude inherits its normal account identity.
	ConfigDir string
}

// ClaudeConfigDirEnv is the variable Claude Code reads its account and
// transcripts root from.
const ClaudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

// Kind implements Profile.
func (Claude) Kind() harness.Kind { return KindClaude }

// Info implements Profile.
func (Claude) Info() harness.Info {
	return harness.Info{
		Name:            "Claude Code",
		RuntimeKind:     string(KindClaude),
		ConfigDir:       ".claude",
		InstructionFile: "CLAUDE.md",
		EnvKeys:         []string{ClaudeConfigDirEnv},
		// Claude Code discovers skills under its cwd's
		// `.claude/skills/<name>/SKILL.md`.
		SkillsDir: ".claude/skills",
		// Nerd Fonts 3.5 ships cod-claude at U+EC82 in its Codicons set.
		Icon: harness.Icon{Nerd: "\uec82", Unicode: "✻", ASCII: "*"},
		Documents: []harness.Document{
			{Path: "CLAUDE.md", Role: "repo_claude"},
			{Path: ".claude/settings.json", Role: "repo_claude_settings"},
		},
		// claude 2.1.282 --help takes all five.
		Efforts:      harness.Efforts,
		AdapterNotes: adapterNotes,
	}
}

// adapterNotes is Claude Code's section of the harness-adapters skill.
//
//go:embed adapter.md
var adapterNotes string

// Launcher implements Profile.
func (c Claude) Launcher() harness.Launcher { return c }

// Screen implements Profile.
func (Claude) Screen() harness.ScreenProfile { return claudeScreen{} }

// Capabilities implements Profile.
func (c Claude) Capabilities() harness.Capabilities {
	return harness.Capabilities{
		GracefulStop: harness.Cap[harness.GracefulStopper]{
			Status: harness.CapVerified,
			Impl:   harness.ExitCommand("/exit"),
			Evidence: harness.Evidence{
				Version:  "claude-code 2.1.251",
				Measured: "mate v1 G1, carried into this repo 2026-09-17",
				Proof:    "`herdr agent prompt <name> \"/exit\"` ended the agent; runtime.Herdr.StopAgent sends it",
			},
		},
		Session: harness.Cap[harness.SessionIdentity]{
			Status: harness.CapVerified,
			Impl:   claudeSessions{},
			Evidence: harness.Evidence{
				Version:  "claude-code 2.1.281",
				Measured: "2026-09-24 (docs/mvp.md section 7, task 35 A2 and A5)",
				Proof:    "live TestLiveMateResumeReloadsManual: `--resume <id>` of the launch's --session-id restores the conversation; the SessionStart hook records the id a /clear mints",
			},
		},
		Hooks: harness.Cap[harness.HookInstaller]{
			Status: harness.CapVerified,
			Impl:   claudeHooks{},
			Evidence: harness.Evidence{
				Version:  "claude-code 2.1.281",
				Measured: "2026-09-24 (docs/mvp.md section 7, tasks 35 A2 and 37)",
				Proof:    "live TestLiveMateSessionStartHookSources and TestLiveMateRecallOnCompact: the settings' SessionStart hook fires on startup, clear, compact and resume and its stdout reaches the model",
			},
		},
		TurnEnd: harness.Cap[harness.TurnEndEvidence]{
			Status: harness.CapVerified,
			Impl:   claudeTurnEnd{},
			Evidence: harness.Evidence{
				Version:  "claude-code 2.1.281",
				Measured: "2026-09-24 (docs/mvp.md task 37)",
				Proof:    "live TestLiveRestartMateStowsFirst: the Stop hook's answer in sent.log ended the stow turn",
			},
		},
		Transcript: harness.Cap[harness.TranscriptSource]{
			Status: harness.CapVerified,
			Impl:   claudeTranscripts{configDir: c.ConfigDir},
			Evidence: harness.Evidence{
				Version:  "claude-code 2.1.278",
				Measured: "2026-09-19 acceptance run (docs/evidence/m4-acceptance-2026-09-19.md)",
				Proof:    "internal/timeline/testdata/claude-2.1.278-transcript.jsonl through TestLedgerCharacterization and the catalog contract suite",
			},
		},
		Quota: harness.Cap[harness.QuotaProvider]{
			Status: harness.CapVerified,
			Impl:   harness.QuotaRow{Provider: "claude"},
			Evidence: harness.Evidence{
				Version:  "quota-axi 0.1.34",
				Measured: "2026-09-26",
				Proof:    "internal/quota/testdata/schema5-0.1.34.json through TestParseARealSchema5Snapshot",
			},
		},
	}
}

// claudeSessions is Claude's SessionIdentity. Prepare names the session at
// launch (--session-id), and the Mate's SessionStart hook records the new
// id a /clear mints, so nothing is left to read at stop.
type claudeSessions struct{}

// AtStop implements SessionIdentity.
func (claudeSessions) AtStop(string, time.Time, string) string { return "" }

// Resumable implements SessionIdentity. Claude keeps every session it
// named; nothing is checked before --resume.
func (claudeSessions) Resumable(string) error { return nil }

// claudeHooks is Claude's HookInstaller: the hooks of the Mate's settings
// file (ClaudeSettings), which Claude never asks the operator to trust.
type claudeHooks struct{}

// Own implements HookInstaller.
func (claudeHooks) Own(string, string) []harness.OwnHook { return nil }

// ReviewOwn implements HookInstaller. Claude has no hook review.
func (claudeHooks) ReviewOwn(context.Context, harness.HookReviewPane, string, []harness.OwnHook) error {
	return observability.NewError(observability.CodeUsage, "claude asks no one to review its hooks; there is no review to walk")
}

// DigestMaxBytes implements HookInstaller.
func (claudeHooks) DigestMaxBytes() int { return ClaudeSessionHookMaxBytes }

// BareSessionHook implements HookInstaller: every Claude Mate's settings
// file, its users' own included, runs `hook mate-session` and nothing more.
func (claudeHooks) BareSessionHook() bool { return true }

// claudeTurnEnd is Claude's TurnEndEvidence: the Mate's Stop hook (`mate
// hook mate-stop`) logs each answer it sees to sent.log. Claude's transcript
// is not read for it.
type claudeTurnEnd struct{}

// LogsAnswers implements TurnEndEvidence.
func (claudeTurnEnd) LogsAnswers() bool { return true }

// EndsInTranscript implements TurnEndEvidence.
func (claudeTurnEnd) EndsInTranscript() bool { return false }

// TranscriptTurnEnded implements TurnEndEvidence.
func (claudeTurnEnd) TranscriptTurnEnded([]byte, time.Time) bool { return false }

// PaneEnv implements Launcher. A Claude launch resolves its config
// directory itself (ClaudeConfigDirForLaunch), so a pane needs none.
func (Claude) PaneEnv() ([]harness.EnvVar, error) { return nil, nil }

// claudeLaunch is what Claude's Prepare hands its Build.
type claudeLaunch struct {
	// sessionID names a fresh session (--session-id). A resumed one comes
	// from AgentSpec.ResumeSessionID.
	sessionID string
	// settingsPath is the --settings file. Claude takes it together with
	// the session identity, fresh or resumed.
	settingsPath string
	// manualInCwd says the cwd already loads the operating manual on its
	// own, so the launch carries no context flag. Claude Code reads
	// `CLAUDE.md` from the directory it starts in, and a Mate's cwd holds
	// one that is exactly `@AGENTS.md`; passing that same manual as
	// --append-system-prompt-file delivered it twice (docs/mvp.md, "No ky
	// thuat").
	manualInCwd bool
}

// Prepare implements Launcher. Every Claude launch names its session and
// carries a settings file with it, fresh or resumed. A Mate also gets the
// `CLAUDE.md` that loads its manual from the cwd and the hook settings of
// its cwd's `.claude/`; a Crew gets hook-less settings beside its brief, so
// nothing lands in its worktree.
func (c Claude) Prepare(_ context.Context, req harness.PrepareRequest) (harness.Prepared, error) {
	if err := req.Check(KindClaude); err != nil {
		return harness.Prepared{}, err
	}
	var launch claudeLaunch
	sessionID := req.ResumeSessionID
	if sessionID == "" {
		sessionID = req.MintSessionID()
		launch.sessionID = sessionID
	}
	if req.Role == harness.RoleMate {
		files, err := ClaudeMateFiles(req)
		if err != nil {
			return harness.Prepared{}, err
		}
		launch.settingsPath = ClaudeSettingsPath(req.Cwd)
		launch.manualInCwd = true
		return harness.Prepared{Files: files, SessionID: sessionID, Launch: launch}, nil
	}
	// A Claude launch can only carry a session id together with a settings
	// file, and decision 9 wants session_id= recorded from the first day,
	// so the crew gets a settings file of its own. It wires no hooks (the
	// Mate's hooks are the Mate's) and turns auto-memory off.
	launch.settingsPath = filepath.Join(req.StateDir, ClaudeSettingsFile)
	return harness.Prepared{
		Files:       []harness.LaunchFile{{Path: launch.settingsPath, Data: CrewClaudeSettings()}},
		SessionID:   sessionID,
		ContextPath: req.ContextPath,
		Launch:      launch,
	}, nil
}

// ClaudeMateFiles are the two files of a Claude Mate's cwd: `CLAUDE.md`,
// which loads the manual by reference, and `.claude/settings.json`, wired
// to the mate binary's `hook mate-prompt`/`hook mate-stop`/`hook
// mate-session` (ClaudeSettings). An existing settings file - the user's
// own, or one a previous start wrote - keeps every key it has; the two
// things added to it are `autoMemoryEnabled: false` when the file does not
// say (EnsureAutoMemoryOff), and the SessionStart hook when no SessionStart
// entry runs it (EnsureSessionHook), so a Mate directory made before task 35
// or 37 starts with auto-memory off and its digest wired too.
func ClaudeMateFiles(req harness.PrepareRequest) ([]harness.LaunchFile, error) {
	manual, err := filepath.Rel(req.Cwd, req.ContextPath)
	if err != nil || manual == ".." || strings.HasPrefix(manual, ".."+string(filepath.Separator)) {
		return nil, observability.WrapError(observability.CodeUsage,
			fmt.Sprintf("the Mate manual %s is not inside its cwd %s, so %s cannot load it", req.ContextPath, req.Cwd, Claude{}.Info().InstructionFile), harness.ErrContextRequired)
	}
	path := ClaudeSettingsPath(req.Cwd)
	var settings []byte
	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		settings, _, err = EnsureAutoMemoryOff(existing)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		settings, _, err = EnsureSessionHook(settings, req.Binary)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	case os.IsNotExist(err):
		if settings, err = ClaudeSettings(req.Binary); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return []harness.LaunchFile{
		{Path: filepath.Join(req.Cwd, Claude{}.Info().InstructionFile), Data: []byte("@" + filepath.ToSlash(manual) + "\n")},
		{Path: path, Data: settings},
	}, nil
}

// Build implements Launcher: a startable Claude spec, or an error. A spec
// whose required context cannot be delivered is refused, not returned.
func (c Claude) Build(_ context.Context, spec harness.AgentSpec) (harness.LaunchSpec, error) {
	if spec.Kind != "" && spec.Kind != KindClaude {
		return harness.LaunchSpec{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("claude adapter got kind %q", spec.Kind))
	}
	launch, ok := spec.Launch.(claudeLaunch)
	if spec.Launch != nil && !ok {
		return harness.LaunchSpec{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("claude adapter got launch data %T that its Prepare did not make", spec.Launch))
	}
	max := c.MaxInlineBytes
	if max <= 0 {
		max = harness.DefaultMaxInlineBytes
	}
	cwd := spec.Cwd
	path := spec.ContextPath
	// manualInCwd is Prepare recording that the cwd already loads the
	// manual (Claude reads CLAUDE.md from the directory it starts in), so
	// this launch carries no context flag at all. The two are mutually
	// exclusive rather than merely redundant: a spec that names a path AND
	// claims the cwd loads it is the double delivery this flag exists to
	// remove, so it is refused instead of silently preferring one.
	switch {
	case launch.manualInCwd && path != "":
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			"claude launch cannot both carry a context path and declare the manual already loaded from cwd", harness.ErrContextRequired)
	case launch.manualInCwd && c.InlineFallback:
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			"claude inline fallback needs a context file to inline; it cannot be combined with a cwd-loaded manual", harness.ErrContextRequired)
	case !launch.manualInCwd && path == "":
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, "claude context path is required", harness.ErrContextRequired)
	case path != "" && !filepath.IsAbs(path):
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, fmt.Sprintf("context path %s is relative", path), harness.ErrContextRequired)
	}
	if cwd == "" || !filepath.IsAbs(cwd) {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, "claude launch requires the absolute cwd the agent will run in", harness.ErrContextRequired)
	}
	if launch.sessionID != "" && spec.ResumeSessionID != "" {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			"claude session id and resume session id are mutually exclusive", harness.ErrContextRequired)
	}
	effectiveID := launch.sessionID
	if spec.ResumeSessionID != "" {
		effectiveID = spec.ResumeSessionID
	}
	if (effectiveID == "") != (launch.settingsPath == "") {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			"claude transcript locator requires both session id and settings path", harness.ErrContextRequired)
	}
	if effectiveID != "" {
		if _, err := uuid.Parse(effectiveID); err != nil {
			return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
				"claude session id must be a UUID", harness.ErrContextRequired)
		}
		if !filepath.IsAbs(launch.settingsPath) {
			return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
				"claude settings path must be absolute", harness.ErrContextRequired)
		}
		info, err := os.Stat(launch.settingsPath)
		if err != nil {
			return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
				"claude settings file is not readable", fmt.Errorf("%w: %w", harness.ErrContextRequired, err))
		}
		if !info.Mode().IsRegular() {
			return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage,
				"claude settings path is not a regular file", harness.ErrContextRequired)
		}
	}
	configDir, setConfigDir, err := ClaudeConfigDirForLaunch(c.ConfigDir)
	if err != nil {
		return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, "claude config directory", err)
	}
	envInput := append([]harness.EnvVar(nil), spec.Env...)
	var unsetEnv []string
	if setConfigDir {
		envInput = append(envInput, harness.EnvVar{Key: ClaudeConfigDirEnv, Value: configDir})
	} else {
		unsetEnv = []string{ClaudeConfigDirEnv}
	}

	extra := make([]string, 0, 6)
	extra = append(extra, "--dangerously-skip-permissions")
	switch {
	case spec.ResumeSessionID != "":
		// --resume and --session-id are mutually exclusive on the Claude
		// CLI; resuming never carries --session-id (2.1.274 --help).
		extra = append(extra, "--resume", spec.ResumeSessionID, "--settings", launch.settingsPath)
	case launch.sessionID != "":
		extra = append(extra, "--session-id", launch.sessionID, "--settings", launch.settingsPath)
	}
	if spec.Role == harness.RoleMate {
		// A coordinator must not inherit the captain's unrelated development
		// plugins, MCP servers and tools. Its project skills and explicit hook
		// settings still load. Keep a stable prefix for prompt caching.
		extra = append(extra, "--setting-sources", "project", "--strict-mcp-config",
			"--tools", "Bash,Read,Write,Edit,Glob,Grep,Skill", "--autocompact", "300000")
		if spec.Model == "" {
			spec.Model = "opus"
		}
		if spec.Effort == "" {
			spec.Effort = harness.EffortMedium
		}
	}
	effortOmitted := spec.Effort != "" && !c.Info().SupportsEffort(spec.Effort)
	if spec.Model != "" {
		extra = append(extra, "--model", spec.Model)
	}
	if c.Info().SupportsEffort(spec.Effort) {
		extra = append(extra, "--effort", string(spec.Effort))
	}
	// dangerousPermissionNotes records why --dangerously-skip-permissions is
	// carried and what it does not cover; see the analogous (not identical -
	// Codex carries its own extra bullets) record on the Codex adapter
	// (internal/harness/codex/codex.go) for the sibling harness.
	dangerousPermissionNotes := []string{
		"the captain ruled on 2026-09-14 that Mate and Crew launches carry the harness permission bypass; there is no external sandbox of any kind, because a Crew runs on the operator's own machine on real project code",
		"--dangerously-skip-permissions suppresses per-tool permission prompts; Claude exposes no sandbox option to remove",
		"two separate one-time walls remain, neither cleared by this flag - naming only one, as an earlier version of this note did, is the exact understatement this record exists to correct: (1) Claude's per-absolute-path directory-trust confirmation, measured 2026-09-14; (2) --dangerously-skip-permissions itself raises a second, separate blocking 'Bypass Permissions mode' acceptance screen the first time a given CLAUDE_CONFIG_DIR sees it - measured 2026-09-14 on a linked worktree of an already-trusted primary repo with an isolated config dir, then persisted to that config dir's settings.json so it never reappears there (why a machine that accepted it long ago never surfaces it)",
		"whether mate should auto-accept that second screen for the operator is a separate open captain call (gomate-claude-bypass-mode-screen), not implemented here",
		"the bypass is unconditional: there is no config key or CLI flag to opt out, a deliberate choice consistent with unattended Crew/Mate operation",
	}
	out := harness.LaunchPlan{
		RuntimeKind:     c.Info().RuntimeKind,
		Screen:          c.Screen(),
		Cwd:             cwd,
		Env:             envInput,
		ContextFiles:    []harness.GeneratedFile{{Path: path, Role: "canonical_context"}},
		ContextPath:     path,
		ContextRequired: true,
		MaxInlineBytes:  max,
		TaskPrompt:      spec.TaskPrompt,
		Model:           spec.Model,
		Effort:          spec.Effort,
		EffortOmitted:   effortOmitted,
		ClaudeConfigDir: configDir,
		UnsetEnv:        unsetEnv,
		EnvKeys:         append(c.Info().EnvKeys, spec.EnvKeys...),
	}
	if launch.manualInCwd {
		out.ContextFiles = nil
		out.ContextRequired = false
		out.Delivery = harness.DeliveryCwdManual
		out.Args = extra
		out.Notes = append(append([]string(nil), dangerousPermissionNotes...),
			"no context flag is passed: Claude Code loads CLAUDE.md from the directory it starts in, and the caller declared that file already loads the manual",
		)
		return harness.NewLaunchSpec(out)
	}
	if c.InlineFallback {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, path+" not found", harness.ErrContextRequired)
			}
			return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, "stat "+path, fmt.Errorf("%w: %w", harness.ErrContextRequired, err))
		}
		if info.Size() > int64(max) {
			return harness.LaunchSpec{}, observability.WrapError(
				observability.CodeUsage,
				fmt.Sprintf("%s is %d bytes, inline limit %d", path, info.Size(), max),
				harness.ErrContextTooLarge,
			)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return harness.LaunchSpec{}, observability.WrapError(observability.CodeUsage, "read "+path, fmt.Errorf("%w: %w", harness.ErrContextRequired, err))
		}
		out.Delivery = harness.DeliveryAppendSystemPrompt
		out.Args = append(extra, "--append-system-prompt", string(data))
		out.Notes = append(append([]string(nil), dangerousPermissionNotes...),
			"inline --append-system-prompt is a bounded fallback; the entire context is visible in the process table",
		)
	} else {
		out.Delivery = harness.DeliveryAppendSystemPromptFile
		out.Args = append(extra, "--append-system-prompt-file", path)
		out.Notes = append(append([]string(nil), dangerousPermissionNotes...),
			"default Claude delivery is --append-system-prompt-file; Claude exits 1 on a missing file, and mate still fails in BuildLaunchSpec first",
			"G5-12 live evidence proves Claude receives the locator flags through Herdr and fires the configured Stop hook",
		)
	}
	return harness.NewLaunchSpec(out)
}

const (
	KindClaude harness.Kind = "claude"
)
