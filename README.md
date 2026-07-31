# setup-go

Install a Go toolchain and prepend it to `PATH` via `$BASH_ENV` so subsequent
job steps pick it up automatically, run `go test`, and manage tool caches
(e.g. golangci-lint's) around separate steps.

## install

### What it does

1. Resolves a Go version spec (`stable`, `latest`, `1.22`, `1.22.3`) against the
   [go.dev release API](https://go.dev/dl/?mode=json&include=all).
2. Downloads the official tarball for the current OS/arch (unless already present
   under `--install-dir`), verifies its SHA-256 checksum, and extracts it.
3. Runs `go env GOPATH` on the installed (or detected) binary to discover the
   effective `GOPATH` — no hardcoded default.
4. Appends an `export PATH=...` statement to `$BASH_ENV` (and mirrors it to
   stdout when `$BASH_ENV` is set) that prepends the toolchain's `bin`
   directory and `GOPATH`'s `bin` directory.
5. On Alpine Linux (which ships no C compiler by default), installs `gcc` and
   `musl-dev` via `apk` so `CGO_ENABLED=1` builds work. This is a no-op on
   any other platform. Failures (e.g. `apk add` failing, or failing to determine
   the current Linux distro) are silent and never fail the go installation.

```yaml
steps:
  - run:
      name: Set up Go
      command: circleci run setup-go@<version> -- install --version 1.22
  - run: go test ./...
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--version` | `stable` | Version spec: `stable`, `latest`, `1.22`, `1.22.3` |
| `--version-file` | (empty) | Path to a `go.mod` or `go.work` file; reads the Go version from its `go` directive. Overrides `--version` when set. |
| `--detect-only` | false | Use the `go` already on `PATH`; skip download and install |
| `--install-dir` | `~/.cache/setup-go` | Base directory for extracted toolchains |
| `--go-cache-checksum` | (empty) | Optional checksum to partition the toolchain cache; different checksums force a re-download even for the same Go version |

Version spec semantics:

| Spec | Resolves to |
|------|-------------|
| `stable` (default) | Newest stable release |
| `latest` | Newest release, including unstable |
| `1.22` | Newest stable `go1.22.x` |
| `1.22.3` | Exact `go1.22.3` |

### Caching

Toolchains are stored at `<install-dir>/<version>/` (e.g.
`~/.cache/setup-go/go1.22.3/`). On re-run, if that directory already contains a
`go` binary whose `go version` output matches the resolved release, the download
is skipped.

When running inside a CircleCI job, `setup-go install` also uses the job cache to
persist the toolchain directory across executors. On the first run it saves the
installed toolchain to cache; on subsequent runs it restores from cache before
checking the local filesystem, so the download is skipped even on a fresh
executor. Cache errors (e.g. running outside a job) are non-fatal and fall back
to a normal install.

## test

### What it does

Runs `go test ./...` (or `gotestsum -- ./...`) against `--project-path`.

```yaml
steps:
  - run:
      name: Set up Go
      command: circleci run setup-go@<version> -- install --version 1.22
  - run:
      name: Test
      command: circleci run setup-go@<version> -- test --project-path .
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--project-path` | `$HOME` | Directory containing your Go project files (`go.mod`, `main.go`, etc). |
| `--race` | false | Run tests with `-race`. Go's own default already switches `-covermode` to `atomic` when `-race` is set. |
| `--covermode` | (go default) | Coverage mode: `set`, `count`, or `atomic` |
| `--failfast` | false | Stop after the first test failure |
| `--count` | (go default) | Run each test, benchmark, and fuzz seed N times. `1` disables test caching. |
| `--use-gotestsum` | false | Run via `gotestsum` instead of `go test`. Requires `gotestsum` on `PATH`. |

## cache

### What it does

`cache load` and `cache save` restore/save a tool's cache directory to/from the
CircleCI job cache, for use around a separate step that runs the tool itself.
Currently supports golangci-lint's cache directory (`~/.cache/golangci-lint`),
selected with `--golangci-lint`; other targets may be added later.

```yaml
steps:
  - run:
      name: Restore golangci-lint cache
      command: circleci run setup-go@<version> -- cache load --golangci-lint --checksum '{{ checksum "go.sum" }}'
  - run:
      name: Lint
      command: golangci-lint run
  - run:
      name: Save golangci-lint cache
      command: circleci run setup-go@<version> -- cache save --golangci-lint --checksum '{{ checksum "go.sum" }}'
```

### Flags

`cache load` and `cache save` take the same flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--golangci-lint` | false | Select golangci-lint's cache directory as the target. Required — `cache load`/`cache save` error if no target is given. |
| `--key` | (empty) | Optional cache key component to partition cache entries. |
| `--checksum` | (empty) | Optional checksum to partition the cache entry; different checksums force a cache miss. |

The cache key mirrors the [`circleci/go` orb](https://circleci.com/developer/orbs/orb/circleci/go)'s
golangci-lint cache key: `v1-<key>-golangci-lint-<CIRCLE_JOB>-<arch>-<checksum>-<72h-rotating epoch>`.
Cache errors (e.g. running outside a job) are non-fatal.

## mod-download

### What it does

Runs `go mod download` in the current directory, restoring/saving the go
module cache (`GOMODCACHE`) around it so downloaded modules persist across
job executors.

1. Unless `--cache=false`, restores the go module cache from the CircleCI job
   cache.
2. Runs `go mod download`.
3. Unless `--cache=false`, saves `GOMODCACHE` (detected via `go env`, or
   `--path` when given) back to the CircleCI job cache.

```yaml
steps:
  - run:
      name: Set up Go
      command: circleci run setup-go@<version> -- install --version 1.22
  - run:
      name: Download modules
      command: circleci run setup-go@<version> -- mod-download --checksum '{{ checksum "go.sum" }}'
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--cache` | true | Set to `false` to skip restoring/saving the go module cache. |
| `--cache-key` | (empty) | Optional cache key component to partition cache entries. |
| `--checksum` | (empty) | Optional checksum to partition the cache entry; different checksums force a cache miss. |
| `--path` | (empty) | Override the go module cache directory (`GOMODCACHE`) to save/restore, instead of detecting it via `go env`. Must not be set to an empty string. |

The cache key mirrors the [`circleci/go` orb](https://circleci.com/developer/orbs/orb/circleci/go)'s
go-mod cache key: `v1-<cache-key>-go-mod-<arch>-<checksum>`. Cache errors (e.g.
running outside a job) are non-fatal; a `go mod download` failure is fatal.

## Platform support

Linux and darwin (amd64 and arm64). The official linux tarball is statically
linked and runs on Alpine (musl) without modification. Windows install is
deferred.

## Tests

Unit tests live under `{goversion,install,jobenv,cache,goenv}/` and call the
exported API directly — `install`/`goversion`/`jobenv` via a fake go.dev server
(`internal/testing/fakegodev`), `cache` via a fake CircleCI plugin server
(`github.com/circleci-functions/toolkit/sdk/sdktest`), `goenv` against the real
`go` binary (no network involved). No network access required.

Acceptance tests live under `acceptance/` and compile the real `setup-go`
binary in `TestMain`, then run it as a subprocess to verify the CLI contract
(`--help`, `install --detect-only` writing `$BASH_ENV`, `test` flag
passthrough, `cache` round-trips, unknown-flag exit code). They also use
`fakegodev`/`sdktest` so no network is required. Both suites run together
under `task test`.
