package console

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/matev2/internal/query"
)

// fakeHealthCycle counts calls and returns whatever is queued, so tests can
// drive the tick chain deterministically instead of waiting on the real
// 5s cadence.
type fakeHealthCycle struct {
	calls int
	views []HealthView
	errs  []error
}

func (f *fakeHealthCycle) cycle(context.Context) (HealthView, error) {
	i := f.calls
	f.calls++
	var view HealthView
	var err error
	if i < len(f.views) {
		view = f.views[i]
	}
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return view, err
}

// crewHealthCrews and crewHealthCrews2 are the two Crew ids sampleTree()
// seeds: crew_01J9P4Q5R6S7T8U9V0W1X2A7CS (attempt 1) and
// crew_01J9P6Q6W0E5V8XK2M4B8DT (attempt 2).
const (
	sampleCrew1 = "crew_01J9P4Q5R6S7T8U9V0W1X2A7CS"
	sampleCrew2 = "crew_01J9P6Q6W0E5V8XK2M4B8DT"
)

// loadedWithHealth is loaded (model_test.go) plus WithHealth, driving
// Init's tea.Batch(loadCmd, healthCmd) by hand: send() only applies one
// concrete tea.Msg, so a Batch's own Cmd (which returns tea.BatchMsg, a
// slice of further Cmds) has to be unpacked here rather than reused from
// the shared helper - keeping that unpacking local to this file rather
// than complicating model_test.go's loaded() for every other suite.
func loadedWithHealth(t *testing.T, tree query.Snapshot, cycle HealthCycleFunc, action ActionFunc) Model {
	t.Helper()
	tree.AsOf = goldenAsOf
	m := New(func(context.Context) (query.Snapshot, error) { return tree, nil }, nil, action)
	m = m.WithHealth(cycle)
	m.p = plainPalette()
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 160, Height: 48})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned a nil Cmd with a health port wired")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init() msg = %T, want tea.BatchMsg", msg)
	}
	for _, sub := range batch {
		m, _ = send(t, m, sub())
	}
	return m
}

func TestInitWithHealthPortRunsOneCycleAndAppliesIt(t *testing.T) {
	fake := &fakeHealthCycle{views: []HealthView{{
		HealthView: query.HealthView{AsOf: goldenAsOf, Crews: map[string]query.Field[query.CrewHealthValue]{
			sampleCrew1: query.KnownField(query.CrewHealthValue{Liveness: "present", Activity: "active", ObservedAt: goldenAsOf}),
		}},
	}}}
	m := loadedWithHealth(t, sampleTree(), fake.cycle, nil)
	if fake.calls != 1 {
		t.Fatalf("cycle calls = %d, want exactly 1 from Init", fake.calls)
	}
	got, ok := m.health.Crews[sampleCrew1]
	if !ok || got.Value.Liveness != "present" {
		t.Fatalf("health.Crews[%s] = %+v ok=%v, want the fake's present sample applied", sampleCrew1, got, ok)
	}
}

// TestHealthTickReschedulesAfterFailureWithoutStoppingMonitoring is ADR
// 0019 §7.6: a failed cycle (a lost Herdr connection at the owner, a DB
// error) must degrade healthErr and still schedule the next tick, not wedge
// the whole pipeline - "DB lỗi thì... retry quan sát ở vòng sau".
func TestHealthTickReschedulesAfterFailureWithoutStoppingMonitoring(t *testing.T) {
	fake := &fakeHealthCycle{errs: []error{errors.New("db busy")}}
	m := loadedWithHealth(t, sampleTree(), fake.cycle, nil)
	if m.healthErr == "" {
		t.Fatal("healthErr empty after a failed cycle")
	}
	// onHealthResult must have scheduled a fresh tick (healthTickCmd), which
	// onHealthTick then turns into a second cycle call.
	_, cmd := send(t, m, healthTickMsg{})
	if cmd == nil {
		t.Fatal("onHealthTick returned a nil Cmd, want another cycle scheduled")
	}
	cmd() // runs fakeHealthCycle.cycle a second time
	if fake.calls != 2 {
		t.Fatalf("cycle calls = %d, want 2 (Init's + the rescheduled tick's)", fake.calls)
	}
}

