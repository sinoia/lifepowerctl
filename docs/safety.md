# Safety

LifePower Control can affect a computer's power source, execute owner commands,
program board wake state, and request shutdown. It is not safety-certified and
must not be used where failure could endanger people or critical equipment.

Its governing rule is: missing, inconsistent, stale, unauthenticated, or
unverified evidence never authorizes a hardware write, automatic job, wake,
shutdown, or reboot.

## Temporary register changes

Only registers admitted by the exact supported board catalogue are editable.
General-purpose editing excludes flash commit, power controls, wake fields,
connectivity-critical controls, and other registers that need dedicated
workflows. Each accepted operation reads fresh state and checks the result.

The application never calls `CFG_WRITE`. Changes made in the web interface are
temporary and can be lost when the board resets or loses power. The board may
then reload different flash-backed values. Record the previous value before a
change and verify the effective value after any reboot or power event.

If a command times out, communication is lost, or read-back differs, the
outcome is uncertain. Do not retry blindly. Keep the Pi powered, inspect the
board directly, and use Activity plus service logs to establish what happened.

## Wake, duty cycle, and shutdown

Enabling a real duty cycle is a physical power procedure. Before activation:

- confirm the system clock and schedule;
- simulate upcoming occurrences;
- ensure every job is repeatable and bounded;
- arrange physical or remote recovery access;
- use a non-critical workload for the first cycle; and
- understand the board's button and manual-boot behavior.

The controller writes and verifies wake state only within its serialized
hardware operation. It asks a fixed root-owned systemd helper for orderly
poweroff only after state is persisted and preceding work succeeds. Errors
leave the machine on and disable automation for review.

## Jobs

Jobs execute with the selected user's existing Linux permissions. LifePower
Control adds no sudo, I2C, or power permission, but it also cannot remove
authority the user already has. A job with sudo or direct hardware access can
bypass application safeguards. Treat shell jobs and scripts as trusted code.

The runner bounds concurrency, time, process count, memory, descriptors, and
retained output, but these are operational limits—not a security sandbox.

## Manual and external changes

The manufacturer CLI, daemon, root, or another I2C process can change the board
outside LifePower Control. The application's coordinator serializes its own
operations only. Avoid concurrent manual board commands, and re-check current
state after any external action.

## Recovery posture

Keep current OS and application backups, the exact installed release assets,
and a way to access the Pi without the web service. Never delete state or
installer metadata to silence a safety refusal. Follow the precise recovery
message and [Recovery](recovery.md).
