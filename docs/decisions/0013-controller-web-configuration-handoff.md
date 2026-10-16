# ADR-0013: Controller-owned state and bounded web handoff

- Status: accepted
- Date: 2026-10-11

## Context

ADR-0012 requires the duty-cycle controller to remain useful while the web
application is stopped. The existing `serve` process owns both the HTTP server
and the aggregate controller snapshot. Allowing a new controller process and
the web process to open that snapshot independently would defeat its single
writer and optimistic-revision guarantees.

The web process needs to display controller health and submit owner-reviewed
configuration changes, but it must not become an alternative automation
authority. A dropped connection must not make proposal acceptance ambiguous or
cause the web process to repeat a state change blindly.

## Decision

The always-on controller is the sole process that opens and writes the
aggregate controller snapshot. It holds the existing state-directory lock for
its lifetime. Offline administrative commands continue to use the same lock.

The controller exposes a fixed Unix socket at
`/run/lifepowerctl/controller.sock`. Communication uses one bounded,
length-prefixed, versioned JSON request and response per connection. Unknown
fields, versions, actions, trailing data, invalid UTF-8, and frames over 64 KiB
are rejected. The controller checks Linux peer credentials in addition to
filesystem permissions.

The protocol includes a read-only health handshake returning the authoritative
aggregate revision and whether state was recovered from the previous snapshot.
Its first typed mutation saves a schedule and run list while automation remains
disabled; a separate proposal-status action resolves uncertain replies. A
read-only preview action uses controller time, and the enable action verifies
the reviewed preview, recalculates from fresh controller
time, and requires another confirmation if the first occurrence changes. The
protocol does not accept an opaque JSON configuration document.

Every future configuration proposal must contain:

- a stable proposal ID used for idempotent status lookup;
- the exact aggregate base revision reviewed by the owner;
- typed schedule, run-list, and referenced job revisions;
- an explicit action such as save-disabled, enable, or disable; and
- the preview identity when enablement depends on a reviewed occurrence.

The controller validates the proposal again against fresh authoritative state.
It either durably commits it and returns the new revision, rejects it without a
state change, or allows its stable ID to be queried after an uncertain
connection result. The web process never infers acceptance from a lost reply.

During migration, the existing combined `serve` mode remains the only way to
use the current UI. It retains its existing exclusive lock. The new
`controller` mode proves the process and protocol boundary but is not packaged
to run concurrently with `serve` until saved-job and configuration mutations
have moved behind the controller interface. This prevents two snapshot writers
from existing temporarily.

## Alternatives considered

- **Both processes write the snapshot:** file replacement is atomic, but
  independent in-memory revisions and recovery make this unsafe.
- **An atomic proposal directory only:** workable, but acknowledgement,
  controller health, and uncertain-response lookup still require a protocol.
- **Opaque configuration blobs:** keeps the protocol superficially generic but
  weakens validation and makes compatibility and ownership difficult to audit.
- **A general HTTP API:** adds unnecessary parsing and server surface to the
  minimal always-on process.

## Consequences

Controller state has one durable owner and the web lifecycle can eventually be
independent. The local protocol remains small and dependency-free. Migrating
existing saved-job and run operations is required before separate production
units can replace the combined service.

Protocol changes require compatibility tests. Socket permissions and peer-UID
rejection must be verified on the deployment image. Web unavailability cannot
pause controller work, and controller unavailability must be shown as such
rather than treated as inactive automation.
