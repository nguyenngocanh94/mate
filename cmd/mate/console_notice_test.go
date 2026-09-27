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
			before, _ := query.Load(context.Background(), f.ws)
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
			after, _ := query.Load(context.Background(), f.ws)
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
	if c, err := consoleNoticeClient(func(string) string { return "" }); c != nil || err != nil {
		t.Fatal("default is not off")
	}
	file := filepath.Join(t.TempDir(), "key")
	env := func(string) string { return file }
	if _, err := consoleNoticeClient(env); err == nil {
		t.Fatal("missing key file accepted")
	}
	if err := os.WriteFile(file, []byte("test-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if c, err := consoleNoticeClient(env); c == nil || err != nil {
		t.Fatalf("config: %v", err)
	}
	if err := os.WriteFile(file, []byte(" \n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := consoleNoticeClient(env); err == nil {
		t.Fatal("empty key accepted")
	}
}
