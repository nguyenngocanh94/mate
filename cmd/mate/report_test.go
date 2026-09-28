package main

import (
	"strings"
	"testing"
)

func TestReportSummaryKeepsLimitationsAndDoesNotReadTheWholeReport(t *testing.T) {
	text := "# Findings\n## Summary\nKhông tái hiện được lỗi.\nEvidence: log.txt\n\n## Detail\n" + strings.Repeat("long", 50000)
	got, err := reportSummary(text)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Không tái hiện được lỗi.\nEvidence: log.txt" {
		t.Fatal(got)
	}
	for _, bad := range []string{"## Detail\nmissing", "## Summary\n" + strings.Repeat("x", reportSummaryLimit+1)} {
		if _, err := reportSummary(bad); err == nil {
			t.Fatal("unbounded fallback")
		}
	}
}
