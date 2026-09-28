package timeline_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

func TestTelemetryReplaysWithoutDuplicatingLedgerAndHeartbeatAdvances(t *testing.T) {
	f := newFixture(t)
	now := fixtureNow
	f.ing = timeline.New(f.ws, f.db, timeline.Deps{Now: func() time.Time { return now }})
	f.ingest(t)
	ledger := f.count(t, `SELECT SUM(input_tokens+cache_read_tokens+cache_write_tokens+output_tokens) FROM turn WHERE actor_id=?`, f.crewActor())
	facts := f.count(t, `SELECT COUNT(*) FROM event WHERE kind LIKE 'telemetry.%'`)
	if facts < 20 {
		t.Fatalf("native facts missing: %d", facts)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE actor_id=? AND kind='telemetry.response' AND json_extract(payload,'$.ledger_ref_offset') IS NOT NULL`, f.crewActor()); n != 11 {
		t.Fatalf("exact ledger aliases=%d want11", n)
	}
	now = now.Add(time.Minute)
	f.ingest(t)
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind LIKE 'telemetry.%'`); n != facts {
		t.Fatalf("unchanged facts duplicated: %d ->%d", facts, n)
	}
	var observed string
	if err := f.db.SQL().QueryRow(`SELECT observed_at FROM telemetry_cursor WHERE actor_id=?`, f.crewActor()).Scan(&observed); err != nil {
		t.Fatal(err)
	}
	if observed != db.FormatTime(now) {
		t.Fatalf("observer heartbeat stale: %s", observed)
	}
	// A process restart reconstructs parser/cache state from the committed rows.
	f.ing = timeline.New(f.ws, f.db, timeline.Deps{Now: func() time.Time { return now }})
	f.ingest(t)
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind LIKE 'telemetry.%'`); n != facts {
		t.Fatalf("restart duplicated facts: %d ->%d", facts, n)
	}
	if err := f.ing.Reindex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind LIKE 'telemetry.%'`); n != facts {
		t.Fatalf("reindex changed facts: %d ->%d", facts, n)
	}
	if n := f.count(t, `SELECT SUM(input_tokens+cache_read_tokens+cache_write_tokens+output_tokens) FROM turn WHERE actor_id=?`, f.crewActor()); n != ledger {
		t.Fatalf("native usage was counted again: %d ->%d", ledger, n)
	}
}

func TestTelemetryLateResultAfterRestartAndSourceReplacementGap(t *testing.T) {
	f := newFixture(t)
	data, err := os.ReadFile(codexFixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, "crew-rollout.jsonl")
	call := telemetryRecord("response_item", map[string]any{"type": "custom_tool_call", "call_id": "late-wrapper", "name": "exec", "input": "const r=await tools.write_stdin({session_id:81234});text(r)"})
	if err := os.WriteFile(path, append(data, call...), 0o600); err != nil {
		t.Fatal(err)
	}
	meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
	if err != nil {
		t.Fatal(err)
	}
	meta["transcript"] = path
	if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
		t.Fatal(err)
	}
	f.ingest(t)
	result := telemetryRecord("response_item", map[string]any{"type": "custom_tool_call_output", "call_id": "late-wrapper", "output": []map[string]string{{"type": "input_text", "text": `{"chunk_id":"last","exit_code":0,"output":""}`}}})
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.Write(result)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	f.ing = timeline.New(f.ws, f.db, timeline.Deps{Now: func() time.Time { return fixtureNow }})
	f.ingest(t)
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE actor_id=? AND kind='telemetry.tool_result' AND json_extract(payload,'$.process_id')='81234' AND json_extract(payload,'$.output.new_bytes')=0`, f.crewActor()); n != 1 {
		t.Fatalf("late result lost opener/progress: %d", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM action WHERE actor_id=? AND id LIKE '%late-wrapper' AND ended_at IS NOT NULL AND ok=1`, f.crewActor()); n != 1 {
		t.Fatalf("late result did not complete original ledger action: %d", n)
	}
	// Replacement can be larger than the original: the prefix fingerprint
	// catches it rather than blindly interpreting only its tail as an append.
	changed := append([]byte(nil), data...)
	for n := range changed {
		if changed[n] == 'b' {
			changed[n] = 'c'
			break
		}
	}
	changed = append(changed, call...)
	changed = append(changed, result...)
	changed = append(changed, '\n')
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	f.ingest(t)
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE actor_id=? AND kind='telemetry.gap' AND json_extract(payload,'$.gaps[0]')='transcript_replaced_or_truncated'`, f.crewActor()); n != 1 {
		t.Fatalf("larger replacement hidden: %d", n)
	}
}

func telemetryRecord(kind string, payload any) []byte {
	b, _ := json.Marshal(map[string]any{"timestamp": "2026-09-19T10:46:50Z", "ordinal": 100000, "type": kind, "payload": payload})
	return append(b, '\n')
}

func TestNativeTelemetryCannotHideLedgerParserFailure(t *testing.T) {
	f := newFixture(t)
	data, err := os.ReadFile(codexFixture)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(map[string]any{"timestamp": "2026-09-19T10:46:50Z", "type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "new-prompt"}})
	path := filepath.Join(f.root, "missing-ordinal.jsonl")
	data = append(data, line...)
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
	if err != nil {
		t.Fatal(err)
	}
	meta["transcript"] = path
	if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
		t.Fatal(err)
	}
	f.ingest(t)
	var problem string
	if err := f.db.SQL().QueryRow(`SELECT error FROM telemetry_cursor WHERE source_path=?`, path).Scan(&problem); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(problem, "ledger parser missing_record_id") {
		t.Fatalf("native adapter hid stalled ledger parser: %q", problem)
	}
}
