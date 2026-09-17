package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/harness"
	"github.com/nguyenngocanh94/matev2/internal/observability"
)

// Herdr error codes observed on 0.8.2. agent_not_found and
// agent_pane_not_found stay distinct: a missing agent name is a target-identity
// problem (not_found); a missing pane was a wrong-session/usage problem.
const (
	HerdrAgentNotFound     = "agent_not_found"
	HerdrAgentPaneNotFound = "agent_pane_not_found"
	HerdrAgentBlocked      = "agent_blocked"
	HerdrAgentNotReady     = "agent_not_ready"
	HerdrTimeout           = "timeout"
	HerdrAgentNameTaken    = "agent_name_taken"
	HerdrInvalidAgentName  = "invalid_agent_name"
	HerdrWorkspaceNotFound = "workspace_not_found"
	// HerdrTabNotFound is `tab close <id>` on a tab that no longer exists -
	// including a tab whose sole pane was already closed by `pane close`,
	// which closes the tab (and a sole tab's workspace) with it. Measured
	// live on Herdr 0.8.2, 2026-09-14 (ADR 0028).
	HerdrTabNotFound         = "tab_not_found"
	HerdrInvalidAgentTimeout = "invalid_agent_timeout"
	HerdrAgentPaneBusy       = "agent_pane_busy"
	// HerdrPaneNotFound is `pane get <id>` on a pane that does not exist
	// (live 0.8.2). It is a plain missing target and must not be folded into
	// agent_pane_not_found, which G1 observed as a wrong-session/usage
	// problem on `agent start --pane`.
	HerdrPaneNotFound = "pane_not_found"
	// HerdrServerNotRunning is any command against a named session whose
	// server is down (live 0.8.2, exit 1). The Herdr side is unavailable,
	// not the target missing.
	HerdrServerNotRunning = "server_not_running"
)

// MapHerdrError maps a Herdr CLI error.code onto the agent taxonomy in
// docs/phase1/agent.md section 4. Unknown Herdr codes stay unknown.
//
// A blocked wait that *succeeds* (exit 0, matching --until blocked) is not
// an error and must not go through this mapper.
func MapHerdrError(code string) observability.Code {
	switch code {
	case HerdrAgentNotFound, HerdrWorkspaceNotFound, HerdrPaneNotFound, HerdrTabNotFound:
		return observability.CodeNotFound
	case HerdrAgentPaneNotFound:
		return observability.CodeUsage
	case HerdrAgentBlocked, HerdrAgentNotReady:
		return observability.CodeTargetBlocked
	case HerdrTimeout:
		return observability.CodeTimeout
	case HerdrAgentNameTaken:
		return observability.CodeAlreadyExists
	case HerdrInvalidAgentName, HerdrInvalidAgentTimeout:
		return observability.CodeUsage
	case HerdrAgentPaneBusy:
		return observability.CodeStateConflict
	case HerdrServerNotRunning:
		return observability.CodeRuntimeUnavailable
	default:
		return observability.CodeUnknown
	}
}

// IsAgentNotFound reports whether err is Herdr's missing-name failure
// (agent_not_found), not a missing pane. Stop confirmation uses this: a
// name that is gone is the only proof the agent is not running.
func IsAgentNotFound(err error) bool {
	if err == nil {
		return false
	}
	var coded *observability.Error
	if !errors.As(err, &coded) {
		return false
	}
	code, _ := coded.Details["herdr_code"].(string)
	return code == HerdrAgentNotFound
}

// IsTabGone reports whether err is Herdr saying the tab a caller asked to
// close no longer exists (tab_not_found, or pane_not_found for its pane). For
// a removal that is the end state the caller wanted, so compensation treats
// it as done; a force stop of a sole pane produces exactly this on the
// `tab close` that follows it.
func IsTabGone(err error) bool {
	if err == nil {
		return false
	}
	var coded *observability.Error
	if !errors.As(err, &coded) {
		return false
	}
	code, _ := coded.Details["herdr_code"].(string)
	return code == HerdrTabNotFound || code == HerdrPaneNotFound
}

