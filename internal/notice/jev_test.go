package notice

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func validResponse() map[string]any {
	p := map[string]float64{}
	for k := range criteria {
		p[k] = 0
	}
	p["quota_warning"] = 1
	return map[string]any{"model": Model, "answers": map[string]any{"notice": map[string]any{
		"type": "choice", "choice": "quota_warning", "confidence": .9, "probabilities": p,
	}}, "usage": map[string]int{"input_tokens": 100, "output_tokens": 20}}
}

func TestClassifyRequestAndResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("invalid request credentials or method")
		}
		var req struct {
			Model     string
			State     string
			Questions map[string]struct {
				Type, Instructions string
				Criteria           map[string]string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Model != Model || req.State != "Only 10% remains\n›" || req.Questions["notice"].Type != "choice" || len(req.Questions["notice"].Criteria) != 7 {
			t.Errorf("bad request shape: %+v", req)
		}
		_ = json.NewEncoder(w).Encode(validResponse())
	}))
	defer srv.Close()
	c := New("test-key")
	c.endpoint = srv.URL
	got, err := c.Classify(context.Background(), "\x1b[33mOnly 10% remains\x1b[0m\n›")
	if err != nil || got.Label != "quota_warning" || got.Confidence != .9 || got.InputTokens != 100 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestClassifyRejectsBrokenResponsesWithoutEcho(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, "private test-key"}, {"rate limit", 429, "private test-key"},
		{"overload", 529, "private test-key"}, {"malformed", 200, "private test-key"},
		{"missing", 200, `{}`}, {"oversized", 200, strings.Repeat("x", 65537)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := New("test-key")
			c.endpoint = srv.URL
			_, err := c.Classify(context.Background(), "hello")
			if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "test-key") {
				t.Fatalf("unsafe error: %v", err)
			}
		})
	}
}

func TestClassifyValidatesAnswer(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(a map[string]any) { a["choice"] = "send_enter" },
		func(a map[string]any) { a["confidence"] = 1.1 },
		func(a map[string]any) { delete(a, "confidence") },
		func(a map[string]any) { a["type"] = "noul" },
		func(a map[string]any) { a["probabilities"] = map[string]float64{"quota_warning": 1} },
	} {
		resp := validResponse()
		mutate(resp["answers"].(map[string]any)["notice"].(map[string]any))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(resp) }))
		c := New("test-key")
		c.endpoint = srv.URL
		if _, err := c.Classify(context.Background(), "hello"); err == nil {
			t.Error("accepted invalid answer")
		}
		srv.Close()
	}
}

func TestClassifyCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	c := New("test-key")
	c.endpoint = srv.URL
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := c.Classify(ctx, "hello"); err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestPrepareBoundsAndRedacts(t *testing.T) {
	input := strings.Repeat("old history\n", 100) + "\x1b]52;c;clipboard\a\x1b[31mBearer secret\x1b[0m\napi_key=abc123\napikey_123_456\n" + strings.Repeat("界", 9000)
	got := Prepare(input)
	if len(got) > MaxBytes || !utf8.ValidString(got) || strings.ContainsAny(got, "\x1b\a") || strings.Contains(got, "old history") {
		t.Fatal("screen not bounded/stripped")
	}
	got = Prepare("Bearer secret\napi_key=abc123\napikey_123_456\nsk-abcdefgh123456")
	for _, secret := range []string{"secret", "abc123", "apikey_123_456", "sk-abcdefgh123456"} {
		if strings.Contains(got, secret) {
			t.Error("credential not redacted")
		}
	}
}

func TestClassifyDoesNotFollowRedirects(t *testing.T) {
	var reached atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c := New("test-key")
	c.endpoint = source.URL
	if _, err := c.Classify(context.Background(), "terminal text"); err == nil {
		t.Fatal("redirect accepted")
	}
	if reached.Load() {
		t.Fatal("terminal text forwarded to redirect destination")
	}
}

func TestClassifyRejectsEmptyInputsAndRemovesOwnKey(t *testing.T) {
	if _, err := New("").Classify(context.Background(), "screen"); err == nil {
		t.Fatal("missing key accepted")
	}
	if _, err := New("test-key").Classify(context.Background(), "\x1b[0m"); err == nil {
		t.Fatal("empty screen accepted")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "test-key") {
			t.Error("own credential leaked in state")
		}
		_ = json.NewEncoder(w).Encode(validResponse())
	}))
	defer srv.Close()
	c := New("test-key")
	c.endpoint = srv.URL
	if _, err := c.Classify(context.Background(), "export VALUE=test-key"); err != nil {
		t.Fatal(err)
	}
}

// The API rounds every probability to two decimals, so a distribution may
// sum to 0.99 or 1.01; one that is off by more than the rounding is refused.
func TestClassifyAcceptsTwoDecimalRounding(t *testing.T) {
	for _, tc := range []struct {
		name          string
		warning, none float64
		accept        bool
	}{
		{"sums to 0.99", 0.95, 0.04, true},
		{"sums to 1.01", 0.95, 0.06, true},
		{"sums to 0.9", 0.85, 0.05, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := validResponse()
			p := resp["answers"].(map[string]any)["notice"].(map[string]any)["probabilities"].(map[string]float64)
			p["quota_warning"], p["none"] = tc.warning, tc.none
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(resp) }))
			defer srv.Close()
			c := New("test-key")
			c.endpoint = srv.URL
			got, err := c.Classify(context.Background(), "Only 10% remains")
			if tc.accept && (err != nil || got.Label != "quota_warning") {
				t.Fatalf("refused: %+v, %v", got, err)
			}
			if !tc.accept && err == nil {
				t.Fatalf("accepted a distribution off by more than rounding: %+v", got)
			}
		})
	}
}

// Ask sends the body its caller builds, from a screen with the key already
// removed, and hands back the answer unvalidated; a refusal echoes nothing.
func TestAskPostsTheBuiltBodyWithoutTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Bearer test-key" || string(body) != "built: export VALUE=[redacted]" {
			t.Errorf("request %q", body)
		}
		_, _ = w.Write([]byte(`{"answers":{}}`))
	}))
	defer srv.Close()
	c := New("test-key")
	c.endpoint = srv.URL
	got, err := c.Ask(context.Background(), "export VALUE=test-key", func(screen string) ([]byte, error) {
		return []byte("built: " + screen), nil
	})
	if err != nil || string(got) != `{"answers":{}}` {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := New("").Ask(context.Background(), "screen", func(s string) ([]byte, error) { return []byte(s), nil }); err == nil {
		t.Fatal("missing key accepted")
	}
}

// roundTrip is an http.RoundTripper from a function.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A client on another transport still posts to the pinned endpoint and
// still does not follow a redirect.
func TestWithTransportKeepsEndpointAndRedirectRule(t *testing.T) {
	var urls []string
	c := New("test-key").WithTransport(roundTrip(func(r *http.Request) (*http.Response, error) {
		urls = append(urls, r.URL.String())
		h := http.Header{}
		h.Set("Location", "https://elsewhere.example/")
		return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: h, Body: io.NopCloser(strings.NewReader("private test-key")), Request: r}, nil
	}))
	_, err := c.Ask(context.Background(), "screen", func(s string) ([]byte, error) { return []byte(s), nil })
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("redirect answer: %v", err)
	}
	if len(urls) != 1 || urls[0] != "https://api.typesafe.ai/v1/systemone" {
		t.Fatalf("requests went to %v", urls)
	}
}
