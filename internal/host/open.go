package host

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/process"
)

// Options configure a host driver. Zero values pick production defaults.
type Options struct {
	Runner process.Runner
	Env    func(string) string
	// Pane is the host pane the console occupies. WezTerm reads WEZTERM_PANE
	// when this is empty.
	Pane string
	// Percent is the right-hand split size for WezTerm. Zero means 70.
	Percent   int
	Herdr     string
	WezTerm   string
	Osascript string
}

func (o Options) runner() process.Runner {
	if o.Runner != nil {
		return o.Runner
	}
	return process.ExecRunner{}
}

func (o Options) herdr() string {
	if o.Herdr != "" {
		return o.Herdr
	}
	return "herdr"
}

func (o Options) percent() int {
	if o.Percent > 0 {
		return o.Percent
	}
	return 70
}

func (o Options) getenv(key string) string {
	if o.Env == nil {
		return ""
	}
	return o.Env(key)
}

// Open returns a Host for kind, or nil when there is no driver (None, ITerm).
func Open(kind Kind, opt Options) Host {
	switch kind {
	case WezTerm:
		return newWezTerm(opt)
	case Ghostty:
		return newGhostty(opt)
	default:
		return nil
	}
}

func attachArgs(herdr, session, agent string) (string, []string) {
	if herdr == "" {
		herdr = "herdr"
	}
	// --takeover: the stage is where the captain asked to see this agent,
	// so it takes the agent's terminal from any client still holding it - a
	// stage pane Ghostty has not closed yet, or one left by an earlier run.
	// Without it herdr refuses ("already has an attached client") and the
	// new pane exits at once.
	return herdr, []string{"--session", session, "agent", "attach", agent, "--takeover"}
}

func attachCommand(herdr, session, agent string) string {
	name, args := attachArgs(herdr, session, agent)
	return name + " " + strings.Join(args, " ")
}

// resolveExec returns an absolute path for name so Ghostty's login wrapper
// (bash --noprofile --norc) can exec it. A name already absolute is kept.
// LookPath failure leaves the name unchanged.
func resolveExec(name string) string {
	if name == "" {
		name = "herdr"
	}
	if filepath.IsAbs(name) {
		return name
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return name
	}
	if filepath.IsAbs(p) {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// ghosttyAttachCommand is AppleScript surface configuration.command.
// Ghostty's embedding API always treats that field as a shell string
// (`exec -l <command>` under login + bash --noprofile --norc). A "direct:"
// prefix is the executable name, not a parser hint. The binary must be
// an absolute path because that bash has no user PATH.
func ghosttyAttachCommand(herdr, session, agent string) string {
	return attachCommand(herdr, session, agent)
}
