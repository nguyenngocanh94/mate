package console

import (
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// designTree is the acme workspace the design boards draw (A-K): twelve
// Projects, payments-api the busy one. Every value is one the query layer
// can actually return; where a board shows something the backend does not
// record, the fixture leaves it out and so does the frame.
func designTree() query.Snapshot {
	at := func(h, m int) time.Time { return time.Date(2026, 9, 10, h, m, 0, 0, time.UTC) }
	running := func(project string, since time.Time) query.MateNode {
		return designMate(project, query.MateRunning, since)
	}
	quiet := func(n int) []query.CrewNode {
		out := make([]query.CrewNode, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, designCrew("c"+string(rune('a'+i)), "a crew", query.CrewWorking, query.HarnessClaude, at(13, 30)))
		}
		return out
	}
	simple := func(name string, mate query.MateNode, crews []query.CrewNode) query.ProjectNode {
		p := query.ProjectNode{
			ProjectID: name, Name: name, Mode: query.ModeManual, Mate: mate, Crews: crews,
			Repos:     query.KnownField([]query.RepoValue{{RepoID: name, DisplayName: name, Path: "~/src/" + name, DefaultBranch: "main"}}),
			Box:       query.KnownField(query.BoxView{}),
			Attention: query.KnownField(query.ProjectAttention{}),
		}
		return p
	}
	docs := simple("docs-site", absentMate("this project has no designated Mate"), quiet(1))
	docs.Attention = query.KnownField(query.ProjectAttention{Kind: query.AttentionNoMate, Why: "the project has no designated Mate, so no crew can be spawned"})
	search := simple("search-indexer", designMate("search-indexer", query.MateUnknown, at(12, 0)), quiet(1))
	search.Attention = query.KnownField(query.ProjectAttention{Kind: query.AttentionMateUnknown, Why: "the project's Mate is recorded unknown"})

	return query.Snapshot{
		WorkspaceID: "ws_acme",
		Workspace:   query.KnownField(query.WorkspaceValue{Name: "acme", Root: "/Users/dev/work/acme"}),
		Projects: []query.ProjectNode{
			simple("auth-gateway", running("auth-gateway", at(13, 0)), quiet(1)),
			simple("billing-worker", designMate("billing-worker", query.MateCreated, time.Time{}), nil),
			simple("customer-notifications-dispatcher", running("customer-notifications-dispatcher", at(12, 0)), quiet(1)),
			docs,
			simple("infra-terraform", running("infra-terraform", at(11, 0)), quiet(2)),
			simple("ledger-recon", designMate("ledger-recon", query.MateStopped, time.Time{}), nil),
			simple("mobile-app", running("mobile-app", at(10, 0)), quiet(3)),
			designPayments(),
			search,
			simple("shipping-rates", running("shipping-rates", at(9, 0)), quiet(1)),
			simple("web-checkout", running("web-checkout", at(9, 30)), quiet(1)),
			simple("zendesk-sync", designMate("zendesk-sync", query.MateCreated, time.Time{}), nil),
		},
	}
}

