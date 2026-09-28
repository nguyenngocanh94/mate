package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

const defaultRefreshContext int64 = 150000
const refreshRetryAfter = 30 * time.Minute

// contextRefresh changes sessions only at a verified checkpoint. Queued work
// remains in the durable outbox. Ordinary restart retains its resume semantics.
func contextRefresh(ctx context.Context, w *store.Workspace, deps spawn.Deps, project string, automatic bool) (bool, error) {
	now := time.Now()
	if deps.Now != nil {
		now = deps.Now()
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return false, err
	}
	meta, err := w.ReadMateMeta(project)
	if err != nil {
		return false, err
	}
	session := meta[spawn.MetaSessionID]
	// Claude records both prompt and Stop hooks. Until equivalent activity
	// evidence exists for Codex, automatic refresh must not infer its quietness.
	if automatic && (cfg.Mate.RefreshContext < 0 || meta[spawn.MetaHarness] != string(harness.KindClaude) || !w.Auto(project) || w.Held(project)) {
		return false, nil
	}
	if session == "" {
		return false, nil
	}
	if automatic {
		quiet, err := refreshQuiet(w, project, now)
		if err != nil || !quiet {
			return false, err
		}
		handle, err := db.OpenRead(w)
		if err != nil {
			return false, nil
		}
		usage, known, err := handle.LatestContext(ctx, timeline.MateActorID(project), session)
		handle.Close()
		if err != nil {
			return false, err
		}
		limit := cfg.Mate.RefreshContext
		if limit == 0 {
			limit = defaultRefreshContext
		}
		if !known || usage.Tokens < limit {
			return false, nil
		}
	}
	release, locked, err := w.TryMateMaintenance(project, true)
	if err != nil || !locked {
		return false, err
	}
	defer release()
	if err := refreshQueueEmpty(w, project); err != nil {
		if automatic {
			return false, nil
		}
		return false, err
	}
	requestPath, err := refreshMeta(w, project, "refresh-request")
	if err != nil {
		return false, err
	}
	prior, err := store.ReadMeta(requestPath)
	if err != nil {
		return false, err
	}
	if automatic {
		if at, err := time.Parse(time.RFC3339Nano, prior["attempted_at"]); err == nil && now.Sub(at) < refreshRetryAfter {
			return false, nil
		}
	}
	// Busy/pending is a deferral, not a failed stow: do not spend the retry
	// cooldown before an LLM request was even possible. Stow rechecks this.
	h, kind, err := spawn.MateHandle(ctx, w, deps, project)
	if err != nil {
		if automatic {
			return false, nil
		}
		return false, err
	}
	screen, err := deps.Runtime.ReadAgentStyled(ctx, h, send.DefaultLines)
	if err != nil {
		return false, err
	}
	composer, err := send.ClassifyComposer(kind, screen)
	if err != nil {
		return false, err
	}
	if composer.State != send.StateEmpty {
		if automatic {
			return false, nil
		}
		return false, fmt.Errorf("refresh postponed: Mate composer is %s", composer.State)
	}
	nonce := rand.Text()
	req := map[string]string{"nonce": nonce, "session": session, "attempted_at": now.UTC().Format(time.RFC3339Nano)}
	if err := store.WriteMeta(requestPath, req); err != nil {
		return false, err
	}
	_, offset, err := w.ReadSent(project, 0)
	if err != nil {
		return false, err
	}
	binary := deps.Binary
	if binary == "" {
		binary, err = os.Executable()
		if err != nil {
			return false, err
		}
	}
	line := memory.StowLine + "; before ending, preserve unresolved questions verbatim, promises, decisions, active crews and next actions, then run " + shellWord(binary) + " checkpoint " + shellWord(project) + " " + nonce
	stowed, err := consoleOutbox(w, deps).Stow(ctx, project, outbox.StowOptions{RequireEmpty: true, RequireCompletion: true, Text: line})
	if err != nil {
		return false, err
	}
	if !stowed.Stowed {
		return false, fmt.Errorf("refresh postponed: %s", stowed.Outcome())
	}
	if err := verifyCheckpoint(w, project, nonce, session); err != nil {
		return false, err
	}
	// A captain prompt during stow takes precedence, even if it has already
	// been answered. Do not discard a conversation that changed since stow.
	entries, _, err := w.ReadSent(project, offset)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Source == store.SourceUser && e.Target == store.TargetMate {
			return false, fmt.Errorf("refresh postponed: captain spoke during stow")
		}
	}
	if err := refreshQueueEmpty(w, project); err != nil {
		return false, err
	}
	current, err := w.ReadMateMeta(project)
	if err != nil {
		return false, err
	}
	if current[spawn.MetaSessionID] != session {
		return false, fmt.Errorf("refresh postponed: Mate session changed")
	}
	if automatic && (!w.Auto(project) || w.Held(project)) {
		return false, fmt.Errorf("refresh postponed: captain took manual control")
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err := w.ArchiveMateSession(project, current); err != nil {
		return false, err
	}
	if _, err := spawn.StopMate(ctx, w, deps, project); err != nil {
		return false, err
	}
	if err := w.FreezeMateSession(project, current); err != nil {
		return false, fmt.Errorf("Mate stopped with checkpoint saved; archive failed: %w", err)
	}
	_, err = spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: project, Harness: harness.Kind(meta[spawn.MetaHarness]), Fresh: true})
	if err != nil {
		return false, fmt.Errorf("checkpoint saved; fresh start failed: %w", err)
	}
	err = w.AppendSent(project, store.SentEntry{Source: store.SourceApp, Target: store.TargetMate, Text: "context refreshed from checkpoint; previous session " + session})
	return true, err
}

func shellWord(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func refreshQueueEmpty(w *store.Workspace, project string) error {
	items, err := w.ReadOutbox(project)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Queued() {
			return fmt.Errorf("refresh postponed: %s item %s is queued", item.Source, strconv.FormatInt(item.ID, 10))
		}
	}
	return nil
}

func refreshQuiet(w *store.Workspace, project string, now time.Time) (bool, error) {
	entries, _, err := w.ReadSent(project, 0)
	if err != nil {
		return false, err
	}
	var asked, answered time.Time
	for _, e := range entries {
		if e.Source == store.SourceUser && e.Target == store.TargetMate {
			asked = e.Time
		}
		if e.Source == store.SourceMate && e.Target == store.SourceUser {
			answered = e.Time
		}
	}
	return !answered.IsZero() && !answered.Before(asked) && now.Sub(answered) >= store.QuietAfter, nil
}
