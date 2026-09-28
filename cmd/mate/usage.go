package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// cmdUsage is `mate usage <project> [crew] [--workspace <dir>]` (mvp.md M5
// task 27): the token/cost ledger `v_task_ledger` computes, read-only.
//
// With no crew it prints one row per task plus the Mate, oldest spawn
// first, and a totals footer. With a crew it prints that crew's own turns -
// one row per model call, the unit `mate events --narrate` calls "ends a
// turn" - rather than the whole-task summary.
func cmdUsage(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate usage <project> [crew] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		fs.Usage()
		return newUsageError("mate usage: want 1 or 2 arguments: <project> [crew]")
	}
	project := fs.Arg(0)

	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	if _, ok := w.Project(project); !ok {
		return newUsageErrorf("no project %q in this workspace", project)
	}

	handle, err := db.OpenRead(w)
	if err != nil {
		return err
	}
	defer handle.Close()

	ctx := context.Background()
	if fs.NArg() == 2 {
		return printCrewTurns(ctx, handle, stdout, project, fs.Arg(1))
	}
	return printLedger(ctx, handle, w, stdout, project)
}

// ledgerRow is one line of the ledger table: the Mate's own row, built by
// mateLedgerRow, or one task's row of `v_task_ledger`.
type ledgerRow struct {
	ID         string
	State      string
	Turns      int64
	In         int64
	Cached     int64
	CacheRead  int64
	CacheWrite int64
	Context    sql.NullInt64
	Out        int64
	Total      int64
	Cost       sql.NullFloat64
	CtxPct     sql.NullFloat64
	Asked      int64
	WaitedMs   int64
	Span       string
}

// printLedger is the no-crew form: the Mate's row first (mvp.md M5 task
// 27), then every task the project has ever recorded, oldest spawn first.
func printLedger(ctx context.Context, handle *db.DB, w *store.Workspace, stdout io.Writer, project string) error {
	mate, err := mateLedgerRow(ctx, handle, project)
	if err != nil {
		return err
	}
	rows := []ledgerRow{mate}

	summaries, err := spawn.ListCrews(w, project)
	if err != nil {
		return err
	}
	stateByID := make(map[string]string, len(summaries))
	for _, s := range summaries {
		stateByID[s.Crew] = s.State
	}

	dbRows, err := handle.SQL().QueryContext(ctx, `
		SELECT crew, turns, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens,
		       cost, context_pct, context_tokens_last, question_count, waited_ms, spawned_at, closed_at
		  FROM v_task_ledger WHERE project = ? ORDER BY spawned_at`, project)
	if err != nil {
		return err
	}
	defer dbRows.Close()
	for dbRows.Next() {
		var crew string
		var turns, in, cacheRead, cacheWrite, out, asked, waited int64
		var cost, ctxPct sql.NullFloat64
		var ctxTokens sql.NullInt64
		var spawnedAt, closedAt sql.NullString
		if err := dbRows.Scan(&crew, &turns, &in, &cacheRead, &cacheWrite, &out,
			&cost, &ctxPct, &ctxTokens, &asked, &waited, &spawnedAt, &closedAt); err != nil {
			return err
		}
		rows = append(rows, ledgerRow{
			ID: crew, State: stateByID[crew], Turns: turns,
			In: in, Cached: cacheRead + cacheWrite, Out: out, Total: in + cacheRead + cacheWrite + out,
			Cost: cost, CtxPct: ctxPct, Context: ctxTokens, CacheRead: cacheRead, CacheWrite: cacheWrite, Asked: asked, WaitedMs: waited,
			Span: spanWord(spawnedAt, closedAt),
		})
	}
	if err := dbRows.Err(); err != nil {
		return err
	}

	printLedgerTable(stdout, rows)
	return nil
}

