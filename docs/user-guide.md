# User guide

LifePower Control is designed for one owner of one local Raspberry Pi. Sign-in
grants access to board changes, arbitrary owner jobs, scheduling, and power
operations; protect the password and browser session accordingly.

## Overview

The overview page reports application and board identity, current power and
battery information, and diagnostics. Treat unavailable or unsupported
identity as a stop condition for changes. Save diagnostic output only after
reviewing it for hostnames, paths, job output, or other private information.

The Host system panel can display one optional identifying image. Upload a
JPEG or PNG of at most 8 MiB and 16 megapixels; the page displays it as a
fixed framed square regardless of its original proportions. Replacing or
removing the image does not retain a previous copy. The file remains private
application state, is preserved by ordinary uninstall, and is included when
the state directory is backed up. Image metadata is retained in the stored
original and the image is not included in diagnostics or Activity.

## Register settings

The Registers page separates read-only values from a small reviewed set of
temporary changes. Each editor validates the board version, reads a fresh
value, writes through the fixed vendor command, and checks the result.

Review the before/after values and the warning shown by the editor. A lost
connection or failed read-back leaves the outcome uncertain; do not blindly
repeat the write. Inspect the board and Activity history first.

These changes are volatile. LifePower Control does not call `CFG_WRITE`, so a
power loss or board reset can return them to their flash-backed values. See
[Safety](safety.md#temporary-register-changes).

## Jobs

A saved job can use direct execution (an executable plus argument list) or an
explicit shell command. Prefer direct execution when shell parsing is not
needed. The job form shows only the fields used by the selected execution
mode; a direct executable may have no arguments. Jobs run sequentially as the
ordinary account selected during installation, with that account's existing
home and permissions.

Before saving a job:

- use absolute or predictable paths;
- make repeated execution safe where possible;
- set a realistic timeout;
- avoid commands that prompt for input;
- do not print credentials or private file contents; and
- test manually before including it in a duty cycle.

Output is bounded and may be truncated. It is stored on disk and displayed as
untrusted text. A successful process exit only proves the command's exit
status, not that its external effect was correct.

## Activity

Activity records security and operational events such as authentication,
register changes, job runs, scheduling transitions, and service actions. It is
bounded operational history, not a permanent audit archive. Export or back it
up separately if longer retention is required, and review it for private data
before sharing.

## Duty cycles

A duty cycle combines a schedule with an ordered run list. Simulate and review
the next occurrences before enabling it. Real activation may program a wake,
run jobs after a verified scheduled boot, program the next wake, and request
orderly shutdown only after every required step succeeds.

The controller intentionally remains conservative:

- jobs run one at a time;
- a job failure, runner failure, invalid clock, uncertain wake, unexpected
  boot, or persistence failure disables automation and leaves the Pi on;
- an automatic run starts only after persisted intent and the consumed wake
  agree; and
- early/manual recovery boots do not continue the automatic cycle.

Keep physical access or another recovery route available while commissioning
a schedule. Begin with simulation, then a generous powered-on window and a
non-critical job. Verify at least one full wake/run/shutdown cycle before
relying on unattended operation.

## Signing out and stopping the web interface

Sign out when finished. With on-demand mode, stop only the web service:

```console
sudo systemctl stop lifepowerctl.service
```

This does not stop the controller or runner. Never stop those services while a
duty cycle is active or a job is running merely to make maintenance proceed.
