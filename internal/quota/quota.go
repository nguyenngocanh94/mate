// Package quota reads a Crew harness's remaining allowance from quota-axi
// (https://github.com/kunchenguid/axi, `quota-axi --json`), the data source
// firstmate's quota-array-dispatch ranks a profile array by.
//
// quota-axi is data only. Each registered harness names the one quota-axi
// provider row that measures it (its Quota capability); this package folds
// that row's provider-wide scopes into one Reading, and names the harness
// the evidence favours: the highest known spendPriority among harnesses
// that pass the gate (not exhausted, some allowance left). Unknown is never
// zero and never healthy; a harness quota-axi cannot measure stays
// eligible, and the output says it is unknown. A harness that names no row
// at all is assumed to have its whole allowance left
// (docs/plans/harness-registry-2026-09-30.md, section 3.7), and the output
// says that too.
//
// The read is strictly read-only (--no-credential-refresh): mate never
// makes quota-axi renew a vendor session behind the captain's back.
package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
)

// Binary is the quota-axi executable name, looked up on PATH.
const Binary = "quota-axi"

// MinVersion is the oldest quota-axi whose --json carries
// selection.spendPriority per scope, measured on 0.1.34 (2026-09-26).
const MinVersion = "0.1.34"

// ReadTimeout bounds one read: quota-axi asks two vendors over the network.
const ReadTimeout = 20 * time.Second

// Runway statuses, as quota-axi names them.
const (
	RunwayThroughReset = "through_reset"
	RunwayProjected    = "projected_exhaustion"
	RunwayExhausted    = "exhausted_now"
	RunwayUnknown      = "unknown"
)

// ErrNotInstalled is quota-axi missing from PATH: dispatch goes on without
// quota evidence.
var ErrNotInstalled = errors.New("quota-axi is not installed")

// Reading is one harness's allowance, folded over the provider-wide scopes
// of its row (all, all_models, all_products): the most binding of each.
type Reading struct {
	Harness  harness.Kind
	Provider string
	// Known is false when quota-axi has no measured scope for the harness;
	// Status and Remedy then say why.
	Known bool
	// PercentLeft is the lowest effectivePercentRemaining; -1 unknown.
	PercentLeft float64
	// SpendPriority is the lowest known spendPriority; nil unknown. Higher
	// is better: positive is allowance on track to reach its reset unused.
	SpendPriority *float64
	// Runway is the worst runway status; RunwaySeconds is set for a
	// projected exhaustion.
	Runway        string
	RunwaySeconds *float64
	ResetsAt      string
	// Status is the provider row's state (fresh, stale, auth_required, ...),
	// and Remedy the command quota-axi says would fix an unmeasured one.
	Status string
	Remedy string
	// Assumed is set when the harness names no quota-axi row: the reading
	// is the assumption that its whole allowance is left, and Assumed is
	// the harness's reason for having no row. Jev's quota_exhausted label is
	// how a wrong assumption shows.
	Assumed string
}

// Eligible is the gate: not exhausted, and not known to be at zero. An
// unknown reading passes, with its uncertainty shown.
func (r Reading) Eligible() bool {
	if r.Runway == RunwayExhausted {
		return false
	}
	return !r.Known || r.PercentLeft < 0 || r.PercentLeft > 0
}

// Snapshot is one read of every mate harness.
type Snapshot struct {
	Version  string
	Read     time.Time
	Readings []Reading
}

// Reading is the harness's reading in s.
func (s Snapshot) Reading(kind harness.Kind) (Reading, bool) {
	for _, r := range s.Readings {
		if r.Harness == kind {
			return r, true
		}
	}
	return Reading{}, false
}

// Favoured is the harness the quota evidence favours, and why; ok is false
// when it favours none: no eligible harness has a known spendPriority, or
// the best two tie.
func (s Snapshot) Favoured() (harness.Kind, string, bool) {
	var best *Reading
	tie := false
	for i := range s.Readings {
		r := &s.Readings[i]
		if !r.Eligible() || r.SpendPriority == nil {
			continue
		}
		switch {
		case best == nil || *r.SpendPriority > *best.SpendPriority:
			best, tie = r, false
		case *r.SpendPriority == *best.SpendPriority:
			tie = true
		}
	}
	if best == nil {
		return "", "no harness has a known spendPriority", false
	}
	if tie {
		return "", "spendPriority ties", false
	}
	return best.Harness, fmt.Sprintf("highest spendPriority %s among eligible harnesses", formatFloat(*best.SpendPriority)), true
}

// harnessQuota is one registered harness and the quota-axi row that
// measures it: the provider, and the account key a schema-6 snapshot files
// it under. unmeasured is the harness's reason when it names no row.
type harnessQuota struct {
	kind       harness.Kind
	provider   string
	lane       string
	unmeasured string
}

