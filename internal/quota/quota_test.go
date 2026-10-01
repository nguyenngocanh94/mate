package quota

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/process"
)

// A real quota-axi 0.1.34 snapshot (2026-09-26): Codex measured at 93%
// with its session window just reset, so its runway and spendPriority are
// unknown; Claude waiting on a Keychain grant.
func TestParseARealSchema5Snapshot(t *testing.T) {
	raw, err := os.ReadFile("testdata/schema5-0.1.34.json")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Parse(raw, catalog.Default())
	if err != nil {
		t.Fatal(err)
	}
	codexReading, claudeReading := rs[0], rs[1]
	if codexReading.Harness != codex.KindCodex || !codexReading.Known || codexReading.PercentLeft != 93 || codexReading.SpendPriority != nil || codexReading.Runway != RunwayUnknown {
		t.Fatalf("codex = %+v", codexReading)
	}
	if claudeReading.Harness != claude.KindClaude || claudeReading.Known || claudeReading.Status != "auth_required" || claudeReading.Remedy != "quota-axi --allow-keychain-prompt" {
		t.Fatalf("claude = %+v", claudeReading)
	}
	if !strings.Contains(codexReading.Line(), "93% left · spendPriority unknown · runway unknown") {
		t.Fatalf("codex line = %q", codexReading.Line())
	}
	if _, why, ok := (Snapshot{Readings: rs}).Favoured(); ok || !strings.Contains(why, "no harness") {
		t.Fatalf("an unknown spendPriority was ranked: %q", why)
	}
	if !claudeReading.Eligible() {
		t.Fatal("an unmeasured harness was gated out; unknown is disclosed, never blocking")
	}
	if !strings.Contains(claudeReading.Line(), "unknown (auth_required)") || !strings.Contains(claudeReading.Line(), "quota-axi --allow-keychain-prompt") {
		t.Fatalf("claude line = %q", claudeReading.Line())
	}
}

func scope(name, pct, spend, runway, secs string) string {
	return fmt.Sprintf(`{"scope":%q,"status":"known","effectivePercentRemaining":%s,"runway":{"status":%q,"usableRunwaySeconds":%s},"selection":{"spendPriority":%s}}`,
		name, pct, runway, secs, spend)
}

func snapshot(schema int, rows ...string) []byte {
	return []byte(fmt.Sprintf(`{"schemaVersion":%d,"providers":[%s]}`, schema, strings.Join(rows, ",")))
}

func row(provider, account string, scopes ...string) string {
	acct := ""
	if account != "" {
		acct = fmt.Sprintf(`"accountKey":%q,`, account)
	}
	return fmt.Sprintf(`{"provider":%q,%s"state":{"status":"fresh"},"windows":[],"quotaSemantics":{"effectiveAvailability":[%s]}}`,
		provider, acct, strings.Join(scopes, ","))
}

// Schema 6 files a provider per account; native Codex is codex-home, and a
// provider without that lane binds its default account (firstmate
// quota_row). Never a row picked by position.
func TestSchema6BindsTheHarnessAccount(t *testing.T) {
	raw := snapshot(6,
		row("codex", "work", scope("all_models", "5", "-2", RunwayProjected, "600")),
		row("codex", "codex-home", scope("all_models", "80", "1.2", RunwayThroughReset, `"unknown"`)),
		row("claude", "other", scope("all", "1", "-9", RunwayExhausted, "0")),
		row("claude", "default", scope("all", "60", "0.4", RunwayThroughReset, `"unknown"`)),
	)
	rs, err := Parse(raw, catalog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].PercentLeft != 80 || *rs[0].SpendPriority != 1.2 {
		t.Fatalf("codex bound the wrong account: %+v", rs[0])
	}
	if rs[1].PercentLeft != 60 || rs[1].Runway != RunwayThroughReset {
		t.Fatalf("claude bound the wrong account: %+v", rs[1])
	}
}

