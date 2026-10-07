package autopilot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/box"
	"github.com/nguyenngocanh94/mate/internal/outbox"
)

// MateCrew is the crew field the daemon's own incidents carry. The Mate is
// not a crew, but `incidents.log` has exactly one identity per line - a
// (crew, kind) pair, mvp.md section 4b - and a wedged composer is a finding
// about the Mate's pane, so this is the name it is filed under.
//
// A crew could legally be called `mate` (store.ValidateCrewID allows it), so
// the digest skips only the one combination the daemon itself writes: crew
// `mate` with kind `wedged`. A crew of that name that actually went stale is
// still reported.
const MateCrew = outbox.MateCrew

// ItemKind is what one digested thing is. The first three spellings are the
// ones mvp.md section 4b uses on screen, so a Mate reading a digest sees the
// same words as a human reading the crews table; ItemBudget is task 27's
// fourth shape, which is deliberately not "blocked" (a budget incident never
// makes a crew blocked, decision 2026-09-20).
type ItemKind string

const (
	// ItemNeedsDecision is a crew's own question, still unanswered.
	ItemNeedsDecision ItemKind = "needs-decision"
	// ItemBlocked is an open observer incident of a blocking kind (`stale`
	// or `runtime_lost`) - the crew cannot report for itself any more.
	ItemBlocked ItemKind = "blocked"
	// ItemWaitMate is a crew's latest status line handing the work back.
	ItemWaitMate ItemKind = "wait-mate"
	// ItemBudget is an open `budget` incident: the crew (or, with no crew
	// name, the project) has crossed a limit in project.yaml's `budget:`
	// block. It is its own kind, not ItemBlocked, because the digest's
	// vocabulary must not tell the Mate a budget item changed the crew's
	// state - it did not.
	ItemBudget ItemKind = "over budget"
)

// Item is one thing a digest reports, with the entry it came from kept so a
// caller can advance the cursor past exactly what it sent.
type Item struct {
	Kind ItemKind
	// Crew is the crew the item is about, empty only for an incident the
	// observer could not attribute.
	Crew string
	// Text is the crew's own words for a status item, and the observer's
	// evidence for an incident.
	Text string
	// IncidentKind is the incident's kind for ItemBlocked, empty otherwise.
	IncidentKind box.IncidentKind
	// QuietFor is how long an ItemBlocked incident has been open. It is
	// measured from the `open` line the observer wrote, so it is a lower
	// bound on how long the crew has actually been quiet: the observer only
	// opens `stale` after its own threshold has already passed.
	QuietFor time.Duration
	// File and Offset are where the line lives, which is what the cursor
	// records.
	File   string
	Offset int64
}

// Limits on the one line. A status line is one line by protocol (mvp.md
// section 4) but nothing bounds its length, and a composer handed a very long
// line wraps and re-flows - which is exactly what send.Send's verification
// then cannot read back. The files the line points at hold the whole of
// everything it quotes.
const (
	// MaxItemRunes bounds each quoted crew line.
	MaxItemRunes = 120
	// MaxItemsListed bounds how many items are spelled out. The count before
	// the dash is always the true one, and the remainder is named as
	// "+N more" - a digest that listed forty items would be a line no
	// composer could take and no Mate could act on in one turn.
	MaxItemsListed = 5
)

// Gather is everything one tick has to report for a project: the unresolved
// inbox plus each open crew's latest `wait-mate`, minus everything at or
// before the cursor, in the merged view's own oldest-first order.
//
// The `wait-mate` rule is deliberately narrow - the crew's *latest* status
// line and nothing else. An older `wait-mate` that the crew has since
// followed with another line is history: the crew moved on, and re-reporting
// it would be the daemon telling the Mate about a hand-back that has already
// been picked up. It also bounds the cold-start case to one line per crew: a
// project whose auto flag is switched on after a week of work digests its
// open questions and each crew's current hand-back, not the whole log.
func Gather(v box.View, cursor map[string]int64, now time.Time) []Item {
	fresh := func(e box.Entry) bool {
		last, ok := cursor[e.Ref.File]
		return !ok || e.Ref.Offset > last
	}

	inbox := make(map[int]box.Item, len(v.Entries))
	for _, it := range box.Inbox(v) {
		if it.State == box.StateWaitMate {
			// A manual view's inbox carries wait-mate for the captain;
			// the rule below is what digests it.
			continue
		}
		inbox[it.Entry.Seq] = it
	}

	waiting := make(map[int]bool)
	for crew, entries := range v.ByCrew {
		if v.Closed[crew] {
			continue
		}
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].Kind != box.KindStatus {
				continue
			}
			if box.ParseStatus(entries[i].Text).State == box.StateWaitMate {
				waiting[entries[i].Seq] = true
			}
			break
		}
	}

	var out []Item
	for _, e := range v.Entries {
		if !fresh(e) {
			continue
		}
		item, ok := inbox[e.Seq]
		switch {
		case ok && item.Incident():
			if e.Crew == MateCrew && item.Kind == box.IncidentWedged {
				// The daemon's own finding about the Mate's composer. The
				// Mate can do nothing with it, and sending it would mean
				// reporting a delivery failure through the delivery that
				// just failed. The captain sees it in the inbox.
				continue
			}
			kind := ItemBlocked
			if item.Kind == box.IncidentBudget {
				// Never ItemBlocked: a budget incident does not change the
				// crew's state (decision 2026-09-20), and the digest's own
				// vocabulary must not suggest otherwise.
				kind = ItemBudget
			}
			out = append(out, Item{
				Kind:         kind,
				Crew:         e.Crew,
				Text:         item.Text,
				IncidentKind: item.Kind,
				QuietFor:     now.Sub(e.At),
				File:         e.Ref.File,
				Offset:       e.Ref.Offset,
			})
		case ok:
			out = append(out, Item{
				Kind:   ItemNeedsDecision,
				Crew:   e.Crew,
				Text:   item.Text,
				File:   e.Ref.File,
				Offset: e.Ref.Offset,
			})
		case waiting[e.Seq]:
			out = append(out, Item{
				Kind:   ItemWaitMate,
				Crew:   e.Crew,
				Text:   box.ParseStatus(e.Text).Text,
				File:   e.Ref.File,
				Offset: e.Ref.Offset,
			})
		}
	}
	return out
}

