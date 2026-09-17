package console

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// ADR 0019 G7-04a2: the Console owner/scheduler and the health/incident
// presentation. This file owns only the Console-side state machine (the
// tick chain, freshness banding, row/inspector rendering); the port itself
// (HealthCycleFunc) is built by cmd/mate's bridge from internal/workspace's
// OS advisory lock, internal/orchestration's ObserveCrewHealth/
// ReconcileCrewHealth and internal/query.LoadHealthView - this package
// never reaches any of those directly (doc.go, boundary_test.go).
//
// The tick chain starts once, in Init, and never stops for the life of the
// program: unlike session mode (which starts and ends with Enter/Esc on one
// row), health monitoring is not a mode the reader enters - it runs
// regardless of which screen is on top, including while Agent View owns
// the keyboard (closing condition 5: "captain typing in Agent View... not
// stealing focus or losing a keystroke"), because it is driven by its own
// independent tea.Tick/Cmd chain that Update processes like any other
// message, the same way session mode's own poll ticks already coexist with
// list navigation.

// HealthCycleFunc is the ADR 0019 §3 Console-owner port. On every call it
// either runs one observe/reconcile cycle (when this process currently
// holds the workspace's health-owner lock) or simply re-reads the shared
// projection (when it does not - "Console khác vẫn đọc được state... không
// lấy được lock thì không khởi tạo vòng reconcile thứ hai"); which of the
// two happened is Owner on the returned HealthView.
//
// Non-overlap is structural here, not something this port has to enforce
// itself: Update only schedules the next tick after this call's result
// message has been applied, exactly like session mode's own poll chain
// (session_mode.go's onSessionTick) - exactly one cycle is ever in flight.
// The deadline is only PARTIALLY structural, and this comment previously
// overclaimed it (PR 92 counter-review N2): orchestration.ObserveCrewHealth
// bounds itself at Config.CycleTimeout, but orchestration.ReconcileCrewHealth
// and query.LoadHealthView both run on whatever ctx this func is called
// with - cmd/mate's bridge passes the Console's own unbounded m.baseCtx(),
// so a slow reconcile or read is not itself time-boxed by this port.
type HealthCycleFunc func(ctx context.Context) (HealthView, error)

// HealthView is one health tick's result: the shared crew health/open
// incident projection (query.HealthView, exactly as LoadHealthView read it
// - this package classifies nothing of its own) plus whether this process
// is currently the cycle's owner.
type HealthView struct {
	query.HealthView
	Owner bool
}

// healthPollInterval is the Console's own tick cadence for the health
// pipeline, and the only thing that actually paces it. It is deliberately
// independent of orchestration.HealthConfig.PollInterval, which - despite
// what an earlier version of this comment claimed - is not read anywhere to
// pace anything (PR 92 counter-review N3): it flows only into a
// config-version digest (health.go's effectiveHealthConfig/validate). A
// non-owner Console re-reading the shared projection at this rate is one
// cheap DB read, unrelated to how often the owner actually samples runtime.
const healthPollInterval = 5 * time.Second

// healthFreshWindow is how old a Known health sample may be before the
// Console calls it stale rather than fresh - the closing condition "Sleep
// or restart: old data shows as stale, and the unobserved interval is never
// counted as evidence of failure." It is deliberately generous relative to
// the observer's own default poll_interval/cycle_timeout (5s/10s, ADR 0019
// §3): a live owner refreshes far more often than this, so a sample older
// than it means either there is currently no owner or the workspace just
// woke from sleep - both cases where the honest word is "stale", never a
// freshness invented from silence.
const healthFreshWindow = 30 * time.Second

type healthTickMsg struct{}

// healthResultMsg carries at, the wall-clock moment this cycle's result
// arrived, whether the cycle succeeded or failed. onHealthResult stamps it
// onto Model.healthLastAttempt unconditionally - see that field's own doc
// comment for why (PR 92 counter-review B2).
type healthResultMsg struct {
	at   time.Time
	view HealthView
	err  error
}