// Several provider-wide scopes fold to the most binding of each; a
// model-only scope does not bound the whole harness.
func TestTheMostBindingScopeWins(t *testing.T) {
	raw := snapshot(5, row("codex", "",
		scope("all_models", "70", "1.5", RunwayThroughReset, `"unknown"`),
		scope("all", "40", "0.2", RunwayProjected, "5400"),
		scope("model:gpt-5.5", "0", "-5", RunwayExhausted, "0"),
	))
	rs, err := Parse(raw, catalog.Default())
	if err != nil {
		t.Fatal(err)
	}
	r := rs[0]
	if r.PercentLeft != 40 || *r.SpendPriority != 0.2 || r.Runway != RunwayProjected || *r.RunwaySeconds != 5400 {
		t.Fatalf("folded = %+v", r)
	}
	if !strings.Contains(r.Line(), "runway projected_exhaustion in 1h30m0s") {
		t.Fatalf("line = %q", r.Line())
	}
}

// The gate comes before the ranking: an exhausted harness is never
// favoured, however high its spendPriority; unknown is never zero.
func TestFavouredRanksOnlyEligibleKnownHarnesses(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		name string
		rs   []Reading
		want harness.Kind
		ok   bool
	}{
		{"higher wins", []Reading{
			{Harness: codex.KindCodex, Known: true, PercentLeft: 50, SpendPriority: f(1.5), Runway: RunwayThroughReset},
			{Harness: claude.KindClaude, Known: true, PercentLeft: 90, SpendPriority: f(0.3), Runway: RunwayThroughReset},
		}, codex.KindCodex, true},
		{"exhausted is gated", []Reading{
			{Harness: codex.KindCodex, Known: true, PercentLeft: 50, SpendPriority: f(9), Runway: RunwayExhausted},
			{Harness: claude.KindClaude, Known: true, PercentLeft: 90, SpendPriority: f(-1), Runway: RunwayThroughReset},
		}, claude.KindClaude, true},
		{"zero left is gated", []Reading{
			{Harness: codex.KindCodex, Known: true, PercentLeft: 0, SpendPriority: f(9), Runway: RunwayUnknown},
			{Harness: claude.KindClaude, Known: true, PercentLeft: 10, SpendPriority: f(-1), Runway: RunwayUnknown},
		}, claude.KindClaude, true},
		{"known beats unknown", []Reading{
			{Harness: codex.KindCodex, Known: true, PercentLeft: 20, SpendPriority: f(-0.5), Runway: RunwayProjected},
			{Harness: claude.KindClaude, PercentLeft: -1, Runway: RunwayUnknown, Status: "auth_required"},
		}, codex.KindCodex, true},
		{"tie favours none", []Reading{
			{Harness: codex.KindCodex, Known: true, PercentLeft: 20, SpendPriority: f(1), Runway: RunwayThroughReset},
			{Harness: claude.KindClaude, Known: true, PercentLeft: 20, SpendPriority: f(1), Runway: RunwayThroughReset},
		}, "", false},
		{"nothing known", []Reading{
			{Harness: codex.KindCodex, PercentLeft: -1, Runway: RunwayUnknown},
			{Harness: claude.KindClaude, PercentLeft: -1, Runway: RunwayUnknown},
		}, "", false},
	} {
		got, _, ok := Snapshot{Readings: tc.rs}.Favoured()
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: Favoured = %q, %v; want %q, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseRefusesAnUnknownSchema(t *testing.T) {
	for _, raw := range []string{`{"schemaVersion":4,"providers":[]}`, `not json`} {
		if _, err := Parse([]byte(raw), catalog.Default()); err == nil {
			t.Errorf("Parse(%q) accepted it", raw)
		}
	}
}

func TestAtLeast(t *testing.T) {
	for v, want := range map[string]bool{"0.1.34": true, "0.1.54": true, "1.0.0": true, "0.1.33": false, "0.0.99": false, "dev": false, "": false} {
		if got := AtLeast(v, MinVersion); got != want {
			t.Errorf("AtLeast(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestReadIsReadOnlyAndNamesBothHarnesses(t *testing.T) {
	raw, err := os.ReadFile("testdata/schema5-0.1.34.json")
	if err != nil {
		t.Fatal(err)
	}
	fr := &process.FakeRunner{Handler: func(_ context.Context, s process.Spec) (process.Result, error) {
		if s.Args[0] == "--version" {
			return process.Result{Stdout: []byte("0.1.34\n")}, nil
		}
		// quota-axi exits 1 when a provider needs attention.
		return process.Result{ExitCode: 1, Stdout: raw}, nil
	}}
	now := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	snap, err := Read(context.Background(), fr, catalog.Default(), now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != "0.1.34" || len(snap.Readings) != 2 || !snap.Read.Equal(now) {
		t.Fatalf("snapshot = %+v", snap)
	}
	got := process.CommandKey(fr.Calls[1])
	if got != "quota-axi --json --no-credential-refresh --provider codex,claude" {
		t.Fatalf("read ran %q", got)
	}
}

func TestReadReportsMissingAndOldQuotaAxi(t *testing.T) {
	missing := &process.FakeRunner{Handler: func(context.Context, process.Spec) (process.Result, error) {
		return process.Result{}, fmt.Errorf("process quota-axi: %w", exec.ErrNotFound)
	}}
	if _, err := Read(context.Background(), missing, catalog.Default(), time.Now()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("missing: err = %v", err)
	}
	old := &process.FakeRunner{Default: process.Result{Stdout: []byte("0.1.20\n")}}
	if _, err := Read(context.Background(), old, catalog.Default(), time.Now()); err == nil || !strings.Contains(err.Error(), "quota-axi update") {
		t.Fatalf("old: err = %v", err)
	}
}

// unmeasured is a harness that names no quota-axi row: its provider follows
// the model, not the harness.
type unmeasured struct{ codex.Codex }

func (unmeasured) Kind() harness.Kind { return "unmeasured" }

func (u unmeasured) Capabilities() harness.Capabilities {
	c := u.Codex.Capabilities()
	c.Quota = harness.Cap[harness.QuotaProvider]{Status: harness.CapUnsupported, Reason: "the provider follows --model"}
	return c
}

// A harness with no quota row is assumed to have its whole allowance left
// (plan section 3.7): it passes the gate, is never favoured on a number it
// does not have, and the line says the reading is an assumption and why.
func TestAHarnessWithNoQuotaRowIsAssumedFull(t *testing.T) {
	reg, err := harness.NewRegistry(map[harness.AgentRole]harness.Kind{harness.RoleCrew: codex.KindCodex},
		claude.Claude{}, codex.Codex{}, unmeasured{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/schema5-0.1.34.json")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Parse(raw, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 || rs[0].Harness != codex.KindCodex || rs[1].Harness != claude.KindClaude {
		t.Fatalf("readings = %+v, want codex, claude, then the unmeasured harness", rs)
	}
	r := rs[2]
	if r.Harness != "unmeasured" || r.Assumed == "" || r.PercentLeft != 100 || !r.Eligible() || r.SpendPriority != nil {
		t.Fatalf("unmeasured reading = %+v", r)
	}
	if line := r.Line(); !strings.Contains(line, "assumed 100% left") || !strings.Contains(line, "the provider follows --model") {
		t.Fatalf("line = %q", line)
	}

	// With no harness quota-axi can measure, it is never asked for a snapshot.
	only, err := harness.NewRegistry(nil, unmeasured{})
	if err != nil {
		t.Fatal(err)
	}
	fr := &process.FakeRunner{Default: process.Result{Stdout: []byte("0.1.34\n")}}
	snap, err := Read(context.Background(), fr, only, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.Calls) != 1 || len(snap.Readings) != 1 || snap.Readings[0].Assumed == "" {
		t.Fatalf("calls %v, snapshot %+v", fr.Calls, snap)
	}
}
