// Package config holds build-time metadata and process-wide defaults.
package config

// Set by the Makefile through -ldflags; "dev"/"unknown" when built with plain `go build`.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)
