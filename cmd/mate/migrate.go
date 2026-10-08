package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/nguyenngocanh94/mate/internal/migrate"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdMigrate is `mate migrate [<workspace-dir>] [--dry-run]`: it moves a
// workspace written before layout 2 so every repo lives under its project's
// directory (internal/migrate), after repairing the links of a workspace
// that was moved or copied. It is the captain's: it moves the captain's
// directories, so a Mate or a Crew that runs it is refused.
func cmdMigrate(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate migrate [<workspace-dir>] [--dry-run]")
	}
	dryRun := fs.Bool("dry-run", false, "print what would move and why it would not, and change nothing")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return newUsageError("mate migrate: want at most 1 argument: <workspace-dir>")
	}
	if caller := spawn.CallerFromEnv(); caller != spawn.CallerUser {
		return observability.NewError(observability.CodePermission,
			fmt.Sprintf("the captain runs mate migrate; a %s cannot move the workspace's repos", caller))
	}
	dir, err := findWorkspaceDir(fs.Arg(0))
	if err != nil {
		return err
	}
	w, err := store.OpenForMigrate(dir)
	if err != nil {
		return err
	}
	live := baseDeps()
	spec, err := spawn.SessionSpec(live, w)
	if err != nil {
		return err
	}
	binary := live.Binary
	if binary == "" {
		binary, _ = os.Executable()
	}
	deps := migrate.Deps{Runtime: live.Runtime, Session: spec, Harnesses: live.Harnesses, Binary: binary}
	return runMigrate(context.Background(), w, deps, *dryRun, stdout)
}

// runMigrate is cmdMigrate's core over any deps, for tests.
func runMigrate(ctx context.Context, w *store.Workspace, deps migrate.Deps, dryRun bool, stdout io.Writer) error {
	if dryRun {
		_, err := migrate.DryRun(ctx, w, deps, stdout)
		return err
	}
	_, err := migrate.Run(ctx, w, deps, stdout)
	return err
}
