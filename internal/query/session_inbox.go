package query

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/nguyenngocanh94/matev2/internal/application"
	"github.com/nguyenngocanh94/matev2/internal/domain"
)

// InboxEntryKind keeps completion handoffs separate from Crew↔Mate
// interactions. A completion is never an interaction awaiting a reply.
type InboxEntryKind string

const (
	InboxEntryInteraction InboxEntryKind = "interaction"
	InboxEntryCompletion  InboxEntryKind = "completion"
)

// InboxEntry is one committed inbox item for the Console session view's
// inbox rail (ADR 0025 step 5, extended by ADR 0029 §4). It is a query-layer
// read model, kept deliberately separate from anything the transcript
// observes at runtime (internal/ui/console.SessionTranscript): interaction
// items come from persistence.InteractionRecord and completion items come
// from persistence.CrewCompletionRecord. Neither is inferred from a live
// agent poll. cmd/mate/session_bridge.go maps this 1:1 onto
// console.SessionInboxEntry; the two shapes are kept apart deliberately so
// this package never has to import internal/ui/console (doc.go's boundary).
type InboxEntry struct {
	Kind          InboxEntryKind
	InteractionID string
	Status        domain.InteractionStatus
	// Awaiting mirrors domain.InteractionStatus.AwaitsReply on the status
	// already read - never recomputed from a transcript, a poll, or any
	// other runtime observation (ADR 0025's own rule for RecordedStatus
	// applies here too: this is committed state, not inferred state).
	Awaiting bool
	Question string
	Reply    string
	SentAt   time.Time
	// Attempt and Task are resolved from the interaction's own CrewID/TaskID
	// against the store, e.g. "attempt 2" / "fix the flaky test" - see
	// resolveAttemptLabel/resolveTaskLabel.
	Attempt string
	Task    string
	// Note is an optional caveat about how this entry was recorded. Always
	// empty from LoadMateInbox today: ADR 0014's `stopped` sweep
	// (ReleaseCrewBindingAndStopDeliveries) only ever moves an interaction
	// addressed *to a Crew* to that status, and this read only ever returns
	// interactions addressed to a Mate, so a Mate's own rows never reach
	// `stopped` through that path. The field stays on the type - and on
	// console.SessionInboxEntry, which this maps onto - for a caveat that a
	// future caller (a Crew-facing read, should one ever be built) would
	// have something true to say here.
	Note string
	// Completion fields are populated only when Kind is completion. The
	// completion's CrewStatus is durable lifecycle state; it is intentionally
	// not squeezed into InteractionStatus.
	CompletionID     string
	CompletionStatus domain.CrewStatus
	Outcome          string
	ReportPath       string
	ReportRevision   string
	ArtifactRefs     []string
	PRURL            string
	PRHeadSHA        string
	SourceEventID    string
}

// inboxRailLimit bounds how many of a Mate's most recent items in each rail
// this read considers, so a poll tick's extra reads (see LoadMateInbox's doc
// comment) stay bounded regardless of how much interaction history a
// long-lived workspace accumulates. The rail itself only ever has room for a
// handful of entries (session-view-contract.md's packing), so a limit this
// size is already far more than any breakpoint can display.
const inboxRailLimit = 50

