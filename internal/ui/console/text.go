package console

import (
	"strings"

	"github.com/rivo/uniseg"
)

// Truncation and abbreviation. Four rules, each with a different reason,
// and none of them applies to the inspector's own values except wrapping:
//
//   - IDs in a list are abbreviated head-and-tail, so two IDs remain
//     distinguishable by one end or the other.
//   - Titles in a list are cut at the end with an ellipsis. The inspector
//     shows the full title.
//   - Paths and branches are never cut; they wrap after a '/' so a suffix
//     ("/a1" vs "/a2") and an embedded ID stay intact.
//   - A line of independent items drops whole items in priority order
//     rather than cutting one in half.

// idHead and idTail are the two ends an abbreviated ID keeps. mate's own
// ids (internal/domain/id.go) are prefix_ plus 16 hex characters from
// crypto/rand, not a ULID - there is no timestamp to read from the head.
// Keeping both ends is collision-resistant, not injective: two ids that
// happen to agree on the first idHead and last idTail characters still
// abbreviate identically, and only the inspector's full id resolves that.
const (
	idHead = 6
	idTail = 4
)

// shortID abbreviates a prefixed identifier for a list column:
// "crew_01J9P6Q6W0E5V8XK2M4B8DT" becomes "crew_01J9P6…B8DT". The prefix up
// to and including the first '_' is kept whole - it says what kind of thing
// this is - and the body keeps its first idHead and last idTail
// characters, which is collision-resistant rather than a guarantee that two
// distinct ids always abbreviate distinctly. An ID that would not get
// shorter is returned unchanged.
func shortID(id string, g glyphSet) string {
	prefix, body := "", id
	if i := strings.Index(id, "_"); i >= 0 {
		prefix, body = id[:i+1], id[i+1:]
	}
	runes := []rune(body)
	if len(runes) <= idHead+idTail+len([]rune(g.Ellipsis)) {
		return id
	}
	return prefix + string(runes[:idHead]) + g.Ellipsis + string(runes[len(runes)-idTail:])
}

// truncateEnd cuts s to at most w display cells, marking the cut with the
// glyph set's ellipsis. Lists only: an inspector value is wrapped, never
// cut. When w is too small for even the ellipsis, the text is cut without
// one rather than replaced by a marker that says nothing.
func truncateEnd(s string, w int, g glyphSet) string {
	if w <= 0 {
		return ""
	}
	if cells(s) <= w {
		return s
	}
	if w <= cells(g.Ellipsis) {
		return cutCells(s, w)
	}
	return cutCells(s, w-cells(g.Ellipsis)) + g.Ellipsis
}

// oneLine collapses an error's message onto a single line: a wrapped
// filesystem error can carry an embedded newline (a multi-line os.PathError
// chain, for instance), and the header (frame.go's "stale" wording) is one
// line of chrome that must stay one line of chrome.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// cutCells returns the longest prefix of s that fits in w display cells,
// measured one grapheme cluster at a time. Summing per-rune widths instead
// (as this once did) disagrees with cells' own measurement of a cluster
// whose runes are not each independently as wide as the cluster is
// together - a base rune plus a variation selector, a flag, a ZWJ family -
// and either splits the cluster mid-way or over/under-counts it.
func cutCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	used, pos := 0, 0
	state := -1
	for pos < len(s) {
		cluster, _, _, newState := uniseg.FirstGraphemeClusterInString(s[pos:], state)
		if used+cells(cluster) > w {
			return s[:pos]
		}
		used += cells(cluster)
		pos += len(cluster)
		state = newState
	}
	return s
}

// graphemeClusters splits s into its grapheme clusters, so a caller that
// needs to measure and break text can do both on the same units cells (and
// therefore the frame contract) measures, rather than on bare runes.
func graphemeClusters(s string) []string {
	var out []string
	state := -1
	for len(s) > 0 {
		cluster, rest, _, newState := uniseg.FirstGraphemeClusterInString(s, state)
		out = append(out, cluster)
		s = rest
		state = newState
	}
	return out
}

