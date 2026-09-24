package main

import (
	"fmt"
	"io"
	"os"

	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The memory nudges (docs/mvp.md task 36, B9).
//
// Task 31 measured that the Mate follows an instruction in a command's
// output and forgets the same instruction in its manual. The two moments a
// lesson is born are closing a task and correcting a Crew after it was
// briefed, and the `shop` workspace lost lessons at exactly both
// (docs/research/firstmate-memory-2026-09-24.md section 1). So the two
// commands the Mate runs at those moments end with one line saying where the
// lesson goes. They print it only to the Mate: the captain running the same
// command from a shell has no memory.md to update.

// crewStopNudge ends a Mate's `mate crew stop`.
const crewStopNudge = "record: update backlog.md Done; if this task taught you anything durable, route it (skill stow)"

// sendCorrectionNudge ends a Mate's `mate send` to a Crew that already
// has its brief, which makes the line a correction after spawn.
const sendCorrectionNudge = "if this correction applies to future crews, route it: memory.md Lessons or propose it for CREW.md"

// writeCrewStopReport prints crew stop's outcome line and, when the Mate
// closed the task, the record nudge. A stop that changed nothing (the crew
// was already closed) has nothing new to record.
func writeCrewStopReport(stdout io.Writer, project, crew string, res spawn.StopResult, caller string) {
	fmt.Fprintln(stdout, crewStopReport(project, crew, res))
	if caller == spawn.CallerMate && !res.AlreadyClosed {
		fmt.Fprintln(stdout, crewStopNudge)
	}
}

// printSendCorrectionNudge prints the correction nudge when the Mate sent
// the line and the Crew has a brief on file.
func printSendCorrectionNudge(stdout io.Writer, w *store.Workspace, project, crew, source string) {
	if source != store.SourceMate {
		return
	}
	if _, err := os.Stat(w.CrewBrief(project, crew)); err != nil {
		return
	}
	fmt.Fprintln(stdout, sendCorrectionNudge)
}
