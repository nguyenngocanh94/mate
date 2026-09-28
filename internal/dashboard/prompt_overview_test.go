package dashboard

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/diagnostics"
	"github.com/nguyenngocanh94/mate/internal/telemetry"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

func TestPromptOverviewConservesMateAndCrewUsage(t *testing.T) {
	f := newFixture(t)
	var mate MateResponse
	f.get(t, "/api/projects/"+fixtureProject+"/mate", http.StatusOK, &mate)
	check := func(label string, overview diagnostics.Overview, calls int64, tokens diagnostics.Tokens) {
		t.Helper()
		if overview.Summary == "" || overview.Categories == nil || overview.Sequence == nil {
			t.Fatalf("%s has no usable overview: %+v", label, overview)
		}
		var total diagnostics.Tokens
		var counted int64
		for _, c := range overview.Categories {
			if c.Tokens != nil {
				total.Add(*c.Tokens)
			}
			counted += int64(c.ModelCalls)
		}
		if total != tokens || counted != calls {
			t.Fatalf("%s overview has %d calls/%+v; prompt has %d/%+v", label, counted, total, calls, tokens)
		}
	}
	for _, exchange := range mate.Exchanges {
		check(exchange.ID, exchange.Overview, exchange.ModelCalls, performanceTokens(exchange.Tokens))
		if exchange.ModelCalls == 0 && len(exchange.Overview.Categories) != 0 {
			t.Fatal("an unanswered request borrowed another prompt's work")
		}
	}
	var crew TaskResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew, http.StatusOK, &crew)
	for _, prompt := range crew.Performance.PromptTurns {
		check(prompt.ID, prompt.Overview, int64(prompt.ModelCalls), prompt.Tokens)
	}
}

func TestMateOverviewKeepsNativeEvidenceInItsOwnPrompt(t *testing.T) {
	f := newFixture(t)
	actor := timeline.MateActorID(fixtureProject)
	var session, prompt string
	if err := f.read.SQL().QueryRow(`SELECT session_id,harness_turn_ref FROM turn WHERE actor_id=? AND harness_turn_ref IS NOT NULL ORDER BY started_at LIMIT 1`, actor).Scan(&session, &prompt); err != nil {
		t.Fatal(err)
	}
	start, end := fixtureNow.Add(-4*time.Second), fixtureNow.Add(-3*time.Second)
	exit := 1
	native := telemetry.Fact{Version: 1, ID: "overview-review", ExecutionID: "overview-review", Kind: "execution", SessionID: session,
		HarnessTurnRef: prompt, Command: "git diff -- src/overview_review.go", Tool: "exec_command", StartedAt: &start, CompletedAt: &end,
		Status: "failed", ExitCode: &exit, OccurredAt: end, ObservedAt: fixtureNow, MeasurementKind: "native",
		SourcePath: "overview-rollout.jsonl", SourceOffset: 431}
	insertDiagnosticFact(t, f, actor, native)
	var got MateResponse
	f.get(t, "/api/projects/"+fixtureProject+"/mate", http.StatusOK, &got)
	found := false
	for _, exchange := range got.Exchanges {
		for _, category := range exchange.Overview.Categories {
			for _, evidence := range category.Evidence {
				if !strings.Contains(evidence.Label, "overview_review.go") {
					continue
				}
				found = true
				if exchange.ID != session+"#prompt#"+prompt || category.Kind != "review" || evidence.SourceRef.Offset != 431 {
					t.Fatalf("native review attached incorrectly: exchange %s, category %+v", exchange.ID, category)
				}
			}
		}
	}
	if !found {
		t.Fatal("Mate overview omitted native command evidence without a model-call parent")
	}
}

func TestMateOverviewShowsPromptWorkBeforeUsageArrives(t *testing.T) {
	f := newFixture(t)
	actor := timeline.MateActorID(fixtureProject)
	var session string
	if err := f.read.SQL().QueryRow(`SELECT id FROM session WHERE actor_id=? LIMIT 1`, actor).Scan(&session); err != nil {
		t.Fatal(err)
	}
	start := fixtureNow.Add(time.Second)
	prompt := telemetry.Fact{Version: 1, ID: "overview-pending-prompt", Kind: "prompt", SessionID: session,
		HarnessTurnRef: "overview-pending", Text: "Look into the pending handback.", OccurredAt: start,
		SourcePath: "pending-rollout.jsonl", SourceOffset: 101}
	insertDiagnosticFact(t, f, actor, prompt)
	native := telemetry.Fact{Version: 1, ID: "overview-pending-tool", ExecutionID: "overview-pending-tool", Kind: "execution", SessionID: session,
		HarnessTurnRef: prompt.HarnessTurnRef, Command: "git diff -- src/pending.go", Tool: "exec_command", StartedAt: &start,
		Status: "running", OccurredAt: start, SourcePath: prompt.SourcePath, SourceOffset: 201}
	insertDiagnosticFact(t, f, actor, native)
	var got MateResponse
	f.get(t, "/api/projects/"+fixtureProject+"/mate", http.StatusOK, &got)
	for _, exchange := range got.Exchanges {
		if exchange.ID != session+"#prompt#"+prompt.HarnessTurnRef {
			continue
		}
		if exchange.ModelCalls != 0 || exchange.Prompt != prompt.Text || len(exchange.Overview.Categories) == 0 {
			t.Fatalf("native work before usage lost: %+v", exchange)
		}
		if exchange.StartedAt != db.FormatTime(start) {
			t.Fatal("native prompt time must use fixed database precision for exchange boundaries")
		}
		for _, category := range exchange.Overview.Categories {
			if category.Tokens != nil {
				t.Fatalf("unreported usage became a measured value: %+v", category)
			}
		}
		return
	}
	t.Fatal("native prompt before first usage omitted from Mate exchanges")
}

