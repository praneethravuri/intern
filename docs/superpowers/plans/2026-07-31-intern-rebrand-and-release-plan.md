# Intern Rebrand and Release Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task with review checkpoints. Steps use checkbox syntax for tracking.

**Goal:** Rename the local agent-coordination product from tether to intern, preserve the existing command verbs and behavior, publish a clean v0.2.0 release with intern_* binaries, update the bundled skill and Obsidian references, and automate future releases.

**Architecture:** Keep the current single Go CLI plus auto-started local daemon, Unix socket, and SQLite store. The executable, Go module, install paths, environment variables, docs, and release archives become intern; the command verbs remain stable. Historical v0.1.0 assets remain available under their original names, while the first renamed release is v0.2.0.

**Tech Stack:** Go 1.26, Cobra, modernc SQLite, Make, GoReleaser v2, GitHub Actions, release-please, shellcheck, the skills.sh GitHub-backed skill index, and the Obsidian CLI bridge.

## Global Constraints

- Work from the existing rebrand-intern branch.
- The branch is based on GitHub main commit 69a958d3aea78f76fefcccc85dd71137478c1a41 (69a958d, 2026-07-31).
- Preserve the existing uncommitted rebrand work; do not reset, clean, or discard it.
- intern is the only current executable name. Existing verbs such as register, send, wait, inbox, claim, release, hooks, and demo remain stable.
- Remove tether and codex from current product source, docs, workflows, examples, tests, generated docs, and skill content. This implementation plan records the rename and is the explicit documentation exception. Historical Git objects and the historical v0.1.0 release are not rewritten.
- Keep the old v0.1.0 GitHub release and assets; do not delete them.
- Use v0.2.0 for the first renamed release.
- README copy must be concise, precise, and human-readable. Do not add broken image references.
- Do not edit Obsidian notes until its CLI bridge is reachable and the target vault is known.
- Do not push, rename the GitHub repository, rename the local directory, or publish a release until local verification is complete and the user approves the external-operation checkpoint.

## Current Context

### Git state

- Current branch: rebrand-intern.
- Current commit, local main, and last-fetched origin/main: 69a958d.
- Current remote: git@github.com:praneethravuri/tether.git.
- Worktree is dirty with approximately 100 changed/renamed files and one new workflow. No commit has been created.
- The requested branch rename has already happened locally; there is no codex/ prefix.

### Existing uncommitted changes to retain and review

- cmd/tether/ has been renamed to cmd/intern/; imports, command text, test fixtures, paths, environment variables, and generated docs were mechanically changed.
- skills/tether/SKILL.md has been renamed to skills/intern/SKILL.md.
- go.mod now declares github.com/praneethravuri/intern.
- Make, GoReleaser, installer, ignore/lint files, issue templates, contributor/security docs, development docs, harness docs, and docs/reference.md use the new name.
- README.md is a shorter landing page with a skills.sh badge and no generated image yet.
- CI paths use intern; .github/workflows/release-please.yml was added as a candidate automation workflow.
- The mechanical rename briefly mapped the removed harness marker to AGENT_*; this must be reviewed and removed rather than becoming an invented harness integration.

### Releases and binaries

- Existing/latest tag: v0.1.0, published 2026-07-29, not draft/prerelease.
- Existing historical assets: tether_darwin_amd64.tar.gz, tether_darwin_arm64.tar.gz, tether_linux_amd64.tar.gz, tether_linux_arm64.tar.gz, tether_windows_amd64.zip, and checksums.txt.
- Existing release workflow triggers on v* tags and uses GoReleaser, cosign, and syft.
- Current GoReleaser config targets Linux/macOS amd64/arm64. Windows exists historically but is absent from the current config; resolve this before promising v0.2.0 Windows support.
- Expected renamed archives are intern_darwin_amd64.tar.gz, intern_darwin_arm64.tar.gz, intern_linux_amd64.tar.gz, and intern_linux_arm64.tar.gz, plus checksums, signatures, and SBOMs.

### Verification and blockers

- Generated docs update passed: go test ./cmd/intern -run TestGeneratedDocsMatchCheckedIn -update.
- git diff --check passed.
- go test ./... compiled the renamed packages but socket tests fail in this sandbox because Unix-socket bind/listen is denied; full tests need CI or an unrestricted environment.
- obsidian vaults currently says the CLI cannot find Obsidian. No Obsidian changes were made.
- Earlier gh auth status reported an invalid token. GitHub mutations need fresh authentication.
- Built-in image generation is unavailable. The image-generation CLI fallback requires explicit user approval and an API key.

## Decisions

### CLI contract

Use intern as the only current executable. Do not keep an undocumented tether alias; the migration is represented by v0.2.0, while v0.1.0 remains available.