// wrapAfterSlash breaks s into lines of at most w cells without cutting a
// token in half. A break is taken at the last space inside the window (the
// space is dropped) or immediately after the last '/' (the slash stays on
// the line it ends), so a path's trailing segment - "/a1" against "/a2", or
// a crew ID inside a worktree path - moves to the next line whole. Only a
// single token longer than the whole window is split, because there is
// nowhere else for it to go.
func wrapAfterSlash(s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}
	clusters := graphemeClusters(s)
	if len(clusters) == 0 {
		return []string{""}
	}
	var out []string
	for i := 0; i < len(clusters); {
		end, width := i, 0
		for end < len(clusters) {
			cw := cells(clusters[end])
			if width+cw > w {
				break
			}
			width += cw
			end++
		}
		if end == i {
			// A single cluster wider than the whole window: put it on its
			// own (overflowing) line rather than looping without progress.
			end = i + 1
		}
		if end >= len(clusters) {
			out = append(out, strings.Join(clusters[i:end], ""))
			break
		}
		// Scan back from the window edge for the latest legal break.
		brk := -1
		for k := end; k > i; k-- {
			if clusters[k-1] == " " {
				brk = k - 1
				break
			}
			if clusters[k-1] == "/" && k < end {
				brk = k
				break
			}
		}
		if brk > i {
			end = brk
		}
		out = append(out, strings.Join(clusters[i:end], ""))
		i = end
		for i < len(clusters) && clusters[i] == " " {
			i++
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// wrapSpans is wrapAfterSlash for a styled value: it wraps the spans as
// one string and hands back one span slice per line, so a value that
// changes style part-way through - a status word followed by a dim reason,
// an availability marker followed by why the read failed - keeps its
// colours across the break.
//
// This is the inspector's wrap. The inspector never truncates a value, so
// every styled field goes through here rather than through the line
// primitive's own edge truncation.
func wrapSpans(spans []span, w int) [][]span {
	type cell struct {
		r  rune
		sp span
	}
	var cs []cell
	for _, s := range spans {
		for _, r := range s.text {
			cs = append(cs, cell{r: r, sp: s})
		}
	}
	if len(cs) == 0 {
		return [][]span{nil}
	}
	var text []rune
	for _, c := range cs {
		text = append(text, c.r)
	}
	var out [][]span
	i := 0
	for _, chunk := range wrapAfterSlash(string(text), w) {
		n := len([]rune(chunk))
		// wrapAfterSlash drops the spaces it breaks at, so advance past any
		// run of spaces before matching the next chunk.
		for i < len(cs) && n > 0 && cs[i].r == ' ' && chunk != "" && []rune(chunk)[0] != ' ' {
			i++
		}
		var lineSpans []span
		for k := 0; k < n && i < len(cs); k++ {
			c := cs[i]
			i++
			if last := len(lineSpans) - 1; last >= 0 && lineSpans[last].plain == c.sp.plain && sameStyle(lineSpans[last], c.sp) {
				lineSpans[last].text += string(c.r)
				continue
			}
			lineSpans = append(lineSpans, span{text: string(c.r), style: c.sp.style, plain: c.sp.plain})
		}
		out = append(out, lineSpans)
	}
	if len(out) == 0 {
		return [][]span{nil}
	}
	return out
}

// sameStyle reports whether two spans can be merged into one run. Styles
// are compared by rendered effect rather than by field, which is the only
// comparison lipgloss offers.
func sameStyle(a, b span) bool {
	return a.style.Render("x") == b.style.Render("x")
}

// dropPriority keeps the leading items that fit in w cells once joined by
// sep, and drops the rest whole. Items must already be ordered by urgency:
// the first item that does not fit ends the line, so a short low-priority
// item never displaces a longer high-priority one that was dropped.
func dropPriority(items []string, w int, sep string) []string {
	kept := make([]string, 0, len(items))
	used := 0
	for _, it := range items {
		need := cells(it)
		if len(kept) > 0 {
			need += cells(sep)
		}
		if used+need > w {
			break
		}
		used += need
		kept = append(kept, it)
	}
	return kept
}

// joinDropped is dropPriority plus the join, for the common case.
func joinDropped(items []string, w int, sep string) string {
	return strings.Join(dropPriority(items, w, sep), sep)
}
