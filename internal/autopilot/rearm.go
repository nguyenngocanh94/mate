package autopilot

import (
	"fmt"
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// DefaultQuietAfter is how long the captain has to leave a finished Mate
// alone before the daemon turns auto mode back on (store.QuietAfter).
const DefaultQuietAfter = store.QuietAfter

// RearmText is the sent.log line the daemon appends when it turns auto mode
// back on, the counterpart of hook.AutoOffText.
func RearmText(quiet time.Duration) string {
	return fmt.Sprintf("auto mode on: the captain has been quiet for %s since the Mate's last answer", quiet)
}

// talk is what one project's sent.log has said about the captain and the
// Mate, read incrementally from offset.
type talk struct {
	offset int64
	// asked is the captain's last prompt to the Mate; answered is the end
	// of the Mate's last turn, its Stop hook's line.
	asked, answered time.Time
}

// read folds the sent.log lines written since the last read into t.
func (t *talk) read(ws *store.Workspace, project string) error {
	entries, next, err := ws.ReadSent(project, t.offset)
	if err != nil {
		return err
	}
	for _, e := range entries {
		switch {
		case e.Source == store.SourceUser && e.Target == store.TargetMate:
			t.asked = e.Time
		case e.Source == store.SourceMate && e.Target == store.SourceUser:
			t.answered = e.Time
		}
	}
	t.offset = next
	return nil
}

// quiet reports whether the Mate is done with the captain: its last turn
// ended after the captain's last prompt, and nobody has typed to it for
// quietAfter since.
//
// A Mate whose harness records no turn ends (a Codex Mate has no Stop hook)
// never reads as quiet, so it is never switched back to auto behind the
// captain's back; the `m` key still turns auto on.
func (t *talk) quiet(now time.Time, quietAfter time.Duration) bool {
	if t.answered.IsZero() || t.answered.Before(t.asked) {
		return false
	}
	return now.Sub(t.answered) >= quietAfter
}

// rearm turns auto mode back on for a project in manual mode when the captain
// has gone quiet (talk.quiet) and has not held manual mode with the console's
// `m` key (store.Held). The captain typing to the Mate is what turned it off
// (hook.HandlePrompt), so the captain typing again is what turns it off next
// time; the crews' events wait in the captain's box meanwhile, and the
// cursor, which only a delivered digest moves, hands the Mate whatever is
// still open once auto is back.
func (p *Pilot) rearm(project string, now time.Time) (bool, error) {
	if p.ws.Held(project) {
		return false, nil
	}
	t := p.talk[project]
	if t == nil {
		t = &talk{}
		p.talk[project] = t
	}
	if err := t.read(p.ws, project); err != nil {
		return false, err
	}
	quietAfter := p.deps.quietAfter()
	if !t.quiet(now, quietAfter) {
		return false, nil
	}
	if err := p.ws.SetAuto(project, true); err != nil {
		return false, err
	}
	if err := p.ws.AppendSent(project, store.SentEntry{Time: now, Source: store.SourceApp, Target: store.TargetMate, Text: RearmText(quietAfter)}); err != nil {
		return true, err
	}
	return true, nil
}