### Release automation

Use release-please for conventional-commit release PRs and GoReleaser for artifacts. The candidate design runs GoReleaser in the same release-please job when release_created == true, because a tag/release created with the default GITHUB_TOKEN does not reliably start a second workflow. Validate this behavior before relying on it.

### skills.sh

Keep skills/intern/SKILL.md in the public GitHub repository. There is no separate upload command; users install it with:

~~~sh
npx skills add praneethravuri/intern --skill intern
~~~

Keep the README badge and command aligned with the final repository and skill slug. Reference: https://www.skills.sh/docs.

## File Map

- cmd/intern/: executable, Cobra commands, identity detection, generated CLI docs, and command tests.
- internal/ and e2e/: runtime paths, protocol, daemon, store, hooks, persistence, and real-process tests.
- skills/intern/SKILL.md: installable agent instructions.
- README.md, docs/reference.md, docs/development.md, docs/harness-verification.md, docs/install.sh, CONTRIBUTING.md, SECURITY.md: public documentation and installer.
- Makefile, .goreleaser.yaml, and .github/workflows/: build, CI, security, release, and automation.
- GitHub settings, remote URL, GitHub releases, local path /Users/praneethravuri/projects/intern, and Obsidian notes: external operations performed last.

## Implementation Tasks

### Task 1: Reconcile the existing worktree

**Files:** Read-only review of all modified files; no source edits.

- [ ] Capture the starting state:

~~~sh
git status --short --branch
git diff --check
git diff --name-status
~~~

- [ ] Review behavior-sensitive replacements in cmd/intern/identity.go, cmd/intern/identity_test.go, docs/install.sh, internal/hooks/claudecode/settings.go, and every release workflow.
- [ ] Confirm the old-name scan is clean after local tasks:

~~~sh
rg -n -i --hidden -g '!.git/**' -g '!docs/superpowers/plans/**' '(tether|codex)' .
~~~

- [ ] Stop for user review of the diff categories before more implementation.

### Task 2: Complete and test the Go/module rename

**Files:** go.mod, cmd/intern/**/*.go, internal/**/*.go, e2e/e2e_test.go; existing directory rename cmd/tether/ → cmd/intern/.

- [ ] Verify exact runtime mappings:

~~~text
github.com/praneethravuri/tether  -> github.com/praneethravuri/intern
cmd/tether                         -> cmd/intern
TETHER_*                           -> INTERN_*
~/.tether                          -> ~/.intern
tether.db                          -> intern.db
~~~

- [ ] Remove the accidental AGENT_HOME/AGENT_SESSION_ID mapping and retain the existing synthetic session fallback for unrecognized harnesses.
- [ ] Format, build, and vet:

~~~sh
gofmt -s -w .
GOCACHE=/tmp/intern-go-cache go build ./...
GOCACHE=/tmp/intern-go-cache go vet ./...
~~~

- [ ] Run non-socket tests:

~~~sh
GOCACHE=/tmp/intern-go-cache go test ./internal/id ./internal/kind ./internal/proc ./internal/sanitize ./internal/store ./internal/wsname ./internal/hooks/claudecode
GOCACHE=/tmp/intern-go-cache go test ./cmd/intern -run 'TestGeneratedDocsMatchCheckedIn|TestDetectHarness|TestValidateName|TestSyntheticSessionID'
~~~

- [ ] Run go test -race -count=1 ./... in CI or an unrestricted local environment.

### Task 3: Finalize the intern skill

**Files:** skills/intern/SKILL.md, README.md; existing directory rename skills/tether/ → skills/intern/.

- [ ] Confirm frontmatter uses name: intern and no stale product/harness names remain.
- [ ] Cross-check every example against intern --help, especially register, send, wait, inbox/replay, claims, hooks, demo, and JSON output.
- [ ] Keep the exact distribution command npx skills add praneethravuri/intern --skill intern and matching badge.

### Task 4: Finalize the concise README and visuals

**Files:** README.md, optional tracked assets under docs/assets/.

- [ ] Keep this order: one-sentence description, install, two-agent handoff, compact command table, guarantees/limits, deeper-doc links, development commands.
- [ ] Remove duplicated reference material and long implementation comparisons.
- [ ] If built-in image generation becomes available, generate and inspect the agreed office-comedy visual, store it under docs/assets/, and add one valid reference. If unavailable, leave images out and report the limitation.
- [ ] Run README drift checks:

~~~sh
GOCACHE=/tmp/intern-go-cache go test ./cmd/intern -run 'TestReadmeFlagsTableMatchesRegisteredFlags|TestUniversalFlagsMatchReadmeClaim|TestGeneratedDocsMatchCheckedIn'
~~~

