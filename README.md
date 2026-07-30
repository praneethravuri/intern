# tether

[![CI](https://img.shields.io/github/actions/workflow/status/praneethravuri/tether/go.yml?branch=main&style=flat-square&label=CI)](https://github.com/praneethravuri/tether/actions/workflows/go.yml)
[![Release](https://img.shields.io/github/v/release/praneethravuri/tether?style=flat-square)](https://github.com/praneethravuri/tether/releases/latest)
[![Stars](https://img.shields.io/github/stars/praneethravuri/tether?style=flat-square)](https://github.com/praneethravuri/tether/stargazers)
[![Last commit](https://img.shields.io/github/last-commit/praneethravuri/tether?style=flat-square)](https://github.com/praneethravuri/tether/commits/main)
[![Platforms](https://img.shields.io/badge/platforms-macOS%20%7C%20Linux-blue?style=flat-square)](#install)
[![skills.sh](https://img.shields.io/badge/skills.sh-install-4c1?style=flat-square)](#skill)
[![Go](https://img.shields.io/github/go-mod/go-version/praneethravuri/tether?style=flat-square)](go.mod)
[![License](https://img.shields.io/github/license/praneethravuri/tether?style=flat-square)](LICENSE)

Every coding agent runs in a silo — Claude Code can't see your Codex session, Codex can't message your Aider session, and each harness keeps its own private mailbox that none of the others can read. **tether** is the local message bus that fixes that: one binary that's both a background daemon holding a shared SQLite mailbox, and the CLI any agent in any harness calls from the shell to register a name, send mail, block until mail arrives, and read it.

Nothing wraps how you launch your agents. You keep running `claude`, `codex`, `aider`, and everything else exactly as you do now; tether is just a command they can call.

> [!TIP]
> No setup step. `tether register frontend` and you're addressable — the daemon starts itself the first time anything talks to it.

## Table of Contents

- [Install](#install)
  - [CLI](#cli)
  - [Skill](#skill)
- [Quickstart](#quickstart)
- [CLI Reference](#cli-reference)
- [Flags](#flags)
- [Exit codes](#exit-codes)
- [How it works](#how-it-works)
- [Behavior details](#behavior-details)
- [Configuration](#configuration)
- [Development](#development)
- [License](#license)

## Install

### CLI

```sh
curl -fsSL https://praneethravuri.github.io/tether/install.sh | sh
```

Installs `tether` to `~/.local/bin` (override with `TETHER_INSTALL_DIR`), verifying the download against the release's checksums. Never uses sudo. macOS and Linux only.

Pin a version with `TETHER_VERSION=v0.2.0 curl ... | sh`. To uninstall, remove the binary and `~/.tether`.

<details>
<summary>Other ways to install</summary>

```sh
# Go toolchain required
go install github.com/praneethravuri/tether/cmd/tether@latest

# From source
git clone https://github.com/praneethravuri/tether
cd tether
make build          # produces ./tether, version stamped from git
./tether version

# Prebuilt archive
# download tether_<os>_<arch>.tar.gz from
# https://github.com/praneethravuri/tether/releases/latest
```

With Docker. The image entrypoint is `tether`, which with no arguments starts the daemon in the foreground, so the socket and database need to live on a mounted volume for anything outside the container to reach them:

```sh
docker build -t tether .
docker run --rm -v "$HOME/.tether:/home/nonroot/.tether" tether
```

</details>

### Skill

```sh
npx skills add praneethravuri/tether
```

Installs `skills/tether/SKILL.md` into your project with everything an agent needs to know to use `tether` — naming conventions, message kinds, exit codes. Works with Claude Code, Cursor, Codex, Gemini CLI, and roughly 70 other harnesses that installer supports. This is the standing-instructions path: no `CLAUDE.md`/`AGENTS.md` paste-in required.

## Quickstart

These are agent commands, not yours — once the [skill](#skill) is installed, your coding agents run them on their own. Say you have Claude Code and Codex open in the same repo, each working on a different part of the same feature:

```sh
# Claude Code, working on the frontend
tether register frontend
tether wait --timeout 5m     # blocks until mail arrives; exits 4 on timeout
```

```sh
# Codex, working on the backend
tether register backend
tether ls                    # bare `tether` starts the daemon instead — use `ls` for the fleet
tether send frontend "the /orders response now returns a cursor, not an offset"
```

Frontend's `wait` returns as soon as the message arrives; it reads it with `tether inbox`, which prints it and clears it in the same step — there is no separate acknowledge command.

Both agents resolved the same workspace — the basename of the git root — so the bare name `frontend` was enough. Across repos, address the full `name@workspace`.

There is no daemon to start by hand and nothing to export. If the socket is dead, the first command run spawns a detached daemon automatically (logging to `~/.tether/daemon.log`, truncated at 1 MB) and retries once. `register` itself is optional — every command derives a name like `claude-code-3f1a` from the harness and session if nothing was given it with `--as` or registered earlier in that session. `register <name>` exists so an agent (or you, testing by hand) can pick a name deliberately and see it confirmed.

## CLI Reference

Every command accepts `--json` for machine-readable output, and `--as <name>` / `--workspace <name>` to act as a name or workspace other than the ones auto-detected for this shell session. Every command except `doctor` auto-starts the daemon when the socket is dead and retries once — `doctor` only diagnoses, so it stays trustworthy as a health check.

| Command | What it does |
| --- | --- |
| `tether` | Starts the daemon in the foreground: blocks, logs to the terminal, stops on Ctrl-C. |
| `tether register <name>` | Claims a name in this workspace so others can address you. Re-running it from the same session renames in place. |
| `tether send <to> [body]` | Sends a message. `<to>` is `name`, `name@workspace`, or `*`/`all` to broadcast to everyone else in the workspace. |
| `tether inbox` | Shows messages waiting for you and acknowledges them in the same call (draining). |
| `tether wait` | Blocks until mail is waiting. Exits 0 as soon as there is something to read, 4 on timeout. |
| `tether ls` | Lists registered agents: address, harness, computed state, pending count, last seen. |
| `tether explain [name[@workspace]]` (alias `status`) | Explains one agent's computed state, the evidence behind it, and its pending mail. Defaults to you. |
| `tether doctor` | Diagnoses the daemon, resolved workspace, detected harness, database health, and every agent. Never auto-starts. |
| `tether version` | Prints the version. |

`tether who` is gone, including as an alias — `tether ls` is the only listing command now. There's also no `ack`, `heartbeat`, or `unregister` command: reading an inbox is how mail is acknowledged (see [Inbox contract](#inbox-contract)); presence is refreshed automatically by every command you run; a dead process is detected, not declared.

## Flags

| Command | Flag | Description |
| --- | --- | --- |
| `send` | `--kind note\|handoff\|question\|answer` | Message kind, default `note`. Advisory only. |
| `send` | `--reply-to <id>` | id of the message this replies to |
| `send` | `--body-file <path\|->` | read the body from this file, or stdin with `-` |
| `inbox` | `--peek` | show what's pending without clearing anything |
| `inbox` | `--replay` | show messages an earlier drain already delivered, up to 7 days back |
| `inbox` | `--limit <n>` | maximum messages to return (default 50, max 500) |
| `inbox` | `--full` | show every message body in full, skipping truncation |
| `wait` | `--timeout <duration>` | how long to block (default `60s`, max `24h`) |
| `ls` | `--all` | list agents in every workspace, not just this one |

`--as`, `--workspace`, and `--json` apply to every command as described under [CLI Reference](#cli-reference) and are omitted from this table.

Message kinds (`note`, `handoff`, `question`, `answer`) are advisory: the receiver decides what to do with each, but a shared vocabulary lets an agent triage its inbox without reading every body.

On a name conflict, `register` suggests a free alternative (`frontend` taken → `frontend-2`).

## Exit codes

Exit codes are part of the contract — a script or an agent branches on these, so they never change meaning:

| Code | Name | Meaning |
| --- | --- | --- |
| `0` | success | the command did what it was asked to do |
| `1` | general error | any failure without a more specific code |
| `3` | no daemon | the daemon could not be reached, including after an auto-start attempt |
| `4` | timeout / not found | `tether wait` returned with no mail, or `tether send` addressed an agent that does not exist ("nobody was there" either way) |
| `5` | conflict | the request collided with existing state — almost always a name already held by a live agent |

## How it works

```
  tether send backend                 tether wait / inbox
          │                                    │
          └──────────────┬─────────────────────┘
                         ▼
           unix socket (0600, in a 0700 dir)
           $TETHER_SOCK
             else $XDG_RUNTIME_DIR/tether/sock
             else ~/.tether/sock
                         │
              newline-delimited JSON
                         ▼
                tether (daemon mode)
                         │
                         ▼
                 ~/.tether/tether.db
                 (SQLite, WAL mode)
```

The CLI is stateless: it opens the socket, writes one JSON request, reads one JSON response, and exits. All state lives in the daemon's SQLite database, so messages survive a daemon restart and a crashed agent loses nothing.

**Package layout**, for anyone reading the source:

| Package | Role |
| --- | --- |
| `cmd/tether` | the CLI: one `cmd_*.go` per subcommand, identity resolution, the socket client, auto-start, and the shared human-output helpers |
| `internal/daemon` | the daemon: request dispatch, the wait registry, computed agent state, the startup lock, the background sweep |
| `pkg/protocol` | the wire format (newline-delimited JSON request/response) and socket transport, shared by both sides |
| `internal/store` | SQLite persistence: agents, messages, observations |
| `internal/id` | monotonic ULID generation, used for both message ids and request ids |
| `internal/wsname` | resolves a working directory to a workspace name from the git root |

**Why a daemon at all.** The obvious alternative — every agent opening the same SQLite file directly — puts several processes on one file with no coordination beyond file locks: `SQLITE_BUSY` storms, and a corrupt database if a process dies mid-write. Routing every write through one process removes that failure mode structurally rather than mitigating it: the write pool is pinned to a single connection, so writes queue instead of colliding, while a separate read pool means a long read never blocks a write.

**`wait` is a long poll, not a loop.** `tether wait` parks on the daemon, and every `send` wakes the recipient's parked call directly — no interval to tune, no missed-then-caught-up delay. It needs zero integration from the harness, so it works even on harnesses with no plugin surface at all. What it doesn't do (yet) is interrupt a harness that isn't already in `wait` — an agent still has to poll `inbox` or block on `wait` to notice mail; `tether doctor` says so plainly.

**No runtime dependencies.** The binary is static: `CGO_ENABLED=0` with a pure-Go SQLite driver (`modernc.org/sqlite`). No C toolchain to build with, no system SQLite to install, nothing needed at runtime but the binary.

## Behavior details

### Passing bodies safely

A body given as a positional argument goes through the shell first. Whenever it contains quotes, backticks, newlines or `$`, pass it on stdin instead — what's read is sent byte for byte, with no trimming or interpretation:

```sh
tether send reviewer --kind handoff --body-file - <<'EOF'
Refactored `parseConfig` — it now returns (Config, error).
Callers in cmd/ still expect the old signature.
EOF
```

`--body-file <path>` reads a file; `--body-file -` reads stdin.

### Broadcast

`tether send '*' "..."` or `tether send all "..."` delivers to every other agent currently registered in the workspace — never back to the sender, even if the sender's own name happens to match. Quote `*` so the shell doesn't glob-expand it. The result reports how many agents actually received it (`SendResult.Delivered`/`Recipients` in `--json`); zero recipients is a normal, unremarkable outcome, not an error.

### Inbox contract

- **Default (`tether inbox`)**: drains — shows and acknowledges in the same call, so there's no window where a message was shown but not yet marked handled.
- **`--peek`**: shows what's pending without clearing anything.
- **`--replay`**: shows messages an earlier drain already delivered, going back up to 7 days — the "what did I already handle?" view. Older history is gone, not hidden: it's deleted in the retention sweep described in [Bounds](#bounds).
- **`--full`**: shows every message body in full. Without it, a body over 2000 characters is truncated in the human-readable view with a `... (N bytes total, --full for all)` hint. `--json` is never truncated, with or without `--full`.

Reading is destructive only through a drain: an agent that reads its mail and then crashes before acting on it doesn't lose the message on restart, because the ack and the read happen together, in one transaction, or not at all.

### Bounds

- A single message body is capped at 64 KiB (`ErrBodyTooLarge` if exceeded).
- One agent's pending mail is capped at 500 messages. Past that, the **oldest** message is dropped first — a silent agent loses its stalest context, never the message that just arrived.
- Every drop increments a per-agent dropped counter, surfaced by `tether ls` (the `PENDING` column, as `N (+M dropped)`), `tether explain`, and `tether inbox`'s stderr warning. Degradation is visible, never silent, and the counter resets to zero the next time that agent actually drains its inbox.
- Unacked mail nobody ever comes back for is swept and marked dead after 24 hours instead of kept forever. Read-or-dead mail older than **7 days** is then deleted outright in the same background sweep, so the database doesn't grow without bound. No `VACUUM` runs; SQLite reuses the freed space, so the file plateaus rather than shrinks.
- `tether doctor` reports the database's file path, its size on disk, row counts for messages, agents, and observations, and the daemon log path — so "is my DB getting too big" has a direct answer.

### Agent state

`tether ls` and `tether explain` compute a state for every agent fresh on each call — nothing is stored as truth. In priority order:

1. **`gone`** — the registered process is no longer alive.
2. **`blocked`** — the agent is parked in a live `tether wait` call right now.
3. **`working`** — the agent ran some `tether` command within the last 60 seconds.
4. **`idle`** — the agent has run a `tether` command before, just not recently.
5. **`unknown`** — the agent is registered but nothing has been observed about it since.

`tether explain` shows the evidence behind the state (`source`, `seen`, `detail`) as well as the state itself, so you can decide whether to trust it before sending someone work.

### Output contract

Every command's real output — the summary line, table rows, message bodies, `Next:` suggestions, and the entirety of `--json` — goes to **stdout**. Warnings secondary to what you asked for — a dropped-mail count, an unrecognised harness, doctor's other diagnostic notes — go to **stderr**, so scripting against stdout never has to filter noise out of it. `tether: <message>` on a failure is also stderr. `--json` is the machine channel: pass it whenever another program, not a human, has to parse the result.

## Configuration

| Variable | Effect |
| --- | --- |
| `TETHER_SOCK` | Socket path. Otherwise `$XDG_RUNTIME_DIR/tether/sock`, otherwise `~/.tether/sock`. |
| `TETHER_DB` | Database path. Otherwise `~/.tether/tether.db`. |
| `TETHER_WORKSPACE` | Overrides workspace detection entirely (otherwise the basename of the git root of the current directory). |
| `TETHER_SESSION_ID` | Overrides the session id used to authenticate "acting as `--as X`" claims. Only needed if your harness is not one `tether` recognises; most callers never set this — an unrecognised harness still gets a stable, per-shell synthetic session id automatically. |

The socket is created mode 0600 inside a 0700 directory, so only your user can reach the bus.

## Development

```sh
make build        # the binary, version stamped from git describe
make test         # go test -race -count=1 ./...
make test-short   # skip the slow tests
make cover        # coverage profile plus a total
make lint         # golangci-lint, using .golangci.yml
make fmt          # gofmt -w
make cross        # CGO_ENABLED=0 builds for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
make help         # list every target
```

CI runs the build, `go vet`, a gofmt check and the race-enabled test suite on Linux and macOS, plus golangci-lint and a cross-compilation matrix, on every push and pull request.

## License

MIT — see [LICENSE](LICENSE).
