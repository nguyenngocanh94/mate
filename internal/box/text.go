package box

import "github.com/rivo/uniseg"

// ellipsis marks a truncated line. This package does not import
// internal/ui/console (box must stay independent of it), so the two
// grapheme-aware width helpers it needs are reimplemented here on top of
// uniseg, the same library console's text.go uses.
const ellipsis = "…"

// cells is the terminal display width of s.
func cells(s string) int { return uniseg.StringWidth(s) }

// truncateEnd cuts s to at most w display cells, marking the cut with an
// ellipsis when there is room for one.
func truncateEnd(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if cells(s) <= w {
		return s
	}
	if w <= cells(ellipsis) {
		return cutCells(s, w)
	}
	return cutCells(s, w-cells(ellipsis)) + ellipsis
}

// cutCells returns the longest prefix of s that fits in w display cells,
// measured one grapheme cluster at a time so a multi-rune cluster (a flag,
// a ZWJ sequence, a base rune plus a variation selector) is never split.
func cutCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	used, pos := 0, 0
	state := -1
	for pos < len(s) {
		cluster, _, width, newState := uniseg.FirstGraphemeClusterInString(s[pos:], state)
		if used+width > w {
			return s[:pos]
		}
		used += width
		pos += len(cluster)
		state = newState
	}
	return s
}
