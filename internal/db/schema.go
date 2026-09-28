package db

// SchemaVersion is the version Migrate brings a database up to. It is the
// same number docs/timeline.md prints at the top of its schema section; a
// reader that finds a different one in `schema_version` is reading a file
// this build does not understand.
const SchemaVersion = 3

// migration is one ordered, all-or-nothing step. Each runs inside the same
// transaction that records its version, so a half-applied schema cannot
// survive a crash.
type migration struct {
	version int
	stmts   []string
}

// migrations are applied in order, skipping every version already recorded.
// A shipped migration is never edited: a change to the schema is a new entry
// with the next version.
var migrations = []migration{
	{version: 1, stmts: schema1},
	{version: 2, stmts: schema2},
	{version: 3, stmts: schema3},
}

// schema1 is the M5 schema of docs/mvp.md. Times are RFC3339 with nanosecond
// precision in UTC, stored as TEXT, so they sort lexicographically and a
// `v_story` dump is byte-identical between two rebuilds of the same files.
//
// Ids are TEXT and derived from the source everywhere except `event`, whose
// id is the AUTOINCREMENT the spec asks for because a live reader follows the
// timeline with `event.id > ?`. Every other row is keyed by what produced it
// (a transcript path and a record's source ref, a log path and a byte
// offset), so re-reading a source can only rewrite the row it already wrote.
var schema1 = []string{
	`CREATE TABLE actor (
		id          TEXT PRIMARY KEY,
		project     TEXT NOT NULL,
		kind        TEXT NOT NULL,
		name        TEXT NOT NULL,
		harness     TEXT NOT NULL DEFAULT '',
		first_seen  TEXT,
		last_seen   TEXT,
		gone_at     TEXT
	)`,
	`CREATE INDEX actor_project ON actor(project, kind)`,

	`CREATE TABLE session (
		id                      TEXT PRIMARY KEY,
		actor_id                TEXT NOT NULL REFERENCES actor(id),
		harness_session_id      TEXT NOT NULL DEFAULT '',
		transcript_path         TEXT NOT NULL DEFAULT '',
		started_at              TEXT,
		ended_at                TEXT,
		resumed_from_session_id TEXT
	)`,
	`CREATE INDEX session_actor ON session(actor_id, started_at)`,

	`CREATE TABLE task (
		crew_actor_id        TEXT PRIMARY KEY REFERENCES actor(id),
		project              TEXT NOT NULL,
		text                 TEXT NOT NULL DEFAULT '',
		brief_path           TEXT NOT NULL DEFAULT '',
		branch               TEXT NOT NULL DEFAULT '',
		worktree             TEXT NOT NULL DEFAULT '',
		spawned_at           TEXT,
		closed_at            TEXT,
		close_state          TEXT,
		close_cause_event_id INTEGER,
		merged_event_id      INTEGER,
		question_count       INTEGER NOT NULL DEFAULT 0,
		handback_count       INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX task_project ON task(project, spawned_at)`,

	// dedup is not in the spec's column list and is the one addition to it.
	// An ingest re-reads a source whenever the trailing bytes of that source
	// may still grow - a transcript's open message group is re-parsed whole
	// on every poll - so without a natural key the same fact would become a
	// second row with a second id, and a dashboard following `event.id > ?`
	// would replay history as news. The key is what produced the fact (kind,
	// actor, source ref), so INSERT OR IGNORE makes an ingest idempotent
	// without any cursor bookkeeping the parser cannot support.
	`CREATE TABLE event (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		dedup            TEXT NOT NULL UNIQUE,
		project          TEXT NOT NULL,
		at               TEXT NOT NULL,
		actor_id         TEXT NOT NULL REFERENCES actor(id),
		kind             TEXT NOT NULL,
		subject_actor_id TEXT REFERENCES actor(id),
		turn_id          TEXT,
		task_actor_id    TEXT,
		cause_event_id   INTEGER,
		payload          TEXT NOT NULL DEFAULT '{}',
		ref_path         TEXT NOT NULL DEFAULT '',
		ref_offset       INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX event_project_at ON event(project, at)`,
	`CREATE INDEX event_actor_at ON event(actor_id, at)`,
	`CREATE INDEX event_kind_at ON event(kind, at)`,
	`CREATE INDEX event_cause ON event(cause_event_id)`,
	`CREATE INDEX event_task_at ON event(task_actor_id, at)`,

	`CREATE TABLE turn (
		id                   TEXT PRIMARY KEY,
		actor_id             TEXT NOT NULL REFERENCES actor(id),
		session_id           TEXT NOT NULL REFERENCES session(id),
		ordinal              INTEGER NOT NULL DEFAULT 0,
		started_at           TEXT,
		ended_at             TEXT,
		trigger_event_id     INTEGER,
		outcome              TEXT NOT NULL DEFAULT '',
		model                TEXT NOT NULL DEFAULT '',
		harness_turn_ref     TEXT NOT NULL DEFAULT '',
		input_tokens         INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens    INTEGER NOT NULL DEFAULT 0,
		cache_write_tokens   INTEGER NOT NULL DEFAULT 0,
		output_tokens        INTEGER NOT NULL DEFAULT 0,
		thinking_tokens      INTEGER NOT NULL DEFAULT 0,
		context_tokens_after INTEGER NOT NULL DEFAULT 0,
		tool_count           INTEGER NOT NULL DEFAULT 0,
		ref_path             TEXT NOT NULL DEFAULT '',
		ref_offset           INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX turn_actor_at ON turn(actor_id, started_at)`,
	`CREATE INDEX turn_session ON turn(session_id, ordinal)`,

	`CREATE TABLE action (
		id          TEXT PRIMARY KEY,
		turn_id     TEXT REFERENCES turn(id),
		event_id    INTEGER,
		at          TEXT NOT NULL,
		ended_at    TEXT,
		actor_id    TEXT NOT NULL REFERENCES actor(id),
		tool        TEXT NOT NULL,
		target      TEXT NOT NULL DEFAULT '',
		summary     TEXT NOT NULL DEFAULT '',
		duration_ms INTEGER,
		ok          INTEGER
	)`,
	`CREATE INDEX action_target ON action(target)`,
	`CREATE INDEX action_turn ON action(turn_id)`,
	`CREATE INDEX action_actor_at ON action(actor_id, at)`,

	`CREATE TABLE message (
		event_id      INTEGER PRIMARY KEY,
		from_actor_id TEXT NOT NULL REFERENCES actor(id),
		to_actor_id   TEXT NOT NULL REFERENCES actor(id),
		channel       TEXT NOT NULL,
		text          TEXT NOT NULL DEFAULT '',
		marked        INTEGER NOT NULL DEFAULT 0
	)`,

	`CREATE TABLE question (
		id                   TEXT PRIMARY KEY,
		asked_event_id       INTEGER NOT NULL,
		crew_actor_id        TEXT NOT NULL REFERENCES actor(id),
		text                 TEXT NOT NULL DEFAULT '',
		asked_at             TEXT NOT NULL,
		answered_event_id    INTEGER,
		answered_by_actor_id TEXT,
		answered_at          TEXT,
		waited_ms            INTEGER
	)`,
	`CREATE INDEX question_crew ON question(crew_actor_id, asked_at)`,

	`CREATE TABLE incident (
		id                 TEXT PRIMARY KEY,
		project            TEXT NOT NULL,
		actor_id           TEXT NOT NULL REFERENCES actor(id),
		kind               TEXT NOT NULL,
		opened_event_id    INTEGER NOT NULL,
		opened_at          TEXT NOT NULL,
		resolved_event_id  INTEGER,
		resolved_at        TEXT
	)`,
	`CREATE INDEX incident_actor ON incident(actor_id, opened_at)`,

	`CREATE TABLE usage_sample (
		id         TEXT PRIMARY KEY,
		session_id TEXT NOT NULL REFERENCES session(id),
		at         TEXT NOT NULL,
		cumulative INTEGER NOT NULL DEFAULT 0,
		input      INTEGER NOT NULL DEFAULT 0,
		cache_read INTEGER NOT NULL DEFAULT 0,
		cache_write INTEGER NOT NULL DEFAULT 0,
		output     INTEGER NOT NULL DEFAULT 0,
		thinking   INTEGER NOT NULL DEFAULT 0,
		ref_path   TEXT NOT NULL DEFAULT '',
		ref_offset INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX usage_sample_session ON usage_sample(session_id, at)`,

	// transition is written by task 26's scene projection. The table and its
	// index exist now because v_now reads them and because a view whose
	// shape is decided late is a view every consumer has to be changed for.
	`CREATE TABLE transition (
		id              TEXT PRIMARY KEY,
		actor_id        TEXT NOT NULL REFERENCES actor(id),
		project         TEXT NOT NULL,
		from_state      TEXT NOT NULL DEFAULT '',
		to_state        TEXT NOT NULL,
		at              TEXT NOT NULL,
		event_id        INTEGER NOT NULL,
		target_actor_id TEXT,
		detail          TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX transition_actor_at ON transition(actor_id, at)`,

	// pricing is filled by task 27 from `.mate/pricing.yaml`. v_task_ledger
	// already joins it, so a ledger gains cost the moment a row lands here
	// and reports NULL cost - not zero - until then.
	`CREATE TABLE pricing (
		model              TEXT PRIMARY KEY,
		input_per_m        REAL NOT NULL DEFAULT 0,
		cache_read_per_m   REAL NOT NULL DEFAULT 0,
		cache_write_per_m  REAL NOT NULL DEFAULT 0,
		output_per_m       REAL NOT NULL DEFAULT 0,
		effective_from     TEXT
	)`,

	`CREATE TABLE cursor (
		source_path TEXT PRIMARY KEY,
		byte_offset INTEGER NOT NULL DEFAULT 0,
		updated_at  TEXT NOT NULL
	)`,

	`CREATE TABLE schema_version (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`,

	// v_story is the timeline as a story: one row per event, with the names
	// a reader would say out loud instead of the ids a join would need.
	`CREATE VIEW v_story AS
		SELECT
			e.id               AS id,
			e.project          AS project,
			e.at               AS at,
			e.kind             AS kind,
			e.actor_id         AS actor_id,
			a.kind             AS actor_kind,
			a.name             AS actor_name,
			e.subject_actor_id AS subject_actor_id,
			s.name             AS subject_name,
			e.task_actor_id    AS task_actor_id,
			t.name             AS task_name,
			e.turn_id          AS turn_id,
			e.cause_event_id   AS cause_event_id,
			c.kind             AS cause_kind,
			e.payload          AS payload,
			e.ref_path         AS ref_path,
			e.ref_offset       AS ref_offset
		FROM event e
		JOIN actor a ON a.id = e.actor_id
		LEFT JOIN actor s ON s.id = e.subject_actor_id
		LEFT JOIN actor t ON t.id = e.task_actor_id
		LEFT JOIN event c ON c.id = e.cause_event_id`,

	// v_task_ledger is one row per task: what it cost, how often it had to
	// come back and ask, and how long it stood at the CEO's door. Cost is
	// NULL until `pricing` has a row for the model, because a missing price
	// is not a price of zero.
	`CREATE VIEW v_task_ledger AS
		SELECT
			t.crew_actor_id   AS crew_actor_id,
			t.project         AS project,
			a.name            AS crew,
			t.text            AS text,
			t.branch          AS branch,
			t.spawned_at      AS spawned_at,
			t.closed_at       AS closed_at,
			t.close_state     AS close_state,
			t.question_count  AS question_count,
			t.handback_count  AS handback_count,
			(SELECT COUNT(*) FROM turn u WHERE u.actor_id = t.crew_actor_id) AS turns,
			(SELECT COALESCE(SUM(u.input_tokens),0)       FROM turn u WHERE u.actor_id = t.crew_actor_id) AS input_tokens,
			(SELECT COALESCE(SUM(u.cache_read_tokens),0)  FROM turn u WHERE u.actor_id = t.crew_actor_id) AS cache_read_tokens,
			(SELECT COALESCE(SUM(u.cache_write_tokens),0) FROM turn u WHERE u.actor_id = t.crew_actor_id) AS cache_write_tokens,
			(SELECT COALESCE(SUM(u.output_tokens),0)      FROM turn u WHERE u.actor_id = t.crew_actor_id) AS output_tokens,
			(SELECT COALESCE(SUM(u.thinking_tokens),0)    FROM turn u WHERE u.actor_id = t.crew_actor_id) AS thinking_tokens,
			(SELECT u.context_tokens_after FROM turn u WHERE u.actor_id = t.crew_actor_id
			  ORDER BY u.started_at DESC, u.ordinal DESC LIMIT 1) AS context_tokens_last,
			(SELECT COALESCE(SUM(q.waited_ms),0) FROM question q WHERE q.crew_actor_id = t.crew_actor_id) AS waited_ms,
			(SELECT SUM(
				u.input_tokens       * COALESCE(p.input_per_m, 0) / 1000000.0 +
				u.cache_read_tokens  * COALESCE(p.cache_read_per_m, 0) / 1000000.0 +
				u.cache_write_tokens * COALESCE(p.cache_write_per_m, 0) / 1000000.0 +
				u.output_tokens      * COALESCE(p.output_per_m, 0) / 1000000.0)
			  FROM turn u JOIN pricing p ON p.model = u.model
			  WHERE u.actor_id = t.crew_actor_id) AS cost,
			(SELECT e.at FROM event e WHERE e.id = t.merged_event_id) AS merged_at
		FROM task t
		JOIN actor a ON a.id = t.crew_actor_id`,

	// v_now is one row per actor: where the scene machine last put it, since
	// when, who it is facing, and what it has spent today. The scene columns
	// read `transition`, which task 26 writes; until then an actor's state is
	// NULL, which is the honest answer for "no projection has run".
	`CREATE VIEW v_now AS
		SELECT
			a.id      AS actor_id,
			a.kind    AS actor_kind,
			a.name    AS actor_name,
			a.project AS project,
			r.to_state        AS state,
			r.at              AS since,
			r.target_actor_id AS target_actor_id,
			r.detail          AS detail,
			(SELECT COALESCE(SUM(u.input_tokens + u.cache_read_tokens + u.cache_write_tokens + u.output_tokens), 0)
			   FROM turn u
			  WHERE u.actor_id = a.id
			    AND substr(u.started_at, 1, 10) = strftime('%Y-%m-%d', 'now')) AS tokens_today
		FROM actor a
		LEFT JOIN transition r
		  ON r.id = (SELECT x.id FROM transition x WHERE x.actor_id = a.id
		              ORDER BY x.at DESC, x.id DESC LIMIT 1)`,
}

