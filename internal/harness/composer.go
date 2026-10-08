package harness

// Each harness package's screen.go locates its composer and its in-flight
// signature; what they share is how far up the snapshot the busy scan
// reads. Measured 2026-09-17 against Claude Code 2.1.274 and codex-cli
// 0.154.0 through `herdr agent read --source recent-unwrapped --lines 40`;
// the captures are internal/screen/fixture/testdata/screens and
// TestClassifyComposerOnCapturedScreens runs the classifiers over them.
//
// `--source recent-unwrapped --format text` hands mate plain text with the
// ANSI styling already gone, so firstmate's dim-ghost stripping
// (fm_tmux_strip_ghost) has nothing to strip here: a placeholder is
// recognised by its measured wording instead of by its SGR 2 attribute.
const (
	// BusyTailLines bounds the busy scan to the bottom of the snapshot, so
	// a transcript line that happens to quote a spinner or an interrupt
	// hint does not make an idle pane look busy forever.
	BusyTailLines = 20
)

// BusyTail bounds the busy scan to the bottom of the snapshot.
func BusyTail(lines []string) []string {
	if len(lines) <= BusyTailLines {
		return lines
	}
	return lines[len(lines)-BusyTailLines:]
}
