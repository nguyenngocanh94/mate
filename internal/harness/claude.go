package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/observability"
)

// Claude is the HarnessAdapter for Claude Code. Default delivery is
// --append-system-prompt-file. Inline --append-system-prompt is a bounded
// fallback only: it carries file contents (Claude 2.1.251 does not resolve
// @path tokens) and exposes the entire context in the process table.
type Claude struct {
	MaxInlineBytes int
}

// Validate reports Claude capabilities. Executable/auth probing is G4; the
// pinned live lab proves the launch flags reach Claude through Herdr.
func (c Claude) Validate(_ context.Context, cfg Config) (CapabilitySet, error) {
	if cfg.Kind != "" && cfg.Kind != KindClaude {
		return CapabilitySet{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("claude adapter got kind %q", cfg.Kind))
	}
	return CapabilitySet{
		Kind:                      KindClaude,
		Deliveries:                []Delivery{DeliveryAppendSystemPromptFile, DeliveryAppendSystemPrompt},
		DefaultDelivery:           DeliveryAppendSystemPromptFile,
		SupportsInlineFallback:    true,
		GracefulStop:              GracefulStop{Available: true, Method: `herdr agent prompt <name> "/exit"`, ProvenOn: "claude-code 2.1.251"},
		InlineExposesProcessTable: true,
		Assumptions: []Assumption{
			{
				ID:      AssumptionInlineProcessTable,
				Status:  "proven",
				Summary: "Inline --append-system-prompt puts the entire canonical context in the process table (ps eww). Default to the file flag.",
			},
			{
				ID:      AssumptionClaudeArgsThroughHerdr,
				Status:  "proven",
				Summary: "The pinned live lab proved --session-id, --settings and --append-system-prompt-file reach Claude through Herdr; the configured Stop hook fired with the launched session id.",
			},
		},
	}, nil
}