// TestHealthResultAppliesOnlyLatestReplacesStalePrevious proves a later
// Known result really does overwrite an earlier one (the two states are
// not silently merged), the shape a mutation deleting "m.health = msg.view"
// would hide.
func TestHealthResultReplacesPreviousView(t *testing.T) {
	fake := &fakeHealthCycle{views: []HealthView{
		{HealthView: query.HealthView{AsOf: goldenAsOf, Crews: map[string]query.Field[query.CrewHealthValue]{
			sampleCrew1: query.KnownField(query.CrewHealthValue{Liveness: "present", ObservedAt: goldenAsOf}),
		}}},
	}}
	m := loadedWithHealth(t, sampleTree(), fake.cycle, nil)
	second := HealthView{HealthView: query.HealthView{AsOf: goldenAsOf.Add(time.Minute), Crews: map[string]query.Field[query.CrewHealthValue]{
		sampleCrew1: query.KnownField(query.CrewHealthValue{Liveness: "absent", ObservedAt: goldenAsOf.Add(time.Minute)}),
	}}}
	m, _ = send(t, m, healthResultMsg{view: second})
	got := m.health.Crews[sampleCrew1]
	if got.Value.Liveness != "absent" {
		t.Fatalf("liveness = %q after second result, want absent", got.Value.Liveness)
	}
}

// TestOnHealthResultStampsHealthLastAttemptEvenOnFailure is PR 92
// counter-review B2(b): healthNow() must advance on a FAILED cycle too, not
// only a successful one, or a sample frozen by an outage is compared
// against itself for as long as the outage lasts and can never age into
// "stale". A success followed by three failures must still let a
// previously-fresh sample cross healthFreshWindow.
func TestOnHealthResultStampsHealthLastAttemptEvenOnFailure(t *testing.T) {
	fake := &fakeHealthCycle{}
	m := loadedWithHealth(t, sampleTree(), fake.cycle, nil)
	// Establish a known-good baseline directly (loadedWithHealth's own
	// Init-driven first cycle stamps a real wall-clock time, not a
	// controllable one), the same pattern TestHealthResultReplacesPreviousView
	// already uses.
	baseline := HealthView{HealthView: query.HealthView{AsOf: goldenAsOf, Crews: map[string]query.Field[query.CrewHealthValue]{
		sampleCrew1: query.KnownField(query.CrewHealthValue{Liveness: "present", ObservedAt: goldenAsOf}),
	}}}
	m, _ = send(t, m, healthResultMsg{at: goldenAsOf, view: baseline})
	if !m.healthNow().Equal(goldenAsOf) {
		t.Fatalf("healthNow() after the first successful cycle = %v, want %v", m.healthNow(), goldenAsOf)
	}
	if healthStale(m.healthNow(), m.health.Crews[sampleCrew1]) {
		t.Fatal("sample reported stale immediately after a successful cycle")
	}

	failAt := goldenAsOf.Add(healthFreshWindow + time.Minute)
	m, _ = send(t, m, healthResultMsg{at: failAt, err: errors.New("db busy")})
	if m.healthErr == "" {
		t.Fatal("healthErr empty after a failed cycle")
	}
	if !m.healthNow().Equal(failAt) {
		t.Fatalf("healthNow() after a FAILED cycle = %v, want it stamped from the failed attempt (%v), not left at the last success", m.healthNow(), failAt)
	}
	// m.health itself is untouched by the failure (the last good sample is
	// preserved), but it must now read as stale because healthNow() has
	// moved on without it.
	if got := m.health.Crews[sampleCrew1]; !healthStale(m.healthNow(), got) {
		t.Fatalf("sample = %+v at healthNow()=%v, want it to have aged into stale during the outage", got, m.healthNow())
	}
}

