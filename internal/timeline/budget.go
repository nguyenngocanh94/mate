package timeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/query"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// projectBudgetCrew is the crew name a project-wide budget crossing is filed
// under: there is no one crew to blame for the project's total, so it goes
// under the same name the daemon's own `wedged` incident already uses for a
// finding that is about the Mate rather than any one crew (mvp.md section
// 4b, internal/autopilot.MateCrew - not imported here to avoid a needless
// dependency between two leaf-ish packages that would otherwise never need
// each other).
const projectBudgetCrew = "mate"

// CheckBudgets is mvp.md M5 task 27's budget check, run by the observer at
// the end of every poll (watch.Deps.Budget): for every project whose
// project.yaml carries a `budget:` block, it opens a `budget` incident on
// whichever crew - or, for the project-wide ceiling, on projectBudgetCrew -
// has crossed a configured limit.
//
// It reads the ledger this same poll's Ingest just wrote, so a crossing
// becomes visible in incidents.log one poll after the turn that caused it -
// the same lag every other incident kind already has behind the transcript
// it is read from.
//
// A budget incident is never resolved (mvp.md section 4b, decision
// 2026-09-20: "nó là một sự thật" - it is a fact about spend that already
// happened, not a condition that can clear). CheckBudgets therefore only
// ever appends one `open` line per (crew, kind): a crew already carrying one
// is left alone even if it has spent more since, because the record already
// says what needs saying.
func (i *Ingester) CheckBudgets(ctx context.Context) error {
	if !i.d.Writable() {
		return errors.New("timeline: this database handle is read-only")
	}
	if err := i.ws.LoadConfig(); err != nil {
		return err
	}
	var errs []error
	for _, ref := range i.ws.Projects() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := i.checkProjectBudget(ctx, ref.Name); err != nil {
			errs = append(errs, fmt.Errorf("timeline: budget check %s: %w", ref.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (i *Ingester) checkProjectBudget(ctx context.Context, project string) error {
	cfg, err := i.ws.LoadProject(project)
	if err != nil {
		return err
	}
	if cfg.Budget == nil {
		return nil
	}

	view, err := box.Load(i.ws, project)
	if err != nil {
		return err
	}
	alreadyOpen := func(crew string) bool {
		for _, inc := range box.OpenIncidents(view, crew) {
			if inc.Kind == box.IncidentBudget {
				return true
			}
		}
		return false
	}

	if cfg.Budget.CrewTokens > 0 || cfg.Budget.CrewUSD > 0 {
		crews, err := listCrewMetas(i.ws, project)
		if err != nil {
			return err
		}
		for _, crew := range crews {
			if alreadyOpen(crew.ID) {
				continue
			}
			tokens, cost, err := i.crewSpend(ctx, crew.ActorID)
			if err != nil {
				return err
			}
			text := ""
			switch {
			case cfg.Budget.CrewTokens > 0 && tokens > cfg.Budget.CrewTokens:
				text = fmt.Sprintf("%s tokens of %s tokens",
					query.HumanizeTokens(tokens), query.HumanizeTokens(cfg.Budget.CrewTokens))
			case cfg.Budget.CrewUSD > 0 && cost.Valid && cost.Float64 > cfg.Budget.CrewUSD:
				text = fmt.Sprintf("%s of %s", query.HumanizeCost(cost.Float64), query.HumanizeCost(cfg.Budget.CrewUSD))
			}
			if text == "" {
				continue
			}
			if err := i.ws.AppendIncident(project, store.IncidentEntry{
				Time: i.deps.now(), Crew: crew.ID, Kind: string(box.IncidentBudget),
				State: store.IncidentOpen, Text: text,
			}); err != nil {
				return err
			}
		}
	}

	if cfg.Budget.ProjectUSD > 0 && !alreadyOpen(projectBudgetCrew) {
		cost, err := i.projectSpend(ctx, project)
		if err != nil {
			return err
		}
		if cost.Valid && cost.Float64 > cfg.Budget.ProjectUSD {
			text := fmt.Sprintf("%s of %s", query.HumanizeCost(cost.Float64), query.HumanizeCost(cfg.Budget.ProjectUSD))
			if err := i.ws.AppendIncident(project, store.IncidentEntry{
				Time: i.deps.now(), Crew: projectBudgetCrew, Kind: string(box.IncidentBudget),
				State: store.IncidentOpen, Text: text,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// crewSpend is one crew's whole-task token total and cost, read the same way
// `v_task_ledger` computes them: cost only counts a turn whose model has a
// real (non-zero) price, so a model that is only listed in pricing.yaml as
// a placeholder never crosses a dollar budget.
func (i *Ingester) crewSpend(ctx context.Context, actorID string) (int64, sql.NullFloat64, error) {
	var tokens int64
	if err := i.d.SQL().QueryRowContext(ctx, `
		SELECT COALESCE(SUM(input_tokens+cache_read_tokens+cache_write_tokens+output_tokens), 0)
		  FROM turn WHERE actor_id = ?`, actorID).Scan(&tokens); err != nil {
		return 0, sql.NullFloat64{}, err
	}
	var cost sql.NullFloat64
	if err := i.d.SQL().QueryRowContext(ctx, `
		SELECT SUM(
			u.input_tokens       * COALESCE(p.input_per_m, 0) / 1000000.0 +
			u.cache_read_tokens  * COALESCE(p.cache_read_per_m, 0) / 1000000.0 +
			u.cache_write_tokens * COALESCE(p.cache_write_per_m, 0) / 1000000.0 +
			u.output_tokens      * COALESCE(p.output_per_m, 0) / 1000000.0)
		  FROM turn u JOIN pricing p ON p.model = u.model
		 WHERE u.actor_id = ?
		   AND (p.input_per_m > 0 OR p.cache_read_per_m > 0 OR p.cache_write_per_m > 0 OR p.output_per_m > 0)`,
		actorID).Scan(&cost); err != nil {
		return 0, sql.NullFloat64{}, err
	}
	return tokens, cost, nil
}

// projectSpend is every crew of a project summed: the cost a `project_usd`
// ceiling is checked against.
func (i *Ingester) projectSpend(ctx context.Context, project string) (sql.NullFloat64, error) {
	var cost sql.NullFloat64
	row := i.d.SQL().QueryRowContext(ctx, `
		SELECT SUM(
			u.input_tokens       * COALESCE(p.input_per_m, 0) / 1000000.0 +
			u.cache_read_tokens  * COALESCE(p.cache_read_per_m, 0) / 1000000.0 +
			u.cache_write_tokens * COALESCE(p.cache_write_per_m, 0) / 1000000.0 +
			u.output_tokens      * COALESCE(p.output_per_m, 0) / 1000000.0)
		FROM turn u
		JOIN actor a ON a.id = u.actor_id
		JOIN pricing p ON p.model = u.model
		WHERE a.project = ? AND a.kind = ?
		  AND (p.input_per_m > 0 OR p.cache_read_per_m > 0 OR p.cache_write_per_m > 0 OR p.output_per_m > 0)`,
		project, ActorCrew)
	if err := row.Scan(&cost); err != nil {
		return sql.NullFloat64{}, err
	}
	return cost, nil
}
