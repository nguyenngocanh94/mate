package memory

import (
	"strings"
	"testing"
	"time"
)

var today = time.Date(2026, 9, 24, 15, 0, 0, 0, time.UTC)

const (
	root = "/ws"
	base = "/ws/.mate/projects/shop"
)

func TestNewEntryShapes(t *testing.T) {
	cases := []struct {
		section, text, source, expiry string
		want                          string
	}{
		{CaptainSection, "Speaks Vietnamese; answer in Vietnamese.", "captain, 2026-09-17", "",
			"- Speaks Vietnamese; answer in Vietnamese. (captain, 2026-09-17)"},
		{LessonsSection, "Scouts commit reports unless told not to.", "sent.log to rpi35 2026-09-18", "",
			"- Scouts commit reports unless told not to. (sent.log to rpi35 2026-09-18) <!--a:2026-09-24-->"},
		{LessonsSection, "Spawn fails with no pane; stop after two tries.", "backlog Failed rpi34", "the spawn fix lands",
			"- Spawn fails with no pane; stop after two tries. (expires: the spawn fix lands) (backlog Failed rpi34) <!--p:2026-09-24-->"},
		{LessonsSection, "  padded  ", "  crews/k1/report.md §Durable facts ", "",
			"- padded (crews/k1/report.md §Durable facts) <!--a:2026-09-24-->"},
	}
	for _, c := range cases {
		e, err := NewEntry(c.section, c.text, c.source, c.expiry, today)
		if err != nil {
			t.Fatalf("NewEntry(%q): %v", c.text, err)
		}
		if got := e.Line(); got != c.want {
			t.Errorf("Line() = %q\nwant      %q", got, c.want)
		}
		// Every shape remember writes is one memory check accepts, and
		// parses back to the same parts.
		file := Append("", c.section, e)
		entries, problems := Check(file, today, root, base)
		if len(problems) != 0 || len(entries) != 1 {
			t.Fatalf("remember's own entry fails memory check: %v\n%s", problems, file)
		}
		back := entries[0].Entry
		if back != e {
			t.Errorf("round trip: got %+v, want %+v", back, e)
		}
	}
}

