package capability_test

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/capability"
)

type thing interface{ Do() }

type impl struct{}

func (impl) Do() {}

var proof = capability.Evidence{Version: "x 1.0", Measured: "2026-10-08", Proof: "TestThing"}

// set is a capabilities struct as a registry's contract declares one.
type set struct {
	First  capability.Cap[thing]
	Second capability.Cap[thing]
}

func TestCheckAcceptsEveryDeclaredState(t *testing.T) {
	s := set{
		First:  capability.Cap[thing]{Status: capability.Verified, Impl: impl{}, Evidence: proof},
		Second: capability.Cap[thing]{Status: capability.Unsupported, Reason: "it has none"},
	}
	if err := capability.Check(s); err != nil {
		t.Fatalf("Check of a fully declared set: %v", err)
	}
	s.Second = capability.Cap[thing]{Status: capability.Unknown, Reason: "not measured yet"}
	if err := capability.Check(s); err != nil {
		t.Fatalf("Check with an unknown capability: %v", err)
	}
}

func TestCheckRefusesEachBrokenDeclaration(t *testing.T) {
	ok := capability.Cap[thing]{Status: capability.Unsupported, Reason: "none"}
	for _, tc := range []struct {
		name string
		cap  capability.Cap[thing]
		want string
	}{
		{"undeclared", capability.Cap[thing]{}, "Second is undeclared"},
		{"unknown status", capability.Cap[thing]{Status: "maybe", Reason: "r"}, `Second has status "maybe"`},
		{"verified without impl", capability.Cap[thing]{Status: capability.Verified, Evidence: proof}, "Impl is set exactly when verified"},
		{"impl without verified", capability.Cap[thing]{Status: capability.Unknown, Impl: impl{}, Reason: "r"}, "Impl is set exactly when verified"},
		{"verified without evidence", capability.Cap[thing]{Status: capability.Verified, Impl: impl{}, Evidence: capability.Evidence{Version: "x 1.0"}}, "Second is verified without full evidence"},
		{"unsupported without reason", capability.Cap[thing]{Status: capability.Unsupported, Reason: "  "}, "Second is unsupported without a reason"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := capability.Check(set{First: ok, Second: tc.cap})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Check = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestCheckRefusesWhatIsNotACapabilitySet(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    any
	}{
		{"not a struct", 3},
		{"no fields", struct{}{}},
		{"a field that is not a Cap", struct {
			A capability.Cap[thing]
			B string
		}{}},
		{"a Cap over a concrete type", struct{ A capability.Cap[int] }{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := capability.Check(tc.v); err == nil {
				t.Fatal("Check accepted it")
			}
		})
	}
}

// Declarations names each field, so a contract suite can say which
// capability a refusal is about.
func TestDeclarationsReadEveryField(t *testing.T) {
	decls, err := capability.Declarations(set{
		First:  capability.Cap[thing]{Status: capability.Verified, Impl: impl{}, Evidence: proof},
		Second: capability.Cap[thing]{Status: capability.Unknown, Reason: "later"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) != 2 {
		t.Fatalf("%d declarations, want 2", len(decls))
	}
	if d := decls[0]; d.Name != "First" || d.Status != capability.Verified || !d.HasImpl || d.Evidence != proof {
		t.Errorf("First = %+v", d)
	}
	if d := decls[1]; d.Name != "Second" || d.Status != capability.Unknown || d.HasImpl || d.Reason != "later" {
		t.Errorf("Second = %+v", d)
	}
}

func TestVerified(t *testing.T) {
	if !(capability.Cap[thing]{Status: capability.Verified}).Verified() {
		t.Error("a verified Cap is not Verified()")
	}
	if (capability.Cap[thing]{Status: capability.Unknown}).Verified() {
		t.Error("an unknown Cap is Verified()")
	}
	var zero capability.CapStatus
	if zero != capability.Undeclared {
		t.Error("the zero status is not Undeclared")
	}
}
