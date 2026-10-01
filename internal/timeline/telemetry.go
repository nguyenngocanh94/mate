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
//
// A file that only grew - larger, with the leading bytes the last read saw -
// is handed to the harness with the batch before it, and the harness decides
// whether to carry on from there or read it whole. A frozen snapshot is read
// at rest.
func (i *Ingester) cachedTranscript(src harness.TranscriptSource, loc Located) (harness.TranscriptBatch, error) {
	info, err := os.Stat(loc.Path)
	if err != nil {
		return harness.TranscriptBatch{}, err
	}
	c, cached := i.transcripts[loc.Path]
	if cached && c.Size == info.Size() && c.MTime.Equal(info.ModTime()) && c.Finalized == loc.Finalized {
		return c.Batch, nil
	}
	grown := false
	if cached && c.Size < info.Size() && c.HeaderBytes > 0 {
		header, err := readHead(loc.Path, c.HeaderBytes)
		if err != nil {
			return harness.TranscriptBatch{}, err
		}
		grown = fmt.Sprintf("%x", sha256.Sum256(header)) == c.HeaderSHA256
	}
	b, err := src.Read(harness.TranscriptReadRequest{Path: loc.Path, Prior: c.Batch, Grown: grown, AtRest: loc.Finalized})
	if err != nil {
		return harness.TranscriptBatch{}, err
	}
	header, err := readHead(loc.Path, 4096)
	if err != nil && info.Size() > 0 {
		return harness.TranscriptBatch{}, err
	}
	c.HeaderBytes, c.HeaderSHA256 = len(header), fmt.Sprintf("%x", sha256.Sum256(header))
	if i.transcripts == nil {
		i.transcripts = map[string]transcriptCache{}
	}
	c.Size, c.MTime, c.Finalized, c.Batch = info.Size(), info.ModTime(), loc.Finalized, b
	i.transcripts[loc.Path] = c
	return b, nil
}

func (p *pass) ingestTelemetry(ctx context.Context, src harness.TranscriptSource, loc Located, session string, tb harness.TranscriptBatch) error {
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
	up, err := src.Telemetry(harness.TelemetryRequest{
		Path: loc.Path, Size: info.Size(), Offset: offset, State: state,
		Changed: previousSize != info.Size() || raw == "", Batch: tb,
	})
	if err != nil {
		return err
	}
	state, offset, parseError = up.State, up.Offset, up.Error
	facts = append(facts, up.Facts...)
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
	capability := telemetry.Fact{Version: telemetry.Version, ID: "capability", Kind: "capability", SourceRef: "adapter-v1", MeasurementKind: "observed", OccurredAt: time.Time{}, Gaps: up.Gaps}
	if len(tb.Records) > 0 {
		capability.OccurredAt = tb.Records[0].OccurredAt
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
