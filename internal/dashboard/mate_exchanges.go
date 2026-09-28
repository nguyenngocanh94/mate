package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/diagnostics"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

type exchangeMessage struct {
	id      int64
	at      string
	kind    string
	channel string
	from    string
	to      string
	text    string
	ref     Ref
}

// mateExchanges groups the database's per-model-call turns by the harness's
// prompt id. sent.log supplies the actual prompt and the Mate's answer. Its
// hook echoes are kept only as a fallback: a hook can say "user prompt"
// without carrying the user's words, while the pane line carries them.
func (s *Server) mateExchanges(ctx context.Context, project string, running bool) ([]MateExchange, error) {
	actorID := timeline.MateActorID(project)
	calls, err := s.turns(ctx, actorID)
	if err != nil {
		return nil, err
	}
	performance, err := s.actorPerformance(ctx, actorID, !running, calls, nil, s.deps.now())
	if err != nil {
		return nil, err
	}
	overviews := make(map[string]diagnostics.Overview, len(performance.PromptTurns))
	for _, prompt := range performance.PromptTurns {
		overviews[prompt.ID] = prompt.Overview
	}
	callPrompts := make(map[string]string, len(calls))
	for _, segment := range performance.Segments {
		for _, callID := range segment.CallIDs {
			callPrompts[callID] = segment.PromptID
		}
	}
	groups := make([]MateExchange, 0)
	byKey := make(map[string]int)
	trigger := make(map[string]int64)
	for _, call := range calls {
		key := call.SessionID + "#prompt#" + call.HarnessTurnRef
		if call.HarnessTurnRef == "" {
			key = call.ID
		}
		if canonical := callPrompts[call.ID]; canonical != "" && !strings.HasPrefix(canonical, "unknown#") {
			// The projector may resolve a missing legacy prompt ID through an
			// exact native response alias. Use that confirmed membership here.
			key = canonical
		}
		index, ok := byKey[key]
		if !ok {
			index = len(groups)
			byKey[key] = index
			groups = append(groups, MateExchange{
				ID:        key,
				StartedAt: call.StartedAt, EndedAt: call.EndedAt,
				Activities: []MateActivity{}, Overview: diagnostics.EmptyOverview(),
			})
			trigger[groups[index].ID] = call.TriggerEventID
		}
		g := &groups[index]
		if call.StartedAt < g.StartedAt {
			g.StartedAt = call.StartedAt
			trigger[g.ID] = call.TriggerEventID
		}
		if call.EndedAt > g.EndedAt {
			g.EndedAt = call.EndedAt
		}
		g.ModelCalls++
		g.ToolCalls += call.ToolCount
		g.Tokens.Input += call.Tokens.Input
		g.Tokens.CacheRead += call.Tokens.CacheRead
		g.Tokens.CacheWrite += call.Tokens.CacheWrite
		g.Tokens.Output += call.Tokens.Output
		g.Tokens.Thinking += call.Tokens.Thinking
		g.Tokens.Total += call.Tokens.Total
		g.ContextAfter, g.Model = call.ContextAfter, call.Model
	}
	// A native prompt can already be executing tools before its first usage
	// record. Preserve that prompt and its evidence without inventing a call.
	for _, prompt := range performance.PromptTurns {
		if prompt.ModelCalls != 0 || prompt.PromptAt == "" || strings.HasPrefix(prompt.ID, "unknown#") {
			continue
		}
		groups = append(groups, MateExchange{
			ID: prompt.ID, Source: "unknown", Prompt: prompt.Prompt, PromptAt: exchangeTime(prompt.PromptAt),
			StartedAt: exchangeTime(prompt.PromptAt), EndedAt: exchangeTime(prompt.EndedAt),
			PromptRef:  Ref{Path: prompt.SourceRef.Path, Offset: prompt.SourceRef.Offset},
			Activities: []MateActivity{}, Overview: prompt.Overview,
		})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].StartedAt == groups[j].StartedAt {
			return groups[i].ID < groups[j].ID
		}
		return groups[i].StartedAt < groups[j].StartedAt
	})
	messages, err := s.exchangeMessages(ctx, project, actorID)
	if err != nil {
		return nil, err
	}
	byEvent := make(map[int64]exchangeMessage, len(messages))
	inputs := make([]exchangeMessage, 0)
	for _, m := range messages {
		byEvent[m.id] = m
		if m.to == actorID && m.channel != "hook" {
			inputs = append(inputs, m)
		}
	}
	paired := make([]bool, len(inputs))
	for i := range groups {
		g := &groups[i]
		if overview, ok := overviews[g.ID]; ok {
			g.Overview = overview
		} else if overview, ok := overviews["unknown#"+g.ID]; ok {
			// A missing harness prompt ID isolates exactly this model call;
			// it never borrows an adjacent prompt's recorded activities.
			g.Overview = overview
		}
		lower := ""
		if i > 0 {
			lower = groups[i-1].StartedAt
		}
		for j := len(inputs) - 1; j >= 0; j-- {
			m := inputs[j]
			if m.at > g.StartedAt || m.at <= lower || paired[j] {
				continue
			}
			g.Prompt, g.PromptAt, g.PromptRef = readablePrompt(m.text), m.at, m.ref
			g.Source = promptSource(m)
			paired[j] = true
			break
		}
		if g.Prompt != "" {
			continue
		}
		if m, ok := byEvent[trigger[g.ID]]; ok && m.text != "" && m.text != "auto mode off: user prompt" {
			g.Prompt, g.PromptAt, g.PromptRef = readablePrompt(m.text), m.at, m.ref
			g.Source = promptSource(m)
		} else {
			g.Prompt = "Prompt not recorded"
			g.PromptAt = g.StartedAt
			g.Source = "unknown"
		}
	}

	for _, m := range messages {
		if m.from != actorID || m.channel != "pane" {
			continue
		}
		i := exchangeAt(groups, m.at)
		if i < 0 {
			continue
		}
		g := &groups[i]
		if m.to == timeline.UserActorID(project) {
			if g.Response != "" {
				g.Response += "\n\n"
			}
			g.Response += m.text
			g.ResponseAt, g.ResponseRef = m.at, m.ref
		} else if strings.HasPrefix(m.to, timeline.ActorCrew+":"+project+":") {
			crew := strings.TrimPrefix(m.to, timeline.ActorCrew+":"+project+":")
			g.Activities = append(g.Activities, MateActivity{
				At: m.at, Kind: "crew.answer", Crew: crew, Text: m.text,
			})
		}
	}
	activities, err := s.exchangeActivities(ctx, project, actorID)
	if err != nil {
		return nil, err
	}
	for _, activity := range activities {
		if i := exchangeAt(groups, activity.At); i >= 0 {
			groups[i].Activities = append(groups[i].Activities, activity)
		}
	}
	for i := range groups {
		g := &groups[i]
		if g.ResponseAt != "" {
			d := db.ParseTime(g.ResponseAt).Sub(db.ParseTime(g.PromptAt)).Milliseconds()
			if d < 0 {
				d = 0
			}
			g.DurationMs = &d
		}
		sort.Slice(g.Activities, func(i, j int) bool { return g.Activities[i].At < g.Activities[j].At })
	}
	// A captain's question or crew digest remains visible even when the
	// transcript has no matching model call yet. It may be queued, pending
	// ingest, or truly unanswered; the page must not silently drop the ask.
	for i, m := range inputs {
		if paired[i] {
			continue
		}
		groups = append(groups, MateExchange{
			ID: fmt.Sprintf("prompt#%d", m.id), Source: promptSource(m),
			Prompt: readablePrompt(m.text), PromptAt: m.at, StartedAt: m.at,
			Activities: []MateActivity{}, PromptRef: m.ref, Overview: diagnostics.EmptyOverview(),
		})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].StartedAt == groups[j].StartedAt {
			return groups[i].ID < groups[j].ID
		}
		return groups[i].StartedAt < groups[j].StartedAt
	})
	return groups, nil
}

