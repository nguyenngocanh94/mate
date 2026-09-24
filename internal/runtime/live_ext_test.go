package runtime_test

import (
	"os"
	"testing"
)

// requireLive is the runtime_test half of the gate documented in live_test.go:
// TestLive* proofs need a provisioned Herdr lab session and run only with
// MATE_LIVE=1.
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("MATE_LIVE") != "1" {
		t.Skip("set MATE_LIVE=1 to run live Herdr proofs")
	}
}
