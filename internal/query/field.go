package query

import (
	"errors"
	"strings"

	"github.com/nguyenngocanh94/matev2/internal/observability"
)

// FieldState says whether a value that could fail to read is known,
// legitimately absent, or unreadable right now. A field with a FieldState
// must never be defaulted to its Go zero value and rendered as fact: a
// worktree path of "" and a worktree that could not be read are different
// things, and the Console needs to tell them apart. This mirrors
// TokenUsage's Known convention (ADR 0016 "no fake numbers") for
// every field that can fail.
//
// It is a string, not the design notes' `uint8` iota, for one reason: with
// an integer enum the zero value would be Known, so a Field nobody filled
// in would claim its empty value was read successfully - the exact
// falsehood this type exists to prevent. The zero FieldState is "" instead,
// which is none of the three and renders as unreadable (see Field.IsKnown).
type FieldState string

const (
	// Known: the value was read successfully and is meaningful as-is. A bad
	// but successfully read value (a missing worktree, a failed Crew) is
	// Known, not Absent and not Unknown.
	Known FieldState = "known"
	// Absent: there is legitimately nothing here - no worktree row, no
	// designated Mate, no recorded event, no previous attempt. Not a read
	// failure.
	Absent FieldState = "absent"
	// Unknown: the underlying read failed, so the value is not obtainable
	// right now. Never treat this as Absent; a retry or a repair may still
	// resolve it, and Field.Reason says what went wrong.
	Unknown FieldState = "unknown"
)

// Field carries one snapshot value together with the state of the read that
// produced it, so a UI asks "what is this field's state and reason" instead
// of re-deriving either.
//
// One Field wraps one read, not one struct member. Where a single read
// backs several values that are only meaningful together - a worktree row's
// path, branch and recorded status; a binding's status, session, tab, pane
// and bound-since - T is a small value struct and those values share the
// one state that read actually has. Splitting them into one Field each
// would triple the state a UI must reconcile and would let it render
// combinations the database cannot produce ("path known, branch unknown"),
// which is a fiction the UI would then have to invent copy for.
//
// Reason is the note that explains the state, and the query layer - not the
// UI - authors it, so `mate` command output and the Console say the same
// thing about the same row:
//   - Unknown: why the read failed ("lookup timed out (2s)"). Always set.
//   - Absent: why there is legitimately nothing ("first attempt"). Always set.
//   - Known: usually empty; set only for a caveat a UI must not drop, such
//     as a binding recorded active not proving the agent is alive
//     (ADR 0019's health observer does not exist).
type Field[T any] struct {
	State  FieldState
	Value  T
	Reason string
}

// IsKnown reports whether Value may be rendered as fact. It is deliberately
// not `State != Absent && State != Unknown`: an unset zero-valued Field is
// not Known either.
func (f Field[T]) IsKnown() bool { return f.State == Known }

// KnownField is a value that was read successfully.
func KnownField[T any](v T) Field[T] { return Field[T]{State: Known, Value: v} }

// KnownNote is a value that was read successfully but carries a caveat the
// UI must not drop.
func KnownNote[T any](v T, note string) Field[T] {
	return Field[T]{State: Known, Value: v, Reason: note}
}

// AbsentField is "the read succeeded and there is legitimately nothing
// here", with the reason a UI shows beside it.
func AbsentField[T any](reason string) Field[T] {
	return Field[T]{State: Absent, Reason: reason}
}

// UnknownField is "the read failed", with the failure reason. Callers pass
// readFailureReason(err) rather than a phrase of their own, so the reason a
// user sees is the one the store actually reported.
func UnknownField[T any](reason string) Field[T] {
	return Field[T]{State: Unknown, Reason: reason}
}

// RowKind names which level of the tree a warning's row sits at, so a UI
// can label it ("worktree of crew_01J9…") without parsing the id prefix.
type RowKind string

const (
	RowWorkspace RowKind = "workspace"
	RowProject   RowKind = "project"
	RowMate      RowKind = "mate"
	RowTask      RowKind = "task"
	RowCrew      RowKind = "crew"
)

// RowRef identifies the row a field belongs to. Label is the row's human
// name (a Project's name, a Task's title) where it has one and the id
// otherwise, so a warning can name the row without a second lookup.
type RowRef struct {
	Kind  RowKind
	ID    string
	Label string
}

// FieldWarning is one field that came back Unknown, with everything the
// footer's "N field(s) unknown: <field> of <row> (<reason>)" line needs and
// nothing it would have to invent.
type FieldWarning struct {
	// Field is the field's display name, lower case, as the line reads it:
	// "worktree", "binding", "last event".
	Field  string
	Row    RowRef
	Reason string
}

// warnings accumulates the Unknown fields of one snapshot read in tree
// order: Projects as ListProjects returned them, then each Project's Mate
// before its Tasks, each Task before its Crews, and within one row the
// fixed order the loader reads its fields in. Every underlying list is
// ordered by (created_at, rowid) in SQL, so two reads of unchanged state
// produce the same warning order - the stable ordering the footer needs to
// avoid a line that reshuffles on every refresh.
type warnings struct {
	list []FieldWarning
}

// note records f when it came back Unknown and returns it unchanged, so a
// loader can wrap the field it is already building instead of repeating the
// state check. Absent and Known are not warnings: only a failed read is.
func note[T any](w *warnings, f Field[T], field string, row RowRef) Field[T] {
	if w != nil && f.State == Unknown {
		w.list = append(w.list, FieldWarning{Field: field, Row: row, Reason: f.Reason})
	}
	return f
}

// readFailureReason turns a store error into the one-line reason a UI shows
// beside Unknown. A coded error contributes its own message (the phrasing
// the rest of `mate` already reports for that failure); anything else
// contributes err.Error(). Newlines are folded because the footer is one
// terminal line.
func readFailureReason(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	var coded *observability.Error
	if errors.As(err, &coded) && strings.TrimSpace(coded.Message) != "" {
		msg = coded.Message
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if msg == "" {
		return "read failed with an empty error message"
	}
	return msg
}
