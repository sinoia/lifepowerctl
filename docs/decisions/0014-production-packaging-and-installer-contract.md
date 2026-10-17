# ADR-0014: Production packaging and installer contract

- Status: accepted
- Date: 2026-10-13
- Amended: 2026-10-17 for configurable installation ports

## Context

The accepted application and service boundaries need a small, inspectable
installation journey suitable for constrained Raspberry Pi systems. The
public entry point is a saved exact-version GitHub release installer.

The installer temporarily runs as root, creates service identities, installs
an executable and system policy, and controls service lifecycle. It therefore
has to handle release substitution, hostile archives, unexpected existing
paths, selected-user authority, interrupted upgrades, state compatibility,
and uninstall without becoming a general system-management framework.

Raspberry Pi currently offers Debian 13 (Trixie) as its current Raspberry Pi
OS image and Debian 12 (Bookworm) as its legacy image. The application is
developed and deployed on ARM64, while ARMv7 has not received a release or
installer acceptance matrix.

## Decision

### Supported candidate and release boundary

- Raspberry Pi OS Lite 64-bit Trixie is the primary first-release candidate.
- Raspberry Pi OS Lite 64-bit Bookworm is the compatibility candidate.
- ARMv7 is withheld from `v0.1.0` unless it is explicitly admitted and passes
  its own build, clean-image, resource, and hardware matrix.
- Local rendering produces non-authoritative exact-version fixtures. Protected
  GitHub CI owns official tags, attestations, and publication.

The installer determines native Debian user-space architecture with
`dpkg --print-architecture`, cross-checks the kernel machine, and maps only
accepted combinations. It does not choose an asset from `uname -m` alone.

### Download and asset identity

The convenient documented journey saves and permits review of the installer
before root execution. Piping a download into a privileged shell is not a
supported path.

Every released installer is rendered with one exact version, commit,
repository, release base, archive filename, and checksum filename. Although
the installer itself may be obtained through GitHub's stable
`releases/latest/download/install.sh` URL, it downloads only assets under its
embedded exact tag. There is no production runtime flag or environment
variable that changes the release origin.

Network tests use a separately rendered installer whose origin is a
fixed loopback fixture. A test-only render cannot be published as a production
installer.

The release archive has a strict allowlist of top-level regular files and
bounded sizes. Absolute paths, parent traversal, duplicate entries, links,
devices, sockets, FIFOs, and unexpected members are rejected before extraction
into a root-private empty directory. The exact archive checksum must appear
once in the exact release checksum manifest. A checksum retrieved through the
same release channel is corruption and substitution detection, not independent
publisher authentication; GitHub attestations provide the separate provenance
verification path.

### Identities, paths, and web lifecycle

The installed identity and layout are:

- binary: `/usr/lib/lifepowerctl/lifepowerctl`, root-owned mode `0755`;
- configuration: `/etc/lifepowerctl/config.json`, root:`lifepowerctl` mode
  `0640`;
- controller state and Activity: `/var/lib/lifepowerctl`, private to the locked
  `lifepowerctl` system account;
- runner spool/output: `/var/lib/lifepowerctl-jobs`, selected job user and
  `lifepowerctl-jobs` shared group;
- sockets: `/run/lifepowerctl` with the accepted setgid shared-group boundary;
- power admission: `/run/lifepowerctl-power`, root:`lifepowerctl` mode `0730`,
  containing only the exclusive fixed `poweroff.request`; and
- installer transaction and compatible rollback metadata: a separate
  root-only area, never controller-owned application state.

The interactive fresh-install default enables the web service continuously so
the setup journey works immediately. The owner may explicitly choose an
on-demand web lifecycle. Reinstall and upgrade preserve that choice. The web
listener remains on `127.0.0.1`; the installer accepts an explicit
unprivileged port from `1024` through `65535` and otherwise defaults to
`8080`. It renders the same port into the listen address and external origin.

The selected port is part of the closed root-only installed, transaction,
rollback, preserved-uninstall, and recovery metadata. Collision checks,
current-service verification, target verification, rollback, uninstall, and
reinstall health checks use that recorded port. Routine lifecycle updates do
not rewrite preserved configuration or change a recorded port. Legacy
`v0.1.0` metadata has no port field and defaults to `8080`; the first upgrade
may explicitly adopt an already customized loopback port only after proving
the current endpoint healthy on that port.

