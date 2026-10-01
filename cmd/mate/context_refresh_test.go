package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

func TestContextRefreshRequiresReceiptAndStartsFresh(t *testing.T) {
	for _, scenario := range []string{"success", "no-receipt", "captain-returned", "changed-after-checkpoint", "no-stop"} {
		t.Run(scenario, func(t *testing.T) {
			w, deps := consoleFixture(t, "shop")
			ctx := context.Background()
			if err := os.WriteFile(w.ProjectDoc("shop"), []byte("# Shop\nFixture project\n"), 0644); err != nil {
				t.Fatal(err)
			}
			before, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop"})
			if err != nil {
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
						if scenario != "no-receipt" {
							if err := writeCheckpoint(w, "shop", req["nonce"]); err != nil {
								return err
							}
						}
						if scenario == "captain-returned" {
							_ = w.AppendSent("shop", store.SentEntry{Source: store.SourceUser, Target: store.TargetMate, Text: "new request"})
						}
						if scenario == "changed-after-checkpoint" {
							_ = os.WriteFile(w.MemoryFile("shop"), []byte("changed"), 0644)
						}
						if scenario != "no-stop" {
							_ = w.AppendSent("shop", store.SentEntry{Source: store.SourceMate, Target: store.SourceUser, Text: "stowed"})
						}
					}
				}
				return nil
			}
			changed, err := contextRefresh(ctx, w, deps, "shop", false)
			meta, readErr := w.ReadMateMeta("shop")
			if readErr != nil {
				t.Fatal(readErr)
			}
			if scenario == "success" {
				if err != nil || !changed {
					t.Fatalf("refresh: %v %v", changed, err)
				}
				if meta[spawn.MetaSessionID] == before.SessionID || meta[spawn.MetaResumed] == "true" {
					t.Fatalf("resumed old context: %v", meta)
				}
				if len(rt.StartArgv) != 2 || strings.Contains(strings.Join(rt.StartArgv[1], " "), "--resume") {
					t.Fatalf("starts: %v", rt.StartArgv)
				}
			} else {
				if err == nil || changed {
					t.Fatalf("unsafe refresh accepted: %v %v", changed, err)
				}
				if meta[spawn.MetaSessionID] != before.SessionID || len(rt.StartArgv) != 1 {
					t.Fatalf("old session lost: %v", meta)
				}
			}
		})
	}
}

func TestRefreshQuietRequiresAnAnsweredCaptainAndFiveMinutes(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	now := time.Now()
	appendEntry := func(source, target string, at time.Time) {
		t.Helper()
		if err := w.AppendSent("shop", store.SentEntry{Source: source, Target: target, Time: at, Text: "line"}); err != nil {
			t.Fatal(err)
		}
	}
	appendEntry(store.SourceUser, store.TargetMate, now.Add(-10*time.Minute))
	appendEntry(store.SourceMate, store.CrewTarget("k3"), now.Add(-9*time.Minute))
	if ok, _ := refreshQuiet(w, "shop", now); ok {
		t.Fatal("crew steer counted as answer")
	}
	appendEntry(store.SourceMate, store.SourceUser, now.Add(-6*time.Minute))
	if ok, err := refreshQuiet(w, "shop", now); err != nil || !ok {
		t.Fatalf("quiet: %v %v", ok, err)
	}
	appendEntry(store.SourceUser, store.TargetMate, now.Add(-time.Minute))
	if ok, _ := refreshQuiet(w, "shop", now); ok {
		t.Fatal("unanswered captain discarded")
	}
}

// claudeWithoutTurnEnd is Claude declaring no turn-end evidence.
type claudeWithoutTurnEnd struct{ claude.Claude }

func (c claudeWithoutTurnEnd) Capabilities() harness.Capabilities {
	caps := c.Claude.Capabilities()
	caps.TurnEnd = harness.Cap[harness.TurnEndEvidence]{Status: harness.CapUnknown, Reason: "a test harness that never measured it"}
	return caps
}

