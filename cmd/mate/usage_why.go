package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/diagnostics"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The digest is read by a Mate whose own context is the thing being saved,
// so every list in it is cut to a fixed length and says what it left out.
const (
	whyFindings     = 6
	whyPrompts      = 3
	whyInstructions = 8
	whyTextWidth    = 160
)

// printCrewWhy is `mate usage <project> <crew> --why`: the dashboard Task
// page's diagnostics as one bounded page of text. It reads through
// dashboard.Server so the two can never disagree, and it adds no
// measurement of its own: every number is the ledger's or the projection's.
func printCrewWhy(ctx context.Context, handle *db.DB, w *store.Workspace, stdout io.Writer, project, crew string) error {
	if err := store.ValidateCrewID(crew); err != nil {
		return err
	}
	server, err := dashboard.New(dashboard.Options{Workspace: w, DB: handle, Deps: dashboardDeps(w)})
	if err != nil {
		return err
	}
	ledger, turns, perf, err := server.CrewPerformance(ctx, project, crew)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "usage why %s %s\n", project, crew)
	if ledger.Text != "" {
		fmt.Fprintf(stdout, "task: %s\n", oneLine(ledger.Text, whyTextWidth))
	}
	fmt.Fprintf(stdout, "run: %s\n", strings.Join(whyRun(ledger, perf), " · "))
	t := ledger.Tokens
	fmt.Fprintf(stdout, "tokens: %s total = %s in + %s cache-read + %s cache-write + %s out · cost %s\n",
		query.HumanizeTokens(t.Total), query.HumanizeTokens(t.Input), query.HumanizeTokens(t.CacheRead),
		query.HumanizeTokens(t.CacheWrite), query.HumanizeTokens(t.Output), whyCost(ledger.Cost))
	fmt.Fprintf(stdout, "shape: %d model call(s) over %d prompt(s) · context %s · asked %d · waited %s\n",
		len(turns), len(perf.PromptTurns), whyContext(turns), ledger.QuestionCount, waitedWord(ledger.WaitedMs))
	if len(turns) == 0 {
		fmt.Fprintf(stdout, "\nno model calls recorded for %s; there is nothing to explain yet\n", crew)
		return nil
	}

	whyWork(stdout, perf, t.Total)
	whyLoops(stdout, perf.Loops)
	whyFindingList(stdout, perf.Findings)
	whyInstructionList(stdout, perf.ObservedInputs)
	whyPromptList(stdout, perf.PromptTurns)
	if missing := perf.Freshness.Missing; len(missing) > 0 {
		fmt.Fprintln(stdout, "\nnot measured (no conclusion may rest on these):")
		for _, m := range missing {
			fmt.Fprintf(stdout, "- %s\n", m)
		}
	}
	return nil
}

func whyRun(ledger dashboard.Task, perf diagnostics.Performance) []string {
	// ledger.State is the office scene, not section 4b's vocabulary; the
	// digest only needs to say whether the numbers can still grow.
	state := "open"
	if ledger.Closed {
		state = "closed"
		if ledger.CloseState != "" {
			state = ledger.CloseState
		}
	}
	parts := []string{state}
	harness := strings.TrimSpace(ledger.Harness + " " + perf.Runtime.HarnessVersion)
	if harness != "" {
		parts = append(parts, "harness "+harness)
	}
	model := perf.Runtime.Model
	if model == "" {
		model = ledger.LastModel
	}
	if model != "" {
		parts = append(parts, "model "+model)
	}
	if perf.Runtime.Effort != "" {
		parts = append(parts, "effort "+perf.Runtime.Effort)
	}
	return append(parts, "alive "+formatAge(time.Duration(ledger.AgeMs)*time.Millisecond))
}

func whyCost(cost *float64) string {
	if cost == nil {
		return "?"
	}
	return query.HumanizeCost(*cost)
}

// whyContext is the peak and the last recorded context: a session whose
// context stays large pays for it again as cache-read on every later call.
func whyContext(turns []dashboard.Turn) string {
	var peak, last int64
	for _, t := range turns {
		if t.ContextAfter == 0 {
			continue
		}
		peak, last = max(peak, t.ContextAfter), t.ContextAfter
	}
	if peak == 0 {
		return "?"
	}
	return fmt.Sprintf("peak %s, last %s", query.HumanizeTokens(peak), query.HumanizeTokens(last))
}

