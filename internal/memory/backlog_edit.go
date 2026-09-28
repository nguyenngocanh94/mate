package memory

import (
	"fmt"
	"github.com/nguyenngocanh94/mate/internal/names"
	"regexp"
	"slices"
	"strings"
)

// EditBacklog edits one identified entry, preserving its complete multiline
// body and all unrelated text. Done overflow is returned for archival first.
func EditBacklog(text, action, id, destination, note string) (string, string, error) {
	if !slices.Contains(BacklogSections, destination) {
		return "", "", fmt.Errorf("unknown backlog section %q", destination)
	}
	if !names.ValidCrew(id) {
		return "", "", fmt.Errorf("backlog id must be %s", names.CrewRule)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for _, sec := range BacklogSections {
		if slices.Index(lines, "## "+sec) < 0 {
			return "", "", fmt.Errorf("backlog lacks section %s", sec)
		}
	}
	match := regexp.MustCompile("^- (?:\\[|`)?" + regexp.QuoteMeta(id) + "(?:\\]|`)?(?:[,: —]|$)")
	start, end := -1, -1
	for i, line := range lines {
		if match.MatchString(line) {
			if start >= 0 {
				return "", "", fmt.Errorf("backlog has ambiguous duplicate id %s", id)
			}
			start = i
			end = i + 1
			for end < len(lines) && !strings.HasPrefix(lines[end], "- ") && !strings.HasPrefix(lines[end], "## ") {
				end++
			}
			for end > start+1 && strings.TrimSpace(lines[end-1]) == "" {
				end--
			}
		}
	}
	var entry []string
	switch action {
	case "add":
		if start >= 0 {
			return "", "", fmt.Errorf("backlog id %s already exists", id)
		}
		if strings.TrimSpace(note) == "" || strings.ContainsAny(note, "\r\n") {
			return "", "", fmt.Errorf("backlog add needs one nonempty line of text")
		}
		entry = []string{"- " + id + ": " + note}
	case "move", "done":
		if start < 0 {
			return "", "", fmt.Errorf("backlog id %s not found", id)
		}
		entry = append([]string(nil), lines[start:end]...)
		lines = append(lines[:start:start], lines[end:]...)
	default:
		return "", "", fmt.Errorf("unknown backlog action %q", action)
	}
	// Newest first within each section, so Done can retain the most recent ten.
	index := slices.Index(lines, "## "+destination) + 1
	lines = append(lines[:index:index], append(entry, lines[index:]...)...)
	var archived []string
	if destination == BacklogDone {
		index = slices.Index(lines, "## "+BacklogDone) + 1
		count := 0
		cut := -1
		end := index
		for end < len(lines) && !strings.HasPrefix(lines[end], "## ") {
			if strings.HasPrefix(lines[end], "- ") {
				count++
				if count == DoneKeep+1 {
					cut = end
				}
			}
			end++
		}
		if cut >= 0 {
			archived = append(archived, lines[cut:end]...)
			lines = append(lines[:cut:cut], lines[end:]...)
		}
	}
	archive := strings.TrimSpace(strings.Join(archived, "\n"))
	if archive != "" {
		archive += "\n"
	}
	return strings.Join(lines, "\n") + "\n", archive, nil
}
