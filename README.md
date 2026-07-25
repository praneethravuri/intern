# tether

Two things in one daemon:

1. **Run and broadcast into terminal sessions.** `tether run claude` (or any command) through a
   background daemon, list what's running, broadcast a message into one or all of them from
   another terminal.
2. **Message other agents.** Independent agent harnesses (Claude Code, Codex, Gemini CLI,
   OpenCode, Aider, ...) register with the same daemon and send each other messages —
   `register` / `send` / `inbox` / `who` — over the CLI by default, or MCP/A2A if a harness
   needs one of those instead.

It has two binaries:

- **`tetherd`** — a background daemon: owns PTY sessions, and runs the mailbox
- **`tether`** — the CLI you actually type

## Requirements

- Go 1.26 or newer
- Session running (`run`/`list`/`broadcast`/`ui`) needs macOS or Linux (Unix domain sockets, PTYs)
- Mailbox (`register`/`send`/`inbox`/`who`) works on any OS — it's a separate TCP loopback
  listener, not tied to the Unix socket above

## Build

From the repo root:

```sh
go build -o tetherd ./cmd/tetherd
go build -o tether ./cmd/tether
```

Put both somewhere on your `PATH` if you want to run `tether` from anywhere.

Or, one command to build both and start the daemon in the background:

```sh
make daemon    # builds, starts tetherd, logs to /tmp/tetherd.log
make stop      # kills it
```

## Sessions: run, list, broadcast

**1. Start the daemon** (in its own terminal, leave it running):

```sh
./tetherd
```

Listens on `/tmp/tether.sock` for sessions, and `127.0.0.1:47530` for the mailbox (override
with `TETHER_MAILBOX_ADDR`). `make daemon` does this step for you, in the background.

**2. Start a session** (in a new terminal):

```sh
./tether run claude
```

Spawns `claude` in a managed pseudo-terminal and connects your terminal to it. Prints the
session's id on startup:

```
tether: session "claude-492" (use: tether broadcast "claude-492" "<msg>")
```

Give it your own id instead of the auto-generated one:

```sh
./tether run my-session zsh
```

Exit the session (e.g. `Ctrl+D`) to end it.

**3. List and broadcast**, from any other terminal while a session is running:

```sh
./tether list
./tether broadcast "my-session" "hello"       # one session
./tether broadcast "hello everyone"           # every active session
./tether ui                                    # interactive TUI (session list + broadcast composer)
```

## Mailbox: message other agents

No daemon-managed session required — any agent (yours or another harness's) can register and
message another by name.

```sh
./tether register alice
./tether register bob

./tether send alice bob "can you check the auth module?"
./tether inbox bob
# [15:04:05] alice: can you check the auth module?

./tether who
# ["alice", "bob"]

./tether send alice "*" "heads up, deploying in 5"   # everyone but the sender
```

An agent finds out the CLI exists the same way it finds out about any other tool — one line in
`CLAUDE.md`/`AGENTS.md`:

```
Run `tether who` to see other active agents, `tether send <name> "msg"` to message one.
```

### MCP and A2A — optional, only if a harness needs them

The CLI is the default because every harness already has shell access and it costs no standing
context — an MCP tool's schema costs real tokens every session whether it's used or not. Wire
MCP in only for a harness sandboxed without shell:

```sh
./tether mcp   # stdio MCP server, exposes tether_register/send/inbox/who
```

```json
{"mcpServers": {"tether": {"command": "tether", "args": ["mcp"]}}}
```

`tetherd` also carries an A2A Agent Card (`cmd/tetherd/a2a.go`) for a genuinely external
A2A-native agent — not any of the 5 target harnesses, which all reach the mailbox via CLI or
MCP instead. Boilerplate only for now; the full request handler isn't wired up until something
actually needs it.

## Command reference

```
tether run <command>                        Run a command with an auto-generated session id
tether run <session-id> <command>           Run a command with a custom session id
tether list                                 List all active sessions
tether broadcast "<message>"                Send a message to every active session
tether broadcast <session-id> "<message>"   Send a message to one session
tether ui                                   Open the interactive TUI (session list + broadcast composer)

tether register <name>                      Register this agent with the mailbox
tether send <from> <to> <message>           Message one agent, or "*" for everyone
tether inbox <name>                         Read and clear pending messages
tether who                                  List agents seen in the last 30 minutes
tether mcp                                  Start the (optional) MCP server
```

## Cleaning up

```sh
pkill -f tetherd            # kill the daemon
pkill -f 'tether run'       # kill any running sessions
pkill -f tether              # or kill everything at once
```

## Notes

- Session ids must be unique — reusing an id that's still running is rejected.
- A broadcast is delivered as raw keystrokes into the target session's terminal, followed by
  Enter — works the same whether the session is a shell or an interactive program.
- The mailbox is in-memory — it resets when `tetherd` restarts, same as sessions do today.
- Mailbox `send` fails if the target isn't registered; broadcast (`to = "*"`) skips the sender,
  which stops the obvious self-reply loop but not a longer chain through two different agents.
