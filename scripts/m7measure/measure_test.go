package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// fixture is a one-project run: ship k1 asks once, hands back, is told to
// fix one thing and hands back again; scout k2 writes a report; the captain
// typed twice.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	w, err := store.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	t0 := time.Date(2026, 9, 24, 5, 0, 0, 0, time.UTC)
	at := func(s int) string { return db.FormatTime(t0.Add(time.Duration(s) * time.Second)) }
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := handle.SQL().Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, a := range [][3]string{{"mate:shop", "mate", "mate"}, {"user:shop", "user", "captain"},
		{"crew:shop:k1", "crew", "k1"}, {"crew:shop:k2", "crew", "k2"}} {
		exec(`INSERT INTO actor(id, project, kind, name) VALUES (?, 'shop', ?, ?)`, a[0], a[1], a[2])
	}
	for _, s := range [][2]string{{"s-mate", "mate:shop"}, {"s-k1", "crew:shop:k1"}, {"s-k2", "crew:shop:k2"}} {
		exec(`INSERT INTO session(id, actor_id) VALUES (?, ?)`, s[0], s[1])
	}
	exec(`INSERT INTO task(crew_actor_id, project, spawned_at) VALUES ('crew:shop:k1', 'shop', ?)`, at(0))
	exec(`INSERT INTO task(crew_actor_id, project, spawned_at) VALUES ('crew:shop:k2', 'shop', ?)`, at(10))
	// k1: three model calls over two prompts; k2 one; the Mate two.
	turn := func(id, actor, session, ref string, in, cr, cw, out int) {
		exec(`INSERT INTO turn(id, actor_id, session_id, started_at, harness_turn_ref,
			input_tokens, cache_read_tokens, cache_write_tokens, output_tokens) VALUES (?,?,?,?,?,?,?,?,?)`,
			id, actor, session, at(1), ref, in, cr, cw, out)
	}
	turn("t1", "crew:shop:k1", "s-k1", "p1", 100, 1000, 0, 10)
	turn("t2", "crew:shop:k1", "s-k1", "p1", 100, 1000, 0, 10)
	turn("t3", "crew:shop:k1", "s-k1", "p2", 100, 1000, 0, 10)
	turn("t4", "crew:shop:k2", "s-k2", "q1", 5, 50, 0, 1)
	turn("t5", "mate:shop", "s-mate", "m1", 3, 3000, 200, 30)
	turn("t6", "mate:shop", "s-mate", "m2", 3, 3000, 200, 30)

	n := 0
	event := func(sec int, actor, kind, payload string) int64 {
		n++
		res, err := handle.SQL().Exec(`INSERT INTO event(dedup, project, at, actor_id, kind, payload) VALUES (?, 'shop', ?, ?, ?, ?)`,
			kind+string(rune('a'+n)), at(sec), actor, kind, payload)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	message := func(sec int, from, to string) {
		id := event(sec, from, "message.sent", `{}`)
		exec(`INSERT INTO message(event_id, from_actor_id, to_actor_id, channel) VALUES (?, ?, ?, 'pane')`, id, from, to)
	}
	message(0, "user:shop", "mate:shop")
	asked := event(60, "crew:shop:k1", "status.appended", `{"verb":"needs-decision"}`)
	exec(`INSERT INTO question(id, asked_event_id, crew_actor_id, asked_at) VALUES ('q1', ?, 'crew:shop:k1', ?)`, asked, at(60))
	message(90, "mate:shop", "crew:shop:k1") // the answer, before any hand-back
	event(125, "crew:shop:k1", "status.appended", `{"verb":"wait-mate"}`)
	message(150, "mate:shop", "crew:shop:k1") // a correction after it: rework
	event(200, "crew:shop:k1", "status.appended", `{"verb":"wait-mate"}`)
	message(210, "user:shop", "mate:shop")

	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(w.CrewBrief("shop", "k1"), `# Task

## Captain's words
Add a Buy button.

## Acceptance
- The button shows. verify: grep it
- The build passes. verify: make build

## Open decisions
none
`)
	write(w.CrewHandback("shop", "k1"), "| criterion | pass/fail | command | output |\n| --- | --- | --- | --- |\n"+
		"| The button shows | pass | grep Buy README.md | [Buy] |\n| The build passes | **fail** | make build | no Makefile |\n\n## Deviations from Build\nnone\n")
	write(w.CrewBrief("shop", "k2"), "# Task\n\n## Captain's words\nFind ESP32.\n")
	write(w.CrewReport("shop", "k2"), "# Report\n")
	return root
}

func TestMeasureReadsEveryMetricFromTheRun(t *testing.T) {
	root := fixture(t)
	// A workspace directory and its database file are the same run.
	for _, path := range []string{root, filepath.Join(root, ".mate", "mate.db")} {
		tasks, mates, err := Measure(context.Background(), "after", path)
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) != 2 || len(mates) != 1 {
			t.Fatalf("%s: %d task(s), %d mate(s); want 2 and 1", path, len(tasks), len(mates))
		}
		k1 := tasks[0]
		want := Task{Label: "after", Project: "shop", Crew: "k1", Kind: "ship", Questions: 1, Rework: 1,
			Calls: 3, Turns: 2, In: 300, CacheRead: 3000, Out: 30, ToWaitMate: 125 * time.Second, Handback: "yes 2/2"}
		if k1 != want {
			t.Fatalf("k1 = %+v\nwant %+v", k1, want)
		}
		k2 := tasks[1]
		if k2.Kind != "scout" || k2.Handback != "n/a" || k2.ToWaitMate != 0 || k2.Rework != 0 || k2.Calls != 1 {
			t.Fatalf("k2 = %+v; want a scout that never handed back", k2)
		}
		m := mates[0]
		if m.Calls != 2 || m.Turns != 2 || m.CacheRead != 6000 || m.CacheWrite != 400 || m.CaptainLines != 2 || m.ToCrew != 2 {
			t.Fatalf("mate = %+v", m)
		}
	}
}

func TestHandbackMissingARow(t *testing.T) {
	root := fixture(t)
	w, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.CrewHandback("shop", "k1"), []byte("| criterion | pass/fail | command | output |\n| --- | --- | --- | --- |\n| The button shows | pass | grep | ok |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, hb := readBriefAndHandback(w, "shop", "k1"); hb != "no 1/2" {
		t.Fatalf("handback = %q, want no 1/2", hb)
	}
	if err := os.Remove(w.CrewHandback("shop", "k1")); err != nil {
		t.Fatal(err)
	}
	if _, hb := readBriefAndHandback(w, "shop", "k1"); hb != "no handback.md" {
		t.Fatalf("handback = %q, want no handback.md", hb)
	}
}

func TestRunPrintsBothTables(t *testing.T) {
	root := fixture(t)
	var out bytes.Buffer
	if err := run([]string{"before=" + root, "after=" + root}, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"| before | shop | k1 | ship | 1 | 1 | 3 | 2 | 300 | 3.0k | 0 | 30 | 3.3k | 2m5s | yes 2/2 |",
		"| after | shop | k2 | scout |",
		"| before | shop | 2 | 2 | 6 | 6.0k | 400 | 60 | 6.5k | 2 | 2 |",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output lacks %q:\n%s", want, text)
		}
	}
	if err := run([]string{"no-label"}, &out); err == nil {
		t.Fatal("an argument without a label was accepted")
	}
}
