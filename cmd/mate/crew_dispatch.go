package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/nguyenngocanh94/mate/internal/dispatch"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The crew dispatch table (.mate/crew-dispatch.json, internal/dispatch) is
// firstmate's: natural-language rules the Mate matches with its own
// judgment, each naming a concrete harness, model and effort. The app never
// matches a rule. It checks the table, prints it for the Mate, and fills a
// spawn that named no harness from the table's default. A workspace without
// the file is governed by the built-in table.

// applyDispatch is the spawn's gate on the table. A malformed table stops
// every spawn until it is corrected. A spawn that named its harness keeps
// its profile as given. One that did not gets the table's default profile
// on the workspace's default harness, and note says so; --model or --effort
// without --harness is refused, since a model name belongs to one harness.
func applyDispatch(w *store.Workspace, req spawn.SpawnCrewRequest) (spawn.SpawnCrewRequest, string, error) {
	t, err := dispatch.Resolve(w.StateDir())
	if errors.Is(err, dispatch.ErrInvalid) {
		// Exit 2 like the bad flags below: both are "fix the table or the
		// invocation and run it again", never a crash.
		return req, "", &usageError{err}
	}
	if err != nil {
		return req, "", err
	}
	if req.Harness != "" {
		return req, "", nil
	}
	if req.Model != "" || req.Effort != "" {
		return req, "", newUsageError("mate crew spawn: --model and --effort need --harness; pick a whole profile from `mate crew dispatch`")
	}
	p, ok := t.DefaultFor(w.Defaults().CrewHarness)
	if !ok {
		return req, "", nil
	}
	req.Harness, req.Model, req.Effort = p.Harness, p.Model, p.Effort
	return req, fmt.Sprintf("no --harness; used the %s default: %s", tableName(t), p.Flags()), nil
}

// tableName is how output names the table in force.
func tableName(t dispatch.Table) string {
	if t.BuiltIn {
		return "built-in dispatch table's"
	}
	return "dispatch table's (" + t.Path + ")"
}

// cmdCrewDispatch implements `mate crew dispatch [--workspace <dir>] [--example]`.
func cmdCrewDispatch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew dispatch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate crew dispatch [--workspace <dir>] [--example]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	exampleFlag := fs.Bool("example", false, "print the built-in table as JSON, to copy into .mate/crew-dispatch.json and edit")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return newUsageError("mate crew dispatch: takes no arguments")
	}
	if *exampleFlag {
		_, err := io.WriteString(stdout, dispatch.BuiltInJSON)
		return err
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	t, err := dispatch.Resolve(w.StateDir())
	if err != nil {
		return err
	}
	writeDispatchTable(stdout, t)
	return nil
}

// writeDispatchTable prints the table the way the Mate uses it: each rule's
// condition, then its profiles as the flags to pass, then why.
func writeDispatchTable(out io.Writer, t dispatch.Table) {
	if t.BuiltIn {
		rel := filepath.Join(store.StateDirName, dispatch.FileName)
		fmt.Fprintf(out, "crew dispatch: built-in (to change it: mate crew dispatch --example > %s, then edit)\n", rel)
	} else {
		fmt.Fprintf(out, "crew dispatch: %s\n", t.Path)
	}
	for i, r := range t.Rules {
		fmt.Fprintf(out, "\nrule %d: %s\n", i+1, r.When)
		writeProfiles(out, r.Use)
		if r.Why != "" {
			fmt.Fprintf(out, "  why %s\n", r.Why)
		}
	}
	if len(t.Default) > 0 {
		fmt.Fprintf(out, "\ndefault: %s\n", t.Default[0].Flags())
		for _, p := range t.Default[1:] {
			fmt.Fprintf(out, "  or  %s\n", p.Flags())
		}
	}
}

func writeProfiles(out io.Writer, ps []dispatch.Profile) {
	for i, p := range ps {
		lead := "  use "
		if i > 0 {
			lead = "  or  "
		}
		fmt.Fprintf(out, "%s%s\n", lead, p.Flags())
	}
}
