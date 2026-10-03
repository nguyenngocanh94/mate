package memory

import (
	"regexp"
	"strings"
)

// HeldEntry is one entry under backlog.md's `## Held for the captain`: a
// question the Mate sent the captain, or a promise it made them, still
// open. The Mate writes the section by hand (manual section 13), so only
// the entry's boundaries are relied on; ID, Date and WaitsOn are empty for
// an entry that does not carry them in the manual's shape.
type HeldEntry struct {
	// ID and Date are the `<task>, <YYYY-MM-DD>:` head.
	ID   string
	Date string
	// Text is the entry's body up to `Waits on:`, on one line.
	Text string
	// WaitsOn is what follows `Waits on:`: what each answer leads to.
	WaitsOn string
	// Question is the sentence the Mate sent the captain, without its
	// quotes, when the entry is a question (`asked "…"`); empty for a
	// promise, a note, or an entry not in the manual's shape.
	Question string
	// Raw is the whole entry without its bullet, on one line.
	Raw string
}

var (
	heldHead    = regexp.MustCompile(`^(.+?), (\d{4}-\d{2}-\d{2}): *(.*)$`)
	heldWaitsOn = regexp.MustCompile(`(?i)\bwaits on: *`)
	// The quote is greedy to the last closing mark, so a question that
	// itself quotes something stays whole.
	heldAsked = regexp.MustCompile(`(?i)^asked:? *["“](.*)["”]`)
)

// HeldEntries reads the `## Held for the captain` section of a backlog.md,
// in file order (newest first, as `mate backlog add` writes them). A
// backlog without the section holds nothing.
func HeldEntries(text string) []HeldEntry {
	var out []HeldEntry
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, heldEntry(strings.Join(cur, " ")))
		}
		cur = nil
	}
	in := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t\r")
		switch {
		case strings.HasPrefix(line, "## "):
			flush()
			in = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == BacklogHeld
		case !in:
		case strings.HasPrefix(line, "- "):
			flush()
			cur = []string{strings.TrimSpace(strings.TrimPrefix(line, "- "))}
		case len(cur) > 0 && strings.TrimSpace(line) != "":
			// A continuation line of a multi-line entry.
			cur = append(cur, strings.TrimSpace(line))
		}
	}
	flush()
	return out
}

func heldEntry(raw string) HeldEntry {
	e := HeldEntry{Raw: raw, Text: raw}
	if m := heldHead.FindStringSubmatch(raw); m != nil {
		e.ID, e.Date, e.Text = m[1], m[2], m[3]
	}
	if loc := heldWaitsOn.FindStringIndex(e.Text); loc != nil {
		e.WaitsOn = strings.TrimSpace(e.Text[loc[1]:])
		e.Text = strings.TrimSpace(e.Text[:loc[0]])
	}
	if m := heldAsked.FindStringSubmatch(e.Text); m != nil {
		e.Question = strings.TrimSpace(m[1])
	}
	return e
}
