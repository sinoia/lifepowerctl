# Project state

Last updated: 2026-10-17

[`v0.1.0`](https://github.com/sinoia/lifepowerctl/releases/tag/v0.1.0) is the
first supported public release. Its immutable ARM64 assets were built from
commit `fed43f90c744346062a371c5a792da448370f0ab` by the protected release
workflow. Hosted validation, checksums, SPDX SBOM, attestations, independent
asset verification, exact target acceptance, and anonymous exact-version and
latest-release download checks pass for the declared Trixie and legacy
Bookworm scope.

The public `main` branch is the development authority. First-release work is
complete; intended later work is tracked in the [roadmap](roadmap.md).

## Implemented

- Read-only board identity, telemetry, diagnostics, and a reviewed register
  catalogue.
- Purpose-specific temporary register changes with validation, serialization,
  read-back, Activity records, and uncertain-outcome handling.
- Single-owner setup, Argon2id password authentication, bounded sessions,
  CSRF/origin protections, rate limiting, and local reset.
- Crash-safe bounded controller state and segmented Activity storage.
- Direct and explicit-shell saved jobs, selected-user execution, sequential
  runs, process-group termination, bounded output, and recovery observation.
- Duty-cycle schedule simulation, lifecycle persistence, conservative boot
  classification, verified wake programming, ordered jobs, and fixed orderly
  poweroff requests.
- Separate controller, web, runner, and root power-helper boundaries under
  hardened systemd units.
- Exact-version ARM64 archive, transactional installer, compatible upgrade and
  rollback, interrupted-operation recovery, data-preserving uninstall, and
  separately confirmed purge.

## Supported first-release scope

- LiFePO4wered/Pi+ with the standard manufacturer daemon and CLI.
- Raspberry Pi OS Lite 64-bit Trixie, with 64-bit Bookworm retained as a legacy
  compatibility target.
- ARM64 production release assets only.
- Single local owner and one explicitly selected ordinary job account.
- Loopback web access locally or through SSH; an explicitly reviewed HTTPS
  proxy configuration is possible but direct Internet exposure is unsupported.

## Known limits

- No board-flash persistence. The application never invokes `CFG_WRITE`.
- No ARMv7/32-bit release asset or support claim.
- No generic Debian, container, alternate-init, or alternate-UPS support.
- Jobs inherit the selected user's existing authority and are not isolated
  from that user by a hostile-code sandbox.
- Activity and job output are deliberately bounded rather than permanent.
- External vendor CLI/daemon or root actions cannot be serialized by the
  application's in-process coordinator.
- Draft releases, development builds, and locally rendered assets are not
  supported production downloads.

## Durable invariants

Unknown or unverifiable state never authorizes hardware or power action.
Hardware changes require a supported exact identity and read-back. State is
persisted before external action. Jobs complete successfully before the next
wake or shutdown. Failure disables automation and leaves the Pi on. The web
service is not required for the always-on controller to complete an accepted
duty cycle.

Future sessions should update this document when implemented or supported
scope changes; use [Roadmap](roadmap.md) for intended work and ADRs for durable
design decisions.
