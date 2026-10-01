package harness

// The composer half of the screen profiles (screen.go): where each harness
// draws its composer, what an empty one holds, and what a turn in flight
// looks like. Moved from internal/send in plan PR 3 with the verdicts
// unchanged; send's ClassifyComposer is the policy that reads them.

// Measured 2026-09-17 against Claude Code 2.1.274 and codex-cli 0.154.0
// through `herdr agent read --source recent-unwrapped --lines 40`; the
// captures are internal/send/testdata/screens and
// TestClassifyComposerOnCapturedScreens runs this classifier over them.
//
// `--source recent-unwrapped --format text` hands mate plain text with the
// ANSI styling already gone, so firstmate's dim-ghost stripping
// (fm_tmux_strip_ghost) has nothing to strip here: a placeholder is
// recognised by its measured wording instead of by its SGR 2 attribute.
const (
	// busyTailLines bounds the busy scan to the bottom of the snapshot, so
	// a transcript line that happens to quote a spinner or an interrupt
	// hint does not make an idle pane look busy forever.
	busyTailLines = 20
)

// busyTail bounds the busy scan to the bottom of the snapshot.
func busyTail(lines []string) []string {
	if len(lines) <= busyTailLines {
		return lines
	}
	return lines[len(lines)-busyTailLines:]
}
