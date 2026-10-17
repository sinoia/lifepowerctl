# Changelog

All notable user-visible changes will be documented here. The project follows
[Semantic Versioning](https://semver.org/) for published releases.

## Unreleased

No user-visible changes since `v0.3.0`.

## v0.3.0 - 2026-10-17

- Added execution-mode-aware saved-job and ad-hoc forms that show only the
  direct-program or shell-command fields used by the selected mode.
- Added an optional bounded host image to the Overview page, with private
  atomic storage and authenticated upload, replacement, and removal.
- Refined UI terminology across the Overview, Registers, Jobs, and Duty Cycle
  pages.

## v0.2.0 - 2026-10-17

- Added installer-selected unprivileged loopback web ports while retaining
  port `8080` as the default.
- Added closed port metadata and port-aware health, recovery, rollback,
  uninstall, and compatible `v0.1.0` adoption behavior.

## v0.1.0 - 2026-10-17

- Initial public release of the local LifePower Control application.
- Added guarded board telemetry and temporary register operations, bounded
  owner jobs, duty-cycle coordination, Activity history, and local web
  administration.
- Added the exact ARM64 package, transactional installer and rollback,
  data-preserving uninstall, systemd integration, owner documentation,
  checksums, SPDX SBOM, and provenance attestations.
