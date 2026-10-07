package main

import (
	"fmt"
	"io"
)

// The turn-end lines (docs/mvp.md tasks 31 and 57).
//
// The Mate is supposed to act and then end its turn, in every mode: a Mate
// inside a turn holds its own composer shut, so the captain's next line
// queues behind it (measured 2026-09-26/27, a Mate polling two Crews for nine
// minutes while the captain's messages waited), and the daemon's digest is
// refused every cycle. A Crew's news reaches the captain's box at once, and
// the Mate through a digest while the project is in auto mode.
//
// The manual says so and a Mate did not comply (task 24): the manual is read
// once at bootstrap and the tool output every time. So the reminder is the
// last line of the three commands a Mate runs at exactly the moments it is
// tempted to keep going. They start with turnLinePrefix, which is what the
// manual tells the Mate to look for.

// turnLinePrefix starts every turn-end line.
const turnLinePrefix = "turn: "

// turnSpawnLine ends `mate crew spawn`.
func turnSpawnLine(crew string) string {
	return fmt.Sprintf(turnLinePrefix+"end it now; when %s speaks it reaches the captain's box, and you as a digest. Do not poll.", crew)
}

// turnStateLine ends `mate state`.
const turnStateLine = turnLinePrefix + "do not poll; end it and a digest will wake you when a crew needs you."

// turnSendLine ends a Mate's `mate send`.
func turnSendLine(crew string) string {
	return fmt.Sprintf(turnLinePrefix+"end it now; a digest will tell you when %s hands back.", crew)
}

// turnRelaunchLine ends `mate crew relaunch`.
func turnRelaunchLine(crew string) string {
	return fmt.Sprintf(turnLinePrefix+"end it now; %s is running again and the console will wake you when it speaks.", crew)
}

// printTurnEnd writes line to stdout.
func printTurnEnd(stdout io.Writer, line string) {
	fmt.Fprintln(stdout, line)
}
