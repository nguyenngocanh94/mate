package send

import (
	"strings"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// pendingMatches proves the entire visible composer, not the first line
// used for classification. Missing boundaries, collapsed paste placeholders,
// truncated snapshots and extra content all refuse recovery. A terminal
// snapshot cannot prove buffer identity against simultaneous human edits.
func pendingMatches(kind harness.Kind, screen, payload string) bool {
	classification, err := ClassifyComposer(kind, screen)
	if err != nil || classification.State != StatePending {
		return false
	}
	lines := strings.Split(StripSGR(screen), "\n")
	var rows []string
	switch kind {
	case harness.KindClaude:
		lines = splitAtClaudeRules(lines)
		for i := len(lines) - 1; i >= 1; i-- {
			rest, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), claudeComposerGlyph)
			if !ok || !isRule(lines[i-1]) {
				continue
			}
			rows = []string{rest}
			closed := false
			for _, line := range lines[i+1:] {
				if isRule(line) {
					closed = true
					break
				}
				rows = append(rows, line)
			}
			if !closed {
				return false
			}
			break
		}
	case harness.KindCodex:
		// The measured footer starts after an empty row and includes the
		// model/cwd separator. Without that boundary we cannot exclude a
		// clipped continuation, even if the first row matches perfectly.
		end := -1
		for i := len(lines) - 1; i >= 1; i-- {
			if strings.TrimSpace(lines[i-1]) == "" && strings.HasPrefix(lines[i], "  ") && strings.Contains(lines[i], " · ") {
				end = i
				break
			}
		}
		if end < 0 {
			return false
		}
		for i := end - 1; i >= 0; i-- {
			if rest, ok := strings.CutPrefix(lines[i], codexComposerGlyph); ok {
				rows = append([]string{rest}, lines[i+1:end]...)
				break
			}
		}
	default:
		return false
	}
	remaining := strings.TrimSpace(payload)
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1] // swallowed Enters / composer padding
	}
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row == "" {
			return false // an interior blank line is a payload edit
		}
		if !strings.HasPrefix(remaining, row) {
			return false
		}
		remaining = strings.TrimPrefix(remaining, row)
		// A word wrap may consume the separating space. Preserve all
		// whitespace inside each row, unlike a whitespace-stripped compare.
		remaining = strings.TrimPrefix(remaining, " ")
	}
	return len(rows) > 0 && remaining == ""
}
