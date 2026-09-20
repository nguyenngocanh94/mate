package timeline_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/timeline"
)

// A crew's branch is short-lived: `matev2 merge` deletes it on its way out,
// so a crew that commits a few seconds before somebody merges can leave no
// window in which a five-second poll could read `git log <default>..<branch>`.
// Measured 2026-09-20 in a live run: six seconds between the commit and the
// merge, no poll inside it, and the merge could not be proved at all.
//
// The crew's own transcript keeps the sha for ever, because git echoes it
// back: `[matev2/buybtn 6b8ee07] docs: add …`. This is that rule, end to end:
// the branch is already gone before the first ingest, and the commit and the
// merge are still recorded.
func TestACommitIsFoundInTheTranscriptWhenTheBranchIsAlreadyGone(t *testing.T) {
	f := newFixture(t)
	repo := filepath.Join(f.root, fixtureProject)

	// The crew's work, already merged and its branch already deleted - the
	// state `matev2 merge` leaves behind.
	gitRun(t, repo, "checkout", "-b", fixtureBranch)
	if err := os.WriteFile(filepath.Join(repo, "README.md"),
		[]byte("# shop\n\n[Buy](pages/checkout-express.html)\n"), 0o644); err != nil {
		t.Fatalf("edit README: %v", err)
	}
	gitRun(t, repo, "commit", "-am", "docs: add Buy link")
	short := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "--short", "HEAD"))
	full := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "HEAD"))
	gitRun(t, repo, "checkout", "main")
	gitRun(t, repo, "merge", "--ff-only", fixtureBranch)
	gitRun(t, repo, "branch", "-D", fixtureBranch)

	// A rollout whose only tool call is the `git commit` that made it.
	rollout := filepath.Join(f.root, "crew-rollout.jsonl")
	writeCommitRollout(t, rollout, repo, fixtureBranch, short)

	meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
	if err != nil {
		t.Fatalf("ReadCrewMeta: %v", err)
	}
	meta[timeline.MetaTranscript] = rollout
	meta[timeline.MetaState] = "finished"
	meta[timeline.MetaStoppedAt] = mustTime("2026-09-19T10:47:00Z").Format(time.RFC3339)
	if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}

	f.ingest(t)

	var sha, subject string
	if err := f.db.SQL().QueryRow(
		`SELECT json_extract(payload, '$.sha'), json_extract(payload, '$.subject')
		   FROM event WHERE kind = ? AND actor_id = ?`,
		timeline.KindGitCommitted, f.crewActor()).Scan(&sha, &subject); err != nil {
		t.Fatalf("read the commit event: %v (the branch was gone, so only the transcript could name it)", err)
	}
	if sha != full {
		t.Fatalf("the commit event names %q, want the full sha %q", sha, full)
	}
	if subject != "docs: add Buy link" {
		t.Fatalf("the commit event's subject is %q", subject)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ?`, timeline.KindMergeDone); n != 1 {
		t.Fatalf("%d merge.done event(s) for a crew whose landed branch was already deleted, want 1", n)
	}

	// Ingesting again must not record the commit a second time: the short sha
	// from the transcript and the full sha from git are one commit.
	f.ingest(t)
	if n := f.count(t, `SELECT COUNT(*) FROM event WHERE kind = ?`, timeline.KindGitCommitted); n != 1 {
		t.Fatalf("%d git.committed event(s) after two ingests, want 1", n)
	}
}

// writeCommitRollout writes the smallest Codex rollout that carries one
// `git commit` and the line git echoed back. Every field the parser insists on
// is there - timestamp, ordinal, type - and nothing else is.
func writeCommitRollout(t *testing.T, path, cwd, branch, short string) {
	t.Helper()
	at := mustTime("2026-09-19T10:46:20Z")
	stamp := func(offset time.Duration) string { return at.Add(offset).Format(time.RFC3339Nano) }
	command := fmt.Sprintf(`git add README.md && git commit -m "docs: add Buy link"`)
	output := fmt.Sprintf("[%s %s] docs: add Buy link\n 1 file changed, 1 insertion(+)\n", branch, short)

	records := []map[string]any{
		{"timestamp": stamp(0), "ordinal": 0, "type": "session_meta", "payload": map[string]any{
			"session_id": "01a0b944-0000-0000-0000-000000000001", "cwd": cwd,
			"cli_version": "0.154.0", "model_provider": "openai",
		}},
		{"timestamp": stamp(time.Second), "ordinal": 1, "type": "event_msg", "payload": map[string]any{
			"type": "task_started", "turn_id": "turn-1",
		}},
		{"timestamp": stamp(2 * time.Second), "ordinal": 2, "type": "turn_context", "payload": map[string]any{
			"model": "gpt-5.6-terra", "turn_id": "turn-1",
		}},
		{"timestamp": stamp(3 * time.Second), "ordinal": 3, "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call", "call_id": "call_commit", "name": "exec",
			"input": fmt.Sprintf(`const r = await tools.exec_command(%s);`,
				mustJSON(t, map[string]any{"cmd": command, "workdir": cwd})),
		}},
		{"timestamp": stamp(4 * time.Second), "ordinal": 4, "type": "response_item", "payload": map[string]any{
			"type": "custom_tool_call_output", "call_id": "call_commit",
			"output": []map[string]any{{"type": "input_text", "text": output}},
		}},
		{"timestamp": stamp(5 * time.Second), "ordinal": 5, "type": "event_msg", "payload": map[string]any{
			"type": "token_count", "info": map[string]any{
				"total_token_usage": map[string]any{
					"input_tokens": 100, "cached_input_tokens": 0, "cache_write_input_tokens": 0,
					"output_tokens": 10, "reasoning_output_tokens": 0, "total_tokens": 110,
				},
				"last_token_usage": map[string]any{
					"input_tokens": 100, "cached_input_tokens": 0, "cache_write_input_tokens": 0,
					"output_tokens": 10, "reasoning_output_tokens": 0, "total_tokens": 110,
				},
			},
		}},
		// A later record so the trailing group is not the one that matters.
		{"timestamp": stamp(6 * time.Second), "ordinal": 6, "type": "event_msg", "payload": map[string]any{
			"type": "task_complete", "turn_id": "turn-1",
		}},
	}

	var b strings.Builder
	for _, record := range records {
		b.WriteString(mustJSON(t, record))
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}
