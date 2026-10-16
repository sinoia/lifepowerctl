# Development

## Requirements

- Go version compatible with `go.mod`; release/security CI uses a currently
  supported Go toolchain.
- GNU Make and standard POSIX shell tools.
- No Node or frontend dependency tree.
- Hardware is not required for the ordinary build and test suite.

Clone the repository and keep project-local caches:

```console
git clone https://github.com/sinoia/lifepowerctl.git
cd lifepowerctl
mkdir -p .cache/go-build .cache/go-mod build
make test
make build
```

The Makefile constrains Go parallelism and memory for small Pi development
hosts. Run only one Go command at a time. `make check` performs the full local
sequence: format verification, tests, vet, and build.

Pull requests are checked on GitHub-hosted runners for formatting, module
integrity, tests, vet, an ARM64 cross-build, shell quality, repository policy,
the minimum declared Go version, and races. Dependency review and CodeQL run
on pull requests; CodeQL and `govulncheck` also run on a weekly schedule. All
referenced actions are pinned to full reviewed commit SHAs.

## Local web development

The compiled binary defaults to production paths. For focused development,
tests normally create isolated temporary state. A manually launched server
can use an explicit strict JSON configuration and loopback port:

```console
./build/lifepowerctl serve -config /path/to/development-config.json \
  -listen 127.0.0.1:18081
```

Never point development commands at installed production state. The vendor
executable path is deliberately fixed; use deterministic fakes and existing
test seams rather than substituting it through configuration.

## Test layers

1. Deterministic fakes are the default for normal tests and every failure or
   hardware mutation path.
2. A development board may be used for explicitly enabled, initially read-only
   identity, telemetry, UI, and integration checks.
3. A recoverable constrained target is used for resource, SD-card, installer,
   systemd, and final acceptance evidence.

Real hardware tests are never auto-detected. Disruptive work requires the
procedure and authorization described in [AGENTS.md](../AGENTS.md).

## Release development

`make release VERSION=v0.1.0-rc.1` renders a local candidate only from a clean,
committed tree with valid version metadata. Local assets are not a public
release. Maintainer CI owns official assets, SBOM, provenance, attestations,
supported-image verification, and human-approved publication.

See [Contributing](../CONTRIBUTING.md), [Architecture](architecture.md), and
the [ADR index](decisions/README.md).
