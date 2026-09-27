package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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

// A key file opts this console into remote classification. Its contents are
// kept in this client only; no key is added to spawned agents' environments.
func consoleNoticeClient(env func(string) string) (*notice.Client, error) {
	path := env("MATE_JEV_API_KEY_FILE")
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
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
