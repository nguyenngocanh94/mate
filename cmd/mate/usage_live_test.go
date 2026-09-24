package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// TestLiveUsageMatchesTheHarness is docs/mvp.md M5 task 27's live proof: the
// ledger's TOTAL for a real Codex crew equals the last
// `total_token_usage.total_tokens` its own rollout reports, and a real
// Claude Mate's TOTAL equals the sum of its transcript's own per-message
// usage (input+cache_read+cache_creation+output, one message counted once,
// the way internal/harness's parser already groups a streamed response).
//
// The two harnesses are checked independently rather than through one
// Mate-supervises-Crew flow: nothing here needs the Mate to know about the
// Crew, and a standalone Mate turn plus a standalone Crew turn is the
// smallest real exercise of both locator paths docs/timeline.md names -
// `meta.transcript` for the Mate (the Stop hook's own path) and
// `herdr.agent_session` for the Crew (Herdr's rollout uuid).
func TestLiveUsageMatchesTheHarness(t *testing.T) {
	requireConsoleLive(t)
	session, configHome := consoleLiveLab(t)

	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	consoleUseLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	repo := filepath.Join(w.Root(), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repo: repo, DefaultBranch: "main"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	names := runtime.NewMemoryNameRegistry()
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = names
	rt.StartServer = func(context.Context, string) error {
		t.Fatal("the lab session is provisioned by the runner; this test must not start a Herdr server")
		return nil
	}
	deps := spawn.Deps{
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               consoleBinaryPath(t),
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 60 * time.Second,
		StartTimeout:         60 * time.Second,
	}
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	action := consoleAction(w, deps)

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k3", true)
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})

	// --- The Mate: two turns, so its Stop hook records a real transcript
	// and the first turn is no longer the trailing message group.
	// harness.ParseTranscript deliberately withholds the trailing group of
	// a live session (it may still grow, internal/harness/transcript.go),
	// so a Mate asked only one question would show zero turns in the
	// ledger forever, not because nothing happened but because the ingest
	// cannot yet prove that group is finished.
	startOut, err := action(ctx, console.ActionRequest{
		Action: console.ActionStart, Target: "shop", TargetKind: "mate", Harness: query.HarnessClaude})
	if err != nil {
		t.Fatalf("start the Mate: %v", err)
	}
	t.Logf("start action: %s", startOut)
	mateHandle, mateKind := mateHandleOrFatal(t, ctx, w, deps, "shop")
	matePane := paneReader(rt, mateHandle)
	t.Cleanup(func() { t.Logf("final Mate pane:\n%s", matePane()) })

	typeCaptainLine(t, ctx, deps, action, "shop", mateHandle, mateKind,
		"Reply with exactly the single word ok and do nothing else.", 3*time.Minute, matePane)
	waitForMateTranscript(t, w, 3*time.Minute)
	typeCaptainLine(t, ctx, deps, action, "shop", mateHandle, mateKind,
		"Reply with exactly the single word ok again and do nothing else.", 3*time.Minute, matePane)
	mateTranscript := waitForMateTranscript(t, w, 3*time.Minute)
	t.Logf("mate transcript: %s", mateTranscript)

	// --- The Crew: one turn that reports and stops, so its rollout is short
	// and unambiguous to re-read. ---
	crewRes, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k3", Harness: harness.KindCodex,
		BriefText: brieftest.Ship(`Append the line wait-mate: done to the status file and do nothing else.`),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	t.Logf("spawned crew %s in pane %s", crewRes.Agent, crewRes.Pane)
	crewTail := crewPaneTail(ctx, rt, session, configHome, crewRes.Agent, "k3")
	waitForCrewStatus(t, ctx, w, "shop", "k3", "wait-mate:", 4*time.Minute, crewTail)

	// --- Reindex, then read the ledger. ---
	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer handle.Close()
	if err := timeline.New(w, handle, timeline.Deps{}).Reindex(ctx); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	crewSession := actorSessionPath(t, handle, timeline.CrewActorID("shop", "k3"))
	if crewSession == "" {
		t.Fatalf("no session/transcript recorded for the crew; ingest.unresolved? %s", dumpUnresolved(t, handle))
	}
	t.Logf("crew rollout: %s", crewSession)
	wantCrewTotal := lastCodexTotalTokens(t, crewSession)
	gotCrewTotal := actorTotalTokens(t, handle, timeline.CrewActorID("shop", "k3"))
	if gotCrewTotal != wantCrewTotal {
		t.Fatalf("crew ledger total = %d, want the rollout's own total_tokens %d", gotCrewTotal, wantCrewTotal)
	}
	t.Logf("crew total tokens: ledger %d == rollout %d", gotCrewTotal, wantCrewTotal)

	wantMateTotal := sumClaudeMessageUsage(t, mateTranscript)
	gotMateTotal := actorTotalTokens(t, handle, timeline.MateActorID("shop"))
	if gotMateTotal != wantMateTotal {
		t.Fatalf("mate ledger total = %d, want the transcript's own per-message sum %d", gotMateTotal, wantMateTotal)
	}
	t.Logf("mate total tokens: ledger %d == transcript %d", gotMateTotal, wantMateTotal)
}

