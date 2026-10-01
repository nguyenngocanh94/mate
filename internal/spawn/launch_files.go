package spawn

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// writeLaunchFiles writes the files a harness's Prepare named. A harness
// never writes: every file is proven to land inside the workspace first,
// and is written only when its bytes differ, so an unchanged file keeps its
// mtime as well as any trust its harness recorded for it.
//
// git is the working tree the cwd is, nil when it is none (a Mate's
// directory). There, a file marked Exclude is added to the tree's local
// exclude, which is why it must sit at the cwd's top.
func writeLaunchFiles(ctx context.Context, w *store.Workspace, git *gitx.Git, cwd string, files []harness.LaunchFile) error {
	for _, f := range files {
		if _, err := w.Resolve(f.Path); err != nil {
			return err
		}
		if have, err := os.ReadFile(f.Path); err != nil || !bytes.Equal(have, f.Data) {
			if err := writeInside(w, f.Path, f.Data, 0o644); err != nil {
				return err
			}
		}
		if !f.Exclude || git == nil {
			continue
		}
		if filepath.Dir(f.Path) != filepath.Clean(cwd) {
			return observability.NewError(observability.CodeUsage,
				fmt.Sprintf("generated file %s is to be kept out of git but is not at the top of %s", f.Path, cwd))
		}
		if err := excludeGeneratedFile(ctx, *git, cwd, filepath.Base(f.Path)); err != nil {
			return err
		}
	}
	return nil
}
