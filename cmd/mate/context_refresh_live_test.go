package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

func TestLiveMateContextRefreshPreservesHeldQuestion(t *testing.T) {
	requireConsoleLive(t)
	session, home := consoleLiveLab(t)
	w, err := store.Init(liveWorkspaceRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	consoleUseLabSession(t, w, session)
	w, err = store.Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ProjectDoc("shop"), []byte("# Shop\nA planning project; no repository yet.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = runtime.NewMemoryNameRegistry()
	deps := spawn.Deps{Runtime: rt, Names: rt.Names, ConfigHome: home, Binary: consoleBinaryPath(t)}
	marker := filepath.Join(home, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = spawn.StopMate(c, w, deps, "shop")
	})
	started, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Harness: harness.KindClaude})
	if err != nil {
		t.Fatal(err)
	}
	ask := func(prompt string) string {
		t.Helper()
		_, offset, err := w.ReadSent("shop", 0)
		if err != nil {
			t.Fatal(err)
		}
		h, _, err := spawn.MateHandle(ctx, w, deps, "shop")
		if err != nil {
			t.Fatal(err)
		}
		if err := rt.PromptAgent(ctx, h, prompt); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			entries, next, err := w.ReadSent("shop", offset)
			if err != nil {
				t.Fatal(err)
			}
			offset = next
			for _, e := range entries {
				if e.Source == store.SourceMate && e.Target == store.SourceUser {
					return e.Text
				}
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
		screen, _ := rt.ReadAgent(ctx, h, 60)
		t.Fatalf("no Stop hook after prompt; pane: %v", screen)
		return ""
	}
	answer := ask(`We need to choose a color before any work. Ask me exactly "Ship blue or green?" and keep that question pending until I answer. Do not create a repository or spawn a crew.`)
	t.Logf("before refresh: %s", answer)
	if !strings.Contains(answer, "Ship blue or green?") {
		t.Fatal("question was not asked verbatim")
	}
	// Give the pane time to draw the idle composer after Stop. The refresh
	// itself still refuses busy/unknown/pending states and never infers a turn end.
	time.Sleep(2 * time.Second)
	changed, err := contextRefresh(ctx, w, deps, "shop", false)
	if err != nil || !changed {
		t.Fatalf("refresh: %v %v", changed, err)
	}
	meta, _ := w.ReadMateMeta("shop")
	if meta[spawn.MetaSessionID] == started.SessionID || meta[spawn.MetaResumed] == "true" {
		t.Fatal("old transcript resumed")
	}
	after := ask("What exact question are you waiting for me to answer?")
	t.Logf("after refresh: %s", after)
	if !strings.Contains(after, "Ship blue or green?") {
		t.Fatal("fresh recall lost the held question")
	}
	// The streaming ledger defers the trailing assistant group until a newer
	// message id appears; close that observation without reading more files.
	_ = ask("Thanks. End your turn without changing any files.")
	handle, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	var usage db.ContextUsage
	var known bool
	deadline := time.Now().Add(15 * time.Second)
	for {
		if err := timeline.New(w, handle, timeline.Deps{}).Ingest(ctx); err != nil {
			t.Fatal(err)
		}
		usage, known, err = handle.LatestContext(ctx, timeline.MateActorID("shop"), meta[spawn.MetaSessionID])
		if err != nil {
			t.Fatal(err)
		}
		if known || time.Now().After(deadline) {
			break
		}
		// Stop hooks can reach sent.log before Claude flushes the JSONL buffer.
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Logf("fresh-session measured context: known=%v tokens=%d model=%s workspace=%s", known, usage.Tokens, usage.Model, w.Root())
	if !known || usage.Tokens >= 150000 {
		t.Fatal("fresh session did not establish a bounded observed context")
	}
}
