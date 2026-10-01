package timeline_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The ledger characterization pins every row the ingest derives from the
// fixture corpus - the Claude Mate transcript and the Codex crew rollout -
// byte for byte. `mate usage` and the dashboard only read these tables, so a
// change that leaves this golden alone leaves their numbers alone. It was
// recorded before the transcript capability moved behind the harness
// registry (docs/plans/harness-registry-2026-09-30.md, PR 5) and must not
// change with it.
//
// Three passes are pinned: one ingest of the whole files; the same files
// ingested half-written and then whole, which takes the incremental paths
// (the Codex tail read and the native telemetry cursor); and a Mate session
// archived as a frozen snapshot after a confirmed stop, which takes the
// at-rest parse.
func TestLedgerCharacterization(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, f *fixture)
	}{
		{name: "whole", setup: func(t *testing.T, f *fixture) { f.ingest(t) }},
		{name: "grown", setup: func(t *testing.T, f *fixture) {
			// The half-written rollout has not echoed the last status line
			// yet, so the first pass dates it by the status file's mtime;
			// pin it, or the golden records the test machine's clock.
			status := filepath.Join(f.root, ".mate", "projects", fixtureProject, "crews", fixtureCrew+".status")
			if err := os.Chtimes(status, fixtureNow, fixtureNow); err != nil {
				t.Fatal(err)
			}
			claude, codex := growingTranscripts(t, f)
			for _, p := range []struct{ path, from string }{{claude, claudeFixture}, {codex, codexFixture}} {
				data, err := os.ReadFile(p.from)
				if err != nil {
					t.Fatal(err)
				}
				// Cut inside a record, so the first pass meets a partial line.
				if err := os.WriteFile(p.path, data[:len(data)/2], 0o600); err != nil {
					t.Fatal(err)
				}
			}
			f.ingest(t)
			copyFile(t, claudeFixture, claude)
			copyFile(t, codexFixture, codex)
			f.ingest(t)
		}},
		{name: "finalized", setup: func(t *testing.T, f *fixture) {
			live := filepath.Join(f.root, "archived-transcript.jsonl")
			copyFile(t, claudeFixture, live)
			if err := f.ws.FreezeMateSession(fixtureProject, map[string]string{
				"harness":    "claude",
				"agent":      "mate-shop",
				"session_id": "archived-0000-4000-8000-000000000000",
				"transcript": live,
				"started_at": mustTime("2026-09-19T10:40:00Z").Format("2006-01-02T15:04:05Z07:00"),
			}); err != nil {
				t.Fatalf("FreezeMateSession: %v", err)
			}
			f.ingest(t)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(t, f)
			got := dumpLedger(t, f)
			golden := filepath.Join("testdata", "ledger-"+tc.name+".golden")
			if os.Getenv("MATE_UPDATE_GOLDEN") == "1" {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden: %v (re-run with MATE_UPDATE_GOLDEN=1 to create it)", err)
			}
			if got != string(want) {
				t.Fatalf("the ledger derived from the fixture corpus changed (%s); first difference:\n%s", golden, firstDiff(string(want), got))
			}
		})
	}
}

// growingTranscripts points the fixture's Mate and crew at copies of their
// transcripts that the test can write as it likes.
func growingTranscripts(t *testing.T, f *fixture) (claude, codex string) {
	t.Helper()
	claude = filepath.Join(f.root, "grown-claude.jsonl")
	codex = filepath.Join(f.root, "grown-codex.jsonl")
	mate, err := f.ws.ReadMateMeta(fixtureProject)
	if err != nil {
		t.Fatal(err)
	}
	mate["transcript"] = claude
	if err := f.ws.WriteMateMeta(fixtureProject, mate); err != nil {
		t.Fatal(err)
	}
	writeCrewBinding(t, f, map[string]string{"transcript": codex})
	return claude, codex
}

// ledgerTables are the tables the transcript ingest writes, with the columns
// that order them. event.id is an insertion counter and source_mtime the
// clock of the test machine; both are left out.
var ledgerTables = []struct{ name, order string }{
	{"session", "id"},
	{"turn", "id"},
	{"action", "id"},
	{"usage_sample", "id"},
	{"event", "dedup"},
	{"cursor", "source_path"},
	{"telemetry_cursor", "source_path"},
}

func dumpLedger(t *testing.T, f *fixture) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		t.Fatal(err)
	}
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	clean := strings.NewReplacer(testdata, "{{TESTDATA}}", root, "{{ROOT}}", f.root, "{{ROOT}}")
	var out strings.Builder
	for _, table := range ledgerTables {
		rows, err := f.db.SQL().Query(fmt.Sprintf(`SELECT * FROM %s ORDER BY %s`, table.name, table.order))
		if err != nil {
			t.Fatalf("dump %s: %v", table.name, err)
		}
		lines := dumpRows(t, rows, table.name)
		sort.Strings(lines)
		fmt.Fprintf(&out, "== %s (%d)\n", table.name, len(lines))
		for _, l := range lines {
			out.WriteString(clean.Replace(l) + "\n")
		}
	}
	return out.String()
}

func dumpRows(t *testing.T, rows *sql.Rows, table string) []string {
	t.Helper()
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		var cells []string
		for i, c := range cols {
			if (table == "event" && c == "id") || c == "source_mtime" {
				continue
			}
			v := values[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			cells = append(cells, fmt.Sprintf("%s=%v", c, v))
		}
		lines = append(lines, strings.Join(cells, " | "))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("line %d\nwant: %s\ngot:  %s", i+1, wl, gl)
		}
	}
	return "(none)"
}
