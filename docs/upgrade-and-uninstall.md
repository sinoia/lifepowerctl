# Upgrade and uninstall

Use assets from one exact immutable release and verify them as described in
[Release verification](release-verification.md). Save the installer locally;
it is also the recovery tool for an interrupted operation.

## Maintenance gate

Before upgrade, downgrade, or uninstall:

1. Disable duty-cycle automation in the web interface.
2. Wait for all jobs and lifecycle activity to finish.
3. Confirm that local status reports `maintenance=safe`.
4. Back up the preserved paths listed in [Operations](operations.md#backup).

The installer does not stop active automation or alter board state to force a
safe result. `unsafe` or `unknown` is a refusal, not a prompt to delete state.

## Upgrade

Run the newly downloaded, verified installer in the same way as a fresh
installation:

```console
sudo sh ./install.sh
```

It classifies same-version, upgrade, or downgrade; verifies the installed
layout; checks state-schema compatibility; and preserves configuration,
authentication, Activity, schedules, jobs, output, selected job identity, and
web lifecycle and port. A same-version run verifies rather than needlessly
replacing a healthy install. Supplying a different `--port` for an installation
whose metadata already records a port is refused; routine upgrades never
rewrite the preserved configuration.

The `v0.1.0` metadata predates the port field and is treated as port `8080`
when `--port` is omitted. If that installation's loopback configuration was
manually changed, pass its actual port once during the upgrade so the new
closed metadata adopts it. The installer verifies the current web health on
that port before mutation.

A downgrade is allowed only when explicitly requested by the installer flow
and when both versions can safely read the retained state. Do not replace the
binary by hand.

## Rollback and interruption

During replacement, a root-only copy of the prior payload and service state is
used for bounded rollback. A handled failure restores automatically. After an
abrupt interruption, use the same saved installer with `--recover`; see
[Recovery](recovery.md#interrupted-installation).

## Ordinary uninstall

```console
sudo sh /usr/lib/lifepowerctl/uninstall.sh
```

Ordinary uninstall removes package-owned binary, units, policy, and runtime
paths. It preserves configuration, credentials, controller state, Activity,
schedules, job definitions, runner metadata, retained output, and the accounts
and groups still needed to own them. The preserved record includes the web
port so reinstall and recovery use the same endpoint. Reinstall can recognize
this closed preserved state without resetting authentication.

## Permanent data purge

`--purge-data` is a separate destructive operation. It requires a controlling
terminal and exact typed confirmation of all three preserved roots; there is
no non-interactive shortcut:

```console
sudo sh /usr/lib/lifepowerctl/uninstall.sh --purge-data
```

Export anything needed first. Purged application data is not recoverable
through LifePower Control. The manufacturer software and board configuration
are outside the uninstaller's ownership and are never removed.
