package diagnostics

// LoopSummary answers "how often did the Crew repeat itself" in one row.
// Polling numbers come from native process identities, so they are only
// claimed when the harness reported native execution status; the finding
// counters do not depend on that and stay valid for every harness.
type LoopSummary struct {
	Measured        bool    `json:"measured"`
	Chains          int     `json:"chains"`
	Polls           int     `json:"polls"`
	UnchangedPolls  int     `json:"unchanged_polls"`
	ProgressPolls   int     `json:"progress_polls"`
	UnknownPolls    int     `json:"unknown_polls"`
	ProcessesPolled int     `json:"processes_polled"`
	PollCalls       int     `json:"poll_calls"`
	PollTokens      *Tokens `json:"poll_tokens"`
	RepeatedReads   int     `json:"repeated_reads"`
	RepeatedErrors  int     `json:"repeated_errors"`
	RepairLoops     int     `json:"repair_loops"`
	SegmentRepeats  int     `json:"segment_repeats"`
	Coverage        string  `json:"coverage"`
}

const (
	loopsMeasured   = "Polling chains need native process identities; this harness reports them."
	loopsUnmeasured = "Polling chains are not measurable for this harness (no native process identities); repeated reads and retries are still detected."
)

// summarizeLoops runs after detection: findings, processes and segment repeat
// counts are final by then. Nothing here is a new measurement; every number
// is a count over rows the response already carries.
func (p *projection) summarizeLoops() {
	l := LoopSummary{Measured: p.capabilities["native execution status"], Coverage: loopsUnmeasured}
	for _, f := range p.out.Findings {
		switch f.Kind {
		case "repeated_read":
			l.RepeatedReads++
		case "repeated_error":
			l.RepeatedErrors++
		case "repair_test_loop":
			l.RepairLoops++
		case "process_polling":
			// A chain is a claim about one native process; without native
			// status the identity behind it is not established.
			if l.Measured {
				l.Chains++
			}
		}
	}
	// Each wait segment owns its calls exclusively, so poll calls and their
	// tokens add up like any other segment slice of the ledger. They are
	// measured for every harness; nil means no such segment, not zero usage.
	for _, s := range p.out.Segments {
		l.SegmentRepeats += s.RepeatCount
		if s.Kind != "wait" {
			continue
		}
		l.PollCalls += s.ModelCalls
		if l.PollTokens == nil {
			l.PollTokens = &Tokens{}
		}
		l.PollTokens.Add(s.Tokens)
	}
	if !l.Measured {
		p.out.Loops = l
		return
	}
	l.Coverage = loopsMeasured
	for _, proc := range p.out.Processes {
		if proc.Polls == 0 {
			continue
		}
		l.ProcessesPolled++
		l.Polls += proc.Polls
		l.UnchangedPolls += proc.UnchangedPolls
		l.ProgressPolls += proc.ProgressPolls
		l.UnknownPolls += proc.UnknownPolls
	}
	p.out.Loops = l
}
