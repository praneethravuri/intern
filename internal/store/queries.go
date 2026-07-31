package store

// All SQL lives here as untyped string constants so the query surface of the
// package can be reviewed in one place. Times are Unix milliseconds.

// qSetSchemaVersion records the current schema revision. Unlike an
// INSERT ... ON CONFLICT DO NOTHING, this must actually overwrite the value
// on a second migration, since a database can move from v1 to v3 in place.
const qSetSchemaVersion = `
INSERT INTO meta (key, value) VALUES ('schema_version', '3')
ON CONFLICT(key) DO UPDATE SET value = excluded.value`

const (
	// qRegister is a guarded upsert; RowsAffected()==0 means the name is taken.
	qRegister = `
INSERT INTO agents (
    workspace, name, harness, session_id, cwd, pid, pid_start,
    registered_at, last_seen
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(workspace, name) DO UPDATE SET
    harness       = excluded.harness,
    session_id    = excluded.session_id,
    cwd           = excluded.cwd,
    pid           = excluded.pid,
    pid_start     = excluded.pid_start,
    registered_at = CASE
                        WHEN agents.session_id = excluded.session_id
                        THEN agents.registered_at
                        ELSE excluded.registered_at
                    END,
    last_seen     = excluded.last_seen
WHERE agents.last_seen < ?
   OR (excluded.session_id <> '' AND agents.session_id = excluded.session_id)`

	// last_note only overwrites when note is non-empty, so an implicit
	// re-register (send/inbox/wait all touch this on every call) can't
	// silently clear a note `register --doing` just set.
	qHeartbeat = `
UPDATE agents SET last_seen = ?, last_kind = ?,
    last_note = CASE WHEN ? = '' THEN last_note ELSE ? END
WHERE workspace = ? AND name = ?`

	agentCols = `workspace, name, harness, session_id, cwd, pid, pid_start,
                 dropped, registered_at, last_seen, last_kind, last_note`

	qGetAgent = `SELECT ` + agentCols + ` FROM agents WHERE workspace = ? AND name = ?`

	// qFindNameBySession resolves a session to whatever name it already
	// holds, so an empty-Name register can refresh it instead of minting a
	// second identity for the same session.
	qFindNameBySession = `
SELECT name FROM agents
WHERE workspace = ? AND session_id = ?
ORDER BY registered_at DESC LIMIT 1`

	// qRenameAgent changes a session's own row to a new name and refreshes
	// its other fields, unless a different session already holds that name
	// (RowsAffected()==0 means the target name is taken).
	qRenameAgent = `
UPDATE agents SET
    name = ?, harness = ?, cwd = ?, pid = ?, pid_start = ?, last_seen = ?
WHERE workspace = ? AND session_id = ?
  AND NOT EXISTS (
    SELECT 1 FROM agents AS other
    WHERE other.workspace = ? AND other.name = ? AND other.session_id <> ?
  )`

	// qRenameMessages moves a renamed agent's pending mail along with it, so
	// no message is left addressed to a name nobody holds any more.
	qRenameMessages = `UPDATE messages SET to_name = ? WHERE to_ws = ? AND to_name = ?`

	// Empty workspace means all workspaces; cutoff 0 disables the staleness filter.
	qListAgents = `
SELECT ` + agentCols + `
FROM agents
WHERE (? = '' OR workspace = ?)
  AND last_seen >= ?
ORDER BY workspace, name`

	qAgentExists = `SELECT 1 FROM agents WHERE workspace = ? AND name = ?`

	// Read then reset in the same transaction as Drain.
	qGetDropped   = `SELECT dropped FROM agents WHERE workspace = ? AND name = ?`
	qResetDropped = `UPDATE agents SET dropped = 0 WHERE workspace = ? AND name = ?`
)

const (
	msgCols = `id, thread_id, reply_to, from_name, from_ws, to_name, to_ws,
               kind, body, created_at, delivered_at, acked_at`

	qThreadOf = `SELECT thread_id FROM messages WHERE id = ?`

	qInsertMessage = `
INSERT INTO messages (
    id, thread_id, reply_to, from_name, from_ws, to_name, to_ws,
    kind, body, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// Reads never delete; ordered by ULID so delivery order matches send order.
	qInbox = `
SELECT ` + msgCols + `
FROM messages
WHERE to_ws = ? AND to_name = ? AND acked_at IS NULL AND dead = 0
ORDER BY id
LIMIT ?`

	// Fires only on first delivery; redelivery doesn't overwrite the timestamp.
	qStampDelivered = `UPDATE messages SET delivered_at = ? WHERE id = ? AND delivered_at IS NULL`

	// qReplay returns acked history, newest first; callers reverse it.
	qReplay = `
SELECT ` + msgCols + `
FROM messages
WHERE to_ws = ? AND to_name = ? AND acked_at IS NOT NULL
ORDER BY id DESC
LIMIT ?`

	// Completed with a generated placeholder list; scoped to the recipient.
	qAckPrefix = `
UPDATE messages SET acked_at = ?
WHERE to_ws = ? AND to_name = ? AND acked_at IS NULL AND id IN `

	qPendingCount = `
SELECT COUNT(*) FROM messages
WHERE to_ws = ? AND to_name = ? AND acked_at IS NULL AND dead = 0`

	qSweepDead = `
UPDATE messages SET dead = 1
WHERE dead = 0 AND acked_at IS NULL AND created_at < ?`

	// qPurgeMessages deletes read (acked) or dead mail past the retention
	// window -- this is what keeps the database from growing forever; unlike
	// qSweepDead, it removes rows instead of only flagging them.
	qPurgeMessages = `
DELETE FROM messages
WHERE (dead = 1 OR acked_at IS NOT NULL) AND created_at < ?`

	// Subquery, not ORDER BY/LIMIT on UPDATE: modernc.org/sqlite rejects that directly.
	// Notes are evicted before anything else, regardless of age. keepID is
	// excluded so Send can never report success for what it just marked dead.
	qDropOldest = `
UPDATE messages SET dead = 1
WHERE id IN (
    SELECT id FROM messages
    WHERE to_ws = ? AND to_name = ? AND acked_at IS NULL AND dead = 0 AND id != ?
    ORDER BY kind = 'note' DESC, id
    LIMIT ?
)`

	qIncrementDropped = `UPDATE agents SET dropped = dropped + ? WHERE workspace = ? AND name = ?`

	// One row per pending recipient in ws, avoiding N+1 in a fleet listing.
	qPendingByWorkspace = `
SELECT to_name, COUNT(*)
FROM messages
WHERE to_ws = ? AND acked_at IS NULL AND dead = 0
GROUP BY to_name`
)
