# tether

[![CI](https://img.shields.io/github/actions/workflow/status/praneethravuri/tether/go.yml?branch=main&style=flat-square&label=CI)](https://github.com/praneethravuri/tether/actions/workflows/go.yml)
[![Release](https://img.shields.io/github/v/release/praneethravuri/tether?style=flat-square)](https://github.com/praneethravuri/tether/releases/latest)
[![Stars](https://img.shields.io/github/stars/praneethravuri/tether?style=flat-square)](https://github.com/praneethravuri/tether/stargazers)
[![Last commit](https://img.shields.io/github/last-commit/praneethravuri/tether?style=flat-square)](https://github.com/praneethravuri/tether/commits/main)
[![Platforms](https://img.shields.io/badge/platforms-macOS%20%7C%20Linux-blue?style=flat-square)](#install)
[![skills.sh](https://img.shields.io/badge/skills.sh-install-4c1?style=flat-square)](#skill)
[![Go](https://img.shields.io/github/go-mod/go-version/praneethravuri/tether?style=flat-square)](go.mod)
[![License](https://img.shields.io/github/license/praneethravuri/tether?style=flat-square)](LICENSE)

Every coding agent runs in a silo by default: Claude Code can't see your Codex session, Codex can't message your Aider session. **tether** is a local coordination layer that fixes that — one binary that's both a background daemon holding a shared SQLite mailbox, and a CLI any agent in any harness calls to register a name, send mail, and block until a reply arrives. No shared prompt, no plugin, no MCP server.

Nothing wraps how you launch your agents — keep running `claude`, `codex`, `aider`, and everything else exactly as you do now; tether is just a command they can call.

## Table of Contents

- [Why Tether](#why-tether)
- [Comparison](#comparison)
- [Install](#install)
  - [CLI](#cli)
  - [Skill](#skill)
- [Quickstart](#quickstart)
- [CLI Reference](#cli-reference)
- [Flags](#flags)
- [Exit codes](#exit-codes)
- [Troubleshooting](#troubleshooting)
- [How it works](#how-it-works)
- [Configuration](#configuration)
- [Development](#development)
- [License](#license)

## Why Tether

- **Frontend/backend handoff.** One agent changes an API's response shape, sends the other a `handoff`, and blocks on `wait` until the other confirms — instead of finding out days later from a broken build.
- **Code review.** A reviewer agent asks questions about a change instead of editing it directly; the author replies inline and keeps working.
- **Docs that don't drift.** A docs agent wakes on a `handoff` the moment an implementation change lands, instead of someone remembering to update the docs later.
- **Release coordination.** A release agent waits for every other agent's "done" before announcing, so nothing ships half-finished.

Every scenario above is the same four commands — `register`, `send`, `wait`, `inbox` — pointed at a different problem.

## Comparison

| Approach | What it costs | What it's missing |
| --- | --- | --- |
| **tether** | One binary, one line in `AGENTS.md`/`CLAUDE.md`, no config, no restart | Local-only — no distributed or cross-machine mode |
| **Shared prompt / one big context window** | Nothing to install | No isolation, no parallelism — one window bloats with every agent's work |
| **File-based mailbox** (`inbox/*.json`) | Zero dependencies, git-versioned | No blocking primitive — an agent has to poll, and concurrent writers need their own locking |
| **MCP server** | Four tool schemas at ~700-1,000 tokens each, ~3-4k tokens/session whether used or not, plus a per-harness `.mcp.json` entry | Still needs a shared broker underneath for agent-to-agent delivery — MCP replaces the front door, not the daemon |

tether is CLI-only on principle, not just preference: in an external benchmark of agentic tool use, a plain CLI (`gh`) scored 86%, while a version applying stricter output-design discipline (`gh-axi`) scored 100% — at a third of the cost. Both were CLIs; the 14-point gap was entirely design discipline. Being a CLI instead of an MCP server is necessary, not sufficient, but it's the foundation everything else here is built on.

## Install

### CLI

```sh
curl -fsSL https://praneethravuri.github.io/tether/install.sh | sh
```

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

</details>

### Skill

```sh
npx skills add praneethravuri/tether
```

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
tether ls                    # bare `tether` only glances at your own inbox — use `ls` for the fleet
tether send frontend "the /orders response now returns a cursor, not an offset"
```

Frontend's `wait` returns as soon as the message arrives; it reads it with `tether inbox`, which prints it and clears it in the same step — there is no separate acknowledge command.

Both agents resolved the same workspace — the basename of the git root — so the bare name `frontend` was enough. Across repos, address the full `name@workspace`.

## CLI Reference

`--json` is accepted by every command except `version`, `top`, and bare `tether`. `--as <name>` and `--workspace <name>` are accepted by `register`, `send`, `inbox`, `wait`, and `explain`; `ls`, `top`, and `doctor` accept `--workspace` but not `--as`; `version`, `demo`, and bare `tether` accept neither. Every identity-bearing command auto-starts the daemon if it isn't running, and also registers implicitly if you never called `register` yourself — usually minting a name from your harness, `<harness>-<hex4>` — except `doctor`, which only diagnoses and never auto-starts.

| Command | What it does |
| --- | --- |
| `tether` | Shows a quick glance at your own inbox: how many messages are pending. Auto-starts the daemon and registers implicitly, like every other command, once the workspace has somebody in it. |
| `tether start` | Starts the daemon in the foreground: blocks, logs to the terminal, stops on Ctrl-C. |
| `tether register <name>` | Claims a name in this workspace so others can address you. Re-running it from the same session renames in place. |
| `tether send <to> [body]` | Sends a message. `<to>` is `name`, `name@workspace`, or `*`/`all` to broadcast to everyone else in the workspace. |
| `tether inbox` | Shows messages waiting for you and acknowledges them in the same call (draining). |
| `tether wait` | Blocks until mail is waiting. Exits 0 as soon as there is something to read, 4 on timeout. |
| `tether ls` | Lists registered agents: address, harness, computed state, pending count, last seen. |
| `tether top` | Same fleet view as `ls`, refreshing on a timer until Ctrl-C — `htop` for agents. |
| `tether explain [name[@workspace]]` (alias `status`) | Explains one agent's computed state, the evidence behind it, and its pending mail. Defaults to you. |
| `tether doctor` | Diagnoses the daemon, resolved workspace, detected harness, database health, and every agent. Never auto-starts. |
| `tether demo` | Spins up an isolated daemon and runs a real handoff between two scripted agents, so you can watch tether work without any setup. |
| `tether version` | Prints the version. |

`ls`/`explain` compute a state fresh on every call, never stored, in priority order: `gone` (pid no longer alive) → `blocked` (parked in a live `wait`) → `working` (ran a command in the last 60s) → `quiet` (ran one, just not recently) → `unknown` (registered, nothing observed yet). `register --doing "compiling tests, ~5min"` sets a note that `explain` shows in place of the generic evidence string, for anything that runs long enough to otherwise read as quiet.

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
| `top` | `--all` | watch agents in every workspace, not just this one |
| `top` | `--interval <duration>` | refresh interval (default `2s`) |
| `register` | `--doing <text>` | what you're doing right now, shown by `explain` |

`--as`, `--workspace`, and `--json` apply as described under [CLI Reference](#cli-reference) and are omitted from this table.

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

## Troubleshooting

**`tether: no daemon` (exit 3).** The daemon isn't running and auto-start failed, or you ran `doctor`, which never auto-starts. Run `tether doctor` — it reports the socket path, whether the daemon answers, and the daemon log path (`~/.tether/daemon.log`) so you can see why it didn't come up.

**`wait`/`send` times out (exit 4).** Either nobody answered in time, or `send` addressed an agent that isn't registered. Run `tether ls` to see who's actually here, and `tether explain <name>` to see one agent's computed state and the evidence behind it.

**Name conflict (exit 5).** Someone else already holds that name as a live agent. `register`'s error message suggests a free alternative (`frontend` → `frontend-2`).

**Database growing large.** `tether doctor` reports the database's file path and size. Read-or-dead mail is deleted outright after 7 days — nothing to clean up by hand.

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

## Configuration

| Variable | Effect |
| --- | --- |
| `TETHER_SOCK` | Socket path. Otherwise `$XDG_RUNTIME_DIR/tether/sock`, otherwise `~/.tether/sock`. |
| `TETHER_DB` | Database path. Otherwise `~/.tether/tether.db`. |
| `TETHER_WORKSPACE` | Overrides workspace detection entirely (otherwise the basename of the git root of the current directory). |
| `TETHER_SESSION_ID` | Overrides the session id used to authenticate "acting as `--as X`" claims. Only needed if your harness is not one `tether` recognises; most callers never set this — an unrecognised harness still gets a stable, per-shell synthetic session id automatically. |

Outside a git repo entirely, workspace detection falls back to the current directory's own basename rather than failing.

The socket is created mode 0600 inside a 0700 directory, so only your user can reach the bus. See [SECURITY.md](SECURITY.md) for the full threat model.

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
