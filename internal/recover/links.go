// Package recover brings a workspace back after the machine under it was
// restarted, or the workspace itself was copied or moved (docs/mvp.md M17).
//
// Nothing on disk is damaged by either: meta, briefs, worktrees and `.status`
// files are all still there. What is lost is processes (the Herdr server and
// its panes) and the links that name the machine: absolute paths git wrote
// into worktrees, the mate binary in Claude's hook file, the Herdr session's
// owner marker, the old root inside a brief. Every step finds its own work, so
// a pass over a healthy workspace changes nothing and running one twice is
// safe, and a step that fails is reported without stopping the others.
package recover

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Fix is one thing a pass repaired, or failed to repair.
type Fix struct {
	// Step names the kind of link: worktree, hook, owner or root.
	Step string
	// What says what was repaired, or what could not be.
	What string
	Err  error
}

// Env is what the link repairs need from the machine they run on.
type Env struct {
	WS  *store.Workspace
	Git gitx.Git
	// Harnesses is the catalog that says whose hook files a Mate has.
	Harnesses harness.Registry
	// Binary is the mate binary hooks should call.
	Binary string
	// ConfigHome is the Herdr config home that holds the session owner
	// markers.
	ConfigHome string
}

// RepairLinks runs the link repairs in the one order that works: the old root
// is read before git rewrites the worktrees that could say it, and the root
// recorded in workspace.yaml moves last, so a failed brief is tried again on
// the next open.
func RepairLinks(ctx context.Context, env Env) []Fix {
	var fixes []Fix
	oldRoot := formerRoot(env)
	fixes = append(fixes, RepairWorktrees(ctx, env)...)
	fixes = append(fixes, RepairHooks(env)...)
	fixes = append(fixes, RepairOwner(env, oldRoot)...)
	fixes = append(fixes, RepairRoot(env, oldRoot)...)
	return fixes
}

// crewRef is one crew's meta with the paths a link repair needs.
type crewRef struct {
	project, crew string
	meta          map[string]string
	repo          string // absolute repository path, "" when unknown
	worktree      string // absolute worktree path, "" when the meta records none
}

func crews(env Env) []crewRef {
	var out []crewRef
	w := env.WS
	for _, ref := range w.Projects() {
		cfg, err := w.LoadProject(ref.Name)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(w.CrewsDir(ref.Name))
		if err != nil {
			continue
		}
		for _, e := range entries {
			id := strings.TrimSuffix(e.Name(), ".meta")
			if e.IsDir() || id == e.Name() || store.ValidateCrewID(id) != nil {
				continue
			}
			meta, err := w.ReadCrewMeta(ref.Name, id)
			if err != nil || len(meta) == 0 {
				continue
			}
			c := crewRef{project: ref.Name, crew: id, meta: meta}
			if rel := meta[spawn.MetaWorktree]; rel != "" {
				c.worktree = filepath.Join(w.Root(), filepath.FromSlash(rel))
			}
			if repo, err := cfg.CrewRepo(meta); err == nil {
				c.repo = w.RepoDir(repo.Path)
			}
			out = append(out, c)
		}
	}
	return out
}

// RepairWorktrees points every crew worktree git no longer recognises back at
// its repository: `git worktree repair`, which is the only thing that
// rewrites the absolute paths git keeps on both sides of the link.
func RepairWorktrees(ctx context.Context, env Env) []Fix {
	var fixes []Fix
	for _, c := range crews(env) {
		if c.worktree == "" || c.repo == "" {
			continue
		}
		if _, err := os.Stat(c.worktree); err != nil {
			continue
		}
		attached, err := env.Git.WorktreeAttached(ctx, c.worktree)
		if err == nil && attached {
			continue
		}
		label := fmt.Sprintf("worktree of crew %s/%s", c.project, c.crew)
		if err := env.Git.RepairWorktree(ctx, c.repo, c.worktree); err != nil {
			fixes = append(fixes, Fix{Step: "worktree", What: label + " could not be re-attached", Err: err})
			continue
		}
		if attached, err := env.Git.WorktreeAttached(ctx, c.worktree); err != nil || !attached {
			fixes = append(fixes, Fix{Step: "worktree", What: label + " is still not attached after git worktree repair", Err: err})
			continue
		}
		fixes = append(fixes, Fix{Step: "worktree", What: label + " re-attached"})
	}
	return fixes
}

