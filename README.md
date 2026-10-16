# setup-go

Installs a Go toolchain, puts it on `PATH` for the rest of the job, and caches
what your build downloads and compiles.

```yaml
version: 2.1

functions:
  setup-go: github.com/circleci-functions/setup-go@v0.6.0

jobs:
  build:
    docker:
      - image: cimg/base:stable
    steps:
      - checkout
      - setup-go:
          with:
            version: "1.24"
            checksum: '{{ checksum "go.sum" }}'
      - run: go build ./...
      - run: go test ./...
```

Later steps pick up `go` automatically, in whatever shell they run — there is
nothing to source or export yourself.

`circleci function add setup-go` writes the `functions:` entry for you, pinned
to the latest release. Each [flag](#flags) is a `with:` key, named without its
leading `--`.

Where the `functions:` block isn't available, as on CircleCI Server, call
setup-go by its full name with `circleci run` and pass the flags after `--`:

```yaml
- run: circleci run github.com/circleci-functions/setup-go@v0.6.0 -- --version 1.24 --checksum '{{ checksum "go.sum" }}'
```

## How it gets onto PATH

A step inherits its environment from the job, not from the step before it, so
setup-go cannot just prepend to `PATH`. It links `go` and `gofmt` into a
directory `PATH` already holds, which reaches every later process whatever
shell it runs in.

The directory is chosen by preference — `$GOPATH/bin`, `~/.local/bin`,
`~/bin`, `/usr/local/bin` — and if none of those is on `PATH` ahead of an
existing toolchain, that toolchain is taken over instead, so the link never
lands somewhere another tool owns. setup-go then asks the `go` that `PATH`
resolves for its `GOROOT`, and fails unless it is the one just installed.

Nothing is written to `$BASH_ENV`. If a job needs the toolchain's location
rather than just `go`, ask the toolchain: `go env GOROOT`.

## Choosing a version

`--version` takes a spec, not just an exact release:

| Spec | Resolves to |
|------|-------------|
| `stable` (default) | Newest stable release |
| `latest` | Newest release, including unstable |
| `1.24` | Newest stable `go1.24.x` |
| `1.24.3` | Exact `go1.24.3` |

To keep the toolchain in step with your module, point at the file that already
declares it:

```yaml
- setup-go:
    with:
      version-file: go.mod
```

## Caching

Caching is on by default. setup-go restores the Go module cache and build cache
before your steps run, and saves them once the job is done — so nothing in
between needs a cache step of its own, and a job that fails, is cancelled or
times out still keeps what it downloaded and compiled.

Pass `--checksum` so the caches refresh when your dependencies change:

```yaml
- setup-go:
    with:
      checksum: '{{ checksum "go.sum" }}'
```

Add `--golangci-lint-cache` if the job also runs golangci-lint:

```yaml
- setup-go:
    with:
      golangci-lint-cache: true
      checksum: '{{ checksum "go.sum" }}'
```

Turn a single cache off with `--build-cache=false`, or all of them with
`--cache=false`. Cache problems never fail the job.

The installed toolchain is cached too, so a fresh executor usually skips the
download entirely.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--version` | `stable` | Version spec: `stable`, `latest`, `1.24`, `1.24.3` |
| `--version-file` | (empty) | Path to a `go.mod` or `go.work` file; reads the version from its `go` directive. Overrides `--version`. |
| `--detect-only` | false | Use the `go` already on `PATH` instead of installing one. `--version` and `--version-file` are ignored |
| `--install-dir` | `~/.cache/setup-go` | Where toolchains are installed |
| `--cache` | true | Set to `false` to disable all caching |
| `--go-mod-cache` | true | Cache the Go module cache (`GOMODCACHE`) |
| `--build-cache` | true | Cache the Go build cache (`GOCACHE`) |
| `--golangci-lint-cache` | false | Cache golangci-lint's cache directory |
| `--key` | (empty) | Cache key component, for keeping caches separate — e.g. per module in a monorepo |
| `--checksum` | (empty) | Refresh the caches when this changes — usually `{{ checksum "go.sum" }}` |

## Platform support

Linux, macOS and Windows, on amd64 and arm64.

On Linux it runs on Alpine (musl) without modification, and installs `gcc` and
`musl-dev` there so `CGO_ENABLED=1` builds work.

Windows steps pick the toolchain up in any shell — bash, cmd or PowerShell —
via small forwarding shims rather than symlinks.

Anything `go install` builds is reachable by name too, provided `$GOPATH/bin`
is on `PATH` or does not exist yet: setup-go points a missing one at a
directory that is. One that already exists off `PATH` is left alone, and said
so in the output.
