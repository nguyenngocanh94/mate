package box

import "strings"

// State is a crew status verb, from docs/mvp.md section 4b: the three verbs
// a crew itself is allowed to write. `blocked`, `finished` and `failed` are
// not among them - they belong to the observer and to `crew stop`, and a
// crew that cannot report any more is exactly the crew that cannot write
// them either.
type State string

const (
	StateWorking       State = "working"
	StateNeedsDecision State = "needs-decision"
	StateWaitMate      State = "wait-mate"

	// StateUnknown is any status line that does not carry one of the verbs
	// above (or one of the legacy verbs below), or does not even have the
	// "verb: text" shape. It is still shown - a crew that echoes something
	// unexpected is not silence - just not recognised as a verb.
	StateUnknown State = "unknown"
)

// The pre-4b verbs, kept only so a status file written before 2026-09-18
// still reads. Nothing writes them any more: the brief template and the
// Mate's manual name three verbs.
//
//   - `done:` is the old spelling of `wait-mate:` and maps straight onto
//     it. mvp.md section 4b says so in as many words: the crew reporting is
//     not the task ending.
//   - `blocked:` from a crew is a crew that could still speak, which is a
//     `needs-decision:` by the new definition - `blocked` now means the
//     observer noticed a crew that cannot speak at all.
//   - `failed:` from a crew is a crew handing back work it could not
//     finish, which is `wait-mate:`; whether the task failed is the Mate's
//     call, recorded by `crew stop`.
//
// The two reinterpreted verbs keep the crew's own word in Status.Text, so
// nothing the crew actually said is lost by the mapping.
const (
	legacyDone    = "done"
	legacyBlocked = "blocked"
	legacyFailed  = "failed"
)

// The pull request verbs (docs/mvp.md M18, M19). A crew writes
// `pr-open:` when it opens its pull request; `mate pr watch` writes
// `pr-merged:` or `pr-closed:` when it ends. All three parse as State
// wait-mate - the crew is handed back and waiting on the Mate - and keep the
// verb they were written with in Status.Verb, so a reader that cares which
// one it was (the digest, which does not repeat what the watcher delivered)
// can tell.
const (
	VerbPROpen   = "pr-open"
	VerbPRMerged = "pr-merged"
	VerbPRClosed = "pr-closed"
)

// Status is a parsed `crews/<id>.status` line.
type Status struct {
	State State
	// Verb is the verb as written when it is one of the pull request verbs,
	// and empty otherwise.
	Verb string
	// Text is the message after the verb; the whole raw line when the line
	// did not parse as "verb: text" (State is StateUnknown), and the whole
	// raw line for a legacy `blocked:`/`failed:` line, whose original verb
	// is part of what the crew said.
	Text string
}

// ParseStatus parses one raw status line into a Status. The format is
// `<state>: <text>`, exactly what a crew's `echo "state: one line"` writes.
// A line with no colon, or whose verb is neither one of the three of
// section 4b nor one of the three legacy verbs, parses as StateUnknown
// carrying the raw line unsplit in Text - this package never guesses a verb
// it does not recognise.
func ParseStatus(line string) Status {
	verb, text, ok := strings.Cut(line, ":")
	if !ok {
		return Status{State: StateUnknown, Text: line}
	}
	verb, text = strings.TrimSpace(verb), strings.TrimSpace(text)
	switch verb {
	case string(StateWorking), string(StateNeedsDecision), string(StateWaitMate):
		return Status{State: State(verb), Text: text}
	case VerbPROpen, VerbPRMerged, VerbPRClosed:
		return Status{State: StateWaitMate, Verb: verb, Text: text}
	case legacyDone:
		return Status{State: StateWaitMate, Text: text}
	case legacyBlocked:
		return Status{State: StateNeedsDecision, Text: strings.TrimSpace(line)}
	case legacyFailed:
		return Status{State: StateWaitMate, Text: strings.TrimSpace(line)}
	default:
		return Status{State: StateUnknown, Text: line}
	}
}

// Attention reports whether a crew's own status verb is one somebody must
// act on. Among the crew verbs that is `needs-decision` and only that: the
// crew asked a question and stopped its turn, and until somebody answers it
// nothing else will happen.
//
// `wait-mate` is deliberately not attention (decision 2026-09-18, mvp.md
// section 4b): it is a report, the crews table's STATE column already
// carries it, and the user can always type into the crew's pane. `blocked`
// is not a crew verb at all - it is the observer's incident, and the inbox
// picks those up on their own.
func Attention(state State) bool { return state == StateNeedsDecision }

// LastVerb is the verb of the most recent line of a status file that
// carries one. Lines the parser does not recognise are skipped rather than
// treated as a state: a crew that echoed something malformed after saying
// `working:` is still working, and `unknown` is not a state any more
// (mvp.md section 4b).
//
// It returns StateUnknown when the file holds no recognisable verb at all,
// which a caller reads as "the crew has said nothing yet".
func LastVerb(lines []string) State {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if st := ParseStatus(lines[i]); st.State != StateUnknown {
			return st.State
		}
	}
	return StateUnknown
}
