package query

import (
	"context"

	"github.com/nguyenngocanh94/mate/internal/runtime"
)

// Liveness is what Herdr says about the workspace's agents at one moment. A
// recorded pane is not evidence of a live agent: a machine restart takes
// Herdr's server and every pane with it and leaves the meta files as they
// were, so the loader asks instead of trusting the file.
//
// The zero value means nobody asked, and the loader keeps the recorded
// state with its caveat - the answer for a one-shot read that never calls
// Herdr. An asked Liveness with no agents is the positive answer "nothing
// is alive".
type Liveness struct {
	Asked  bool
	Agents map[string]bool
}

// Alive reports whether Herdr listed the named agent. It is only meaningful
// when Asked.
func (l Liveness) Alive(agent string) bool { return l.Agents[agent] }

// ReadLiveness asks Herdr which agents of the session are up. It never
// starts a server: a session that is not running, or a server that is not
// there, is the answer that no agent is alive. Any other failure leaves the
// question unasked, so a transport fault cannot make every row read as
// stopped.
func ReadLiveness(ctx context.Context, rt runtime.ReadAdapter, spec runtime.SessionSpec) Liveness {
	if rt == nil {
		return Liveness{}
	}
	session, running, err := rt.LookupSession(ctx, spec)
	if err != nil {
		if runtime.IsServerNotRunning(err) {
			return Liveness{Asked: true}
		}
		return Liveness{}
	}
	if !running {
		return Liveness{Asked: true}
	}
	agents, err := rt.ListAgents(ctx, session)
	if err != nil {
		if runtime.IsServerNotRunning(err) {
			return Liveness{Asked: true}
		}
		return Liveness{}
	}
	out := Liveness{Asked: true, Agents: make(map[string]bool, len(agents))}
	for _, a := range agents {
		if a.LiveHandleOK {
			out.Agents[a.Handle.Name] = true
		}
	}
	return out
}
