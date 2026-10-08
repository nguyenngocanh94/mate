// Package jev is the screen.Observer that asks Jev (TypeSafe, jev-1.13.0)
// what a pane shows: one request of three questions per snapshot, the ones
// measured in docs/evidence/jev-observer-2026-10-08.md (request.go), sent
// through internal/notice's client so the endpoint, key handling, timeout
// and no-redirect, no-retry rules are that client's.
//
// Jev answers labels with probabilities and no text: an Observation from
// it has no Draft, no Highlight and no Startup. It is never used alone;
// internal/screen/chain puts it in front of the fixture observer.
package jev

import (
	"context"
	"errors"
	"fmt"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/screen"
)

// Source is the Observation.Source this observer writes.
const Source = "jev"

// Observer is the Jev observer.
type Observer struct {
	client *notice.Client
}

// New returns the Jev observer over client.
func New(client *notice.Client) screen.Observer { return &Observer{client: client} }

// Observe asks Jev about one pane snapshot. The deadline is the caller's,
// capped at notice.Timeout; nothing is retried. A failed request or an
// answer that does not validate is an error, never an Observation.
func (o *Observer) Observe(ctx context.Context, _ harness.ScreenProfile, pane string) (screen.Observation, error) {
	ctx, cancel := context.WithTimeout(ctx, notice.Timeout)
	defer cancel()
	data, err := o.client.Ask(ctx, pane, func(redacted string) ([]byte, error) {
		prepared := Prepare(redacted, "")
		if prepared == "" {
			return nil, errors.New("no terminal text to observe")
		}
		return RequestBody(prepared)
	})
	if err != nil {
		return screen.Observation{}, err
	}
	resp, err := Parse(data)
	if err != nil {
		return screen.Observation{}, fmt.Errorf("invalid Jev answer: %w", err)
	}
	return observation(resp), nil
}

// observation is what one validated answer says about the pane.
// Confidence is the lower of the composer and dialog answers' confidences,
// the two that decide what a caller does; Reason names both answers, so a
// caller can say which one fell short.
func observation(resp Response) screen.Observation {
	c, d, n := resp.Answers[AxisComposer], resp.Answers[AxisDialog], resp.Answers[AxisNotice]
	obs := screen.Observation{
		Composer:   composers[c.Choice],
		Dialog:     dialogs[d.Choice],
		Highlight:  -1,
		Confidence: min(c.Confidence, d.Confidence),
		Source:     Source,
		Reason:     fmt.Sprintf("composer %s %.2f, dialog %s %.2f", c.Choice, c.Confidence, d.Choice, d.Confidence),
	}
	if n.Choice != "none" {
		obs.Notice = n.Choice
	}
	return obs
}

// composers maps the composer question's labels; Parse admits no other.
var composers = map[string]screen.ComposerState{
	"empty":   screen.ComposerEmpty,
	"draft":   screen.ComposerDraft,
	"busy":    screen.ComposerBusy,
	"none":    screen.ComposerUnknown,
	"unknown": screen.ComposerUnknown,
}

// dialogs maps the dialog question's labels; Parse admits no other.
var dialogs = map[string]screen.DialogKind{
	"none":         screen.DialogNone,
	"trust":        screen.DialogTrust,
	"update":       screen.DialogUpdate,
	"hooks_review": screen.DialogHooksReview,
	"permission":   screen.DialogPermission,
	"other":        screen.DialogOther,
	"unknown":      screen.DialogUnknown,
}
