package recovery

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/prwatch"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Item is one agent recovery tried to bring back.
type Item struct {
	// Crew is "" for the project's Mate.
	Project, Crew string
	// Resumed is true when the new agent picked its conversation up again;
	// Note says why it did not when it was asked to.
	Resumed bool
	Note    string
	// Err is why the agent did not come back. It stays on the agent's row.
	Err error
}

// Name is how the agent is called in a sentence.
func (i Item) Name() string {
	if i.Crew == "" {
		return "mate " + i.Project
	}
	return "crew " + i.Project + "/" + i.Crew
}

// Watcher is one pull request watcher recovery found dead and started again
// (docs/mvp.md M18).
type Watcher struct {
	Project, Crew string
	// PID is the new watcher's pid.
	PID int
	// Err is why it could not be started.
	Err error
}

// Name is how the watcher is called in a sentence.
func (w Watcher) Name() string { return "pull request watcher of " + w.Project + "/" + w.Crew }

// Progress is reported as recovery moves. Total is 0 until the agents to
// restart are known.
type Progress struct {
	Done, Total int
}

// Result is everything one recovery pass did.
type Result struct {
	// Fixes are the link repairs, failed ones included.
	Fixes []Fix
	// Items are the agents it tried to restart, in the order it tried them.
	Items []Item
	// Watchers are the pull request watchers it started again.
	Watchers []Watcher
	// Err is a failure that stopped the agent step as a whole: Herdr could
	// not be asked or could not be started.
	Err error
}

// Recovered is how many agents came back.
func (r Result) Recovered() int {
	n := 0
	for _, it := range r.Items {
		if it.Err == nil {
			n++
		}
	}
	return n
}

// Failures are the sentences for everything that did not come back or could
// not be repaired.
func (r Result) Failures() []string {
	var out []string
	for _, f := range r.Fixes {
		if f.Err != nil {
			out = append(out, f.What+": "+oneLine(f.Err))
		}
	}
	if r.Err != nil {
		out = append(out, oneLine(r.Err))
	}
	for _, wt := range r.Watchers {
		if wt.Err != nil {
			out = append(out, wt.Name()+": "+oneLine(wt.Err))
		}
	}
	for _, it := range r.Items {
		if it.Err != nil {
			out = append(out, it.Name()+": "+oneLine(it.Err))
		}
	}
	return out
}

// Idle is true when the pass found nothing to do and nothing wrong.
func (r Result) Idle() bool {
	return len(r.Fixes) == 0 && len(r.Items) == 0 && len(r.Watchers) == 0 && r.Err == nil
}

func oneLine(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	return strings.TrimSpace(msg)
}

// Run recovers the workspace: the machine-bound links, then the Herdr server,
// then every Mate and Crew whose meta records a pane that Herdr does not list.
// It holds `.mate/recover.lock` throughout, so a second console opened at the
// same moment waits and then finds nothing left to do. A failure of one item
// never stops the next; progress, when non-nil, hears of each step.
func Run(ctx context.Context, ws *store.Workspace, deps spawn.Deps, progress func(Progress)) Result {
	unlock, err := ws.LockRecover(ctx)
	if err != nil {
		return Result{Err: fmt.Errorf("recovery lock: %w", err)}
	}
	defer unlock()
	say := func(p Progress) {
		if progress != nil {
			progress(p)
		}
	}

	spec, err := spawn.SessionSpec(deps, ws)
	if err != nil {
		return Result{Err: err}
	}
	binary := deps.Binary
	if binary == "" {
		binary, _ = os.Executable()
	}
	var res Result

	// 1. Links. The workspace is read again afterwards: this step may have
	// renamed the session, and every later one must use the new name.
	say(Progress{})
	res.Fixes = RepairLinks(ctx, Env{WS: ws, Git: gitx.New(), Harnesses: deps.Harnesses, Binary: binary, ConfigHome: spec.ConfigHome})
	if spec, err = spawn.SessionSpec(deps, ws); err != nil {
		res.Err = err
		return res
	}

	// 1b. Pull request watchers. They are background processes of their own
	// and need no Herdr, so this runs before Herdr is asked and whatever
	// Herdr answers: a watcher that died with the machine is the reason a
	// merged pull request would go unreported (docs/mvp.md M18).
	res.Watchers = RestartWatchers(ws)

	// 2. Which agents are gone. Herdr is asked without starting anything: a
	// workspace whose agents all stopped on purpose has no reason to bring
	// a server up just because a console opened.
	live := query.ReadLiveness(ctx, deps.Runtime, spec)
	if !live.Asked {
		res.Err = fmt.Errorf("herdr could not be asked which agents are running")
		return res
	}
	todo := lost(ws, live)
	if len(todo) == 0 {
		return res
	}
	say(Progress{Total: len(todo)})

	// 3. The Herdr server, then the agents.
	if _, err := deps.Runtime.EnsureSession(ctx, spec); err != nil {
		res.Err = fmt.Errorf("herdr session %s could not be started: %w", spec.Name, err)
		return res
	}
	for i, it := range todo {
		if ctx.Err() != nil {
			res.Err = ctx.Err()
			return res
		}
		res.Items = append(res.Items, restart(ctx, ws, deps, it))
		say(Progress{Done: i + 1, Total: len(todo)})
	}
	return res
}

