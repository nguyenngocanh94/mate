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
//	<RFC3339> <kind> <hash12> <latency_ms> <source> composer=<x> dialog=<y> conf=<0.00> [fallback=<why>]
//
// Composer, Dialog and Confidence are Jev's answer ("-" when the request
// failed); Source is the observer whose reading the chain returned, and
// Fallback the rule that sent it to the fixture. No screen text and no key
// is ever in it.
type LogLine struct {
	Time       time.Time
	Kind       string
	Hash       string
	Latency    time.Duration
	Source     string
	Composer   screen.ComposerState
	Dialog     screen.DialogKind
	Confidence float64
	Fallback   string
}

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
	if l.Fallback != "" {
		out += " fallback=" + l.Fallback
	}
	return out
}

// ParseLogLine reads one line String wrote.
func ParseLogLine(line string) (LogLine, error) {
	f := strings.Fields(line)
	if len(f) != 8 && len(f) != 9 {
		return LogLine{}, fmt.Errorf("%d fields, want 8 or 9", len(f))
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
	if len(f) == 9 {
		if l.Fallback, err = field(f[8], "fallback"); err != nil {
			return LogLine{}, err
		}
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
	// Malformed counts lines that do not parse; a trailing partial line,
	// caught mid-append, is not counted.
	Malformed int
}

// Summarize reads a log.
func Summarize(r io.Reader) (Summary, error) {
	s := Summary{ByFallback: map[string]int{}}
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
