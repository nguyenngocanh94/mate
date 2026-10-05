package host

import "strings"

// Kind is which host terminal the console is running inside.
type Kind string

const (
	// None means no host driver: the console keeps the embedded PTY stream.
	None Kind = ""
	// WezTerm is wezterm's mux CLI ($WEZTERM_PANE).
	WezTerm Kind = "wezterm"
	// Ghostty is Ghostty 1.3+ AppleScript on macOS.
	Ghostty Kind = "ghostty"
	// ITerm is reserved; Open returns nil until a driver exists.
	ITerm Kind = "iterm"
)

// Detect inspects the environment and stops at the first match:
// WEZTERM_PANE, TERM_PROGRAM=ghostty, TERM_PROGRAM=iTerm.app.
func Detect(getenv func(string) string) Kind {
	if getenv == nil {
		return None
	}
	if strings.TrimSpace(getenv("WEZTERM_PANE")) != "" {
		return WezTerm
	}
	switch getenv("TERM_PROGRAM") {
	case "ghostty":
		return Ghostty
	case "iTerm.app":
		return ITerm
	}
	return None
}

// NoHostHint says how to get a next pane when Detect found no driver. Over
// ssh the host terminal is on the other machine and no variable crosses
// to say which one it is; WezTerm's ssh multiplexing gives the remote shell
// a pane of its own, Ghostty has no equivalent.
func NoHostHint(getenv func(string) string) string {
	if getenv != nil && (getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "") {
		return "over ssh, connect with wezterm connect SSHMUX:<host> instead"
	}
	return "run mate console inside WezTerm or Ghostty"
}