// harnessQuotas is every registered harness in the order a snapshot lists
// them: the Crew's default first, since dispatch is a choice of a Crew's
// harness, then the rest as registered.
func harnessQuotas(harnesses harness.Registry) []harnessQuota {
	kinds := harnesses.Kinds()
	if first, err := harnesses.Default(harness.RoleCrew); err == nil {
		ordered := []harness.Kind{first}
		for _, k := range kinds {
			if k != first {
				ordered = append(ordered, k)
			}
		}
		kinds = ordered
	}
	out := make([]harnessQuota, 0, len(kinds))
	for _, k := range kinds {
		profile, err := harnesses.Lookup(k)
		if err != nil {
			continue
		}
		q := profile.Capabilities().Quota
		if !q.Verified() {
			out = append(out, harnessQuota{kind: k, unmeasured: q.Reason})
			continue
		}
		out = append(out, harnessQuota{kind: k, provider: q.Impl.QuotaProvider(), lane: q.Impl.QuotaLane()})
	}
	return out
}

// Read runs quota-axi once, read-only, for every registered harness.
func Read(ctx context.Context, r process.Runner, harnesses harness.Registry, now time.Time) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	ver, err := r.Run(ctx, process.Spec{Name: Binary, Args: []string{"--version"}})
	if err != nil {
		if isNotFound(err) {
			return Snapshot{}, ErrNotInstalled
		}
		return Snapshot{}, fmt.Errorf("quota-axi --version: %w", err)
	}
	if ver.ExitCode != 0 {
		return Snapshot{}, fmt.Errorf("quota-axi --version exited %d", ver.ExitCode)
	}
	version := strings.TrimSpace(string(ver.Stdout))
	if !AtLeast(version, MinVersion) {
		return Snapshot{}, fmt.Errorf("quota-axi %q is older than %s; run `quota-axi update`", version, MinVersion)
	}
	var names []string
	for _, q := range harnessQuotas(harnesses) {
		if q.provider != "" {
			names = append(names, q.provider)
		}
	}
	if len(names) == 0 {
		return Snapshot{Version: version, Read: now, Readings: fold(harnesses, rawSnapshot{})}, nil
	}
	res, err := r.Run(ctx, process.Spec{Name: Binary, Args: []string{"--json", "--no-credential-refresh", "--provider", strings.Join(names, ",")}})
	if err != nil {
		return Snapshot{}, fmt.Errorf("quota-axi --json: %w", err)
	}
	// quota-axi exits non-zero when a provider needs attention and still
	// prints the whole snapshot; the JSON is the answer either way.
	readings, err := Parse(res.Stdout, harnesses)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Version: version, Read: now, Readings: readings}, nil
}

func isNotFound(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}

type rawSnapshot struct {
	SchemaVersion int           `json:"schemaVersion"`
	Providers     []rawProvider `json:"providers"`
}

type rawProvider struct {
	Provider   string `json:"provider"`
	AccountKey string `json:"accountKey"`
	State      struct {
		Status        string `json:"status"`
		RemedyCommand string `json:"remedyCommand"`
	} `json:"state"`
	Windows []struct {
		ResetsAt string `json:"resetsAt"`
	} `json:"windows"`
	QuotaSemantics struct {
		EffectiveAvailability []rawScope `json:"effectiveAvailability"`
	} `json:"quotaSemantics"`
}

type rawScope struct {
	Scope                     string          `json:"scope"`
	Status                    string          `json:"status"`
	EffectivePercentRemaining json.RawMessage `json:"effectivePercentRemaining"`
	Runway                    struct {
		Status              string          `json:"status"`
		UsableRunwaySeconds json.RawMessage `json:"usableRunwaySeconds"`
	} `json:"runway"`
	Selection struct {
		SpendPriority json.RawMessage `json:"spendPriority"`
	} `json:"selection"`
}

// Parse folds a `quota-axi --json` snapshot, schema 5 or 6, into one
// Reading per registered harness, in the order of harnessQuotas.
func Parse(raw []byte, harnesses harness.Registry) ([]Reading, error) {
	var s rawSnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("quota-axi --json: %w", err)
	}
	if s.SchemaVersion != 5 && s.SchemaVersion != 6 {
		return nil, fmt.Errorf("quota-axi --json: schema %d, mate reads 5 and 6", s.SchemaVersion)
	}
	return fold(harnesses, s), nil
}

// fold is one Reading per registered harness from a parsed snapshot. A
// harness that names no row is assumed to have its whole allowance left.
func fold(harnesses harness.Registry, s rawSnapshot) []Reading {
	quotas := harnessQuotas(harnesses)
	out := make([]Reading, 0, len(quotas))
	for _, q := range quotas {
		if q.provider == "" {
			out = append(out, Reading{Harness: q.kind, PercentLeft: 100, Runway: RunwayUnknown, Status: "unmeasured", Assumed: q.unmeasured})
			continue
		}
		row, ok := pickRow(s, q.provider, q.lane)
		r := Reading{Harness: q.kind, Provider: q.provider, PercentLeft: -1, Runway: RunwayUnknown, Status: "absent"}
		if ok {
			r = foldRow(r, row)
		}
		out = append(out, r)
	}
	return out
}

