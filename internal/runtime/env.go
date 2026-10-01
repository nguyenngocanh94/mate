package runtime

import (
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
)

// AllowlistedEnv is the live allowlist: the identity keys
// (config.IdentityEnvKeys) plus the provider roots a harness declares
// (providerKeys, from harness.Info.EnvKeys), no HERDR_* (Herdr injects
// those), and no secrets. Unknown keys, empty values, duplicates, and
// KEY=VALUE-breaking characters are refused. Dropping a key the caller
// intended to inject would start an agent that cannot prove its identity to
// `mate`.
func AllowlistedEnv(vars []EnvVar, providerKeys []string) ([]EnvVar, error) {
	allow := make(map[string]struct{}, 8)
	for _, k := range append(config.IdentityEnvKeys(), providerKeys...) {
		allow[k] = struct{}{}
	}
	seen := make(map[string]struct{}, len(vars))
	out := make([]EnvVar, 0, len(vars))
	for _, v := range vars {
		key := strings.TrimSpace(v.Key)
		if key == "" {
			return nil, observability.NewError(observability.CodeUsage, "env key is empty")
		}
		if strings.ContainsAny(key, "=\x00\n") || strings.ContainsAny(v.Value, "\x00\n") {
			return nil, observability.NewError(observability.CodeUsage, "env assignment is not a single KEY=VALUE token")
		}
		if _, ok := allow[key]; !ok {
			return nil, observability.NewError(
				observability.CodeUsage,
				fmt.Sprintf("env key %q is not allowlisted; unknown keys are refused rather than dropped (allowlist is the MATE_* identity keys; HERDR_* is injected by Herdr)", key),
			)
		}
		if v.Value == "" {
			return nil, observability.NewError(
				observability.CodeUsage,
				fmt.Sprintf("env %s has an empty value; omit the key instead of injecting an empty assignment", key),
			)
		}
		if _, dup := seen[key]; dup {
			return nil, observability.NewError(observability.CodeUsage, fmt.Sprintf("duplicate env key %s", key))
		}
		seen[key] = struct{}{}
		out = append(out, EnvVar{Key: key, Value: v.Value})
	}
	return out, nil
}

// PaneEnv copies LaunchSpec.Env onto the runtime env shape that workspace
// and tab create inject. Agent start has no --env flag (Herdr 0.8.2), so
// Herdr.StartAgent exports the same allowlisted variables into the pane's
// shell before every start.
func PaneEnv(launch harness.LaunchSpec) ([]EnvVar, error) {
	return AllowlistedEnv(runtimeEnv(launch.Env()), launch.EnvKeys())
}

func runtimeEnv(src []harness.EnvVar) []EnvVar {
	vars := make([]EnvVar, 0, len(src))
	for _, v := range src {
		vars = append(vars, EnvVar{Key: v.Key, Value: v.Value})
	}
	return vars
}

// exportAssignments renders vars as the words of one POSIX `export` typed
// into a pane's shell: KEY='value', with a single quote in the value closed,
// escaped and reopened, so a path with spaces or quotes arrives whole.
// AllowlistedEnv has already refused keys and values that could not be one
// assignment.
func exportAssignments(vars []EnvVar) []string {
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		out = append(out, v.Key+"='"+strings.ReplaceAll(v.Value, "'", `'\''`)+"'")
	}
	return out
}

func envFlags(vars []EnvVar) []string {
	out := make([]string, 0, len(vars)*2)
	for _, v := range vars {
		out = append(out, "--env", v.Key+"="+v.Value)
	}
	return out
}
