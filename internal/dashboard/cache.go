package dashboard

import "sync"

// cache holds one rendered response per request, valid for exactly one
// `event.id`.
//
// The ingest only appends to `event`, so `MAX(event.id)` is the whole
// freshness signal of this database: while it stands still nothing any
// endpoint reads can have changed, and when it moves every endpoint may
// have. That makes the cache a single generation rather than a per-key
// expiry - one integer read decides whether the bytes on hand are still the
// truth, and a move drops all of them at once instead of leaving one page
// showing a scene two ids old beside another showing the current one.
//
// The inbox is the one input that is not `event`: it is read from
// `crews/*.status`, `sent.log` and `incidents.log` (box.Inbox, through the
// loader the console uses). Every line of all three becomes an event on the
// observer's next pass - `status.appended`, `message.sent`,
// `incident.opened` - so the id moves for anything the inbox can show; what
// the generation does not promise is the few seconds between a file growing
// and the ingest reading it, which is the same lag the console's own rail
// has.
type cache struct {
	mu      sync.Mutex
	id      int64
	entries map[string][]byte
	// builds counts how many times a response was actually computed rather
	// than served from the generation on hand. The cache test reads it;
	// nothing in production does.
	builds int64
}

func newCache() *cache { return &cache{entries: map[string][]byte{}} }

// get returns the bytes cached for key at generation id, if any. A
// generation that has moved on takes every entry with it.
func (c *cache) get(key string, id int64) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.id != id {
		c.id = id
		c.entries = map[string][]byte{}
		return nil, false
	}
	body, ok := c.entries[key]
	return body, ok
}

// put records bytes for key at generation id, unless the generation has
// moved while they were being built - in which case they are already stale
// and are dropped rather than served to the next reader.
func (c *cache) put(key string, id int64, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.builds++
	if c.id != id {
		return
	}
	c.entries[key] = body
}