// TestHeaderShowsMonitoringErrorOnlyDuringAFailingCycle is PR 92
// counter-review B2(a): a failing health pipeline must be visible, not
// silent, and must stop being visible the moment a cycle succeeds again.
func TestHeaderShowsMonitoringErrorOnlyDuringAFailingCycle(t *testing.T) {
	fake := &fakeHealthCycle{views: []HealthView{{HealthView: query.HealthView{AsOf: goldenAsOf}}}}
	m := loadedWithHealth(t, sampleTree(), fake.cycle, nil)
	if strings.Contains(renderFrame(t, m), "monitoring error") {
		t.Fatal("header shows a monitoring error before any cycle has failed")
	}

	m, _ = send(t, m, healthResultMsg{at: goldenAsOf.Add(time.Second), err: errors.New("state_conflict: database is busy")})
	view := renderFrame(t, m)
	if !strings.Contains(view, "monitoring error") {
		t.Fatalf("header after a failed cycle = %q, want a monitoring-error marker", firstLine(view))
	}

	// A later success clears it.
	m, _ = send(t, m, healthResultMsg{at: goldenAsOf.Add(2 * time.Second), view: HealthView{HealthView: query.HealthView{AsOf: goldenAsOf.Add(2 * time.Second)}}})
	if strings.Contains(renderFrame(t, m), "monitoring error") {
		t.Fatal("header still shows a monitoring error after a later cycle succeeded")
	}
}

func TestCrewHealthNotYetCheckedIsAbsentNotZeroValue(t *testing.T) {
	m := loadedWithHealth(t, sampleTree(), (&fakeHealthCycle{}).cycle, nil)
	f := m.crewHealth(sampleCrew1)
	if f.State != query.Absent || f.Reason != "not yet checked" {
		t.Fatalf("crewHealth on an unsampled crew = %+v, want Absent/\"not yet checked\"", f)
	}
}

func TestCrewRowWarningDistinguishesMissingStaleAndHealthy(t *testing.T) {
	now := goldenAsOf
	cases := []struct {
		name string
		f    query.Field[query.CrewHealthValue]
		want string // substring expected, "" means no warning at all
	}{
		{"present and fresh draws nothing", query.KnownField(query.CrewHealthValue{Liveness: "present", ObservedAt: now}), ""},
		{"absent draws runtime missing", query.KnownField(query.CrewHealthValue{Liveness: "absent", ObservedAt: now}), "runtime missing"},
		{"unknown liveness draws runtime unknown", query.KnownField(query.CrewHealthValue{Liveness: "unknown", ObservedAt: now}), "runtime unknown"},
		{"identity mismatch draws its own word", query.KnownField(query.CrewHealthValue{Liveness: "identity_mismatch", ObservedAt: now}), "identity mismatch"},
		{"stale overrides a present liveness", query.KnownField(query.CrewHealthValue{Liveness: "present", ObservedAt: now.Add(-time.Hour)}), "stale"},
		// ADR 0019 §6 rule 7b: a Crew that is alive and has never done any
		// work must not sit on a row looking exactly like a working one.
		{"never started work draws on a present row", query.KnownField(query.CrewHealthValue{Liveness: "present", Activity: "never_started", ObservedAt: now}), "never started work"},
		{"activity unknown still draws nothing", query.KnownField(query.CrewHealthValue{Liveness: "present", Activity: "activity_unknown", ObservedAt: now}), ""},
		{"not yet checked draws nothing on the row", query.AbsentField[query.CrewHealthValue]("not yet checked"), ""},
		{"a failed read draws nothing on the row (footer/incidents carry it)", query.UnknownField[query.CrewHealthValue]("db busy"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spans := crewHealthWarningSpan(now, tc.f, unicodeGlyphs, plainPalette())
			text := renderSpans(spans, 80)
			if tc.want == "" {
				if strings.TrimSpace(text) != "" {
					t.Fatalf("warning = %q, want none", text)
				}
				return
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("warning = %q, want it to contain %q", text, tc.want)
			}
		})
	}
}

