package store

import "path/filepath"

// Names of the directories and files of docs/mvp.md section 3. They are here
// so no other package spells them.
const (
	StateDirName     = ".mate"
	WorktreesDirName = ".worktrees"

	workspaceFileName = "workspace.yaml"
	workspaceDocName  = "WORKSPACE.md"
	pricingFileName   = "pricing.yaml"
	envFileName       = ".env"
	jevLogName        = "jev.log"
	projectsDirName   = "projects"
	projectFileName   = "project.yaml"
	projectDocName    = "PROJECT.md"
	sentLogName       = "sent.log"
	incidentsLogName  = "incidents.log"
	mateDirName       = "mate"
	mateMetaName      = "mate.meta"
	autoFlagName      = ".auto"
	manualHoldName    = ".manual"
	autoCursorName    = ".auto-cursor"
	outboxName        = ".outbox"
	outboxLockName    = ".outbox.lock"
	memoryFileName    = "memory.md"
	backlogFileName   = "backlog.md"
	crewsDirName      = "crews"
	briefFileName     = "brief.md"
	reportFileName    = "report.md"
	handbackFileName  = "handback.md"
	crewDocName       = "CREW.md"
)

// Every helper below returns an absolute path built from the resolved
// workspace root. They are pure string joins: they do not touch the
// filesystem and they do not validate names, because the methods that write
// validate first and resolve the result through the workspace boundary.

// Root is the resolved absolute path of the workspace.
func (w *Workspace) Root() string { return w.root }

// StateDir is `<root>/.mate`.
func (w *Workspace) StateDir() string { return filepath.Join(w.root, StateDirName) }

// WorkspaceFile is `<root>/.mate/workspace.yaml`.
func (w *Workspace) WorkspaceFile() string { return filepath.Join(w.StateDir(), workspaceFileName) }

// WorkspaceDoc is `<root>/.mate/WORKSPACE.md`, the user's rules for every Mate.
func (w *Workspace) WorkspaceDoc() string { return filepath.Join(w.StateDir(), workspaceDocName) }

// WorkspaceCrewDoc is `<root>/.mate/CREW.md`, the captain's standing rules
// for every Crew in the workspace, appended to the end of every brief
// (docs/mvp.md M7). Optional.
func (w *Workspace) WorkspaceCrewDoc() string { return filepath.Join(w.StateDir(), crewDocName) }

// PricingFile is `<root>/.mate/pricing.yaml`, unused before the token monitor.
func (w *Workspace) PricingFile() string { return filepath.Join(w.StateDir(), pricingFileName) }

// EnvFile is `<root>/.mate/.env`, the workspace's settings for this machine
// (docs/mvp.md section 3): `KEY=VALUE` lines that LoadEnv reads. Optional;
// it holds switches and paths, never a secret itself.
func (w *Workspace) EnvFile() string { return filepath.Join(w.StateDir(), envFileName) }

// JevLog is `<root>/.mate/jev.log`: one line per request the Jev observer
// chain sent (internal/screen/chain), only while `.mate/.env` turns the
// chain on (a readable key, MATE_JEV not off or fixture). It never holds
// screen text or a key.
func (w *Workspace) JevLog() string { return filepath.Join(w.StateDir(), jevLogName) }

// ProjectsDir is `<root>/.mate/projects`.
func (w *Workspace) ProjectsDir() string { return filepath.Join(w.StateDir(), projectsDirName) }

// ProjectDir is `<root>/.mate/projects/<project>`.
func (w *Workspace) ProjectDir(project string) string {
	return filepath.Join(w.ProjectsDir(), project)
}

// ProjectFile is `projects/<project>/project.yaml`.
func (w *Workspace) ProjectFile(project string) string {
	return filepath.Join(w.ProjectDir(project), projectFileName)
}

// ProjectDoc is `projects/<project>/PROJECT.md`.
func (w *Workspace) ProjectDoc(project string) string {
	return filepath.Join(w.ProjectDir(project), projectDocName)
}

// ProjectCrewDoc is `projects/<project>/CREW.md`, the captain's standing
// rules for every Crew of one project, appended after the workspace's.
// Optional.
func (w *Workspace) ProjectCrewDoc(project string) string {
	return filepath.Join(w.ProjectDir(project), crewDocName)
}

// SentLog is `projects/<project>/sent.log`.
func (w *Workspace) SentLog(project string) string {
	return filepath.Join(w.ProjectDir(project), sentLogName)
}

// IncidentsLog is `projects/<project>/incidents.log`, the observer's
// append-only record of the incidents it opened and resolved (docs/mvp.md
// section 4b). Only internal/watch writes it; internal/box reads it.
func (w *Workspace) IncidentsLog(project string) string {
	return filepath.Join(w.ProjectDir(project), incidentsLogName)
}

