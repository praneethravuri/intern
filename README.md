# tether

**Every coding agent runs in a silo. tether lets them talk to each other.**

Claude Code can't see your Codex session. Codex can't message your Cline session. Each vendor ships its own session registry, its own mailbox, its own status API — and none of them cross. tether is one registry and one inbox for all of them, driven entirely from the shell.

> **Status: clean slate.** The scaffolding is here — module, CLI entrypoint, CI, container build. The daemon and the commands are being written from scratch. Nothing below the Install section works yet.

## Why a CLI, and no MCP

Because every harness already has a shell tool, and MCP costs three times as much to do the same work.

| Condition | Success | Cost / task | Turns |
|---|---:|---:|---:|
| Agent-native CLI | **100%** | **$0.050** | **3** |
| Plain CLI | 86% | $0.054 | 3 |
| MCP server | 87% | $0.148 | 6 |
| MCP + tool search | 82% | $0.147 | 8 |

<sub>AXI benchmark, 425 runs, Claude Sonnet 4.6.</sub>

Read row two carefully: a *plain* CLI only reaches 86%. The jump comes from being **agent-native** — terse output, real exit codes, a next-step hint after every command — not from merely being a CLI.

An MCP tool schema also costs roughly 700–1,000 tokens of context per session whether the agent calls it or not. A binary on `PATH` costs nothing until it runs.

## The one mechanism that works everywhere

Of nine harnesses surveyed, only three expose a live session registry and only five accept a push. But **all nine can run a shell command** — so that's the foundation, and everything else is an optimization layered on top.

| Tier | Mechanism | Coverage |
|---|---|---|
| 0 | The agent runs `tether send` / `tether inbox` itself | every harness |
| 1 | Push delivery — HTTP hooks, `turn/steer`, hub attach | per harness |
| 2 | PTY injection | only sessions tether spawned |

The rule that keeps this honest: **if a feature only works for Claude Code, it's tier 1, never core.**

## Install

```sh
go install github.com/praneethravuri/tether/cmd/tether@latest
```

Or build from source:

```sh
make build && ./tether version
```

## Planned interface

Five commands. None of these are implemented yet.

```sh
tether                      # the fleet — bare command shows data, not help
tether run claude --as api  # spawn and register in one step
tether ls                   # who's out there, and what they're doing
tether send api "ci is red" # message one agent, or "*" for all
tether inbox                # read and clear yours
```

```
$ tether ls
3 agents · 1 blocked · 1 idle

NAME    HARNESS       AGE   PENDING
api     claude-code   12m   2
web     codex         4m    —
docs    gemini-cli    31m   —

Next: tether inbox --as api
```

Integration is one line in `AGENTS.md` or `CLAUDE.md`. No config, no restart, no standing token cost:

```
Other agents are reachable: `tether ls` to see them, `tether send <name> "msg"` to
message one, `tether inbox` to read yours.
```

## Development

```sh
make build    # compile with version stamped from git
make test     # go test -race ./...
make lint     # golangci-lint
```

CI runs build, race-enabled tests, and lint on every push.

## License

MIT — see [LICENSE](LICENSE).
