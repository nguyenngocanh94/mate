package beads

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/tool"
)

// command is `mate tool beads <project> -- <bd arguments>`: bd in the
// project's tracker, with the arguments whole.
type command struct{}

// textFlags take a value that may itself look like a flag: a title or a
// description is never read as scope.
var textFlags = []string{"--title", "--description", "-d", "--notes", "--append-notes", "--reason", "--body-file", "--metadata", "--set-metadata", "--actor"}

// scopeFlags would point bd at another tracker than the project's.
var scopeFlags = []string{"--db", "--database", "--directory", "--global", "--repo"}

// Run passes args to bd unchanged, within the project's tracker, which it
// makes first when it is absent. A failed command may still have written
// some records, so the viewer's export follows failures too; an export
// error never suggests rerunning a write that may have succeeded.
func (command) Run(ctx context.Context, env tool.CommandEnv, args []string, in io.Reader, out, stderr io.Writer) error {
	t, err := open(env)
	if err != nil {
		return err
	}
	if err := scopeErr(args); err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "init" {
		return fmt.Errorf("use mate tool beads %s --init to initialize; pass a bd subcommand after --", t.name)
	}
	unlock, err := t.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := t.ensure(ctx, stderr); err != nil {
		return err
	}
	commandErr := t.run(ctx, tracker, args, in, out, stderr)
	exportErr := t.export(ctx)
	if exportErr != nil {
		exportErr = fmt.Errorf("Beads command may already have saved changes; viewer refresh failed: %w; run mate tool beads %s --init to refresh, do not repeat the write", exportErr, t.name)
	}
	return errors.Join(commandErr, exportErr)
}

// scopeErr refuses a flag that would change which tracker bd uses. Values
// of text flags, and everything after a bare --, are bd's literal text.
func scopeErr(args []string) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if slices.Contains(textFlags, a) {
			i++
			continue
		}
		for _, flag := range scopeFlags {
			if a == flag || strings.HasPrefix(a, flag+"=") {
				return fmt.Errorf("%s would change project scope; select the Mate project instead", flag)
			}
		}
		if strings.HasPrefix(a, "-C") {
			return errors.New("-C would change project scope; select the Mate project instead")
		}
	}
	return nil
}
