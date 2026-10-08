package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestConsoleSaysWhenTheWorkspaceHasTheOldLayout: the snapshot carries the
// workspace's layout, and the status line of a workspace still on layout 1
// says to run mate migrate; one on layout 2 says nothing about it.
func TestConsoleSaysWhenTheWorkspaceHasTheOldLayout(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	snap, err := query.Load(context.Background(), w, consoleHarnesses(), consoleTools())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Layout != 2 {
		t.Fatalf("snapshot layout = %d, want 2", snap.Layout)
	}
	if got := consoleLayoutNotice(w); got != "" {
		t.Fatalf("notice on layout 2 = %q, want none", got)
	}

	raw, err := os.ReadFile(w.WorkspaceFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.WorkspaceFile(), []byte(strings.Replace(string(raw), "layout: 2\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := store.Open(w.Root())
	if err != nil {
		t.Fatalf("the console's workspace no longer opens: %v", err)
	}
	snap, err = query.Load(context.Background(), old, consoleHarnesses(), consoleTools())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Layout != 1 {
		t.Fatalf("snapshot layout = %d, want 1", snap.Layout)
	}
	if got := consoleLayoutNotice(old); got != "old layout: run mate migrate" {
		t.Fatalf("notice on the old layout = %q, want %q", got, "old layout: run mate migrate")
	}
}
