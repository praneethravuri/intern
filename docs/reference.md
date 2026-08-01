# CLI Reference

Generated from the live command definitions -- see `cmd/tether/docsgen.go`.
Run `go test ./cmd/tether -run TestGeneratedDocsMatchCheckedIn -update` after
changing a command's flags or help text to regenerate it.

`--json` is accepted by every command except `version`, `top`, and bare `tether`.
`--as <name>` and `--workspace <name>` are accepted by `register`, `send`,
`inbox`, `wait`, and `explain`; `ls`, `top`, `doctor`, `claim`,
`release`, and `claims` accept `--workspace` but not `--as` (a claim is owned by
a process, not a registered agent name); `version`, `demo`, and bare
`tether` accept neither. Every identity-bearing command auto-starts the
daemon if it isn't running, and also registers implicitly if you never called
`register` yourself -- usually minting a name from your harness,
`<harness>-<hex4>` -- except `doctor`, which only diagnoses and never
auto-starts.

| Command | What it does |
| --- | --- |
| `tether` | Shows a quick glance at your own inbox: how many messages are pending. Auto-starts the daemon and registers implicitly, like every other command, once the workspace has somebody in it. |
| `tether start` | Run the daemon in the foreground |
| `tether register [name]` | Register this agent so others can reach it |
| `tether send <to> [body]` | Send a message to another agent |
| `tether inbox` | Show messages waiting for this agent |
| `tether wait` | Block until a message is waiting |
| `tether ls` | List registered agents and what each is doing |
| `tether top` | Watch the fleet view, refreshing on a timer |
| `tether explain [name[@workspace]]` | Explain one agent's state and pending count |
| `tether claim <key>` | Claim exclusive ownership of a key in this workspace |
| `tether release <key>` | Release a claim you hold |
| `tether claims` | List claims and who holds them |
| `tether doctor` | Check the daemon, this workspace, and every agent here |
| `tether demo` | Watch two real agents exchange a message, end to end |
| `tether hooks install` | Write the Claude Code hooks into settings.json |
| `tether version` | Print the version |

`ls`/`explain` compute a state fresh on every call, never stored, in priority
order: `gone` (pid no longer alive) -> `blocked` (parked in a live `wait`) ->
`working` (ran a command in the last 60s) -> `quiet` (ran one, just not
recently) -> `unknown` (registered, nothing observed yet). `register --doing "compiling tests, ~5min"`
sets a note that `explain` shows in place of the generic evidence string, for
anything that runs long enough to otherwise read as quiet.

A claim answers "who owns this file right now": three independent facts,
never inferred from one another -- which live process holds it (self-heals
like presence, via the same pid+start-time check), a durable lease id
(128-bit random, freshly minted on every acquisition, required to release),
and a free-text holder label (diagnostic only, shown by `tether claims`,
never checked by `release`). A claim held by a process that has since died
is reclaimed immediately by the next `claim`, without waiting for its TTL
(15m, a daemon-side default) to elapse.

## Flags

| Command | Flag | Description |
| --- | --- | --- |
| `register` | `--doing <text>` | what you're doing right now, shown by tether explain (e.g. "compiling tests, ~5min") |
| `send` | `--body-file <path|->` | read the body from this file, or from stdin when set to - |
| `send` | `--kind note|handoff|question|answer` | message kind: note, handoff, question, answer |
| `send` | `--reply-to <id>` | id of the message this replies to |
| `inbox` | `--full` | show every message body in full, without the human-view truncation (--json is always full) |
| `inbox` | `--limit <n>` | maximum messages to return (default 50, max 500) |
| `inbox` | `--peek` | show messages without clearing them |
| `inbox` | `--replay` | show messages an earlier drain already delivered |
| `wait` | `--timeout <duration>` | how long to block, as a Go duration (30s, 5m, 1h30m) |
| `ls` | `--all` | list agents in every workspace, not just this one |
| `top` | `--all` | watch agents in every workspace, not just this one |
| `top` | `--interval <duration>` | refresh interval, as a Go duration |
| `claim` | `--holder <text>` | free-text label shown by tether claims (e.g. "refactoring auth") |
| `release` | `--if-claim-id <id>` | lease id returned by tether claim (required) |
| `claims` | `--all` | list claims in every workspace, not just this one |
| `hooks install` | `--user` | target ~/.claude/settings.json instead of the project-level file |

`--as`, `--workspace`, and `--json` apply as described above and are omitted from this table.

Message kinds (`note`, `handoff`, `question`, `answer`) are advisory: the receiver
decides what to do with each, but a shared vocabulary lets an agent triage
its inbox without reading every body.

On a name conflict, `register` suggests a free alternative (`frontend` taken ->
`frontend-2`).

## Exit codes

Exit codes are part of the contract -- a script or an agent branches on
these, so they never change meaning:

| Code | Name | Meaning |
| --- | --- | --- |
| `0` | success | the command did what it was asked to do |
| `1` | general error | any failure without a more specific code |
| `3` | no daemon | the daemon could not be reached, including after an auto-start attempt |
| `4` | timeout / not found | `tether wait` returned with no mail, or `tether send` addressed an agent that does not exist ("nobody was there" either way) |
| `5` | conflict | the request collided with existing state -- a name already held by a live agent, a key already claimed by a live process, or a `release --if-claim-id` that doesn't match the claim's current lease |

## Configuration

| Variable | Effect |
| --- | --- |
| `TETHER_SOCK` | Socket path. Otherwise `$XDG_RUNTIME_DIR/tether/sock`, otherwise `~/.tether/sock`. |
| `TETHER_DB` | Database path. Otherwise `~/.tether/tether.db`. |
| `TETHER_WORKSPACE` | Overrides workspace detection entirely (otherwise the basename of the git root of the current directory). |
| `TETHER_SESSION_ID` | Overrides the session id used to authenticate "acting as `--as X`" claims. Only needed if your harness is not one `tether` recognises; most callers never set this -- an unrecognised harness still gets a stable, per-shell synthetic session id automatically. |

Outside a git repo entirely, workspace detection falls back to the current
directory's own basename rather than failing.

The socket is created mode 0600 inside a 0700 directory, so only your user
can reach the bus. See [SECURITY.md](../SECURITY.md) for the full threat
model.
