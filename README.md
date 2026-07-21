# helios

Helios lets you run interactive terminal sessions (like `claude`, or any command) through a
background daemon, so you can list what's running and broadcast a message into one or all of
them from a different terminal.

It has two binaries:
- **`heliosd`** — a background daemon that spawns and owns your sessions
- **`helios`** — the CLI you actually type; it talks to `heliosd` over a local Unix socket

## Requirements

- Go 1.26 or newer
- macOS or Linux (uses Unix domain sockets and PTYs)

## Build

From the repo root:

```sh
go build -o heliosd ./cmd/heliosd
go build -o helios ./cmd/helios
```

This produces two binaries, `heliosd` and `helios`, in the current directory. Put them
somewhere on your `PATH` if you want to run `helios` from anywhere.

## Run

**1. Start the daemon** (in its own terminal, leave it running):

```sh
./heliosd
```

It listens on `/tmp/helios.sock`. Leave this running in the background for everything below.

**2. Start a session** (in a new terminal):

```sh
./helios run claude
```

This spawns `claude` in a managed pseudo-terminal and connects your terminal to it — it
behaves like you ran `claude` directly. On startup it prints the session's id, e.g.:

```
helios: session "claude-492" (use: helios broadcast "claude-492" "<msg>")
```

Give it your own id instead of the auto-generated one:

```sh
./helios run my-session zsh
```

Exit the session (e.g. `Ctrl+D` or however the running command normally quits) to end it.

**3. List active sessions** (from any other terminal):

```sh
./helios list
```

**4. Broadcast a message into a session**, from another terminal, while it's running:

```sh
# send to one specific session
./helios broadcast "my-session" "hello"

# send to every active session
./helios broadcast "hello everyone"
```

The daemon replies with how many sessions received it, e.g. `Delivered to 1 of 1 session(s).`

## Command reference

```
helios run <command>                        Run a command with an auto-generated session id
helios run <session-id> <command>           Run a command with a custom session id
helios list                                 List all active sessions
helios broadcast "<message>"                Send a message to every active session
helios broadcast <session-id> "<message>"   Send a message to one session
```

## Notes

- Session ids must be unique — reusing an id that's still running is rejected.
- A broadcast is delivered as raw keystrokes into the target session's terminal, followed by
  Enter — it works the same whether the session is a shell or an interactive program.
