package dashboard

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/diagnostics"
	"github.com/nguyenngocanh94/mate/internal/telemetry"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

func TestCrewPerformanceTotalsRepeatLedgerAndPreserveEvidence(t *testing.T) {
	f := newFixture(t)
	var got TaskResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew, http.StatusOK, &got)
	p := got.Performance
	if p.Version == "" || p.ModelCalls != int(got.Ledger.Turns) || p.Tokens != performanceTokens(got.Ledger.Tokens) {
		t.Fatalf("performance %+v differs from ledger %+v", p, got.Ledger)
	}
	var segments, prompts diagnostics.Tokens
	seen := map[string]bool{}
	for _, s := range p.Segments {
		segments.Add(s.Tokens)
		for _, call := range s.CallIDs {
			if seen[call] {
				t.Fatalf("call %s charged twice", call)
			}
			seen[call] = true
		}
	}
	for _, prompt := range p.PromptTurns {
		prompts.Add(prompt.Tokens)
	}
	if segments != p.Tokens || prompts != p.Tokens || len(seen) != len(got.Turns) {
		t.Fatalf("segment/prompt membership lost token conservation: %+v %+v %+v", segments, prompts, p.Tokens)
	}
	for _, e := range p.Executions {
		if e.Tool == "thinking" {
			t.Fatal("synthetic thinking action leaked into measured executions")
		}
	}
	if len(p.Freshness.Missing) == 0 {
		t.Fatal("historical fixture must disclose unavailable native coverage")
	}
	if len(p.TopFindingIDs) > 3 {
		t.Fatal("first screen has more than three findings")
	}
}

func TestCrewPerformanceShowsNativeFailureDespiteSuccessfulWrapper(t *testing.T) {
	f := newFixture(t)
	actor := timeline.CrewActorID(fixtureProject, fixtureCrew)
	var session string
	if err := f.read.SQL().QueryRow(`SELECT id FROM session WHERE actor_id=? LIMIT 1`, actor).Scan(&session); err != nil {
		t.Fatal(err)
	}
	start, end := fixtureNow.Add(-20*time.Second), fixtureNow.Add(-10*time.Second)
	exit := 42
	native := telemetry.Fact{Version: 1, ID: "native-test-failed", ExecutionID: "native-test-failed", Kind: "execution", SessionID: session,
		Command: "go test ./trip -run TestTrip", Tool: "exec_command", StartedAt: &start, CompletedAt: &end, Status: "failed", ExitCode: &exit,
		OccurredAt: end, ObservedAt: fixtureNow, MeasurementKind: "native", SourcePath: "test-rollout.jsonl", SourceOffset: 12,
		Output: &telemetry.Output{Bytes: 14, SHA256: "failed-output", Preview: "FAIL TestTrip"}}
	insertDiagnosticFact(t, f, actor, native)
	var got TaskResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew, http.StatusOK, &got)
	found := false
	for _, e := range got.Performance.Executions {
		if e.Command != native.Command {
			continue
		}
		found = true
		if e.ExitCode == nil || *e.ExitCode != 42 || e.Status != "failed" || e.SourceRef.Offset != 12 {
			t.Fatalf("native execution %+v", e)
		}
	}
	if !found {
		t.Fatal("native command missing")
	}
	found = false
	for _, finding := range got.Performance.Findings {
		if finding.Kind == "execution_failure" {
			found = true
			if len(finding.Evidence) == 0 || !strings.Contains(finding.Evidence[0].Label, "exit 42") {
				t.Fatalf("finding lacks native exit evidence %+v", finding)
			}
		}
	}
	if !found {
		t.Fatal("successful wrapper hid child command failure")
	}
	if got.Performance.Tokens != performanceTokens(got.Ledger.Tokens) {
		t.Fatal("native facts changed ledger total")
	}
}

func TestCrewPerformanceFreshnessAdvancesWithoutAnEvent(t *testing.T) {
	f := newFixture(t)
	now := fixtureNow
	f.server.deps.Now = func() time.Time { return now }
	path := "/api/projects/" + fixtureProject + "/tasks/" + fixtureCrew
	var first, second TaskResponse
	f.get(t, path, http.StatusOK, &first)
	now = now.Add(time.Minute)
	f.get(t, path, http.StatusOK, &second)
	if first.LastEventID != second.LastEventID {
		t.Fatal("read mutated event ledger")
	}
	if first.GeneratedAt == second.GeneratedAt {
		t.Fatal("live task freshness trapped in event-generation cache")
	}
	if first.Performance.Freshness.AgeMs != nil && second.Performance.Freshness.AgeMs != nil {
		if *second.Performance.Freshness.AgeMs-*first.Performance.Freshness.AgeMs != 60000 {
			t.Fatal("recording age did not advance")
		}
	}
	if first.Performance.Tokens != second.Performance.Tokens {
		t.Fatal("clock extrapolated usage")
	}
}

func insertDiagnosticFact(t *testing.T, f *fixture, actor string, fact telemetry.Fact) {
	t.Helper()
	writer, err := db.Open(f.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	payload, err := json.Marshal(fact)
	if err != nil {
		t.Fatal(err)
	}
	_, err = writer.SQL().Exec(`INSERT INTO event(dedup,project,at,actor_id,kind,payload,ref_path,ref_offset) VALUES(?,?,?,?,?,?,?,?)`,
		"diagnostic-test#"+fact.ID, fixtureProject, db.FormatTime(fact.OccurredAt), actor, "telemetry."+fact.Kind, string(payload), fact.SourcePath, fact.SourceOffset)
	if err != nil {
		t.Fatal(err)
	}
}
