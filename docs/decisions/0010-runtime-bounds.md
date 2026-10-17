# ADR-0010: Initial runtime and resource bounds

- Status: accepted
- Date: 2026-10-10
- Amended: 2026-10-17 for bounded host-image upload

## Context

LifePower Control accepts browser input, polls hardware, invokes vendor and
power commands, runs arbitrary user jobs, stores output and history, and runs
on Pi 3 systems with SD cards. Every resource needs a measurable initial bound.
Unrelated constants would be difficult to review and could drift between the
controller, runner, persistence, and packaging.

Maker jobs vary widely. Defaults should handle ordinary scripts without making
unbounded execution, output, process creation, or storage the implicit policy.

## Decision

One validated `internal/limits` configuration owns initial defaults and
relationships. Subsystems may expose a smaller per-operation setting but may
not exceed the applicable maximum. Values become user-configurable only when a
safe UI and persistence rule exists; until then they are reviewed constants.
The controller applies its Go soft-memory target during service startup; the
larger systemd memory limit remains the hard containment boundary.

### HTTP and authentication

| Resource | Initial bound |
| --- | ---: |
| Read header / whole request | 5 s / 15 s |
| Response write / idle connection | 30 s / 60 s |
| Graceful server shutdown | 5 s |
| Headers / request body | 16 KiB / 64 KiB |
| Host-image upload / decoded pixels | 8 MiB / 16 megapixels |
| Open connections / concurrent handlers | 32 / 16 |
| Active sessions | 16 |
| Session idle / absolute lifetime | 12 hours / 7 days |
| First-run setup token | 30 minutes |

The ordinary body limit covers forms and job definitions. The authenticated
host-image endpoint has its own larger streaming cap plus bounded multipart
overhead and accepts only decoded JPEG or PNG input within the pixel limit.
Handlers apply the applicable body cap before form, JSON, or multipart parsing.
Expensive handlers use their own concurrency gate in addition to the connection
limit.

### Internal commands and polling

| Resource | Initial bound |
| --- | ---: |
| Vendor CLI | 3 s; 4 KiB stdout and 4 KiB stderr |
| Fixed power command | 10 s; 4 KiB stdout and 4 KiB stderr |
| Normal board poll | every 10 s |
| Minimum board/browser poll | 2 s |
| Telemetry considered stale | 30 s |
| Browser progressive-enhancement poll | every 10 s |

Polling never writes durable state or activity merely because a value was
sampled. Hardware coordination may delay a poll; polling does not delay a
waiting mutation.

### Jobs and output

| Resource | Initial bound |
| --- | ---: |
| Default / maximum job runtime | 15 minutes / 24 hours |
| Grace before forced termination | 5 s |
| Default stdout / stderr capture | 1 MiB each |
| Maximum selectable capture | 8 MiB per stream |
| Command or shell text | 16 KiB |
| Argument count / aggregate argument bytes | 128 / 16 KiB |
| Environment entries / encoded bytes | 64 / 32 KiB |
| Runner frame / identifier / path | 64 KiB / 128 B / 4 KiB |
| Concurrent jobs / queued requests | 1 / 8 |
| Retained runs | 20 per job, 200 total |
| Total retained job output | 64 MiB |

Reaching an output capture limit does not block the child process: the runner
continues draining and discarding excess bytes, marks the stream truncated, and
reports total bytes observed. Stdout and stderr remain separate. Retention is
enforced after each completed run and during startup recovery. Automatic and
manual jobs share the single global execution slot on constrained systems.

Jobs cannot disable their timeout. A maker may select a longer timeout up to 24
hours. Work intended to remain permanently resident belongs in its own reviewed
system service rather than a duty-cycle job.

### Persistence and service containment

| Resource | Initial bound |
| --- | ---: |
| Aggregate controller snapshot | 256 KiB |
| Activity | 16 × 64 KiB segments; 8 KiB per event |
| Always-on controller systemd `MemoryMax` | 128 MiB |
| On-demand web systemd `MemoryMax` | 128 MiB |
| Job-runner tree systemd `MemoryMax` | 256 MiB |
| Job-runner tree systemd `TasksMax` | 64 |
| Go controller soft memory target | 64 MiB |
| Go web soft memory target | 64 MiB |

ADR-0012 adds stricter Pi 3A+ engineering targets below these containment
ceilings: below 20 MiB idle for each controller/web process and below 50 MiB
combined during ordinary administration. The limits do not reserve memory.

The runner limits apply to the selected-user runner service and all of its job
descendants. They are containment defaults, not promises that every possible
maker program will fit. Installation may offer a clearly documented override
for capable hosts, but the Pi 3 profile stays conservative.

### Failure behavior

- Limit rejection occurs before starting an operation whenever possible.
- Vendor/power timeout is a typed external-command failure; uncertain hardware
  outcomes are not retried blindly.
- Job timeout sends the process group a graceful termination request, waits the
  bounded grace period, then forces termination and records surviving-process
  checks.
- Queue overflow rejects the new request visibly rather than growing memory.
- Storage retention never deletes an active run or the controller snapshot.
- Resource exhaustion, truncation, timeout, and cleanup failure are visible in
  results and activity without storing discarded content.

## Alternatives considered

- **Unlimited user jobs because the owner controls the Pi:** one accidental
  verbose or stuck script can exhaust storage, memory, or a duty cycle.
- **Parallel manual and automatic jobs:** makes resource use and shutdown
  ownership unpredictable on Pi 3.
- **One large combined output stream:** loses stdout/stderr meaning and makes
  truncation order difficult to explain.
- **Persist every telemetry poll:** creates unnecessary SD-card wear and
  unbounded history.
- **Pi 5-sized defaults:** development hardware would conceal failures on the
  supported constrained deployment target.

## Consequences

Some legitimate long-running or highly verbose jobs require an explicit larger
setting, and programs needing more than the runner containment profile need
administrator configuration. The ordinary UI remains simple because defaults
work without exposing every limit during first-run setup.

Central validation provides executable documentation and prevents unsafe
relationships such as parallel jobs, default timeouts above maxima, activity
records larger than segments, or session idle life exceeding absolute life.
