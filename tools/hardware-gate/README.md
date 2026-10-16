# Dangerous vendor fault-injection proxy

This developer tool injects faults at the fixed manufacturer CLI boundary for
an explicitly reviewed hardware acceptance procedure. It is not installed by
LifePower Control, is not a production component, and must never be enabled on
a valued or unattended host.

`vendor-cli-proxy.sh` is designed for a temporary root-owned systemd mount-
namespace overlay while the real manufacturer binary remains at a separately
protected root-owned path. A root-owned mode file selects one one-shot fault:

- `fail-absolute-write` rejects the first nonzero `RTC_WAKE_TIME` write;
- `mismatch-absolute-read` changes the first post-write read-back by one; or
- `pause-absolute-write` pauses immediately before a nonzero absolute write so
  restart recovery can be observed.

Zero-valued disarm writes pass through. Failure and mismatch modes remove
themselves when triggered; pause mode requires deliberate operator release.

Do not run the script directly as an exploratory test. A procedure must name a
recoverable target, record initial wake registers and service state, protect
the original executable, define timeout and console access, and specify exact
cleanup. Afterwards remove the overlay, copied binary, mode/state directory,
and service drop-in; restore and restart the controller; then verify the fixed
CLI path, both wake registers, automation state, and normal read-only board
access. Retain the evidence without committing host details or controller
state.
