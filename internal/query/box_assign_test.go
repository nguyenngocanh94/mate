package query

import (
	"testing"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

// An inbox entry's assign state is the Mate's outbox read for that entry's
// key (mvp.md task 30): queued, then sent with its time. Only the entry the
// line was queued for is marked, a digest never marks a row (it is not the
// captain's [assign]), and a dropped item is an assign that never happened.
func TestBoxEntriesCarryTheirAssignFromTheOutbox(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.AppendStatus("shop", "k3", "needs-decision: pick A or B"); err != nil {
		t.Fatal(err)
	}
	if err := ws.AppendStatus("shop", "k9", "needs-decision: rebase or merge"); err != nil {
		t.Fatal(err)
	}
	inbox := LoadBox(ws, "shop").Value.Inbox
	if len(inbox) != 2 {
		t.Fatalf("inbox = %+v, want two questions", inbox)
	}
	k3, k9 := inbox[0], inbox[1]
	if k3.AssignKey != "crews/k3.status@0" || k9.AssignKey != "crews/k9.status@0" {
		t.Fatalf("assign keys = %q, %q; want each status line's file and offset", k3.AssignKey, k9.AssignKey)
	}
	for _, e := range inbox {
		if e.Assigned.State != "" {
			t.Fatalf("entry %s = %+v before anything was queued", e.Crew, e.Assigned)
		}
	}

	at := time.Date(2026, 9, 24, 14, 32, 0, 0, time.UTC)
	write := func(items []store.OutboxItem) {
		t.Helper()
		if err := ws.UpdateOutbox("shop", at, func([]store.OutboxItem) ([]store.OutboxItem, bool, error) {
			return items, true, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	write([]store.OutboxItem{
		{ID: 1, At: at, Source: store.OutboxSourceAssign, Key: k3.AssignKey, Text: k3.Resolve, State: store.OutboxQueued},
		{ID: 2, At: at, Source: store.OutboxSourceDigest, Key: k9.AssignKey, Text: "digest: …", State: store.OutboxSent, SentAt: at},
	})
	inbox = LoadBox(ws, "shop").Value.Inbox
	if got := inbox[0].Assigned; got.State != BoxAssignQueued || !got.At.Equal(at) {
		t.Fatalf("k3 = %+v, want queued at %s", got, at)
	}
	if got := inbox[1].Assigned; got.State != "" {
		t.Fatalf("k9 = %+v, want nothing: a digest is not an assign", got)
	}

	sentAt := at.Add(3 * time.Minute)
	write([]store.OutboxItem{
		{ID: 1, At: at, Source: store.OutboxSourceAssign, Key: k3.AssignKey, Text: k3.Resolve, State: store.OutboxSent, SentAt: sentAt},
		{ID: 3, At: at, Source: store.OutboxSourceAssign, Key: k9.AssignKey, Text: k9.Resolve, State: store.OutboxDropped},
	})
	box := LoadBox(ws, "shop").Value
	if got := box.Inbox[0].Assigned; got.State != BoxAssignSent || !got.SentAt.Equal(sentAt) {
		t.Fatalf("k3 = %+v, want sent at %s", got, sentAt)
	}
	if got := box.Inbox[1].Assigned; got.State != "" {
		t.Fatalf("k9 = %+v, want nothing for a dropped item", got)
	}
	// The same row in [all] carries the same state: one flattener.
	for _, e := range box.Entries {
		if e.Seq == box.Inbox[0].Seq && e.Assigned != box.Inbox[0].Assigned {
			t.Fatalf("[all] entry %+v disagrees with its inbox row %+v", e.Assigned, box.Inbox[0].Assigned)
		}
	}
}
