// Package brieftest builds briefs that pass internal/brief's check, for tests
// that spawn a Crew to exercise something other than the brief: the worktree
// saga, the observer, send, merge. Since `crew spawn` refuses any brief
// without the M7 shape, those tests need one; wrapping their one-line
// instruction here keeps the instruction verbatim and in one place, as both
// the captain's words and the whole of the build.
package brieftest

import (
	"fmt"
	"strings"
)

// Ship returns a ship brief whose `## Captain's words` and `## Build` are
// instruction, word for word.
func Ship(instruction string) string {
	instruction = strings.TrimSpace(instruction)
	return fmt.Sprintf(`## Captain's words
%[1]s

## What we already know
- This task was written by a test, not by the captain.
- Unknown: everything else.

## Build
%[1]s
- Out of scope: anything the words above do not ask for.

## Acceptance
- The words above are done exactly as written. verify: read your own pane and the status file.

## Open decisions
none
`, instruction)
}

// Scout returns a scout brief for instruction whose deliverable answers
// question.
func Scout(instruction, question string) string {
	return Ship(instruction) + "\n## Deliverable\n- " + strings.TrimSpace(question) + "\n"
}
