# tether

Every coding agent runs in a silo. Claude Code can't see your Codex session; Codex can't message your Cline session. Each harness ships its own session registry and mailbox, and none of them cross.

tether is one shared registry for all of them: a daemon (`tetherd`) that tracks which agents are running, and a CLI (`tether`) that talks to it over a local unix socket.

## How it works

`tetherd` runs in the background and listens on a unix socket (`$TETHER_SOCK`, or `$XDG_RUNTIME_DIR/tether/sock`, or `~/.tether/sock`). The `tether` CLI connects to that socket, sends a JSON request, and prints the response.

```
tether ls / register
        │
        ▼
  unix socket (0600, owner-only)
        │
        ▼
     tetherd  ──  in-memory registry of agents
```

## Install

```sh
go install github.com/praneethravuri/tether/cmd/tether@latest
go install github.com/praneethravuri/tether/cmd/tetherd@latest
```

Or build from source:

```sh
make build && ./tether version
```

## Usage

Start the daemon first:

```sh
tetherd
```

Then, from any shell:

```sh
tether            # bare command — same as `tether ls`
tether register   # register this agent with the daemon
tether ls         # list all registered agents
tether version    # print the tether version
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