// designPayments is payments-api: a running claude Mate, a codex crew at
// work, a claude crew waiting on the captain, and one failed crew in
// Completed.
func designPayments() query.ProjectNode {
	at := func(h, m int) time.Time { return time.Date(2026, 9, 10, h, m, 0, 0, time.UTC) }
	mate := designMate("payments-api", query.MateRunning, at(13, 24))
	mate.Tokens = query.KnownField(query.TokenValue{Total: 182_000})
	mate.LastEvent = query.KnownField(query.EventValue{EventType: "status", OccurredAt: at(14, 1)})

	index := designCrew("k7", "Add index to orders.created_at", query.CrewWorking, query.HarnessCodex, at(13, 38))
	backfill := designCrew("k3", "Backfill ledger v2", query.CrewNeedsDecision, query.HarnessClaude, at(13, 29))
	backfill.Attention = query.KnownField(query.Attention{
		Kind: query.AttentionDecision,
		Why:  "crew k3 asked a question and stopped its turn; it waits on an answer",
	})
	migrate := designCrew("k1", "Migrate ledger v1", query.CrewFailed, query.HarnessClaude, at(12, 40))
	migrate.Closed = true
	migrate.Error = query.KnownField(query.ErrorReason("exit 1"))
	migrate.Attention = query.KnownField(query.Attention{Kind: query.AttentionFailed, Why: "crew k1 failed: exit 1"})

	question := "Plan ready: backfill ledger_v2 in 3 batches of 50k rows. Dry run on staging: 0 diffs. Run on the prod replica now?"
	ask := query.BoxEntry{
		Seq: 4, At: at(13, 56), Kind: query.BoxStatus, Source: "crew", Target: "crew:k3",
		Crew: "k3", Verb: "needs-decision", Text: question, Attention: true,
		Resolve: testResolveLine("k3", question),
	}
	for _, c := range []*query.CrewNode{&index, &backfill, &migrate} {
		c.ProjectID = "payments-api"
	}
	return query.ProjectNode{
		ProjectID: "payments-api", Name: "payments-api", Mode: query.ModeManual,
		Mate:        mate,
		Repos:       query.KnownField([]query.RepoValue{{RepoID: "payments-api", DisplayName: "payments-api", Path: "~/src/payments-api", DefaultBranch: "main"}}),
		Crews:       []query.CrewNode{index, backfill, migrate},
		ClosedCrews: 1,
		Attention:   query.KnownField(query.ProjectAttention{CrewsNeedingAttention: 2}),
		Box: query.KnownField(query.BoxView{
			Entries: []query.BoxEntry{ask}, Inbox: []query.BoxEntry{ask},
			Crews: 1, Awaiting: 1, LastAt: at(13, 56),
		}),
	}
}

func designMate(project string, status query.MateStatus, since time.Time) query.MateNode {
	agent := project + ".mate"
	mate := query.MateNode{
		Designated: query.KnownField(query.MateIdentity{
			MateID: "mate_" + project, HarnessKind: query.HarnessClaude, Status: status, IsDefault: true,
		}),
		AgentName: query.KnownField(agent),
		Error:     query.AbsentField[query.ErrorReason](notErrorState),
	}
	switch status {
	case query.MateRunning, query.MateUnknown:
		mate.Binding = query.KnownField(query.BindingValue{
			Status: query.BindingActive, AgentName: agent, Runtime: "herdr", Session: "mate-acme",
			BoundSince: since, BoundSinceKind: query.BoundSinceActivated,
		})
	default:
		mate.Binding = query.AbsentField[query.BindingValue]("the Mate is not running")
	}
	return mate
}

func designCrew(id, task string, status query.CrewStatus, harness query.HarnessKind, created time.Time) query.CrewNode {
	agent := "crew-" + id
	return query.CrewNode{
		CrewID: id, Task: task, Status: status, HarnessKind: harness, CreatedAt: created,
		AgentName: query.KnownField(agent),
		Worktree: query.KnownField(query.WorktreeValue{
			Path: "~/src/.worktrees/" + id, Branch: "crew/" + id, Status: query.WorktreeRecordedCreated,
		}),
		Repo: query.KnownField(query.RepoValue{RepoID: "payments-api", DisplayName: "payments-api", Path: "~/src/payments-api", DefaultBranch: "main"}),
		Binding: query.KnownField(query.BindingValue{
			Status: query.BindingActive, AgentName: agent, Runtime: "herdr", Session: "mate-acme",
			BoundSince: created, BoundSinceKind: query.BoundSinceActivated,
		}),
		Error:     query.AbsentField[query.ErrorReason](notErrorState),
		Attention: query.AbsentField[query.Attention]("nothing needs attention"),
	}
}
