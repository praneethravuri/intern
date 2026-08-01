# intern

[![CI](https://img.shields.io/github/actions/workflow/status/praneethravuri/intern/go.yml?branch=main&style=flat-square&label=CI)](https://github.com/praneethravuri/intern/actions/workflows/go.yml)
[![Release](https://img.shields.io/github/v/release/praneethravuri/intern?style=flat-square)](https://github.com/praneethravuri/intern/releases/latest)
[![Skills](https://skills.sh/b/praneethravuri/intern)](https://skills.sh/praneethravuri/intern/intern)
[![Platforms](https://img.shields.io/badge/platforms-macOS%20%7C%20Linux-blue?style=flat-square)](#install)

Intern is a tiny local message bus for coding agents. It carries messages between
team leads, waits for replies, and keeps agents from editing the same file at
the same time.

It is one CLI, one background daemon, and no server to configure.

## Install

```sh
curl -fsSL https://praneethravuri.github.io/intern/install.sh | sh
npx skills add praneethravuri/intern --skill intern
```

Or install with Go:

```sh
go install github.com/praneethravuri/intern/cmd/intern@latest
```

## Two agents, one handoff

Team lead A registers and waits:

```sh
intern register frontend
intern wait --timeout 5m
```

Team lead B sends the update:

```sh
intern register backend
intern send frontend "The /orders response now returns a cursor."
```

The daemon starts on first use. Try the complete exchange with:

```sh
intern demo
```

## Core commands

| Command | Use it to |
| --- | --- |
| `intern register <name>` | join the current workspace |
| `intern ls` | see registered agents and their state |
| `intern send <name> <message>` | send a note, question, or handoff |
| `intern wait` | block until mail arrives |
| `intern inbox` | read pending mail |
| `intern claim <path>` | reserve a file before editing it |
| `intern release <path>` | release a file claim |
| `intern hooks install` | deliver mail through Claude Code hooks |

Use `intern <command> --help` for flags and JSON output.

## What it does

- Keeps state in a per-user SQLite database under `~/.intern/`.
- Communicates over a local Unix socket; no network service is required.
- Scopes names and messages to the current workspace.
- Expires stale agents and file claims automatically.
- Works from any coding-agent harness that can run a shell command.

For a different location, set `INTERN_SOCK`, `INTERN_DB`, or
`INTERN_WORKSPACE`. Set `INTERN_VERSION=v0.2.0` when pinning the installer.

## The intern's clipboard

```text
agent A ── send ──┐
                  ├─ intern daemon ── SQLite
agent B ── wait ──┘       │
                      Unix socket
```

Each command is a short-lived client. The daemon owns durable mail, presence,
and claims, so a restarted agent does not lose its inbox.

## More

- [CLI reference](docs/reference.md)
- [Harness verification](docs/harness-verification.md)
- [Development guide](docs/development.md)
- [Security model](SECURITY.md)
- [Intern skill](skills/intern/SKILL.md)

## Development

```sh
git clone https://github.com/praneethravuri/intern
cd intern
make build
make test
```

MIT — see [LICENSE](LICENSE).
