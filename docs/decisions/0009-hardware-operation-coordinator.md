# ADR-0009: One serialized hardware-operation coordinator

- Status: accepted
- Date: 2026-10-10

## Context

Register changes, wake programming, flash commits, shutdown preparation, and
telemetry reads all reach the board through separate vendor CLI processes. The
vendor library locks `/dev/i2c-1` for one transaction, but that lock does not
protect a multi-command workflow from application reads, another application
operation, the vendor daemon, or an external local user.

A write is not successful until fresh validation, write, read-back, persistence,
and activity evidence complete. Shutdown must not begin while board work is in
progress, and no new board work may begin after a power request is committed.

## Decision

One process-wide coordinator owns every LifePower Control board operation:

- telemetry and identity reads;
- temporary register changes;
- wake disarm/program/read-back;
- flash preparation and commit;
- boot-classification reads and safe-mode disarm; and
- the transition into shutdown or reboot.

The initial implementation serializes reads as well as writes. A context-aware
lease may span several fixed vendor CLI invocations and persistence steps, so
no application read can interleave with a mutation sequence. This intentionally
forgoes read concurrency for a small, predictable safety boundary; coalesced
reads can be added later behind the same coordinator if measurement justifies
them.

Ordinary operations use a callback wrapper that defers lease release, including
during panic unwinding. Code that acquires a raw ordinary lease must defer its
release immediately. Shutdown cannot use that wrapper: its lease must end in
an explicit commit or abort so a panic or forgotten cleanup cannot
accidentally reopen hardware access after a power transition has begun.

Every acquisition has an operation kind, human-readable subject, and start
time for busy UI and diagnostics. Waiting is bounded by the caller's context.
The coordinator does not retry hardware commands implicitly.

### Mutation sequence

```text
acquire exclusive lease
  -> read identity and affected state
  -> validate fresh state and ownership
  -> perform fixed write(s) in reviewed order
  -> read back every affected value
  -> persist result and append activity evidence
  -> release lease
```

Failure at any stage records the exact completed prefix. A lost connection or
read-back mismatch is an uncertain outcome, not a failed-before-change result.
Rollback is operation-specific and occurs only while the same lease remains
held.

### Power transition

Shutdown/reboot waits for the current lease, acquires a shutdown lease, and
quiesces the coordinator. It then rechecks the persisted lifecycle and wake
verification before invoking the fixed systemd request. A successful request
commits quiescence so future acquisitions fail. If the request itself fails,
the caller aborts the shutdown lease, reopens ordinary access, disables
automation as required, records the failure, and leaves the Pi running.

Code must defer abort immediately after acquiring the shutdown lease. Commit
and abort share one-time resolution, so a successful commit makes the deferred
abort a no-op while any pre-commit return or panic reopens the coordinator.
Merely losing a shutdown lease is not a recovery strategy.

### External access

The coordinator controls only LifePower Control. Before every mutation it uses
fresh reads and compares expected values to detect vendor-daemon or user CLI
changes. Read-back detects interference during the workflow where possible.
It cannot prevent an authorized local user from changing hardware immediately
after verification.

Controller-owned wake registers are not exposed through the generic editor.
Daemon-owned registers are not changed through generic workflows. Hardware
tests must decide whether any future operation requires explicitly stopping or
otherwise coordinating with the vendor daemon.

## Alternatives considered

- **Rely on `/dev/i2c-1` flock:** protects one file descriptor transaction, not
  validation/write/read-back sequences.
- **One mutex per register/module:** permits unsafe interleaving between wake,
  flash, register, and shutdown workflows.
- **Concurrent reads during writes:** improves polling latency but can observe
  and publish an intermediate multi-register state.
- **Queue automatic retries:** can repeat an action whose first outcome is
  unknown.
- **Hold a lock across shutdown forever without failure recovery:** a failed
  power request would leave the application unnecessarily unusable.

## Consequences

Long board operations temporarily delay telemetry. The UI can show the active
operation rather than presenting stale controls. Hardware throughput is not a
goal; bounded, explainable ordering is.

The coordinator is an in-process safety boundary, not an OS-wide I2C lock.
Documentation and activity records must remain honest about external mutation.
