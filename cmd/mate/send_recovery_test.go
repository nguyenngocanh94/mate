package main

import (
	"context"
	"errors"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestCrewSendRecoversOnlyItsRecordedDraft(t *testing.T) {
	for _, mode := range []string{"same draft", "human suffix", "different payload", "new incarnation", "empty unconfirmed", "no record"} {
		t.Run(mode, func(t *testing.T) {
			w := liveCrewWorkspace(t, "shop")
			rt := runtime.NewFake()
			deps := fakeSpawnDeps(t, rt)
			res := spawnFakeCrew(t, w, deps, "shop", "k3")
			h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: res.Session}, Name: res.Agent}
			rt.SetReadOutput(h, codexEmptyScreen)
			rt.OnSendText = func(h runtime.AgentHandle, text string) { rt.SetReadOutput(h, codexPendingScreen(text)) }
			if mode != "no record" {
				_, err := sendToCrew(context.Background(), w, deps, "shop", "k3", "hello world", store.SourceMate, send.Options{})
				if !errors.Is(err, send.ErrEnterSwallowed) {
					t.Fatalf("initial send = %v", err)
				}
			}
			text := "hello world"
			switch mode {
			case "same draft":
				rt.SetReadOutput(h, codexPendingScreen("hello\n  world\n\n"))
			case "human suffix":
				rt.SetReadOutput(h, codexPendingScreen("hello world\n  extra"))
			case "different payload":
				text = "another task"
			case "empty unconfirmed":
				rt.SetReadOutput(h, codexEmptyScreen)
			case "no record":
				rt.SetReadOutput(h, codexPendingScreen(text))
			case "new incarnation":
				meta, _ := w.ReadCrewMeta("shop", "k3")
				meta[spawn.MetaStartedAt] = "2099-01-01T00:00:00Z"
				if err := w.WriteCrewMeta("shop", "k3", meta); err != nil {
					t.Fatal(err)
				}
			}
			keys, typed := len(rt.SentKeys), len(rt.SentText)
			rt.OnSendKeys = func(h runtime.AgentHandle, _ []string) { rt.SetReadOutput(h, codexBusyScreen) }
			report, err := sendToCrew(context.Background(), w, deps, "shop", "k3", text, store.SourceMate, send.Options{})
			entries, _, readErr := w.ReadSent("shop", 0)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(rt.SentText) != typed {
				t.Fatal("recovery retyped payload")
			}
			if mode == "same draft" {
				if err != nil || !report.Delivered() || !report.Resumed || len(entries) != 1 || len(rt.SentKeys) != keys+1 {
					t.Fatalf("report=%+v err=%v sent=%v keys=%v", report, err, entries, rt.SentKeys)
				}
			} else if err == nil || len(entries) != 0 || len(rt.SentKeys) != keys {
				t.Fatalf("unsafe recovery: err=%v sent=%v keys=%v", err, entries, rt.SentKeys)
			}
		})
	}
}

func TestCrewUnknownAfterEnterDoesNotWriteSent(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeSpawnDeps(t, rt)
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	h := runtime.AgentHandle{Session: runtime.SessionHandle{Name: res.Session}, Name: res.Agent}
	rt.SetReadOutput(h, codexEmptyScreen)
	rt.OnSendKeys = func(h runtime.AgentHandle, _ []string) { rt.SetReadOutput(h, "unexpected dialog") }
	_, err := sendToCrew(context.Background(), w, deps, "shop", "k3", "hello", store.SourceMate, send.Options{})
	entries, _, readErr := w.ReadSent("shop", 0)
	if err == nil || readErr != nil || len(entries) != 0 {
		t.Fatalf("err=%v read=%v sent=%v", err, readErr, entries)
	}
}
