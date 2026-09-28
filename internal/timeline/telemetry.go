package timeline

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/telemetry"
)

type transcriptCache struct {
	Size         int64
	MTime        time.Time
	Finalized    bool
	Batch        harness.TranscriptBatch
	HeaderSHA256 string
	HeaderBytes  int
}

type transcriptFactMark struct{ turns, actions, events, usage int }

func (p *pass) legacyTranscriptOffset(ctx context.Context, loc Located) (int64, error) {
	var consumed int64
	err := p.tx.QueryRowContext(ctx, `SELECT byte_offset FROM cursor WHERE source_path=?`, loc.Path).Scan(&consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(loc.Path)
	if err != nil {
		return 0, err
	}
	if consumed > info.Size() {
		return 0, nil
	}
	var raw string
	var priorSize, priorMTime int64
	err = p.tx.QueryRowContext(ctx, `SELECT state_json,source_size,source_mtime FROM telemetry_cursor WHERE source_path=?`, loc.Path).Scan(&raw, &priorSize, &priorMTime)
	if errors.Is(err, sql.ErrNoRows) {
		return consumed, nil
	}
	if err != nil {
		return 0, err
	}
	if priorSize == info.Size() && priorMTime != 0 && priorMTime != info.ModTime().UnixNano() {
		return 0, nil
	}
	var state harness.TelemetryState
	if json.Unmarshal([]byte(raw), &state) == nil && state.HeaderBytes > 0 {
		header, err := readHead(loc.Path, int(state.HeaderBytes))
		if err != nil {
			return 0, err
		}
		if state.HeaderSHA256 != fmt.Sprintf("%x", sha256.Sum256(header)) {
			return 0, nil
		}
	}
	return consumed, nil
}

// Existing normalized parsers retain whole-file semantics, but the writer only
// needs new responses and changed calls. A late result selects its original
// action/turn even if the invocation precedes the last committed byte cursor.
func (p *pass) filterTranscriptFacts(loc Located, session string, tb harness.TranscriptBatch, since int64, mark transcriptFactMark) {
	if since == 0 {
		return
	}
	changedActions := map[string]bool{}
	results := resultsByCall(tb)
	for _, call := range tb.ToolCalls {
		res, ok := results[call.SourceRef]
		if call.Offset >= since || (ok && res.Offset >= since) {
			changedActions[actionRowID(session, call.SourceRef)] = true
		}
	}
	changedTurns := map[string]bool{}
	for _, turn := range p.b.turns[mark.turns:] {
		if turn.RefOffset >= since {
			changedTurns[turn.ID] = true
		}
	}
	for _, action := range p.b.actions[mark.actions:] {
		if changedActions[action.ID] && action.TurnID != "" {
			changedTurns[action.TurnID] = true
		}
	}
	turns := p.b.turns[:mark.turns]
	for _, t := range p.b.turns[mark.turns:] {
		if changedTurns[t.ID] {
			turns = append(turns, t)
		}
	}
	p.b.turns = turns
	actions := p.b.actions[:mark.actions]
	for _, a := range p.b.actions[mark.actions:] {
		if changedActions[a.ID] || changedTurns[a.TurnID] {
			actions = append(actions, a)
		}
	}
	p.b.actions = actions
	events := p.b.events[:mark.events]
	for _, e := range p.b.events[mark.events:] {
		if e.RefOffset >= since || changedTurns[e.TurnID] {
			events = append(events, e)
		}
	}
	p.b.events = events
	usage := p.b.usage[:mark.usage]
	for _, u := range p.b.usage[mark.usage:] {
		if u.RefOffset >= since {
			usage = append(usage, u)
		}
	}
	p.b.usage = usage
}

// Both cursor rows were committed with the preceding pass's facts. The check
// therefore survives restart and rollback, unlike an in-memory "already wrote"
// flag; Reindex clears these rows and forces a complete replay.
func (p *pass) transcriptPreviouslyRecorded(ctx context.Context, loc Located, tb harness.TranscriptBatch) (bool, error) {
	info, err := os.Stat(loc.Path)
	if err != nil {
		return false, err
	}
	var size, mtime, consumed int64
	err = p.tx.QueryRowContext(ctx, `SELECT t.source_size,t.source_mtime,c.byte_offset FROM telemetry_cursor t JOIN cursor c ON c.source_path=t.source_path WHERE t.source_path=?`, loc.Path).Scan(&size, &mtime, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return size == info.Size() && mtime == info.ModTime().UnixNano() && consumed == tb.ConsumedBytes, nil
}

// cachedTranscript avoids decoding unchanged multi-megabyte transcripts on
// every observer tick. The original ledger still sees its complete normalized
// batch; native telemetry independently tails from its persisted byte cursor.
func (i *Ingester) cachedTranscript(loc Located) (harness.TranscriptBatch, error) {
	info, err := os.Stat(loc.Path)
	if err != nil {
		return harness.TranscriptBatch{}, err
	}
	c, cached := i.transcripts[loc.Path]
	if cached && c.Size == info.Size() && c.MTime.Equal(info.ModTime()) && c.Finalized == loc.Finalized {
		return c.Batch, nil
	}
	if cached && loc.Kind == harness.KindCodex && c.Size < info.Size() && c.Batch.ConsumedBytes <= info.Size() && c.Batch.Malformed == nil && c.HeaderBytes > 0 {
		header, err := readHead(loc.Path, c.HeaderBytes)
		if err != nil {
			return harness.TranscriptBatch{}, err
		}
		if fmt.Sprintf("%x", sha256.Sum256(header)) == c.HeaderSHA256 {
			base := c.Batch.ConsumedBytes
			file, err := os.Open(loc.Path)
			if err != nil {
				return harness.TranscriptBatch{}, err
			}
			data := make([]byte, info.Size()-base)
			n, readErr := file.ReadAt(data, base)
			_ = file.Close()
			if readErr != nil && n == 0 {
				return harness.TranscriptBatch{}, readErr
			}
			tail := (harness.Codex{}).ParseTranscript(c.Batch.NextState, data[:n])
			b := appendCodexTranscript(c.Batch, tail, base)
			c.Size, c.MTime, c.Batch = info.Size(), info.ModTime(), b
			i.transcripts[loc.Path] = c
			return b, nil
		}
	}
	data, err := os.ReadFile(loc.Path)
	if err != nil {
		return harness.TranscriptBatch{}, err
	}
	parser, err := transcriptParser(loc.Kind)
	if err != nil {
		return harness.TranscriptBatch{}, err
	}
	b := parser.ParseTranscript(harness.TranscriptParseState{}, data)
	if loc.Kind == harness.KindClaude && loc.Finalized {
		b = parser.ParseTranscriptFinal(harness.TranscriptParseState{}, data)
	}
	if i.transcripts == nil {
		i.transcripts = map[string]transcriptCache{}
	}
	headerBytes := len(data)
	if headerBytes > 4096 {
		headerBytes = 4096
	}
	i.transcripts[loc.Path] = transcriptCache{Size: info.Size(), MTime: info.ModTime(), Finalized: loc.Finalized, Batch: b, HeaderBytes: headerBytes, HeaderSHA256: fmt.Sprintf("%x", sha256.Sum256(data[:headerBytes]))}
	return b, nil
}

// Codex source refs and ordinals are native, absolute record identities. Only
// byte offsets need rebasing when appending a parsed tail to the cached batch.
// The cursor parser already guarantees every emitted fact precedes ConsumedBytes.
func appendCodexTranscript(prior, tail harness.TranscriptBatch, base int64) harness.TranscriptBatch {
	for n := range tail.Records {
		tail.Records[n].Offset += base
	}
	for n := range tail.Turns {
		tail.Turns[n].Offset += base
	}
	for n := range tail.ToolCalls {
		tail.ToolCalls[n].Offset += base
	}
	for n := range tail.ToolResults {
		tail.ToolResults[n].Offset += base
	}
	for n := range tail.CodexUsageSnapshots {
		tail.CodexUsageSnapshots[n].Offset += base
	}
	for n := range tail.UsageFailures {
		tail.UsageFailures[n].Offset += base
	}
	prior.Records = append(prior.Records, tail.Records...)
	prior.Turns = append(prior.Turns, tail.Turns...)
	prior.ToolCalls = append(prior.ToolCalls, tail.ToolCalls...)
	prior.ToolResults = append(prior.ToolResults, tail.ToolResults...)
	prior.CodexUsageSnapshots = append(prior.CodexUsageSnapshots, tail.CodexUsageSnapshots...)
	prior.UsageFailures = append(prior.UsageFailures, tail.UsageFailures...)
	prior.ConsumedBytes, prior.TotalBytes, prior.PendingBytes = base+tail.ConsumedBytes, base+tail.TotalBytes, tail.PendingBytes
	prior.NextState, prior.Malformed = tail.NextState, tail.Malformed
	if prior.Malformed != nil {
		prior.Malformed.Offset += base
		prior.Malformed.EndOffset += base
	}
	for kind, count := range tail.Skipped {
		prior.Skipped[kind] += count
	}
	return prior
}

func (p *pass) ingestTelemetry(ctx context.Context, loc Located, session string, tb harness.TranscriptBatch) error {
	info, err := os.Stat(loc.Path)
	if err != nil {
		return err
	}
	var offset, previousSize, mtime int64
	var raw string
	err = p.tx.QueryRowContext(ctx, `SELECT byte_offset,state_json,source_size,source_mtime FROM telemetry_cursor WHERE source_path=?`, loc.Path).Scan(&offset, &raw, &previousSize, &mtime)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var state harness.TelemetryState
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &state); err != nil {
			return fmt.Errorf("telemetry cursor %s: %w", loc.Path, err)
		}
	}
	var facts []telemetry.Fact
	parseError := ""
	headerBytes := state.HeaderBytes
	if headerBytes == 0 {
		headerBytes = info.Size()
		if headerBytes > 4096 {
			headerBytes = 4096
		}
	}
	header, headerErr := readHead(loc.Path, int(headerBytes))
	if headerErr != nil && info.Size() > 0 {
		return headerErr
	}
	headerHash := fmt.Sprintf("%x", sha256.Sum256(header))
	headerBytes = int64(len(header))
	changedPrefix := state.HeaderSHA256 != "" && state.HeaderSHA256 != headerHash
	if changedPrefix || offset > info.Size() || (previousSize == info.Size() && mtime != 0 && mtime != info.ModTime().UnixNano()) {
		// A replaced/truncated source is a visible gap. Reset only this cursor;
		// historical facts retain their source references for investigation.
		facts = append(facts, telemetry.Fact{Version: telemetry.Version, ID: fmt.Sprint(info.ModTime().UnixNano()), Kind: "gap", SourceRef: "source_changed", OccurredAt: p.now, MeasurementKind: "observed", Gaps: []string{"transcript_replaced_or_truncated"}})
		offset = 0
		state = harness.TelemetryState{}
	}
	state.HeaderSHA256, state.HeaderBytes = headerHash, headerBytes
	if loc.Kind == harness.KindCodex && offset < info.Size() {
		f, err := os.Open(loc.Path)
		if err != nil {
			return err
		}
		data := make([]byte, info.Size()-offset)
		n, readErr := f.ReadAt(data, offset)
		_ = f.Close()
		if readErr != nil && n == 0 {
			return readErr
		}
		b := harness.ParseCodexTelemetry(state, data[:n], offset)
		state = b.State
		offset += b.Consumed
		facts = append(facts, b.Facts...)
		parseError = b.Error
	} else if loc.Kind == harness.KindClaude && (offset != tb.ConsumedBytes || previousSize != info.Size() || raw == "") {
		facts = append(facts, harness.NormalizedTelemetry(tb)...)
		offset = tb.ConsumedBytes
	}
	if tb.Malformed != nil {
		if parseError != "" {
			parseError += "; "
		}
		parseError += fmt.Sprintf("ledger parser %s at byte %d", tb.Malformed.Reason, tb.Malformed.Offset)
	}
	for _, failure := range tb.UsageFailures {
		facts = append(facts, telemetry.Fact{Version: telemetry.Version, ID: fmt.Sprintf("usage:%d", failure.Offset), Kind: "gap", SourceRef: "normalized_usage_failure", SourceOffset: failure.Offset, MeasurementKind: "normalized", Gaps: []string{failure.Reason}})
	}
	if len(tb.UsageFailures) > 0 {
		if parseError != "" {
			parseError += "; "
		}
		last := tb.UsageFailures[len(tb.UsageFailures)-1]
		parseError += fmt.Sprintf("ledger usage %s at byte %d", last.Reason, last.Offset)
	}
	for _, f := range facts {
		p.telemetryFact(loc, session, f)
	}
	capability := telemetry.Fact{Version: telemetry.Version, ID: "capability", Kind: "capability", SourceRef: "adapter-v1", MeasurementKind: "observed", OccurredAt: time.Time{}, Gaps: []string{"child_usage_unavailable"}}
	if len(tb.Records) > 0 {
		capability.OccurredAt = tb.Records[0].OccurredAt
	}
	if loc.Kind == harness.KindClaude {
		capability.Gaps = append(capability.Gaps, "native_execution_unavailable", "native_process_unavailable", "native_timing_unavailable")
	} else {
		capability.Gaps = append(capability.Gaps, "wrapper_parent_unavailable")
	}
	p.telemetryFact(loc, session, capability)
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = p.tx.ExecContext(ctx, `INSERT INTO telemetry_cursor(source_path,actor_id,session_id,byte_offset,state_json,source_size,source_mtime,observed_at,error) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(source_path) DO UPDATE SET actor_id=excluded.actor_id,session_id=excluded.session_id,byte_offset=excluded.byte_offset,state_json=excluded.state_json,source_size=excluded.source_size,source_mtime=excluded.source_mtime,observed_at=excluded.observed_at,error=excluded.error`, loc.Path, loc.ActorID, session, offset, string(encoded), info.Size(), info.ModTime().UnixNano(), db.FormatTime(p.now), parseError)
	return err
}

