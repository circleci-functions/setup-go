# setup-go

Installs a Go toolchain, puts it on `PATH` for the rest of the job, and caches
what your build downloads and compiles.

```yaml
steps:
  - run:
      name: Set up Go
      command: circleci run setup-go@<version> -- --version 1.24 --checksum '{{ checksum "go.sum" }}'
  - run: go build ./...
  - run: go test ./...
```

Later steps pick up `go` automatically, in whatever shell they run — there is
nothing to source or export yourself.

## How it gets onto PATH

A step inherits its environment from the job, not from the step before it, so
setup-go cannot simply prepend to `PATH`. Instead it links `go` and `gofmt`
into a directory `PATH` already holds, ahead of any toolchain the image ships.
That reaches every later process, including ones that run under a shell other
than bash and ones that source nothing at all.

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
- run: circleci run setup-go@<version> -- --version-file go.mod
```

## Caching

Caching is on by default. setup-go restores the Go module cache and build cache
before your steps run, and saves them once the job is done — so nothing in
between needs a cache step of its own, and a job that fails, is cancelled or
times out still keeps what it downloaded and compiled.

Pass `--checksum` so the caches refresh when your dependencies change:

```yaml
- run: circleci run setup-go@<version> -- --checksum '{{ checksum "go.sum" }}'
```

Add `--golangci-lint-cache` if the job also runs golangci-lint:

```yaml
- run: circleci run setup-go@<version> -- --golangci-lint-cache --checksum '{{ checksum "go.sum" }}'
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
| `--detect-only` | false | Use the `go` already on `PATH` instead of installing one |
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

Windows steps pick the toolchain up in any shell — bash, cmd or PowerShell.
Where a job holds the privilege to create symlinks it gets symlinks, and where
it does not it gets small forwarding shims instead.

Anything `go install` builds is reachable by name too, as long as
`$GOPATH/bin` is on `PATH` or does not exist yet — setup-go points a missing
one at a directory that is. A `$GOPATH/bin` that already exists and is not on
`PATH` is left alone, and setup-go says so in its output.
