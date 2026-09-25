package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHost answers a font report the way Ghostty and WezTerm word theirs.
func fakeHost(t *testing.T, out string, err error) (runOutput, *[]string) {
	t.Helper()
	var calls []string
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, filepath.Base(name)+" "+strings.Join(args, " "))
		return []byte(out), err
	}, &calls
}

func envOf(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

// fakeBinary is an executable file standing in for the terminal's CLI.
func fakeBinary(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNerdIconsFollowGhosttysOwnFontReport(t *testing.T) {
	dir := filepath.Dir(fakeBinary(t, "ghostty"))
	env := envOf(map[string]string{"TERM_PROGRAM": "ghostty", "GHOSTTY_BIN_DIR": dir})

	run, calls := fakeHost(t, "U+EC82 «  » found in face “Symbols Nerd Font Mono”.", nil)
	if !probeNerdIcons(env, run) {
		t.Fatal("a font that has both marks did not turn the Nerd icons on")
	}
	if len(*calls) != 2 || !strings.Contains((*calls)[0], "+show-face --cp=0xec82") || !strings.Contains((*calls)[1], "--cp=0xec81") {
		t.Fatalf("asked Ghostty %q, want both codepoints", *calls)
	}

	// Ghostty 1.3.1 with MesloLGS NF, measured 2026-09-25: neither mark.
	run, _ = fakeHost(t, "U+EC82 «  » not found.", nil)
	if probeNerdIcons(env, run) {
		t.Fatal("a font without the marks turned the Nerd icons on; they would draw as boxes")
	}
}

func TestNerdIconsFollowWeztermsOwnFontReport(t *testing.T) {
	env := envOf(map[string]string{"WEZTERM_PANE": "3", "WEZTERM_EXECUTABLE_DIR": filepath.Dir(fakeBinary(t, "wezterm"))})
	have := " 0     \\u{ec82}     x_adv=8  cells=1  glyph=cod-claude,1  wezterm.font(\"Symbols Nerd Font Mono\")"
	if run, _ := fakeHost(t, have, nil); !probeNerdIcons(env, run) {
		t.Fatal("WezTerm reported the glyph and the icons stayed off")
	}
	// WezTerm with JetBrains Mono only, measured 2026-09-25.
	missing := "No fonts contain glyphs for these codepoints: \\u{ec82}.\n 0     \\u{ec82}     x_adv=7  cells=1  glyph=.notdef"
	if run, _ := fakeHost(t, missing, nil); probeNerdIcons(env, run) {
		t.Fatal("WezTerm reported .notdef and the icons turned on")
	}
}

func TestNerdIconsStayOffElsewhereAndOnFailure(t *testing.T) {
	run, calls := fakeHost(t, "found in face", nil)
	if probeNerdIcons(envOf(map[string]string{"TERM_PROGRAM": "Apple_Terminal"}), run) || len(*calls) != 0 {
		t.Fatalf("a terminal with no font report turned the icons on (calls %q)", *calls)
	}
	dir := filepath.Dir(fakeBinary(t, "ghostty"))
	run, _ = fakeHost(t, "found in face", errors.New("exit 1"))
	if probeNerdIcons(envOf(map[string]string{"TERM_PROGRAM": "ghostty", "GHOSTTY_BIN_DIR": dir}), run) {
		t.Fatal("a failed font report turned the icons on")
	}
}

func TestMateIconsOverridesTheProbe(t *testing.T) {
	run, calls := fakeHost(t, "", errors.New("must not run"))
	if !probeNerdIcons(envOf(map[string]string{"MATE_ICONS": "nerd"}), run) {
		t.Fatal("MATE_ICONS=nerd did not turn the icons on")
	}
	if probeNerdIcons(envOf(map[string]string{"MATE_ICONS": "unicode", "TERM_PROGRAM": "ghostty"}), run) {
		t.Fatal("MATE_ICONS=unicode did not keep the stand-ins")
	}
	if len(*calls) != 0 {
		t.Fatalf("an explicit MATE_ICONS still asked the host: %q", *calls)
	}
}
