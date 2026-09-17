// Package process defines the external-process port (Spec, Result, Runner)
// and its two implementations: the exec-backed ExecRunner that cmd wires,
// and FakeRunner, which records calls and returns scripted results so
// adapters (runtime, harness) can be tested without spawning anything.
package process
