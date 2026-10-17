# Architecture

LifePower Control separates the always-on safety controller, optional web
administration, owner job execution, and root power action.

```text
Browser over loopback/SSH (or reviewed HTTPS proxy)
        |
        v
web service: authentication, UI, read-only board views
        | bounded authenticated Unix-socket requests
        v
controller: authoritative state, scheduling, board writes, Activity
        |                         |
        | peer-authenticated      | fixed vendor CLI and /dev/i2c-1
        v                         v
selected-user job runner       LiFePO4wered/Pi+
        |
        v
owner job process groups

controller -- exclusive fixed request --> root-owned systemd poweroff helper
```

The locked `lifepowerctl` account owns authentication and controller state.
The selected ordinary user owns the runner and job output. A narrowly scoped
shared group permits the controller to reach the runner socket and read output
without exposing controller secrets to jobs. The web process shares the
locked service identity but accesses authoritative automation through the
controller protocol rather than writing its snapshot.

## State and failure model

Durable JSON envelopes use versioned schemas, bounded reads, atomic synced
replacement, and a previous snapshot. Critical transitions persist intent
before external action. Startup reconciliation never assumes that an
interrupted operation failed before changing the outside world.

Board access is serialized by a process-visible coordinator. Exact product
identity selects a closed register catalogue. Writes validate a purpose-
specific constraint, operate through a fixed absolute executable without a
shell, and require fresh read-back.

Duty-cycle execution is a persisted state machine. Only a previously armed and
then consumed expected wake can authorize automatic jobs. Jobs run in order;
the next wake and fixed poweroff request follow only complete success.

## Resource model

All inputs, HTTP bodies, uploaded host images, vendor output, state, Activity
segments, job queues, processes, runtime, and retained stdout/stderr have
explicit bounds. Services have systemd memory, task, descriptor, filesystem,
device, and address-family limits appropriate to their responsibilities. The
design favors standard Go, small static assets, and no client build toolchain.

The accepted detailed contracts live in `docs/decisions/` and the behavioral
models in `docs/reference/`.