// TestRuntimeHealthInspectorDistinguishesReachableStates is the closing
// condition itself, over the states query.LoadHealthView can actually
// produce: a Known field whose Liveness value is "unknown" (the runtime
// query failed for that one Crew - a real, reachable classifier output),
// stale and not yet checked must each be distinguishable from missing, and
// from each other. (PR 92 counter-review B2(c): a Field[T] whose own STATE
// is Unknown is a different, NOT production-reachable thing - see
// TestRuntimeHealthInspectorUnknownFieldStateDegradesSafely below - and is
// no longer conflated with this closing condition.)
func TestRuntimeHealthInspectorDistinguishesReachableStates(t *testing.T) {
	now := goldenAsOf
	g, p := unicodeGlyphs, plainPalette()
	missing := renderSpans(runtimeHealthValueSpans(now, query.KnownField(query.CrewHealthValue{Liveness: "absent", ObservedAt: now}), g, p), 80)
	unknownLiveness := renderSpans(runtimeHealthValueSpans(now, query.KnownField(query.CrewHealthValue{Liveness: "unknown", ObservedAt: now}), g, p), 80)
	notYetChecked := renderSpans(runtimeHealthValueSpans(now, query.AbsentField[query.CrewHealthValue]("not yet checked"), g, p), 80)
	stale := renderSpans(runtimeHealthValueSpans(now, query.KnownField(query.CrewHealthValue{Liveness: "present", ObservedAt: now.Add(-time.Hour)}), g, p), 80)
	fresh := renderSpans(runtimeHealthValueSpans(now, query.KnownField(query.CrewHealthValue{Liveness: "present", ObservedAt: now}), g, p), 80)

	all := map[string]string{"missing": missing, "unknown liveness": unknownLiveness, "not yet checked": notYetChecked, "stale": stale, "fresh": fresh}
	for name, text := range all {
		for other, otherText := range all {
			if name != other && text == otherText {
				t.Fatalf("%q and %q render identically (%q) - closing condition requires each distinguishable", name, other, text)
			}
		}
	}
	if !strings.Contains(missing, "runtime missing") {
		t.Fatalf("missing rendering = %q, want the humanised word shared with the row and incident overlay (PR 92 B1)", missing)
	}
	if !strings.Contains(unknownLiveness, "runtime unknown") {
		t.Fatalf("unknown-liveness rendering = %q, want the humanised word", unknownLiveness)
	}
	if !strings.Contains(notYetChecked, "not yet checked") {
		t.Fatalf("not-yet-checked rendering = %q", notYetChecked)
	}
	if !strings.Contains(stale, "stale") {
		t.Fatalf("stale rendering = %q, want a stale marker", stale)
	}
	if strings.Contains(fresh, "stale") {
		t.Fatalf("fresh rendering = %q, must not say stale", fresh)
	}
}

// TestRuntimeHealthInspectorUnknownFieldStateDegradesSafely is NOT a
// closing-condition proof: query.LoadHealthView (internal/query/health.go)
// reads crew_health/health_incident as two whole-collection queries and
// never produces a per-Crew query.Field[CrewHealthValue] in the Unknown
// state - only a whole-view error, handled separately via healthErr (see
// TestOnHealthResultStampsHealthLastAttemptEvenOnFailure). This only proves
// runtimeHealthValueSpans/runtimeHealthReasonSpans degrade to visible,
// non-empty text rather than panicking or rendering blank if a Field this
// package did not itself produce is ever passed in (PR 92 counter-review
// B2(c): the previous version of this test asserted this was an exercised,
// distinguishable UI state, which was not true).
func TestRuntimeHealthInspectorUnknownFieldStateDegradesSafely(t *testing.T) {
	g, p := unicodeGlyphs, plainPalette()
	f := query.UnknownField[query.CrewHealthValue]("session query failed")
	value := renderSpans(runtimeHealthValueSpans(goldenAsOf, f, g, p), 80)
	reason := renderSpans(runtimeHealthReasonSpans(f, p), 80)
	if strings.TrimSpace(value) == "" {
		t.Fatal("runtimeHealthValueSpans rendered nothing for an Unknown field")
	}
	if !strings.Contains(reason, "session query failed") {
		t.Fatalf("runtimeHealthReasonSpans = %q, want the field's own reason", reason)
	}
}

func TestCrewInspectorShowsRuntimeHealthOnlyWhenWired(t *testing.T) {
	fake := &fakeHealthCycle{views: []HealthView{{HealthView: query.HealthView{AsOf: goldenAsOf}}}}
	withHealth := loadedWithHealth(t, sampleTree(), fake.cycle, nil)
	var ok bool
	withHealth, ok = withHealth.jumpToCrew(sampleCrew1)
	if !ok {
		t.Fatal("jumpToCrew failed to locate the sample crew")
	}
	r, ok := withHealth.selectedRow()
	if !ok || r.kind != rowCrew {
		t.Fatalf("selected row after jumpToCrew = %+v ok=%v, want the crew row", r, ok)
	}
	view := withHealth.crewFields(r, 40)
	found := false
	for _, l := range view {
		if strings.Contains(l.render(80), "Runtime health") {
			found = true
		}
	}
	if !found {
		t.Fatal("crewFields with a health port wired never shows Runtime health")
	}

	bare := loaded(t, sampleTree(), nil)
	bare, ok = bare.jumpToCrew(sampleCrew1)
	if !ok {
		t.Fatal("jumpToCrew failed to locate the sample crew")
	}
	r, ok = bare.selectedRow()
	if !ok || r.kind != rowCrew {
		t.Fatalf("selected row after jumpToCrew = %+v ok=%v, want the crew row", r, ok)
	}
	view = bare.crewFields(r, 40)
	for _, l := range view {
		if strings.Contains(l.render(80), "Runtime health") {
			t.Fatal("crewFields with no health port wired must not show Runtime health at all")
		}
	}
}

