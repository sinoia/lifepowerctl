# ADR-0011: Local selected-user job-runner protocol

- Status: accepted
- Date: 2026-10-10
- Amended: 2026-10-15 to reference fixed-path power separation

## Context

The controller intentionally accepts arbitrary commands but must not execute
them with its state, I2C, or fixed power authority. ADR-0006 assigns execution
to one fixed service running as the installer-selected ordinary user. The
boundary needs a small protocol that survives controller disconnects without
accidentally running a command twice.

## Decision

### Transport and identity

The runner listens on one root-installed Unix stream socket. On every accepted
connection it reads Linux `SO_PEERCRED` and accepts only the configured
controller UID. Socket ownership is defence in depth, not caller identity.

Each connection carries exactly one request and one response. A frame is a
four-byte unsigned big-endian length followed by one UTF-8 JSON object. Version
1 frames are at most 64 KiB. Reads, writes, and idle connections have bounded
deadlines. Unknown fields, unknown versions/actions, trailing JSON values,
invalid UTF-8, and over-limit frames are rejected before execution.

### Actions and stable identity

Version 1 has only three actions:

- `start`: validate and enqueue one run;
- `status`: return the recorded state and result for one run; and
- `cancel`: request termination of a queued or running run.

Every request has a controller-generated 128-bit random run ID encoded as 32
lowercase hexadecimal characters. `start` is idempotent for that ID. Repeating
an identical accepted start returns the existing run; reusing the ID with
different content is rejected. The runner never invents a second execution
because a response was lost. `status` and `cancel` refer only to this ID.

The runner accepts at most the configured queue and runs exactly one child
process group at a time. Queue overflow rejects the new start. The controller
does not infer acceptance from a timeout; after an uncertain start response it
queries the same ID.

### Start payload

A start contains immutable execution settings:

- saved-job ID and revision for correlation;
- either direct mode with executable and argument vector, or explicit shell
  mode with command text;
- absolute working directory;
- explicitly configured environment entries;
- timeout; and
- stdout and stderr capture limits.

It cannot select a UID/GID, supplementary groups, capabilities, namespaces,
shell path, systemd unit, board operation, power action, output path, or socket
path. Direct arguments are never joined into shell text. Shell mode always uses
the one installed and UI-disclosed `/bin/bash -lc` invocation.

The runner constructs a small environment with its installed `HOME`, `USER`,
`LOGNAME`, and documented `PATH`, then adds validated user entries without
allowing those identity keys to be replaced. The selected user may access any
working directory or executable already permitted by the OS; supporting home
projects is intentional.

### Results, output, and recovery

Responses use a closed set of states: `queued`, `running`, `succeeded`,
`failed`, `timed-out`, `cancelled`, or `lost`. They report timestamps, exit
code or signal, truncation flags, and total bytes observed. Output bytes are
not returned in protocol frames. The runner chooses paths beneath its fixed
bounded spool, keeps stdout and stderr separate, drains beyond capture limits,
and atomically records run metadata.

The runner creates a new process group. Timeout or cancellation sends the
reviewed graceful signal to the group, waits the configured grace period, then
forces termination and checks for survivors. A runner restart marks any
previously recorded non-terminal run `lost`; it never automatically reruns it.
The controller treats `lost`, an unknown run after an uncertain start, and any
protocol inconsistency as a failed run that disables automatic duty cycling
and leaves the Pi on.

Runner records are not controller schedule authority. They cannot request a
wake, shutdown, or retry. The controller copies final run evidence into its
own activity/state model before making later duty-cycle decisions.

### Power requests stay separate

The runner protocol contains no power action. Only the controller may create
the exclusive fixed request accepted by ADR-0006. The root-owned runtime
directory excludes the selected job user, while the root-owned path and helper
units accept no parameters. Neither controller nor job user may modify the
units, choose a command, or obtain sudo authority from LifePower Control.

## Alternatives considered

- **Run jobs directly in the controller:** exposes controller credentials,
  state, I2C, and power authority to child processes.
- **Synchronous run-only RPC:** a lost connection leaves acceptance ambiguous
  and encourages unsafe retries.
- **Trust socket permissions alone:** another process with group access could
  impersonate the controller.
- **Send output in responses:** couples process draining to controller/socket
  availability and makes frame memory unbounded.
- **Accept arbitrary systemd or credential fields:** turns the runner into a
  privilege-management interface rather than a selected-user executor.

## Consequences

The fixed runner must maintain a small durable spool and reconcile it at
startup. Stable IDs make controller retry and restart behavior deterministic.
The selected user retains all authority they already have, including any
pre-existing I2C or sudo access documented by the installer.