func healthTickCmd() tea.Cmd {
	return tea.Tick(healthPollInterval, func(time.Time) tea.Msg { return healthTickMsg{} })
}

func healthCycleCmd(ctx context.Context, cycle HealthCycleFunc) tea.Cmd {
	return func() tea.Msg {
		view, err := cycle(ctx)
		return healthResultMsg{at: time.Now(), view: view, err: err}
	}
}

// WithHealth attaches the ADR 0019 G7-04a2 health port. A nil cycle (the
// zero value, when WithHealth is never called) keeps the Console reading no
// health at all - every existing test and every row/inspector helper below
// degrades to "not yet checked" rather than panicking on a nil map.
func (m Model) WithHealth(cycle HealthCycleFunc) Model {
	m.healthCycle = cycle
	return m
}

// beginHealth starts the tick chain, or does nothing if no port is wired.
// Called once, from Init - never from beginSession/onEnter/open, because
// the whole point is that monitoring is not tied to which screen the
// reader has open.
func (m Model) beginHealth() tea.Cmd {
	if m.healthCycle == nil {
		return nil
	}
	return healthCycleCmd(m.baseCtx(), m.healthCycle)
}

// onHealthResult applies one cycle's result and always reschedules the next
// tick, success or failure: a failed cycle (a DB error, a lost Herdr
// connection at the owner) degrades healthErr to a message the header
// renders (headerLine, frame.go) without discarding the last good view, and
// must never stop monitoring (ADR 0019 §7.6 - "DB lỗi thì hiển thị
// monitoring error... retry quan sát ở vòng sau"). healthLastAttempt is
// stamped from msg.at on EVERY result, success or failure: unlike
// health.AsOf (which only advances when a cycle actually succeeds), this is
// what healthNow() reports, so a sample that stops refreshing during an
// outage genuinely ages past healthFreshWindow into "stale" instead of
// being compared against itself forever (PR 92 counter-review B2).
func (m Model) onHealthResult(msg healthResultMsg) (Model, tea.Cmd) {
	m.healthLastAttempt = msg.at
	if msg.err != nil {
		m.healthErr = msg.err.Error()
		return m, healthTickCmd()
	}
	m.health = msg.view
	m.healthErr = ""
	return m, healthTickCmd()
}

func (m Model) onHealthTick(healthTickMsg) (Model, tea.Cmd) {
	if m.healthCycle == nil {
		return m, nil
	}
	return m, healthCycleCmd(m.baseCtx(), m.healthCycle)
}

// crewHealth looks up one Crew's last sample. A Crew absent from the map has
// simply never been sampled - "not yet checked", a distinct, honest state
// from a Known sample whose Liveness is "absent" (sampled and found
// missing). See query.HealthView's own doc comment.
func (m Model) crewHealth(crewID string) query.Field[query.CrewHealthValue] {
	if f, ok := m.health.Crews[crewID]; ok {
		return f
	}
	return query.AbsentField[query.CrewHealthValue]("not yet checked")
}

// healthStale reports whether a Known sample is old enough that the Console
// must say so rather than let a captain returning from sleep believe a
// stopped-refreshing "present" was still current.
func healthStale(now time.Time, f query.Field[query.CrewHealthValue]) bool {
	return f.State == query.Known && now.Sub(f.Value.ObservedAt) > healthFreshWindow
}

// healthNow is the timestamp freshness banding is computed against: the
// health pipeline's own last-attempt time (Model.healthLastAttempt), stamped
// on every cycle result whether it succeeded or failed - never a live wall
// clock reached from inside View (deterministic for tests), and
// deliberately NOT health.AsOf, which only advances on success and would
// otherwise compare a frozen sample against itself for the whole length of
// an outage (PR 92 counter-review B2).
func (m Model) healthNow() time.Time {
	return m.healthLastAttempt
}

