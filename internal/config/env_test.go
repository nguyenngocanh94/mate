package config

import (
	"log/slog"
	"testing"
)

func TestParseLogLevel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
		{"nope", slog.LevelInfo},
	}
	for _, tc := range tests {
		if got := ParseLogLevel(tc.in); got != tc.want {
			t.Errorf("ParseLogLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
