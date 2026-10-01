package spawn_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

// These tests pin what StopResult.Confirmed means: the agent was proved
// absent by `agent get` and the session inventory. A stop that only inferred
// absence because Herdr's session list did not report the session running
// must not say Confirmed, because context refresh freezes the transcript for
// ParseTranscriptFinal only on a confirmed stop.

func TestStopMateConfirmsAStoppedAgent(t *testing.T) {
	for _, tc := range []struct {
		name string
		// gone removes the agent before the stop, so the stop finds the
		// record stale while the session is still running.
		gone bool
	}{
		{name: "live agent stopped"},
		{name: "agent already gone from a running session", gone: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorkspace(t, "shop")
			rt := runtime.NewFake()
			deps := fakeDeps(t, rt)
			if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"}); err != nil {
				t.Fatalf("StartMate: %v", err)
			}
			if tc.gone {
				handle, _, err := spawn.MateHandle(context.Background(), w, deps, "shop")
				if err != nil {
					t.Fatalf("MateHandle: %v", err)
				}
				if err := rt.StopAgent(context.Background(), handle, runtime.StopForce); err != nil {
					t.Fatalf("StopAgent: %v", err)
				}
			}
			res, err := spawn.StopMate(context.Background(), w, deps, "shop")
			if err != nil {
				t.Fatalf("StopMate: %v", err)
			}
			if !res.Confirmed || res.AlreadyGone != tc.gone {
				t.Fatalf("confirmed=%v alreadyGone=%v, want confirmed=true alreadyGone=%v", res.Confirmed, res.AlreadyGone, tc.gone)
			}
		})
	}
}

// The fake's SessionNotRunning leaves the agent in place: `agent get` would
// still find it, and StopMate never asks.
func TestStopMateDoesNotConfirmWhenTheSessionIsNotRunning(t *testing.T) {
	w := newWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	handle, _, err := spawn.MateHandle(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("MateHandle: %v", err)
	}
	rt.SessionNotRunning = true
	rt.Calls = nil

	res, err := spawn.StopMate(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if res.Confirmed || !res.AlreadyGone {
		t.Fatalf("confirmed=%v alreadyGone=%v, want an unconfirmed already-gone stop", res.Confirmed, res.AlreadyGone)
	}
	if !slices.Equal(rt.Calls, []string{"LookupSession"}) {
		t.Fatalf("calls = %v, want LookupSession alone", rt.Calls)
	}
	if _, err := rt.InspectAgent(context.Background(), handle); err != nil {
		t.Fatalf("the scenario needs `agent get` to still find the agent: %v", err)
	}
}

// The same answers through the real Herdr adapter, with `herdr` itself
// scripted: the session missing from `session list`, listed but not running,
// and `session list` failing. Any other command would mean StopMate asked
// about the agent, which nothing can answer on these branches.
func TestStopMateThroughHerdrWhenTheSessionIsNotRunning(t *testing.T) {
	errUnreachable := errors.New("herdr: connection refused")
	for _, tc := range []struct {
		name    string
		list    func(session string) ([]byte, error)
		wantErr bool
	}{
		{name: "session missing", list: func(string) ([]byte, error) { return []byte(`{"sessions":[]}`), nil }},
		{name: "session listed not running", list: func(s string) ([]byte, error) {
			return []byte(`{"sessions":[{"default":false,"name":"` + s + `","running":false,"socket_path":""}]}`), nil
		}},
		{name: "herdr unreachable", list: func(string) ([]byte, error) { return nil, errUnreachable }, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorkspace(t, "shop")
			fake := runtime.NewFake()
			deps := fakeDeps(t, fake)
			started, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
			if err != nil {
				t.Fatalf("StartMate: %v", err)
			}
			runner := &process.FakeRunner{Handler: func(_ context.Context, spec process.Spec) (process.Result, error) {
				if strings.HasPrefix(strings.Join(spec.Args, " "), "session list --json") {
					out, err := tc.list(started.Session)
					return process.Result{Stdout: out}, err
				}
				t.Errorf("StopMate ran %v; nothing beyond `session list` can answer here", spec.Args)
				return process.Result{}, errors.New("unexpected herdr command")
			}}
			deps.Runtime = runtime.NewHerdr(runner)

			res, err := spawn.StopMate(context.Background(), w, deps, "shop")
			if tc.wantErr {
				if !errors.Is(err, errUnreachable) {
					t.Fatalf("err = %v, want the session list failure", err)
				}
				if res.Confirmed {
					t.Fatal("a failed stop reported Confirmed")
				}
				assertMateNotReleased(t, w, fake, started)
				return
			}
			if err != nil {
				t.Fatalf("StopMate: %v", err)
			}
			if res.Confirmed || !res.AlreadyGone {
				t.Fatalf("confirmed=%v alreadyGone=%v, want an unconfirmed already-gone stop", res.Confirmed, res.AlreadyGone)
			}
		})
	}
}
