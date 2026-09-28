package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/db"
)

func TestLatestContextDoesNotNeedPricingOrBorrowAnOlderWindow(t *testing.T) {
	d, err := db.OpenPath(filepath.Join(t.TempDir(), db.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	for _, q := range []string{
		`INSERT INTO actor(id,project,kind,name) VALUES ('mate:p','p','mate','mate')`,
		`INSERT INTO session(id,actor_id,harness_session_id) VALUES ('s','mate:p','harness-session')`,
		`INSERT INTO pricing(model,context_window) VALUES ('known',200000)`,
		`INSERT INTO turn(id,actor_id,session_id,started_at,model,context_tokens_after) VALUES ('old','mate:p','s','2026-09-27T00:00:00Z','known',100000)`,
		`INSERT INTO turn(id,actor_id,session_id,started_at,model,context_tokens_after) VALUES ('new','mate:p','s','2026-09-27T00:01:00Z','unknown',180000)`,
	} {
		if _, err := d.SQL().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	c, ok, err := d.LatestContext(context.Background(), "mate:p", "")
	if err != nil || !ok {
		t.Fatalf("context: %+v %v %v", c, ok, err)
	}
	if c.Tokens != 180000 || c.Pct != nil || c.Model != "unknown" || c.SessionID != "harness-session" {
		t.Fatalf("latest unpriced context: %+v", c)
	}
	if _, ok, err := d.LatestContext(context.Background(), "mate:p", "fresh-session"); err != nil || ok {
		t.Fatalf("a fresh session borrowed old usage: ok=%v err=%v", ok, err)
	}
	if _, err := d.SQL().Exec(`INSERT INTO pricing(model,context_window) VALUES ('unknown',300000)`); err != nil {
		t.Fatal(err)
	}
	c, ok, err = d.LatestContext(context.Background(), "mate:p", "harness-session")
	if err != nil || !ok || c.Pct == nil || *c.Pct != 60 {
		t.Fatalf("priced context: %+v %v %v", c, ok, err)
	}
}
