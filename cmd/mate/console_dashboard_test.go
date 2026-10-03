package main

import (
	"context"
	"net/http"
	"testing"
)

// The console serves the dashboard while it is open and stops with it.
func TestStartConsoleDashboardServesUntilStopped(t *testing.T) {
	w := usageWorkspace(t)
	url, stop, err := startConsoleDashboard(context.Background(), w)
	if err != nil {
		t.Skipf("default dashboard port unavailable: %v", err)
	}
	resp, err := http.Get(url + "api/workspace")
	if err != nil {
		stop()
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	stop()
	stop() // idempotent
	if r, err := http.Get(url + "api/workspace"); err == nil {
		r.Body.Close()
		t.Error("dashboard still answering after stop")
	}
}

// A busy port is reported, not fatal.
func TestStartConsoleDashboardBusyPort(t *testing.T) {
	w := usageWorkspace(t)
	_, stop, err := startConsoleDashboard(context.Background(), w)
	if err != nil {
		t.Skipf("default dashboard port unavailable: %v", err)
	}
	defer stop()
	if _, _, err := startConsoleDashboard(context.Background(), w); err == nil {
		t.Fatal("second start on the same port should fail")
	}
}
