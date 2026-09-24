package main

import (
	"fmt"
	"io"

	"github.com/nguyenngocanh94/mate/internal/config"
)

// cmdVersion implements `mate --version`.
func cmdVersion(args []string, stdout, stderr io.Writer) error {
	if len(args) != 0 {
		return newUsageError("usage: mate --version")
	}
	fmt.Fprintf(stdout, "mate %s (%s, %s)\n", config.Version, config.Commit, config.BuildDate)
	return nil
}
