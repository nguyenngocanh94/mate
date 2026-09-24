package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/nguyenngocanh94/mate/internal/observability"
)

const (
	// HerdrAgentNameMax is the Herdr 0.8.2 live-agent name cap:
	// [a-z][a-z0-9_-]{0,31}.
	HerdrAgentNameMax = 32
)

var (
	// ErrEmptyAgentName is returned when prefix or raw id is missing.
	ErrEmptyAgentName = errors.New("empty agent name")
	// ErrInvalidAgentName is returned when a name cannot be made valid.
	ErrInvalidAgentName = errors.New("invalid agent name")
	// ErrNameCollision is returned when two distinct raw ids sanitize to the
	// same live name and the collision policy refuses to invent a new name.
	// It is errNameCollision so a durable registry in another package
	// returns the very sentinel AllocateAgentName tests with errors.Is.
	ErrNameCollision = errNameCollision
)

// CollisionPolicy is the explicit contract for sanitized-name collisions.
// SanitizeAgentName is collision-resistant, not injective: a truncated hash
// cannot guarantee uniqueness. G1 produced a real colliding pair (see
// docs/phase1/decisions/0003-g1-herdr-feasibility.md). No caller may assume
// a sanitized name is unique.
type CollisionPolicy int

const (
	// FailOnCollision returns already_exists / ErrNameCollision when the
	// sanitized name is already reserved for a different raw id.
	FailOnCollision CollisionPolicy = iota
	// RetryWithNonce re-sanitizes with a bounded nonce suffix and retries
	// reservation. Exhaustion is still already_exists.
	RetryWithNonce
)

// LiveNameRegistry is the session-scoped set of live Herdr agent names.
// Uniqueness comes from this registry, never from SanitizeAgentName alone.
//
// MemoryNameRegistry is process-local (tests, G2); since G3 the durable
// backer is persistence.NameRegistry, one per workspace database (ADR
// 0005), so a reservation is a cross-process fact within one workspace but
// still Mate's recorded intent. Herdr's agent_name_taken is the final
// arbiter for agent names; nothing arbitrates session names (the durable
// registry refuses that scope). ADR 0004 (Runtime port) states what every
// backer must guarantee, including how a reservation left by a crash is
// treated.
type LiveNameRegistry interface {
	// Occupied reports whether name is reserved in session and, if so, which
	// raw domain id owns it. ownerRawID is empty when occupied is false.
	Occupied(session, name string) (ownerRawID string, occupied bool)
	// Reserve records name for rawID. It must fail if name is already held
	// by a different rawID.
	Reserve(session, name, rawID string) error
	// Release drops a reservation. Missing names are not an error.
	Release(session, name string)
}

// SanitizeAgentName maps a domain-style id onto a Herdr live agent name.
// prefix must already match [a-z][a-z0-9_-]* and is kept stable.
//
// A missing raw id is an error, not a bare prefix.
//
// The mapping is collision-resistant, not injective. Typical ids keep the
// readable form "<prefix>-<raw>". Every other raw id maps to
// "<prefix>--<hash>" with a truncated SHA-256. A 22-character prefix leaves
// 32 hash bits, and those bits collide: SanitizeAgentName("abcdefghijklmnopqrstuv",
// "id-120227X") and SanitizeAgentName("abcdefghijklmnopqrstuv", "id-139869X")
// both yield "abcdefghijklmnopqrstuv--87b98bfa". Callers MUST run the
// result through AllocateAgentName / a LiveNameRegistry.
func SanitizeAgentName(prefix, raw string) (string, error) {
	if err := checkNamePrefix(prefix); err != nil {
		return "", err
	}
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("%w: raw id is missing", ErrEmptyAgentName)
	}
	body := sanitizeBody(raw)
	if body == raw {
		if candidate := prefix + "-" + body; validAgentName(candidate) {
			return candidate, nil
		}
	}
	sum := sha256.Sum256([]byte(prefix + "\x00" + raw))
	hexed := hex.EncodeToString(sum[:])
	room := HerdrAgentNameMax - len(prefix) - 2
	if room < 8 {
		return "", fmt.Errorf("%w: prefix %q leaves no room for a hash", ErrInvalidAgentName, prefix)
	}
	if room > len(hexed) {
		room = len(hexed)
	}
	out := prefix + "--" + hexed[:room]
	if !validAgentName(out) {
		return "", fmt.Errorf("%w: %q", ErrInvalidAgentName, out)
	}
	return out, nil
}

