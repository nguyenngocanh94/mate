package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type skipRule struct {
	pattern string
	goos    string
}

type skipEvent struct {
	Package string
	Test    string
	Output  string
}

type testEvent struct {
	Action  string
	Package string
	Test    string
	Output  string
	Elapsed float64
}

func parseRules(text string) []skipRule {
	var rules []skipRule
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		rule := skipRule{pattern: fields[0]}
		if len(fields) > 1 {
			rule.goos = fields[1]
		}
		rules = append(rules, rule)
	}
	return rules
}

func skipExpected(test, goos string, rules []skipRule) bool {
	for _, r := range rules {
		if r.goos != "" && r.goos != goos {
			continue
		}
		if strings.HasSuffix(r.pattern, "*") {
			if strings.HasPrefix(test, strings.TrimSuffix(r.pattern, "*")) {
				return true
			}
			continue
		}
		if test == r.pattern {
			return true
		}
	}
	return false
}

// skipReason keeps the t.Skip / t.Fatal lines and drops go test's
// RUN/PAUSE/CONT/SKIP banners, which otherwise drown the summary.
func skipReason(output string) string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if strings.HasPrefix(trim, "===") || strings.HasPrefix(trim, "---") {
			continue
		}
		lines = append(lines, strings.TrimRight(line, "\n"))
	}
	return strings.Join(lines, "\n")
}

func unexpectedSkips(skips []skipEvent, rules []skipRule, goos string) []skipEvent {
	var out []skipEvent
	for _, s := range skips {
		if !skipExpected(s.Test, goos, rules) {
			out = append(out, s)
		}
	}
	return out
}

func run(in io.Reader, stdout, stderr io.Writer, goos, rulesText, reportPath string) int {
	rules := parseRules(rulesText)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	type testKey struct{ pkg, test string }
	outputs := map[testKey]string{}
	var skips []skipEvent
	failed := false
	sawEvent := false

	for scanner.Scan() {
		line := scanner.Text()
		var ev testEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			fmt.Fprintln(stderr, line)
			continue
		}
		sawEvent = true
		key := testKey{ev.Package, ev.Test}
		switch ev.Action {
		case "output":
			if ev.Test != "" {
				outputs[key] += ev.Output
				break
			}
			// Package-level output is the human log: compiler errors,
			// `ok`/`FAIL`/`?` banners. go test emits one JSON event per
			// line, so forwarding only `[build failed]` would drop the
			// syntax error on the previous line.
			fmt.Fprint(stdout, ev.Output)
		case "skip":
			if ev.Test == "" {
				continue
			}
			out := skipReason(outputs[key])
			skips = append(skips, skipEvent{Package: ev.Package, Test: ev.Test, Output: out})
			fmt.Fprintf(stdout, "--- SKIP: %s %s (%.2fs)\n", ev.Package, ev.Test, ev.Elapsed)
			if out != "" {
				fmt.Fprintln(stdout, out)
			}
		case "fail":
			failed = true
			if ev.Test != "" {
				fmt.Fprintf(stdout, "--- FAIL: %s %s (%.2fs)\n", ev.Package, ev.Test, ev.Elapsed)
				if out := skipReason(outputs[key]); out != "" {
					fmt.Fprintln(stdout, out)
				}
			}
		case "pass":
			// Package `ok` already came through as package-level output.
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "gotestreport: read: %v\n", err)
		return 1
	}
	if !sawEvent {
		fmt.Fprintln(stderr, "gotestreport: no go test JSON events on stdin")
		return 1
	}

	fmt.Fprintf(stdout, "\n=== SKIP SUMMARY (%d) ===\n", len(skips))
	if len(skips) == 0 {
		fmt.Fprintln(stdout, "(none)")
	}
	for _, s := range skips {
		kind := "expected"
		if !skipExpected(s.Test, goos, rules) {
			kind = "UNEXPECTED"
		}
		fmt.Fprintf(stdout, "--- SKIP [%s]: %s %s\n", kind, s.Package, s.Test)
		if s.Output != "" {
			fmt.Fprintln(stdout, s.Output)
		}
	}

	unexpected := unexpectedSkips(skips, rules, goos)
	expectedN := len(skips) - len(unexpected)
	writeNotProven(stdout, expectedN)
	if err := writeSkipReport(reportPath, expectedN, skips, rules, goos); err != nil {
		fmt.Fprintf(stderr, "gotestreport: skip report: %v\n", err)
		return 1
	}
	if len(unexpected) > 0 {
		fmt.Fprintf(stderr, "gotestreport: %d unexpected skip(s); declare them in scripts/gotestreport/expected-skips.txt if they are intentional\n", len(unexpected))
		for _, s := range unexpected {
			fmt.Fprintf(stderr, "  UNEXPECTED SKIP: %s %s\n", s.Package, s.Test)
		}
		return 1
	}
	if failed {
		return 1
	}
	return 0
}

func writeNotProven(w io.Writer, expected int) {
	if expected == 0 {
		return
	}
	fmt.Fprintf(w, "\n=== NOT PROVEN: %d expected skip(s) ===\n", expected)
	fmt.Fprintln(w, "These tests did not run. A green result is not evidence that skipped behaviour works.")
	fmt.Fprintln(w, "Live Herdr/Claude/Codex proofs skip unless their env is set; this machine does not set it.")
}

func writeSkipReport(path string, expected int, skips []skipEvent, rules []skipRule, goos string) error {
	if path == "" || expected == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## NOT PROVEN: %d expected skip(s)\n\n", expected)
	b.WriteString("These tests did not run. A green result is not evidence that skipped behaviour works.\n")
	b.WriteString("Live Herdr, Claude, and Codex proofs skip unless their env is set. This machine does not set it.\n\n")
	for _, s := range skips {
		if !skipExpected(s.Test, goos, rules) {
			continue
		}
		fmt.Fprintf(&b, "- `%s %s`\n", s.Package, s.Test)
		if s.Output != "" {
			fmt.Fprintf(&b, "  %s\n", strings.TrimSpace(s.Output))
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
