package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The Console draws the harness as its brand mark: Nerd Fonts 3.5's
// cod-claude (U+EC82) and cod-openai (U+EC81) when a font on the host has
// them, and the Unicode stand-ins ✻ ⌬ otherwise. Nothing inside a
// character grid can tell whether a font has a glyph, so mate asks the host
// terminal's own font report before the TUI starts: Ghostty's
// `+show-face`, WezTerm's `ls-fonts`. Anything else keeps the stand-ins
// unless MATE_ICONS=nerd says the font is there.

// nerdCodepoints are the two marks; both must be drawable.
var nerdCodepoints = []string{"ec82", "ec81"}

// iconProbeWait bounds the font report; a host that takes longer keeps
// the stand-ins.
const iconProbeWait = 2 * time.Second

// runOutput runs a command and returns its combined output.
type runOutput func(ctx context.Context, name string, args ...string) ([]byte, error)

func execOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// probeNerdIcons reports whether to draw the Nerd Font brand marks.
func probeNerdIcons(getenv func(string) string, run runOutput) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("MATE_ICONS"))) {
	case "nerd", "nerdfont", "nerd-font":
		return true
	case "unicode", "plain":
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), iconProbeWait)
	defer cancel()
	if bin := ghosttyBinary(getenv); bin != "" {
		for _, cp := range nerdCodepoints {
			out, err := run(ctx, bin, "+show-face", "--cp=0x"+cp)
			if err != nil || !strings.Contains(string(out), "found in face") {
				return false
			}
		}
		return true
	}
	if bin := weztermBinary(getenv); bin != "" {
		out, err := run(ctx, bin, "ls-fonts", "--text", "")
		text := string(out)
		return err == nil && strings.Contains(text, "glyph=") &&
			!strings.Contains(text, ".notdef") && !strings.Contains(text, "No fonts contain")
	}
	return false
}

// ghosttyBinary is the running Ghostty's CLI: GHOSTTY_BIN_DIR, which
// Ghostty sets for its shells, then the app bundle.
func ghosttyBinary(getenv func(string) string) string {
	if !strings.EqualFold(getenv("TERM_PROGRAM"), "ghostty") {
		return ""
	}
	for _, p := range []string{
		filepath.Join(getenv("GHOSTTY_BIN_DIR"), "ghostty"),
		"/Applications/Ghostty.app/Contents/MacOS/ghostty",
	} {
		if isExecutable(p) {
			return p
		}
	}
	return ""
}

// weztermBinary is the running WezTerm's CLI: `wezterm` in
// WEZTERM_EXECUTABLE_DIR, which WezTerm sets for its panes
// (WEZTERM_EXECUTABLE is the GUI, which has no ls-fonts).
func weztermBinary(getenv func(string) string) string {
	if getenv("WEZTERM_PANE") == "" {
		return ""
	}
	if p := filepath.Join(getenv("WEZTERM_EXECUTABLE_DIR"), "wezterm"); getenv("WEZTERM_EXECUTABLE_DIR") != "" && isExecutable(p) {
		return p
	}
	if p, err := exec.LookPath("wezterm"); err == nil {
		return p
	}
	return ""
}

func isExecutable(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}
