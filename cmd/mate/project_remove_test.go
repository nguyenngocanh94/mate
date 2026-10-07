package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/spawn"
)

// `mate project remove` stops the Mate (stowing first for the captain)
// before it unregisters, and says so line by line.
func TestProjectRemoveStopsTheMateThenUnregisters(t *testing.T) {
	f := newBoxFixture(t)
	f.rt.SetReadOutput(f.mate, claudeEmptyScreen)
	var out, errw bytes.Buffer
	if err := projectRemove(context.Background(), f.ws, f.deps, "shop", spawn.CallerUser, &out, &errw); err != nil {
		t.Fatalf("projectRemove: %v (%s)", err, errw.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "shop/k3: agent ") || !strings.HasPrefix(lines[1], "shop: Mate stopped (agent ") || lines[2] != "removed project shop" {
		t.Fatalf("stdout = %q, want a line per agent and the removed line", out.String())
	}
	if len(f.rt.SentText) != 1 {
		t.Fatalf("typed %+v, want the captain's stow", f.rt.SentText)
	}
	if _, ok := f.ws.Project("shop"); ok {
		t.Fatal("project is still registered")
	}
	if f.rt.LiveAgentCount() != 0 {
		t.Fatal("the Mate is still running")
	}
}