### Safe lifecycle operations

Fresh install, reinstall, upgrade, downgrade, rollback, and uninstall classify
the current controller and runner state before service mutation. Upgrade,
rollback, and uninstall refuse while automation, a job, wake handling, or a
power transition is active or unverifiable. They do not write hardware or
disable automation to manufacture a serviceable state.

Same-version reinstall is primarily a verification operation. It does not
restart healthy services, rewrite unchanged files, or reset authentication.
Fresh install uses a create-if-unconfigured setup operation. Existing
credentials and sessions are never reset by install, reinstall, or upgrade.

The installed power boundary is durable package machinery: an enabled
root-owned `.path` unit consumes the fixed request and starts a static helper.
The controller and selected job user cannot modify either unit, select an
executable, argument, or delay, or acquire sudo authority. Upgrade, rollback,
uninstall, and interrupted recovery preserve or restore path-unit enablement
alongside the other package-owned unit state. The compatibility sudoers file
is intentionally empty.

Rollback is permitted only when package and state-schema compatibility are
explicit. A restored executable and unit set cannot be reported as a
successful rollback if the retained state is unreadable by that version.

Ordinary uninstall removes only manifest-owned application machinery. It
preserves configuration, credentials, controller state, Activity, schedules,
job definitions, runner metadata, and retained output, and never removes
vendor software. Destructive data purge is a separate interactive operation
that names its exact paths and requires typed confirmation; it is not a
non-interactive uninstall option.
Both manifest-owned runtime roots, `/run/lifepowerctl` and
`/run/lifepowerctl-power`, are package machinery: ordinary uninstall removes
them after the services and path watcher stop, and install or recovery
recreates and validates both through the tmpfiles policy.

## Alternatives considered

- **Publish ARMv7 immediately:** broadens apparent compatibility without the
  required image, resource, and hardware evidence.
- **Resolve latest assets at execution time:** permits the reviewed installer
  and downloaded archive to refer to different releases.
- **Allow a base-URL environment override:** simplifies tests but creates a
  production channel-redirection feature. Separately rendered fixtures keep
  that power out of the released script.
- **Use only `uname -m`:** can select a kernel architecture rather than the
  native user-space architecture that must execute the binary.
- **Extract after pathname-only listing:** misses link, duplicate, type, and
  race concerns that matter to a root process.
- **Automatically stop or disable active automation:** makes a packaging
  operation an implicit hardware and power-state workflow.
- **Reset setup on every install:** destroys an existing authentication
  boundary and sessions during otherwise routine maintenance.
- **Infer the lifecycle port from JSON with shell text matching:** cannot
  safely parse every valid strict-JSON formatting choice. Closed metadata
  keeps recovery deterministic; the explicit legacy adoption path handles an
  existing `v0.1.0` customization.
- **Allow arbitrary or privileged listener ports:** would either broaden the
  network boundary or depend on host-specific privileged-port policy. The
  installer retains loopback and the ordinary unprivileged range.
- **Delete data during uninstall:** makes a reversible package removal
  unnecessarily destructive.
- **Adopt a Debian package or installer framework now:** adds packaging and
  maintenance surface before the small verified archive flow has evidence.

## Consequences

The first public release has a deliberately narrow ARM64 support claim and a
larger disposable-image test obligation. The release renderer, binary, and
installer need a machine-stable version/schema compatibility contract.

Owners may need to disable automation and wait for work to become idle before
maintenance. This is visible friction in exchange for keeping package
operations outside the hardware state machine.

The installer must maintain an explicit owned-file manifest and transaction
record, preserve service enablement choices, and distinguish package rollback
from application-state compatibility. The uninstaller remains safe by default
but cannot promise to remove every preserved byte without the separate purge
workflow.

## Verification and release gate

Deterministic packaging tests cover exact rendered assets, hostile downloads
and archives, install/upgrade/rollback/uninstall/recovery transactions, owned
paths, preserved data, and schema compatibility. Release acceptance verifies
real accounts, groups, modes, units, empty sudo authority, sockets, path-unit
enablement, cross-account access, service resources, and lifecycle operations
on clean supported Trixie and Bookworm images. Official publication further
requires the final GitHub origin, SBOM, provenance, attestations, immutable
release settings, and human review of the exact draft assets.
