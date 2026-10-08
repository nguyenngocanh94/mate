package jev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Replay is an http.RoundTripper that answers every request from a
// cassette scripts/jeveval recorded: dir/<HashOf(body)>.json. A request the
// cassette has no answer for is a transport error. It never reaches the
// network; tests here and in internal/screen/chain run the corpus through
// it (notice.Client.WithTransport).
type Replay struct {
	Dir string
}

// RoundTrip implements http.RoundTripper.
func (r Replay) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body.Close()
	hash := HashOf(body)
	data, err := os.ReadFile(filepath.Join(r.Dir, hash+".json"))
	if err != nil {
		return nil, fmt.Errorf("cassette has no answer for request %s", hash[:12])
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
}

// Recorded is one screen a cassette was recorded from.
type Recorded struct {
	// File is module-relative, as the cassette's manifest names it; a
	// notice fixture is internal/notice/testdata/notices.json#<name>.
	File string
	Text string
}

// Screens reads the cassette's manifest and the text of every screen it
// was recorded from, in recording order. root is the module root.
func (r Replay) Screens(root string) ([]Recorded, error) {
	raw, err := os.ReadFile(filepath.Join(r.Dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m struct {
		Entries []struct {
			File string `json:"file"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(r.Dir, "manifest.json"), err)
	}
	notices := map[string]string{}
	var out []Recorded
	for _, e := range m.Entries {
		file, name, isNotice := strings.Cut(e.File, "#")
		if !isNotice {
			text, err := os.ReadFile(filepath.Join(root, file))
			if err != nil {
				return nil, err
			}
			out = append(out, Recorded{File: e.File, Text: string(text)})
			continue
		}
		if len(notices) == 0 {
			data, err := os.ReadFile(filepath.Join(root, file))
			if err != nil {
				return nil, err
			}
			var fixtures []struct{ Name, Screen string }
			if err := json.Unmarshal(data, &fixtures); err != nil {
				return nil, fmt.Errorf("%s: %w", file, err)
			}
			for _, f := range fixtures {
				notices[f.Name] = f.Screen
			}
		}
		text, ok := notices[name]
		if !ok {
			return nil, fmt.Errorf("%s: no notice fixture %q", file, name)
		}
		out = append(out, Recorded{File: e.File, Text: text})
	}
	return out, nil
}
