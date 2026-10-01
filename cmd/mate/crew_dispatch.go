package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/nguyenngocanh94/mate/internal/dispatch"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/quota"
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
	t, err := dispatch.Resolve(w.StateDir(), harnesses)
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
	def := w.Defaults().CrewHarness
	if def == "" {
		k, err := harnesses.Default(harness.RoleCrew)
		if err != nil {
			return req, "", err
		}
		def = string(k)
	}
	p, ok := t.DefaultFor(def)
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
	t, err := dispatch.Resolve(w.StateDir(), harnesses)
	if err != nil {
		return err
	}
	writeDispatchTable(stdout, t)
	snap, qerr := readQuota(context.Background())
	writeQuota(stdout, snap, qerr)
	return nil
}

// readQuota is the one quota-axi read a dispatch or a spawn makes; tests
// replace it so no suite depends on this machine's vendor accounts. It is
// a plain variable because this package's tests run serially (none calls
// t.Parallel); a parallel test must not swap it.
var readQuota = func(ctx context.Context) (quota.Snapshot, error) {
	return quota.Read(ctx, process.ExecRunner{}, time.Now())
}

// writeQuota prints the quota evidence for choosing between a rule's
// alternatives (skill crew-dispatch section 4). No quota-axi, or an
// unreadable one, is said in one line and never fails the command.
func writeQuota(out io.Writer, snap quota.Snapshot, err error) {
	switch {
	case errors.Is(err, quota.ErrNotInstalled):
		fmt.Fprintln(out, "\nquota: quota-axi is not installed; alternatives are chosen without quota evidence (install: npm install -g quota-axi)")
		return
	case err != nil:
		fmt.Fprintf(out, "\nquota: unreadable (%v); alternatives are chosen without quota evidence\n", err)
		return
	}
	fmt.Fprintf(out, "\nquota (quota-axi %s, read-only, %s):\n", snap.Version, snap.Read.UTC().Format("2006-01-02 15:04 UTC"))
	for _, r := range snap.Readings {
		fmt.Fprintf(out, "  %s\n", r.Line())
	}
	if kind, why, ok := snap.Favoured(); ok {
		fmt.Fprintf(out, "  favours %s: %s\n", kind, why)
	} else {
		fmt.Fprintf(out, "  favours none: %s\n", why)
	}
}

// spawnQuotaBudget bounds the spawn's quota read; past it the spawn goes on
// with no warning.
const spawnQuotaBudget = 5 * time.Second

// quotaWarning is the spawn's one quota check: a harness quota-axi measures
// as exhausted, or at zero, is warned about and still launched, because the
// captain's words may have chosen it; "" when there is nothing to say.
func quotaWarning(kind harness.Kind) string {
	// Advisory only, so it gets a short budget: a slow vendor must never
	// hold up a launch for the full dispatch read.
	ctx, cancel := context.WithTimeout(context.Background(), spawnQuotaBudget)
	defer cancel()
	snap, err := readQuota(ctx)
	if err != nil {
		return ""
	}
	r, ok := snap.Reading(kind)
	if !ok || r.Eligible() {
		return ""
	}
	return fmt.Sprintf("%s quota is used up (quota-axi: %s); the Crew may stop mid-task - another alternative of its rule avoids that", kind, r.Line())
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
