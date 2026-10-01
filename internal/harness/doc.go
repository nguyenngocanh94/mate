// Package harness is the harness contract (contract.go) and the Registry
// that holds the harnesses a binary launches. A harness is a Profile: its
// Launcher builds LaunchSpec values and never forks the harness process.
//
// For callers outside this package, constructors are the only way to obtain
// a startable LaunchSpec, and they refuse to return one whose required
// context would be silently dropped. Inside the package a startable-shaped
// spec can still be hand-assembled (Startable is only a shape check), so
// consuming boundaries - the runtime start path included - run
// ValidateRequiredContext again rather than trusting shape or provenance;
// it also refuses the zero spec Go always allows. Validation models where
// the agent will run (absolute cwd and context path) and the target CLI's
// real argument semantics (last-duplicate-wins, the `--` terminator, cwd
// discovery), not only file properties.
//
// Copied from v1 (github.com/nguyenngocanh94/mate). The kinds, roles and model
// ref that v1 kept in internal/domain live here in kind.go instead; the
// transcript parsers are carried over for the post-MVP token monitor and have
// no caller yet.
package harness
