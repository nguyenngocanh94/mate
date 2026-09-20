package timeline

import (
	"context"

	"github.com/nguyenngocanh94/matev2/internal/timeline/scene"
)

// projectScene runs the scene projection of task 26 over the project this
// pass has just written, inside the same transaction: the `transition` rows
// and the events they are derived from commit together, so a reader never
// sees a story the scene has not caught up with.
//
// It runs last because the projection reads `cause_event_id` - a Mate turn is
// `reading(crew)` because the note that started it names that crew - and the
// causality pass fills those links.
//
// A pass that inserted no event moved nobody, so the projection is skipped;
// the exception is a project that has never been projected at all, which is
// what fills `transition` for a database written before this existed.
func (p *pass) projectScene(ctx context.Context) error {
	if p.w.insertedEvents == 0 {
		var rows int
		if err := p.tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM transition WHERE project = ?`, p.project).Scan(&rows); err != nil {
			return err
		}
		if rows > 0 {
			return nil
		}
	}
	return scene.Run(ctx, p.tx, p.project)
}
