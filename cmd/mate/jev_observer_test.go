package main

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/chain"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// The MATE_JEV value table (jevSettings): with a readable key, on and
// observer are the observer chain and the notice action; unset and fixture
// keep the action and read panes with the fixture observer (nil) until the
// day-long run's evidence is committed; off is neither. Without a key, or on any mistake, the observer is the fixture's,
// with one line saying why when the setting asked for Jev.
func TestConfiguredObserver(t *testing.T) {
	ws := noticeWorkspace(t)
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyLine := "MATE_JEV_API_KEY_FILE=" + key + "\n"
	const (
		noKey     = "Jev disabled: MATE_JEV_API_KEY_FILE is not set in .mate/.env"
		badSwitch = "Jev disabled: MATE_JEV must be on, observer, fixture or off"
		badLimit  = "Jev observer disabled: MATE_JEV_THRESHOLD must be a number from 0 to 1"
	)
	for _, tc := range []struct {
		env       string
		chained   bool
		noticed   bool
		err       string
		noticeErr string
	}{
		{env: ""},
		{env: keyLine, noticed: true},
		{env: "MATE_JEV_API_KEY_FILE=" + filepath.Join(t.TempDir(), "missing") + "\n",
			noticeErr: "Jev disabled: cannot read MATE_JEV_API_KEY_FILE"},
		{env: "MATE_JEV=on\nMATE_JEV_API_KEY_FILE=" + filepath.Join(t.TempDir(), "missing") + "\n",
			err: "Jev disabled: cannot read MATE_JEV_API_KEY_FILE", noticeErr: "Jev disabled: cannot read MATE_JEV_API_KEY_FILE"},
		{env: "MATE_JEV=off\n" + keyLine},
		{env: "MATE_JEV=OFF\n" + keyLine},
		{env: "MATE_JEV=fixture\n" + keyLine, noticed: true},
		{env: "MATE_JEV=fixture\n", noticeErr: noKey},
		{env: "MATE_JEV=on\n" + keyLine, chained: true, noticed: true},
		{env: "MATE_JEV=on\n", err: noKey, noticeErr: noKey},
		{env: "MATE_JEV=observer\n" + keyLine, chained: true, noticed: true},
		{env: "MATE_JEV=Observer\n" + keyLine + "MATE_JEV_THRESHOLD=0.9\n", chained: true, noticed: true},
		{env: "MATE_JEV=observer\n" + keyLine + "MATE_JEV_THRESHOLD=0\n", chained: true, noticed: true},
		{env: keyLine + "MATE_JEV_THRESHOLD=1\n", noticed: true},
		{env: "MATE_JEV=on\n" + keyLine + "MATE_JEV_THRESHOLD=1\n", chained: true, noticed: true},
		{env: "MATE_JEV=observer\n", err: noKey, noticeErr: noKey},
		{env: "MATE_JEV=observer\n" + keyLine + "MATE_JEV_THRESHOLD=1.5\n", noticed: true, err: badLimit},
		{env: keyLine + "MATE_JEV_THRESHOLD=high\n", noticed: true},
		{env: "MATE_JEV=on\n" + keyLine + "MATE_JEV_THRESHOLD=high\n", noticed: true, err: badLimit},
		{env: "MATE_JEV=observer\n" + keyLine + "MATE_JEV_THRESHOLD=-0.1\n", noticed: true, err: badLimit},
		{env: "MATE_JEV=maybe\n" + keyLine, err: badSwitch, noticeErr: badSwitch},
	} {
		writeEnv(t, ws, tc.env)
		observer, err := configuredObserver(ws)
		if _, ok := observer.(*chain.Chain); ok != tc.chained || (observer != nil) != tc.chained {
			t.Errorf("%q: observer %T, want the chain %v", tc.env, observer, tc.chained)
		}
		if got := errText(err); got != tc.err {
			t.Errorf("%q: error %q, want %q", tc.env, got, tc.err)
		}
		client, err := consoleNoticeClient(ws)
		if (client != nil) != tc.noticed {
			t.Errorf("%q: notice client %v, want one %v", tc.env, client, tc.noticed)
		}
		if got := errText(err); got != tc.noticeErr {
			t.Errorf("%q: notice error %q, want %q", tc.env, got, tc.noticeErr)
		}
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
	t.Run("console outbox", func(t *testing.T) {
		f := newBoxFixture(t)
		observer := &seesBusy{}
		f.deps.Observer = observer
		typedBefore := len(f.rt.SentText)
		s := consoleOutbox(f.ws, f.deps)
		if _, err := s.Enqueue("shop", outbox.Request{Source: store.OutboxSourceAssign, Key: "crews/k3.status@0", Text: "resolve: A"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Drain(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(f.rt.SentText) != typedBefore || observer.calls != 1 {
			t.Fatalf("typed %v after %d observations, want the observer's busy to hold the line", f.rt.SentText[typedBefore:], observer.calls)
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
		// The watcher asks the observer in the background once the pane
		// has held still for a poll, and uses its answer on the next round.
		for range 2 {
			if err := watcher.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		watcher.AwaitObserver()
		if err := watcher.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		if h, ok := watcher.Health("shop", "k3"); !ok || h.Composer != send.StateBusy {
			t.Fatalf("health %+v, want the observer's busy", h)
		}
	})
}

// callersOf maps each function in this package's non-test files to the
// package-level calls it makes, as "pkg.Name" or "Name".
func callersOf(t *testing.T) map[string]map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			calls := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch f := call.Fun.(type) {
				case *ast.Ident:
					calls[f.Name] = true
				case *ast.SelectorExpr:
					if x, ok := f.X.(*ast.Ident); ok {
						calls[x.Name+"."+f.Sel.Name] = true
					}
				}
				return true
			})
			out[fn.Name.Name] = calls
		}
	}
	return out
}

// Every command that reads a pane builds its deps with liveDeps, so it
// observes through what `.mate/.env` configures. baseDeps (spawn.LiveDeps
// plus this binary's tools, no observer) is left only where no pane is
// read (status, stop, peek, merge, migrate, events, reindex), and in the
// console, which sets the observer itself. spawn.LiveDeps is called by
// baseDeps alone.
func TestCommandsThatReadAPaneUseTheConfiguredObserver(t *testing.T) {
	calls := callersOf(t)
	for _, fn := range []string{"cmdSend", "cmdBriefAppend", "cmdMateStart", "cmdMateRefresh", "cmdMateStop", "cmdProjectRemove",
		"cmdCrewSpawn", "cmdCrewRelaunch", "cmdReview", "cmdState", "cmdPeek", "runPRWatch"} {
		if !calls[fn]["liveDeps"] {
			t.Errorf("%s does not build its deps with liveDeps", fn)
		}
	}
	readsNoPane := map[string]bool{"liveDeps": true, "runConsole": true, "cmdMateStatus": true, "cmdCrewStop": true,
		"cmdMerge": true, "cmdMigrate": true, "cmdEvents": true, "cmdReindex": true}
	for fn, c := range calls {
		if c["spawn.LiveDeps"] && fn != "baseDeps" {
			t.Errorf("%s builds spawn.LiveDeps directly; go through baseDeps or liveDeps", fn)
		}
		if c["baseDeps"] && !readsNoPane[fn] {
			t.Errorf("%s builds baseDeps without the configured observer", fn)
		}
	}
	if !calls["runConsole"]["configuredObserver"] {
		t.Error("the console does not set the configured observer")
	}
}
