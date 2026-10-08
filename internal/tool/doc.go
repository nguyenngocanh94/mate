// Package tool is the contract for the outside programs mate drives: a
// program the console binds a key to and the CLI wraps, whose data is its
// own and not mate's state
// (docs/plans/workspace-layout-and-tools-2026-10-08.md, section 5).
//
// A tool is a Profile in a Registry, the way a harness is
// (internal/harness): who it is (Info), and capabilities it declares one by
// one in the shared vocabulary of internal/capability, each verified with
// evidence or refused with a reason. The core asks the registry instead of
// naming a tool.
//
// This package names no tool. Each tool's own package imports it;
// internal/tool/catalog is the one place that lists them, and only
// cmd/mate imports the catalog. Package tool imports none of those, nor
// the store, the runtime or cmd/mate.
package tool