func TestNewEntryRefusals(t *testing.T) {
	cases := []struct {
		name, section, text, source, expiry, want string
	}{
		{"empty text", LessonsSection, "  ", "captain", "", "the entry is empty"},
		{"two lines", LessonsSection, "a\nb", "captain", "", "the entry must be one line"},
		{"marker in text", LessonsSection, "a <!--a:2026-01-01-->", "captain", "", "HTML comment"},
		{"leading dash", LessonsSection, "- a", "captain", "", `leading "- "`},
		{"heading", LessonsSection, "## a", "captain", "", `leading "- " or "#"`},
		{"no source", LessonsSection, "a", "", "", "--source is empty"},
		{"source two lines", LessonsSection, "a", "x\ny", "", "--source must be one line"},
		{"source parens", LessonsSection, "a", "report (section 2)", "", "--source may not contain parentheses"},
		{"captain perishable", CaptainSection, "a", "captain", "next week", "--perishable goes with --lesson"},
		{"expiry parens", LessonsSection, "a", "captain", "fix (r2) lands", "--perishable may not contain parentheses"},
		{"expiry two lines", LessonsSection, "a", "captain", "a\nb", "--perishable must be one line"},
		{"unknown section", "Other", "a", "captain", "", "unknown memory section"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewEntry(c.section, c.text, c.source, c.expiry, today)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}

func TestCheckSourcePaths(t *testing.T) {
	ok := []string{
		"captain, 2026-09-24",
		`captain, "bạn có đang spawn 1 crew mới ko? hay tự làm /checkout", 2026-09-18`,
		"crews/k1/report.md §Durable facts",
		"sent.log to rpi35 2026-09-18, power12 2026-09-19",
		"mate project facts, main@3f2a91c, 2026-09-24",
		"../../WORKSPACE.md", // .mate/WORKSPACE.md: inside
		"https://example.com/docs/x",
	}
	for _, s := range ok {
		if err := CheckSourcePaths(s, root, base); err != nil {
			t.Errorf("CheckSourcePaths(%q) = %v, want ok", s, err)
		}
	}
	bad := map[string]string{
		"/ws/.mate/projects/shop/crews/k1/report.md §2": "write it relative to the project directory: crews/k1/report.md",
		"/private/tmp/claude-501/scratch/report.md":     "outside the workspace",
		"~/notes.md":                         "outside the workspace",
		"../../../../etc/passwd":             "climbs out of the workspace",
		"crews/k1/report.md, /tmp/x.md":      "outside the workspace",
		"`/ws/.mate/projects/shop/sent.log`": "relative to the project directory: sent.log",
	}
	for s, want := range bad {
		err := CheckSourcePaths(s, root, base)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("CheckSourcePaths(%q) = %v, want one containing %q", s, err, want)
		}
	}
}

func TestAppendCreatesTheFileAndSections(t *testing.T) {
	a, _ := NewEntry(LessonsSection, "lesson one.", "captain", "", today)
	b, _ := NewEntry(CaptainSection, "pref one.", "captain, 2026-09-24", "", today)
	c, _ := NewEntry(LessonsSection, "lesson two.", "captain", "", today)

	got := Append("", LessonsSection, a)
	got = Append(got, CaptainSection, b)
	got = Append(got, LessonsSection, c)
	want := Header[:strings.Index(Header, "## ")] +
		"## Captain\n" + b.Line() + "\n\n## Lessons\n" + a.Line() + "\n" + c.Line() + "\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	// The 9-byte file mateassets wrote before task 36 gains both sections,
	// Captain before Lessons, whichever is written first.
	old := "# Memory\n"
	got = Append(old, LessonsSection, a)
	got = Append(got, CaptainSection, b)
	if want := "# Memory\n\n## Captain\n" + b.Line() + "\n\n## Lessons\n" + a.Line() + "\n"; got != want {
		t.Fatalf("from the old header, got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAppendNeverTouchesOtherLines(t *testing.T) {
	existing := "# Memory\n<!-- memory tiers: see the stow skill -->\n\n## Captain\n- keep me (captain)\n- and me (captain)\n## Lessons\n- stale one (captain) <!--a:2025-01-01-->\n"
	e, _ := NewEntry(CaptainSection, "new.", "captain", "", today)
	got := Append(existing, CaptainSection, e)
	want := "# Memory\n<!-- memory tiers: see the stow skill -->\n\n## Captain\n- keep me (captain)\n- and me (captain)\n" + e.Line() + "\n\n## Lessons\n- stale one (captain) <!--a:2025-01-01-->\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	// A stale entry stays: remember never curates.
	if !strings.Contains(got, "<!--a:2025-01-01-->") {
		t.Fatal("remember dropped a stale entry")
	}
}

// checkOne runs Check over a file with body under section and returns the
// problem texts.
func checkOne(t *testing.T, text string) []string {
	t.Helper()
	_, problems := Check(text, today, root, base)
	var out []string
	for _, p := range problems {
		out = append(out, p.String())
	}
	return out
}

func TestCheckRules(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string // "" means no problem
	}{
		{"captain ok", "## Captain\n- pref (captain, 2026-09-17)", ""},
		{"lesson aging ok", "## Lessons\n- l (captain) <!--a:2026-09-01-->", ""},
		{"lesson aging 29 days ok", "## Lessons\n- l (captain) <!--a:2026-08-26-->", ""},
		{"perishable 6 days ok", "## Lessons\n- l (expires: r2 lands) (captain) <!--p:2026-09-18-->", ""},
		{"source with nested parens ok", "## Captain\n- use the 12 V (not 5 V) module (captain, 2026-09-17)", ""},
		{"header and pointer ok", Header, ""},

		{"entry without source", "## Captain\n- pref with no source", "memory.md:2: entry has no source"},
		{"lesson without source", "## Lessons\n- l <!--a:2026-09-20-->", "memory.md:2: entry has no source"},
		{"lesson without marker", "## Lessons\n- l (captain)", "memory.md:2: lesson has no tier marker"},
		{"aging 30 days stale", "## Lessons\n- l (captain) <!--a:2026-08-25-->", "memory.md:2: aging entry last reinforced 2026-08-25, 30 days ago (stale at 30)"},
		{"perishable 7 days stale", "## Lessons\n- l (expires: r2 lands) (captain) <!--p:2026-09-17-->", "memory.md:2: perishable entry last reinforced 2026-09-17, 7 days ago (stale at 7)"},
		{"perishable without expiry", "## Lessons\n- l (captain) <!--p:2026-09-20-->", "memory.md:2: perishable entry names no expiry condition"},
		{"future date", "## Lessons\n- l (captain) <!--a:2026-10-01-->", "memory.md:2: marker date 2026-10-01 is in the future"},
		{"bad date", "## Lessons\n- l (captain) <!--a:2026-13-01-->", `memory.md:2: marker date "2026-13-01" is not a date`},
		{"unknown marker", "## Lessons\n- l (captain) <!--P-->", "memory.md:2: unknown tier marker"},
		{"absolute source", "## Lessons\n- l (/private/tmp/x/report.md) <!--a:2026-09-20-->", `memory.md:2: source "/private/tmp/x/report.md" is an absolute path outside the workspace`},
		{"absolute source in workspace", "## Captain\n- p (/ws/.mate/projects/shop/crews/k1/report.md)", "memory.md:2: source \"/ws/.mate/projects/shop/crews/k1/report.md\" is an absolute path; write it relative to the project directory: crews/k1/report.md"},
		{"out of workspace source", "## Captain\n- p (../../../../x.md)", `memory.md:2: source "../../../../x.md" climbs out of the workspace`},
		{"unknown section", "## Notes\n- n (captain)", "memory.md:1: unknown section `## Notes`"},
		{"entry before section", "# Memory\n- n (captain)", "memory.md:2: entry before any section"},
		{"prose line", "## Captain\nThe captain likes short replies.", "memory.md:2: not a one-line entry"},
		{"continuation line", "## Captain\n- p (captain)\n  continued", "memory.md:3: not a one-line entry"},
		{"text-less entry", "## Captain\n- (captain)", "memory.md:2: entry has a source but no text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := checkOne(t, c.text)
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("want no problem, got %q", got)
				}
				return
			}
			if len(got) != 1 || !strings.HasPrefix(got[0], c.want) {
				t.Fatalf("got %q, want one problem starting %q", got, c.want)
			}
		})
	}
}