// LoadMateInbox reads mateID's committed Crew->Mate interactions for the
// session view's inbox rail, newest first - the shape
// session_render.go's sessionRailLines/sessionDigestLines expect (entries
// arrive newest-first; both drop from the tail when they do not all fit).
//
// A Crew that is currently being stopped never causes this read to show a
// question as still-awaiting past the point that is actually true: ADR 0014
// (ReleaseCrewBindingAndStopDeliveries) moves every queued|delivered
// interaction addressed to a Crew being stopped to the terminal `stopped`
// status inside the same transaction that releases its runtime binding, and
// does so before that transaction commits. This read only ever observes
// committed state - InteractionRecord.Status - so it never needs its own
// copy of that exclusion: a row this read returns is either still genuinely
// open, or already `stopped`, never a stale "awaiting" view of one that
// ADR 0014 has already closed out. This function creates no interaction, no
// claim and no reply of its own; claim/reply/version/permission stay in
// application.AskMate/ReplyToCrew/ClaimDueInteractions, which this read does
// not call.
//
// Cost: one indexed interaction read (interaction_recipient, migration 0009)
// and one indexed completion read (crew_completion_mate_created, migration
// 0015), each bounded by inboxRailLimit, plus one GetCrew and one GetTask per
// *distinct* Crew/Task id among the returned rows (deduplicated in this call,
// not per row). This mirrors the budget cmd/mate/session_bridge.go
// already established for RecordedStatus: targeted point reads on a
// 300-500ms poll tick, not internal/query.LoadSnapshot's whole-workspace
// aggregate. At Phase 1 workspace volumes (a handful of open Tasks/Crews per
// project) this is a small, fixed number of extra point reads per tick, not
// a join; a workspace whose Crew/Task count per Mate grows large would need
// to lower inboxRailLimit before this floor matters, since the dedup only
// bounds queries per tick, not per historical Crew/Task.
func LoadMateInbox(ctx context.Context, deps application.Deps, mateID string) ([]InboxEntry, error) {
	interactions, err := deps.Store.ListInteractionsForRecipient(ctx, mateID, inboxRailLimit)
	if err != nil {
		return nil, err
	}
	completions, err := deps.Store.ListCrewCompletionsForMate(ctx, mateID, inboxRailLimit)
	if err != nil {
		return nil, err
	}
	crews := map[string]string{} // crewID -> "attempt N" (or a fallback)
	tasks := map[string]string{} // taskID -> title (or a fallback)
	entries := make([]InboxEntry, 0, len(interactions)+len(completions))
	for _, r := range interactions {
		attempt, err := resolveAttemptLabel(ctx, deps, crews, r.CrewID)
		if err != nil {
			return nil, err
		}
		task, err := resolveTaskLabel(ctx, deps, tasks, r.TaskID)
		if err != nil {
			return nil, err
		}
		entries = append(entries, InboxEntry{
			Kind:          InboxEntryInteraction,
			InteractionID: r.InteractionID,
			Status:        r.Status,
			Awaiting:      r.Status.AwaitsReply(),
			Question:      r.QuestionText,
			Reply:         r.ReplyText,
			SentAt:        r.SentAt,
			Attempt:       attempt,
			Task:          task,
		})
	}
	for _, r := range completions {
		attempt, err := resolveAttemptLabel(ctx, deps, crews, r.CrewID)
		if err != nil {
			return nil, err
		}
		task, err := resolveTaskLabel(ctx, deps, tasks, r.TaskID)
		if err != nil {
			return nil, err
		}
		entries = append(entries, InboxEntry{
			Kind: InboxEntryCompletion, CompletionID: r.CompletionID,
			CompletionStatus: r.CrewStatus, Outcome: r.Outcome,
			ReportPath: r.ReportPath, ReportRevision: r.ReportRevision,
			ArtifactRefs: append([]string(nil), r.ArtifactRefs...), PRURL: r.PRURL,
			PRHeadSHA: r.PRHeadSHA, SourceEventID: r.SourceEventID,
			SentAt: r.CreatedAt, Attempt: attempt, Task: task,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].SentAt.Equal(entries[j].SentAt) {
			if entries[i].Kind != entries[j].Kind {
				return entries[i].Kind == InboxEntryCompletion
			}
			return entries[i].InteractionID+entries[i].CompletionID > entries[j].InteractionID+entries[j].CompletionID
		}
		return entries[i].SentAt.After(entries[j].SentAt)
	})
	if len(entries) > inboxRailLimit {
		entries = entries[:inboxRailLimit]
	}
	return entries, nil
}

// resolveAttemptLabel resolves crewID to "attempt N" via GetCrew, caching
// the result per call so a rail with several requests from the same Crew
// attempt costs one read, not one per interaction. A read failure or an
// empty crewID degrades to a plain label rather than failing the whole rail
// - the rail's own caveat convention (session.go's SessionInboxEntry.Note)
// is for a recorded fact about the interaction, not a label placeholder, so
// this deliberately does not invent a Note for it.
func resolveAttemptLabel(ctx context.Context, deps application.Deps, cache map[string]string, crewID string) (string, error) {
	if crewID == "" {
		return "no crew attempt recorded", nil
	}
	if label, ok := cache[crewID]; ok {
		return label, nil
	}
	label := "attempt unknown"
	if crew, err := deps.Store.GetCrew(ctx, crewID); err == nil {
		label = fmt.Sprintf("attempt %d", crew.Attempt)
	}
	cache[crewID] = label
	return label, nil
}

// resolveTaskLabel resolves taskID to its title via GetTask, cached the same
// way resolveAttemptLabel is.
func resolveTaskLabel(ctx context.Context, deps application.Deps, cache map[string]string, taskID string) (string, error) {
	if taskID == "" {
		return "no task recorded", nil
	}
	if label, ok := cache[taskID]; ok {
		return label, nil
	}
	label := "task unknown"
	if task, err := deps.Store.GetTask(ctx, taskID); err == nil {
		label = task.Title
	}
	cache[taskID] = label
	return label, nil
}
