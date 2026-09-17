// Command matev2 is the single binary for the matev2 workspace orchestrator.
//
// `matev2 <workspace-dir>` opens the console for a workspace.
// Every other subcommand is the agent-facing CLI that Mates and Crews call
// from inside their Herdr panes.
package main

import (
	"fmt"
	"os"

	"github.com/nguyenngocanh94/matev2/internal/config"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "matev2:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr *os.File) error {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-V") {
		fmt.Fprintf(stdout, "matev2 %s (%s, %s)\n", config.Version, config.Commit, config.BuildDate)
		return nil
	}
	fmt.Fprintln(stderr, "usage: matev2 <workspace-dir> | matev2 <subcommand> ...")
	fmt.Fprintln(stderr, "subcommands are added per docs/mvp.md task; nothing is wired yet")
	return fmt.Errorf("no subcommands implemented")
}