// lost lists the Mates and Crews to restart: the meta records a pane, Herdr
// does not list the agent, and the crew is not over. A meta with no pane is a
// stop somebody asked for and is left alone.
func lost(ws *store.Workspace, live query.Liveness) []Item {
	var out []Item
	for _, ref := range ws.Projects() {
		if meta, err := ws.ReadMateMeta(ref.Name); err == nil && meta[spawn.MetaPane] != "" && !live.Alive(meta[spawn.MetaAgent]) {
			out = append(out, Item{Project: ref.Name})
		}
		for _, c := range crewsOf(ws, ref.Name) {
			if c.meta[spawn.MetaPane] == "" || live.Alive(c.meta[spawn.MetaAgent]) {
				continue
			}
			if query.CrewStateOf(c.meta, false, "").Closed() {
				continue
			}
			out = append(out, Item{Project: ref.Name, Crew: c.crew})
		}
	}
	return out
}

// WatcherStarter starts a detached pull request watcher. A test replaces it.
var WatcherStarter prwatch.Starter = prwatch.ExecStarter{}

// RestartWatchers starts `mate pr watch` again for every open crew whose meta
// records a pull request that has not ended (`pr_state` is neither `merged`
// nor `closed`) and whose watcher is not alive, by the pid in its
// `.prwatch` file. A crew that is over has nobody to wake and is left alone.
func RestartWatchers(ws *store.Workspace) []Watcher {
	var out []Watcher
	for _, ref := range ws.Projects() {
		for _, c := range crewsOf(ws, ref.Name) {
			url := strings.TrimSpace(c.meta[crewstate.MetaPRURL])
			if url == "" || query.CrewStateOf(c.meta, false, "").Closed() {
				continue
			}
			if state := c.meta[crewstate.MetaPRState]; state == crewstate.PRStateMerged || state == crewstate.PRStateClosed {
				continue
			}
			if prwatch.Running(ws, ref.Name, c.crew) {
				continue
			}
			started, err := prwatch.Ensure(ws, WatcherStarter, ref.Name, c.crew, url)
			out = append(out, Watcher{Project: ref.Name, Crew: c.crew, PID: started.PID, Err: err})
		}
	}
	return out
}

func restart(ctx context.Context, ws *store.Workspace, deps spawn.Deps, it Item) Item {
	if it.Crew != "" {
		res, err := spawn.RelaunchCrew(ctx, ws, deps, it.Project, it.Crew, "", spawn.RelaunchOptions{Resume: true})
		it.Resumed, it.Note, it.Err = res.Resumed, res.ResumeNote, err
		return it
	}
	req := spawn.StartRequest{Project: it.Project, Resume: true}
	if meta, err := ws.ReadMateMeta(it.Project); err == nil && strings.TrimSpace(meta[spawn.MetaHarness]) != "" {
		// A restart keeps the harness the Mate was running (task 38).
		kind, err := deps.Harnesses.Parse(meta[spawn.MetaHarness])
		if err != nil {
			it.Err = err
			return it
		}
		req.Harness = harness.Kind(kind)
	}
	res, err := spawn.StartMate(ctx, ws, deps, req)
	it.Resumed, it.Note, it.Err = res.Resumed, res.ResumeNote, err
	return it
}
