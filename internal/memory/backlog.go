package memory

// backlog.md's sections (docs/research/firstmate-memory-2026-09-24.md A3,
// B12). The Mate writes the file; the app only seeds it and never touches it
// again, and `matev2 backlog` is the live table it is reconciled against.
const (
	BacklogInFlight = "In flight"
	// BacklogHeld holds every question sent to the captain and every
	// promise the Mate made, each with the verbatim question, the date and
	// what it waits on, so a restarted Mate knows what it asked.
	BacklogHeld   = "Held for the captain"
	BacklogQueued = "Queued"
	BacklogDone   = "Done"
)

// BacklogSections is backlog.md's sections in file order.
var BacklogSections = []string{BacklogInFlight, BacklogHeld, BacklogQueued, BacklogDone}

// DoneKeep is how many Done entries backlog.md keeps; older ones move to
// BacklogArchiveName (firstmate `.tasks.toml` `done_keep = 10`).
const DoneKeep = 10

// The Mate's two cold files, never read at a session start.
const (
	ArchiveName        = "memory-archive.md"
	BacklogArchiveName = "backlog-archive.md"
)

// StowLine is the line the app sends the Mate, after the `⟦matev2⟧ `
// sentinel, just before it restarts it (docs/mvp.md task 37, B7). The stow
// skill quotes it and a test holds the two together.
const StowLine = "stow: you are about to be restarted; record anything that exists only in this conversation (skill stow), then end your turn"

// BacklogHeader is what a fresh backlog.md starts with.
func BacklogHeader() string {
	s := "# Backlog\n"
	for _, sec := range BacklogSections {
		s += "\n## " + sec + "\n"
	}
	return s
}