// RepairHooks rewrites the mate binary in each Mate's hook files when the
// binary they name is gone, keeping every other key and hook. Which files
// those are is the Mate's harness's to say.
func RepairHooks(env Env) []Fix {
	var fixes []Fix
	for _, ref := range env.WS.Projects() {
		meta, err := env.WS.ReadMateMeta(ref.Name)
		if err != nil || strings.TrimSpace(meta[spawn.MetaHarness]) == "" {
			continue
		}
		kind, err := env.Harnesses.Parse(meta[spawn.MetaHarness])
		if err != nil {
			continue
		}
		profile, err := env.Harnesses.Lookup(kind)
		if err != nil {
			continue
		}
		hooks := profile.Capabilities().Hooks
		if !hooks.Verified() {
			continue
		}
		changed, err := hooks.Impl.Repoint(env.Binary, env.WS.MateDir(ref.Name), fileExists)
		switch {
		case err != nil:
			fixes = append(fixes, Fix{Step: "hook", What: "hooks of the " + ref.Name + " Mate could not be repaired", Err: err})
		case changed:
			fixes = append(fixes, Fix{Step: "hook", What: "hooks of the " + ref.Name + " Mate repointed at " + env.Binary})
		}
	}
	return fixes
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// RepairOwner settles the Herdr session's owner marker, which names the
// workspace id of the path the workspace had when the session was first
// claimed. A marker for another id is one of two things: the old workspace is
// gone (moved, or its disk copied elsewhere), and this one takes the session
// over; or it is still on disk (copied beside it), and this one gets a session
// name of its own, because two workspaces must never share a session.
func RepairOwner(env Env, oldRoot string) []Fix {
	w := env.WS
	session := w.Session()
	owner, found, err := runtime.SessionOwner(env.ConfigHome, session)
	if err != nil {
		return []Fix{{Step: "owner", What: "the owner marker of session " + session + " could not be read", Err: err}}
	}
	current := store.SessionName(w.Root())
	if !found || owner == current {
		return nil
	}
	if oldRoot != "" && oldRoot != w.Root() && store.SessionName(oldRoot) == owner && !workspaceHolds(oldRoot, session) {
		if err := runtime.ReclaimSessionOwner(env.ConfigHome, session, current); err != nil {
			return []Fix{{Step: "owner", What: "session " + session + " could not be taken over from the workspace that left", Err: err}}
		}
		return []Fix{{Step: "owner", What: "session " + session + " taken over from " + oldRoot + ", which is gone"}}
	}
	next := current
	if next == session {
		next = store.SessionName(w.Root() + "#copy")
	}
	if err := w.SetSession(next); err != nil {
		return []Fix{{Step: "owner", What: "session " + session + " belongs to another workspace and a new name could not be saved", Err: err}}
	}
	return []Fix{{Step: "owner", What: "session " + session + " belongs to another workspace; this one now uses " + next}}
}

// workspaceHolds reports whether the workspace at root is still on disk and
// still names session.
func workspaceHolds(root, session string) bool {
	old, err := store.Open(root)
	return err == nil && old.Session() == session
}

// RepairRoot rewrites the old root into the briefs that carry it and records
// the current root in workspace.yaml. A workspace written before the file
// carried a root has it inferred from the worktrees (formerRoot); with none
// to infer, the current root is simply recorded.
func RepairRoot(env Env, oldRoot string) []Fix {
	w := env.WS
	root := w.Root()
	if w.RecordedRoot() == root {
		return nil
	}
	var fixes []Fix
	failed := false
	if oldRoot != "" && oldRoot != root {
		for _, c := range crews(env) {
			path := w.CrewBrief(c.project, c.crew)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			next := ReplaceRoot(string(data), oldRoot, root)
			if next == string(data) {
				continue
			}
			if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
				failed = true
				fixes = append(fixes, Fix{Step: "root", What: fmt.Sprintf("brief of crew %s/%s could not be rewritten", c.project, c.crew), Err: err})
				continue
			}
			fixes = append(fixes, Fix{Step: "root", What: fmt.Sprintf("brief of crew %s/%s moved from %s to %s", c.project, c.crew, oldRoot, root)})
		}
	}
	if failed {
		return fixes
	}
	if err := w.SetRoot(root); err != nil {
		fixes = append(fixes, Fix{Step: "root", What: "the workspace root could not be recorded in workspace.yaml", Err: err})
	}
	return fixes
}

// ReplaceRoot swaps the path prefix oldRoot for root wherever text names a
// path under it: the old root followed by a separator or by the end of a path
// (not by more name, so /work/a does not match inside /work/ab).
func ReplaceRoot(text, oldRoot, root string) string {
	oldRoot = strings.TrimRight(oldRoot, "/")
	if oldRoot == "" {
		return text
	}
	re := regexp.MustCompile(regexp.QuoteMeta(oldRoot) + `(/|[^A-Za-z0-9._-]|$)`)
	return re.ReplaceAllStringFunc(text, func(m string) string {
		return root + m[len(oldRoot):]
	})
}

// formerRoot is the root the workspace had before it was copied or moved: the
// one workspace.yaml recorded, or for a workspace older than that field, the
// one a worktree's `.git` link still names. "" when there is nothing to go on.
func formerRoot(env Env) string {
	if recorded := env.WS.RecordedRoot(); recorded != "" {
		return recorded
	}
	for _, c := range crews(env) {
		if c.worktree == "" || c.repo == "" {
			continue
		}
		if root := rootFromWorktreeLink(env.WS, c); root != "" {
			return root
		}
	}
	return ""
}

// rootFromWorktreeLink reads `gitdir: <old repo>/.git/worktrees/<name>` out of
// a worktree's `.git` file and strips the repository's path inside the
// workspace, which is the same on every machine, leaving the old root.
func rootFromWorktreeLink(w *store.Workspace, c crewRef) string {
	data, err := os.ReadFile(filepath.Join(c.worktree, ".git"))
	if err != nil {
		return ""
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return ""
	}
	gitdir = filepath.ToSlash(strings.TrimSpace(gitdir))
	i := strings.LastIndex(gitdir, "/.git/worktrees/")
	if i < 0 {
		return ""
	}
	oldRepo := gitdir[:i]
	rel, err := filepath.Rel(w.Root(), c.repo)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	rel = "/" + filepath.ToSlash(rel)
	if !strings.HasSuffix(oldRepo, rel) {
		return ""
	}
	return strings.TrimSuffix(oldRepo, rel)
}
