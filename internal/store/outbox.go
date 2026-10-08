package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// The Mate's outbox (docs/mvp.md task 30): every line the app means to type
// into a Mate's composer on its own initiative - an `[assign]` the captain
// pressed, a digest the auto daemon built - waits here until internal/outbox
// has verified it into an empty composer.
//
// # Format
//
// `mate/.outbox` holds one JSON object per line, oldest first (OutboxItem).
// The file is rewritten whole and atomically (temp file plus rename) on every
// change, and never appended to.
//
// # Why rewritten, not append-only
//
// An item changes state in place, and often: a queued item gets `attempts`
// and `last_refusal` updated on every two-second retry while the Mate is
// mid-turn, which task 24 measured lasting minutes. Append-only would mean
// one line per retry, and then every reader - the sender, the auto daemon,
// the inbox row in internal/query - folding the history back into current
// state, plus a compaction pass anyway so the file does not grow by a line
// every two seconds for as long as a Mate is busy. The queue is small (what
// is waiting, plus a bounded tail of what was settled), so rewriting it whole
// is cheap, and a rename gives every reader a complete file with no lock at
// all - which matters, because the console's refresh reads it on the UI's
// load path while a send, which holds the writers' lock for a second or two,
// may be in flight.
//
// Writers are serialised by an exclusive flock on a sibling lock file,
// `mate/.outbox.lock`, not on `.outbox` itself: a lock on a file that is then
// replaced by rename protects the old inode, not the name. The lock is held
// across the read, the caller's change and the write (UpdateOutbox), which is
// what lets a caller hold it across a send and makes "never type one item
// twice" hold across goroutines and across two consoles over one workspace.
//
// # Compaction
//
// Every write keeps every queued item, and of the settled ones (sent or
// dropped) only those settled within OutboxRetention, and at most
// OutboxKeepSettled of them, newest first. A settled item is kept at all for
// two readers: the dedup of a second `[assign]` on the same inbox item
// ("already assigned 14:32"), and the inbox row that says the item was
// handed over. Both degrade honestly when the item has been compacted away:
// the row loses its suffix, and a later assign is queued again.

// The states an outbox item is in.
const (
	// OutboxQueued is waiting for an empty composer.
	OutboxQueued = "queued"
	// OutboxSent was verified into the composer and recorded in sent.log.
	OutboxSent = "sent"
	// OutboxDropped was withdrawn without being typed: a digest whose
	// project left auto mode before it could be delivered, or one with
	// nothing left to say.
	OutboxDropped = "dropped"
)

// The producers of an outbox item.
const (
	OutboxSourceAssign = "assign"
	OutboxSourceDigest = "digest"
	// OutboxSourceStow is the `⟦mate⟧ stow:` line the app sends just
	// before it restarts the Mate (docs/mvp.md task 37, B7).
	OutboxSourceStow = "stow"
	// OutboxSourcePR is the line `mate pr watch` queues when a crew's pull
	// request is merged or closed (docs/mvp.md M18). Unlike a digest it is
	// delivered in manual mode too.
	OutboxSourcePR = "pr"
)

// Compaction bounds (see the package comment above).
const (
	OutboxRetention   = 24 * time.Hour
	OutboxKeepSettled = 128
)

// OutboxItem is one line of `mate/.outbox`.
type OutboxItem struct {
	// ID is unique within the file and increases with every item.
	ID int64 `json:"id"`
	// At is when the item was queued. The `wedged` clock runs from the
	// oldest queued item's At, so it survives a console restart.
	At time.Time `json:"at"`
	// Source is OutboxSourceAssign or OutboxSourceDigest.
	Source string `json:"source"`
	// Key is the dedup key. For an assign it names the inbox entry
	// (`crews/k3.status@120`), so the same question is never queued twice;
	// for a digest it names the exact set of items the line reports.
	Key string `json:"key"`
	// Text is the line without the from-app marker, exactly as sent.log
	// records it once delivered.
	Text  string `json:"text"`
	State string `json:"state"`
	// SentAt is when the line was verified into the composer.
	SentAt time.Time `json:"sent_at,omitempty"`
	// Attempts counts every try that reached the point of looking at the
	// Mate's pane.
	Attempts int `json:"attempts"`
	// LastRefusal is why the last attempt did not deliver, and TriedAt when
	// that attempt was made.
	LastRefusal string    `json:"last_refusal,omitempty"`
	TriedAt     time.Time `json:"tried_at,omitempty"`
	// SentLogFrom is sent.log's size when the item was queued. A crash
	// between the verified send (which writes sent.log first) and the
	// rewrite that marks the item sent leaves the item queued; the sender
	// looks for its line in sent.log from this offset before it types,
	// and marks it sent instead of typing it a second time.
	SentLogFrom int64 `json:"sent_log_from,omitempty"`
	// Cursor is, for a digest, the auto cursor (ReadAutoCursor's shape,
	// absolute paths) to record once the digest is delivered - and only
	// then (mvp.md section 5: only a verified send advances the cursor).
	Cursor map[string]int64 `json:"cursor,omitempty"`
}

