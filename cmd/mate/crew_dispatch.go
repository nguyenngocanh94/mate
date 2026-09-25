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
// matches a rule. It checks the table, prints it for the Mate, and refuses
// a spawn that would fall back to the workspace default by omission while a
// table exists.

// checkDispatch is the spawn's gate on the table: a malformed table stops
// every spawn until it is corrected, and a present one needs an explicit
// --harness.
func checkDispatch(w *store.Workspace, req spawn.SpawnCrewRequest) error {
	_, ok, err := dispatch.Load(dispatch.Path(w.StateDir()))
	if errors.Is(err, dispatch.ErrInvalid) {
		// Exit 2 like the missing --harness below: both are "fix the table
		// or the invocation and run it again", never a crash.
		return &usageError{err}
	}
	if err != nil {
		return err
	}
	if ok && req.Harness == "" {
		return newUsageError("mate crew spawn: this workspace has a crew dispatch table; pick a profile from `mate crew dispatch` and pass --harness (and its --model and --effort)")
	}
	return nil
}

// cmdCrewDispatch implements `mate crew dispatch [--workspace <dir>] [--example]`.
func cmdCrewDispatch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew dispatch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate crew dispatch [--workspace <dir>] [--example]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	exampleFlag := fs.Bool("example", false, "print a starting table to copy into .mate/crew-dispatch.json")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return newUsageError("mate crew dispatch: takes no arguments")
	}
	if *exampleFlag {
		_, err := io.WriteString(stdout, exampleDispatchTable)
		return err
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	t, ok, err := dispatch.Load(dispatch.Path(w.StateDir()))
	if err != nil {
		return err
	}
	if !ok {
		rel := filepath.Join(store.StateDirName, dispatch.FileName)
		fmt.Fprintf(stdout, "no crew dispatch table: crews use the workspace default harness (%s)\n", w.Defaults().CrewHarness)
		fmt.Fprintf(stdout, "to add one: mate crew dispatch --example > %s\n", rel)
		return nil
	}
	writeDispatchTable(stdout, t)
	return nil
}

// writeDispatchTable prints the table the way the Mate uses it: each rule's
// condition, then its profiles as the flags to pass, then why.
func writeDispatchTable(out io.Writer, t dispatch.Table) {
	fmt.Fprintf(out, "crew dispatch: %s\n", t.Path)
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

// exampleDispatchTable is a starting table, after firstmate's
// docs/examples/crew-dispatch.json, with the harnesses mate launches.
const exampleDispatchTable = `{
  "rules": [
    {
      "when": "The task is a trivial mechanical edit such as a rote rename, formatting sweep, targeted typo fix, or simple file gathering.",
      "use": { "harness": "claude", "model": "haiku", "effort": "low" },
      "why": "Use the cheapest fast profile when the task is narrow and low ambiguity."
    },
    {
      "when": "The task is a big or ambiguous multi-file feature, a risky refactor, or work that requires holding many moving parts in mind.",
      "use": [
        { "harness": "claude", "model": "opus", "effort": "high" },
        { "harness": "codex", "effort": "xhigh" }
      ],
      "why": "Use a strong coding profile for big, ambiguous work; pick the alternative that is less loaded."
    }
  ],
  "default": { "harness": "codex", "effort": "medium" }
}
`
