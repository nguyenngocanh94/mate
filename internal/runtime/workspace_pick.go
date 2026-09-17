package runtime

import "strings"

// pickWorkspaceID chooses one Herdr workspace among several that share
// label+cwd. Occupied ids (a live agent) win over empty ones; ties break by
// workspace-id order. This is the recovery for a lookup-then-create race that
// produced duplicates: start must become possible again without a human
// closing extras by hand.
//
// What the tie-break has to be is a *deterministic total order*, so that two
// processes handed the same set of duplicates adopt the same workspace and
// close the same extras. That is the whole requirement, and it is all this is
// documented to give.
//
// It is explicitly **not** "the oldest workspace". Herdr 0.8.2 ids are opaque
// and not monotone: live in a lab session they ran w1..w9, wA..wH, wJ, wK, wM
// (Crockford base32 - I, L, O, U skipped), and after w1Z the next two creates
// returned w10 and then w21. No ordering of those strings is creation order.
// An earlier reading of this code promised "lowest numeric id (w1 < w2 < w10)"
// and, past nine workspaces, silently fell back to byte order instead.
//
// Age is not needed anyway: an occupied duplicate always outranks an empty
// one, and among empty duplicates the extras are closed, so any choice every
// process agrees on is correct.
func pickWorkspaceID(ids []string, occupied map[string]bool) string {
	if len(ids) == 0 {
		return ""
	}
	var preferred []string
	for _, id := range ids {
		if occupied[id] {
			preferred = append(preferred, id)
		}
	}
	pool := ids
	if len(preferred) > 0 {
		pool = preferred
	}
	chosen := pool[0]
	for _, id := range pool[1:] {
		if workspaceIDLess(id, chosen) {
			chosen = id
		}
	}
	return chosen
}

// workspaceIDLess is that total order: by id-suffix length, then bytes, then
// the whole id. Length first keeps it stable for ids of mixed width; nothing
// beyond totality and stability is claimed.
func workspaceIDLess(a, b string) bool {
	sa, sb := workspaceIDSuffix(a), workspaceIDSuffix(b)
	if len(sa) != len(sb) {
		return len(sa) < len(sb)
	}
	if sa != sb {
		return sa < sb
	}
	return a < b
}

// workspaceIDSuffix is the counter part of a Herdr workspace id ("w1" -> "1").
// An id with no leading prefix is returned whole.
func workspaceIDSuffix(id string) string {
	id = strings.TrimSpace(id)
	return strings.TrimLeft(id, "wW")
}
