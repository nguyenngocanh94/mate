package runtime

import (
	"errors"
	"testing"

	"github.com/nguyenngocanh94/matev2/internal/observability"
)

func TestSanitizeAgentName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		prefix string
		raw    string
		want   string
		err    error
	}{
		{name: "short mate", prefix: "m", raw: "mate_001", want: "m-mate_001"},
		{name: "crew id", prefix: "c", raw: "crew-a1", want: "c-crew-a1"},
		{name: "empty raw is rejected", prefix: "mate", raw: "", err: ErrEmptyAgentName},
		{name: "blank raw is rejected", prefix: "mate", raw: "   ", err: ErrEmptyAgentName},
		{name: "empty prefix", prefix: "", raw: "x", err: ErrEmptyAgentName},
		{name: "prefix starts with digit", prefix: "1m", raw: "x", err: ErrInvalidAgentName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := SanitizeAgentName(tc.prefix, tc.raw)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if !ValidAgentName(got) {
				t.Fatalf("%q is not a valid Herdr agent name", got)
			}
		})
	}
}

func TestSanitizeAgentNameTruncatedHashCollision(t *testing.T) {
	t.Parallel()
	const prefix = "abcdefghijklmnopqrstuv"
	a, err := SanitizeAgentName(prefix, "id-120227X")
	if err != nil {
		t.Fatal(err)
	}
	b, err := SanitizeAgentName(prefix, "id-139869X")
	if err != nil {
		t.Fatal(err)
	}
	const want = "abcdefghijklmnopqrstuv--87b98bfa"
	if a != want || b != want {
		t.Fatalf("got %q and %q, want both %q (documented truncated-hash collision)", a, b, want)
	}
}

func TestAllocateAgentNameFailsOnDocumentedCollision(t *testing.T) {
	t.Parallel()
	const prefix = "abcdefghijklmnopqrstuv"
	reg := NewMemoryNameRegistry()
	session := "mate-ws1"
	first, err := AllocateAgentName(reg, session, prefix, "id-120227X", FailOnCollision)
	if err != nil {
		t.Fatal(err)
	}
	if first.Name() != "abcdefghijklmnopqrstuv--87b98bfa" {
		t.Fatalf("first = %q", first.Name())
	}
	if first.RawID() != "id-120227X" || first.Session() != session {
		t.Fatalf("reservation must bind the owning raw id and session: %+v", first)
	}
	_, err = AllocateAgentName(reg, session, prefix, "id-139869X", FailOnCollision)
	if !errors.Is(err, ErrNameCollision) {
		t.Fatalf("err = %v, want ErrNameCollision", err)
	}
	if observability.ExitCode(err) != observability.ExitStateConflict {
		t.Fatalf("collision must map to already_exists/exit 10, got %d", observability.ExitCode(err))
	}
	// Same raw id is idempotent, not a collision.
	again, err := AllocateAgentName(reg, session, prefix, "id-120227X", FailOnCollision)
	if err != nil || again != first {
		t.Fatalf("idempotent reserve: got %+v err %v", again, err)
	}
}

func TestAllocateAgentNameRetryWithNonceAvoidsCollision(t *testing.T) {
	t.Parallel()
	const prefix = "abcdefghijklmnopqrstuv"
	reg := NewMemoryNameRegistry()
	session := "mate-ws1"
	if _, err := AllocateAgentName(reg, session, prefix, "id-120227X", FailOnCollision); err != nil {
		t.Fatal(err)
	}
	got, err := AllocateAgentName(reg, session, prefix, "id-139869X", RetryWithNonce)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name() == "abcdefghijklmnopqrstuv--87b98bfa" {
		t.Fatal("retry policy must not reuse the colliding live name")
	}
	if !ValidAgentName(got.Name()) {
		t.Fatalf("retry name %q is invalid", got.Name())
	}
	owner, occupied := reg.Occupied(session, got.Name())
	if !occupied || owner != "id-139869X" {
		t.Fatalf("reserved owner = %q occupied=%v", owner, occupied)
	}
}

func TestAllocateAgentNameRequiresRegistry(t *testing.T) {
	t.Parallel()
	if _, err := AllocateAgentName(nil, "s", "m", "id", FailOnCollision); err == nil {
		t.Fatal("nil registry must be rejected; uniqueness cannot be assumed")
	}
}

func TestSanitizeAgentNameDoesNotProveUniqueness(t *testing.T) {
	t.Parallel()
	// The sanitizer is a pure function. Two callers that only sanitize and
	// then start would both believe they own the colliding name. Allocation
	// is the contract that closes that hole.
	const prefix = "abcdefghijklmnopqrstuv"
	a, _ := SanitizeAgentName(prefix, "id-120227X")
	b, _ := SanitizeAgentName(prefix, "id-139869X")
	if a != b {
		t.Fatal("test fixture no longer collides; update the documented pair")
	}
}
