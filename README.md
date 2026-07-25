# helios

Two things in one daemon:

1. **Run and broadcast into terminal sessions.** `helios run claude` (or any command) through a
   background daemon, list what's running, broadcast a message into one or all of them from
   another terminal.
2. **Message other agents.** Independent agent harnesses (Claude Code, Codex, Gemini CLI,
   OpenCode, Aider, ...) register with the same daemon and send each other messages —
   `register` / `send` / `inbox` / `who` — over the CLI by default, or MCP/A2A if a harness
   needs one of those instead.

It has two binaries:

- **`heliosd`** — a background daemon: owns PTY sessions, and runs the mailbox
- **`helios`** — the CLI you actually type

## Requirements

- Go 1.26 or newer
- Session running (`run`/`list`/`broadcast`/`ui`) needs macOS or Linux (Unix domain sockets, PTYs)
- Mailbox (`register`/`send`/`inbox`/`who`) works on any OS — it's a separate TCP loopback
  listener, not tied to the Unix socket above

## Build

From the repo root:

```sh
go build -o heliosd ./cmd/heliosd
go build -o helios ./cmd/helios
```

Put both somewhere on your `PATH` if you want to run `helios` from anywhere.

Or, one command to build both and start the daemon in the background:

```sh
make daemon    # builds, starts heliosd, logs to /tmp/heliosd.log
make stop      # kills it
```

## Sessions: run, list, broadcast

**1. Start the daemon** (in its own terminal, leave it running):

```sh
./heliosd
```

Listens on `/tmp/helios.sock` for sessions, and `127.0.0.1:47530` for the mailbox (override
with `HELIOS_MAILBOX_ADDR`). `make daemon` does this step for you, in the background.

**2. Start a session** (in a new terminal):

```sh
./helios run claude
```

Spawns `claude` in a managed pseudo-terminal and connects your terminal to it. Prints the
session's id on startup:

```
helios: session "claude-492" (use: helios broadcast "claude-492" "<msg>")
```

Give it your own id instead of the auto-generated one:

```sh
./helios run my-session zsh
```

Exit the session (e.g. `Ctrl+D`) to end it.

**3. List and broadcast**, from any other terminal while a session is running:

```sh
./helios list
./helios broadcast "my-session" "hello"       # one session
./helios broadcast "hello everyone"           # every active session
./helios ui                                    # interactive TUI (session list + broadcast composer)
```

## Mailbox: message other agents

No daemon-managed session required — any agent (yours or another harness's) can register and
message another by name.

```sh
./helios register alice
./helios register bob

./helios send alice bob "can you check the auth module?"
./helios inbox bob
# [15:04:05] alice: can you check the auth module?

./helios who
# ["alice", "bob"]

./helios send alice "*" "heads up, deploying in 5"   # everyone but the sender
```

An agent finds out the CLI exists the same way it finds out about any other tool — one line in
`CLAUDE.md`/`AGENTS.md`:

```
Run `helios who` to see other active agents, `helios send <name> "msg"` to message one.
```

### MCP and A2A — optional, only if a harness needs them

The CLI is the default because every harness already has shell access and it costs no standing
context — an MCP tool's schema costs real tokens every session whether it's used or not. Wire
MCP in only for a harness sandboxed without shell:

```sh
./helios mcp   # stdio MCP server, exposes helios_register/send/inbox/who
```

```json
{"mcpServers": {"helios": {"command": "helios", "args": ["mcp"]}}}
```

`heliosd` also carries an A2A Agent Card (`cmd/heliosd/a2a.go`) for a genuinely external
A2A-native agent — not any of the 5 target harnesses, which all reach the mailbox via CLI or
MCP instead. Boilerplate only for now; the full request handler isn't wired up until something
actually needs it.

## Command reference

```
helios run <command>                        Run a command with an auto-generated session id
helios run <session-id> <command>           Run a command with a custom session id
helios list                                 List all active sessions
helios broadcast "<message>"                Send a message to every active session
helios broadcast <session-id> "<message>"   Send a message to one session
helios ui                                   Open the interactive TUI (session list + broadcast composer)

helios register <name>                      Register this agent with the mailbox
helios send <from> <to> <message>           Message one agent, or "*" for everyone
helios inbox <name>                         Read and clear pending messages
helios who                                  List agents seen in the last 30 minutes
helios mcp                                  Start the (optional) MCP server
```

## Cleaning up

```sh
pkill -f heliosd            # kill the daemon
pkill -f 'helios run'       # kill any running sessions
pkill -f helios              # or kill everything at once
```

## Notes

- Session ids must be unique — reusing an id that's still running is rejected.
- A broadcast is delivered as raw keystrokes into the target session's terminal, followed by
  Enter — works the same whether the session is a shell or an interactive program.
- The mailbox is in-memory — it resets when `heliosd` restarts, same as sessions do today.
- Mailbox `send` fails if the target isn't registered; broadcast (`to = "*"`) skips the sender,
  which stops the obvious self-reply loop but not a longer chain through two different agents.