func TestCheckProjectAnchors(t *testing.T) {
	doc := ProjectTemplate("shop")
	if anchors, problems := CheckProject(doc, "main"); len(anchors) != 0 || len(problems) != 0 {
		t.Fatalf("a fresh PROJECT.md has anchors %v, problems %v", anchors, problems)
	}
	doc = strings.Join([]string{
		"# shop",
		"## What this project is",
		"- A storefront. (captain, 2026-09-17)", // not repo state: no anchor needed
		"## Layout and state",
		"- main has no commit. (mate project facts, main@none, 2026-09-19)",
		"- A Hugo site. (crews/k1/report.md §Durable facts, main@3f2a91c, 2026-09-24)",
		"- An app with no anchor. (crews/k1/report.md)",
		"- Anchored on another branch. (dev@3f2a91c)",
		"- Only the word domain@none.com. (captain)",
		"## How to work here",
		"- make build (crews/k1/report.md, main@3f2a91cdeadbeef)",
		"- make test (crews/k1/report.md)",
		"## Research on file",
		"- crews/k1/report.md", // not repo state
	}, "\n")
	anchors, problems := CheckProject(doc, "main")
	var got []string
	for _, p := range problems {
		got = append(got, p.String())
	}
	if len(got) != 4 ||
		!strings.HasPrefix(got[0], "PROJECT.md:7: `## Layout and state` line has no main@<sha> anchor") ||
		!strings.HasPrefix(got[1], "PROJECT.md:8: ") || !strings.HasPrefix(got[2], "PROJECT.md:9: ") ||
		!strings.HasPrefix(got[3], "PROJECT.md:12: `## How to work here` line has no main@<sha> anchor") {
		t.Fatalf("problems:\n%s", strings.Join(got, "\n"))
	}
	want := []Anchor{{5, "none"}, {6, "3f2a91c"}, {11, "3f2a91cdeadbeef"}}
	if len(anchors) != len(want) {
		t.Fatalf("anchors = %v, want %v", anchors, want)
	}
	for i := range want {
		if anchors[i] != want[i] {
			t.Errorf("anchor %d = %v, want %v", i, anchors[i], want[i])
		}
	}
}

func TestEstimateTokensIsCeilOfBytesOverThree(t *testing.T) {
	for bytes, want := range map[int]int{0: 0, 1: 1, 2: 1, 3: 1, 4: 2, 11999: 4000, 12000: 4000, 12001: 4001} {
		if got := EstimateTokens(bytes); got != want {
			t.Errorf("EstimateTokens(%d) = %d, want %d", bytes, got, want)
		}
	}
	if BudgetTokens != 4000 {
		t.Fatalf("BudgetTokens = %d; docs/mvp.md task 36 fixes it at 4,000", BudgetTokens)
	}
}

func TestHeadersHaveTheirSections(t *testing.T) {
	for _, s := range Sections {
		if !strings.Contains(Header, "\n## "+s+"\n") {
			t.Errorf("memory.md header lacks ## %s", s)
		}
	}
	if !strings.Contains(Header, HeaderPointer) {
		t.Error("memory.md header lacks the stow pointer")
	}
	for _, s := range BacklogSections {
		if !strings.Contains(BacklogHeader(), "\n## "+s+"\n") {
			t.Errorf("backlog.md header lacks ## %s", s)
		}
	}
	for _, s := range ProjectSections {
		if !strings.Contains(ProjectTemplate("shop"), "\n## "+s+"\n") {
			t.Errorf("PROJECT.md template lacks ## %s", s)
		}
	}
}