### Task 5: Complete CI, installer, and release automation

**Files:** Makefile, .goreleaser.yaml, docs/install.sh, .github/workflows/go.yml, .github/workflows/release.yml, .github/workflows/security.yml, .github/workflows/release-please.yml, .golangci.yml, .gitignore.

- [ ] Confirm make build produces ./intern and ./intern version reports the Git-derived version.
- [ ] Confirm GoReleaser uses ./cmd/intern, binary intern, archive template intern_{{ .Os }}_{{ .Arch }}, and retains cosign/SBOM behavior.
- [ ] Decide explicitly whether Windows returns in v0.2.0; do not imply support not produced by GoReleaser.
- [ ] Validate installer variables INTERN_VERSION, INTERN_INSTALL_DIR, INTERN_BASE_URL, checksum lookup, extraction, and installed path.
- [ ] Validate release-please uses release-type: go, targets main, does not permanently pin release-as, checks out the generated tag, and runs GoReleaser only when a release was created.
- [ ] Run:

~~~sh
shellcheck docs/install.sh
git diff --check
~~~

### Task 6: Regenerate and verify docs

**Files:** docs/reference.md, docs/development.md, docs/harness-verification.md, CONTRIBUTING.md, SECURITY.md, issue templates.

- [ ] Regenerate:

~~~sh
GOCACHE=/tmp/intern-go-cache go test ./cmd/intern -run TestGeneratedDocsMatchCheckedIn -update
~~~

- [ ] Run command coverage and README/reference checks.
- [ ] Run the repository-wide stale-name scan and require no current-worktree matches.

### Task 7: Update Obsidian through the CLI

**Files:** Related Obsidian notes and links discovered through the CLI; no guessed vault paths.

- [ ] Run obsidian vaults. If the bridge is still unavailable, stop and report the blocker.
- [ ] Search only project-related notes for old product, repository, branch, CLI, release, and skill references.
- [ ] Update related notes to intern, rebrand-intern, the renamed repository, v0.2.0, and the new skill command.
- [ ] Re-search and verify renamed note links.

### Task 8: Verify the local release candidate

**Files:** No intended source changes; ignored dist/ may be used for artifacts.

- [ ] Run:

~~~sh
gofmt -s -l .
GOCACHE=/tmp/intern-go-cache go build ./...
GOCACHE=/tmp/intern-go-cache go vet ./...
GOCACHE=/tmp/intern-go-cache go test -race -count=1 ./...
~~~

- [ ] If GoReleaser is installed, run goreleaser release --snapshot --clean --skip=sign,sbom and confirm archive/binary names.
- [ ] Exercise the installer against a locally served trusted HTTPS snapshot using the same pattern as .github/workflows/go.yml.
- [ ] Confirm only intern_* release archives are generated by the new configuration.

### Task 9: External rename, push, and bootstrap release

**Systems:** GitHub repository, Git remote, GitHub release, and local path /Users/praneethravuri/projects/tether.

- [ ] Verify authentication with gh auth status; stop if the token is invalid.
- [ ] Commit the approved local changes in logical groups: source/module rename, docs/skill, and CI/release automation.
- [ ] Rename the GitHub repository to praneethravuri/intern, then set:

~~~sh
git remote set-url origin git@github.com:praneethravuri/intern.git
~~~

- [ ] Push rebrand-intern and open a pull request into main if that is the repository’s review flow.
- [ ] After merge, use exactly one bootstrap path:

~~~sh
git tag -a v0.2.0 -m "Release v0.2.0"
git push origin v0.2.0
~~~

Or use the validated release-please flow; never run both bootstrap paths.
- [ ] Rename the local directory last:

~~~sh
mv /Users/praneethravuri/projects/tether /Users/praneethravuri/projects/intern
~~~

Reopen the workspace from the new path.

### Task 10: Verify publication

- [ ] Verify from /Users/praneethravuri/projects/intern:

~~~sh
pwd
git status --short --branch
git remote -v
git describe --tags --always
~~~

- [ ] Confirm GitHub latest release is v0.2.0, assets use intern_, checksums match, and installer URLs resolve.
- [ ] Confirm npx skills add praneethravuri/intern --skill intern resolves the public skill and the README badge points to the same slug.
- [ ] Re-run stale-name, generated-doc, build, and test checks.
- [ ] Report separately any checks requiring GitHub Actions, valid GitHub auth, image generation, or a live Obsidian bridge.

## Review Checkpoints

1. Review the existing uncommitted diff and this plan before more source edits.
2. Review the final local diff and verification output before committing.
3. Explicitly approve GitHub rename, branch push, release/tag, and local-directory move.
4. Verify the public release, skill index, and Obsidian notes after publication.
