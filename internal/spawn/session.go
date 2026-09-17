package spawn

import (
	"github.com/nguyenngocanh94/matev2/internal/runtime"
	"github.com/nguyenngocanh94/matev2/internal/store"
)

// SessionSpec exposes Deps' own Herdr session naming to callers outside this
// package (task 13's `matev2 send/peek/state`, which need to LookupSession a
// crew's pane the same way StopCrew and MateStatus already do, without
// duplicating the config-home and workspace-id resolution deps.go owns).
func SessionSpec(deps Deps, w *store.Workspace) (runtime.SessionSpec, error) {
	return deps.sessionSpec(w)
}
