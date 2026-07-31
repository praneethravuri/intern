# Contributing

## Build and test

```sh
make build        # the binary, version stamped from git describe
make test         # go test -race -count=1 ./...
make lint         # golangci-lint, using .golangci.yml
make fmt          # gofmt -w
make help         # list every target
```

CI runs the same build, vet, gofmt check, race-enabled test suite (Linux and
macOS), golangci-lint, and a cross-compilation matrix on every push and PR.

## Before you open a PR

- Add or update tests for the behavior you're changing — TDD (failing test
  first) is the house style; match the existing suite (stdlib only, no
  testify).
- `make test`, `make lint`, and `make fmt` all pass locally.
- Passing tests is necessary but not sufficient evidence a change works.
  Include a real CLI transcript in the PR description that demonstrates the
  behavior — the reviewer should be able to see the effect, not just trust
  that it exists.
- Keep comments to 1-2 lines, only where the *why* isn't obvious from the code.

## Reporting a bug

Open an issue with the output of `tether version`, your OS/arch, and the
smallest command sequence that reproduces it. For anything you believe is a
security issue, do not open a public issue — email the maintainer instead.
