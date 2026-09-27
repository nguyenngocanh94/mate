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
