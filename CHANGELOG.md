# Changelog

All notable changes to setup-go will be documented in this file. Each release's section is added
by its release PR (see [RELEASE.md](RELEASE.md)) and becomes its GitHub release notes.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.7.0] - 2026-10-22

### What's Changed
* Use the toolkit orb's release-pr and publish jobs by @stiyyagura0901 in https://github.com/circleci-functions/setup-go/pull/28
* Update module golang.org/x/mod to v0.40.0 [SECURITY] by @renovate[bot] in https://github.com/circleci-functions/setup-go/pull/30
* Update module golang.org/x/text to v0.41.0 [SECURITY] by @renovate[bot] in https://github.com/circleci-functions/setup-go/pull/31
* Update toolkit to v0.3.0 by @stiyyagura0901 in https://github.com/circleci-functions/setup-go/pull/45
* Resolve the Go version from both the go and toolchain lines by @stiyyagura0901 in https://github.com/circleci-functions/setup-go/pull/42
* Fix module cache fallback, the golangci-lint cache dir and --detect-only by @stiyyagura0901 in https://github.com/circleci-functions/setup-go/pull/43
* Lead the README with the functions block and fill its gaps by @stiyyagura0901 in https://github.com/circleci-functions/setup-go/pull/44

### New Contributors
* @renovate[bot] made their first contribution in https://github.com/circleci-functions/setup-go/pull/30

**Full Changelog**: https://github.com/circleci-functions/setup-go/compare/v0.6.0...v0.7.0

## [0.6.0] - 2026-10-09

### What's Changed
* Do not fetch from go.dev on --detect-only by @mangyau in https://github.com/circleci-functions/setup-go/pull/25
* Release by merging a release PR by @stiyyagura0901 in https://github.com/circleci-functions/setup-go/pull/26

### New Contributors
* @mangyau made their first contribution in https://github.com/circleci-functions/setup-go/pull/25

**Full Changelog**: https://github.com/circleci-functions/setup-go/compare/v0.5.4...v0.6.0

## [0.5.4] - 2026-10-07

The last release made by pushing a tag, published as `v0.5.4-2ad832a`. Earlier releases are in
the repo's [tags](https://github.com/circleci-functions/setup-go/tags).
