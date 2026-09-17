package observability

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// codeExits is the complete published Code -> process exit mapping from
// docs/phase1/agent.md section 4. Every Code constant must appear here.
var codeExits = map[Code]int{
	CodeUsage:              ExitUsage,
	CodeStateConflict:      ExitStateConflict,
	CodeAlreadyExists:      ExitStateConflict,
	CodeRuntimeUnavailable: ExitRuntimeUnavailable,
	CodeTargetBlocked:      ExitTargetBlocked,
	CodeInteractionTimeout: ExitInteractionWait,
	CodeInteractionExpired: ExitInteractionWait,
	CodeNeedsRepair:        ExitNeedsRepair,
	CodeNotFound:           ExitGeneric,
	CodePermission:         ExitGeneric,
	CodeTimeout:            ExitGeneric,
	CodeUnknown:            ExitGeneric,
}

func TestExitCodeMappingIsComplete(t *testing.T) {
	t.Parallel()
	declared := []Code{
		CodeUsage,
		CodeStateConflict,
		CodeRuntimeUnavailable,
		CodeTargetBlocked,
		CodeInteractionTimeout,
		CodeInteractionExpired,
		CodeNeedsRepair,
		CodeNotFound,
		CodeAlreadyExists,
		CodePermission,
		CodeTimeout,
		CodeUnknown,
	}
	if len(declared) != len(codeExits) {
		t.Fatalf("declared %d codes, mapping has %d", len(declared), len(codeExits))
	}
	for _, code := range declared {
		want, ok := codeExits[code]
		if !ok {
			t.Fatalf("code %q has no documented exit", code)
		}
		if got := ExitCode(NewError(code, "boom")); got != want {
			t.Errorf("ExitCode(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestNotFoundIsNotRuntimeUnavailable(t *testing.T) {
	t.Parallel()
	for _, code := range []Code{CodeNotFound, CodeTimeout, CodePermission} {
		if got := ExitCode(NewError(code, "boom")); got == ExitRuntimeUnavailable {
			t.Errorf("%q exits %d, which agents read as runtime unavailable", code, got)
		}
	}
}

func TestExitCodeMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want int
	}{
		{nil, ExitOK},
		{errors.New("plain"), ExitGeneric},
		{NewError(Code("custom"), "undeclared"), ExitGeneric},
		{NewError(CodeInteractionTimeout, "wait").WithDetails(map[string]any{"reason": "timeout"}), ExitInteractionWait},
		{NewError(CodeInteractionExpired, "wait").WithDetails(map[string]any{"reason": "expired"}), ExitInteractionWait},
	}
	for _, tc := range tests {
		if got := ExitCode(tc.err); got != tc.want {
			t.Errorf("ExitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestErrorEnvelopeJSON(t *testing.T) {
	t.Parallel()
	err := NewError(CodeInteractionTimeout, "ask timed out").WithDetails(map[string]any{"reason": "timeout"})
	env := ErrorEnvelope("ask", err)
	raw, marshalErr := json.Marshal(env)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	s := string(raw)
	for _, want := range []string{`"ok":false`, `"code":"interaction_timeout"`, `"reason":"timeout"`, `"schema_version":1`} {
		if !strings.Contains(s, want) {
			t.Fatalf("envelope %s missing %s", s, want)
		}
	}
}

func TestWrapErrorUnwraps(t *testing.T) {
	t.Parallel()
	inner := errors.New("socket missing")
	err := WrapError(CodeRuntimeUnavailable, "herdr", inner)
	if !errors.Is(err, inner) {
		t.Fatal("expected unwrap")
	}
}

func TestErrorStringAndNil(t *testing.T) {
	t.Parallel()
	var none *Error
	if none.Error() != "" {
		t.Fatalf("nil Error() = %q", none.Error())
	}
	if none.ExitCode() != ExitOK {
		t.Fatalf("nil ExitCode = %d", none.ExitCode())
	}
	if none.Unwrap() != nil {
		t.Fatal("nil unwrap")
	}
	if none.WithDetails(nil) != nil {
		t.Fatal("nil WithDetails")
	}

	plain := NewError(CodeUsage, "")
	if plain.Error() != string(CodeUsage) {
		t.Fatalf("code-only Error() = %q", plain.Error())
	}
	wrapped := WrapError(CodeTimeout, "wait", errors.New("deadline"))
	if !strings.Contains(wrapped.Error(), "deadline") {
		t.Fatalf("wrapped = %q", wrapped.Error())
	}
	if ExitCode(NewError(Code("custom"), "x")) != ExitGeneric {
		t.Fatal("unknown code should be generic exit")
	}
}

func TestWithDetailsDoesNotMutateOriginal(t *testing.T) {
	t.Parallel()
	base := NewError(CodeUsage, "bad")
	next := base.WithDetails(map[string]any{"flag": "--json"})
	if base.Details != nil {
		t.Fatal("original mutated")
	}
	if next.Details["flag"] != "--json" {
		t.Fatalf("details = %#v", next.Details)
	}
}
