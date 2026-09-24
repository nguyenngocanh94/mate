// Command mate is the single binary for the mate workspace orchestrator.
//
// `mate <workspace-dir>` opens the console for a workspace (not yet
// implemented). Every other subcommand is the agent-facing CLI that Mates
// and Crews call from inside their Herdr panes.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(mainRun(os.Args[1:], os.Stdout, os.Stderr))
}

// mainRun runs the CLI and returns the process exit code: 0 on success, 2 for
// a usage error, 1 for any other error.
func mainRun(args []string, stdout, stderr io.Writer) int {
	err := run(args, stdout, stderr)
	if err == nil {
		return 0
	}
	fmt.Fprintln(stderr, "mate:", err)
	var ue *usageError
	if errors.As(err, &ue) {
		return 2
	}
	return 1
}