// mateLedgerRow computes the Mate's own row the same way `v_task_ledger`
// computes a crew's: cost only counts a turn whose model has a real price,
// and context_pct is the most recent turn's. There is no task row for a
// Mate - its turns belong to the project rather than to any one task
// (docs/timeline.md) - so State and Span are dashes and Asked/WaitedMs are
// zero: those columns are a task's shape, not a Mate's.
func mateLedgerRow(ctx context.Context, handle *db.DB, project string) (ledgerRow, error) {
	row := ledgerRow{ID: "mate", State: "-", Span: "-"}
	actorID := timeline.MateActorID(project)

	var cacheRead, cacheWrite int64
	if err := handle.SQL().QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(cache_read_tokens),0),
		       COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(output_tokens),0)
		  FROM turn WHERE actor_id = ?`, actorID).
		Scan(&row.Turns, &row.In, &cacheRead, &cacheWrite, &row.Out); err != nil {
		return row, err
	}
	row.Cached = cacheRead + cacheWrite
	row.CacheRead, row.CacheWrite = cacheRead, cacheWrite
	row.Total = row.In + row.Cached + row.Out

	if err := handle.SQL().QueryRowContext(ctx, `
		SELECT SUM(
			u.input_tokens       * COALESCE(p.input_per_m, 0) / 1000000.0 +
			u.cache_read_tokens  * COALESCE(p.cache_read_per_m, 0) / 1000000.0 +
			u.cache_write_tokens * COALESCE(p.cache_write_per_m, 0) / 1000000.0 +
			u.output_tokens      * COALESCE(p.output_per_m, 0) / 1000000.0)
		  FROM turn u JOIN pricing p ON p.model = u.model
		 WHERE u.actor_id = ?
		   AND (p.input_per_m > 0 OR p.cache_read_per_m > 0 OR p.cache_write_per_m > 0 OR p.output_per_m > 0)`,
		actorID).Scan(&row.Cost); err != nil {
		return row, err
	}

	if usage, ok, err := handle.LatestContext(ctx, actorID, ""); err != nil {
		return row, err
	} else if ok {
		row.Context = sql.NullInt64{Int64: usage.Tokens, Valid: true}
		if usage.Pct != nil {
			row.CtxPct = sql.NullFloat64{Float64: *usage.Pct, Valid: true}
		}
	}

	return row, nil
}

// spanWord is the SPAWN→CLOSE column: local times, "→ open" for a task
// still in flight, and a dash for a row (the Mate's) that has neither.
func spanWord(spawnedAt, closedAt sql.NullString) string {
	if !spawnedAt.Valid || spawnedAt.String == "" {
		return "-"
	}
	start := db.ParseTime(spawnedAt.String).Local().Format("15:04:05")
	if closedAt.Valid && closedAt.String != "" {
		return start + " → " + db.ParseTime(closedAt.String).Local().Format("15:04:05")
	}
	return start + " → open"
}

// printLedgerTable renders the columns of docs/mvp.md M5 task 27, humanised
// (query.HumanizeTokens, query.HumanizeCost), with a totals footer.
func printLedgerTable(stdout io.Writer, rows []ledgerRow) {
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tTURNS\tIN\tCACHE-READ\tCACHE-WRITE\tOUT\tTOTAL\tCOST\tCTX\tCTX%\tASKED\tWAITED\tSPAWN→CLOSE")

	var totalTurns, totalIn, totalCached, totalOut, totalTotal, totalAsked, totalWaited int64
	var totalCost float64
	haveCost := false
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			r.ID, dashIfEmpty(r.State), r.Turns,
			query.HumanizeTokens(r.In), query.HumanizeTokens(r.CacheRead), query.HumanizeTokens(r.CacheWrite), query.HumanizeTokens(r.Out), query.HumanizeTokens(r.Total),
			costWord(r.Cost), contextWord(r.Context), ctxWord(r.CtxPct), r.Asked, waitedWord(r.WaitedMs), r.Span)
		totalTurns += r.Turns
		totalIn += r.In
		totalCached += r.Cached
		totalOut += r.Out
		totalTotal += r.Total
		totalAsked += r.Asked
		totalWaited += r.WaitedMs
		if r.Cost.Valid {
			totalCost += r.Cost.Float64
			haveCost = true
		}
	}
	_ = tw.Flush()

	cost := "?"
	if haveCost {
		cost = query.HumanizeCost(totalCost)
	}
	fmt.Fprintf(stdout, "total: %d turn(s), %s in, %s cached, %s out, %s total, %s cost, %d asked, %s waited\n",
		totalTurns, query.HumanizeTokens(totalIn), query.HumanizeTokens(totalCached), query.HumanizeTokens(totalOut),
		query.HumanizeTokens(totalTotal), cost, totalAsked, waitedWord(totalWaited))
}

// costWord and ctxWord render a nullable value as "?" - a missing price or
// an unknown context window, never zero (mvp.md M5: "cost is NULL until
// then, because a missing price is not a price of zero").
func costWord(v sql.NullFloat64) string {
	if !v.Valid {
		return "?"
	}
	return query.HumanizeCost(v.Float64)
}

func ctxWord(v sql.NullFloat64) string {
	if !v.Valid {
		return "?"
	}
	return fmt.Sprintf("%.0f%%", v.Float64)
}

// waitedWord renders a millisecond total the way formatAge (cmd/mate/
// backlog.go) renders an age: the largest whole unit that keeps the number
// readable.
func waitedWord(ms int64) string {
	if ms <= 0 {
		return "0s"
	}
	return formatAge(time.Duration(ms) * time.Millisecond)
}

// printCrewTurns is the `usage <project> <crew>` form: one row per model
// call, in the order they happened.
func printCrewTurns(ctx context.Context, handle *db.DB, stdout io.Writer, project, crew string) error {
	actorID := timeline.CrewActorID(project, crew)
	rows, err := handle.SQL().QueryContext(ctx, `
		SELECT ordinal, started_at, ended_at, model, input_tokens, cache_read_tokens,
		       cache_write_tokens, output_tokens, context_tokens_after, outcome
		  FROM turn WHERE actor_id = ? ORDER BY started_at, ordinal`, actorID)
	if err != nil {
		return err
	}
	defer rows.Close()

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TURN\tSTARTED\tMODEL\tIN\tCACHED\tOUT\tTOTAL\tCTX\tDURATION\tOUTCOME")
	n := 0
	for rows.Next() {
		var ordinal int
		var startedAt sql.NullString
		var endedAt, model, outcome sql.NullString
		var in, cacheRead, cacheWrite, out, ctxAfter int64
		if err := rows.Scan(&ordinal, &startedAt, &endedAt, &model, &in, &cacheRead, &cacheWrite,
			&out, &ctxAfter, &outcome); err != nil {
			return err
		}
		started := db.ParseTime(startedAt.String)
		var dur time.Duration
		if endedAt.Valid {
			dur = db.ParseTime(endedAt.String).Sub(started)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			ordinal+1, started.Local().Format("15:04:05"), dashIfEmpty(model.String),
			query.HumanizeTokens(in), query.HumanizeTokens(cacheRead+cacheWrite), query.HumanizeTokens(out),
			query.HumanizeTokens(in+cacheRead+cacheWrite+out), query.HumanizeTokens(ctxAfter),
			dur.Round(time.Millisecond), dashIfEmpty(outcome.String))
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_ = tw.Flush()
	if n == 0 {
		fmt.Fprintf(stdout, "no turns recorded for %s\n", crew)
	}
	return nil
}

// contextWord keeps missing usage distinct from a measured zero.
func contextWord(v sql.NullInt64) string {
	if !v.Valid {
		return "?"
	}
	return query.HumanizeTokens(v.Int64)
}