// schema2 is task 27's economics migration (mvp.md M5 task 27,
// docs/timeline.md's Economics section): a context window per model, and
// the two views recreated with the columns that need it.
//
// A view is not data - dropping and recreating one changes no row - so this
// migration is free to give `v_task_ledger` a richer shape outright. `v_now`
// gets exactly one column added at the end, `context_pct`: task 26 owns
// `state`/`since`/`target_actor_id`/`detail` and reads them by name, so
// every one of those keeps its old position and its old SQL untouched here.
var schema2 = []string{
	`ALTER TABLE pricing ADD COLUMN context_window INTEGER NOT NULL DEFAULT 0`,

	`DROP VIEW v_task_ledger`,

	// v_task_ledger, plus: the model and context size of the crew's most
	// recent turn, and the resulting context_pct - NULL when either the
	// crew has no turn yet or that turn's model has no context_window
	// priced. cost keeps the meaning docs/timeline.md already gives it:
	// NULL until pricing has a row for every turn's model, never zero.
	`CREATE VIEW v_task_ledger AS
		SELECT
			t.crew_actor_id   AS crew_actor_id,
			t.project         AS project,
			a.name            AS crew,
			t.text            AS text,
			t.branch          AS branch,
			t.spawned_at      AS spawned_at,
			t.closed_at       AS closed_at,
			t.close_state     AS close_state,
			t.question_count  AS question_count,
			t.handback_count  AS handback_count,
			(SELECT COUNT(*) FROM turn u WHERE u.actor_id = t.crew_actor_id) AS turns,
			(SELECT COALESCE(SUM(u.input_tokens),0)       FROM turn u WHERE u.actor_id = t.crew_actor_id) AS input_tokens,
			(SELECT COALESCE(SUM(u.cache_read_tokens),0)  FROM turn u WHERE u.actor_id = t.crew_actor_id) AS cache_read_tokens,
			(SELECT COALESCE(SUM(u.cache_write_tokens),0) FROM turn u WHERE u.actor_id = t.crew_actor_id) AS cache_write_tokens,
			(SELECT COALESCE(SUM(u.output_tokens),0)      FROM turn u WHERE u.actor_id = t.crew_actor_id) AS output_tokens,
			(SELECT COALESCE(SUM(u.thinking_tokens),0)    FROM turn u WHERE u.actor_id = t.crew_actor_id) AS thinking_tokens,
			(SELECT u.context_tokens_after FROM turn u WHERE u.actor_id = t.crew_actor_id
			  ORDER BY u.started_at DESC, u.ordinal DESC LIMIT 1) AS context_tokens_last,
			(SELECT u.model FROM turn u WHERE u.actor_id = t.crew_actor_id
			  ORDER BY u.started_at DESC, u.ordinal DESC LIMIT 1) AS last_model,
			(SELECT p.context_window FROM turn u JOIN pricing p ON p.model = u.model
			  WHERE u.actor_id = t.crew_actor_id
			  ORDER BY u.started_at DESC, u.ordinal DESC LIMIT 1) AS context_window,
			(SELECT CASE WHEN p.context_window > 0
			         THEN 100.0 * u.context_tokens_after / p.context_window
			         ELSE NULL END
			   FROM turn u JOIN pricing p ON p.model = u.model
			  WHERE u.actor_id = t.crew_actor_id
			  ORDER BY u.started_at DESC, u.ordinal DESC LIMIT 1) AS context_pct,
			(SELECT COALESCE(SUM(q.waited_ms),0) FROM question q WHERE q.crew_actor_id = t.crew_actor_id) AS waited_ms,
			(SELECT SUM(
				u.input_tokens       * COALESCE(p.input_per_m, 0) / 1000000.0 +
				u.cache_read_tokens  * COALESCE(p.cache_read_per_m, 0) / 1000000.0 +
				u.cache_write_tokens * COALESCE(p.cache_write_per_m, 0) / 1000000.0 +
				u.output_tokens      * COALESCE(p.output_per_m, 0) / 1000000.0)
			  FROM turn u JOIN pricing p ON p.model = u.model
			  WHERE u.actor_id = t.crew_actor_id
			    AND (p.input_per_m > 0 OR p.cache_read_per_m > 0 OR p.cache_write_per_m > 0 OR p.output_per_m > 0)
			 ) AS cost,
			(SELECT e.at FROM event e WHERE e.id = t.merged_event_id) AS merged_at
		FROM task t
		JOIN actor a ON a.id = t.crew_actor_id`,

	`DROP VIEW v_now`,

	// v_now, unchanged except for one column appended at the end:
	// context_pct, the same computation v_task_ledger now does, over
	// whichever actor's most recent turn this row is (Mate or crew alike).
	`CREATE VIEW v_now AS
		SELECT
			a.id      AS actor_id,
			a.kind    AS actor_kind,
			a.name    AS actor_name,
			a.project AS project,
			r.to_state        AS state,
			r.at              AS since,
			r.target_actor_id AS target_actor_id,
			r.detail          AS detail,
			(SELECT COALESCE(SUM(u.input_tokens + u.cache_read_tokens + u.cache_write_tokens + u.output_tokens), 0)
			   FROM turn u
			  WHERE u.actor_id = a.id
			    AND substr(u.started_at, 1, 10) = strftime('%Y-%m-%d', 'now')) AS tokens_today,
			(SELECT CASE WHEN p.context_window > 0
			         THEN 100.0 * u.context_tokens_after / p.context_window
			         ELSE NULL END
			   FROM turn u JOIN pricing p ON p.model = u.model
			  WHERE u.actor_id = a.id
			  ORDER BY u.started_at DESC, u.ordinal DESC LIMIT 1) AS context_pct
		FROM actor a
		LEFT JOIN transition r
		  ON r.id = (SELECT x.id FROM transition x WHERE x.actor_id = a.id
		              ORDER BY x.at DESC, x.id DESC LIMIT 1)`,
}

// derivedTables are every table `mate reindex` empties before rebuilding.
// `schema_version` is not one of them: the schema is not derived from the
// files, it is what the files are read into.
var derivedTables = []string{
	"telemetry_cursor",
	"transition", "usage_sample", "incident", "question", "message",
	"action", "turn", "event", "task", "session", "actor", "cursor", "pricing",
}

// Telemetry tails carry parser correlations alongside the cursor. Updating this
// row and inserting its facts in one transaction makes late results replayable.
var schema3 = []string{
	// fillTriggers joins each response to its turn event. Native evidence
	// increases event volume substantially; this must be an indexed lookup.
	`CREATE INDEX event_turn_kind ON event(turn_id, kind)`,
	`CREATE TABLE telemetry_cursor (
		source_path TEXT PRIMARY KEY,
		actor_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		byte_offset INTEGER NOT NULL DEFAULT 0,
		state_json TEXT NOT NULL DEFAULT '{}',
		source_size INTEGER NOT NULL DEFAULT 0,
		source_mtime INTEGER NOT NULL DEFAULT 0,
		observed_at TEXT NOT NULL,
		error TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX telemetry_cursor_actor ON telemetry_cursor(actor_id)`,
}
