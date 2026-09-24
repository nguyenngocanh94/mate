package console

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// FakeSessionController is a deterministic, in-memory implementation of the
// SessionReader/SessionPrompt/SessionClose ports used by the snapshot renderer
// and polling-bridge tests. It never touches runtime.Adapter or
// internal/persistence; scripting a scenario is done by calling Seed, not by
// driving Herdr.
//
// It is scaffolding for tests and local development, not a production seam:
// cmd/mate/console.go's bridge builds the real snapshot ports from
// internal/query, internal/application and runtime.Adapter.
type FakeSessionController struct {
	mu sync.Mutex
	// Now, when set, replaces time.Now for AsOf/ObservedAt so callers get
	// deterministic snapshots. Nil uses the wall clock.
	Now func() time.Time

	sessions map[sessionTargetKey]*fakeSession
}

type sessionTargetKey struct {
	kind SessionTargetKind
	id   string
}

type fakeSession struct {
	snapshot SessionSnapshot
	seeded   bool
	closed   bool
	prompts  []string
}

// NewFakeSessionController returns a controller with no seeded targets.
func NewFakeSessionController() *FakeSessionController {
	return &FakeSessionController{sessions: make(map[sessionTargetKey]*fakeSession)}
}

func (f *FakeSessionController) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func sessionKeyOf(target SessionTarget) sessionTargetKey {
	return sessionTargetKey{kind: target.Kind, id: target.ID}
}

// Seed installs (or replaces) the snapshot Read returns for target, so a
// caller can script a scenario - a missing agent, unparseable output, a
// queued inbox entry - without touching runtime.Adapter. Target and AsOf on
// the seeded snapshot are overwritten by Read on every call, so callers
// only need to set the fields the scenario is actually about.
func (f *FakeSessionController) Seed(target SessionTarget, snapshot SessionSnapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[sessionKeyOf(target)] = &fakeSession{snapshot: snapshot, seeded: true}
}

// Read implements SessionReader. An un-seeded target returns a snapshot
// with RecordedStatus and Runtime both Unknown - "nothing has been
// observed yet" - rather than fabricating a Known/Absent value the fake
// has no basis for.
func (f *FakeSessionController) Read(_ context.Context, target SessionTarget) (SessionSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	s, ok := f.sessions[sessionKeyOf(target)]
	if !ok {
		s = &fakeSession{snapshot: SessionSnapshot{
			RecordedStatus: query.UnknownField[string]("no session seeded"),
			Runtime: SessionRuntime{
				Status: query.Unknown,
				Reason: "no observation has happened yet",
			},
			Transcript: SessionTranscript{
				Source:      SessionTranscriptPolled,
				HarnessKind: target.HarnessKind,
				Status:      SessionTranscriptUnknown,
			},
		}}
		f.sessions[sessionKeyOf(target)] = s
	}

	if s.closed {
		return SessionSnapshot{}, fmt.Errorf("fake session controller: target %s/%s is closed", target.Kind, target.ID)
	}

	snap := s.snapshot
	snap.Target = target
	snap.AsOf = f.now()
	return snap, nil
}

// Prompt implements SessionPrompt: records text against target's session so
// a test can assert what was sent. It never mutates Transcript itself - a
// prompt's effect is only visible on the next Seed/Read, matching ADR
// 0025's rule that PromptAgent's result is observed through the next poll.
func (f *FakeSessionController) Prompt(_ context.Context, target SessionTarget, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	s, ok := f.sessions[sessionKeyOf(target)]
	if !ok {
		return fmt.Errorf("fake session controller: prompt to unseeded target %s/%s", target.Kind, target.ID)
	}
	if s.closed {
		return fmt.Errorf("fake session controller: prompt to closed target %s/%s", target.Kind, target.ID)
	}
	s.prompts = append(s.prompts, text)
	return nil
}

// Close implements SessionClose: marks target closed so a subsequent Read
// or Prompt errors instead of silently continuing to serve a session
// mode has left.
func (f *FakeSessionController) Close(_ context.Context, target SessionTarget) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	s, ok := f.sessions[sessionKeyOf(target)]
	if !ok {
		return nil
	}
	s.closed = true
	return nil
}

// Reader adapts the controller to the SessionReader function type.
func (f *FakeSessionController) Reader() SessionReader { return f.Read }

// PromptFn adapts the controller to the SessionPrompt function type.
func (f *FakeSessionController) PromptFn() SessionPrompt { return f.Prompt }

// Closer adapts the controller to the SessionClose function type.
func (f *FakeSessionController) Closer() SessionClose { return f.Close }

// Prompts returns the prompts sent to target, in order, for test
// assertions. Returns nil for a target that was never prompted.
func (f *FakeSessionController) Prompts(target SessionTarget) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	s, ok := f.sessions[sessionKeyOf(target)]
	if !ok {
		return nil
	}
	out := make([]string, len(s.prompts))
	copy(out, s.prompts)
	return out
}
