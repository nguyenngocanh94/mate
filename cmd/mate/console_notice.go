package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

type noticeClassifier interface {
	Classify(context.Context, string) (notice.Result, error)
}

// The workspace's `.mate/.env` opts this console into remote classification:
// `MATE_JEV_API_KEY_FILE` names a key file, `~/` or relative to the
// workspace root, and with it the action is on unless `MATE_JEV=off` (the
// values are jevSettings'). The key is read here and stays in this client;
// no key is added to spawned agents' environments. The process environment
// is not consulted, so one workspace's switch never reaches another's
// console.
func consoleNoticeClient(ws *store.Workspace) (*notice.Client, error) {
	env, mode, err := jevSettings(ws)
	if err != nil || mode == jevOff {
		return nil, err
	}
	return jevClient(ws, env)
}

// jevMode is what `MATE_JEV` turns on.
type jevMode int

const (
	jevOff jevMode = iota
	// jevNotice is the console's Explain notice action alone, the panes
	// read by the fixture observer.
	jevNotice
	// jevObserver is that action and the observer chain.
	jevObserver
)

// jevSettings reads `.mate/.env` and its MATE_JEV switch:
//
//	off (false, 0, no)               nothing goes to Jev
//	fixture                          the notice action; panes read by the fixture observer
//	on, observer (true, 1, yes)      the notice action and the observer chain
//	unset, with MATE_JEV_API_KEY_FILE  the same as fixture
//	unset, no key file named         off, silently: a workspace that never set Jev up
//
// A key alone keeps the panes on the fixture observer until the day-long
// live run's evidence is committed (docs/plans/jev-observer-2026-10-08.md
// section 6, PR 3); the chain is asked for by name.
//
// Any other value is a mistake, said in the error, and Jev stays off.
func jevSettings(ws *store.Workspace) (map[string]string, jevMode, error) {
	env, err := ws.LoadEnv()
	if err != nil {
		return nil, jevOff, fmt.Errorf("Jev disabled: %w", err)
	}
	switch value := strings.ToLower(strings.TrimSpace(env["MATE_JEV"])); value {
	case "":
		if env["MATE_JEV_API_KEY_FILE"] == "" {
			return env, jevOff, nil
		}
		return env, jevNotice, nil
	case "off", "false", "0", "no":
		return env, jevOff, nil
	case "fixture":
		return env, jevNotice, nil
	case "on", "observer", "true", "1", "yes":
		return env, jevObserver, nil
	}
	return nil, jevOff, errors.New("Jev disabled: MATE_JEV must be on, observer, fixture or off")
}

// jevClient is the Jev client over the key file `.mate/.env` names.
func jevClient(ws *store.Workspace, env map[string]string) (*notice.Client, error) {
	path := env["MATE_JEV_API_KEY_FILE"]
	if path == "" {
		return nil, errors.New("Jev disabled: MATE_JEV_API_KEY_FILE is not set in .mate/.env")
	}
	f, err := os.Open(keyFilePath(ws.Root(), path))
	if err != nil {
		return nil, errors.New("Jev disabled: cannot read MATE_JEV_API_KEY_FILE")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	key := strings.TrimSpace(string(b))
	if err != nil || len(b) > 4096 || key == "" || strings.ContainsAny(key, "\r\n\t ") {
		return nil, errors.New("Jev disabled: invalid API key file")
	}
	return notice.New(key), nil
}

// keyFilePath expands a leading `~/` and anchors a relative path at the
// workspace root, the directory `.mate/.env` describes.
func keyFilePath(root, path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

// This wrapper receives no observer health and writes no state. One explicit
// action produces one capture and one request, bounded by a shared deadline.
func consoleNoticeAction(ws *store.Workspace, deps spawn.Deps, classifier noticeClassifier, next console.ActionFunc) console.ActionFunc {
	return func(ctx context.Context, req console.ActionRequest) (string, error) {
		if req.Action != console.ActionNotice {
			return next(ctx, req)
		}
		if classifier == nil {
			return "", errors.New("Jev is not configured")
		}
		ctx, cancel := context.WithTimeout(ctx, notice.Timeout)
		defer cancel()
		// Do not share Workspace's config cache with the periodic loader.
		fresh, err := store.Open(ws.Root())
		if err != nil {
			return "", err
		}
		var handle runtime.AgentHandle
		var kind harness.Kind
		switch {
		case req.TargetKind == "mate" && req.Crew == "":
			handle, kind, err = spawn.MateHandle(ctx, fresh, deps, req.Target)
		case req.TargetKind == "crew" && req.Crew != "":
			handle, kind, err = spawn.CrewHandle(ctx, fresh, deps, req.Target, req.Crew)
		default:
			return "", errors.New("notice explanation needs a Mate or Crew")
		}
		if err != nil {
			return "", err
		}
		screens, err := screenOf(deps, kind)
		if err != nil {
			return "", err
		}
		screen, err := deps.Runtime.ReadAgentStyled(ctx, handle, screens.ReadSource(), notice.MaxLines)
		if err != nil {
			return "", errors.New("cannot read terminal for notice explanation")
		}
		captured := time.Now()
		result, err := classifier.Classify(ctx, screen)
		if err != nil {
			return "", err
		}
		label, ok := noticeLabels[result.Label]
		if !ok {
			return "", errors.New("unrecognized notice classification")
		}
		return fmt.Sprintf("Jev suggests: %s\n\nAdvisory only; inspect the current pane before acting.\nCaptured: %s\n\nModel confidence: %.2f (not measured accuracy)\nModel: %s\nInput tokens: %d\n\nNo automatic action is taken.",
			label, captured.Format(time.RFC3339), result.Confidence, notice.Model, result.InputTokens), nil
	}
}

var noticeLabels = map[string]string{
	"quota_warning":       "quota warning (may still continue)",
	"quota_exhausted":     "quota exhausted (requests appear blocked)",
	"auth_required":       "authentication required",
	"permission_required": "permission or trust requested",
	"update_notice":       "software update notice",
	"none":                "no current notice identified",
	"unknown":             "unclear notice; inspect the pane",
}
