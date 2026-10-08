package send_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/send"
)

func TestUnknownAfterEnterIsNotDelivered(t *testing.T) {
	rt := &scripted{screens: []string{claudeScreen(""), "unrecognised dialog"}}
	deps, _ := testDeps(rt)
	report, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "hello", send.Options{})
	if err == nil || report.Delivered() || len(rt.keys) != 1 {
		t.Fatalf("unknown must stop unconfirmed: report=%+v err=%v keys=%v", report, err, rt.keys)
	}
}

func TestBusyBeforeAndAfterEnterIsNotConfirmation(t *testing.T) {
	rt := &scripted{screens: []string{claudeBusyScreen()}}
	deps, _ := testDeps(rt)
	report, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "hello", send.Options{QueueWhileBusy: true})
	if !errors.Is(err, send.ErrSubmissionUnconfirmed) || report.Delivered() {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestRetryNeverSubmitsEditedComposer(t *testing.T) {
	rt := &scripted{screens: []string{claudeScreen(""), claudeScreen("hello changed by human")}}
	deps, _ := testDeps(rt)
	_, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "hello", send.Options{})
	if err == nil || len(rt.keys) != 1 {
		t.Fatalf("must not Enter edited text: err=%v keys=%v", err, rt.keys)
	}
}

func TestResumeChecksWholeComposerAndNeverTypes(t *testing.T) {
	for _, tc := range []struct {
		name, screen string
		kind         harness.Kind
		ok           bool
	}{
		{"codex wrapped", "› hello\n  world\n\n\n  model · cwd\n", codex.KindCodex, true},
		{"codex suffix", "› hello world\n  extra human text\n\n  model · cwd\n", codex.KindCodex, false},
		{"codex interior newline", "› hello\n\n  world\n\n  model · cwd\n", codex.KindCodex, false},
		{"codex truncated", "› hello\n\n  model · cwd\n", codex.KindCodex, false},
		{"codex no footer", "› hello world\n", codex.KindCodex, false},
		{"codex whitespace edit", "› hello  world\n\n  model · cwd\n", codex.KindCodex, false},
		{"codex empty", "›\n\n  model · cwd\n", codex.KindCodex, false},
		{"claude single line", claudeScreen("hello world"), claude.KindClaude, true},
		{"claude unrecognised multiline", claudeScreen("hello\n  world"), claude.KindClaude, false},
		{"claude suffix", claudeScreen("hello world\n  extra"), claude.KindClaude, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &scripted{screens: []string{tc.screen}}
			rt.onEnter = func(int) []string {
				if tc.kind == claude.KindClaude {
					return []string{claudeScreen("")}
				}
				return []string{"›\n\n  model · cwd\n"}
			}
			deps, _ := testDeps(rt)
			report, err := send.Send(context.Background(), deps, target(), tc.kind, "hello world", send.Options{ResumePending: true})
			if len(rt.typed) != 0 || (err == nil) != tc.ok || report.Delivered() != tc.ok {
				t.Fatalf("report=%+v err=%v typed=%v", report, err, rt.typed)
			}
			if !tc.ok && len(rt.keys) != 0 {
				t.Fatalf("refused recovery pressed keys: %v", rt.keys)
			}
		})
	}
}

func TestRememberFailurePreventsTyping(t *testing.T) {
	rt := &scripted{screens: []string{claudeScreen("")}}
	deps, _ := testDeps(rt)
	boom := errors.New("cannot persist attempt")
	deps.BeforeType = func() error { return boom }
	_, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "hello", send.Options{})
	if !errors.Is(err, boom) || len(rt.typed) != 0 || len(rt.keys) != 0 {
		t.Fatalf("err=%v typed=%v keys=%v", err, rt.typed, rt.keys)
	}
}

func TestResumeRechecksAfterSettle(t *testing.T) {
	rt := &scripted{screens: []string{claudeScreen("hello")}}
	deps, _ := testDeps(rt)
	deps.Sleep = func(context.Context, time.Duration) error {
		rt.screens = append(rt.screens, claudeScreen("human edit"))
		return nil
	}
	_, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "hello", send.Options{ResumePending: true})
	if err == nil || len(rt.keys) != 0 {
		t.Fatalf("err=%v keys=%v", err, rt.keys)
	}
}

func TestInvalidControlBytesDoNotLeaveARecoveryReceipt(t *testing.T) {
	rt := &scripted{screens: []string{claudeScreen("")}}
	deps, _ := testDeps(rt)
	remembered := false
	deps.BeforeType = func() error { remembered = true; return nil }
	_, err := send.Send(context.Background(), deps, target(), claude.KindClaude, "bad\x1b[201~input", send.Options{})
	if err == nil || remembered || len(rt.typed) != 0 {
		t.Fatalf("err=%v remembered=%v typed=%v", err, remembered, rt.typed)
	}
}
