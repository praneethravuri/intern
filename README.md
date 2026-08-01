# tether

[![CI](https://img.shields.io/github/actions/workflow/status/praneethravuri/tether/go.yml?branch=main&style=flat-square&label=CI)](https://github.com/praneethravuri/tether/actions/workflows/go.yml)
[![Release](https://img.shields.io/github/v/release/praneethravuri/tether?style=flat-square)](https://github.com/praneethravuri/tether/releases/latest)
[![Platforms](https://img.shields.io/badge/platforms-macOS%20%7C%20Linux-blue?style=flat-square)](#install)
[![Go](https://img.shields.io/github/go-mod/go-version/praneethravuri/tether?style=flat-square)](go.mod)
[![License](https://img.shields.io/github/license/praneethravuri/tether?style=flat-square)](LICENSE)

Every coding agent runs in a silo by default — Claude Code can't see your Codex session, Codex can't message your Aider session — and **tether** is the local coordination layer that fixes that, letting agents register a name, send mail, claim a file before editing it, and block until a reply arrives.

These are commands *your agents* run, not you: install it once, and Claude Code, Codex, and every other harness in the repo call `tether` on their own from then on.

```sh
curl -fsSL https://praneethravuri.github.io/tether/install.sh | sh
npx skills add praneethravuri/tether
```

```sh
# Claude Code — working on the frontend
tether register frontend
tether wait --timeout 5m       # blocks until mail arrives
```

```sh
# Codex — working on the backend
tether register backend
tether send frontend "the /orders response now returns a cursor, not an offset"
```

Frontend's `wait` returns the moment that message arrives; `tether inbox` reads it. No setup step beyond the two install lines above — the daemon auto-starts on first use. First time trying tether? `tether demo` runs this exact exchange for you, in an isolated sandbox, with nothing to configure.

## Status: what runs on your machine

- **Trust boundary.** Same-uid forgery of tether's own session id is technically possible; the real security boundary is the 0700 socket directory (0600 socket inside it), not any in-protocol identity check. See [SECURITY.md](SECURITY.md).
- **What it writes to disk**, all under `~/.tether/` (paths overridable via `$TETHER_SOCK`/`$TETHER_DB`, see [docs/reference.md](docs/reference.md#configuration)): the unix socket (`sock`), the SQLite database (`tether.db`), the daemon's log (`daemon.log`), and — only if you run `tether hooks install` for Claude Code — per-hook lock/budget state under `hooks/`.
- **Retention.** Read-or-dead mail is deleted after 7 days. An agent that misses a heartbeat for 5 minutes reads as stale; 24 hours with no heartbeat and it's swept as dead. A file claim expires after 15 minutes without renewal, or immediately once its owning process is confirmed dead — whichever comes first.

## Why tether

- **Frontend/backend handoff.** One agent changes an API's response shape, sends the other a `handoff`, and blocks on `wait` until the other confirms — instead of finding out days later from a broken build.
- **Claim before you edit.** `tether claim src/orders.go` before touching a shared file, so a second agent about to edit the same path finds out immediately instead of merge-conflicting later. `tether release` when done; `tether claims` shows who holds what.
- **Code review.** A reviewer agent asks questions about a change instead of editing it directly; the author replies inline and keeps working.
- **Release coordination.** A release agent waits for every other agent's "done" before announcing, so nothing ships half-finished.
- **Watch it happen.** `tether top` is a live, refreshing fleet view — who's registered, what they're doing, how much mail is pending — and `tether demo` runs a real handoff end to end with zero setup.

Every scenario above reduces to a handful of commands — `register`, `send`, `wait`, `inbox`, `claim`, `release` — pointed at a different problem. Using Claude Code? `tether hooks install` delivers mail through a hook instead of a polling `wait` loop.

## Comparison

| Approach | What it costs | What it's missing |
| --- | --- | --- |
| **tether** | One binary, one line in `AGENTS.md`/`CLAUDE.md`, no config, no restart | Local-only — no distributed or cross-machine mode |
| **Shared prompt / one big context window** | Nothing to install | No isolation, no parallelism — one window bloats with every agent's work |
| **File-based mailbox** (`inbox/*.json`) | Zero dependencies, git-versioned | No blocking primitive — an agent has to poll, and concurrent writers need their own locking |
| **MCP server** | Four tool schemas at ~700-1,000 tokens each, ~3-4k tokens/session whether used or not, plus a per-harness `.mcp.json` entry | Still needs a shared broker underneath for agent-to-agent delivery — MCP replaces the front door, not the daemon |

tether is CLI-only on principle, not just preference: in an external benchmark of agentic tool use, a plain CLI (`gh`) scored 86%, while a version applying stricter output-design discipline (`gh-axi`) scored 100% — at a third of the cost. Both were CLIs; the 14-point gap was entirely design discipline. Being a CLI instead of an MCP server is necessary, not sufficient, but it's the foundation everything else here is built on.

## Other ways to install

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

Pin a version with `TETHER_VERSION=v0.2.0 curl ... | sh`. To uninstall, remove the binary and `~/.tether`.

## Docs

- **[docs/reference.md](docs/reference.md)** — the full CLI Reference (every command), Flags, exit codes, and configuration environment variables. Generated from the live command definitions, not hand-typed, so it can't silently drift.
- **[docs/harness-verification.md](docs/harness-verification.md)** — what's actually been verified per harness, and what hasn't (hooks are Claude-Code-only and honestly labeled as such).
- **[docs/development.md](docs/development.md)** — build, test, lint, and how to regenerate `docs/reference.md`.
- **[SECURITY.md](SECURITY.md)** — the full threat model.

## Troubleshooting

**`tether: no daemon` (exit 3).** The daemon isn't running and auto-start failed, or you ran `doctor`, which never auto-starts. Run `tether doctor` — it reports the socket path, whether the daemon answers, and the daemon log path (`~/.tether/daemon.log`) so you can see why it didn't come up.

**`wait`/`send` times out (exit 4).** Either nobody answered in time, or `send` addressed an agent that isn't registered. Run `tether ls` to see who's actually here, and `tether explain <name>` to see one agent's computed state and the evidence behind it.

**Name or claim conflict (exit 5).** Someone else already holds that name as a live agent, or that key as a live claim. `register`'s error message suggests a free alternative (`frontend` → `frontend-2`); `tether claims` shows who holds a contested key.

**Database growing large.** `tether doctor` reports the database's file path and size. Read-or-dead mail is deleted outright after 7 days — nothing to clean up by hand.

Full exit code table: [docs/reference.md](docs/reference.md#exit-codes).

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

## License

MIT — see [LICENSE](LICENSE).
