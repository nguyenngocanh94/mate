package store

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The files `mate migrate` keeps in `.mate/` (docs/mvp.md section 3).
const (
	migrateLockName = "migrate.lock"
	migrateLogName  = "migrate.log"
)

// MigrateLockFile is `.mate/migrate.lock`, held for the whole of one
// `mate migrate`.
func (w *Workspace) MigrateLockFile() string {
	return filepath.Join(w.StateDir(), migrateLockName)
}

// MigrateLog is `.mate/migrate.log`, the append-only record of every
// directory `mate migrate` renamed and every repo it finished moving.
func (w *Workspace) MigrateLog() string {
	return filepath.Join(w.StateDir(), migrateLogName)
}

// LockMigrate takes `.mate/migrate.lock` without waiting, the advisory flock
// LockRecover takes on recover.lock. acquired is false while another process
// holds it: a second migrate refuses rather than queueing behind the first.
// The lock dies with the process, so a migrate that crashed leaves nothing
// to clean up.
func (w *Workspace) LockMigrate() (unlock func(), acquired bool, err error) {
	f, err := os.OpenFile(w.MigrateLockFile(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	ok, err := tryLockFile(f, true)
	if err != nil || !ok {
		f.Close()
		return nil, false, err
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, true, nil
}

// MigrateLocked reports whether another process holds `.mate/migrate.lock`.
// It creates nothing: a workspace that never ran a migrate has no lock file,
// and nobody holds it.
func (w *Workspace) MigrateLocked() (bool, error) {
	f, err := os.OpenFile(w.MigrateLockFile(), os.O_RDWR, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	ok, err := tryLockFile(f, true)
	if err != nil {
		return false, err
	}
	if ok {
		_ = unlockFile(f)
	}
	return !ok, nil
}

// MigrateEntry is one line of `.mate/migrate.log`:
// `RFC3339 \t <project> \t <old> \t <new>`, with a fifth field for a step
// taken on the way. Old and New are the repo's absolute paths before and
// after the move.
type MigrateEntry struct {
	Time    time.Time
	Project string
	Old     string
	New     string
	// Step is "" on the line that says the move is finished. Otherwise it
	// names the rename just done: MigrateRenamed when the repo reached New,
	// or MigrateStaged plus the staging directory when a repo sharing its
	// project's name was moved aside first.
	Step string
}

const (
	// MigrateRenamed is the step of a repo renamed to its new path, whose
	// project.yaml, briefs and finished line may still be to come.
	MigrateRenamed = "renamed"
	// MigrateStaged prefixes the step of a repo moved aside, to the path
	// after the space, so its project's directory can take its old name.
	MigrateStaged = "staged"
)

// Done reports the line that ends a move.
func (e MigrateEntry) Done() bool { return e.Step == "" }

// AppendMigrateLog appends one line to `.mate/migrate.log`.
func (w *Workspace) AppendMigrateLog(e MigrateEntry) error {
	fields := []string{e.Time.UTC().Format(time.RFC3339), e.Project, e.Old, e.New}
	if e.Step != "" {
		fields = append(fields, e.Step)
	}
	for _, f := range fields {
		if f == "" || strings.ContainsAny(f, "\t\n") {
			return fmt.Errorf("store: migrate log field %q is empty or holds a tab or newline", f)
		}
	}
	return w.appendLine(w.MigrateLog(), strings.Join(fields, "\t"))
}

// ReadMigrateLog reads `.mate/migrate.log` in order; a workspace that never
// migrated has none. A line that does not parse is skipped: the log is a
// record, and one torn line must not hide the others.
func (w *Workspace) ReadMigrateLog() ([]MigrateEntry, error) {
	f, err := os.Open(w.MigrateLog())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	if err := lockFile(f, false); err != nil {
		return nil, err
	}
	defer unlockFile(f)
	var out []MigrateEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) != 4 && len(fields) != 5 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, fields[0])
		if err != nil {
			continue
		}
		e := MigrateEntry{Time: ts, Project: fields[1], Old: fields[2], New: fields[3]}
		if len(fields) == 5 {
			e.Step = fields[4]
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// WriteCrewBrief replaces `crews/<crew>/brief.md` atomically. It is for a
// rewrite of a brief that already exists, such as `mate migrate` changing
// the repo paths it names.
func (w *Workspace) WriteCrewBrief(project, crew string, data []byte) error {
	if err := ValidateProjectName(project); err != nil {
		return err
	}
	if err := ValidateCrewID(crew); err != nil {
		return err
	}
	return w.writeFile(w.CrewBrief(project, crew), data, 0o644)
}

// SetLayoutProjectDirs records layout 2 in workspace.yaml: every repo now
// lives under its project's directory. `mate migrate` writes it once every
// project is moved.
func (w *Workspace) SetLayoutProjectDirs() error {
	w.cfg.Layout = layoutProjectDirs
	return w.SaveConfig()
}