func whyWork(stdout io.Writer, perf diagnostics.Performance, total int64) {
	categories := append([]diagnostics.WorkCategory(nil), perf.Overview.Categories...)
	if len(categories) == 0 {
		return
	}
	owned := func(c diagnostics.WorkCategory) int64 {
		if c.Tokens == nil {
			return -1
		}
		return c.Tokens.Total
	}
	sort.SliceStable(categories, func(i, j int) bool { return owned(categories[i]) > owned(categories[j]) })
	fmt.Fprintln(stdout, "\nwork by kind (each model call is counted once, under the kind of work it ran):")
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tCALLS\tEXECS\tTOTAL\tOUT\tSHARE")
	for _, c := range categories {
		if c.ModelCalls == 0 && c.ExecutionCount == 0 {
			continue
		}
		tokens, out, share := "?", "?", "?"
		if c.Tokens != nil {
			tokens, out = query.HumanizeTokens(c.Tokens.Total), query.HumanizeTokens(c.Tokens.Output)
			if total > 0 {
				share = fmt.Sprintf("%.0f%%", float64(c.Tokens.Total)*100/float64(total))
			}
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%s\n", c.Label, c.ModelCalls, c.ExecutionCount, tokens, out, share)
	}
	_ = tw.Flush()
	if perf.Overview.Coverage != "" {
		fmt.Fprintf(stdout, "coverage: %s\n", oneLine(perf.Overview.Coverage, whyTextWidth))
	}
}

func whyLoops(stdout io.Writer, l diagnostics.LoopSummary) {
	line := fmt.Sprintf("%d repeated read(s), %d repeated error(s), %d repair/retest loop(s)", l.RepeatedReads, l.RepeatedErrors, l.RepairLoops)
	if l.PollTokens != nil {
		line += fmt.Sprintf(", %d waiting call(s) costing %s", l.PollCalls, query.HumanizeTokens(l.PollTokens.Total))
	}
	if l.Measured {
		line += fmt.Sprintf(", %d poll(s) of %d process(es) with %d unchanged", l.Polls, l.ProcessesPolled, l.UnchangedPolls)
	} else {
		line += "; polling chains are not measurable on this harness"
	}
	fmt.Fprintf(stdout, "\nloops: %s\n", line)
}

func whyFindingList(stdout io.Writer, findings []diagnostics.Finding) {
	if len(findings) == 0 {
		fmt.Fprintln(stdout, "\nfindings: none")
		return
	}
	fmt.Fprintln(stdout, "\nfindings (they overlap, and a finding's tokens are the whole model calls it touched; never add them up):")
	for i, f := range findings {
		if i == whyFindings {
			fmt.Fprintf(stdout, "... %d more finding(s) not shown; the dashboard Task page lists all of them\n", len(findings)-whyFindings)
			break
		}
		spent := "tokens ?"
		if f.Tokens != nil {
			spent = fmt.Sprintf("%s in %d call(s)", query.HumanizeTokens(f.Tokens.Total), len(f.CallIDs))
		}
		fmt.Fprintf(stdout, "%d. %s %s x%d, %s: %s\n", i+1, f.Severity, f.Kind, f.Count, spent, oneLine(f.Title, whyTextWidth))
		if len(f.Evidence) > 0 {
			fmt.Fprintf(stdout, "   evidence: %s\n", oneLine(f.Evidence[0].Label, whyTextWidth))
		}
		if f.Review != "" {
			fmt.Fprintf(stdout, "   review: %s\n", f.Review)
		}
	}
}

// whyInstructionList prints each instruction file the harness loaded, once,
// at its last recorded size, largest first: these bytes ride in every call.
func whyInstructionList(stdout io.Writer, inputs []diagnostics.ContextInput) {
	latest := map[string]diagnostics.ContextInput{}
	for _, in := range inputs {
		name := in.Path
		if name == "" {
			name = in.Kind
		}
		if prev, ok := latest[name]; !ok || in.At >= prev.At {
			latest[name] = in
		}
	}
	if len(latest) == 0 {
		return
	}
	names := make([]string, 0, len(latest))
	for name := range latest {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if a, b := latest[names[i]].Bytes, latest[names[j]].Bytes; a != b {
			return a > b
		}
		return names[i] < names[j]
	})
	fmt.Fprintln(stdout, "\ninstructions loaded:")
	for i, name := range names {
		if i == whyInstructions {
			fmt.Fprintf(stdout, "... %d more not shown\n", len(names)-whyInstructions)
			break
		}
		fmt.Fprintf(stdout, "- %s  %.1f KB\n", name, float64(latest[name].Bytes)/1024)
	}
}

func whyPromptList(stdout io.Writer, prompts []diagnostics.Prompt) {
	if len(prompts) < 2 {
		return
	}
	ranked := append([]diagnostics.Prompt(nil), prompts...)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Tokens.Total > ranked[j].Tokens.Total })
	fmt.Fprintln(stdout, "\ncostliest prompts:")
	for i, p := range ranked {
		if i == whyPrompts {
			break
		}
		text := oneLine(p.Prompt, whyTextWidth)
		if text == "" {
			text = "(prompt text not recorded)"
		}
		fmt.Fprintf(stdout, "%d. %s in %d call(s): %s\n", i+1, query.HumanizeTokens(p.Tokens.Total), p.ModelCalls, text)
	}
}

// oneLine folds whitespace and cuts at n runes, so a prompt or a command
// quoted in the digest can never break its line structure.
func oneLine(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}
