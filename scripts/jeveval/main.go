// Command jeveval measures Jev (TypeSafe, jev-1.13.0) on every pane screen
// the repo already labels deterministically, for the Jev observer plan's
// PR 0 gate (docs/plans/jev-observer-2026-10-08.md, section 5).
//
//	go run ./scripts/jeveval [-cassette dir] [-replay] [-list]
//
// The key is read from the file MATE_JEV_API_KEY_FILE names, else
// ~/.config/mate/jev-api-key. Nothing but the corpus is sent, one request
// per screen, sequentially, 8 s deadline, no retry. Neither the key nor a
// screen is printed: the table names files and labels only.
//
// -cassette dir records each response as dir/<sha256 of request>.json and
// the run as dir/manifest.json; -replay answers from that cassette instead
// of the network. -list prints the corpus and its labels without calling.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/notice"
)

// manifest is the run a cassette was recorded from.
type manifest struct {
	Model   string          `json:"model"`
	Date    string          `json:"date"`
	Prompt  string          `json:"prompt"`
	Entries []manifestEntry `json:"entries"`
}

type manifestEntry struct {
	File      string `json:"file"`
	Request   string `json:"request_sha256"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

func main() {
	cassette := flag.String("cassette", "", "record responses into (or, with -replay, read them from) this directory")
	replay := flag.Bool("replay", false, "answer from -cassette instead of calling Jev")
	list := flag.Bool("list", false, "print the corpus and its deterministic labels; call nothing")
	flag.Parse()
	if err := run(os.Stdout, *cassette, *replay, *list); err != nil {
		fmt.Fprintln(os.Stderr, "jeveval:", err)
		os.Exit(1)
	}
}

func run(w io.Writer, cassette string, replay, list bool) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	corpus, err := loadCorpus(root)
	if err != nil {
		return err
	}
	if list {
		printCorpus(w, corpus)
		return nil
	}
	if replay && cassette == "" {
		return errors.New("-replay needs -cassette")
	}

	var key string
	var recorded map[string]manifestEntry
	if replay {
		recorded, err = readManifest(cassette)
		if err != nil {
			return err
		}
	} else {
		key, err = readKey()
		if err != nil {
			return err
		}
	}
	client := &http.Client{Timeout: notice.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	rows := make([]Row, 0, len(corpus))
	m := manifest{Model: notice.Model, Date: time.Now().Format("2006-01-02"), Prompt: promptHash()}
	for _, s := range corpus {
		body, err := requestBody(prepare(s.Text, key))
		if err != nil {
			return err
		}
		row := Row{Screen: s, Hash: hashOf(body)}
		var data []byte
		if replay {
			e, ok := recorded[row.Hash]
			if !ok {
				return fmt.Errorf("%s: request %s is not in the cassette", s.File, row.Hash[:12])
			}
			row.LatencyMS, row.Err = e.LatencyMS, e.Error
			if e.Error == "" {
				data, err = os.ReadFile(filepath.Join(cassette, row.Hash+".json"))
				if err != nil {
					return err
				}
			}
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), notice.Timeout)
			start := time.Now()
			data, err = call(ctx, client, key, body)
			row.LatencyMS = time.Since(start).Milliseconds()
			cancel()
			var apiErr *APIError
			if errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusUnprocessableEntity) {
				// The key or the request shape is refused: every
				// further screen would be refused the same way.
				return fmt.Errorf("%s: Jev refused the request: %v", s.File, err)
			}
			if err != nil {
				row.Err = err.Error()
			}
			if cassette != "" && err == nil {
				if err := os.MkdirAll(cassette, 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(cassette, row.Hash+".json"), data, 0o644); err != nil {
					return err
				}
			}
		}
		// The manifest records transport errors only: an answer that
		// fails validation is in the cassette and is re-judged on replay.
		m.Entries = append(m.Entries, manifestEntry{File: s.File, Request: row.Hash, LatencyMS: row.LatencyMS, Error: row.Err})
		if row.Err == "" {
			row.Resp, err = parse(data)
			if err != nil {
				row.Err = "invalid answer: " + err.Error()
			}
		}
		rows = append(rows, row)
	}
	if cassette != "" && !replay {
		data, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cassette, "manifest.json"), append(data, '\n'), 0o644); err != nil {
			return err
		}
	}
	if replay {
		if old, err := readManifestFile(cassette); err == nil {
			m.Date = old.Date
		}
	}
	report(w, m, rows)
	return nil
}

// readKey reads the API key file; it never prints the key.
func readKey() (string, error) {
	path := os.Getenv("MATE_JEV_API_KEY_FILE")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".config", "mate", "jev-api-key")
	} else if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, rest)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read the Jev key file %s: %w", path, err)
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return "", fmt.Errorf("the Jev key file %s is empty", path)
	}
	return key, nil
}

func readManifestFile(dir string) (manifest, error) {
	var m manifest
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(data, &m)
}

func readManifest(dir string) (map[string]manifestEntry, error) {
	m, err := readManifestFile(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]manifestEntry{}
	for _, e := range m.Entries {
		out[e.Request] = e
	}
	return out, nil
}

func printCorpus(w io.Writer, corpus []Screen) {
	fmt.Fprintln(w, "| File | Source | Harness | Axis | Deterministic | Composer | Lines |")
	fmt.Fprintln(w, "| --- | --- | --- | --- | --- | --- | ---: |")
	for _, s := range corpus {
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %d |\n", s.File, s.Source, s.Kind, s.Axis, s.Det, s.Composer, len(strings.Split(strings.TrimSpace(s.Text), "\n")))
	}
}

// report prints the evidence tables: every screen, the gate's dangerous
// list, the composer cross-check and the summary numbers.
func report(w io.Writer, m manifest, rows []Row) {
	fmt.Fprintf(w, "Model `%s`, run %s, prompt `%s`, %d screens.\n\n", m.Model, m.Date, m.Prompt, len(rows))
	fmt.Fprintln(w, "| # | File | Axis | Deterministic | Jev | Match | Confidence | Latency ms |")
	fmt.Fprintln(w, "| ---: | --- | --- | --- | --- | --- | ---: | ---: |")
	matched := 0
	var lat []int64
	for i, r := range rows {
		jev, conf := "error: "+r.Err, "-"
		if r.Err == "" {
			a := r.Resp.Answers[r.Axis]
			jev, conf = a.Choice, fmt.Sprintf("%.3f", a.Confidence)
		}
		ok := "no"
		switch {
		case !r.Scored():
			ok = "unscored"
		case r.Match():
			ok = "yes"
			matched++
		}
		lat = append(lat, r.LatencyMS)
		fmt.Fprintf(w, "| %d | `%s` | %s | %s | %s | %s | %s | %d |\n", i+1, short(r.File), r.Axis, r.Det, jev, ok, conf, r.LatencyMS)
	}

	fmt.Fprintln(w, "\n### Empty when Draft or Busy")
	fmt.Fprintln(w)
	n, nScored := 0, 0
	for _, r := range rows {
		if r.Dangerous() {
			n++
			where := "cross-check: the screen is scored on " + string(r.Axis)
			if r.Axis == AxisComposer {
				nScored++
				where = "scored axis"
			}
			fmt.Fprintf(w, "- `%s`: ClassifyComposer %s, Jev composer empty (%.3f); %s\n", short(r.File), r.Composer, r.Resp.Answers[AxisComposer].Confidence, where)
		}
	}
	if n == 0 {
		fmt.Fprintln(w, "None.")
	}

	fmt.Fprintln(w, "\n### Composer on every harness screen")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| File | ClassifyComposer | Jev composer | Confidence | Agree |")
	fmt.Fprintln(w, "| --- | --- | --- | ---: | --- |")
	cAgree, cTotal := 0, 0
	for _, r := range rows {
		if r.Kind == "" {
			continue
		}
		cTotal++
		jev, conf, agree := "error", "-", "no"
		if r.Err == "" {
			a := r.Resp.Answers[AxisComposer]
			jev, conf = a.Choice, fmt.Sprintf("%.3f", a.Confidence)
			if slices.Contains(accepts(AxisComposer, string(r.Composer)), a.Choice) {
				agree = "yes"
				cAgree++
			}
		}
		fmt.Fprintf(w, "| `%s` | %s | %s | %s | %s |\n", short(r.File), r.Composer, jev, conf, agree)
	}

	below := 0
	for _, r := range rows {
		if r.Err == "" && r.Scored() && r.Resp.Answers[r.Axis].Confidence < 0.85 {
			below++
		}
	}
	scored := 0
	for _, r := range rows {
		if r.Scored() {
			scored++
		}
	}
	slices.Sort(lat)
	fmt.Fprintln(w, "\n### Summary")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- Match on the scored axis: %d/%d (%.1f%%); %d screens unscored (the startup classifier abstains).\n", matched, scored, 100*float64(matched)/float64(scored), len(rows)-scored)
	fmt.Fprintf(w, "- Empty when Draft or Busy: %d (%d on the scored composer axis, %d in the cross-check).\n", n, nScored, n-nScored)
	fmt.Fprintf(w, "- Composer cross-check on all %d harness screens: %d agree.\n", cTotal, cAgree)
	errs := 0
	for _, r := range rows {
		if r.Err != "" {
			errs++
		}
	}
	rounded := 0
	for _, r := range rows {
		if r.Err == "" && r.Resp.Rounded {
			rounded++
		}
	}
	fmt.Fprintf(w, "- Failed requests or invalid answers: %d.\n", errs)
	fmt.Fprintf(w, "- Valid answers whose probabilities sum to one only within the API's two-decimal rounding (notice.Client would refuse them): %d.\n", rounded)
	fmt.Fprintf(w, "- Scored answers below confidence 0.85: %d.\n", below)
	// What the plan's chain would have taken from Jev at its proposed
	// threshold: only answers at or above it, the rest going to fixtures.
	kept, keptMatch := 0, 0
	for _, r := range rows {
		if r.Err != "" || !r.Scored() || r.Resp.Answers[r.Axis].Confidence < 0.85 {
			continue
		}
		kept++
		if r.Match() {
			keptMatch++
		}
	}
	var sure []string
	for _, r := range rows {
		if r.Dangerous() && r.Resp.Answers[AxisComposer].Confidence >= 0.85 {
			sure = append(sure, "`"+short(r.File)+"`")
		}
	}
	fmt.Fprintf(w, "- Scored answers at or above 0.85: %d, of which %d match.\n", kept, keptMatch)
	fmt.Fprintf(w, "- Empty when Draft or Busy with composer confidence at or above 0.85: %d %v.\n", len(sure), sure)
	in, out, answered := 0, 0, 0
	for _, r := range rows {
		if r.Err == "" {
			in, out, answered = in+r.Resp.InputTokens, out+r.Resp.OutputTokens, answered+1
		}
	}
	if answered > 0 {
		fmt.Fprintf(w, "- Tokens per request (mean over %d answers, three questions each): %d in, %d out.\n", answered, in/answered, out/answered)
	}
	if len(lat) > 0 {
		fmt.Fprintf(w, "- Latency over %d requests: p50 %d ms, p95 %d ms, min %d ms, max %d ms.\n", len(lat), pct(lat, 50), pct(lat, 95), lat[0], lat[len(lat)-1])
	}
}

// pct is the nearest-rank percentile of sorted values.
func pct(sorted []int64, p int) int64 {
	rank := (p*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	return sorted[rank-1]
}

func short(file string) string {
	for _, prefix := range []string{"internal/send/testdata/screens/", "internal/harness/", "internal/notice/testdata/", "scripts/jeveval/testdata/screens/"} {
		if rest, ok := strings.CutPrefix(file, prefix); ok {
			return strings.Replace(rest, "/testdata/", "/", 1)
		}
	}
	return file
}