func TestAutomaticRefreshUsesOnlyCurrentSessionAndHonorsThreshold(t *testing.T) {
	for _, scenario := range []string{"old-session", "below-limit", "disabled", "held", "queued", "busy", "codex-meta", "no-turn-end", "eligible"} {
		t.Run(scenario, func(t *testing.T) {
			w, deps := consoleFixture(t, "shop")
			ctx := context.Background()
			started, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop"})
			if err != nil {
				t.Fatal(err)
			}
			rt := deps.Runtime.(*runtime.Fake)
			h, _, _ := spawn.MateHandle(ctx, w, deps, "shop")
			rt.SetReadOutput(h, claudeEmptyScreen)
			if err := w.SetAuto("shop", true); err != nil {
				t.Fatal(err)
			}
			if err := w.AppendSent("shop", store.SentEntry{Source: store.SourceMate, Target: store.SourceUser, Time: deps.Now().Add(-6 * time.Minute), Text: "answered"}); err != nil {
				t.Fatal(err)
			}
			handle, err := db.Open(w)
			if err != nil {
				t.Fatal(err)
			}
			defer handle.Close()
			actor := timeline.MateActorID("shop")
			tokens := int64(160000)
			if scenario == "below-limit" {
				tokens = 149999
			}
			seedActorTurnAndPricing(t, handle, actor, "shop", "mate", "unpriced", tokens, 0, 0, 1, 0)
			session := started.SessionID
			if scenario == "old-session" {
				session = "prior-session"
			}
			if _, err := handle.SQL().Exec(`UPDATE session SET harness_session_id=?`, session); err != nil {
				t.Fatal(err)
			}
			if scenario == "disabled" {
				cfg, _ := w.LoadProject("shop")
				cfg.Mate.RefreshContext = -1
				if err := w.SaveProject("shop", cfg); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "held" {
				if err := w.SetMode("shop", false); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "busy" {
				rt.SetReadOutput(h, claudeBusyBoxScreen)
			}
			// Quietness is judged by answers the Stop hook logs; a harness
			// whose turn-end evidence is not that log is never refreshed
			// automatically, whether it is Codex or a Claude declaring none.
			// The Codex Mate shows an empty Codex composer, so the turn-end
			// gate is the only thing that keeps it from being refreshed.
			if scenario == "codex-meta" {
				meta, _ := w.ReadMateMeta("shop")
				meta[spawn.MetaHarness] = string(codex.KindCodex)
				if err := w.WriteMateMeta("shop", meta); err != nil {
					t.Fatal(err)
				}
				rt.SetReadOutput(h, codexEmptyScreen)
			}
			if scenario == "no-turn-end" {
				reg, err := harness.NewRegistry(nil, claudeWithoutTurnEnd{})
				if err != nil {
					t.Fatal(err)
				}
				deps.Harnesses = reg
			}
			if scenario == "queued" {
				_, err := consoleOutbox(w, deps).Enqueue("shop", outbox.Request{Source: store.OutboxSourceAssign, Key: "new-work", Text: "resolve: new work"})
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = contextRefresh(ctx, w, deps, "shop", true)
			if scenario == "eligible" {
				if err == nil || len(rt.SentText) != 1 {
					t.Fatalf("eligible refresh did not attempt strict stow: %v %v", err, rt.SentText)
				}
			} else if err != nil || len(rt.SentText) != 0 {
				t.Fatalf("ineligible refresh spoke: %v %v", err, rt.SentText)
			}
		})
	}
}

func TestCheckpointRequiresEveryOpenCrewInFlight(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	ctx := context.Background()
	if _, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ProjectDoc("shop"), []byte("# Shop\n"), 0644); err != nil {
		t.Fatal(err)
	}
	meta, _ := w.ReadMateMeta("shop")
	path, _ := refreshMeta(w, "shop", "refresh-request")
	if err := store.WriteMeta(path, map[string]string{"nonce": "test-nonce", "session": meta[spawn.MetaSessionID]}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "auth", map[string]string{spawn.MetaState: spawn.CrewStateSpawned}); err != nil {
		t.Fatal(err)
	}
	if err := w.EditBacklog("shop", "add", "author", memory.BacklogInFlight, "unrelated item"); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpoint(w, "shop", "test-nonce"); err == nil || !strings.Contains(err.Error(), "auth absent") {
		t.Fatalf("missing crew accepted: %v", err)
	}
	if err := w.EditBacklog("shop", "add", "auth", memory.BacklogInFlight, "crew remains in flight"); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpoint(w, "shop", "test-nonce"); err != nil {
		t.Fatal(err)
	}
}