// BuildLaunchSpec constructs a startable Claude spec or returns an error.
// A spec whose required context cannot be delivered is refused, not returned.
func (c Claude) BuildLaunchSpec(_ context.Context, spec AgentSpec) (LaunchSpec, error) {
	if spec.Kind != "" && spec.Kind != KindClaude {
		return LaunchSpec{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("claude adapter got kind %q", spec.Kind))
	}
	max := c.MaxInlineBytes
	if max <= 0 {
		max = DefaultMaxInlineBytes
	}
	cwd := spec.Cwd
	path := spec.ContextPath
	// ManualInCwd is the caller asserting that the cwd already loads the
	// manual (Claude reads CLAUDE.md from the directory it starts in), so
	// this launch carries no context flag at all. The two are mutually
	// exclusive rather than merely redundant: a spec that names a path AND
	// claims the cwd loads it is the double delivery this flag exists to
	// remove, so it is refused instead of silently preferring one.
	switch {
	case spec.ManualInCwd && path != "":
		return LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			"claude launch cannot both carry a context path and declare the manual already loaded from cwd", ErrContextRequired)
	case spec.ManualInCwd && spec.InlineFallback:
		return LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			"claude inline fallback needs a context file to inline; it cannot be combined with a cwd-loaded manual", ErrContextRequired)
	case !spec.ManualInCwd && path == "":
		return LaunchSpec{}, observability.WrapError(observability.CodeUsage, "claude context path is required", ErrContextRequired)
	case path != "" && !filepath.IsAbs(path):
		return LaunchSpec{}, observability.WrapError(observability.CodeUsage, fmt.Sprintf("context path %s is relative", path), ErrContextRequired)
	}
	if cwd == "" || !filepath.IsAbs(cwd) {
		return LaunchSpec{}, observability.WrapError(observability.CodeUsage, "claude launch requires the absolute cwd the agent will run in", ErrContextRequired)
	}
	if spec.ClaudeSessionID != "" && spec.ResumeSessionID != "" {
		return LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			"claude session id and resume session id are mutually exclusive", ErrContextRequired)
	}
	effectiveID := spec.ClaudeSessionID
	if spec.ResumeSessionID != "" {
		effectiveID = spec.ResumeSessionID
	}
	if (effectiveID == "") != (spec.ClaudeSettingsPath == "") {
		return LaunchSpec{}, observability.WrapError(observability.CodeUsage,
			"claude transcript locator requires both session id and settings path", ErrContextRequired)
	}
	if effectiveID != "" {
		if _, err := uuid.Parse(effectiveID); err != nil {
			return LaunchSpec{}, observability.WrapError(observability.CodeUsage,
				"claude session id must be a UUID", ErrContextRequired)
		}
		if !filepath.IsAbs(spec.ClaudeSettingsPath) {
			return LaunchSpec{}, observability.WrapError(observability.CodeUsage,
				"claude settings path must be absolute", ErrContextRequired)
		}
		info, err := os.Stat(spec.ClaudeSettingsPath)
		if err != nil {
			return LaunchSpec{}, observability.WrapError(observability.CodeUsage,
				"claude settings file is not readable", fmt.Errorf("%w: %w", ErrContextRequired, err))
		}
		if !info.Mode().IsRegular() {
			return LaunchSpec{}, observability.WrapError(observability.CodeUsage,
				"claude settings path is not a regular file", ErrContextRequired)
		}
	}
	configDir, setConfigDir, err := ClaudeConfigDirForLaunch(spec.Config.ClaudeConfigDir)
	if err != nil {
		return LaunchSpec{}, observability.WrapError(observability.CodeUsage, "claude config directory", err)
	}
	envInput := append([]EnvVar(nil), spec.Env...)
	var unsetEnv []string
	if setConfigDir {
		envInput = append(envInput, EnvVar{Key: config.EnvClaudeConfigDir, Value: configDir})
	} else {
		unsetEnv = []string{config.EnvClaudeConfigDir}
	}
	env, err := filterLaunchEnv(envInput)
	if err != nil {
		return LaunchSpec{}, err
	}

	extra := make([]string, 0, 6)
	extra = append(extra, "--dangerously-skip-permissions")
	switch {
	case spec.ResumeSessionID != "":
		// --resume and --session-id are mutually exclusive on the Claude
		// CLI; resuming never carries --session-id (2.1.274 --help).
		extra = append(extra, "--resume", spec.ResumeSessionID, "--settings", spec.ClaudeSettingsPath)
	case spec.ClaudeSessionID != "":
		extra = append(extra, "--session-id", spec.ClaudeSessionID, "--settings", spec.ClaudeSettingsPath)
	}
	if spec.Role == RoleMate {
		// A coordinator must not inherit the captain's unrelated development
		// plugins, MCP servers and tools. Its project skills and explicit hook
		// settings still load. Keep a stable prefix for prompt caching.
		extra = append(extra, "--setting-sources", "project", "--strict-mcp-config",
			"--tools", "Bash,Read,Write,Edit,Glob,Grep,Skill", "--autocompact", "300000")
		if spec.Model == "" {
			spec.Model = "opus"
		}
		if spec.Effort == "" {
			spec.Effort = EffortMedium
		}
	}
	profile, effortOmitted := profileArgs(KindClaude, spec.Model, spec.Effort)
	extra = append(extra, profile...)
	// dangerousPermissionNotes records why --dangerously-skip-permissions is
	// carried and what it does not cover; see the analogous (not identical -
	// Codex carries its own extra bullets) record on the Codex adapter
	// (internal/harness/codex.go) for the sibling harness.
	dangerousPermissionNotes := []string{
		"the captain ruled on 2026-09-14 that Mate and Crew launches carry the harness permission bypass; there is no external sandbox of any kind, because a Crew runs on the operator's own machine on real project code",
		"--dangerously-skip-permissions suppresses per-tool permission prompts; Claude exposes no sandbox option to remove",
		"two separate one-time walls remain, neither cleared by this flag - naming only one, as an earlier version of this note did, is the exact understatement this record exists to correct: (1) Claude's per-absolute-path directory-trust confirmation, measured 2026-09-14; (2) --dangerously-skip-permissions itself raises a second, separate blocking 'Bypass Permissions mode' acceptance screen the first time a given CLAUDE_CONFIG_DIR sees it - measured 2026-09-14 on a linked worktree of an already-trusted primary repo with an isolated config dir, then persisted to that config dir's settings.json so it never reappears there (why a machine that accepted it long ago never surfaces it)",
		"whether mate should auto-accept that second screen for the operator is a separate open captain call (gomate-claude-bypass-mode-screen), not implemented here",
		"the bypass is unconditional: there is no config key or CLI flag to opt out, a deliberate choice consistent with unattended Crew/Mate operation",
	}
	out := LaunchSpec{
		kind:            string(KindClaude),
		cwd:             cwd,
		env:             env,
		contextFiles:    []GeneratedFile{{Path: path, Role: "canonical_context"}},
		contextPath:     path,
		contextRequired: true,
		maxInlineBytes:  max,
		taskPrompt:      spec.TaskPrompt,
		model:           spec.Model,
		effort:          spec.Effort,
		effortOmitted:   effortOmitted,
		claudeConfigDir: configDir,
		unsetEnv:        unsetEnv,
	}
	if spec.ManualInCwd {
		out.contextFiles = nil
		out.contextRequired = false
		out.delivery = DeliveryCwdManual
		out.args = extra
		out.notes = append(append([]string(nil), dangerousPermissionNotes...),
			"no context flag is passed: Claude Code loads CLAUDE.md from the directory it starts in, and the caller declared that file already loads the manual",
		)
		return finalize(out)
	}
	if spec.InlineFallback {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return LaunchSpec{}, observability.WrapError(observability.CodeUsage, path+" not found", ErrContextRequired)
			}
			return LaunchSpec{}, observability.WrapError(observability.CodeUsage, "stat "+path, fmt.Errorf("%w: %w", ErrContextRequired, err))
		}
		if info.Size() > int64(max) {
			return LaunchSpec{}, observability.WrapError(
				observability.CodeUsage,
				fmt.Sprintf("%s is %d bytes, inline limit %d", path, info.Size(), max),
				ErrContextTooLarge,
			)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return LaunchSpec{}, observability.WrapError(observability.CodeUsage, "read "+path, fmt.Errorf("%w: %w", ErrContextRequired, err))
		}
		out.delivery = DeliveryAppendSystemPrompt
		out.args = append(extra, "--append-system-prompt", string(data))
		out.notes = append(append([]string(nil), dangerousPermissionNotes...),
			"inline --append-system-prompt is a bounded fallback; the entire context is visible in the process table",
		)
	} else {
		out.delivery = DeliveryAppendSystemPromptFile
		out.args = append(extra, "--append-system-prompt-file", path)
		out.notes = append(append([]string(nil), dangerousPermissionNotes...),
			"default Claude delivery is --append-system-prompt-file; Claude exits 1 on a missing file, and mate still fails in BuildLaunchSpec first",
			"G5-12 live evidence proves Claude receives the locator flags through Herdr and fires the configured Stop hook",
		)
	}
	return finalize(out)
}
