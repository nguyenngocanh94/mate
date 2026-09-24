package harness

import (
	"context"
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/matev2/internal/observability"
)

// Adapter validates a harness and builds a LaunchSpec. It never forks the
// harness process; RuntimeAdapter hands the spec to Herdr.
type Adapter interface {
	Validate(ctx context.Context, cfg Config) (CapabilitySet, error)
	BuildLaunchSpec(ctx context.Context, spec AgentSpec) (LaunchSpec, error)
}

// Config is the user/project harness configuration.
type Config struct {
	Kind       Kind
	Executable string
	Model      ModelRef
	// CodexHome is $CODEX_HOME used for global instruction discovery.
	// Empty means "not provided"; Codex validation then skips the global
	// file rather than guessing ~/.codex (which would mix operator files
	// into the chain silently).
	CodexHome string
	// ClaudeConfigDir is the optional configured CLAUDE_CONFIG_DIR. Empty means
	// use the provider default for transcript discovery and leave the launch
	// variable unset so Claude inherits its normal account identity.
	ClaudeConfigDir string
	// ProjectDocMaxBytes overrides the instruction-file cap the launch
	// meters against and hands Codex. Zero means CodexDefaultMaxBytes.
	ProjectDocMaxBytes int
	// FallbackFilenames are Codex project_doc_fallback_filenames. Empty
	// means only AGENTS.override.md / AGENTS.md, never an assumed TEAM_GUIDE.md.
	FallbackFilenames []string
}

// AgentSpec is the input to BuildLaunchSpec. Cwd is the directory the agent
// process will run in (Herdr pane cwd), not Mate's process cwd.
type AgentSpec struct {
	ID          string
	Role        AgentRole
	Kind        Kind
	Cwd         string
	ContextPath string
	// ClaudeSessionID and ClaudeSettingsPath are the launch-time Claude
	// transcript locator inputs. They are optional for callers that only
	// validate context delivery; an actual Mate/Crew launch supplies both so
	// the provider session identity and Stop-hook settings reach Claude in
	// the same argv passed through Herdr.
	ClaudeSessionID    string
	ClaudeSettingsPath string
	// ResumeSessionID requests resuming a previous harness session instead
	// of starting a fresh one (docs/mvp.md task 10). It is mutually
	// exclusive with ClaudeSessionID: a fresh id names the session a start
	// creates, a resume id names one that already exists. Claude resumes
	// with `--resume <id>` (still paired with --settings so the Stop hook
	// wires up the same as a fresh start); Codex with `codex resume <flags>
	// <id>`, which must be a UUID (task 35, measured on codex-cli 0.154.0).
	ResumeSessionID string
	// ManualInCwd says the agent's cwd already loads the operating manual on
	// its own, so the launch must not carry a context flag as well. Claude
	// Code reads `CLAUDE.md` from the directory it starts in, and a Mate's
	// cwd holds one that is exactly `@AGENTS.md`; passing that same manual
	// as --append-system-prompt-file delivered it twice (docs/mvp.md, "No ky
	// thuat"). With this set, ContextPath must be empty and the resulting
	// spec's delivery is DeliveryCwdManual. It is ignored by Codex, which
	// has no cwd auto-load of CLAUDE.md and whose one delivery mechanism is
	// itself a file in the cwd.
	ManualInCwd bool
	// InlineFallback requests Claude's bounded --append-system-prompt path.
	// Ignored for Codex. The constructor still fails closed if the file is
	// missing, empty, relative, or over budget.
	InlineFallback bool
	Config         Config
	Env            []EnvVar
	// TaskPrompt is a Crew's first user message. It is intentionally separate
	// from ContextPath: every harness instruction delivery is passive.
	TaskPrompt string
	Model      ModelRef
}

// CapabilitySet is what Validate returns. Unproven items stay marked.
type CapabilitySet struct {
	Kind                      Kind
	Deliveries                []Delivery
	DefaultDelivery           Delivery
	SupportsInlineFallback    bool
	GracefulStop              GracefulStop
	ProjectDocMaxBytes        int
	InlineExposesProcessTable bool
	Assumptions               []Assumption
}

// GracefulStop is harness-specific. Herdr has no `agent stop`.
type GracefulStop struct {
	Available bool
	Method    string // e.g. `herdr agent prompt <name> "/exit"`
	ProvenOn  string // e.g. "claude-code 2.1.251"
}

// Assumption mirrors runtime honesty for harness-side leftovers.
type Assumption struct {
	ID      string
	Status  string
	Summary string
}

const (
	AssumptionInlineProcessTable     = "inline_append_system_prompt_exposes_process_table"
	AssumptionClaudeArgsThroughHerdr = "claude_flags_survive_agent_start_dash_dash"
	AssumptionCodexConfigFallbacks   = "codex_config_toml_fallback_filenames_not_read_in_g2"
)

// ParseDelivery rejects unknown strategies.
func ParseDelivery(s string) (Delivery, error) {
	switch Delivery(strings.TrimSpace(s)) {
	case DeliveryAppendSystemPromptFile, DeliveryAppendSystemPrompt, DeliveryInstructionFile:
		return Delivery(strings.TrimSpace(s)), nil
	case "":
		return "", fmt.Errorf("delivery: empty")
	default:
		return "", observability.NewError(observability.CodeUsage, fmt.Sprintf("unknown delivery %q", s))
	}
}

// AdapterFor returns this binary's adapter for a harness kind. It is the one
// place the delivery matrix is resolved from a kind, so a caller cannot
// silently fall back to a default harness for a kind mate does not support.
func AdapterFor(kind Kind) (Adapter, error) {
	switch kind {
	case KindClaude:
		return Claude{}, nil
	case KindCodex:
		return Codex{}, nil
	default:
		return nil, observability.NewError(observability.CodeUsage,
			fmt.Sprintf("no harness adapter for kind %q", kind))
	}
}
