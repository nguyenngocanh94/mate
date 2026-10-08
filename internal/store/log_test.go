package store_test

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestStoreAppendStatusAndRead(t *testing.T) {
	w := newProjectWorkspace(t)

	entries, next, err := w.ReadStatus("shop", "k3", 0)
	if err != nil {
		t.Fatalf("ReadStatus before the file exists: %v", err)
	}
	if len(entries) != 0 || next != 0 {
		t.Fatalf("empty read = %+v, next = %d", entries, next)
	}

	lines := []string{"working: reading the tests", "needs-decision: see crews/k3/report.md", "done: 3 tests added"}
	for _, l := range lines {
		if err := w.AppendStatus("shop", "k3", l); err != nil {
			t.Fatalf("AppendStatus: %v", err)
		}
	}

	entries, next, err = w.ReadStatus("shop", "k3", 0)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	for i, e := range entries {
		if e.Line != lines[i] {
			t.Fatalf("entry %d = %q, want %q", i, e.Line, lines[i])
		}
	}
	if entries[0].Offset != 0 {
		t.Fatalf("first offset = %d, want 0", entries[0].Offset)
	}

	// A cursor resumes where it stopped.
	resumed, after, err := w.ReadStatus("shop", "k3", next)
	if err != nil {
		t.Fatalf("ReadStatus from the end: %v", err)
	}
	if len(resumed) != 0 || after != next {
		t.Fatalf("read from the end returned %d entries, next = %d (was %d)", len(resumed), after, next)
	}

	// The multi-line status a shell should never produce is flattened.
	if err := w.AppendStatus("shop", "k3", "blocked: needs\na decision"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}
	entries, _, err = w.ReadStatus("shop", "k3", next)
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if len(entries) != 1 || entries[0].Line != "blocked: needs a decision" {
		t.Fatalf("flattened entry = %+v", entries)
	}
}

