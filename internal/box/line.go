package box

import "strings"

// Line renders one Entry as a single plain-text line, truncated to fit
// width display cells. Format by kind:
//
//	status:   HH:MM  crew <id>  <state>  <text>
//	incident: HH:MM  crew <id>  incident:<kind>  <text>
//	message:  HH:MM  <source>->#<target>  <text>
//
// A crew's status verb and an incident's kind are recovered from Entry.Text
// with ParseStatus / ParseIncidentText, the same way they were packed in by
// Load - Entry itself carries no kind-specific field.
func Line(e Entry, width int) string {
	if width <= 0 {
		return ""
	}
	ts := e.At.Format("15:04")

	var label, verb, text string
	switch e.Kind {
	case KindStatus:
		st := ParseStatus(e.Text)
		label = "crew " + e.Crew
		verb = string(st.State)
		text = st.Text
	case KindIncident:
		kind, msg := ParseIncidentText(e.Text)
		label = "crew " + e.Crew
		verb = "incident:" + string(kind)
		text = msg
	default: // KindMessage
		label = e.Source.arrow(e.Target)
		text = e.Text
	}

	parts := make([]string, 0, 4)
	parts = append(parts, ts, label)
	if verb != "" {
		parts = append(parts, verb)
	}
	parts = append(parts, text)
	line := strings.Join(parts, "  ")

	return truncateEnd(line, width)
}

// arrow renders a message's source and target as "source->target".
func (s Source) arrow(target string) string {
	if target == "" {
		return string(s)
	}
	return string(s) + "->" + target
}