// Advance returns the cursor a caller should record once the items have been
// delivered: the previous cursor with each file moved to the last offset
// digested out of it. Files that contributed nothing keep the offset they
// had, so a cursor never moves backwards and never skips a line nobody sent.
func Advance(cursor map[string]int64, items []Item) map[string]int64 {
	next := make(map[string]int64, len(cursor)+len(items))
	for file, offset := range cursor {
		next[file] = offset
	}
	for _, item := range items {
		if last, ok := next[item.File]; !ok || item.Offset > last {
			next[item.File] = item.Offset
		}
	}
	return next
}

// Key is the outbox dedup key of a digest (store.OutboxItem.Key): the exact
// set of source lines it reports, so a tick that finds the same items as the
// digest already queued leaves that digest alone, and a tick that finds a
// different set refreshes it (outbox.Sender.Offer).
func Key(items []Item) string {
	h := sha256.New()
	for _, item := range items {
		fmt.Fprintf(h, "%s@%d\n", item.File, item.Offset)
	}
	return "digest:" + hex.EncodeToString(h.Sum(nil))[:16]
}

// Line is the one line a digest becomes, without the from-app marker
// (send.Send prefixes that). It is spelled once, here, so the daemon, the
// spec (mvp.md section 5) and the manual task 20 writes for the Mate all
// agree on the shape the Mate is taught to read:
//
//	digest: <k> item(s) — <crew> <verb>: … · <crew> <verb>: … — status files under <dir>; act per AGENTS.md section 10
//
// crewsDir is absolute because the Mate's cwd is its own `mate/` directory,
// not the project's, so `crews/<id>.status` resolves to nothing there - the
// same reason the `[resolve]` line carries an absolute path
// (query.BoxResolveLine).
func Line(items []Item, crewsDir string) string {
	shown := items
	extra := 0
	if len(shown) > MaxItemsListed {
		extra = len(shown) - MaxItemsListed
		shown = shown[:MaxItemsListed]
	}
	parts := make([]string, 0, len(shown)+1)
	for _, item := range shown {
		parts = append(parts, itemText(item))
	}
	if extra > 0 {
		parts = append(parts, fmt.Sprintf("+%d more", extra))
	}
	return fmt.Sprintf("digest: %d item(s) — %s — status files under %s; act per AGENTS.md section 10",
		len(items), strings.Join(parts, " · "), crewsDir)
}

func itemText(item Item) string {
	crew := item.Crew
	if crew == "" {
		// An incident the observer could not attribute to a crew. The dash
		// is honest: the record names no crew, and inventing one would send
		// the Mate looking for a status file that does not exist.
		crew = "-"
	}
	switch item.Kind {
	case ItemBlocked:
		kind := string(item.IncidentKind)
		if kind == "" {
			kind = "incident"
		}
		return fmt.Sprintf("%s blocked: %s, quiet for %s", crew, kind, quietWord(item.QuietFor))
	case ItemBudget:
		// item.Text is the observer's incident text, written as
		// "<total> of <limit>" (internal/watch's budget check) - the fourth
		// item shape of mvp.md section 5: "<crew> over budget: <total> of
		// <limit>".
		return fmt.Sprintf("%s over budget: %s", crew, item.Text)
	default:
		return fmt.Sprintf("%s %s: %q", crew, item.Kind, oneLine(item.Text, MaxItemRunes))
	}
}

// quietWord renders a duration the way the health column does: rounded to the
// second, and never negative - a clock that went backwards between the
// observer's write and this read is not evidence of a crew waiting less than
// no time.
func quietWord(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}

// oneLine flattens text to a single line of at most n runes: every run of
// whitespace collapses to one space, and a double quote becomes a single one
// so a quoted crew line cannot close its own quotes.
func oneLine(text string, n int) string {
	text = strings.Join(strings.Fields(strings.ReplaceAll(text, "\"", "'")), " ")
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return strings.TrimRight(string(r[:n]), " ") + "…"
}
