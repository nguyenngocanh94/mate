package scene

import (
	"encoding/json"

	"github.com/nguyenngocanh94/mate/internal/db"
)

// The two line shapes `mate events --scene` prints. The field order is the
// JSON field order and it is part of the contract, the same way
// timeline.StoryEvent's is: one row per line, a field is added at the end and
// never in the middle, so a consumer can diff two runs byte for byte.
type transitionLine struct {
	ID        string `json:"id"`
	At        string `json:"at"`
	Project   string `json:"project"`
	Actor     string `json:"actor"`
	ActorKind string `json:"actor_kind"`
	From      string `json:"from"`
	To        string `json:"to"`
	Detail    string `json:"detail,omitempty"`
	Target    string `json:"target,omitempty"`
	Event     int64  `json:"event"`
}

type nowLine struct {
	Actor       string `json:"actor"`
	ActorKind   string `json:"actor_kind"`
	Project     string `json:"project"`
	State       string `json:"state"`
	Since       string `json:"since,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Target      string `json:"target,omitempty"`
	TokensToday int64  `json:"tokens_today"`
}

// JSONLine renders one transition as the line `--scene --follow` prints.
func (r Row) JSONLine() (string, error) {
	raw, err := json.Marshal(transitionLine{
		ID:        r.ID,
		At:        db.FormatTime(r.At),
		Project:   r.Project,
		Actor:     r.ActorName,
		ActorKind: r.ActorKind,
		From:      string(r.From),
		To:        string(r.To),
		Detail:    r.Detail,
		Target:    r.TargetName,
		Event:     r.EventID,
	})
	return string(raw), err
}

// JSONLine renders one snapshot row as the line `--scene` prints.
func (n NowRow) JSONLine() (string, error) {
	raw, err := json.Marshal(nowLine{
		Actor:       n.ActorName,
		ActorKind:   n.ActorKind,
		Project:     n.Project,
		State:       string(n.State),
		Since:       db.FormatTime(n.Since),
		Detail:      n.Detail,
		Target:      n.TargetName,
		TokensToday: n.TokensToday,
	})
	return string(raw), err
}
