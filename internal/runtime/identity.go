package runtime

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// This file holds the few pieces of v1's internal/domain and
// internal/fsboundary that the Herdr adapter actually uses. matev2 does not
// have those packages, so they are inlined here rather than carried across.

// WorkspaceID is the opaque identity of a workspace. It seeds the Herdr
// session name, so it must never be a path segment that could escape.
type WorkspaceID string

func (id WorkspaceID) String() string { return string(id) }

var (
	// ErrEmptyID is returned when an opaque ID is missing.
	ErrEmptyID = errors.New("empty id")
	// ErrInvalidID is returned when an ID is not an opaque path-safe token.
	ErrInvalidID = errors.New("invalid id")
)

// ParseWorkspaceID rejects empty IDs and values that could be used as path
// segments to escape a workspace directory.
func ParseWorkspaceID(s string) (WorkspaceID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("workspace_id: %w", ErrEmptyID)
	}
	if s == "." || s == ".." || strings.ContainsAny(s, "/\\\x00") {
		return "", fmt.Errorf("workspace_id: %w", ErrInvalidID)
	}
	return WorkspaceID(s), nil
}

// errNameCollision is the sentinel two distinct raw ids resolving to the same
// live runtime name report. AllocateAgentName decides to retry with errors.Is
// against it, so a registry returning a look-alike would silently disable
// that retry. It is re-exported as ErrNameCollision in names.go.
var errNameCollision = errors.New("agent name collision")

// Clock reports the current time. Production code uses SystemClock; tests use
// FakeClock.
type Clock interface {
	Now() time.Time
}

// SystemClock returns time.Now in UTC.
type SystemClock struct{}

// Now returns the current UTC time.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// FakeClock is a deterministic clock for tests.
type FakeClock struct {
	mu sync.Mutex
	t  time.Time
}

// NewFakeClock returns a FakeClock frozen at t (normalized to UTC).
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{t: t.UTC()} }

// Now returns the frozen time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Advance moves the clock forward by d.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// Set freezes the clock at t (UTC).
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t.UTC()
}

// samePath reports whether a and b name the same location, resolving
// symlinks when both sides exist. v1 kept this in internal/fsboundary; the
// Herdr adapter's workspace and pane cwd checks are the only callers here.
func samePath(a, b string) bool {
	ca := filepath.Clean(strings.TrimSpace(a))
	cb := filepath.Clean(strings.TrimSpace(b))
	if ca == cb {
		return true
	}
	ra, err := filepath.EvalSymlinks(ca)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(cb)
	if err != nil {
		return false
	}
	return filepath.Clean(ra) == filepath.Clean(rb)
}
