package notice

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Only synthetic terminal excerpts leave the machine in this test. Neither
// the response body nor key is logged. Run explicitly; normal CI has no key.
func TestLiveJevNotices(t *testing.T) {
	if os.Getenv("MATE_LIVE") != "1" || os.Getenv("MATE_JEV_API_KEY_FILE") == "" {
		t.Skip("requires MATE_LIVE=1 and MATE_JEV_API_KEY_FILE")
	}
	key, err := os.ReadFile(os.Getenv("MATE_JEV_API_KEY_FILE"))
	if err != nil {
		t.Fatal("cannot read Jev key file")
	}
	data, err := os.ReadFile("testdata/notices.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct{ Name, Screen, Want string }
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	c := New(strings.TrimSpace(string(key)))
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), Timeout)
			defer cancel()
			result, err := c.Classify(ctx, f.Screen)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("label=%s confidence=%.3f latency=%s input_tokens=%d", result.Label, result.Confidence, time.Since(start).Round(time.Millisecond), result.InputTokens)
			if result.Label != f.Want {
				t.Errorf("got %s, want %s", result.Label, f.Want)
			}
		})
	}
}
