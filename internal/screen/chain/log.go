package chain

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/screen"
)

// LogLine is one request the chain sent to Jev, as `.mate/jev.log` keeps
// it, one line each:
//
//	<RFC3339> <kind> <hash12> <latency_ms> <source> composer=<x> dialog=<y> conf=<0.00> fixture=<z> caller=<who> used=<yes|no> [fallback=<why>]
//
// Composer, Dialog and Confidence are Jev's answer ("-" when the request
// failed); Fixture is the fixture's own composer reading of the same
// snapshot, so a day's log can count where the two disagree; Source is the
// observer whose reading the chain returned, and
// Fallback the rule that sent it to the fixture. Caller is who asked
// (screen.WithCaller: watch, send, settle, stow; "-" for none), and Used
// whether any of Jev's answer is in the reading the chain returned. A
// caller can still drop a reading it was handed (watch, for a pane that
// changed while Jev was asked); the log cannot see that. No screen text and
// no key is ever in it. A line written before fixture, caller and used
// were logged reads with them empty.
type LogLine struct {
	Time       time.Time
	Kind       string
	Hash       string
	Latency    time.Duration
	Source     string
	Composer   screen.ComposerState
	Dialog     screen.DialogKind
	Confidence float64
	// Fixture is the fixture's composer reading, "" on an older line.
	Fixture screen.ComposerState
	Caller  string
	// Used is "yes", "no", or "" on a line from before it was logged.
	Used     string
	Fallback string
}

// The two values of LogLine.Used.
const (
	UsedYes = "yes"
	UsedNo  = "no"
)

// String formats the line, without its newline.
func (l LogLine) String() string {
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	out := fmt.Sprintf("%s %s %s %d %s composer=%s dialog=%s conf=%.2f", l.Time.UTC().Format(time.RFC3339), dash(l.Kind),
		dash(l.Hash), l.Latency.Milliseconds(), dash(l.Source), dash(string(l.Composer)), dash(string(l.Dialog)), l.Confidence)
	if l.Fixture != "" {
		out += " fixture=" + string(l.Fixture)
	}
	if l.Caller != "" || l.Used != "" {
		out += fmt.Sprintf(" caller=%s used=%s", dash(l.Caller), dash(l.Used))
	}
	if l.Fallback != "" {
		out += " fallback=" + l.Fallback
	}
	return out
}

// BreakerLine is the log's line for a breaker event (BreakerOpen,
// BreakerClosed), without its newline:
//
//	<RFC3339> breaker <open|closed>
func BreakerLine(at time.Time, event string) string {
	return at.UTC().Format(time.RFC3339) + " breaker " + event
}

// ParseLogLine reads one line String wrote.
func ParseLogLine(line string) (LogLine, error) {
	f := strings.Fields(line)
	if len(f) < 8 || len(f) > 12 {
		return LogLine{}, fmt.Errorf("%d fields, want 8 to 12", len(f))
	}
	at, err := time.Parse(time.RFC3339, f[0])
	if err != nil {
		return LogLine{}, err
	}
	ms, err := strconv.ParseInt(f[3], 10, 64)
	if err != nil || ms < 0 {
		return LogLine{}, fmt.Errorf("latency %q", f[3])
	}
	field := func(s, name string) (string, error) {
		v, ok := strings.CutPrefix(s, name+"=")
		if !ok {
			return "", fmt.Errorf("want %s=, got %q", name, s)
		}
		return v, nil
	}
	l := LogLine{Time: at, Kind: f[1], Hash: f[2], Latency: time.Duration(ms) * time.Millisecond, Source: f[4]}
	composer, err := field(f[5], "composer")
	if err != nil {
		return LogLine{}, err
	}
	dialog, err := field(f[6], "dialog")
	if err != nil {
		return LogLine{}, err
	}
	conf, err := field(f[7], "conf")
	if err != nil {
		return LogLine{}, err
	}
	if l.Confidence, err = strconv.ParseFloat(conf, 64); err != nil {
		return LogLine{}, fmt.Errorf("conf %q", conf)
	}
	l.Composer, l.Dialog = screen.ComposerState(composer), screen.DialogKind(dialog)
	rest := f[8:]
	if len(rest) >= 1 && strings.HasPrefix(rest[0], "fixture=") {
		fix, _ := field(rest[0], "fixture")
		if fix == "" || fix == "-" {
			return LogLine{}, fmt.Errorf("fixture %q", fix)
		}
		l.Fixture = screen.ComposerState(fix)
		rest = rest[1:]
	}
	if len(rest) >= 2 && strings.HasPrefix(rest[0], "caller=") {
		if l.Caller, err = field(rest[0], "caller"); err != nil {
			return LogLine{}, err
		}
		if l.Used, err = field(rest[1], "used"); err != nil {
			return LogLine{}, err
		}
		if l.Used != UsedYes && l.Used != UsedNo {
			return LogLine{}, fmt.Errorf("used %q", l.Used)
		}
		if l.Caller == "-" {
			l.Caller = ""
		}
		rest = rest[2:]
	}
	switch len(rest) {
	case 0:
	case 1:
		if l.Fallback, err = field(rest[0], "fallback"); err != nil {
			return LogLine{}, err
		}
	default:
		return LogLine{}, fmt.Errorf("unexpected fields %q", rest)
	}
	return l, nil
}

// Summary is what a day of `.mate/jev.log` says: how many requests, how
// fast, and how often the fixture's reading was used instead.
type Summary struct {
	Calls     int
	P50, P95  time.Duration
	Fallbacks int
	// ByFallback counts the fallbacks per rule.
	ByFallback map[string]int
	// Used and Unused count the requests whose answer was, and was not,
	// in the reading the chain returned; a line from before Used was
	// logged is in neither.
	Used, Unused int
	// Disagreed counts the answered requests whose composer differs from
	// the fixture's reading of the same snapshot (lines that log one).
	Disagreed int
	// ByCaller counts the requests per caller, "-" for none named.
	ByCaller map[string]int
	// BreakerOpens counts the times the circuit opened (BreakerLine).
	BreakerOpens int
	// Malformed counts lines that do not parse; a trailing partial line,
	// caught mid-append, is not counted.
	Malformed int
}

// Summarize reads a log.
func Summarize(r io.Reader) (Summary, error) {
	s := Summary{ByFallback: map[string]int{}, ByCaller: map[string]int{}}
	var lat []time.Duration
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if err == io.EOF {
			// A line without its newline is an append in progress.
			break
		}
		if err != nil {
			return s, err
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if f := strings.Fields(line); len(f) == 3 && f[1] == "breaker" {
			if f[2] == BreakerOpen {
				s.BreakerOpens++
			}
			continue
		}
		l, perr := ParseLogLine(line)
		if perr != nil {
			s.Malformed++
			continue
		}
		s.Calls++
		lat = append(lat, l.Latency)
		if l.Fallback != "" {
			s.Fallbacks++
			s.ByFallback[l.Fallback]++
		}
		if l.Fixture != "" && l.Composer != "-" && l.Composer != l.Fixture {
			s.Disagreed++
		}
		switch l.Used {
		case UsedYes:
			s.Used++
		case UsedNo:
			s.Unused++
		}
		if l.Used != "" {
			caller := l.Caller
			if caller == "" {
				caller = "-"
			}
			s.ByCaller[caller]++
		}
	}
	slices.Sort(lat)
	s.P50, s.P95 = percentile(lat, 50), percentile(lat, 95)
	return s, nil
}

// percentile is the nearest-rank percentile of sorted values, 0 for none.
func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := max((p*len(sorted)+99)/100, 1)
	return sorted[rank-1]
}