func TestMateOverviewUsesConfirmedNativePromptAlias(t *testing.T) {
	f := newFixture(t)
	actor := timeline.MateActorID(fixtureProject)
	var call, session, prompt, path string
	var offset int64
	if err := f.read.SQL().QueryRow(`SELECT id,session_id,harness_turn_ref,ref_path,ref_offset FROM turn WHERE actor_id=? AND harness_turn_ref IS NOT NULL ORDER BY started_at LIMIT 1`, actor).Scan(&call, &session, &prompt, &path, &offset); err != nil {
		t.Fatal(err)
	}
	w, err := db.Open(f.ws)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.SQL().Exec(`UPDATE turn SET harness_turn_ref='' WHERE id=?`, call)
	_ = w.Close()
	if err != nil {
		t.Fatal(err)
	}
	insertDiagnosticFact(t, f, actor, telemetry.Fact{Version: 1, ID: "overview-native-alias", Kind: "response", SessionID: session,
		ResponseID: "overview-native-alias", HarnessTurnRef: prompt, SourcePath: path, SourceOffset: offset + 1,
		LedgerRefOffset: &offset, OccurredAt: fixtureNow, MeasurementKind: "native"})
	var got MateResponse
	f.get(t, "/api/projects/"+fixtureProject+"/mate", http.StatusOK, &got)
	found := false
	for _, exchange := range got.Exchanges {
		var total diagnostics.Tokens
		for _, category := range exchange.Overview.Categories {
			if category.Tokens != nil {
				total.Add(*category.Tokens)
			}
			for _, id := range category.CallIDs {
				if id == call {
					found = true
					if exchange.ID != session+"#prompt#"+prompt {
						t.Fatalf("confirmed prompt alias lost: %s", exchange.ID)
					}
				}
			}
		}
		if total != performanceTokens(exchange.Tokens) {
			t.Fatalf("canonical membership changed overview accounting: %+v", exchange)
		}
	}
	if !found {
		t.Fatal("native-resolved call absent from Mate overview")
	}
}

func TestMateOverviewRefreshesWithoutANewEvent(t *testing.T) {
	f := newFixture(t)
	actor := timeline.MateActorID(fixtureProject)
	var session string
	if err := f.read.SQL().QueryRow(`SELECT id FROM session WHERE actor_id=? LIMIT 1`, actor).Scan(&session); err != nil {
		t.Fatal(err)
	}
	start := fixtureNow.Add(-time.Second)
	insertDiagnosticFact(t, f, actor, telemetry.Fact{Version: 1, ID: "clock-prompt", Kind: "prompt", SessionID: session,
		HarnessTurnRef: "clock-prompt", Text: "Inspect progress", OccurredAt: start})
	insertDiagnosticFact(t, f, actor, telemetry.Fact{Version: 1, ID: "clock-tool", ExecutionID: "clock-tool", Kind: "execution", SessionID: session,
		HarnessTurnRef: "clock-prompt", Tool: "exec_command", Command: "git diff -- src/clock.go", StartedAt: &start, Status: "running", OccurredAt: start})
	now := fixtureNow
	f.server.deps.Now = func() time.Time { return now }
	var first, second MateResponse
	path := "/api/projects/" + fixtureProject + "/mate"
	f.get(t, path, http.StatusOK, &first)
	now = now.Add(time.Minute)
	f.get(t, path, http.StatusOK, &second)
	if first.LastEventID != second.LastEventID || first.GeneratedAt == second.GeneratedAt {
		t.Fatal("prompt overview timing must refresh without a new event or ledger mutation")
	}
	if first.Mate.Tokens != second.Mate.Tokens {
		t.Fatal("clock advance changed recorded usage")
	}
	elapsed := func(body MateResponse) int64 {
		t.Helper()
		for _, exchange := range body.Exchanges {
			if exchange.ID == session+"#prompt#clock-prompt" {
				for _, category := range exchange.Overview.Categories {
					if category.Kind == "review" && category.ElapsedMs != nil {
						return *category.ElapsedMs
					}
				}
			}
		}
		t.Fatal("running review duration unavailable")
		return 0
	}
	if elapsed(second)-elapsed(first) != 60000 {
		t.Fatal("running prompt work time froze without new usage/events")
	}
}
