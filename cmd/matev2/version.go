package main

import (
	"fmt"
	"io"

	"github.com/nguyenngocanh94/matev2/internal/config"
)

// cmdVersion implements `matev2 --version`.
func cmdVersion(args []string, stdout, stderr io.Writer) error {
	if len(args) != 0 {
		return newUsageError("usage: matev2 --version")
	}
	fmt.Fprintf(stdout, "matev2 %s (%s, %s)\n", config.Version, config.Commit, config.BuildDate)
	return nil
}
