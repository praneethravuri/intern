# Releasing v0.3.1

This project publishes releases manually from a merged, green `main` branch.
Do not move the published `v0.3.0` tag. The v0.3.1 tag must be annotated and
must point at the merged `main` commit.

## 1. Prepare the release branch and pull request

Start from an up-to-date `main` with no unrelated changes:

```sh
git fetch origin --tags
git switch main
git pull --ff-only origin main
git switch -c codex/v0.3.1-release-hardening
```

Make the release-hardening changes, including this documentation. Maintain the
root `TODO.md` as a local working checklist, but leave it unstaged and do not
commit it. Before every commit, activate Ponytail Ultra and run the Ponytail
review on the staged diff. Apply valid simplifications, re-run the relevant
verification, then commit with the configured human author only. Do not add
co-author or AI-tool trailers.

In an agent host that provides the Ponytail commands, the required pre-commit
review is:

```text
/ponytail ultra
/ponytail-review
```

Push the branch and open a pull request into `main`:

```sh
git push -u origin codex/v0.3.1-release-hardening
gh pr create --base main --head codex/v0.3.1-release-hardening \
  --title "release: v0.3.1 hardening" --fill
```

Run and record every pre-PR release check before requesting review:

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

Confirm the snapshot installer E2E succeeds. It must serve the snapshot's
four archives and `checksums.txt` over trusted HTTPS, run `docs/install.sh`
with `INTERN_BASE_URL` and a temporary `INTERN_INSTALL_DIR`, then run the
installed binary's `version` command.

Wait for all pull-request checks to pass, then merge the PR with the normal
GitHub review process.

## 2. Verify merged main

Fetch the merge and verify that the release branch's tip is in `main`:

```sh
git fetch origin --tags
git switch main
git pull --ff-only origin main
git merge-base --is-ancestor codex/v0.3.1-release-hardening origin/main
```

Run the same release verification again on the merged commit:

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

Confirm the Go CI and Security workflow runs for the merged commit are green
before tagging. With GitHub CLI, list the runs for that exact commit and watch
each incomplete run:

```sh
gh run list --commit "$(git rev-parse HEAD)" --workflow "Go CI"
gh run list --commit "$(git rev-parse HEAD)" --workflow "Security"
gh run watch <run-id> --exit-status
```

## 3. Tag and publish

Create and push the annotated tag from the verified `main` commit:

```sh
git tag -a v0.3.1 -m "Release v0.3.1"
git push origin v0.3.1
```

Confirm it is an annotated tag at the merged commit:

```sh
test "$(git cat-file -t v0.3.1)" = tag
test "$(git rev-list -n 1 v0.3.1)" = "$(git rev-parse origin/main)"
git show --no-patch --format=fuller v0.3.1
```

Publish the GitHub release with GoReleaser v2.17.0, matching CI.
`GITHUB_TOKEN` must be able to create releases in `praneethravuri/intern`:

```sh
GITHUB_TOKEN="$(gh auth token)" goreleaser release --clean
```

GoReleaser creates four archives—Linux and macOS for `amd64` and `arm64`—and
one `checksums.txt` file. It does not require signing or SBOM tooling for this
manual release flow.

## 4. Verify published artifacts and installer

Download the release into a disposable directory, validate every checksum,
then exercise the installer against the public release:

```sh
release_dir="$(mktemp -d)"
gh release download v0.3.1 --repo praneethravuri/intern \
  --pattern 'intern_*.tar.gz' --pattern checksums.txt --dir "$release_dir"

(
  cd "$release_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c checksums.txt
  else
    shasum -a 256 -c checksums.txt
  fi
)

install_dir="$release_dir/install"
INTERN_VERSION=v0.3.1 INTERN_INSTALL_DIR="$install_dir" sh docs/install.sh
test "$("$install_dir/intern" version)" = v0.3.1
```

The checksum command must report all four archives as valid, and the installed
binary must print exactly `v0.3.1`.

## 5. Clean up merged branches

Keep `main` and `origin/main`. After the tag and release verification succeed,
delete only the merged hardening branch and the already-merged local
`v0.3.0-simplify` branch:

```sh
if git ls-remote --exit-code --heads origin codex/v0.3.1-release-hardening >/dev/null; then
  git push origin --delete codex/v0.3.1-release-hardening
fi
git branch -d codex/v0.3.1-release-hardening
git branch -d v0.3.0-simplify
git switch main
```

If GitHub auto-deleted the remote pull-request branch, the conditional skips
the deletion. Confirm it is gone:

```sh
git ls-remote --exit-code --heads origin codex/v0.3.1-release-hardening
```

That command must return no branch. Finish by confirming the release tag still
points at `origin/main`:

```sh
git fetch origin --tags
test "$(git rev-list -n 1 v0.3.1)" = "$(git rev-parse origin/main)"
```