// MateDir is `projects/<project>/mate`, the working directory of Mate.
func (w *Workspace) MateDir(project string) string {
	return filepath.Join(w.ProjectDir(project), mateDirName)
}

// MateMeta is `projects/<project>/mate/mate.meta`.
func (w *Workspace) MateMeta(project string) string {
	return filepath.Join(w.MateDir(project), mateMetaName)
}

// AutoFlag is `projects/<project>/mate/.auto`; its presence means auto mode.
func (w *Workspace) AutoFlag(project string) string {
	return filepath.Join(w.MateDir(project), autoFlagName)
}

// ManualHold is `projects/<project>/mate/.manual`; its presence means the
// captain chose manual mode with the console's `m` key, so context refresh
// does not run on its own while the captain holds the Mate.
func (w *Workspace) ManualHold(project string) string {
	return filepath.Join(w.MateDir(project), manualHoldName)
}

// AutoCursorFile is `projects/<project>/mate/.auto-cursor`; it records how
// far the auto daemon has digested (mvp.md section 5).
func (w *Workspace) AutoCursorFile(project string) string {
	return filepath.Join(w.MateDir(project), autoCursorName)
}

// MemoryFile is `projects/<project>/mate/memory.md`.
func (w *Workspace) MemoryFile(project string) string {
	return filepath.Join(w.MateDir(project), memoryFileName)
}

// BacklogFile is `projects/<project>/mate/backlog.md`.
func (w *Workspace) BacklogFile(project string) string {
	return filepath.Join(w.MateDir(project), backlogFileName)
}

// CrewsDir is `projects/<project>/crews`.
func (w *Workspace) CrewsDir(project string) string {
	return filepath.Join(w.ProjectDir(project), crewsDirName)
}

// CrewMeta is `projects/<project>/crews/<crew>.meta`.
func (w *Workspace) CrewMeta(project, crew string) string {
	return filepath.Join(w.CrewsDir(project), crew+".meta")
}

// CrewStatus is `projects/<project>/crews/<crew>.status`, the append-only log
// a crew writes with echo.
func (w *Workspace) CrewStatus(project, crew string) string {
	return filepath.Join(w.CrewsDir(project), crew+".status")
}

// CrewPRWatch is `projects/<project>/crews/<crew>.prwatch`: the pid of the
// background `mate pr watch` of the crew's pull request, one number per file
// (docs/mvp.md M18).
func (w *Workspace) CrewPRWatch(project, crew string) string {
	return filepath.Join(w.CrewsDir(project), crew+".prwatch")
}

// CrewPRWatchLog is `projects/<project>/crews/<crew>/prwatch.log`, where the
// detached watcher writes what it did.
func (w *Workspace) CrewPRWatchLog(project, crew string) string {
	return filepath.Join(w.CrewDir(project, crew), "prwatch.log")
}

// CrewDir is `projects/<project>/crews/<crew>`, kept after teardown.
func (w *Workspace) CrewDir(project, crew string) string {
	return filepath.Join(w.CrewsDir(project), crew)
}

// CrewBrief is `projects/<project>/crews/<crew>/brief.md`.
func (w *Workspace) CrewBrief(project, crew string) string {
	return filepath.Join(w.CrewDir(project, crew), briefFileName)
}

// CrewReport is `projects/<project>/crews/<crew>/report.md`.
func (w *Workspace) CrewReport(project, crew string) string {
	return filepath.Join(w.CrewDir(project, crew), reportFileName)
}

// CrewHandback is `projects/<project>/crews/<crew>/handback.md`, the ship
// Crew's own account of its acceptance checks, written before `wait-mate`
// (docs/mvp.md M7). Like the report it lives outside the worktree, so it
// outlives the Crew.
func (w *Workspace) CrewHandback(project, crew string) string {
	return filepath.Join(w.CrewDir(project, crew), handbackFileName)
}

// WorktreesDir is `<root>/.worktrees`.
func (w *Workspace) WorktreesDir() string { return filepath.Join(w.root, WorktreesDirName) }

// WorktreeDir is `<root>/.worktrees/<project>-<crew>`, the git worktree a crew
// runs in.
func (w *Workspace) WorktreeDir(project, crew string) string {
	return filepath.Join(w.WorktreesDir(), project+"-"+crew)
}

// RepoDir is the absolute path of a registered repository, from the relative
// path stored in workspace.yaml.
func (w *Workspace) RepoDir(repo string) string { return filepath.Join(w.root, repo) }
