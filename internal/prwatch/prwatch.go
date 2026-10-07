// Package prwatch is `mate pr watch` (docs/mvp.md M18): the background
// process that watches the pull request a crew opened and, when it is merged
// or closed, tells the Mate - with no console open.
//
// # What one watch does
//
// It asks `gh pr view` for the pull request's state every Interval. While it
// is open nothing happens: the crew stays alive to answer a review comment or
// a failing check, and the watcher has nothing to say about either. When it is
// merged the watcher fetches `origin` and fast-forwards the default branch of
// the crew's primary checkout (unless that checkout is dirty or not on the
// default branch, in which case it says why it did not), records
// `pr_state=merged` and `merge_commit=` in the crew's meta, appends
// `pr-merged: <url>` to the crew's status file, and wakes the Mate. A pull
// request closed without merging is the same with `pr-closed`. Then it exits.
//
// # How the Mate is woken
//
// The line goes into the Mate's outbox with source `pr` and is delivered by
// the outbox sender's own code (internal/outbox), so the same composer and
// turn-end checks apply and `mate/.outbox.lock` keeps this process and a
// console's sender from typing it twice. A `pr` line is delivered in manual
// mode too: it is not a digest, and no captain's key withdraws it. If the
// Mate is mid-turn the watcher retries for DefaultDeliverFor and then leaves
// the line queued, where a console's sender, or the next `mate pr watch`
// for the same pull request, finishes it.
package prwatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/github"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/store"
)

const (
	// DefaultInterval is how often `gh pr view` is asked.
	DefaultInterval = time.Minute
	// DefaultDeliverFor is how long the watcher keeps trying to type its
	// line into the Mate's composer before it leaves it to a console.
	DefaultDeliverFor = 10 * time.Minute
)

// Deps are the watcher's collaborators; every one is a seam, so a test never
// reaches GitHub, a real remote or a real Herdr.
type Deps struct {
	WS     *store.Workspace
	GH     github.Client
	Git    gitx.Git
	Outbox *outbox.Sender
	// Sleep pauses between polls and delivery attempts; nil is a real
	// sleep that returns early with the context.
	Sleep func(ctx context.Context, d time.Duration) error
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Interval and DeliverFor default to the constants above.
	Interval   time.Duration
	DeliverFor time.Duration
	// Log receives one line per event; nil discards.
	Log io.Writer
}

func (d Deps) interval() time.Duration {
	if d.Interval > 0 {
		return d.Interval
	}
	return DefaultInterval
}

