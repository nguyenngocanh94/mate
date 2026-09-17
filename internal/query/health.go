package query

import (
	"context"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/application"
)

// CrewHealthValue is the last durable health sample the ADR 0019 observer
// recorded for one Crew's held binding. It is read on its own pipeline
// (LoadHealthView), separately from and far more often than Snapshot: work
// outcome (Task/Crew status, in Snapshot) and runtime health are two
// independent axes, and a Crew can honestly be "Task: running" and
// "Runtime: missing" at the same time.
//
// Liveness/Activity mirror orchestration.HealthLiveness/HealthActivity's
// string values without this package importing that one (internal/ui/
// console/doc.go's import boundary forbids the Console from reaching
// internal/orchestration, and this package is what stands between them) -
// the same mirroring WorktreeStatus/BindingStatus already do for
// persistence's own enums.
type CrewHealthValue struct {
	Liveness   string
	Activity   string
	Reason     string
	ObservedAt time.Time
	ChangedAt  time.Time
}

// HealthIncident is one open health_incident row (ADR 0019 §8): the
// workspace-level notification a captain sees regardless of which Project
// they are currently viewing. A session-scoped incident (a failing Herdr
// query) names every Crew it affects in AffectedCrewIDs rather than one
// incident per Crew.
type HealthIncident struct {
	IncidentID      string
	Scope           string // "crew" or "session"
	ScopeID         string // a CrewID for scope "crew", a Herdr session name for scope "session"
	Kind            string
	Reason          string
	FirstObservedAt time.Time
	OpenedAt        time.Time
	AffectedCrewIDs []string
	AcknowledgedAt  *time.Time
	AcknowledgedBy  string
}

// HealthView is one lightweight read of the shared health/incident
// projection ADR 0019's observer writes. It carries no Project/Task context
// of its own - a renderer resolves a Crew's label against whatever Snapshot
// is already on screen. A Crew absent from Crews has simply never been
// sampled ("not yet checked"), which is a different, honest state from a
// Known sample whose Liveness is "absent" (sampled and found missing) or a
// failed LoadHealthView call (Unknown, the error return) - three states a
// caller must keep visibly distinct rather than collapsing them.
type HealthView struct {
	AsOf      time.Time
	Crews     map[string]Field[CrewHealthValue]
	Incidents []HealthIncident
}

// LoadHealthView reads ADR 0019's durable health/incident projection. It
// performs no runtime query and classifies nothing itself: every Console,
// owner or not, calls this on its own tick to render the one shared result
// the observer's owner produced (ADR 0019 §3 - "Console khác vẫn đọc được
// state... không lấy được lock thì không khởi tạo vòng reconcile thứ hai").
func LoadHealthView(ctx context.Context, deps application.Deps) (HealthView, error) {
	now := deps.Clock.Now().UTC()
	records, err := deps.Store.ListCrewHealth(ctx)
	if err != nil {
		return HealthView{}, err
	}
	crews := make(map[string]Field[CrewHealthValue], len(records))
	for _, r := range records {
		crews[r.CrewID] = KnownField(CrewHealthValue{
			Liveness:   r.Liveness,
			Activity:   r.Activity,
			Reason:     r.Reason,
			ObservedAt: r.ObservedAt,
			ChangedAt:  r.ChangedAt,
		})
	}
	incidentRows, err := deps.Store.ListHealthIncidents(ctx, "open")
	if err != nil {
		return HealthView{}, err
	}
	incidents := make([]HealthIncident, 0, len(incidentRows))
	for _, r := range incidentRows {
		var opened time.Time
		if r.OpenedAt != nil {
			opened = *r.OpenedAt
		}
		incidents = append(incidents, HealthIncident{
			IncidentID:      r.IncidentID,
			Scope:           r.Scope,
			ScopeID:         r.ScopeID,
			Kind:            r.Kind,
			Reason:          r.Reason,
			FirstObservedAt: r.FirstObservedAt,
			OpenedAt:        opened,
			AffectedCrewIDs: append([]string(nil), r.AffectedCrewIDs...),
			AcknowledgedAt:  r.AcknowledgedAt,
			AcknowledgedBy:  r.AcknowledgedBy,
		})
	}
	// incidentRows is already ordered by (first_observed_at, incident_id) -
	// persistence.ListHealthIncidents' own SQL (selectHealthIncident +
	// "ORDER BY first_observed_at, incident_id") - so incidents inherits that
	// order without a second sort here.
	return HealthView{AsOf: now, Crews: crews, Incidents: incidents}, nil
}
