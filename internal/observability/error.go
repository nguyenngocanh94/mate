package observability

import (
	"errors"
	"fmt"
)

// Stable error codes. CLI exit mapping is in ExitCode.
const (
	CodeUsage              Code = "usage"
	CodeStateConflict      Code = "state_conflict"
	CodeRuntimeUnavailable Code = "runtime_unavailable"
	CodeTargetBlocked      Code = "target_blocked"
	CodeInteractionTimeout Code = "interaction_timeout"
	CodeInteractionExpired Code = "interaction_expired"
	CodeNeedsRepair        Code = "needs_repair"
	CodeNotFound           Code = "not_found"
	CodeAlreadyExists      Code = "already_exists"
	CodePermission         Code = "permission"
	CodeTimeout            Code = "timeout"
	CodeUnknown            Code = "unknown"
)

// Code is a stable, machine-readable error taxonomy value.
type Code string

// Process exit codes from the agent CLI contract.
const (
	ExitOK                 = 0
	ExitGeneric            = 1
	ExitUsage              = 2
	ExitStateConflict      = 10
	ExitRuntimeUnavailable = 20
	ExitTargetBlocked      = 30
	ExitInteractionWait    = 31
	ExitNeedsRepair        = 40
)

// EnvAttachResultFile is an internal hand-off channel used by the Console's
// mate attach child. The child still writes its normal envelope to the
// terminal for scripting and human callers, and mirrors it here so the
// Console can recover the error code after the child releases the TTY.
const EnvAttachResultFile = "MATE_ATTACH_RESULT_FILE"

// Error is a coded error with optional details. CodeInteractionTimeout and
// CodeInteractionExpired both map to exit 31 and are distinguished by
// details["reason"]; see the table in docs/phase1/agent.md section 4.
type Error struct {
	Code    Code
	Message string
	Details map[string]any
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewError constructs a coded error.
func NewError(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// WithDetails returns a copy with details set. The original is not mutated.
func (e *Error) WithDetails(details map[string]any) *Error {
	if e == nil {
		return nil
	}
	cp := *e
	if details == nil {
		cp.Details = nil
		return &cp
	}
	cp.Details = make(map[string]any, len(details))
	for k, v := range details {
		cp.Details[k] = v
	}
	return &cp
}

// WrapError attaches a coded taxonomy to an existing error.
func WrapError(code Code, message string, err error) *Error {
	return &Error{Code: code, Message: message, Err: err}
}

// ExitCode maps err onto the CLI exit convention. Unknown errors are 1.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var coded *Error
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return ExitGeneric
}

// ExitCode returns the process exit for this coded error. The mapping is the
// published contract in docs/phase1/agent.md section 4; codes without a
// dedicated exit there report the generic failure exit 1.
func (e *Error) ExitCode() int {
	if e == nil {
		return ExitOK
	}
	switch e.Code {
	case CodeUsage:
		return ExitUsage
	case CodeStateConflict, CodeAlreadyExists:
		return ExitStateConflict
	case CodeRuntimeUnavailable:
		return ExitRuntimeUnavailable
	case CodeTargetBlocked:
		return ExitTargetBlocked
	case CodeInteractionTimeout, CodeInteractionExpired:
		return ExitInteractionWait
	case CodeNeedsRepair:
		return ExitNeedsRepair
	case CodeNotFound, CodePermission, CodeTimeout, CodeUnknown:
		return ExitGeneric
	default:
		return ExitGeneric
	}
}