// healthWordTable maps an ADR 0019 health/incident enum value (Liveness,
// Activity, or a HealthIncidentKind - internal/orchestration/health.go's own
// string constants, mirrored here without importing that package, per
// query.CrewHealthValue/query.HealthIncident's own doc comment) to the one
// operator-facing word every surface shares: the Crew row warning
// (crewHealthWarningSpan), the Crew inspector (runtimeHealthValueSpans) and
// the incident overlay (incidents.go's healthIncidentKindSpan) all read
// healthWord rather than each inventing its own text, so the same Crew
// cannot read "identity mismatch" in one place and "identity_mismatch" in
// another (PR 92 counter-review B1). "absent" and "runtime_missing" share
// one word ("runtime missing") deliberately: they are the same fact seen
// from the Crew-liveness and the incident-kind vocabularies.
var healthWordTable = map[string]string{
	"absent":                 "runtime missing",
	"unknown":                "runtime unknown",
	"runtime_unbound":        "no runtime binding",
	"identity_mismatch":      "identity mismatch",
	"activity_unknown":       "activity unknown",
	"never_started":          "never started work",
	"waiting_for_mate":       "waiting for mate",
	"waiting_for_user":       "waiting for user",
	"provider_retrying":      "provider retrying",
	"idle_after_error":       "idle after harness error",
	"tool_running":           "tool running",
	"stalled_suspected":      "stalled (suspected)",
	"idle_after_report":      "idle after report",
	"idle_unreported":        "idle, not yet reported",
	"runtime_missing":        "runtime missing",
	"monitoring_unavailable": "monitoring unavailable",
	"harness_error":          "harness error",
	"wait_reminder":          "wait reminder",
	"operation_needs_check":  "operation needs check",
}

// healthWord looks up raw in healthWordTable, falling back to raw itself
// when unmapped - honest (the value is still shown) rather than blanking an
// enum this table has not been taught yet.
func healthWord(raw string) string {
	if w, ok := healthWordTable[raw]; ok {
		return w
	}
	return raw
}

// crewHealthWarningSpan is the Crew row's short runtime-health warning
// (part 2 of ADR 0019 G7-04a2's closing conditions: "the Crew row carries a
// short warning"). A happy classification (present, fresh, no open
// incident) draws nothing - a healthy fleet must not fill every row with a
// marker nobody asked for - but a stale sample always draws one regardless
// of the liveness word underneath it, because silence must never render as
// current fact (the bar: "a stalled or unobserved state must never look
// healthy").
//
// Liveness drives this, plus exactly one Activity value: never_started.
// The other activity-based warnings (stalled_suspected, idle after harness
// error) are still deliberately not handled, because the classifier still
// has no turn/tool coverage to produce them honestly - rendering "stalled"
// here would contradict that before G7-04b ships the capability that makes
// it true (PR 92 counter-review N8). never_started is different in kind: it
// is decided from the absence of a harness transcript, not from coverage
// this observer does not have (ADR 0019 §6 rule 7b), and a Crew that has
// never done any work is the one thing that must not sit on a row looking
// exactly like a healthy one.
func crewHealthWarningSpan(now time.Time, f query.Field[query.CrewHealthValue], g glyphSet, p palette) []span {
	if f.State != query.Known {
		return nil
	}
	if healthStale(now, f) {
		return []span{{text: "stale runtime data", style: p.Amber}}
	}
	switch f.Value.Liveness {
	case "absent":
		return []span{failureSpan(healthWord("absent"), p)}
	case "identity_mismatch":
		return []span{failureSpan(healthWord("identity_mismatch"), p)}
	case "unknown":
		return []span{unknownMarkSpan(healthWord("unknown"), p)}
	}
	if f.Value.Activity == "never_started" {
		return []span{failureSpan(healthWord("never_started"), p)}
	}
	return nil
}

// healthLivenessStyle tints a raw ADR 0019 liveness word for the inspector
// block below - a small vocabulary of its own, kept separate from
// statusStyle (signals.go), which is documented against the specific
// Crew/Mate/worktree/binding status words it already serves.
func healthLivenessStyle(word string, p palette) lipgloss.Style {
	switch word {
	case "absent", "identity_mismatch":
		return p.Red
	case "unknown":
		return p.Amber
	case "starting", "transitioning", "runtime_unbound":
		return p.Dim
	default:
		return p.Fg
	}
}