// NewHerdrError wraps a Herdr code as a coded mate error. The original Herdr
// code is kept in details so callers can tell pane-not-found from agent-not-found
// after both have been mapped.
func NewHerdrError(herdrCode, message string) *observability.Error {
	tax := MapHerdrError(herdrCode)
	if message == "" {
		message = herdrCode
	}
	return observability.NewError(tax, message).WithDetails(map[string]any{
		"herdr_code": herdrCode,
	})
}

// MapSessionStreamError preserves the established runtime taxonomy for the
// PTY stream boundary. Coded errors (including agent_not_found,
// runtime_unavailable and timeout) pass through unchanged; cancellation and
// deadline errors (both context's and a raw transport's, e.g. a PTY/socket
// SetReadDeadline) remain distinguishable sentinel errors so callers can tell
// them apart with errors.Is rather than have them collapsed. io.EOF is a
// normal end-of-stream signal, not a runtime failure, and also passes through
// unchanged. Only a genuinely untyped transport failure becomes an unavailable
// runtime rather than a generic unclassified error.
func MapSessionStreamError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, os.ErrDeadlineExceeded) ||
		errors.Is(err, io.EOF) {
		return err
	}
	var coded *observability.Error
	if errors.As(err, &coded) {
		return err
	}
	return observability.WrapError(
		observability.CodeRuntimeUnavailable,
		"session stream "+operation+" failed",
		err,
	)
}

// SessionBeforeTerminator reports whether --session appears in the option
// region (before `--`). Extra harness args after `--` swallow a trailing
// --session; G1 observed that as agent_pane_not_found. Production argv must
// put --session before --.
func SessionBeforeTerminator(args []string) bool {
	inOptions := true
	for i, a := range args {
		if a == "--" {
			inOptions = false
			continue
		}
		if !inOptions {
			continue
		}
		if a == "--session" {
			if i+1 >= len(args) || args[i+1] == "--" {
				return false
			}
			return true
		}
	}
	return false
}

// AgentStartCommand is a constructed `herdr agent start` invocation. Its
// fields are unexported, so only NewAgentStartCommand can fill them; the
// zero value Go still allows emits a nil argv rather than one carrying an
// empty --session or --pane (G1 observed a lost --session as
// agent_pane_not_found).
type AgentStartCommand struct {
	session string
	name    string
	kind    string
	pane    string
	timeout time.Duration
	extra   []string
}

// Herdr 0.8.2 `agent start --timeout` bounds from the CLI help, live-verified
// in ADR 0003: the value must be > 3000 and <= 300000 ms. Zero means the flag
// is not emitted at all.
const (
	startTimeoutMinExclusiveMS = 3000
	startTimeoutMaxMS          = 300000
)

// CheckStartTimeout enforces Herdr's documented --timeout range on the value
// the argv would actually emit (whole milliseconds). Zero is valid and means
// no --timeout flag.
func CheckStartTimeout(timeout time.Duration) error {
	if timeout == 0 {
		return nil
	}
	ms := timeout.Milliseconds()
	if ms <= startTimeoutMinExclusiveMS || ms > startTimeoutMaxMS {
		return observability.NewError(
			observability.CodeUsage,
			fmt.Sprintf("agent start --timeout must be > %d and <= %d ms (Herdr CLI contract), got %d ms", startTimeoutMinExclusiveMS, startTimeoutMaxMS, ms),
		)
	}
	return nil
}