func (d Deps) deliverFor() time.Duration {
	if d.DeliverFor > 0 {
		return d.DeliverFor
	}
	return DefaultDeliverFor
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) sleep(ctx context.Context, dur time.Duration) error {
	if d.Sleep != nil {
		return d.Sleep(ctx, dur)
	}
	timer := time.NewTimer(dur)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (d Deps) logf(format string, args ...any) {
	if d.Log == nil {
		return
	}
	fmt.Fprintf(d.Log, "%s %s\n", d.now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// Watch polls the pull request until it ends or ctx is cancelled. It returns
// nil when the pull request ended (merged or closed, and the Mate told as far
// as it could be), when the crew was closed meanwhile, and ctx's error when it
// was cancelled; a failed `gh pr view` is logged and tried again, never fatal.
func Watch(ctx context.Context, d Deps, project, crew, url string) error {
	if d.WS == nil || d.Outbox == nil {
		return errors.New("prwatch: a workspace and an outbox are required")
	}
	cfg, err := d.WS.LoadProject(project)
	if err != nil {
		return err
	}
	for {
		meta, err := d.WS.ReadCrewMeta(project, crew)
		if err != nil {
			return err
		}
		if crewstate.Declare(crewstate.Declaration{Meta: meta}).Closed() {
			d.logf("crew %s/%s is closed; nothing left to watch", project, crew)
			return nil
		}
		repoCfg, err := cfg.CrewRepo(meta)
		if err != nil {
			return err
		}
		repo := d.WS.RepoDir(repoCfg.Path)

		pr, err := d.GH.PRView(ctx, repo, url)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			d.logf("gh pr view %s: %v", url, err)
		case pr.State == github.StateMerged:
			return d.ended(ctx, project, crew, url, repo, repoCfg, pr, crewstate.PRStateMerged)
		case pr.State == github.StateClosed:
			return d.ended(ctx, project, crew, url, repo, repoCfg, pr, crewstate.PRStateClosed)
		}
		if err := d.sleep(ctx, d.interval()); err != nil {
			return err
		}
	}
}

// ended records and announces the end of a pull request.
func (d Deps) ended(ctx context.Context, project, crew, url, repo string, repoCfg store.RepoConfig, pr github.PR, state string) error {
	set := map[string]string{crewstate.MetaPRURL: url, crewstate.MetaPRState: state}
	verb, line := "pr-closed", ""
	statusPath := d.WS.CrewStatus(project, crew)
	if state == crewstate.PRStateMerged {
		verb = "pr-merged"
		sync := d.syncCheckout(ctx, repo, repoCfg.DefaultBranch, pr)
		set[crewstate.MetaMergeCommit] = pr.MergeCommit
		set[crewstate.MetaPRSync] = sync
		line = fmt.Sprintf("pr-merged: %s %s was merged on GitHub as %s (%s); read %s, then run mate crew stop %s %s and take the next task from the backlog",
			crew, url, short(pr.MergeCommit), sync, statusPath, project, crew)
		d.logf("%s merged as %s; primary checkout: %s", url, short(pr.MergeCommit), sync)
	} else {
		line = fmt.Sprintf("pr-closed: %s %s was closed on GitHub without being merged; read %s, then tell the captain and ask whether to rework it or close the crew",
			crew, url, statusPath)
		d.logf("%s closed without a merge", url)
	}
	if err := d.WS.UpdateCrewMeta(project, crew, set); err != nil {
		return err
	}
	if err := d.appendStatusOnce(project, crew, verb+": "+url); err != nil {
		return err
	}
	return d.wake(ctx, project, crew, state, url, line)
}

// appendStatusOnce appends a line unless the crew's last line already is it,
// so a watcher restarted after it wrote the line does not write it twice.
func (d Deps) appendStatusOnce(project, crew, line string) error {
	entries, _, err := d.WS.ReadStatus(project, crew, 0)
	if err != nil {
		return err
	}
	if n := len(entries); n > 0 && strings.TrimSpace(entries[n-1].Line) == line {
		return nil
	}
	return d.WS.AppendStatus(project, crew, line)
}

// syncCheckout brings the default branch of the primary checkout up to the
// merge: fetch, then fast-forward. It says what it did, or why it did not,
// and never forces anything: a checkout the captain is working in is theirs.
func (d Deps) syncCheckout(ctx context.Context, repo, defaultBranch string, pr github.PR) string {
	skip := func(format string, args ...any) string {
		return "skipped: " + fmt.Sprintf(format, args...)
	}
	if pr.Base != "" && pr.Base != defaultBranch {
		return skip("the pull request targets %s, not %s", pr.Base, defaultBranch)
	}
	head, err := d.Git.CurrentBranch(ctx, repo)
	if err != nil {
		return skip("could not read the checkout's branch: %v", err)
	}
	if head != defaultBranch {
		return skip("the primary checkout is on %s, not %s", head, defaultBranch)
	}
	dirty, err := d.Git.IsDirty(ctx, repo)
	if err != nil {
		return skip("could not read the checkout's state: %v", err)
	}
	if dirty > 0 {
		return skip("the primary checkout has %d uncommitted file(s)", dirty)
	}
	if err := d.Git.Fetch(ctx, repo, "origin"); err != nil {
		return skip("git fetch origin failed: %v", err)
	}
	if err := d.Git.MergeFFOnly(ctx, repo, "origin/"+defaultBranch); err != nil {
		return skip("fast-forward to origin/%s failed: %v", defaultBranch, err)
	}
	return "fast-forwarded " + defaultBranch
}

// wake queues the line for the Mate and tries to deliver it through the
// outbox sender until it is settled or DeliverFor has passed.
func (d Deps) wake(ctx context.Context, project, crew, state, url, text string) error {
	key := fmt.Sprintf("pr:%s:%s:%s", crew, state, url)
	if _, err := d.Outbox.Enqueue(project, outbox.Request{Source: store.OutboxSourcePR, Key: key, Text: text}); err != nil {
		return err
	}
	deadline := d.now().Add(d.deliverFor())
	for {
		pending, err := d.queued(project, key)
		if err != nil {
			return err
		}
		if !pending {
			d.logf("the Mate was told")
			return nil
		}
		att, err := d.Outbox.Attempt(ctx, project)
		if err != nil {
			d.logf("outbox: %v", err)
		}
		if att.Refusal != nil {
			d.logf("not delivered yet: %v", att.Refusal)
		}
		if !d.now().Before(deadline) {
			d.logf("the line is still queued after %s; a console's sender will deliver it", d.deliverFor())
			return nil
		}
		if err := d.sleep(ctx, outbox.DefaultInterval); err != nil {
			return err
		}
	}
}

// queued reports whether the item under key is still waiting.
func (d Deps) queued(project, key string) (bool, error) {
	items, err := d.WS.ReadOutbox(project)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if item.Key == key && item.Queued() {
			return true, nil
		}
	}
	return false, nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	if sha == "" {
		return "an unknown commit"
	}
	return sha
}
