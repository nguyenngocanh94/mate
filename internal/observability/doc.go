// Package observability is the coded-error and CLI-envelope contract shared
// by every other package: a stable Code taxonomy, the *Error carrying it,
// the process exit mapping, and the JSON envelope the agent-facing commands
// print. It is deliberately small - v1's event catalog, logging and
// redaction helpers are not part of mate and are not copied here.
package observability