// NewAgentStartCommand fails closed on any missing required value or a
// --timeout outside Herdr's documented range.
func NewAgentStartCommand(session, name, kind, pane string, timeout time.Duration, extra []string) (AgentStartCommand, error) {
	required := []struct {
		field string
		value string
	}{
		{"session", session},
		{"name", name},
		{"kind", kind},
		{"pane", pane},
	}
	for _, r := range required {
		if strings.TrimSpace(r.value) == "" {
			return AgentStartCommand{}, observability.NewError(
				observability.CodeUsage,
				fmt.Sprintf("agent start requires a non-empty %s", r.field),
			)
		}
	}
	if err := CheckStartTimeout(timeout); err != nil {
		return AgentStartCommand{}, err
	}
	// Herdr receives kind verbatim as --kind; an unknown kind is an
	// invocation Herdr rejects, so the constructor refuses it.
	parsedKind, err := harness.ParseKind(kind)
	if err != nil {
		return AgentStartCommand{}, observability.WrapError(observability.CodeUsage, "agent start --kind", err)
	}
	return AgentStartCommand{
		session: session,
		name:    name,
		kind:    parsedKind.String(),
		pane:    pane,
		timeout: timeout,
		extra:   append([]string(nil), extra...),
	}, nil
}

// Argv emits `herdr agent start` with --session before any `--` extra args.
// extra are harness CLI args (for example Claude's
// --session-id, --settings and --append-system-prompt-file). The pinned
// Herdr path and child-side flag survival are proven by G5-12 live evidence.
// The argv shape remains locked so future changes do not
// copy the lab helper's append-at-end pattern. --timeout is included when set; whether start timeout fires if the
// process never reaches blocked or idle is unproven
// (AssumptionStartTimeoutNeverReady).
//
// Go cannot stop an outside caller holding the zero AgentStartCommand, so a
// command that did not pass NewAgentStartCommand's validation emits nil
// rather than an argv with empty required values (G1 observed a lost
// --session as agent_pane_not_found).
func (c AgentStartCommand) Argv() []string {
	if c.session == "" || c.name == "" || c.kind == "" || c.pane == "" {
		return nil
	}
	args := []string{
		"agent", "start", c.name,
		"--kind", c.kind,
		"--pane", c.pane,
		"--session", c.session,
	}
	if c.timeout > 0 {
		args = append(args, "--timeout", fmt.Sprintf("%d", c.timeout.Milliseconds()))
	}
	if len(c.extra) > 0 {
		args = append(args, "--")
		args = append(args, c.extra...)
	}
	return args
}

// AgentStartArgv builds the argv for a start with no timeout.
func AgentStartArgv(session, name, kind, pane string, extra []string) ([]string, error) {
	return AgentStartArgvTimeout(session, name, kind, pane, 0, extra)
}

// AgentStartArgvTimeout builds the argv, including --timeout when set.
func AgentStartArgvTimeout(session, name, kind, pane string, timeout time.Duration, extra []string) ([]string, error) {
	cmd, err := NewAgentStartCommand(session, name, kind, pane, timeout, extra)
	if err != nil {
		return nil, err
	}
	return cmd.Argv(), nil
}

// NamedSocketPath is the Herdr 0.8.2 socket for a non-default named session.
// Session must be a single path segment; traversal is rejected.
func NamedSocketPath(configHome, session string) string {
	p, err := namedSocketPath(configHome, session)
	if err != nil {
		return ""
	}
	return p
}

func namedSocketPath(configHome, session string) (string, error) {
	if err := checkSessionName(session); err != nil {
		return "", err
	}
	base := filepath.Join(configHome, "herdr", "sessions")
	dir := filepath.Clean(filepath.Join(base, session))
	rel, err := filepath.Rel(base, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel != session {
		return "", fmt.Errorf("session: socket path escapes herdr/sessions")
	}
	return filepath.Join(dir, "herdr.sock"), nil
}

func unixSocketPathLimit() int {
	return len(syscall.RawSockaddrUnix{}.Path)
}

func checkUnixSocketPath(p string) error {
	limit := unixSocketPathLimit()
	if len(p)+1 > limit {
		return observability.NewError(
			observability.CodeUsage,
			fmt.Sprintf("herdr socket path is %d bytes; sockaddr_un.sun_path holds %d including the trailing NUL (shorten the config home)", len(p), limit),
		)
	}
	return nil
}

// DefaultSocketPath is the Herdr 0.8.2 socket for the default session.
// Mate must not use the default session as a workspace mapping.
func DefaultSocketPath(configHome string) string {
	return configHome + "/herdr/herdr.sock"
}
