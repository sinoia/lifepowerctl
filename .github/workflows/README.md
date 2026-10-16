# GitHub Actions policy

Workflow files in this directory are part of the release and supply-chain
boundary. Third-party actions must be pinned to full reviewed commit SHAs,
job permissions must be explicit and least-privilege, and pull-request checks
must not receive release publication authority or repository secrets.

The checked-in workflows currently provide:

- `ci.yml`: formatting, module integrity, tests, vet, an ARM64 build, shell
  checks, repository-policy checks, minimum-Go coverage, and the race detector;
- `dependency-review.yml`: review of dependency changes on pull requests;
- `codeql.yml`: CodeQL analysis on changes and a weekly schedule; and
- `security.yml`: pinned `govulncheck` analysis on changes and weekly.
- `release.yml`: an exact semantic-version tag build, SPDX SBOM, provenance
  and SBOM attestations, immutable workflow artifact, and protected draft
  release assembly.

These checks use hosted runners and must remain hardware-independent. They
must never discover or access a board. Local development commands retain the
more conservative Pi resource limits documented in `AGENTS.md`; hosted jobs
may run the deeper checks that are intentionally omitted from small systems.

Release workflows build only from an approved committed tag, produce ARM64
assets, SHA-256 checksums, SPDX SBOM, provenance, and artifact attestations,
then assemble a draft for human acceptance. Publication uses a protected
environment and immutable release settings. No workflow may publish from a
pull request or uncommitted/local source.

The release workflow separates repository-controlled build steps from the job
holding `contents: write`. Its draft-assembly job requires the protected
`release` environment, refuses an existing release, and never publishes one.
A newly added workflow is not considered proven until it has run successfully
on GitHub.
