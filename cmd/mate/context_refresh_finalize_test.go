package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// A refresh marks the old session's archive finalized - which makes the
// timeline flush its last message group with ParseTranscriptFinal - only
// after StopMate positively confirmed the agent gone. Herdr reporting the
// session not running is not that confirmation: the agent is still present
// in these fakes, and nothing proved the harness stopped writing.
func TestContextRefreshFinalizesOnlyAfterAConfirmedStop(t *testing.T) {
	for _, tc := range []struct {
		name string
		// lose is what happens to Herdr after the checkpoint is written
		// and before refresh stops the Mate.
		lose          func(rt *runtime.Fake)
		wantRefreshed bool
		wantFinalized bool
	}{
		{name: "confirmed stop", lose: func(*runtime.Fake) {}, wantRefreshed: true, wantFinalized: true},
		{name: "session not running", lose: func(rt *runtime.Fake) { rt.SessionNotRunning = true }, wantRefreshed: true},
		{name: "herdr unreachable", lose: func(rt *runtime.Fake) { rt.SessionLookupErr = errors.New("herdr: connection refused") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, deps := consoleFixture(t, "shop")
			ctx := context.Background()
			if err := os.WriteFile(w.ProjectDoc("shop"), []byte("# Shop\nFixture project\n"), 0644); err != nil {
				t.Fatal(err)
			}
			before, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop"})
			if err != nil {
				t.Fatal(err)
			}
			transcript := filepath.Join(t.TempDir(), before.SessionID+".jsonl")
			if err := os.WriteFile(transcript, []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			meta, err := w.ReadMateMeta("shop")
			if err != nil {
				t.Fatal(err)
			}
			meta[spawn.MetaTranscript] = transcript
			if err := w.WriteMateMeta("shop", meta); err != nil {
				t.Fatal(err)
			}
			deps.NewSessionID = func() string { return "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" }
			rt := deps.Runtime.(*runtime.Fake)
			handle, _, err := spawn.MateHandle(ctx, w, deps, "shop")
			if err != nil {
				t.Fatal(err)
			}
			rt.SetReadOutput(handle, claudeEmptyScreen)
			finished := false
			deps.Sleep = func(context.Context, time.Duration) error {
				if finished {
					return nil
				}
				items, err := w.ReadOutbox("shop")
				if err != nil {
					return err
				}
				for _, item := range items {
					if item.Source == store.OutboxSourceStow && item.State == store.OutboxSent {
						finished = true
						path, _ := refreshMeta(w, "shop", "refresh-request")
						req, _ := store.ReadMeta(path)
						if err := writeCheckpoint(w, "shop", req["nonce"]); err != nil {
							return err
						}
						_ = w.AppendSent("shop", store.SentEntry{Source: store.SourceMate, Target: store.SourceUser, Text: "stowed"})
						tc.lose(rt)
					}
				}
				return nil
			}

			changed, err := contextRefresh(ctx, w, deps, "shop", false)
			if tc.wantRefreshed && (err != nil || !changed) {
				t.Fatalf("refresh: changed=%v err=%v", changed, err)
			}
			if !tc.wantRefreshed && (err == nil || changed) {
				t.Fatalf("refresh accepted although the stop failed: changed=%v err=%v", changed, err)
			}
			if _, err := rt.InspectAgent(ctx, handle); !tc.wantFinalized && err != nil {
				t.Fatalf("the scenario needs the old agent still present: %v", err)
			}

			archives, err := w.MateSessionArchives("shop")
			if err != nil {
				t.Fatal(err)
			}
			var old map[string]string
			for _, a := range archives {
				if a[spawn.MetaSessionID] == before.SessionID {
					old = a
				}
			}
			if old == nil {
				t.Fatalf("no archive for the old session %s in %v", before.SessionID, archives)
			}
			if got := old["finalized"] == "true"; got != tc.wantFinalized {
				t.Fatalf("archive finalized=%q, want finalized=%v: %v", old["finalized"], tc.wantFinalized, old)
			}
			if !tc.wantFinalized && old[spawn.MetaTranscript] != transcript {
				t.Fatalf("an unconfirmed stop must leave the archive on the live transcript, got %v", old)
			}
		})
	}
}
