package store

import (
	"fmt"
	"regexp"
)

var (
	projectNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	crewIDPattern      = regexp.MustCompile(`^[a-z][a-z0-9]{1,15}$`)
	metaKeyPattern     = regexp.MustCompile(`^[a-z_]+$`)
)

// ValidateProjectName accepts the names a project directory may have:
// `[a-z][a-z0-9-]{0,31}`. Layout helpers assume a validated name; every method
// that writes validates before touching the filesystem.
func ValidateProjectName(name string) error {
	if !projectNamePattern.MatchString(name) {
		return fmt.Errorf("store: invalid project name %q: want [a-z][a-z0-9-]{0,31}", name)
	}
	return nil
}

// ValidateCrewID accepts the ids a crew may have: `[a-z][a-z0-9]{1,15}`.
func ValidateCrewID(id string) error {
	if !crewIDPattern.MatchString(id) {
		return fmt.Errorf("store: invalid crew id %q: want [a-z][a-z0-9]{1,15}", id)
	}
	return nil
}
