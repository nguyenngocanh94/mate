package observability

import (
	"encoding/json"
	"io"
)

// SchemaVersion is the CLI JSON envelope version.
const SchemaVersion = 1

// Envelope is the agent-facing JSON contract: success on stdout, errors on
// stderr with ok=false. Human commands (help, version) do not use this.
type Envelope struct {
	OK            bool       `json:"ok"`
	SchemaVersion int        `json:"schema_version"`
	Command       string     `json:"command,omitempty"`
	Data          any        `json:"data,omitempty"`
	Error         *ErrorBody `json:"error,omitempty"`
}

// ErrorBody is the machine-readable error object.
type ErrorBody struct {
	Code    Code           `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// SuccessEnvelope builds an ok=true envelope.
func SuccessEnvelope(command string, data any) Envelope {
	return Envelope{
		OK:            true,
		SchemaVersion: SchemaVersion,
		Command:       command,
		Data:          data,
	}
}

// ErrorEnvelope builds an ok=false envelope from a coded error.
func ErrorEnvelope(command string, err *Error) Envelope {
	body := &ErrorBody{}
	if err != nil {
		body.Code = err.Code
		body.Message = err.Message
		body.Details = err.Details
	}
	return Envelope{
		OK:            false,
		SchemaVersion: SchemaVersion,
		Command:       command,
		Error:         body,
	}
}

// WriteJSON writes one envelope as a single JSON object plus newline.
func WriteJSON(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	return enc.Encode(env)
}
