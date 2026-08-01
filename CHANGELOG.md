# Changelog

All notable changes to this project are documented here. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.3.0] - 2026-08-01

### Removed

- Removed Claude Code hooks (`intern hooks install`, `run-stop`, `run-session-start`). `intern wait` is the universal push mechanism.
- Removed `intern explain` — merged into `intern ls --detail <name>`.
- Removed `intern demo` and `intern top`.
- Removed `--json` flag — all commands emit JSON by default.
- Removed `--doing` flag and `last_note` column.
- Removed protocol version guard (`v` field on every request).
- Reset schema version to v1 (collapsed 5 migration steps into one canonical schema).

### Changed

- All CLI commands output JSON to stdout. Human-readable output is gone.
- Bare `intern` (no args) returns workspace listing as JSON.
- `intern ls --detail <name>` replaces `intern explain`.

### Simplified

- ~3,000 lines of code removed (hooks, migrations, human-readable formatting, dead commands).
- Schema migrations collapsed: fresh databases use v1 directly.

## [0.2.0] - 2026-07-31

### Changed

- Renamed the CLI, Go module, local state directory, installer variables, and release archives to `intern`.
- Kept the existing command verbs and local daemon protocol stable.
- Reworked the README and bundled agent skill for the `intern` workflow.

### Added

- Published v0.2.0 manually with GoReleaser; future releases use explicit version tags.
