package query

import "github.com/nguyenngocanh94/matev2/internal/domain"

// ActionAvailability is capability derived only from committed snapshot
// state. Available is never a claim that an agent is alive; it only says the
// application service may be asked to attempt the operation. Unavailable
// actions remain present so callers can explain the refusal consistently.
type ActionAvailability struct {
	Action    string `json:"action"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

func action(name string, available bool, reason string) ActionAvailability {
	return ActionAvailability{Action: name, Available: available, Reason: reason}
}

func workspaceActions() []ActionAvailability {
	return []ActionAvailability{action("onboard", true, "add a Project to this workspace")}
}

func projectActions(p ProjectNode) []ActionAvailability {
	if p.Mate.Designated.State == Unknown {
		return []ActionAvailability{
			action("start", false, "Mate designation is unknown; refresh before starting"),
			action("resume", false, "Mate designation is unknown; refresh before resuming"),
			action("onboard", false, "Mate designation is unknown; refresh before onboarding"),
		}
	}
	if !p.Mate.Designated.IsKnown() || p.Mate.Designated.Value.MateID == "" {
		return []ActionAvailability{action("onboard", true, "create and start this Project's Mate")}
	}

	status := p.Mate.Designated.Value.Status
	return []ActionAvailability{
		action("start", status == domain.MateCreated, "Mate is recorded "+string(status)),
		action("resume", status == domain.MateStopped, "Mate is recorded "+string(status)),
		action("onboard", false, "this Project already has a Mate"),
	}
}

func mateActions(m MateNode) []ActionAvailability {
	out := make([]ActionAvailability, 0, 5)
	if m.Designated.State != Known || m.Designated.Value.MateID == "" {
		// Absent and Unknown are different facts and must not share wording.
		// Absent means the read succeeded and there is no Mate, so one may be
		// created. Unknown means the read itself failed: nothing may be
		// offered, and the refusal may not claim the Project has no Mate.
		absent := m.Designated.State == Absent
		noMate := "this Project has no readable Mate"
		onboardReason := "the Mate designation could not be read; refresh before onboarding"
		switchReason := "the Mate designation could not be read; refresh before switching harness"
		if absent {
			noMate = "this Project has no Mate"
			onboardReason = "create and start this Project's Mate"
			switchReason = "this Project has no Mate to switch"
		}
		return []ActionAvailability{
			action("start", false, noMate),
			action("stop", false, "this Project has no readable Mate binding"),
			action("resume", false, noMate),
			action("onboard", absent, onboardReason),
			action("switch_harness", false, switchReason),
		}
	}
	status := m.Designated.Value.Status
	out = append(out, action("start", status == domain.MateCreated, "Mate is recorded "+string(status)))
	stop := m.Binding.State == Known && m.Binding.Value.Status == BindingActive
	stopReason := "no active binding to stop"
	if m.Binding.State == Unknown {
		stopReason = "binding is unknown; refresh before stopping"
	}
	out = append(out, action("stop", stop, stopReason))
	out = append(out, action("resume", status == domain.MateStopped, "Mate is recorded "+string(status)))
	out = append(out, action("onboard", false, "this Project already has a Mate"))
	// A harness switch is a restart of this same Mate, so it is offered at
	// every recorded status. It stops the Mate first (domain.md invariant 9),
	// which is why a running Mate is not a refusal here.
	out = append(out, action("switch_harness", true, "restart this Mate under a different harness"))
	return out
}

func crewActions(c CrewNode, siblings []CrewNode) []ActionAvailability {
	stop := c.Binding.State == Known && c.Binding.Value.Status == BindingActive
	stopReason := "no active binding to stop"
	if c.Binding.State == Unknown {
		stopReason = "binding is unknown; refresh before stopping"
	}
	activeOther := false
	for _, other := range siblings {
		if other.CrewID == c.CrewID {
			continue
		}
		if other.Status.OccupiesRepoSlot() {
			activeOther = true
		}
	}
	retry := (c.Status == domain.CrewFailed || c.Status == domain.CrewNeedsRepair) && !activeOther
	retryReason := "recorded status is " + string(c.Status)
	if activeOther {
		retryReason = "another attempt is active"
	}
	repair := c.Binding.State == Known && c.Binding.Value.Status == BindingStale
	repairReason := "no stale binding is recorded; inspect worktree state before repair"
	if c.Binding.State == Unknown {
		repairReason = "binding is unknown; refresh before repair"
	}
	return []ActionAvailability{
		action("stop", stop, stopReason),
		action("retry", retry, retryReason),
		action("repair", repair, repairReason),
		discardAction(c),
	}
}

// discardAction is the snapshot-visible half of orchestration.DiscardCrew.
// DiscardCrew itself does not filter by Crew status: it refuses an open
// merge request, then a worktree that is not known-created (inspectDiscardCandidate).
// Those two facts are what this publishes. Live Git identity and the
// confirmed runtime stop are not on the snapshot, so they stay a service
// refusal rather than a preflight one.
func discardAction(c CrewNode) ActionAvailability {
	const name = "discard"
	switch c.OpenMerge.State {
	case Known:
		return action(name, false, "crew has an open merge request; refusing discard")
	case Unknown:
		return action(name, false, "merge requests could not be read; refresh before discard")
	}
	switch c.Worktree.State {
	case Known:
		if c.Worktree.Value.Status == WorktreeRecordedCreated {
			return action(name, true, "remove this Crew's worktree and branch")
		}
		return action(name, false, "worktree is recorded "+string(c.Worktree.Value.Status)+"; discard needs a known-created worktree")
	case Absent:
		reason := c.Worktree.Reason
		if reason == "" {
			reason = "no worktree is recorded"
		}
		return action(name, false, reason)
	default:
		return action(name, false, "worktree is unknown; refresh before discard")
	}
}

// deriveActions fills capability DTOs after all dependent rows have been
// read. Keeping this in query means CLI/Console consumers share one answer.
func deriveActions(s *Snapshot) {
	s.Actions = workspaceActions()
	for pi := range s.Projects {
		p := &s.Projects[pi]
		p.Actions = projectActions(*p)
		p.Mate.Actions = mateActions(p.Mate)
		for ci := range p.Tasks {
			t := &p.Tasks[ci]
			crew := make([]CrewNode, len(t.Crews))
			copy(crew, t.Crews)
			for i := range t.Crews {
				t.Crews[i].Actions = crewActions(t.Crews[i], crew)
			}
		}
	}
}
