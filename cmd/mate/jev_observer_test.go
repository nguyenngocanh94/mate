package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/chain"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// MATE_JEV=observer in `.mate/.env`, with a readable key and a threshold in
// [0, 1], is the observer chain; anything else is the fixture observer
// (nil), with one line saying why when the setting is a mistake.
func TestConfiguredObserver(t *testing.T) {
	ws := noticeWorkspace(t)
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		env     string
		chained bool
		err     string
	}{
		{"", false, ""},
		{"MATE_JEV=off\nMATE_JEV_API_KEY_FILE=" + key + "\n", false, ""},
		{"MATE_JEV=on\nMATE_JEV_API_KEY_FILE=" + key + "\n", false, ""},
		{"MATE_JEV=observer\nMATE_JEV_API_KEY_FILE=" + key + "\n", true, ""},
		{"MATE_JEV=Observer\nMATE_JEV_API_KEY_FILE=" + key + "\nMATE_JEV_THRESHOLD=0.9\n", true, ""},
		{"MATE_JEV=observer\nMATE_JEV_API_KEY_FILE=" + key + "\nMATE_JEV_THRESHOLD=0\n", true, ""},
		{"MATE_JEV=observer\nMATE_JEV_API_KEY_FILE=" + key + "\nMATE_JEV_THRESHOLD=1\n", true, ""},
		{"MATE_JEV=observer\n", false, "Jev disabled: MATE_JEV_API_KEY_FILE is not set in .mate/.env"},
		{"MATE_JEV=observer\nMATE_JEV_API_KEY_FILE=" + key + "\nMATE_JEV_THRESHOLD=1.5\n", false,
			"Jev observer disabled: MATE_JEV_THRESHOLD must be a number from 0 to 1"},
		{"MATE_JEV=observer\nMATE_JEV_API_KEY_FILE=" + key + "\nMATE_JEV_THRESHOLD=high\n", false,
			"Jev observer disabled: MATE_JEV_THRESHOLD must be a number from 0 to 1"},
		{"MATE_JEV=observer\nMATE_JEV_API_KEY_FILE=" + key + "\nMATE_JEV_THRESHOLD=-0.1\n", false,
			"Jev observer disabled: MATE_JEV_THRESHOLD must be a number from 0 to 1"},
		{"MATE_JEV=maybe\nMATE_JEV_API_KEY_FILE=" + key + "\n", false, "Jev disabled: MATE_JEV must be on, observer or off"},
	} {
		writeEnv(t, ws, tc.env)
		observer, err := configuredObserver(ws)
		if _, ok := observer.(*chain.Chain); ok != tc.chained || (observer != nil) != tc.chained {
			t.Errorf("%q: observer %T, want the chain %v", tc.env, observer, tc.chained)
		}
		if got := errText(err); got != tc.err {
			t.Errorf("%q: error %q, want %q", tc.env, got, tc.err)
		}
	}
}

// MATE_JEV=observer keeps the console's notice action, as MATE_JEV=on does.
func TestObserverModeKeepsTheNoticeAction(t *testing.T) {
	ws := noticeWorkspace(t)
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, ws, "MATE_JEV=observer\nMATE_JEV_API_KEY_FILE="+key+"\n")
	if c, err := consoleNoticeClient(ws); c == nil || err != nil {
		t.Fatalf("notice client %v, %v", c, err)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// A command's deps carry the configured observer; a mistake in the setting
// is one line on stderr and the fixture observer.
func TestLiveDepsCarryTheConfiguredObserver(t *testing.T) {
	ws := noticeWorkspace(t)
	var stderr bytes.Buffer
	if deps := liveDeps(ws, &stderr); deps.Observer != nil || stderr.Len() != 0 {
		t.Fatalf("unset: observer %T, stderr %q", deps.Observer, stderr.String())
	}
	writeEnv(t, ws, "MATE_JEV=observer\n")
	if deps := liveDeps(ws, &stderr); deps.Observer != nil || stderr.String() != "Jev disabled: MATE_JEV_API_KEY_FILE is not set in .mate/.env\n" {
		t.Fatalf("no key: observer %T, stderr %q", deps.Observer, stderr.String())
	}
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	writeEnv(t, ws, "MATE_JEV=observer\nMATE_JEV_API_KEY_FILE="+key+"\n")
	if deps := liveDeps(ws, &stderr); deps.Observer == nil || stderr.Len() != 0 {
		t.Fatalf("observer: observer %T, stderr %q", deps.Observer, stderr.String())
	}
}

// seesBusy reads every pane as a turn in flight, whatever is on it.
type seesBusy struct{ calls int }

func (o *seesBusy) Observe(context.Context, harness.ScreenProfile, string) (screen.Observation, error) {
	o.calls++
	return screen.Observation{Composer: screen.ComposerBusy, Evidence: "observer says busy", Dialog: screen.DialogNone,
		Highlight: -1, Confidence: 1, Source: "jev"}, nil
}

// The deps' observer is the one `mate send`, the console's reply and the
// console's watcher read the pane through: each refuses or reports busy on
// a pane that shows an empty composer, because the observer says busy.
func TestCallSitesReadThroughTheDepsObserver(t *testing.T) {
	t.Run("mate send", func(t *testing.T) {
		w := liveCrewWorkspace(t, "shop")
		rt := runtime.NewFake()
		deps := fakeSpawnDeps(t, rt)
		res := spawnFakeCrew(t, w, deps, "shop", "k3")
		rt.SetReadOutput(runtime.AgentHandle{Session: runtime.SessionHandle{Name: res.Session}, Name: res.Agent}, codexEmptyScreen)
		observer := &seesBusy{}
		deps.Observer = observer
		_, err := sendToCrew(context.Background(), w, deps, "shop", "k3", "A", store.SourceMate, send.Options{})
		if !errors.Is(err, send.ErrAgentBusy) || observer.calls != 1 {
			t.Fatalf("err %v after %d observations, want busy from the observer", err, observer.calls)
		}
	})
	t.Run("console reply", func(t *testing.T) {
		f := newBoxFixture(t)
		observer := &seesBusy{}
		f.deps.Observer = observer
		_, err := boxReplyAction(context.Background(), f.ws, f.deps, console.ActionRequest{
			Action: console.ActionReply, Target: "shop", TargetKind: "project", Crew: "k3", Input: "A"})
		if !errors.Is(err, send.ErrAgentBusy) || observer.calls != 1 {
			t.Fatalf("err %v after %d observations, want busy from the observer", err, observer.calls)
		}
	})
	t.Run("console watcher", func(t *testing.T) {
		w, deps := consoleFixture(t, "shop")
		res := spawnFakeCrew(t, w, deps, "shop", "k3")
		deps.Runtime.(*runtime.Fake).SetReadOutput(spawnedHandle(res), codexEmptyScreen)
		observer := &seesBusy{}
		deps.Observer = observer
		watcher, err := consoleWatcher(w.Root(), deps)
		if err != nil {
			t.Fatal(err)
		}
		if err := watcher.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		if h, ok := watcher.Health("shop", "k3"); !ok || h.Composer != send.StateBusy {
			t.Fatalf("health %+v, want the observer's busy", h)
		}
	})
}
