package timeline_test

import (
	"context"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// The fixture crew's turns total 23272+209152+0+1544 = 233968 tokens
// (timeline_test.go's codexTotal* constants: input net of cache, per
// codexTurns's doc comment), all on model gpt-5.6-terra.

// A crew_tokens ceiling below the fixture crew's total opens a budget
// incident on that crew, and CheckBudgets is idempotent: running it twice
// leaves exactly one open incident, never a second one (mvp.md section 4b -
// a budget incident is a fact, recorded once, never resolved).
func TestCheckBudgetsOpensAnIncidentOnceWhenCrewTokensIsCrossed(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)

	if err := f.ws.SaveProject(fixtureProject, store.ProjectConfig{
		Repo: fixtureProject, DefaultBranch: "main",
		Budget: &store.BudgetConfig{CrewTokens: 200000},
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}

	if err := f.ing.CheckBudgets(context.Background()); err != nil {
		t.Fatalf("CheckBudgets: %v", err)
	}
	if err := f.ing.CheckBudgets(context.Background()); err != nil {
		t.Fatalf("CheckBudgets (second run): %v", err)
	}

	view, err := box.Load(f.ws, fixtureProject)
	if err != nil {
		t.Fatalf("box.Load: %v", err)
	}
	open := box.OpenIncidents(view, fixtureCrew)
	if len(open) != 1 {
		t.Fatalf("open incidents for %s = %+v, want exactly 1", fixtureCrew, open)
	}
	if open[0].Kind != box.IncidentBudget {
		t.Fatalf("incident kind = %q, want %q", open[0].Kind, box.IncidentBudget)
	}
	if open[0].Text != "234k tokens of 200k tokens" {
		t.Fatalf("incident text = %q, want the humanised total-of-limit shape", open[0].Text)
	}
}

// A crew whose spend has not crossed crew_tokens gets no incident at all.
func TestCheckBudgetsOpensNothingUnderTheLimit(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)

	if err := f.ws.SaveProject(fixtureProject, store.ProjectConfig{
		Repo: fixtureProject, DefaultBranch: "main",
		Budget: &store.BudgetConfig{CrewTokens: 10_000_000},
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := f.ing.CheckBudgets(context.Background()); err != nil {
		t.Fatalf("CheckBudgets: %v", err)
	}
	view, err := box.Load(f.ws, fixtureProject)
	if err != nil {
		t.Fatalf("box.Load: %v", err)
	}
	if open := box.OpenIncidents(view, fixtureCrew); len(open) != 0 {
		t.Fatalf("open incidents = %+v, want none: the crew is under budget", open)
	}
}

// A crew_usd ceiling only fires once the model actually has a real price -
// the seeded placeholder (price 0) must never look like a crossing.
func TestCheckBudgetsCrewUSDIgnoresAnUnpricedModel(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)

	if err := f.ws.SaveProject(fixtureProject, store.ProjectConfig{
		Repo: fixtureProject, DefaultBranch: "main",
		Budget: &store.BudgetConfig{CrewUSD: 0.01},
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := f.ing.CheckBudgets(context.Background()); err != nil {
		t.Fatalf("CheckBudgets: %v", err)
	}
	view, err := box.Load(f.ws, fixtureProject)
	if err != nil {
		t.Fatalf("box.Load: %v", err)
	}
	if open := box.OpenIncidents(view, fixtureCrew); len(open) != 0 {
		t.Fatalf("open incidents = %+v, want none: the model is still unpriced", open)
	}

	// Now price it for real, above the ceiling, and re-ingest so `pricing`
	// picks it up.
	if err := f.ws.SavePricing(store.PricingConfig{Models: []store.PricingModel{
		{Model: "gpt-5.6-terra", InputPerM: 1000000},
	}}); err != nil {
		t.Fatalf("SavePricing: %v", err)
	}
	f.ingest(t)
	if err := f.ing.CheckBudgets(context.Background()); err != nil {
		t.Fatalf("CheckBudgets after pricing: %v", err)
	}
	view, err = box.Load(f.ws, fixtureProject)
	if err != nil {
		t.Fatalf("box.Load: %v", err)
	}
	open := box.OpenIncidents(view, fixtureCrew)
	if len(open) != 1 || open[0].Kind != box.IncidentBudget {
		t.Fatalf("open incidents = %+v, want one budget incident now that the model is priced", open)
	}
}

// project_usd is checked against every crew of the project summed, and is
// filed under "mate" rather than under any one crew.
func TestCheckBudgetsProjectUSDIsFiledUnderMate(t *testing.T) {
	f := newFixture(t)
	if err := f.ws.SavePricing(store.PricingConfig{Models: []store.PricingModel{
		{Model: "gpt-5.6-terra", InputPerM: 1000000},
	}}); err != nil {
		t.Fatalf("SavePricing: %v", err)
	}
	f.ingest(t)

	if err := f.ws.SaveProject(fixtureProject, store.ProjectConfig{
		Repo: fixtureProject, DefaultBranch: "main",
		Budget: &store.BudgetConfig{ProjectUSD: 0.01},
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := f.ing.CheckBudgets(context.Background()); err != nil {
		t.Fatalf("CheckBudgets: %v", err)
	}

	view, err := box.Load(f.ws, fixtureProject)
	if err != nil {
		t.Fatalf("box.Load: %v", err)
	}
	if open := box.OpenIncidents(view, fixtureCrew); len(open) != 0 {
		t.Fatalf("open incidents for the crew = %+v, want none: this is a project-wide ceiling", open)
	}
	open := box.OpenIncidents(view, "mate")
	if len(open) != 1 || open[0].Kind != box.IncidentBudget {
		t.Fatalf("open incidents for mate = %+v, want one budget incident", open)
	}
}

// A project with no `budget:` block at all is checked and finds nothing -
// CheckBudgets must not error just because most projects never configure one.
func TestCheckBudgetsNoBudgetConfiguredIsANoOp(t *testing.T) {
	f := newFixture(t)
	f.ingest(t)
	if err := f.ing.CheckBudgets(context.Background()); err != nil {
		t.Fatalf("CheckBudgets: %v", err)
	}
	view, err := box.Load(f.ws, fixtureProject)
	if err != nil {
		t.Fatalf("box.Load: %v", err)
	}
	if open := box.OpenIncidents(view, fixtureCrew); len(open) != 0 {
		t.Fatalf("open incidents = %+v, want none: no budget is configured", open)
	}
}
