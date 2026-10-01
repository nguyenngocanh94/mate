package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

// cmdMerge implements `mate merge <project> <crew>`: docs/mvp.md task 22.
//
// It carries no `--force`, no `--no-ff` and no `--discard`. Every way a
// merge can go wrong is a refusal that changes nothing and says what to do
// instead, and a flag that turned any of them off would be a flag for
// landing work nobody reviewed.
//
// Who is asking is read from the pane environment rather than from a flag:
// spawn injects MATE_CALLER=mate into a Mate's pane and
// MATE_CALLER=crew into a Crew's, so a Mate cannot claim to be the
// captain by passing a different word on the command line.
func cmdMerge(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate merge <project> <crew> [--workspace <dir>]")
	}
	reviewFlag := fs.String("review", "", "require a passing review of this exact branch and contract")
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate merge: want exactly 2 arguments: <project> <crew>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	project, crew := fs.Arg(0), fs.Arg(1)
	var reviewed []spawn.ReviewedCommit
	if *reviewFlag != "" {
		fp, _, err := checkReview(context.Background(), w, gitx.New(), project, crew, *reviewFlag)
		if err != nil {
			return err
		}
		reviewed = append(reviewed, spawn.ReviewedCommit{Head: fp["head"], Base: fp["base"]})
	}
	res, err := spawn.MergeCrew(context.Background(), w, spawn.LiveDeps(harnesses), project, crew, spawn.CallerFromEnv(), reviewed...)
	if err != nil {
		// Including the one failure that is not a refusal: a teardown that
		// failed after the fast-forward landed. MergeCrew's error already
		// says the merge is done and the crew is not closed, which is the
		// whole of what the caller needs, so nothing is added here.
		return err
	}
	fmt.Fprintln(stdout, res.Line())
	return nil
}
