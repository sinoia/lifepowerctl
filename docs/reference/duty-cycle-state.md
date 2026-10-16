# Duty-cycle lifecycle state review

This document defines the conservative lifecycle for the controller-owned,
singleton duty cycle. It complements the narrower
[boot-classification review](boot-classification-state.md). The names describe
durable controller state, not transient UI progress.

## Safety invariants

- Saving a schedule, saving a job, or using **Run now** never arms a wake or
  requests shutdown.
- **Enable duty cycle** is the single deliberate action that starts automatic
  operation. It validates the complete configuration, arms the first future
  occurrence, verifies it, persists the evidence, and then requests shutdown.
- Only an expected timed boot may start the automatic run list.
- An occurrence is consumed once. Persisting `expected-boot-observed` before
  launching its first job prevents a restart from running it again.
- Jobs run sequentially from immutable snapshots of the enabled schedule and
  run-list revisions for that occurrence.
- Every enabled job must succeed before another wake can be armed.
- Shutdown requires a freshly read-back wake and durable
  `shutdown-requested` evidence for the same occurrence and revisions.
- No uncertain state is retried or interpreted as success. Recovery disables
  automation, attempts to disarm both wake registers, and leaves the Pi on.

## Nominal lifecycle

```text
inactive
  | owner selects Enable duty cycle
  v
validating activation
  | schedule, run list, job definitions, board identity and RTC are valid
  v
next-wake-calculated
  | acquire global hardware-operation lease
  v
wake-programming
  | WAKE_TIME := 0; verify 0
  | RTC_WAKE_TIME := intended absolute time; read back exact value
  v
wake-verified
  | atomically persist occurrence, revisions, intended wake and read-back
  v
shutdown-requested
  | quiesce hardware coordinator; recheck durable evidence
  | invoke the fixed orderly-shutdown boundary
  v
powered off
  | board consumes RTC_WAKE_TIME and powers the Pi
  v
startup-classification
  | exact consumed-wake evidence inside the accepted window
  v
expected-boot-observed          (persist before starting any job)
  v
preparing-run
  | load the stored occurrence snapshots and validate them
  v
running-job[0]
  | success -> running-job[1] -> ... -> success
  v
run-list-succeeded
  | calculate the next occurrence from a fresh board-clock sample
  v
next-wake-calculated -> wake-programming -> wake-verified
  -> shutdown-requested -> powered off
```

The schedule remains logically enabled throughout the nominal loop. The
durable occurrence identifier changes for each intended wake. Activity records
may lag the aggregate snapshot after a crash and therefore provide evidence,
but never authority, for a transition.

## Activation and schedule calculation

Enabling is a single user-facing operation, although its safe completion has
several internal states. Before any board write it requires:

- a supported board/register set and readable RTC;
- one valid singleton schedule;
- an ordered run list containing at least one enabled, valid saved job;
- a selected non-root job user and available job-runner boundary;
- no running manual job and no other duty-cycle transition;
- revisions for the schedule, run list, and every referenced job; and
- a representable future absolute RTC timestamp.

A fixed wall-clock schedule selects the next future civil-time occurrence and
skips missed occurrences; it does not replay them. A delay-after-completion
schedule adds its delay to a fresh board-clock sample after the successful run
list. For initial activation, it adds the delay to the activation-time board
sample. The UI shows the resulting absolute next occurrence before final
confirmation.

The exact recurrence choices, quiet-period filtering, timezone handling, and
preview rules are defined in the
[duty-cycle schedule specification](schedule-specification.md).

The minimum safe programming lead time and civil-time/RTC conversion rules
remain hardware-test inputs. If calculation cannot produce a safely future,
32-bit board timestamp, activation or continuation fails before a write.

Saving changes while a duty cycle is armed must not silently change an owned
occurrence. The initial implementation requires the owner to disable the duty
cycle, edit it, and explicitly enable it again. Manual job execution continues
to use the one global job slot and has no scheduling side effects.

## Conservative exits

Every failure transition below ends in `manual-safe`, with the schedule
disabled and the Pi left running:

```text
startup-classification
  | inactive intent
  +--> inactive (no automatic work; remain on)
  |
  | early/manual or ambiguous evidence
  +--> manual-safe

validating activation / next-wake-calculated
  | invalid, stale, conflicting or unrepresentable configuration
  +--> manual-safe (no wake write if none was attempted)

wake-programming / wake-verified
  | command failure, timeout, mismatch, interference or persistence failure
  +--> manual-safe

preparing-run / running-job[n]
  | missing revision, launch failure, non-zero exit, timeout, cancellation,
  | output/storage failure that prevents a trustworthy result, or restart
  +--> manual-safe; remaining jobs are recorded as not run

run-list-succeeded / next-wake-calculated
  | clock read or calculation failure
  +--> manual-safe

shutdown-requested
  | power request rejected, times out, or returns unexpectedly
  +--> abort coordinator quiescence -> manual-safe
```

Entering `manual-safe` first atomically disables automation and records the
reason. It then uses the global hardware coordinator to attempt
`WAKE_TIME := 0` and `RTC_WAKE_TIME := 0`, reads both back, and records either
verified or uncertain disarm. A disarm failure never re-enables automation.

## Restart reconciliation

At service startup, durable state is reconciled before jobs or power actions:

| Persisted phase | Startup result |
| --- | --- |
| `inactive` | Remain on; no automatic work. |
| `shutdown-requested` | Run the strict board wake classifier. Only `expected` may continue. |
| `expected-boot-observed` | Enter manual-safe; the occurrence may already have started. Do not rerun it. |
| `preparing-run` or `running-job` | Recover/terminate any runner process, then enter manual-safe. Never advance or rerun automatically. |
| `run-list-succeeded`, `next-wake-calculated`, `wake-programming`, or `wake-verified` | Enter manual-safe; completion or wake ownership is not proven. |
| `manual-safe` | Remain on and retry no automatic action. Surface any uncertain disarm. |
| Missing, corrupt, future-schema, or unknown state | Not ready/manual-safe; remain on without guessing. |

`wake-verified` alone deliberately cannot authorize shutdown after a restart.
The controller must make the live power request in the same supervised
transition that durably records `shutdown-requested` and quiesces hardware
access. Conversely, `shutdown-requested` never authorizes another shutdown on
startup; it authorizes only boot classification.

## Ownership and concurrency

Duty Cycle alone owns schedule state, occurrence identifiers, `WAKE_TIME`,
`RTC_WAKE_TIME`, and automatic shutdown. The register editor cannot expose
these registers for generic writes, and jobs cannot call the internal power
boundary. Boot reads, disarm, wake programming, verification, and shutdown
quiescence all use the one global hardware-operation coordinator.

The occurrence snapshot contains the schedule revision, ordered run-list
revision, and each job revision. An external or UI change that makes these
stale stops the transition. External board changes are detected by fresh
reads where possible; because local privileged users can still race after
verification, that limitation remains an explicit residual risk.

## Required regression coverage

The implemented state machine retains deterministic coverage for schedule and
run-list revisions, timezone and daylight-saving transitions, quiet periods,
missed occurrences, RTC horizon, crash/storage failures around durable phases,
runner restart cleanup without automatic rerun, job contention, failed power
requests, and uncertain disarm. Release acceptance additionally verifies the
supported board's wake write/read-back and startup classifications through an
explicit recoverable hardware procedure.
