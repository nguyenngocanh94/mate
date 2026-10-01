package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

type noticeFunc func(context.Context, string) (notice.Result, error)

func (f noticeFunc) Classify(ctx context.Context, screen string) (notice.Result, error) {
	return f(ctx, screen)
}

func TestConsoleNoticeReadsOnlySelectedAgent(t *testing.T) {
	for _, target := range []string{"mate", "crew"} {
		t.Run(target, func(t *testing.T) {
			f := newBoxFixture(t)
			f.rt.SetReadOutput(f.mate, "Only 10% remains\n❯")
			f.rt.SetReadOutput(f.crew, "New update available\n›")
			before, _ := query.Load(context.Background(), f.ws, consoleHarnesses())
			callsBefore := len(f.rt.Calls)
			count := 0
			classifier := noticeFunc(func(ctx context.Context, screen string) (notice.Result, error) {
				count++
				want := "10%"
				if target == "crew" {
					want = "update"
				}
				if !strings.Contains(screen, want) {
					t.Errorf("read wrong agent: %q", screen)
				}
				return notice.Result{Label: "quota_warning", Confidence: .95}, nil
			})
			req := console.ActionRequest{Action: console.ActionNotice, Target: "shop", TargetKind: target}
			if target == "crew" {
				req.Crew = "k3"
			}
			text, err := consoleNoticeAction(f.ws, f.deps, classifier, f.action)(context.Background(), req)
			if err != nil || count != 1 || !strings.Contains(text, "Advisory only") || !strings.Contains(text, "quota warning") {
				t.Fatalf("result %q, %v; calls=%d", text, err, count)
			}
			for _, call := range f.rt.Calls[callsBefore:] {
				if strings.Contains(call, "Send") || strings.Contains(call, "Prompt") || strings.Contains(call, "Stop") {
					t.Fatalf("mutating runtime call: %s", call)
				}
			}
			after, _ := query.Load(context.Background(), f.ws, consoleHarnesses())
			before.AsOf = after.AsOf
			if !reflect.DeepEqual(before, after) {
				t.Fatal("notice action changed workspace state")
			}
		})
	}
}

func TestConsoleNoticeFailureAndInvalidTarget(t *testing.T) {
	f := newBoxFixture(t)
	count := 0
	c := noticeFunc(func(context.Context, string) (notice.Result, error) {
		count++
		return notice.Result{}, errors.New("unavailable")
	})
	action := consoleNoticeAction(f.ws, f.deps, c, f.action)
	_, err := action(context.Background(), console.ActionRequest{Action: console.ActionNotice, Target: "shop", TargetKind: "workspace"})
	if err == nil || count != 0 {
		t.Fatal("invalid target reached classifier")
	}
	_, err = action(context.Background(), console.ActionRequest{Action: console.ActionNotice, Target: "shop", TargetKind: "mate"})
	if err == nil || count != 1 {
		t.Fatal("API failure hidden")
	}
}

func TestConsoleNoticeConfig(t *testing.T) {
	ws := noticeWorkspace(t)
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := consoleNoticeClient(ws); c != nil || err != nil {
		t.Fatal("default is not off")
	}
	t.Setenv("MATE_JEV_API_KEY_FILE", key)
	if c, err := consoleNoticeClient(ws); c != nil || err != nil {
		t.Fatal("the process environment configured Jev")
	}
	writeEnv(t, ws, "MATE_JEV=on\n")
	if _, err := consoleNoticeClient(ws); err == nil || !strings.Contains(err.Error(), "MATE_JEV_API_KEY_FILE") {
		t.Fatalf("missing key path accepted: %v", err)
	}
	writeEnv(t, ws, "MATE_JEV=on\nMATE_JEV_API_KEY_FILE="+key+"\n")
	if c, err := consoleNoticeClient(ws); c == nil || err != nil {
		t.Fatalf("config: %v", err)
	}
	writeEnv(t, ws, "MATE_JEV=off\nMATE_JEV_API_KEY_FILE="+key+"\n")
	if c, err := consoleNoticeClient(ws); c != nil || err != nil {
		t.Fatalf("off with a key path still configured Jev: %v", err)
	}
	writeEnv(t, ws, "MATE_JEV=maybe\nMATE_JEV_API_KEY_FILE="+key+"\n")
	if _, err := consoleNoticeClient(ws); err == nil || !strings.Contains(err.Error(), "MATE_JEV") {
		t.Fatalf("unknown switch value accepted: %v", err)
	}
	writeEnv(t, ws, "MATE_JEV=on\nMATE_JEV_API_KEY_FILE="+filepath.Join(t.TempDir(), "missing")+"\n")
	if _, err := consoleNoticeClient(ws); err == nil {
		t.Fatal("missing key file accepted")
	}
	if err := os.WriteFile(key, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, ws, "MATE_JEV=on\nMATE_JEV_API_KEY_FILE="+key+"\n")
	if _, err := consoleNoticeClient(ws); err == nil {
		t.Fatal("empty key accepted")
	}
	writeEnv(t, ws, "MATE_JEV=on\nnot a setting\n")
	if _, err := consoleNoticeClient(ws); err == nil || !strings.Contains(err.Error(), ".env") {
		t.Fatalf("malformed .env did not name itself: %v", err)
	}
}

func TestConsoleNoticeKeyPathResolvesHomeAndWorkspace(t *testing.T) {
	ws := noticeWorkspace(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "jev-key"), []byte("k1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, ws, "MATE_JEV=on\nMATE_JEV_API_KEY_FILE=~/jev-key\n")
	if c, err := consoleNoticeClient(ws); c == nil || err != nil {
		t.Fatalf("~/ not expanded: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws.Root(), "local-key"), []byte("k2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, ws, "MATE_JEV=on\nMATE_JEV_API_KEY_FILE=local-key\n")
	if c, err := consoleNoticeClient(ws); c == nil || err != nil {
		t.Fatalf("relative path not resolved against the workspace root: %v", err)
	}
}

func noticeWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	ws, err := store.Init(t.TempDir(), workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func writeEnv(t *testing.T, ws *store.Workspace, text string) {
	t.Helper()
	if err := os.WriteFile(ws.EnvFile(), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}
