# Release asset contract

This directory defines the release-asset contract used by local candidate
builds and the official GitHub release workflow. It contains source templates,
not published or directly executable release assets.

For version `v0.2.0`, the ARM64 asset set is:

```text
lifepowerctl_0.2.0_linux_arm64.tar.gz
install.sh
uninstall.sh
SBOM.spdx.json
checksums.txt
```

`checksums.txt` contains exactly one SHA-256 entry for the archive, installer,
uninstaller, and SPDX SBOM. It does not contain its own checksum. The release
installer is rendered with the exact version, commit, archive name, and
exact-tag release base; it never resolves the latest release during execution.

## Operator journey

An official release supplies the exact-tag URL, checksums, provenance, and
attestation. Preserve this save, verify, inspect, then execute journey:

```console
wget -O install.sh "https://github.com/sinoia/lifepowerctl/releases/download/v0.2.0/install.sh"
chmod 0600 install.sh
sha256sum install.sh
sh -n install.sh
sh ./install.sh --release-info
less install.sh
sudo sh ./install.sh
```

Use these URLs only when GitHub shows `v0.2.0` as a published release. Later
releases replace that tag with their exact immutable version. The operator
compares the first checksum with the independently obtained published `checksums.txt`
before execution and verifies the GitHub attestation. Interactive installation displays
the fixed release origin, detected platform and board prerequisite, selected
job identity, web lifecycle, and mutation summary before confirmation. For
automation, both choices must be explicit:

```console
sudo sh ./install.sh --non-interactive --job-user pi --web=enabled --port 18081
```

Use `--web=on-demand` instead only when the owner wants the controller and
runner enabled continuously but intends to start web administration manually:

```console
sudo systemctl start lifepowerctl.service
sudo systemctl stop lifepowerctl.service
```

The web listener remains on loopback. Port `8080` is the default; `--port`
accepts an unprivileged port from `1024` through `65535`. From the browser
workstation, use the selected installation port on both sides of the tunnel:

```console
ssh -N -L 18081:127.0.0.1:18081 pi@raspberrypi.local
```

Local bounded status and ordinary data-preserving uninstall are:

```console
sudo -u lifepowerctl /usr/lib/lifepowerctl/lifepowerctl \
  admin status -config /etc/lifepowerctl/config.json
sudo sh /usr/lib/lifepowerctl/uninstall.sh
```

Never improvise recovery. An interrupted install is recovered with the exact
same saved installer and an interrupted uninstall with the root-only saved
uninstaller named by its error message:

```console
sudo sh ./install.sh --recover
sudo sh /var/lib/lifepowerctl-installer/uninstall-previous/uninstall.sh --recover
```

## Archive contract

The gzip-compressed ustar archive has one version-independent top-level
directory and this exact member allowlist:

```text
lifepowerctl/
lifepowerctl/LICENSE
lifepowerctl/README.md
lifepowerctl/lifepowerctl
lifepowerctl/release-manifest.txt
```

The directory is the only directory member. Every other member is a regular
file. Links, devices, FIFOs, sockets, duplicate names, absolute paths, parent
traversal, and additional members are invalid. The initial maximum compressed
archive size is 32 MiB; the executable is limited to 24 MiB and each included
text file to 1 MiB. These are verification bounds, not retention targets.

`release-manifest.txt` is informational and uses a closed, line-oriented
`name=value` format. The staged binary's `version --machine` output is the
authority for embedded build and state-schema compatibility information; the
renderer compares it with the requested release before archiving.

## Local renderer

From a clean tracked working tree:

```console
make release VERSION=v0.2.0-rc.1
```

The renderer performs one constrained ARM64 Go build, refuses development or
dirty metadata, uses normalized archive ownership/time/order, and writes a new
version directory under ignored `dist/`. It refuses to replace an existing
output directory. The intentional untracked `.review/` tree is excluded from
the source-cleanliness decision and is never included in an asset.

The local renderer creates the archive, installer, uninstaller, and their
initial checksum manifest. The official tag workflow additionally requires
that the exact tag resolve to `HEAD`, generates `SBOM.spdx.json` from the
archive contents with a pinned and checksum-verified Syft release, and
regenerates the final checksum manifest over all four downloadable payloads.
It attests the payloads, adds an SBOM attestation for the archive, transfers an
immutable workflow artifact to a separately permissioned job, and creates a
draft release behind the protected `release` environment. It cannot publish
the draft or replace an existing release.

The rendered fresh installer embeds the reviewed config, systemd, tmpfiles,
empty compatibility sudoers file, and owned-layout policy. It performs all
preflight and exact-asset validation before its first system mutation, creates the initial setup link on
the controlling terminal, and rolls back an incomplete fresh installation.
Each fresh-install mutation boundary replaces a root-only, exact-release
transaction marker atomically. A caught failure or signal attempts rollback;
an abrupt interruption is recovered only by the same saved installer with
`sudo sh ./install.sh --recover`. Recovery parses rather than executes the
marker, validates its exact fields, release binding, destination types,
account shapes, and private temporary directory, and refuses completed,
foreign, or tampered state. The marker is retained until cleanup and the
systemd reload succeed, so a failed recovery has one idempotent retry path.

An installed record classifies a later exact installer as same-version,
upgrade, or downgrade. Same-version is a read-only verification and exits
before staging. Upgrade and explicitly allowed downgrade require healthy,
maintenance-safe local status, exact existing identities and paths, and
two-way durable-schema compatibility. They preserve configuration and all
application/job data, retain the selected job identity, web lifecycle, and
loopback port, and replace only package machinery. Format-1 `v0.1.0` metadata
defaults to port `8080`; an explicit `--port` lets the first upgrade adopt an
already customized loopback port after its health check succeeds. Format-2
records make the port mandatory and immutable during routine verification and
updates. A root-only previous-payload directory and lifecycle transaction
restore the prior payload, units, policy, installed record, enablement, port,
and prior web activity after failure or `--recover`.

The installed uninstaller is release-bound and validates the exact installed
record, account/group shape, owned-file manifest, package paths, local service
health, idle jobs, inactive lifecycle, disabled/absent automation, and safe
maintenance state before mutation. Ordinary uninstall uses a root-only closed
backup and transaction, removes only package machinery, and retains config,
authentication, controller state, Activity, jobs, output, and the identities
that still own those paths. A caught failure restores automatically; after an
abrupt interruption the saved backup uninstaller provides `--recover`.

Successful uninstall leaves one root-only `preserved` record containing the
selected job user, web lifecycle, web port, previous release identity, and
durable schema.
The installer recognizes only that closed state, checks target schema
compatibility, and can reinstall without replacing preserved data or creating
new credentials. Its reinstall transaction returns to the safely uninstalled
state after failure or `--recover`.

`--purge-data` is separate from ordinary uninstall. It requires an interactive
terminal and the exact typed list of the three data roots, refuses symlinks or
non-file entries within them, and has no non-interactive shortcut. Destructive
purge testing is confined to disposable fixture paths.