func (p *pass) telemetryFact(loc Located, session string, f telemetry.Fact) {
	f.SessionID, f.SourcePath, f.ObservedAt = session, loc.Path, p.now
	data, _ := json.Marshal(f)
	var payload map[string]any
	_ = json.Unmarshal(data, &payload)
	key := fmt.Sprint(f.SourceOffset)
	if f.LedgerRefOffset != nil {
		key += fmt.Sprintf(":ledger:%d", *f.LedgerRefOffset)
	}
	p.b.event(pendingEvent{Dedup: dedup("telemetry."+f.Kind, session, f.ID, key), Project: p.project, At: f.OccurredAt, ActorID: loc.ActorID, Kind: "telemetry." + f.Kind, TaskActor: loc.ActorID, Payload: payload, RefPath: loc.Path, RefOffset: f.SourceOffset})
}

func (p *pass) ingestProfiles(ctx context.Context) error {
	for _, crew := range p.crews {
		profiles, err := p.ing.ws.ReadCrewHarnessProfiles(p.project, crew.ID)
		if err != nil {
			return err
		}
		for _, profile := range profiles {
			data, _ := json.Marshal(profile)
			var payload map[string]any
			_ = json.Unmarshal(data, &payload)
			p.b.event(pendingEvent{Dedup: dedup("telemetry.profile", crew.ActorID, profile.CapturedAt, profile.Fingerprint), Project: p.project, At: db.ParseTime(profile.CapturedAt), ActorID: crew.ActorID, Kind: "telemetry.profile", TaskActor: crew.ActorID, Payload: payload, RefPath: profile.SnapshotPath})
		}
	}
	return nil
}
