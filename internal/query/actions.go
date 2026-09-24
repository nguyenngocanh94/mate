package query

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
		action("start", status == MateCreated, "Mate is recorded "+string(status)),
		action("resume", status == MateStopped, "Mate is recorded "+string(status)),
		action("onboard", false, "this Project already has a Mate"),
	}
}

func mateActions(m MateNode) []ActionAvailability {
	out := make([]ActionAvailability, 0, 4)
	if m.Designated.State != Known || m.Designated.Value.MateID == "" {
		// Absent and Unknown are different facts and must not share wording.
		// Absent means the read succeeded and there is no Mate, so one may be
		// created. Unknown means the read itself failed: nothing may be
		// offered, and the refusal may not claim the Project has no Mate.
		absent := m.Designated.State == Absent
		noMate := "this Project has no readable Mate"
		onboardReason := "the Mate designation could not be read; refresh before onboarding"
		if absent {
			noMate = "this Project has no Mate"
			onboardReason = "create and start this Project's Mate"
		}
		return []ActionAvailability{
			action("start", false, noMate),
			action("stop", false, "this Project has no readable Mate binding"),
			action("resume", false, noMate),
			action("onboard", absent, onboardReason),
		}
	}
	status := m.Designated.Value.Status
	out = append(out, action("start", status == MateCreated, "Mate is recorded "+string(status)))
	stop := m.Binding.State == Known && m.Binding.Value.Status == BindingActive
	stopReason := "no active binding to stop"
	if m.Binding.State == Unknown {
		stopReason = "binding is unknown; refresh before stopping"
	}
	out = append(out, action("stop", stop, stopReason))
	out = append(out, action("resume", status == MateStopped, "Mate is recorded "+string(status)))
	out = append(out, action("onboard", false, "this Project already has a Mate"))
	// TODO(task 10): v1 also published switch_harness here. Restarting a
	// Mate under another harness is mvp.md's task 10, and publishing a
	// capability nothing implements is exactly the drift this package
	// exists to prevent.
	return out
}

// crewActions publishes the per-Crew actions mate can offer today.
//
// retry and discard are not here: mvp.md has no retry at all (a Crew runs
// once), so offering one would publish a capability nothing implements.
// TODO(task 22): add merge once `mate merge <crew>` exists.
func crewActions(c CrewNode) []ActionAvailability {
	stop := c.Binding.State == Known && c.Binding.Value.Status == BindingActive
	stopReason := "no active binding to stop"
	if c.Binding.State == Unknown {
		stopReason = "binding is unknown; refresh before stopping"
	}
	repair := c.Binding.State == Known && c.Binding.Value.Status == BindingStale
	repairReason := "no stale binding is recorded; inspect worktree state before repair"
	if c.Binding.State == Unknown {
		repairReason = "binding is unknown; refresh before repair"
	}
	out := []ActionAvailability{
		action("stop", stop, stopReason),
		action("repair", repair, repairReason),
	}
	// diff (mvp.md task 21) needs only a branch to compare, and it needs it
	// in every state: reviewing a Crew is what a reader does *before*
	// deciding it is done, so tying the capability to a state would withhold
	// it exactly when somebody is trying to find out where a Crew got to.
	// An unreadable worktree row is Unknown, and Unknown is never treated as
	// "there is no branch": the refusal says the read failed.
	diff, diffReason := c.Worktree.IsKnown() && c.Worktree.Value.Branch != "",
		"this Crew records no branch to compare"
	if !c.Worktree.IsKnown() && c.Worktree.State != Absent {
		diffReason = "the worktree record could not be read; refresh before diffing"
	}
	out = append(out, action("diff", diff, diffReason))
	return out
}

// deriveActions fills capability DTOs after all dependent rows have been
// read. Keeping this in query means CLI/Console consumers share one answer.
func deriveActions(s *Snapshot) {
	s.Actions = workspaceActions()
	for pi := range s.Projects {
		p := &s.Projects[pi]
		p.Actions = projectActions(*p)
		p.Mate.Actions = mateActions(p.Mate)
		for ci := range p.Crews {
			p.Crews[ci].Actions = crewActions(p.Crews[ci])
		}
	}
}
