package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/telemetry"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// seedSmallerCrews adds two more tasks to usageWorkspace's ledger, both
// smaller than k3's 97.2k: k1 at 10.5k and k2 at 50.5k.
func seedSmallerCrews(t *testing.T, w *store.Workspace) {
	t.Helper()
	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer handle.Close()
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "k1"), "shop", "crew", "unpriced-model", 10_000, 0, 0, 500, 200_000)
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "k2"), "shop", "crew", "unpriced-model", 50_000, 0, 0, 500, 200_000)
}

func TestUsageTopKeepsTheMateAndTheLargestTasksAndTotalsEverything(t *testing.T) {
	w := usageWorkspace(t)
	seedSmallerCrews(t, w)
	out := runCLI(t, "usage", "shop", "--top", "2", "--workspace", w.Root())
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("want header, mate, two tasks, total and the cut notice; got %d line(s):\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[1], "mate") || !strings.HasPrefix(lines[2], "k3") || !strings.HasPrefix(lines[3], "k2") {
		t.Fatalf("want the mate, then k3 and k2 largest first:\n%s", out)
	}
	if strings.Contains(out, "k1") {
		t.Fatalf("the smallest task must be cut:\n%s", out)
	}
	// 97.2k + 50.5k + 10.5k + the Mate's 500: the cut task still counts.
	if !strings.HasPrefix(lines[4], "total:") || !strings.Contains(lines[4], "158.7k total") {
		t.Fatalf("the total line must count every task, shown or not:\n%s", lines[4])
	}
	if !strings.Contains(lines[5], "2 largest of 3 task(s)") {
		t.Fatalf("a cut table must say what it left out:\n%s", lines[5])
	}
}

func TestUsageTopLargerThanTheLedgerChangesNothing(t *testing.T) {
	w := usageWorkspace(t)
	seedSmallerCrews(t, w)
	plain := runCLI(t, "usage", "shop", "--workspace", w.Root())
	if top := runCLI(t, "usage", "shop", "--top", "10", "--workspace", w.Root()); top != plain {
		t.Fatalf("--top above the task count must print the plain ledger:\n%s\n--- plain ---\n%s", top, plain)
	}
}

func TestUsageWhyExplainsOneTask(t *testing.T) {
	w := usageWorkspace(t)
	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	actor := timeline.CrewActorID("shop", "k3")
	end := time.Now().UTC()
	start := end.Add(-10 * time.Second)
	exit := 1
	fact := telemetry.Fact{Version: 1, ID: "failed-test", ExecutionID: "failed-test", Kind: "execution", SessionID: actor + "#s",
		Command: "go test ./cart", Tool: "exec_command", StartedAt: &start, CompletedAt: &end, Status: "failed", ExitCode: &exit,
		OccurredAt: end, ObservedAt: end, MeasurementKind: "native", SourcePath: "rollout.jsonl", SourceOffset: 7,
		Output: &telemetry.Output{Bytes: 9, SHA256: "failed-output", Preview: "FAIL cart"}}
	payload, err := json.Marshal(fact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.SQL().Exec(`INSERT INTO event(dedup,project,at,actor_id,kind,payload,ref_path,ref_offset) VALUES(?,?,?,?,?,?,?,?)`,
		"why-test#failed-test", "shop", db.FormatTime(end), actor, "telemetry.execution", string(payload), fact.SourcePath, fact.SourceOffset); err != nil {
		handle.Close()
		t.Fatalf("seed telemetry: %v", err)
	}
	handle.Close()

	out := runCLI(t, "usage", "shop", "k3", "--why", "--workspace", w.Root())
	for _, want := range []string{
		"usage why shop k3",
		// The ledger's own numbers, bucket by bucket; an unpriced model is "?".
		"tokens: 97.2k total = 90k in + 6k cache-read + 0 cache-write + 1.2k out · cost ?",
		"1 model call(s)",
		"context peak 96k, last 96k",
		// The failed command is named with its exit code and the review hint
		// that says where a fix would live.
		"execution_failure",
		"go test ./cart · exit 1",
		"review: ",
		// Findings overlap, and the digest has to say so where they are read.
		"never add them up",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--why output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "TURN ") {
		t.Errorf("--why must replace the per-call table, not add to it:\n%s", out)
	}
}

func TestUsageWhyAndTopRefuseTheWrongShape(t *testing.T) {
	w := usageWorkspace(t)
	for name, args := range map[string][]string{
		"why without a crew": {"usage", "shop", "--why", "--workspace", w.Root()},
		"top with a crew":    {"usage", "shop", "k3", "--top", "2", "--workspace", w.Root()},
		"negative top":       {"usage", "shop", "--top", "-1", "--workspace", w.Root()},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			err := run(args, &stdout, &stderr)
			var ue *usageError
			if !errors.As(err, &ue) {
				t.Fatalf("want a usage error, got %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("a refused command printed:\n%s", stdout.String())
			}
		})
	}
}

func TestUsageWhyOnAnUnknownCrewNamesTheCrew(t *testing.T) {
	w := usageWorkspace(t)
	var stdout, stderr strings.Builder
	err := run([]string{"usage", "shop", "nope", "--why", "--workspace", w.Root()}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("want an error naming the crew, got %v", err)
	}
}
