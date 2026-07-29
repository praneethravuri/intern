package store

// All SQL lives here as untyped string constants so the query surface of the
// package can be reviewed in one place. Times are Unix milliseconds.

// qSetSchemaVersion records the current schema revision. Unlike an
// INSERT ... ON CONFLICT DO NOTHING, this must actually overwrite the value
// on a second migration, since a database can move from v1 to v2 in place.
const qSetSchemaVersion = `
INSERT INTO meta (key, value) VALUES ('schema_version', '2')
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

	// State is derived from observations, not stored on the agent row.
	qHeartbeat = `UPDATE agents SET last_seen = ? WHERE workspace = ? AND name = ?`

	agentCols = `workspace, name, harness, session_id, cwd, pid, pid_start,
                 dropped, registered_at, last_seen`

	qGetAgent = `SELECT ` + agentCols + ` FROM agents WHERE workspace = ? AND name = ?`

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

	// Subquery, not ORDER BY/LIMIT on UPDATE: modernc.org/sqlite rejects that directly.
	qDropOldest = `
UPDATE messages SET dead = 1
WHERE id IN (
    SELECT id FROM messages
    WHERE to_ws = ? AND to_name = ? AND acked_at IS NULL AND dead = 0
    ORDER BY id
    LIMIT ?
)`

	qIncrementDropped = `UPDATE agents SET dropped = dropped + ? WHERE workspace = ? AND name = ?`
)

const (
	qObserve = `
INSERT INTO observations (workspace, name, kind, detail, at)
VALUES (?, ?, ?, ?, ?)`

	// Uses idx_obs_latest for an index seek, not a scan.
	qLastObservation = `
SELECT kind, detail, at
FROM observations
WHERE workspace = ? AND name = ?
ORDER BY id DESC
LIMIT 1`

	// One row per name (the latest) for a whole workspace, avoiding N+1.
	qLastObservations = `
SELECT name, kind, detail, at
FROM (
    SELECT name, kind, detail, at,
           ROW_NUMBER() OVER (PARTITION BY name ORDER BY id DESC) AS rn
    FROM observations
    WHERE workspace = ?
)
WHERE rn = 1`

	qPendingByWorkspace = `
SELECT to_name, COUNT(*)
FROM messages
WHERE to_ws = ? AND acked_at IS NULL AND dead = 0
GROUP BY to_name`

	qSweepObservations = `DELETE FROM observations WHERE at < ?`
)
