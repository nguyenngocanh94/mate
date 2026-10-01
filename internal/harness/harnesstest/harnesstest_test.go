package harnesstest

import (
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
)

func TestReadingVisibleChangesOnlyTheReadSource(t *testing.T) {
	base := catalog.Default()
	r := ReadingVisible(base)
	if got, want := r.Kinds(), base.Kinds(); len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for _, role := range []harness.AgentRole{harness.RoleMate, harness.RoleCrew} {
		got, _ := r.Default(role)
		want, _ := base.Default(role)
		if got != want {
			t.Errorf("default %s = %q, want %q", role, got, want)
		}
	}
	for _, k := range base.Kinds() {
		p, err := r.Lookup(k)
		if err != nil {
			t.Fatal(err)
		}
		orig, _ := base.Lookup(k)
		if src := p.Screen().ReadSource(); src != harness.ReadVisible {
			t.Errorf("%s reads %q, want visible", k, src)
		}
		if p.Kind() != k || p.Screen().ReadyScreen() != orig.Screen().ReadyScreen() {
			t.Errorf("%s: the wrapper changed more than the read source", k)
		}
	}
}
