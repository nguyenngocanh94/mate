package console

import (
	"fmt"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// Status words (design I, "8 Status words"). Status is a word, tinted only
// when it needs somebody; the recorded word is what a two-line row and
// detail show, and the one-line row gets a word of at most seven cells.

// shortStatusWidth is the one-line row's status column.
const shortStatusWidth = 7

// statusTok is the tint of a recorded status word.
func statusTok(word string) tok {
	switch word {
	case string(query.CrewNeedsDecision), string(query.CrewWaitMate), string(query.CrewBlocked),
		string(query.MateUnknown), "stale", "no mate":
		return tAmber
	case string(query.CrewFailed), "missing":
		return tRed
	case string(query.CrewFinished):
		return tGreen
	}
	return tDim
}

// shortStatus is a recorded status word cut to the one-line column: a word
// that already fits stays itself.
func shortStatus(word string) string {
	switch word {
	case string(query.CrewNeedsDecision):
		return "decide"
	case string(query.CrewWaitMate):
		return "review"
	case string(query.CrewFinished):
		return "done"
	case string(query.MateStarting):
		return "start"
	case string(query.MateStopping):
		return "stop"
	}
	if cells(word) > shortStatusWidth {
		return cutCells(word, shortStatusWidth)
	}
	return word
}

// mateWord is the Mate's status word: its recorded status, "no mate" when
// the Project has none, and "unknown" when the record could not be read.
func mateWord(mate query.MateNode) string {
	switch mate.Designated.State {
	case query.Absent:
		return "no mate"
	case query.Unknown:
		return "unknown"
	}
	if mate.Designated.Value.MateID == "" {
		return "no mate"
	}
	return string(mate.Designated.Value.Status)
}

// mateHarness is the Mate's recorded harness kind, or "".
func mateHarness(mate query.MateNode) string {
	if mate.Designated.IsKnown() {
		return string(mate.Designated.Value.HarnessKind)
	}
	return ""
}

// crewWord is a Crew's declared state (mvp.md section 4b).
func crewWord(c query.CrewNode) string {
	if c.Status == "" {
		return "unknown"
	}
	return string(c.Status)
}

// crewNeedsCaptain reports whether a Crew is something the captain has to
// act on: the query layer's own attention (query/attention.go is the one
// definition of "needs attention").
func crewNeedsCaptain(c query.CrewNode) bool { return c.Attention.IsKnown() }

// crewFailed reports whether a Crew is recorded failed.
func crewFailed(c query.CrewNode) bool { return c.Status == query.CrewFailed }

// projectAttention is the !N of a Project row: its Crews needing attention
// plus one for the Project's own problem (no Mate, a stale or unknown
// Mate), and whether one of them failed - red when one failed, amber
// otherwise (design A). An unreadable attention counts as one: something
// has to be looked at.
func projectAttention(p query.ProjectNode) (n int, failed bool) {
	switch p.Attention.State {
	case query.Unknown:
		n = 1
	case query.Known:
		n = p.Attention.Value.CrewsNeedingAttention
		if p.Attention.Value.Kind != "" {
			n++
		}
	}
	for _, c := range p.Crews {
		if crewFailed(c) {
			failed = true
		}
	}
	return n, failed
}

// bangTok is the tint of an attention count.
func bangTok(failed bool) tok {
	if failed {
		return tRed
	}
	return tAmber
}

// age is a short duration: 20s, 38m, 3h, 2d.
func age(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// since is the age of t at the snapshot's own read time, never a UI clock.
func (m Model) since(t time.Time) string {
	if t.IsZero() || m.tree.AsOf.IsZero() {
		return ""
	}
	return age(m.tree.AsOf.Sub(t))
}

// tokensWord is a token total: 182k, 1.2M.
func tokensWord(total int64) string {
	switch {
	case total >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(total)/1_000_000)
	case total >= 1_000:
		return fmt.Sprintf("%dk", total/1_000)
	}
	return fmt.Sprintf("%d", total)
}