// NameReservation proves one live agent name was reserved for one raw domain
// id in one session. Only AllocateAgentName produces a non-zero reservation,
// so a caller outside this package cannot hold a live name without the raw
// id that owns it, and NewAgentStartSpec cannot be given a name that skipped
// the registry. A reservation is proof of allocation, not of current
// ownership: the adapter's start path still re-reserves atomically, so a
// name released and re-allocated since fails as a collision.
type NameReservation struct {
	session string
	name    string
	rawID   string
}

// Session is the Herdr session the name is reserved in.
func (r NameReservation) Session() string { return r.session }

// Name is the reserved live agent name.
func (r NameReservation) Name() string { return r.name }

// RawID is the domain agent id that owns Name.
func (r NameReservation) RawID() string { return r.rawID }

// IsZero reports whether the reservation proves nothing.
func (r NameReservation) IsZero() bool {
	return r.session == "" || r.name == "" || r.rawID == ""
}

// AllocateAgentName sanitizes raw and reserves the live name. Occupied by
// the same rawID is idempotent success. Occupied by a different rawID is a
// collision and follows policy. The returned reservation binds the reserved
// live name to the raw id that owns it.
func AllocateAgentName(reg LiveNameRegistry, session, prefix, raw string, policy CollisionPolicy) (NameReservation, error) {
	if reg == nil {
		return NameReservation{}, fmt.Errorf("allocate agent name: live-name registry is required")
	}
	if strings.TrimSpace(session) == "" {
		return NameReservation{}, fmt.Errorf("allocate agent name: session is required")
	}
	if strings.TrimSpace(raw) == "" {
		return NameReservation{}, fmt.Errorf("%w: raw id is missing", ErrEmptyAgentName)
	}
	return allocate(reg, session, prefix, raw, raw, policy, 0)
}

const maxCollisionRetries = 8

func allocate(reg LiveNameRegistry, session, prefix, raw, nonceSource string, policy CollisionPolicy, attempt int) (NameReservation, error) {
	name, err := SanitizeAgentName(prefix, nonceSource)
	if err != nil {
		return NameReservation{}, err
	}
	// Reserve is the atomic uniqueness step. Occupied-then-Reserve would
	// lose a race and skip RetryWithNonce.
	if err := reg.Reserve(session, name, raw); err != nil {
		if errors.Is(err, ErrNameCollision) {
			return resolveCollision(reg, session, prefix, raw, name, policy, attempt)
		}
		return NameReservation{}, err
	}
	return NameReservation{session: session, name: name, rawID: raw}, nil
}

func resolveCollision(reg LiveNameRegistry, session, prefix, raw, name string, policy CollisionPolicy, attempt int) (NameReservation, error) {
	switch policy {
	case RetryWithNonce:
		if attempt >= maxCollisionRetries {
			return NameReservation{}, collisionError(name, raw)
		}
		next := fmt.Sprintf("%s~%d", raw, attempt+1)
		return allocate(reg, session, prefix, raw, next, policy, attempt+1)
	default:
		return NameReservation{}, collisionError(name, raw)
	}
}

func collisionError(name, raw string) error {
	return observability.WrapError(
		observability.CodeAlreadyExists,
		fmt.Sprintf("sanitized agent name %q is already live for a different id (raw %q); sanitizer is not injective", name, raw),
		ErrNameCollision,
	)
}

func checkNamePrefix(prefix string) error {
	if prefix == "" {
		return ErrEmptyAgentName
	}
	if prefix[0] < 'a' || prefix[0] > 'z' {
		return ErrInvalidAgentName
	}
	for i := 1; i < len(prefix); i++ {
		if !allowedNameByte(prefix[i]) {
			return ErrInvalidAgentName
		}
	}
	if len(prefix) >= HerdrAgentNameMax {
		return ErrInvalidAgentName
	}
	return nil
}

func sanitizeBody(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		if r > unicode.MaxASCII {
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
			continue
		}
		c := byte(r)
		if allowedNameByte(c) {
			b.WriteByte(c)
			lastDash = c == '-' || c == '_'
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-_")
}

func allowedNameByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

func validAgentName(s string) bool {
	if s == "" || len(s) > HerdrAgentNameMax {
		return false
	}
	if s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !allowedNameByte(s[i]) {
			return false
		}
	}
	return true
}

// ValidAgentName reports whether s matches [a-z][a-z0-9_-]{0,31}.
func ValidAgentName(s string) bool {
	return validAgentName(s)
}
