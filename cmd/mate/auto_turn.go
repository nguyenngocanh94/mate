package main

import (
	"fmt"
	"io"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// The auto-mode turn-end lines (docs/mvp.md task 31).
//
// In auto mode the Mate is supposed to act and then end its turn, so that
// the daemon (internal/autopilot) can wake it with a `digest:` line when a
// Crew speaks. A Mate that keeps supervising inside one turn holds its own
// composer Busy, the daemon's verified send is refused every cycle, and auto
// mode degenerates into a slower manual mode. Measured 2026-09-19 (task 24):
// the manual said so and the Mate did not comply in either run, because the
// manual is read once at bootstrap and the tool output is read every time.
//
// So the reminder goes where the Mate cannot miss it: as the last line of the
// three commands it runs at exactly the moments it is tempted to keep going.
// In manual mode none of them print anything extra, because there polling is
// the Mate's job (manual section 9).

// autoSpawnLine ends `mate crew spawn` in auto mode.
func autoSpawnLine(crew string) string {
	return fmt.Sprintf("auto mode: end your turn now; the console will wake you with a digest when %s speaks. Do not poll.", crew)
}

// autoStateLine ends `mate state` in auto mode.
const autoStateLine = "auto mode: do not poll; end your turn and wait for the digest."

// autoSendLine ends a Mate's `mate send` in auto mode.
func autoSendLine(crew string) string {
	return fmt.Sprintf("auto mode: end your turn; the digest will tell you when %s hands back.", crew)
}

// printAutoTurnEnd writes line to stdout when project is in auto mode, and
// nothing otherwise. The flag is read at the moment of printing, not when the
// command started: `crew spawn` can take minutes, and what the Mate should do
// next depends on the mode it is in when it reads the output.
func printAutoTurnEnd(stdout io.Writer, w *store.Workspace, project, line string) {
	if w.Auto(project) {
		fmt.Fprintln(stdout, line)
	}
}