// Exchange boundaries use the database's fixed precision timestamps. Native
// RFC3339Nano can omit fractional digits, which would sort incorrectly as text.
func exchangeTime(at string) string {
	if at == "" {
		return ""
	}
	return db.FormatTime(db.ParseTime(at))
}

func promptSource(m exchangeMessage) string {
	if m.channel == "digest" || m.kind == timeline.KindAssignClicked || strings.HasPrefix(m.text, "digest:") || strings.HasPrefix(m.text, "resolve:") {
		return "crew"
	}
	if strings.HasPrefix(m.from, timeline.ActorUser+":") {
		return "captain"
	}
	return "system"
}

func readablePrompt(text string) string {
	if strings.HasPrefix(text, "digest:") {
		if _, rest, ok := strings.Cut(text, " — "); ok {
			text = rest
		}
		if head, _, ok := strings.Cut(text, " — status files under "); ok {
			text = head
		}
		text = strings.ReplaceAll(text, " needs-decision: ", " asks: ")
		text = strings.ReplaceAll(text, " wait-mate: ", " reports: ")
	}
	return strings.TrimPrefix(text, "resolve: ")
}

// exchangeAt uses the prompt's first model call as a boundary. sent.log has
// second-resolution timestamps, so a message just before that call belongs
// to the next exchange when its prompt has already been observed.
func exchangeAt(groups []MateExchange, at string) int {
	if len(groups) == 0 {
		return -1
	}
	i := sort.Search(len(groups), func(i int) bool { return groups[i].StartedAt > at }) - 1
	if i < 0 {
		if groups[0].PromptAt != "" && at >= groups[0].PromptAt {
			return 0
		}
		return -1
	}
	if i+1 < len(groups) && at >= groups[i+1].PromptAt && groups[i+1].PromptAt != "" {
		return i + 1
	}
	return i
}

