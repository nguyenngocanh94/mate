package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/memory"
	"github.com/nguyenngocanh94/matev2/internal/spawn"
)

// `matev2 mate stop` stows first only for the captain, and only into an
// empty composer (docs/mvp.md task 37, B7).
func TestMateStopStowsOnlyForTheCaptain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		caller string
		noStow bool
		screen string
		want   string
		typed  int
	}{
		{"captain, empty composer", spawn.CallerUser, false, claudeEmptyScreen, "; stowed\n", 1},
		{"captain, unsent text", spawn.CallerUser, false, claudePendingScreen, "; not stowed: the composer holds unsent text\n", 0},
		{"captain, --no-stow", spawn.CallerUser, true, claudeEmptyScreen, "; not stowed: --no-stow\n", 0},
		{"the Mate itself", spawn.CallerMate, false, claudeEmptyScreen, "; not stowed: the Mate stopped itself\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBoxFixture(t)
			f.rt.SetReadOutput(f.mate, tc.screen)
			var out, errw bytes.Buffer
			if err := mateStop(context.Background(), f.ws, f.deps, "shop", tc.caller, tc.noStow, &out, &errw); err != nil {
				t.Fatalf("mateStop: %v (%s)", err, errw.String())
			}
			if !strings.HasPrefix(out.String(), "shop: stopped (agent ") || !strings.HasSuffix(out.String(), tc.want) {
				t.Fatalf("stdout = %q, want it to end %q", out.String(), tc.want)
			}
			if len(f.rt.SentText) != tc.typed {
				t.Fatalf("typed %+v, want %d line(s)", f.rt.SentText, tc.typed)
			}
			if tc.typed == 1 && !strings.HasSuffix(f.rt.SentText[0].Text, memory.StowLine) {
				t.Fatalf("typed %q, want the stow line", f.rt.SentText[0].Text)
			}
			if status, err := spawn.MateStatus(context.Background(), f.ws, f.deps, "shop"); err != nil || status.State == spawn.StateRunning {
				t.Fatalf("after mate stop: %+v, %v", status, err)
			}
		})
	}
}
