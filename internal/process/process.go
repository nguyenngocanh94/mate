package process

import (
	"context"
	"strings"
	"sync"
)

// Spec describes an external process invocation.
type Spec struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string
	Stdin []byte
}

// Result is the observed outcome of a process run.
type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// Runner executes external processes. Adapters receive this port so
// tests can inject a fake with failure injection.
//
// A cancelled or deadline-exceeded context must return ctx.Err() (or an
// error that unwraps to it) and must not report a Result with a nil
// error. FakeRunner honors this; real adapters (G2) must as well.
type Runner interface {
	Run(ctx context.Context, spec Spec) (Result, error)
}

// FakeRunner records calls and returns scripted results.
type FakeRunner struct {
	mu        sync.Mutex
	Calls     []Spec
	Handler   func(ctx context.Context, spec Spec) (Result, error)
	Responses map[string]Result
	Failures  map[string]error
	Default   Result
}

// CommandKey is the lookup key for scripted responses: name plus args.
func CommandKey(spec Spec) string {
	if len(spec.Args) == 0 {
		return spec.Name
	}
	return spec.Name + " " + strings.Join(spec.Args, " ")
}

// Run records spec and returns a scripted result, a Handler result, or Default.
func (f *FakeRunner) Run(ctx context.Context, spec Spec) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	f.mu.Lock()
	f.Calls = append(f.Calls, cloneSpec(spec))
	handler := f.Handler
	key := CommandKey(spec)
	fail, hasFail := f.Failures[key]
	resp, hasResp := f.Responses[key]
	def := f.Default
	f.mu.Unlock()

	if handler != nil {
		return handler(ctx, spec)
	}
	if hasFail {
		return Result{}, fail
	}
	if hasResp {
		return resp, nil
	}
	return def, nil
}

func cloneSpec(spec Spec) Spec {
	out := spec
	if spec.Args != nil {
		out.Args = append([]string(nil), spec.Args...)
	}
	if spec.Env != nil {
		out.Env = append([]string(nil), spec.Env...)
	}
	if spec.Stdin != nil {
		out.Stdin = append([]byte(nil), spec.Stdin...)
	}
	return out
}
