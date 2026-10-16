# ADR-0001: Repository identity and authority

- Status: superseded by ADR-0015
- Date: 2026-10-09
- Superseded: 2026-10-16

## Context

The Go module, public source location, security-reporting route, release
authority, and backup destinations must not disagree. Early development used a
private repository while ownership, licensing, and publication boundaries were
being established.

## Decision

The stable module and public repository identity is
`github.com/sinoia/lifepowerctl`, with `main` as its default branch. Published
source, tags, CI, attestations, and releases are authoritative only in that
GitHub repository. Security reports use GitHub private vulnerability
reporting, not a personal address.

Private development history and operational evidence are retained separately
and are not Git remotes of a public working copy. Backups likewise remain
separate from public distribution authority.

ADR-0015 owns the clean-history transition, remote separation, immutable
release policy, architecture scope, and provenance requirements.

## Consequences

Documentation, module metadata, release URLs, provenance, and attestations use
one public identity. A public clone contains no private history or configured
private backup remote. Maintainers must verify the working repository and
remote before pushing or publishing.