func TestStoreReadStatusResumesFromCursor(t *testing.T) {
	w := newProjectWorkspace(t)
	for _, l := range []string{"one", "two", "three"} {
		if err := w.AppendStatus("shop", "k3", l); err != nil {
			t.Fatal(err)
		}
	}

	all, _, err := w.ReadStatus("shop", "k3", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d entries, want 3", len(all))
	}

	rest, _, err := w.ReadStatus("shop", "k3", all[1].Offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 2 || rest[0].Line != "two" || rest[1].Line != "three" {
		t.Fatalf("resumed read = %+v, want two and three", rest)
	}
}

func TestStoreReadStopsAtAPartialLine(t *testing.T) {
	w := newProjectWorkspace(t)
	if err := w.AppendStatus("shop", "k3", "working: one"); err != nil {
		t.Fatal(err)
	}
	// A writer caught mid-append: bytes with no newline yet.
	f, err := os.OpenFile(w.CrewStatus("shop", "k3"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("done: hal"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	entries, next, err := w.ReadStatus("shop", "k3", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Line != "working: one" {
		t.Fatalf("entries = %+v, want only the complete line", entries)
	}

	// Finishing the line makes it readable, whole, from the cursor.
	f, err = os.OpenFile(w.CrewStatus("shop", "k3"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("f done\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	entries, _, err = w.ReadStatus("shop", "k3", next)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Line != "done: half done" {
		t.Fatalf("entries = %+v, want the completed line", entries)
	}
}

func TestStoreAppendSentAndRead(t *testing.T) {
	w := newProjectWorkspace(t)

	when := time.Date(2026, 9, 17, 10, 30, 0, 0, time.UTC)
	want := []store.SentEntry{
		{Time: when, Source: store.SourceUser, Target: store.TargetMate, Text: "ship the checkout fix"},
		{Time: when.Add(time.Second), Source: store.SourceMate, Target: store.CrewTarget("k3"), Text: "read brief.md first"},
		{Time: when.Add(2 * time.Second), Source: store.SourceApp, Target: store.TargetMate, Text: "signal: crews/k3.status"},
	}
	for _, e := range want {
		if err := w.AppendSent("shop", e); err != nil {
			t.Fatalf("AppendSent: %v", err)
		}
	}

	got, next, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatalf("ReadSent: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].Time.Equal(want[i].Time) {
			t.Errorf("entry %d time = %v, want %v", i, got[i].Time, want[i].Time)
		}
		if got[i].Source != want[i].Source || got[i].Target != want[i].Target || got[i].Text != want[i].Text {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	rest, _, err := w.ReadSent("shop", got[1].Offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 2 || rest[0].Text != want[1].Text {
		t.Fatalf("resumed read = %+v", rest)
	}

	// A multi-line text becomes one line, and a missing time is filled in.
	if err := w.AppendSent("shop", store.SentEntry{Source: store.SourceUser, Target: store.TargetMate, Text: "first\nsecond\tthird"}); err != nil {
		t.Fatal(err)
	}
	tail, _, err := w.ReadSent("shop", next)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 || tail[0].Text != "first second third" {
		t.Fatalf("flattened entry = %+v", tail)
	}
	if tail[0].Time.IsZero() {
		t.Fatal("AppendSent did not fill in the time")
	}
}

func TestStoreReadSentSkipsUnparseableLines(t *testing.T) {
	w := newProjectWorkspace(t)
	if err := w.AppendSent("shop", store.SentEntry{Source: store.SourceUser, Target: store.TargetMate, Text: "good"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(w.SentLog("shop"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("junk hand-edited line\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := w.AppendSent("shop", store.SentEntry{Source: store.SourceMate, Target: store.CrewTarget("k3"), Text: "after"}); err != nil {
		t.Fatal(err)
	}

	got, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Text != "good" || got[1].Text != "after" {
		t.Fatalf("entries = %+v, want the two parseable lines", got)
	}
}

func TestStoreAppendsInterleaveWholeLines(t *testing.T) {
	const writers = 2
	const perWriter = 200

	t.Run("status", func(t *testing.T) {
		w := newProjectWorkspace(t)
		var wg sync.WaitGroup
		errs := make(chan error, writers*perWriter)
		for id := 0; id < writers; id++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for i := 0; i < perWriter; i++ {
					line := fmt.Sprintf("working: writer %d line %d %s", id, i, strings.Repeat("x", 200))
					if err := w.AppendStatus("shop", "k3", line); err != nil {
						errs <- err
						return
					}
				}
			}(id)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("AppendStatus: %v", err)
		}

		entries, _, err := w.ReadStatus("shop", "k3", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != writers*perWriter {
			t.Fatalf("got %d lines, want %d", len(entries), writers*perWriter)
		}
		seen := map[string]bool{}
		for _, e := range entries {
			if !strings.HasPrefix(e.Line, "working: writer ") || !strings.HasSuffix(e.Line, strings.Repeat("x", 200)) {
				t.Fatalf("torn line: %q", e.Line)
			}
			if seen[e.Line] {
				t.Fatalf("duplicate line: %q", e.Line)
			}
			seen[e.Line] = true
		}
	})

	t.Run("sent", func(t *testing.T) {
		w := newProjectWorkspace(t)
		var wg sync.WaitGroup
		errs := make(chan error, writers*perWriter)
		for id := 0; id < writers; id++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for i := 0; i < perWriter; i++ {
					entry := store.SentEntry{
						Source: store.SourceApp,
						Target: store.CrewTarget("k3"),
						Text:   fmt.Sprintf("writer %d line %d %s", id, i, strings.Repeat("y", 200)),
					}
					if err := w.AppendSent("shop", entry); err != nil {
						errs <- err
						return
					}
				}
			}(id)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("AppendSent: %v", err)
		}

		entries, _, err := w.ReadSent("shop", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != writers*perWriter {
			t.Fatalf("got %d parseable lines, want %d", len(entries), writers*perWriter)
		}
		for _, e := range entries {
			if e.Source != store.SourceApp || e.Target != store.CrewTarget("k3") {
				t.Fatalf("torn entry: %+v", e)
			}
			if !strings.HasSuffix(e.Text, strings.Repeat("y", 200)) {
				t.Fatalf("torn text: %q", e.Text)
			}
		}
	})
}

func TestStoreLogsRejectBadNames(t *testing.T) {
	w := newProjectWorkspace(t)

	if err := w.AppendStatus("shop", "K3", "working: x"); err == nil {
		t.Fatal("AppendStatus with an invalid crew id succeeded")
	}
	if err := w.AppendStatus("Shop", "k3", "working: x"); err == nil {
		t.Fatal("AppendStatus with an invalid project name succeeded")
	}
	if err := w.AppendSent("../evil", store.SentEntry{Text: "x"}); err == nil {
		t.Fatal("AppendSent with an invalid project name succeeded")
	}
	if _, _, err := w.ReadStatus("shop", "k", 0); err == nil {
		t.Fatal("ReadStatus with an invalid crew id succeeded")
	}
}

// The Jev log is appended line by line under the workspace's state
// directory, one line per call however many writers.
func TestStoreAppendJevLog(t *testing.T) {
	w := newProjectWorkspace(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.AppendJevLog(fmt.Sprintf("2026-10-08T09:00:00Z claude 0123456789ab %d jev composer=empty dialog=none conf=0.90", i)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := w.AppendJevLog("two\nlines"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(w.JevLog())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 21 || lines[20] != "two lines" {
		t.Fatalf("jev.log = %q", data)
	}
	if w.JevLog() != w.StateDir()+"/jev.log" {
		t.Fatalf("JevLog = %s", w.JevLog())
	}
}