// Queued reports whether the item is still waiting.
func (i OutboxItem) Queued() bool { return i.State == OutboxQueued }

// OutboxFile is `projects/<project>/mate/.outbox`.
func (w *Workspace) OutboxFile(project string) string {
	return filepath.Join(w.MateDir(project), outboxName)
}

func (w *Workspace) outboxLockFile(project string) string {
	return filepath.Join(w.MateDir(project), outboxLockName)
}

// ReadOutbox returns a project's outbox, oldest first. A missing file is an
// empty outbox. It takes no lock: the file is only ever replaced by rename,
// so a reader sees one whole version of it. A line that does not parse is
// skipped rather than failing the read, like every other log reader here.
func (w *Workspace) ReadOutbox(project string) ([]OutboxItem, error) {
	if err := ValidateProjectName(project); err != nil {
		return nil, err
	}
	return readOutboxFile(w.OutboxFile(project))
}

func readOutboxFile(path string) ([]OutboxItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []OutboxItem
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var item OutboxItem
		if err := json.Unmarshal(line, &item); err != nil || item.State == "" {
			continue
		}
		out = append(out, item)
	}
	return out, scanner.Err()
}

// UpdateOutbox runs change over the current outbox while holding the
// writers' lock, and writes back what it returns when it reports a change.
// The lock is held for the whole call, so change may do slow work - a
// verified send - and no other writer, in this process or another, can act
// on the same items meanwhile.
//
// now is the compaction clock. Items the caller appends need an ID; NextOutboxID
// gives one.
func (w *Workspace) UpdateOutbox(project string, now time.Time,
	change func(items []OutboxItem) ([]OutboxItem, bool, error)) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	lockPath := w.outboxLockFile(project)
	if _, err := w.resolve(lockPath); err != nil {
		return err
	}
	if err := w.mkdirAll(filepath.Dir(lockPath)); err != nil {
		return err
	}
	lock, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lockFile(lock, true); err != nil {
		return err
	}
	defer unlockFile(lock)

	items, err := readOutboxFile(w.OutboxFile(project))
	if err != nil {
		return err
	}
	next, changed, err := change(items)
	if err != nil || !changed {
		return err
	}
	return w.writeOutbox(project, compactOutbox(next, now))
}

// NextOutboxID is one more than the largest ID in items.
func NextOutboxID(items []OutboxItem) int64 {
	var max int64
	for _, item := range items {
		if item.ID > max {
			max = item.ID
		}
	}
	return max + 1
}

func (w *Workspace) writeOutbox(project string, items []OutboxItem) error {
	var b bytes.Buffer
	for _, item := range items {
		line, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("store: outbox item %d: %w", item.ID, err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return w.writeFile(w.OutboxFile(project), b.Bytes(), 0o644)
}

// compactOutbox applies the retention rule of the package comment, keeping
// the survivors in their original order.
func compactOutbox(items []OutboxItem, now time.Time) []OutboxItem {
	type settled struct {
		index int
		at    time.Time
	}
	var done []settled
	for i, item := range items {
		if item.Queued() {
			continue
		}
		at := item.SentAt
		if at.IsZero() {
			at = item.At
		}
		done = append(done, settled{index: i, at: at})
	}
	sort.SliceStable(done, func(a, b int) bool { return done[a].at.After(done[b].at) })
	keep := make(map[int]bool, len(done))
	for n, d := range done {
		if n >= OutboxKeepSettled || now.Sub(d.at) > OutboxRetention {
			continue
		}
		keep[d.index] = true
	}
	out := make([]OutboxItem, 0, len(items))
	for i, item := range items {
		if item.Queued() || keep[i] {
			out = append(out, item)
		}
	}
	return out
}

// SentLogSize is the current size of sent.log, zero when it does not exist:
// the offset an item records as SentLogFrom.
func (w *Workspace) SentLogSize(project string) (int64, error) {
	if err := ValidateProjectName(project); err != nil {
		return 0, err
	}
	info, err := os.Stat(w.SentLog(project))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}
