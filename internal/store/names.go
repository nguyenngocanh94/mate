package store

import (
	"fmt"
	"regexp"

	"github.com/nguyenngocanh94/mate/internal/names"
)

var metaKeyPattern = regexp.MustCompile(`^[a-z_]+$`)

// ValidateProjectName accepts the names a project directory may have:
// `[a-z][a-z0-9-]{0,31}`. Layout helpers assume a validated name; every method
// that writes validates before touching the filesystem.
func ValidateProjectName(name string) error {
	if !names.ValidProject(name) {
		return fmt.Errorf("store: invalid project name %q: want [a-z][a-z0-9-]{0,31}", name)
	}
	return nil
}

// ValidateCrewID accepts the ids a crew may have (names.ValidCrew).
func ValidateCrewID(id string) error {
	if !names.ValidCrew(id) {
		return fmt.Errorf("store: invalid crew id %q: want %s", id, names.CrewRule)
	}
	return nil
}
