# Development

Intern is a Go CLI plus an auto-started local daemon. The daemon exposes a
Unix socket and persists workspace-scoped messages and claims in SQLite.
Changes to command behavior must keep the Cobra help, README, bundled agent
skill, and tests aligned.

## Prerequisites

- The Go version declared in [`go.mod`](../go.mod)
- `golangci-lint`
- `ShellCheck`
- GoReleaser v2.17.0

## Local development

```sh
make build       # builds ./intern with a version derived from git
make test        # race-enabled package tests
make lint        # golangci-lint
make fmt         # formats Go files in place
make vet         # static analysis
make tidy        # updates module metadata
```

Run the binary from another terminal while developing:

```sh
./intern start
```

Client commands auto-start the daemon when needed, so normally you can run
`./intern register <name>`, `./intern send ...`, and the other commands
directly. For an isolated local run, set `INTERN_SOCK`, `INTERN_DB`, and
`INTERN_WORKSPACE` to test-specific values.

## Required verification

Before opening a pull request, run the same release checks used for v0.3.1:

```sh
go mod tidy -diff
go build ./...
go vet ./...
test -z "$(gofmt -s -l .)"
go test -race -count=1 ./...
golangci-lint run
shellcheck docs/install.sh
goreleaser check
goreleaser release --snapshot --clean
```

The snapshot must also pass the installer end-to-end check: serve its four
archives and `checksums.txt` over trusted HTTPS, run `docs/install.sh` with
`INTERN_BASE_URL` and a temporary `INTERN_INSTALL_DIR`, then run the installed
binary's `version` command. The `install-e2e` GitHub Actions job is the
canonical implementation of that check.

`gofmt -s -l .` must produce no paths. The command above uses `test -z` so a
non-empty result fails immediately without rewriting files.

## Continuous integration

The pull-request workflow enforces module tidiness, builds and race-enabled
tests on Linux and macOS, a formatting check, `golangci-lint`, ShellCheck for
the installer, and the GoReleaser snapshot installer E2E. Scheduled security
scanning runs the pinned Go vulnerability scanner; Dependabot monitors Go
modules and GitHub Actions.

`go vet ./...` remains a required local and release verification, even though
it is not a separate CI job.

## Documentation and releases

The command reference is maintained with the command code rather than
generated. When a command, positional argument, flag, output shape, or exit
status changes, update all of these in the same change:

- [`README.md`](../README.md)
- [`skills/intern/SKILL.md`](../skills/intern/SKILL.md)
- Cobra help and examples under `cmd/intern/`
- tests that exercise the changed command

Follow [`docs/releasing.md`](releasing.md) for the complete manual v0.3.1
release procedure.
