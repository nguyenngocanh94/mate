package memory

import (
	"reflect"
	"testing"
)

// The section's entries come back in file order, each split into the head
// the manual prescribes (task, date), the words sent to the captain, and
// what the entry waits on. Other sections are not held.
func TestHeldEntriesReadsTheHeldSection(t *testing.T) {
	text := `# Backlog

## In flight
- fix-cart-total: crew working

## Held for the captain
- checkout-button, 2026-09-24: asked "Should the button link to the classic checkout page or the express one?" Waits on: the captain's choice; then brief append their words to checkout-button.
- go-live, 2026-09-20: promised an end-to-end run
  once the accounts exist. Waits on: the captain.

## Queued
- later-thing: blocked-by: checkout-button

## Done
`
	want := []HeldEntry{
		{
			ID: "checkout-button", Date: "2026-09-24",
			Text:     `asked "Should the button link to the classic checkout page or the express one?"`,
			WaitsOn:  "the captain's choice; then brief append their words to checkout-button.",
			Question: "Should the button link to the classic checkout page or the express one?",
			Raw:      `checkout-button, 2026-09-24: asked "Should the button link to the classic checkout page or the express one?" Waits on: the captain's choice; then brief append their words to checkout-button.`,
		},
		{
			ID: "go-live", Date: "2026-09-20",
			Text:    "promised an end-to-end run once the accounts exist.",
			WaitsOn: "the captain.",
			Raw:     "go-live, 2026-09-20: promised an end-to-end run once the accounts exist. Waits on: the captain.",
		},
	}
	if got := HeldEntries(text); !reflect.DeepEqual(got, want) {
		t.Errorf("HeldEntries =\n%#v\nwant\n%#v", got, want)
	}
}

// The Mate writes the section by hand: an entry without the prescribed head
// or without `Waits on:` is still an open question and is shown whole.
func TestHeldEntriesKeepsAnEntryThatIsNotInTheManualsShape(t *testing.T) {
	got := HeldEntries("## Held for the captain\n- which database do you want?\n")
	want := []HeldEntry{{Text: "which database do you want?", Raw: "which database do you want?"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("HeldEntries = %#v, want %#v", got, want)
	}
}

func TestHeldEntriesIsEmptyWhenNothingIsHeld(t *testing.T) {
	for name, text := range map[string]string{
		"fresh backlog": BacklogHeader(),
		"no section":    "# Backlog\n\n## Queued\n- a: b\n",
		"empty file":    "",
	} {
		if got := HeldEntries(text); len(got) != 0 {
			t.Errorf("%s: HeldEntries = %#v, want none", name, got)
		}
	}
}
