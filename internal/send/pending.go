package send

import (
	"strings"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// pendingMatches proves the entire visible composer, not the first line
// used for classification. Missing boundaries, collapsed paste placeholders,
// truncated snapshots and extra content all refuse recovery. A terminal
// snapshot cannot prove buffer identity against simultaneous human edits.
//
// The composer's rows, and where they end, are the harness's
// (harness.ScreenProfile.ComposerRows).
func pendingMatches(profile harness.ScreenProfile, screen, payload string) bool {
	if ClassifyComposer(profile, screen).State != StatePending {
		return false
	}
	rows, ok := profile.ComposerRows(strings.Split(StripSGR(screen), "\n"))
	if !ok {
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