// runtimeHealthValueSpans renders the "Runtime health" inspector value: the
// liveness/activity classification plus a freshness word, both through
// healthWord so the inspector never disagrees with the row (B1). This is
// the one place a Known-but-stale sample is distinguished from a
// Known-and-fresh one - the closing condition this whole block exists to
// satisfy: "unknown, stale and not yet checked must each be distinguishable
// from missing".
func runtimeHealthValueSpans(now time.Time, f query.Field[query.CrewHealthValue], g glyphSet, p palette) []span {
	switch f.State {
	case query.Known:
		out := []span{{text: healthWord(f.Value.Liveness), style: healthLivenessStyle(f.Value.Liveness, p)}}
		if f.Value.Activity != "" {
			out = append(out, span{text: " " + g.Dot + " " + healthWord(f.Value.Activity), style: p.Dim})
		}
		if healthStale(now, f) {
			out = append(out, span{text: " " + g.Dot + " stale", style: p.Amber})
		} else {
			out = append(out, span{text: " " + g.Dot + " fresh", style: p.Dim})
		}
		return out
	case query.Absent:
		return append([]span{{text: "not yet checked", style: p.Dim}}, reasonSpan(f.Reason, g, p)...)
	default:
		// query.LoadHealthView reads crew_health/health_incident as two
		// whole-collection queries (internal/query/health.go): a per-Crew
		// partial read failure is not expressible today, so a Field[T] in
		// this Unknown state never actually reaches this function in
		// production - unlike Snapshot's own per-row Unknown fields, which
		// come from many separate reads that really can fail independently.
		// This branch exists only so a Field[T] this package did not itself
		// produce (the Known/Absent/Unknown contract's own type, field.go)
		// degrades safely instead of falling through unhandled; it is not a
		// proven, exercised UI state (PR 92 counter-review B2(c)).
		return append([]span{{text: "unavailable", style: p.Amber}}, reasonSpan(f.Reason, g, p)...)
	}
}

// runtimeHealthLastCheckSpans renders when the sample was taken. Absent and
// Unknown fields have nothing to time-stamp, so they render the same "-"
// placeholder the design uses elsewhere for a field with no value at all.
func runtimeHealthLastCheckSpans(f query.Field[query.CrewHealthValue], p palette) []span {
	if f.State != query.Known {
		return []span{{text: "-", style: p.Dim}}
	}
	return []span{{text: f.Value.ObservedAt.Format("15:04:05"), style: p.Fg}}
}

// runtimeHealthReasonSpans renders the classifier's own reason text -
// Known's caveat/explanation, or Absent/Unknown's own reason. The default
// branch is the same not-production-reachable fallback runtimeHealthValueSpans
// documents (B2(c)).
func runtimeHealthReasonSpans(f query.Field[query.CrewHealthValue], p palette) []span {
	switch f.State {
	case query.Known:
		if f.Value.Reason == "" {
			return []span{{text: "-", style: p.Dim}}
		}
		return []span{{text: f.Value.Reason, style: p.Fg}}
	case query.Absent:
		return []span{{text: f.Reason, style: p.Dim}}
	default:
		return []span{{text: f.Reason, style: p.Amber}}
	}
}

// crewOpenIncidents returns the open incidents whose AffectedCrewIDs (crew
// scope: itself; session scope: every Crew sharing the failing session)
// name crewID, in the same order HealthView.Incidents already carries them.
func (m Model) crewOpenIncidents(crewID string) []query.HealthIncident {
	var out []query.HealthIncident
	for _, inc := range m.health.Incidents {
		for _, id := range inc.AffectedCrewIDs {
			if id == crewID {
				out = append(out, inc)
				break
			}
		}
	}
	return out
}
