package timeline

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nguyenngocanh94/matev2/internal/store"
)

// ingestPricing loads `.matev2/pricing.yaml` and upserts it into the
// `pricing` table (mvp.md M5 task 27): this is the one writer of that table,
// the same way the rest of this package is the one writer of everything
// else derived from the workspace's files.
//
// It runs once per Ingest pass, before any project's own pass, because
// `v_task_ledger` and `v_now` read `pricing` by model on every query - a
// project ingested before the prices are in place would still get a
// consistent (if unpriced) answer, but reindexing project order must not
// change what a rebuild produces, and pricing has no project of its own to
// be ordered by.
//
// A missing or empty pricing.yaml is not an error: every model stays
// unpriced, which `v_task_ledger` already renders honestly as a NULL cost
// rather than a free one (docs/timeline.md).
func (i *Ingester) ingestPricing(ctx context.Context, shared *sql.Tx) error {
	cfg, err := i.ws.LoadPricing()
	if err != nil && !errors.Is(err, store.ErrNoPricingFile) {
		return err
	}

	tx := shared
	if tx == nil {
		var beginErr error
		tx, beginErr = i.d.SQL().BeginTx(ctx, nil)
		if beginErr != nil {
			return beginErr
		}
		defer func() { _ = tx.Rollback() }()
	}

	for _, m := range cfg.Models {
		if m.Model == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO pricing(model, input_per_m, cache_read_per_m, cache_write_per_m, output_per_m, context_window)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(model) DO UPDATE SET
				input_per_m       = excluded.input_per_m,
				cache_read_per_m  = excluded.cache_read_per_m,
				cache_write_per_m = excluded.cache_write_per_m,
				output_per_m      = excluded.output_per_m,
				context_window    = excluded.context_window`,
			m.Model, m.InputPerM, m.CacheReadPerM, m.CacheWritePerM, m.OutputPerM, m.ContextWindow); err != nil {
			return err
		}
	}

	if shared != nil {
		return nil
	}
	return tx.Commit()
}
