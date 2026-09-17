package box

import "strings"

// State is a crew status verb, from docs/mvp.md section 4: "Crew -> Mate".
type State string

const (
	StateWorking       State = "working"
	StateNeedsDecision State = "needs-decision"
	StateBlocked       State = "blocked"
	StateDone          State = "done"
	StateFailed        State = "failed"

	// StateUnknown is any status line that does not carry one of the five
	// verbs above, or does not even have the "verb: text" shape. It is still
	// shown - a crew that echoes something unexpected is not silence - just
	// not recognised as one of the five.
	StateUnknown State = "unknown"
)

// Status is a parsed `crews/<id>.status` line.
type Status struct {
	State State
	// Text is the message after the verb, or the whole raw line when the
	// line did not parse as "verb: text" (State is StateUnknown in that
	// case): a malformed line is still shown, just not split.
	Text string
}

// ParseStatus parses one raw status line into a Status. The format is
// `<state>: <text>`, exactly what a crew's `echo "state: one line"` writes.
// A line with no colon, or whose verb is not one of the five in section 4,
// parses as StateUnknown carrying the raw line unsplit in Text - this
// package never guesses a verb it does not recognise.
func ParseStatus(line string) Status {
	verb, text, ok := strings.Cut(line, ":")
	if !ok {
		return Status{State: StateUnknown, Text: line}
	}
	switch State(strings.TrimSpace(verb)) {
	case StateWorking, StateNeedsDecision, StateBlocked, StateDone, StateFailed:
		return Status{State: State(strings.TrimSpace(verb)), Text: strings.TrimSpace(text)}
	default:
		return Status{State: StateUnknown, Text: line}
	}
}

// Attention reports whether a state is one Mate must see, per firstmate's
// captain-relevant verb set (bin/fm-classify-lib.sh
// FM_CLASSIFY_CAPTAIN_RE_DEFAULT, restricted to the five verbs section 4
// keeps): needs-decision, blocked, done, failed. working - and anything
// StateUnknown - is not attention on its own.
func Attention(state State) bool {
	switch state {
	case StateNeedsDecision, StateBlocked, StateDone, StateFailed:
		return true
	default:
		return false
	}
}
