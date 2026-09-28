package names

import "regexp"

// CrewPattern is a crew id: a short kebab-case name of its task, such as
// `fix-cart-total` or `scout-login-timeout` - lowercase words of letters
// and digits joined by single hyphens, starting with a letter. It is the
// regexp source, unanchored, so a caller matching ids inside a line (the
// backlog's `- <id>` entries) uses the same rule the store checks.
const CrewPattern = `[a-z][a-z0-9]*(?:-[a-z0-9]+)*`

// MaxCrew is the longest crew id. The crew's Herdr agent is `crew-<id>`,
// and a Herdr agent name holds 32 characters; 24 keeps the name readable
// with room for a collision suffix instead of falling back to a hash.
const MaxCrew = 24

// CrewRule is the crew-id rule in words.
const CrewRule = "kebab-case words naming the task (a-z 0-9, single hyphens, starts with a letter), 2 to 24 characters"

var crewPattern = regexp.MustCompile(`^` + CrewPattern + `$`)

// ValidCrew reports whether id may be a crew id.
func ValidCrew(id string) bool {
	return len(id) >= 2 && len(id) <= MaxCrew && crewPattern.MatchString(id)
}
