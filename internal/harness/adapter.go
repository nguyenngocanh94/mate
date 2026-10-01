package harness

import (
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// Config is the user/project harness configuration.
type Config struct {
	Kind       Kind
	Executable string
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
	// Model and Effort are the launch profile (profile.go): empty is the
	// harness's own default. An effort the harness does not take stays on
	// the spec, so it is recorded, and is left out of the argv.
	Model  string
	Effort Effort
}

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
