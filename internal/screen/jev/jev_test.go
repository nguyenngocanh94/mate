package jev

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/screen"
)

const moduleRoot = "../../.."

var cassette = filepath.Join(moduleRoot, "scripts", "jeveval", "testdata", "cassette")

// evidenceRow is one row of the per-screen table in
// docs/evidence/jev-observer-2026-10-08.md.
type evidenceRow struct {
	file, axis, det, jev, match string
}

// evidenceRows reads the evidence's per-screen table, keyed by the short
// file name it prints.
func evidenceRows(t *testing.T) map[string]evidenceRow {
	t.Helper()
	f, err := os.Open(filepath.Join(moduleRoot, "docs", "evidence", "jev-observer-2026-10-08.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	row := regexp.MustCompile("^\\| \\d+ \\| `([^`]+)` \\| (composer|dialog|notice) \\| (\\w+) \\| (\\w+) \\| (yes|no|unscored) \\| [\\d.]+ \\| \\d+ \\|$")
	out := map[string]evidenceRow{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := row.FindStringSubmatch(sc.Text()); m != nil {
			out[m[1]] = evidenceRow{file: m[1], axis: m[2], det: m[3], jev: m[4], match: m[5]}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// short is the name the evidence prints for a corpus file (jeveval's short).
func short(file string) string {
	for _, prefix := range []string{"internal/screen/fixture/testdata/screens/", "internal/harness/", "internal/notice/testdata/", "scripts/jeveval/testdata/screens/"} {
		if rest, ok := strings.CutPrefix(file, prefix); ok {
			return strings.Replace(rest, "/testdata/", "/", 1)
		}
	}
	return file
}

// onAxis is what an Observation says on one axis, in the evidence's words
// where they map one to one; Jev's composer "none" and "unknown" are both
// ComposerUnknown.
func onAxis(obs screen.Observation, axis string) string {
	switch axis {
	case "composer":
		return string(obs.Composer)
	case "dialog":
		return string(obs.Dialog)
	}
	if obs.Notice == "" {
		return "none"
	}
	return obs.Notice
}

// expected is the evidence's Jev label as onAxis reports it.
func expected(axis, label string) string {
	switch axis {
	case "composer":
		return string(composers[label])
	case "dialog":
		return string(dialogs[label])
	}
	return label
}

// agrees is the evidence's match rule (jeveval's accepts) on an
// Observation: false and unscored when the deterministic classifier
// abstained.
func agrees(obs screen.Observation, axis, det string) (match, scored bool) {
	want := map[string]map[string]string{
		"composer": {"empty": "empty", "pending": "draft", "busy": "busy", "unknown": "unknown"},
		"dialog":   {"ready": "none", "trust_dialog": "trust", "update_dialog": "update", "hooks_review": "hooks-review", "bypass_dialog": "other"},
	}
	if axis == "notice" {
		return onAxis(obs, axis) == det, true
	}
	w, ok := want[axis][det]
	if !ok {
		return false, false
	}
	return onAxis(obs, axis) == w, true
}

// The whole 2026-10-08 corpus, answered from its cassette with no network,
// observes as the evidence's per-screen table says Jev answered, and the
// totals are the evidence's: 65 of 75 scored screens match.
func TestObserveReplaysTheEvidence(t *testing.T) {
	replay := Replay{Dir: cassette}
	screens, err := replay.Screens(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	rows := evidenceRows(t)
	if len(screens) != 90 || len(rows) != 90 {
		t.Fatalf("%d recorded screens, %d evidence rows; want 90 of each", len(screens), len(rows))
	}
	observer := New(notice.New("test-key").WithTransport(replay))
	type tally struct{ matched, scored int }
	axes := map[string]*tally{"composer": {}, "dialog": {}, "notice": {}}
	for _, s := range screens {
		row, ok := rows[short(s.File)]
		if !ok {
			t.Fatalf("%s: not in the evidence table", s.File)
		}
		obs, err := observer.Observe(context.Background(), nil, s.Text)
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
		if got, want := onAxis(obs, row.axis), expected(row.axis, row.jev); got != want {
			t.Errorf("%s: %s %s, the evidence says Jev answered %s", row.file, row.axis, got, row.jev)
		}
		if obs.Source != Source || obs.Highlight != -1 || obs.Draft != "" || obs.Startup != "" {
			t.Errorf("%s: %+v carries what Jev cannot answer", row.file, obs)
		}
		match, scored := agrees(obs, row.axis, row.det)
		evidenceSays := map[string]string{"yes": "yes", "no": "no", "unscored": "unscored"}[row.match]
		got := "unscored"
		if scored {
			got = map[bool]string{true: "yes", false: "no"}[match]
			axes[row.axis].scored++
			if match {
				axes[row.axis].matched++
			}
		}
		if got != evidenceSays {
			t.Errorf("%s: match %s, the evidence says %s", row.file, got, evidenceSays)
		}
	}
	matched, scored := 0, 0
	for _, a := range axes {
		matched, scored = matched+a.matched, scored+a.scored
	}
	if matched != 65 || scored != 75 || *axes["notice"] != (tally{12, 12}) || *axes["dialog"] != (tally{31, 32}) || *axes["composer"] != (tally{22, 31}) {
		t.Fatalf("matched %d/%d (composer %v, dialog %v, notice %v), the evidence says 65/75 (22/31, 31/32, 12/12)",
			matched, scored, *axes["composer"], *axes["dialog"], *axes["notice"])
	}
}

// answer is a valid three-question response: each question's choice
// at its confidence, the rest of the probability on one other label.
func answer(t *testing.T, choices map[Axis]string, conf map[Axis]float64) []byte {
	t.Helper()
	answers := map[string]any{}
	for name, q := range questions {
		axis, choice := Axis(name), choices[Axis(name)]
		probs := map[string]float64{}
		rest := false
		for label := range q.Criteria {
			switch {
			case label == choice:
				probs[label] = conf[axis]
			case !rest:
				probs[label], rest = 1-conf[axis], true
			default:
				probs[label] = 0
			}
		}
		answers[name] = map[string]any{"type": "choice", "choice": choice, "confidence": conf[axis], "probabilities": probs}
	}
	data, err := json.Marshal(map[string]any{"model": notice.Model, "answers": answers,
		"usage": map[string]int{"input_tokens": 1, "output_tokens": 1}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// roundTrip is an http.RoundTripper from a function.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func respond(status int, body []byte) roundTrip {
	return func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	}
}

// One answer maps onto the Observation's vocabulary: Confidence is the
// weaker of the two deciding answers, a notice of none is no notice, and
// what Jev cannot answer (Deterministic among it) is left unknown.
func TestObserveMapsOneAnswer(t *testing.T) {
	data := answer(t, map[Axis]string{AxisComposer: "none", AxisDialog: "hooks_review", AxisNotice: "none"},
		map[Axis]float64{AxisComposer: 0.91, AxisDialog: 0.86, AxisNotice: 0.5})
	obs, err := New(notice.New("k").WithTransport(respond(200, data))).Observe(context.Background(), nil, "› hooks")
	if err != nil {
		t.Fatal(err)
	}
	want := screen.Observation{Composer: screen.ComposerUnknown, Deterministic: screen.ComposerUnknown, Dialog: screen.DialogHooksReview, Highlight: -1,
		Confidence: 0.86, Source: "jev", Reason: "composer none 0.91, dialog hooks_review 0.86"}
	if obs != want {
		t.Fatalf("got %+v\nwant %+v", obs, want)
	}
	data = answer(t, map[Axis]string{AxisComposer: "busy", AxisDialog: "none", AxisNotice: "quota_exhausted"},
		map[Axis]float64{AxisComposer: 0.7, AxisDialog: 0.9, AxisNotice: 0.95})
	obs, err = New(notice.New("k").WithTransport(respond(200, data))).Observe(context.Background(), nil, "• Working")
	if err != nil || obs.Composer != screen.ComposerBusy || obs.Notice != "quota_exhausted" || obs.Confidence != 0.7 {
		t.Fatalf("got %+v, %v", obs, err)
	}
}

// A request that fails, an answer that does not validate and a screen with
// nothing to send are errors, never an Observation; nothing is retried.
func TestObserveFailsWithoutObserving(t *testing.T) {
	good := answer(t, map[Axis]string{AxisComposer: "empty", AxisDialog: "none", AxisNotice: "none"},
		map[Axis]float64{AxisComposer: 1, AxisDialog: 1, AxisNotice: 1})
	for name, tc := range map[string]struct {
		rt     roundTrip
		screen string
	}{
		"HTTP 500":        {respond(500, []byte("down")), "› "},
		"transport error": {func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") }, "› "},
		"other model":     {respond(200, []byte(strings.Replace(string(good), notice.Model, "jev-latest", 1))), "› "},
		"not JSON":        {respond(200, []byte("{")), "› "},
		"empty screen":    {respond(200, good), "\x1b[0m  \n"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			rt := roundTrip(func(r *http.Request) (*http.Response, error) { calls++; return tc.rt(r) })
			obs, err := New(notice.New("k").WithTransport(rt)).Observe(context.Background(), nil, tc.screen)
			if err == nil {
				t.Fatalf("observed %+v", obs)
			}
			if calls > 1 {
				t.Fatalf("%d requests: retried", calls)
			}
		})
	}
}

// The caller's deadline reaches the request, capped at notice.Timeout.
func TestObserveDeadlineIsTheCallersCapped(t *testing.T) {
	good := answer(t, map[Axis]string{AxisComposer: "empty", AxisDialog: "none", AxisNotice: "none"},
		map[Axis]float64{AxisComposer: 1, AxisDialog: 1, AxisNotice: 1})
	var deadline time.Time
	rt := roundTrip(func(r *http.Request) (*http.Response, error) {
		deadline, _ = r.Context().Deadline()
		return respond(200, good)(r)
	})
	o := New(notice.New("k").WithTransport(rt))
	if _, err := o.Observe(context.Background(), nil, "› "); err != nil {
		t.Fatal(err)
	}
	if left := time.Until(deadline); left <= 0 || left > notice.Timeout {
		t.Fatalf("no deadline: %v left", left)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := o.Observe(ctx, nil, "› "); err != nil {
		t.Fatal(err)
	}
	if left := time.Until(deadline); left > time.Second {
		t.Fatalf("the caller's 1s deadline was not kept: %v left", left)
	}
}