// waitForMateTranscript polls `mate.meta` for the Stop hook's own
// `transcript=` (docs/mvp.md decision 9), which appears only once the Mate
// has finished a real turn.
func waitForMateTranscript(t *testing.T, w *store.Workspace, within time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		meta, err := w.ReadMateMeta("shop")
		if err == nil {
			if tp := strings.TrimSpace(meta[spawn.MetaTranscript]); tp != "" {
				return tp
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("mate.meta never recorded a transcript within %s", within)
		}
		time.Sleep(2 * time.Second)
	}
}

// actorSessionPath and actorTotalTokens read the two facts
// TestLiveUsageMatchesTheHarness checks against raw files: which transcript
// the ingest resolved for an actor, and what it summed out of it.
func actorSessionPath(t *testing.T, handle *db.DB, actorID string) string {
	t.Helper()
	var path sql.NullString
	err := handle.SQL().QueryRow(
		`SELECT transcript_path FROM session WHERE actor_id = ? ORDER BY started_at DESC LIMIT 1`,
		actorID).Scan(&path)
	if err != nil && err != sql.ErrNoRows {
		t.Fatalf("read session for %s: %v", actorID, err)
	}
	return path.String
}

func actorTotalTokens(t *testing.T, handle *db.DB, actorID string) int64 {
	t.Helper()
	var total int64
	err := handle.SQL().QueryRow(`
		SELECT COALESCE(SUM(input_tokens+cache_read_tokens+cache_write_tokens+output_tokens), 0)
		  FROM turn WHERE actor_id = ?`, actorID).Scan(&total)
	if err != nil {
		t.Fatalf("sum turn tokens for %s: %v", actorID, err)
	}
	return total
}

func dumpUnresolved(t *testing.T, handle *db.DB) string {
	t.Helper()
	rows, err := handle.SQL().Query(`SELECT actor_id, payload FROM event WHERE kind = 'ingest.unresolved'`)
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var actorID, payload string
		if err := rows.Scan(&actorID, &payload); err == nil {
			out = append(out, actorID+" "+payload)
		}
	}
	return strings.Join(out, "; ")
}

// lastCodexTotalTokens re-reads a Codex rollout directly (not through
// internal/harness) and returns the last `total_token_usage.total_tokens`
// it reports - the ground truth `mate usage`'s crew TOTAL is checked
// against.
func lastCodexTotalTokens(t *testing.T, path string) int64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open rollout %s: %v", path, err)
	}
	defer f.Close()

	var last int64
	found := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var rec struct {
			Type    string `json:"type"`
			Payload struct {
				Type string `json:"type"`
				Info struct {
					Total struct {
						TotalTokens int64 `json:"total_tokens"`
					} `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		line := scanner.Text()
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Type == "event_msg" && rec.Payload.Type == "token_count" {
			last = rec.Payload.Info.Total.TotalTokens
			found = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan rollout %s: %v", path, err)
	}
	if !found {
		t.Fatalf("rollout %s carries no token_count record", path)
	}
	return last
}

// sumClaudeMessageUsage re-reads a Claude transcript directly and sums
// input_tokens+cache_read_input_tokens+cache_creation_input_tokens+
// output_tokens once per assistant message id - the ground truth the Mate's
// ledger TOTAL is checked against. A message id is counted once: Claude
// restates usage as a response streams, and the last record for a given id
// carries the complete figure, which is what internal/harness's own parser
// keys turns by (docs/timeline.md's turn.started, "one assistant message
// group (one message.id)").
//
// The trailing message id is dropped, matching harness.ParseTranscript:
// the group may still grow while the Mate's session is alive, so the
// ingest withholds it and it is never a turn (internal/harness/
// transcript.go). This test sends the Mate two prompts for exactly that
// reason - so the first message group has a successor and is safe to
// count, while the second (still the file's last) is excluded here the
// same way the ingest excludes it.
func sumClaudeMessageUsage(t *testing.T, path string) int64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open transcript %s: %v", path, err)
	}
	defer f.Close()

	perMessage := map[string]int64{}
	order := []string{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				ID    string `json:"id"`
				Usage struct {
					Input       int64 `json:"input_tokens"`
					CacheRead   int64 `json:"cache_read_input_tokens"`
					CacheCreate int64 `json:"cache_creation_input_tokens"`
					Output      int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &rec) != nil {
			continue
		}
		if rec.Type != "assistant" || rec.Message.ID == "" {
			continue
		}
		total := rec.Message.Usage.Input + rec.Message.Usage.CacheRead +
			rec.Message.Usage.CacheCreate + rec.Message.Usage.Output
		if total == 0 {
			continue
		}
		if _, seen := perMessage[rec.Message.ID]; !seen {
			order = append(order, rec.Message.ID)
		}
		// Last one wins: a streamed response restates usage as it grows,
		// and the final record for an id carries the complete figure.
		perMessage[rec.Message.ID] = total
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan transcript %s: %v", path, err)
	}
	if len(order) > 0 {
		order = order[:len(order)-1] // drop the trailing, still-open group
	}
	var sum int64
	for _, id := range order {
		sum += perMessage[id]
	}
	return sum
}
