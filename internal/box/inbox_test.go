package box_test

import (
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/box"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// The inbox rule table. Each case builds a real workspace through store -
// the same appends a crew and `matev2 send` make - and asserts exactly which
// items survive box.Inbox. The two rules under test are the ones inbox.go
// documents: a later status line from the same crew (exact), and a sent.log
// line addressed to that crew after the question's time (approximate, and
// the approximation is what the last two cases pin).

// step is one append to the fixture workspace: a crew status line, or a
// sent.log line. runInbox applies a sequence of them and returns the inbox.
type step struct {
	crew   string // "" for a sent.log line
	status string
	// a sent.log line: who sent it and to which target
	source string
	target string
	text   string
	at     time.Time
}

func runInbox(t *testing.T, incidents []box.Incident, steps ...step) []box.Item {
	t.Helper()
	w := newFixtureWorkspace(t)
	for _, s := range steps {
		if s.crew != "" {
			if err := w.AppendStatus("shop", s.crew, s.status); err != nil {
				t.Fatalf("AppendStatus(%s, %q): %v", s.crew, s.status, err)
			}
			// A status file's mtime is the only time signal box has, and the
			// filesystem's resolution is coarse enough that two appends in
			// the same millisecond are indistinguishable from a reply that
			// landed between them. Space them.
			time.Sleep(15 * time.Millisecond)
			continue
		}
		at := s.at
		if at.IsZero() {
			at = time.Now()
		}
		if err := w.AppendSent("shop", store.SentEntry{
			Time: at, Source: s.source, Target: s.target, Text: s.text,
		}); err != nil {
			t.Fatalf("AppendSent(%s -> %s): %v", s.source, s.target, err)
		}
		time.Sleep(15 * time.Millisecond)
	}
	v, err := box.Load(w, "shop", incidents)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return box.Inbox(v)
}

func itemStates(items []box.Item) []string {
	out := make([]string, 0, len(items))
	for _, i := range items {
		if i.Incident() {
			out = append(out, i.Crew()+" incident:"+string(i.Kind))
			continue
		}
		out = append(out, i.Crew()+" "+string(i.State))
	}
	return out
}

func assertInbox(t *testing.T, items []box.Item, want ...string) {
	t.Helper()
	got := itemStates(items)
	if len(got) != len(want) {
		t.Fatalf("inbox = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("inbox = %v, want %v", got, want)
		}
	}
}

// TestInboxKeepsOnlyUnansweredQuestions is the headline case the user's
// feedback asks for: a box full of working/done lines and chat messages has
// an empty-but-for-the-question inbox.
func TestInboxKeepsOnlyUnansweredQuestions(t *testing.T) {
	items := runInbox(t, nil,
		step{crew: "k3", status: "working: reading the ticket"},
		step{source: store.SourceUser, target: store.TargetMate, text: "spawn a crew"},
		step{crew: "k3", status: "working: still reading"},
		step{crew: "k3", status: "needs-decision: SQLite or Postgres?"},
	)
	assertInbox(t, items, "k3 needs-decision")
	if got := items[0].Text; got != "SQLite or Postgres?" {
		t.Fatalf("item text = %q, want the crew's own question", got)
	}
}

// TestInboxBlockedIsAlsoAQuestion: blocked is the other verb that stops a
// crew waiting on somebody.
func TestInboxBlockedIsAlsoAQuestion(t *testing.T) {
	assertInbox(t, runInbox(t, nil,
		step{crew: "k3", status: "blocked: no credentials for the staging API"},
	), "k3 blocked")
}

// TestInboxDoneAndFailedAreNotQuestions: an outcome is not a decision. The
// crews table's STATUS column is where those belong.
func TestInboxDoneAndFailedAreNotQuestions(t *testing.T) {
	assertInbox(t, runInbox(t, nil,
		step{crew: "k3", status: "done: PR ready"},
		step{crew: "k9", status: "failed: the build never went green"},
	))
}

// TestInboxResolvedByUserReply is rule 2 with the user as the replier: `r`
// in the rail records a user line to `crew:k3`, and the question goes.
func TestInboxResolvedByUserReply(t *testing.T) {
	assertInbox(t, runInbox(t, nil,
		step{crew: "k3", status: "needs-decision: A or B?"},
		step{source: store.SourceUser, target: store.CrewTarget("k3"), text: "A"},
	))
}

// TestInboxResolvedByMateReply is rule 2 with the Mate as the replier - the
// whole point of `resolve:`. `matev2 send` records Source: mate when it runs
// in the Mate's own pane, and that is what closes the item.
func TestInboxResolvedByMateReply(t *testing.T) {
	assertInbox(t, runInbox(t, nil,
		step{crew: "k3", status: "needs-decision: A or B?"},
		step{source: store.SourceMate, target: store.CrewTarget("k3"), text: "go with A"},
	))
}

// TestInboxReplyToAnotherCrewResolvesNothing: a reply is addressed, and an
// answer to k9 says nothing about k3's question.
func TestInboxReplyToAnotherCrewResolvesNothing(t *testing.T) {
	assertInbox(t, runInbox(t, nil,
		step{crew: "k3", status: "needs-decision: A or B?"},
		step{source: store.SourceUser, target: store.CrewTarget("k9"), text: "A"},
	), "k3 needs-decision")
}

// TestInboxReplyToTheMateResolvesNothing: the `resolve:` line itself goes to
// `mate`, not to `crew:<id>`. Handing the question to the Mate is not an
// answer to the crew, and an inbox that emptied on the forward would hide
// exactly the item whose answer is still outstanding.
func TestInboxReplyToTheMateResolvesNothing(t *testing.T) {
	assertInbox(t, runInbox(t, nil,
		step{crew: "k3", status: "needs-decision: A or B?"},
		step{source: store.SourceApp, target: store.TargetMate, text: "resolve: k3 asked ..."},
	), "k3 needs-decision")
}

// TestInboxResolvedByTheCrewMovingOn is rule 1, the exact one: the crew
// appended something after its question, so it is no longer waiting.
func TestInboxResolvedByTheCrewMovingOn(t *testing.T) {
	assertInbox(t, runInbox(t, nil,
		step{crew: "k3", status: "needs-decision: A or B?"},
		step{crew: "k3", status: "done: chose A"},
	))
}

// TestInboxKeepsTheNewestQuestionWhenTheCrewAsksTwice: rule 1 resolves the
// older question - the crew moved past it - and leaves the newest one, which
// is the one still waiting.
func TestInboxKeepsTheNewestQuestionWhenTheCrewAsksTwice(t *testing.T) {
	items := runInbox(t, nil,
		step{crew: "k3", status: "needs-decision: A or B?"},
		step{crew: "k3", status: "blocked: the staging API is down"},
	)
	assertInbox(t, items, "k3 blocked")
}

// TestInboxSeparatesCrews: two crews, one answered and one not.
func TestInboxSeparatesCrews(t *testing.T) {
	assertInbox(t, runInbox(t, nil,
		step{crew: "k3", status: "needs-decision: A or B?"},
		step{crew: "k9", status: "blocked: waiting on review"},
		step{source: store.SourceMate, target: store.CrewTarget("k3"), text: "A"},
	), "k9 blocked")
}

// TestInboxIncidentIsOpenUntilTheCrewSpeaks: an incident has no answer of
// its own, so the crew writing anything at all is what closes it.
func TestInboxIncidentIsOpenUntilTheCrewSpeaks(t *testing.T) {
	open := runInbox(t, []box.Incident{{
		At: time.Now().Add(-time.Hour), Crew: "k3", Kind: box.IncidentStale, Text: "no status for 20m",
	}},
		step{crew: "k3", status: "working: reading the ticket"},
	)
	// The status line is newer than the incident, so it closed it.
	assertInbox(t, open)

	// The other way round: the incident is the newest thing about k3.
	still := runInbox(t, []box.Incident{{
		At: time.Now().Add(time.Hour), Crew: "k3", Kind: box.IncidentStale, Text: "no status for 20m",
	}},
		step{crew: "k3", status: "working: reading the ticket"},
	)
	assertInbox(t, still, "k3 incident:stale")
	if still[0].Text != "no status for 20m" {
		t.Fatalf("incident text = %q, want the observer's own", still[0].Text)
	}
}

// TestInboxIsOldestFirst: the rail draws the newest at the bottom, so the
// order Inbox returns has to be the order Entries is in.
func TestInboxIsOldestFirst(t *testing.T) {
	items := runInbox(t, nil,
		step{crew: "k3", status: "needs-decision: A or B?"},
		step{crew: "k9", status: "blocked: waiting on review"},
	)
	assertInbox(t, items, "k3 needs-decision", "k9 blocked")
	if !items[0].Entry.At.Before(items[1].Entry.At) && items[0].Entry.Seq > items[1].Entry.Seq {
		t.Fatalf("inbox is not oldest-first: %+v", items)
	}
}

// TestInboxDoesNotShrinkTheView is the boundary this whole change rests on:
// the filter is a view over the merge, and the merge keeps everything.
func TestInboxDoesNotShrinkTheView(t *testing.T) {
	w := newFixtureWorkspace(t)
	for _, s := range []string{"working: a", "needs-decision: A or B?", "done: chose A"} {
		if err := w.AppendStatus("shop", "k3", s); err != nil {
			t.Fatalf("AppendStatus: %v", err)
		}
	}
	v, err := box.Load(w, "shop", nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(v.Entries) != 3 {
		t.Fatalf("Entries = %d, want all 3 status lines kept", len(v.Entries))
	}
	if got := box.Inbox(v); len(got) != 0 {
		t.Fatalf("inbox = %v, want empty: the crew moved on twice", itemStates(got))
	}
}
