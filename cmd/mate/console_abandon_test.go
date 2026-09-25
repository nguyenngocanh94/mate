package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestWaitAbandonedWaitsForTheActionThenReports: the Console waits for an
// action it quit in the middle of, and says whether it stopped in time.
func TestWaitAbandonedWaitsForTheActionThenReports(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(done)
	}()
	var errw bytes.Buffer
	start := time.Now()
	waitAbandoned(&errw, "onboard shop", done, 5*time.Second)
	if time.Since(start) < 25*time.Millisecond {
		t.Fatalf("returned after %s, before the action had stopped", time.Since(start))
	}
	if !strings.Contains(errw.String(), "onboard shop has stopped") {
		t.Fatalf("stderr = %q", errw.String())
	}

	errw.Reset()
	waitAbandoned(&errw, "onboard shop", make(chan struct{}), 20*time.Millisecond)
	if !strings.Contains(errw.String(), "had not stopped after 20ms") {
		t.Fatalf("stderr on timeout = %q", errw.String())
	}
}
