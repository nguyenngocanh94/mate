package harness

import (
	"context"
	"fmt"
	"sync"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

// Fake routes BuildLaunchSpec to the real Claude/Codex constructors (the
// contract is the constructors) and adds failure injection for later gates.
type Fake struct {
	mu       sync.Mutex
	Claude   Claude
	Codex    Codex
	BuildErr error
	Calls    []string
}

// BuildLaunchSpec implements Launcher. It never forks a process.
func (f *Fake) BuildLaunchSpec(ctx context.Context, spec AgentSpec) (LaunchSpec, error) {
	f.mu.Lock()
	buildErr := f.BuildErr
	kind := spec.Kind
	f.Calls = append(f.Calls, "BuildLaunchSpec:"+string(kind))
	claude := f.Claude
	codex := f.Codex
	f.mu.Unlock()
	if buildErr != nil {
		return LaunchSpec{}, buildErr
	}
	switch kind {
	case KindClaude, "":
		return claude.BuildLaunchSpec(ctx, spec)
	case KindCodex:
		return codex.BuildLaunchSpec(ctx, spec)
	default:
		return LaunchSpec{}, observability.NewError(observability.CodeUsage, fmt.Sprintf("unknown harness %q", kind))
	}
}