// ---------- incidents overlay ----------

func healthIncidentTestView() HealthView {
	return HealthView{HealthView: query.HealthView{
		AsOf: goldenAsOf,
		Crews: map[string]query.Field[query.CrewHealthValue]{
			sampleCrew1: query.KnownField(query.CrewHealthValue{Liveness: "absent", ObservedAt: goldenAsOf}),
		},
		Incidents: []query.HealthIncident{
			{IncidentID: "incident_1", Scope: "crew", ScopeID: sampleCrew1, Kind: "runtime_missing",
				Reason: "three consecutive absent samples", FirstObservedAt: goldenAsOf.Add(-time.Hour), AffectedCrewIDs: []string{sampleCrew1}},
			{IncidentID: "incident_2", Scope: "session", ScopeID: "mate-acme", Kind: "monitoring_unavailable",
				Reason: "session query failed", FirstObservedAt: goldenAsOf.Add(-30 * time.Minute), AffectedCrewIDs: []string{sampleCrew1, sampleCrew2}},
		},
	}}
}

func TestIncidentsKeyOpensOnlyWhenHealthWired(t *testing.T) {
	bare := loaded(t, sampleTree(), nil)
	if bare.incidentsAvailable() {
		t.Fatal("incidentsAvailable true with no health port wired")
	}
	bare, _ = send(t, bare, key("i"))
	if bare.incidents.open {
		t.Fatal("'i' opened the incidents overlay with no health port wired")
	}

	fake := &fakeHealthCycle{views: []HealthView{healthIncidentTestView()}}
	wired := loadedWithHealth(t, sampleTree(), fake.cycle, nil)
	if !wired.incidentsAvailable() {
		t.Fatal("incidentsAvailable false with a health port wired and a ready snapshot")
	}
	wired, _ = send(t, wired, key("i"))
	if !wired.incidents.open {
		t.Fatal("'i' did not open the incidents overlay")
	}
	view := renderFrame(t, wired)
	if !strings.Contains(view, "2 open") {
		t.Fatalf("incidents view = %q, want the open count", view)
	}
}

