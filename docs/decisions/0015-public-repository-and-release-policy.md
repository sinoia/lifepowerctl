# ADR-0015: Public repository and first-release policy

- Status: accepted
- Date: 2026-10-16

## Context

The private development repository contains valuable implementation history,
acceptance evidence, deployment snapshots, and operational recovery detail. It
also contains private infrastructure names and paths that have no place in a
public product repository. Removing current files would not remove those
details from Git history.

The first public release also needs a fixed repository identity, security
reporting channel, architecture scope, provenance policy, immutable publication
process, and clear authority transition without damaging the private archive.

## Decision

- The public repository identity is `github.com/sinoia/lifepowerctl` on branch
  `main`, matching the existing Go module and release templates.
- Construct a new Git repository from an explicit reviewed allowlist. Do not
  filter, graft, or publish the private history.
- Retain the existing repository and its backup as a private historical and
  operational archive. The public repository receives a distinct checkout and
  distinct backup.
- Stage the GitHub repository privately after the local publication gate, then
  make it public before running the attested release-candidate workflow.
- GitHub private vulnerability reporting is the public security channel. Do
  not publish a personal email address. Initially, only the latest published
  patch release receives fixes.
- Protect `main` and release tags with the available public-repository rules,
  require the established CI checks, block force pushes and deletion, require
  full-length commit SHAs for Actions, and keep default workflow credentials
  read-only.
- Enable immutable releases. Assemble and verify all assets in a draft, then
  publish once through a protected human approval. Correct defects with a new
  patch release rather than replacing a published asset.
- Publish ARM64 production assets only for `v0.1.0`. ARMv7 and AMD64 release
  assets remain withheld until they have an admitted need and applicable test
  evidence.
- Publish the minimal archive, exact-version installer and uninstaller,
  SHA-256 manifest, SPDX SBOM, build provenance, and immutable-release
  attestation from a clean semantic-version tag.
- Preserve the accepted owner journey of saving and inspecting the installer
  before privileged execution. Never recommend piping a network response into
  a privileged shell.
- LifePower Control owns its visual assets and guidance under the repository's
  MIT licence. No cross-project design provenance or dependency is required.

## Alternatives considered

Publishing the private history was rejected because deleting sensitive files
from the current tip would leave them in earlier commits. Filtering that
history would be difficult to prove complete and would retain large amounts of
private phase evidence with no public user value.

Creating the repository public immediately was possible, but private staging
allows the scrubbed history, documentation, and CI to be checked before
disclosure. The repository must become public before the release-candidate
attestation workflow on plans where attestations are public-repository only.

Publishing development-only AMD64 or untested ARMv7 binaries was rejected for
the first release because an available cross-build is not a support claim.

Publishing a personal email address was rejected because GitHub provides a
structured private vulnerability-reporting channel for public repositories.

## Consequences

The public initial commit will not preserve private commit ancestry. Durable
architecture and safety decisions are rewritten into public ADRs, reference
models, development guidance, project state, and roadmap documents. Detailed
host and acceptance evidence remains available only in the private archive.

The two repositories have different histories and remotes. Automation and
operator instructions must make their authority explicit so the private
history cannot be pushed to the public remote accidentally and the public
history cannot overwrite the private backup.

Release automation must support draft assembly, exact build identity, SBOM and
provenance generation, immutable publication, and ARM64-only asset naming.

## Publication verification

- Complete public-tree content, licence, privacy, secret, link, and placeholder
  scans before its first commit.
- Build and review the new local Git history before adding a public remote.
- Verify the selected GitHub settings and Actions permissions on the staged
  repository.
- Pass the complete first-release checklist and exact draft-asset acceptance
  before publishing `v0.1.0`.
