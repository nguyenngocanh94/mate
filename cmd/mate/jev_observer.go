package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/chain"
	"github.com/nguyenngocanh94/mate/internal/screen/fixture"
	"github.com/nguyenngocanh94/mate/internal/screen/jev"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// defaultJevThreshold is the confidence below which the chain uses the
// fixture's reading (docs/plans/jev-observer-2026-10-08.md section 4.1).
const defaultJevThreshold = 0.85

// configuredObserver is the screen.Observer `.mate/.env` asks for. When
// `MATE_JEV` is `on` or `observer` and a readable key is set (jevSettings),
// it is the chain - Jev first, the fixture observer when Jev
// is unsure (internal/screen/chain) - logging every request to
// `.mate/jev.log`, with the threshold `MATE_JEV_THRESHOLD` sets. Otherwise,
// and on any configuration problem, it is nil, which every Deps reads as the
// fixture observer; the problem is the error, one line for the caller to
// show.
func configuredObserver(ws *store.Workspace) (screen.Observer, error) {
	env, mode, err := jevSettings(ws)
	if err != nil || mode != jevObserver {
		return nil, err
	}
	threshold, err := jevThreshold(env["MATE_JEV_THRESHOLD"])
	if err != nil {
		return nil, fmt.Errorf("Jev observer disabled: MATE_JEV_THRESHOLD %w", err)
	}
	client, err := jevClient(ws, env)
	if err != nil {
		return nil, err
	}
	return chain.New(jev.New(client), fixture.New(), threshold, chain.WithLog(ws.AppendJevLog)), nil
}

// jevThreshold reads MATE_JEV_THRESHOLD: unset is the default, anything
// else a number from 0 to 1.
func jevThreshold(value string) (float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultJevThreshold, nil
	}
	t, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(t) || t < 0 || t > 1 {
		return 0, errors.New("must be a number from 0 to 1")
	}
	return t, nil
}

// baseDeps is spawn.LiveDeps with this binary's tools, so a Mate's manual
// installs their skills beside its own. Commands that never read a pane, and
// the console (which wires its own observer once for the whole run), use it
// directly.
func baseDeps() spawn.Deps {
	deps := spawn.LiveDeps(harnesses)
	deps.Tools = tools
	return deps
}

// liveDeps is baseDeps plus the observer `.mate/.env` asks for, for a command
// that reads a pane (send, state, a start's settle). A Jev configuration
// problem is one line on stderr, and the command goes on with the fixture
// observer.
func liveDeps(w *store.Workspace, stderr io.Writer) spawn.Deps {
	deps := baseDeps()
	observer, err := configuredObserver(w)
	if err != nil {
		fmt.Fprintln(stderr, err)
	}
	deps.Observer = observer
	return deps
}
