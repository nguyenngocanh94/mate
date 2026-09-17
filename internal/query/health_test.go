package query

import (
	"context"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/application"
	"github.com/nguyenngocanh94/matev2/internal/domain"
	"github.com/nguyenngocanh94/matev2/internal/persistence"
)

// TestLoadHealthViewReadsCrewHealthAndOpenIncidentsOnly is the G7-04a2 read
// side: LoadHealthView is a plain projection of crew_health + open
// health_incident rows (ADR 0019 §8), performing no runtime query and no
// classification of its own - the observer (internal/orchestration.
// ObserveCrewHealth/ReconcileCrewHealth) already did that. A resolved
// incident must not appear: only "open" is ever requested.
func TestLoadHealthViewReadsCrewHealthAndOpenIncidentsOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "watch the build", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	crewID := reserved.Crew.CrewID
	bindingID := "bind_" + crewID
	observedAt := f.deps.Clock.Now().Add(-2 * time.Minute)
	changedAt := f.deps.Clock.Now().Add(-90 * time.Second)
	if err := f.deps.Store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.ReserveBinding(persistence.BindingRecord{
			BindingID: bindingID, WorkspaceID: reserved.Crew.WorkspaceID, ProjectID: reserved.Crew.ProjectID,
			AgentID: crewID, Role: domain.RoleCrew, CrewID: crewID,
			HerdrSession: "s1", HerdrAgent: "crew-agent",
		}); err != nil {
			return err
		}
		if err := tx.UpsertCrewHealth(persistence.CrewHealthRecord{
			CrewID: crewID, BindingID: bindingID, SessionName: "s1",
			Liveness: "absent", Activity: "activity_unknown", Reason: "three consecutive absent samples",
			EvidenceRef: "evt_1", ObservedAt: observedAt, ChangedAt: changedAt, ConfigVersion: "g7-04a1-v1",
			AbsenceSamples: 3,
		}, 0); err != nil {
			return err
		}
		if err := tx.InsertHealthIncident(persistence.HealthIncidentRecord{
			IncidentID: "incident_open_1", Scope: "crew", ScopeID: crewID, BindingID: bindingID,
			Kind: "runtime_missing", Status: "open", FirstObservedAt: observedAt, Reason: "three consecutive absent samples",
			AffectedCrewIDs: []string{crewID},
		}); err != nil {
			return err
		}
		if err := tx.InsertHealthIncident(persistence.HealthIncidentRecord{
			IncidentID: "incident_resolved_1", Scope: "session", ScopeID: "s2",
			Kind: "monitoring_unavailable", Status: "open", FirstObservedAt: observedAt, Reason: "session query failed",
			AffectedCrewIDs: []string{crewID},
		}); err != nil {
			return err
		}
		if err := tx.ResolveHealthIncident("incident_resolved_1", 1, f.deps.Clock.Now(), "session query recovered"); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, reserved.Crew.WorkspaceID, reserved.Crew.ProjectID)
	}); err != nil {
		t.Fatalf("seed health rows: %v", err)
	}

	view, err := LoadHealthView(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadHealthView: %v", err)
	}
	if !view.AsOf.Equal(f.deps.Clock.Now()) {
		t.Fatalf("AsOf = %v, want the clock's current time", view.AsOf)
	}
	crew, ok := view.Crews[crewID]
	if !ok || crew.State != Known {
		t.Fatalf("Crews[%s] = %+v, want a Known sample", crewID, crew)
	}
	if crew.Value.Liveness != "absent" || crew.Value.Activity != "activity_unknown" {
		t.Fatalf("crew health = %+v", crew.Value)
	}
	if !crew.Value.ObservedAt.Equal(observedAt) || !crew.Value.ChangedAt.Equal(changedAt) {
		t.Fatalf("crew health timestamps = %+v, want observed=%v changed=%v", crew.Value, observedAt, changedAt)
	}
	if len(view.Incidents) != 1 {
		t.Fatalf("incidents = %+v, want exactly the one still-open incident", view.Incidents)
	}
	inc := view.Incidents[0]
	if inc.IncidentID != "incident_open_1" || inc.Scope != "crew" || inc.ScopeID != crewID || inc.Kind != "runtime_missing" {
		t.Fatalf("incident = %+v", inc)
	}
	if len(inc.AffectedCrewIDs) != 1 || inc.AffectedCrewIDs[0] != crewID {
		t.Fatalf("affected crew ids = %v", inc.AffectedCrewIDs)
	}
	if inc.AcknowledgedAt != nil {
		t.Fatalf("acknowledged at = %v, want nil before acknowledgement", inc.AcknowledgedAt)
	}
}

// TestLoadHealthViewCrewWithNoSampleYetIsAbsentNotUnknown proves "not yet
// checked" is a distinct, honest state: a Crew the observer has never
// sampled must not render as though a read failed (Unknown) or as though it
// was sampled and found missing (a Known absent liveness) - LoadHealthView
// itself never fails here, so per the Field convention that Crew is simply
// not a key in the map, which the caller (the Console) must read as "not
// yet checked", never defaulted to a zero Field claiming a value.
func TestLoadHealthViewCrewWithNoSampleYetIsAbsentNotUnknown(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	reserved, err := application.ReserveCrewAttempt(ctx, f.deps, userCaller(), "", application.ReserveCrewRequest{
		Title: "never observed", RepoID: f.repoID,
	})
	if err != nil {
		t.Fatalf("ReserveCrewAttempt: %v", err)
	}
	view, err := LoadHealthView(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadHealthView: %v", err)
	}
	if _, ok := view.Crews[reserved.Crew.CrewID]; ok {
		t.Fatalf("Crews[%s] present, want no entry before the observer ever samples it", reserved.Crew.CrewID)
	}
	if len(view.Incidents) != 0 {
		t.Fatalf("incidents = %+v, want none", view.Incidents)
	}
}

func TestLoadHealthViewOrdersIncidentsByFirstObservedThenID(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	base := f.deps.Clock.Now().Add(-time.Hour)
	ws, err := f.deps.Store.GetWorkspace(ctx)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if err := f.deps.Store.Write(ctx, f.deps.Clock.Now(), func(tx persistence.Tx) error {
		if err := tx.InsertHealthIncident(persistence.HealthIncidentRecord{
			IncidentID: "incident_b", Scope: "session", ScopeID: "sB",
			Kind: "monitoring_unavailable", Status: "open", FirstObservedAt: base.Add(time.Minute), Reason: "r",
		}); err != nil {
			return err
		}
		if err := tx.InsertHealthIncident(persistence.HealthIncidentRecord{
			IncidentID: "incident_a", Scope: "session", ScopeID: "sA",
			Kind: "monitoring_unavailable", Status: "open", FirstObservedAt: base, Reason: "r",
		}); err != nil {
			return err
		}
		return appendFixtureEvent(t, f.deps, tx, ws.WorkspaceID, f.project.ProjectID)
	}); err != nil {
		t.Fatalf("seed incidents: %v", err)
	}
	view, err := LoadHealthView(ctx, f.deps)
	if err != nil {
		t.Fatalf("LoadHealthView: %v", err)
	}
	if len(view.Incidents) != 2 || view.Incidents[0].IncidentID != "incident_a" || view.Incidents[1].IncidentID != "incident_b" {
		t.Fatalf("incidents = %+v, want [incident_a, incident_b] in first-observed order", view.Incidents)
	}
}
