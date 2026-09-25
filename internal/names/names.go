// Package names holds the naming rules more than one layer checks: the
// store before it writes, and the Console while the captain types. One
// definition, so the form never accepts a name the store refuses.
package names

import "regexp"

// ProjectRule is the project-name rule in words, for the people typing one.
const ProjectRule = "a-z 0-9 -, starts with a letter, at most 32"

var projectPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidProject reports whether name may be a project directory:
// `[a-z][a-z0-9-]{0,31}`.
func ValidProject(name string) bool { return projectPattern.MatchString(name) }