func TestIncidentsHeaderBadgeShowsOpenCount(t *testing.T) {
	fake := &fakeHealthCycle{views: []HealthView{healthIncidentTestView()}}
	m := loadedWithHealth(t, sampleTree(), fake.cycle, nil)
	view := renderFrame(t, m)
	if !strings.Contains(view, "! 2 incidents") {
		t.Fatalf("header = %q, want the open-incident badge even while the incidents overlay is closed", firstLine(view))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func TestIncidentsEnterJumpsToAffectedCrewAndCloses(t *testing.T) {
	fake := &fakeHealthCycle{views: []HealthView{healthIncidentTestView()}}
	m := loadedWithHealth(t, sampleTree(), fake.cycle, nil)
	m, _ = send(t, m, key("i"))
	m, _ = send(t, m, key("enter")) // incident_1, scope crew, sampleCrew1
	if m.incidents.open {
		t.Fatal("Enter did not close the incidents overlay")
	}
	r, ok := m.selectedRow()
	if !ok || r.id != sampleCrew1 {
		t.Fatalf("selected row = %+v ok=%v, want crew %s selected", r, ok, sampleCrew1)
	}
	if m.cur().kind != frameTask {
		t.Fatalf("frame kind = %v, want frameTask after jumping to a crew", m.cur().kind)
	}
}

func TestIncidentsEnterOnSessionScopeJumpsToFirstAffectedCrew(t *testing.T) {
	fake := &fakeHealthCycle{views: []HealthView{healthIncidentTestView()}}
	m := loadedWithHealth(t, sampleTree(), fake.cycle, nil)
	m, _ = send(t, m, key("i"))
	m, _ = send(t, m, key("down")) // incident_2, session scope
	m, _ = send(t, m, key("enter"))
	r, ok := m.selectedRow()
	if !ok || r.id != sampleCrew1 {
		t.Fatalf("selected row = %+v ok=%v, want the session incident's first affected crew %s", r, ok, sampleCrew1)
	}
}

func TestIncidentsAcknowledgeCallsActionPortAndReportsFailure(t *testing.T) {
	var gotReq ActionRequest
	failing := func(_ context.Context, req ActionRequest) (string, error) {
		gotReq = req
		return "", errors.New("state_conflict: incident already resolved")
	}
	fake := &fakeHealthCycle{views: []HealthView{healthIncidentTestView()}}
	m := loadedWithHealth(t, sampleTree(), fake.cycle, failing)
	m, _ = send(t, m, key("i"))
	m, cmd := send(t, m, key("a"))
	if cmd == nil {
		t.Fatal("acknowledge did not return a Cmd")
	}
	msg := cmd()
	ackMsg, ok := msg.(incidentAckDoneMsg)
	if !ok {
		t.Fatalf("acknowledge msg = %T, want incidentAckDoneMsg", msg)
	}
	m, _ = send(t, m, ackMsg)
	if gotReq.Action != ActionAcknowledgeIncident || gotReq.Target != "incident_1" || gotReq.TargetKind != "health_incident" {
		t.Fatalf("action request = %+v, want ActionAcknowledgeIncident on incident_1", gotReq)
	}
	if !strings.Contains(m.msg.text, "Acknowledge failed") {
		t.Fatalf("footer message = %q, want the failure reported", m.msg.text)
	}
}

func TestIncidentsAcknowledgeSucceeds(t *testing.T) {
	ok := func(_ context.Context, req ActionRequest) (string, error) { return "acked", nil }
	fake := &fakeHealthCycle{views: []HealthView{healthIncidentTestView()}}
	m := loadedWithHealth(t, sampleTree(), fake.cycle, ok)
	m, _ = send(t, m, key("i"))
	m, cmd := send(t, m, key("a"))
	m, _ = send(t, m, cmd())
	if !strings.Contains(m.msg.text, "Acknowledged incident_1") {
		t.Fatalf("footer message = %q, want acknowledgement confirmed", m.msg.text)
	}
}

func TestIncidentsEscCloses(t *testing.T) {
	fake := &fakeHealthCycle{views: []HealthView{healthIncidentTestView()}}
	m := loadedWithHealth(t, sampleTree(), fake.cycle, nil)
	m, _ = send(t, m, key("i"))
	m, _ = send(t, m, key("esc"))
	if m.incidents.open {
		t.Fatal("esc did not close the incidents overlay")
	}
}

// TestObservingAndAcknowledgingNeverMutateTheTreeSnapshot is the Console-
// side half of the observer's own "only observes" property: applying health
// results and acknowledging must never touch m.tree (the Crew/Task/Project
// Snapshot) - the orchestration-level mutation sentinel already covers the
// backend; this covers the Console not inventing a second mutation path of
// its own on top of it.
func TestObservingAndAcknowledgingNeverMutateTheTreeSnapshot(t *testing.T) {
	acked := func(_ context.Context, req ActionRequest) (string, error) { return "ok", nil }
	fake := &fakeHealthCycle{views: []HealthView{healthIncidentTestView(), healthIncidentTestView()}}
	m := loadedWithHealth(t, sampleTree(), fake.cycle, acked)
	before := m.tree
	m, _ = send(t, m, healthTickMsg{})
	m, _ = send(t, m, key("i"))
	m, cmd := send(t, m, key("a"))
	m, _ = send(t, m, cmd())
	// reflect.DeepEqual, not a hand-rolled field-by-field comparator: the
	// previous version here compared only Projects' length/ProjectID/Tasks
	// length and AsOf, so it would not have caught a mutated Crew status or
	// Task title (PR 92 counter-review N4). query.Snapshot holds only
	// slices/structs/strings/times - nothing DeepEqual cannot compare.
	if got := m.tree; !reflect.DeepEqual(before, got) {
		t.Fatalf("tree Snapshot changed after health ticks/acknowledge:\nbefore=%+v\nafter=%+v", before, got)
	}
}
