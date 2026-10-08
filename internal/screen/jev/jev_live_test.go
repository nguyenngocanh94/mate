package jev

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/notice"
)

// TestLiveJevObserverDrift sends the 90 screens of the 2026-10-08 corpus to
// Jev live and reports, per axis, how many answers agree with the cassette
// they were recorded in (docs/plans/jev-observer-2026-10-08.md section 7:
// the model drifting). It fails only on a request that does not answer; a
// disagreement is reported, for the day the observer is turned on to
// judge. Run explicitly, with a key; it costs 90 requests.
func TestLiveJevObserverDrift(t *testing.T) {
	if os.Getenv("MATE_LIVE") != "1" || os.Getenv("MATE_JEV_API_KEY_FILE") == "" {
		t.Skip("requires MATE_LIVE=1 and MATE_JEV_API_KEY_FILE")
	}
	key, err := os.ReadFile(os.Getenv("MATE_JEV_API_KEY_FILE"))
	if err != nil {
		t.Fatal("cannot read Jev key file")
	}
	replay := Replay{Dir: cassette}
	screens, err := replay.Screens(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	recorded := New(notice.New("test-key").WithTransport(replay))
	live := New(notice.New(strings.TrimSpace(string(key))))
	agree := map[string]int{}
	asked := 0
	for _, s := range screens {
		want, err := recorded.Observe(context.Background(), nil, s.Text)
		if err != nil {
			t.Fatalf("%s: the cassette has no answer: %v", s.File, err)
		}
		got, err := live.Observe(context.Background(), nil, s.Text)
		if err != nil {
			t.Errorf("%s: %v", s.File, err)
			continue
		}
		asked++
		for axis, same := range map[string]bool{
			"composer": got.Composer == want.Composer,
			"dialog":   got.Dialog == want.Dialog,
			"notice":   got.Notice == want.Notice,
		} {
			if same {
				agree[axis]++
			} else {
				t.Logf("%s: %s differs from the cassette (live %+v, recorded %+v)", s.File, axis, got, want)
			}
		}
	}
	t.Logf("agreement with the cassette on %d of %d screens answered: composer %d, dialog %d, notice %d",
		asked, len(screens), agree["composer"], agree["dialog"], agree["notice"])
}
