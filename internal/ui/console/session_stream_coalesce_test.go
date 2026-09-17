package console

import (
	"fmt"
	"strings"
	"testing"
)

// truecolorScreenBuffer paints every cell of a cols x rows screen with one
// truecolor SGR style. Truecolor is what makes the emulator-to-lipgloss
// translation visible in the allocator: cellLipglossStyle builds a fresh
// Style and allocates for every colour it sets, and an RGBA colour allocates
// where an ANSI index does not. One uniform style also means the screen
// coalesces into very few runs (one per row), which is the whole point: the
// translation must run per run, not per cell.
func truecolorScreenBuffer(cols, rows int) *TerminalBuffer {
	b := NewTerminalBuffer(cols, rows)
	var sb strings.Builder
	sb.WriteString("\x1b[2J\x1b[H\x1b[38;2;200;30;40m")
	line := strings.Repeat("x", cols)
	for y := 0; y < rows; y++ {
		fmt.Fprintf(&sb, "\x1b[%d;1H%s", y+1, line)
	}
	_, _ = b.Write([]byte(sb.String()))
	return b
}

// TestStreamFrameTranslatesOneStylePerCoalescedRun pins the allocation
// contract session_render.go's own comment states: cellLipglossStyle runs
// once per coalesced run, not once per cell. There is no production seam to
// count calls, so the observable is the allocator, which is deterministic for
// a fixed input.
//
// Measured on this tree (160x45 truecolor screen, 160x48 Mate frame):
//   - hoisted, as shipped: 9065 allocs/run
//   - per-cell (item 1's pre-fix shape): 23373 allocs/run
//
// 15000 sits well clear of both. A Go release that shifts absolute numbers
// will move both together, and this comment is where the next reader finds
// the two figures to re-measure - not a reason to delete the guard.
func TestStreamFrameTranslatesOneStylePerCoalescedRun(t *testing.T) {
	buf := truecolorScreenBuffer(160, 45)
	snapshot := SessionSnapshot{Target: SessionTarget{Kind: SessionTargetMate}}
	g, p := unicodeGlyphs, plainPalette()

	allocs := testing.AllocsPerRun(20, func() {
		_ = RenderStreamSessionFrame(snapshot, buf, false, 160, 48, g, p)
	})
	if allocs > 15000 {
		t.Errorf("stream frame allocated %.0f times/run; hoisted translation measures ~9065 and the per-cell shape ~23373 - cellLipglossStyle is running per cell again", allocs)
	}
}
