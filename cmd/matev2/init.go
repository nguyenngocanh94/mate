package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

// cmdInit implements `matev2 init [<dir>]`. It is idempotent: an existing
// workspace is opened rather than recreated.
func cmdInit(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: matev2 init [<dir>]")
	}
	if err := fs.Parse(args); err != nil {
		return &usageError{err}
	}
	if fs.NArg() > 1 {
		return newUsageError("usage: matev2 init [<dir>]")
	}
	dir := "."
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	w, err := store.Init(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "initialized workspace at %s\n", w.Root())
	return nil
}
