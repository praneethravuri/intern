# Development

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

CI runs the build, `go vet`, a gofmt check and the race-enabled test suite on
Linux and macOS, plus golangci-lint and a cross-compilation matrix, on every
push and pull request.

## Regenerating the CLI docs

[`docs/reference.md`](reference.md)'s CLI Reference and Flags tables are
generated from the live cobra command tree (`cmd/intern/docsgen.go`), not
hand-typed, so they can't silently drift the way nine earlier bugs did
before that guarantee existed. After changing a command's flags, `Short`, or
`Long` text, regenerate the checked-in file:

```sh
go test ./cmd/intern -run TestGeneratedDocsMatchCheckedIn -update
```

`go test ./...` fails on its own if you forget: `TestGeneratedDocsMatchCheckedIn`
byte-compares the checked-in file against what the command tree produces
right now, and `TestDocsCommandCoverage` fails if a new top-level command is
never added to `docsCommandOrder` in the first place.

This intentionally does not use `cobra`'s own `cobra/doc` package: `cobra/doc`
shares one package between its Markdown and man-page generators, so
importing it for Markdown alone still pulls in `go-md2man` as a transitive
dependency of the whole `doc` package. `docsgen.go` instead reads the same
`*cobra.Command`/`*pflag.Flag` structs directly — already-imported types,
zero new dependencies, and nothing added to `go.mod`.