// pickRow is firstmate's quota_row: schema 6 binds the lane's account, else
// the default account; schema 5 has one row per provider.
func pickRow(s rawSnapshot, provider, lane string) (rawProvider, bool) {
	var rows []rawProvider
	for _, p := range s.Providers {
		if p.Provider == provider {
			rows = append(rows, p)
		}
	}
	if s.SchemaVersion == 5 {
		if len(rows) == 0 {
			return rawProvider{}, false
		}
		return rows[0], true
	}
	for _, key := range []string{lane, "default"} {
		for _, p := range rows {
			if key != "" && p.AccountKey == key {
				return p, true
			}
		}
	}
	return rawProvider{}, false
}

// providerWide are the scopes that bound every model of the provider.
var providerWide = map[string]bool{"all": true, "all_models": true, "all_products": true}

// runwayRank orders runway statuses worst first.
var runwayRank = map[string]int{RunwayExhausted: 0, RunwayProjected: 1, RunwayUnknown: 2, RunwayThroughReset: 3}

func foldRow(r Reading, p rawProvider) Reading {
	r.Status, r.Remedy = p.State.Status, p.State.RemedyCommand
	runway := ""
	for _, sc := range p.QuotaSemantics.EffectiveAvailability {
		if !providerWide[sc.Scope] || sc.Status != "known" {
			continue
		}
		r.Known = true
		if pct, ok := number(sc.EffectivePercentRemaining); ok && (r.PercentLeft < 0 || pct < r.PercentLeft) {
			r.PercentLeft = pct
		}
		if sp, ok := number(sc.Selection.SpendPriority); ok && (r.SpendPriority == nil || sp < *r.SpendPriority) {
			r.SpendPriority = &sp
		}
		status := sc.Runway.Status
		if _, ok := runwayRank[status]; !ok {
			status = RunwayUnknown
		}
		if runway == "" || runwayRank[status] < runwayRank[runway] {
			runway = status
		}
		if secs, ok := number(sc.Runway.UsableRunwaySeconds); ok && (r.RunwaySeconds == nil || secs < *r.RunwaySeconds) {
			r.RunwaySeconds = &secs
		}
	}
	if runway != "" {
		r.Runway = runway
	}
	for _, w := range p.Windows {
		if w.ResetsAt != "" && (r.ResetsAt == "" || w.ResetsAt < r.ResetsAt) {
			r.ResetsAt = w.ResetsAt
		}
	}
	return r
}

// number reads a JSON number; quota-axi writes the string "unknown" where it
// has none, and that is never zero.
func number(raw json.RawMessage) (float64, bool) {
	var f float64
	if len(raw) == 0 || json.Unmarshal(raw, &f) != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// AtLeast reports whether version (as `quota-axi --version` prints it) is
// at least min. An unparseable version is never assumed current.
func AtLeast(version, min string) bool {
	got, ok := semver(version)
	want, wok := semver(min)
	if !ok || !wok {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return got[i] > want[i]
		}
	}
	return true
}

func semver(s string) ([3]int, bool) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func formatFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// Line is the reading as one line for `mate crew dispatch`.
func (r Reading) Line() string {
	if r.Assumed != "" {
		return fmt.Sprintf("%-7s assumed 100%% left: no quota-axi row measures it (%s)", r.Harness, r.Assumed)
	}
	if !r.Known {
		s := fmt.Sprintf("%-7s unknown (%s)", r.Harness, r.Status)
		if r.Remedy != "" {
			s += "; the captain can run `" + r.Remedy + "`"
		}
		return s
	}
	parts := []string{}
	if r.PercentLeft >= 0 {
		parts = append(parts, formatFloat(r.PercentLeft)+"% left")
	} else {
		parts = append(parts, "percent unknown")
	}
	if r.SpendPriority != nil {
		parts = append(parts, "spendPriority "+formatFloat(*r.SpendPriority))
	} else {
		parts = append(parts, "spendPriority unknown")
	}
	runway := "runway " + r.Runway
	if r.Runway == RunwayProjected && r.RunwaySeconds != nil {
		runway += " in " + (time.Duration(*r.RunwaySeconds) * time.Second).Round(time.Minute).String()
	}
	parts = append(parts, runway)
	if r.ResetsAt != "" {
		parts = append(parts, "next reset "+r.ResetsAt)
	}
	s := fmt.Sprintf("%-7s %s", r.Harness, strings.Join(parts, " · "))
	if r.Status != "" && r.Status != "fresh" {
		s += " (" + r.Status + ")"
	}
	return s
}
