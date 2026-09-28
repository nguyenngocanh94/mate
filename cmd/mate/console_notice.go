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
// `MATE_JEV=on` turns the action on for this workspace only, and
// `MATE_JEV_API_KEY_FILE` names a key file, `~/` or relative to the
// workspace root. The key is read here and stays in this client; no key is
// added to spawned agents' environments. The process environment is not
// consulted, so one workspace's switch never reaches another's console.
func consoleNoticeClient(ws *store.Workspace) (*notice.Client, error) {
	env, err := ws.LoadEnv()
	if err != nil {
		return nil, fmt.Errorf("Jev disabled: %w", err)
	}
	on, err := envSwitch(env["MATE_JEV"])
	if err != nil {
		return nil, fmt.Errorf("Jev disabled: MATE_JEV %w", err)
	}
	if !on {
		return nil, nil
	}
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

// envSwitch reads an on/off setting; unset is off, anything else is a
// mistake worth a message rather than a silent default.
func envSwitch(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off", "false", "0", "no":
		return false, nil
	case "on", "true", "1", "yes":
		return true, nil
	}
	return false, errors.New("must be on or off")
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
		switch {
		case req.TargetKind == "mate" && req.Crew == "":
			handle, _, err = spawn.MateHandle(ctx, fresh, deps, req.Target)
		case req.TargetKind == "crew" && req.Crew != "":
			handle, _, err = spawn.CrewHandle(ctx, fresh, deps, req.Target, req.Crew)
		default:
			return "", errors.New("notice explanation needs a Mate or Crew")
		}
		if err != nil {
			return "", err
		}
		screen, err := deps.Runtime.ReadAgentStyled(ctx, handle, notice.MaxLines)
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