func (s *Server) exchangeMessages(ctx context.Context, project, mateID string) ([]exchangeMessage, error) {
	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT e.id,e.at,e.kind,m.channel,m.from_actor_id,m.to_actor_id,m.text,e.ref_path,e.ref_offset
		  FROM message m JOIN event e ON e.id=m.event_id
		 WHERE e.project=? AND (m.from_actor_id=? OR m.to_actor_id=?)
		 ORDER BY e.at,e.id`, project, mateID, mateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []exchangeMessage{}
	for rows.Next() {
		var m exchangeMessage
		if err := rows.Scan(&m.id, &m.at, &m.kind, &m.channel, &m.from, &m.to, &m.text,
			&m.ref.Path, &m.ref.Offset); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Server) exchangeActivities(ctx context.Context, project, mateID string) ([]MateActivity, error) {
	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT at,kind,payload FROM event
		 WHERE project=? AND actor_id=? AND kind IN (?,?,?,?,?)
		 ORDER BY at,id`, project, mateID, timeline.KindCrewSpawned,
		timeline.KindCrewFinished, timeline.KindCrewFailed, timeline.KindMergeDone,
		timeline.KindReviewStarted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MateActivity{}
	for rows.Next() {
		var at, kind, payload string
		if err := rows.Scan(&at, &kind, &payload); err != nil {
			return nil, err
		}
		var p struct {
			Crew   string `json:"crew"`
			Task   string `json:"task"`
			Into   string `json:"into"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return nil, fmt.Errorf("decode Mate activity %s: %w", kind, err)
		}
		a := MateActivity{At: at, Kind: kind, Crew: p.Crew}
		switch kind {
		case timeline.KindCrewSpawned:
			a.Text = "Started: " + p.Task
		case timeline.KindCrewFinished:
			a.Text = "Closed as finished"
		case timeline.KindCrewFailed:
			a.Text = "Closed as failed"
			if p.Reason != "" {
				a.Text += ": " + p.Reason
			}
		case timeline.KindMergeDone:
			a.Text = "Merged into " + p.Into
		case timeline.KindReviewStarted:
			a.Text = "Started a review"
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
