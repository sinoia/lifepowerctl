# ADR-0008: Absolute-wake boot classification and manual escape

- Status: accepted
- Date: 2026-10-10

## Context

The controller may run jobs and shut the Pi down automatically only after a
boot caused by its verified wake request. A person must be able to escape a bad
duty cycle by pressing the board button before the scheduled wake. Missing,
stale, externally changed, or incoherent evidence must leave the Pi running.

Register-set 7 offers two wake mechanisms. `RTC_WAKE_TIME` contains an absolute
Unix timestamp and the board clears it to zero when that value causes boot.
When another event boots the host early, the future value remains programmed.
`WAKE_TIME` is a minute countdown with different consumption semantics and is
not accepted as proof of an expected absolute-wake boot.

## Decision

### Initial wake mechanism

The initial automatic duty cycle uses only `RTC_WAKE_TIME`. It explicitly
disarms `WAKE_TIME` and verifies zero before shutdown. Relative wakes remain
visible for diagnosis but cannot authorize an automatic run or shutdown until
their timing is separately characterized.

Before requesting shutdown, the controller atomically persists:

- schedule enabled state and revision;
- lifecycle phase `shutdown-requested`;
- intended board-clock wake timestamp;
- the exact `RTC_WAKE_TIME` value written and read back;
- verified zero `WAKE_TIME`; and
- the run-list/schedule revision that owns the occurrence.

Any state other than `shutdown-requested` is insufficient to authorize a run
after process or host restart.

### Startup observation

Classification happens before automatic jobs. The controller reads:

```text
RTC_TIME before
RTC_WAKE_TIME
WAKE_TIME
RTC_TIME after
```

The two RTC samples must be monotonic and no more than two seconds apart. The
classifier uses the board clock, not the Linux wall clock, so a daemon clock
correction or unavailable network time cannot manufacture an expected boot.

Initial timing policy:

- two seconds of early-boundary tolerance for one-second register resolution
  and read timing; and
- at most five minutes from the intended wake to controller classification,
  allowing conservative Pi 3 startup time without accepting an indefinitely
  stale occurrence.

These are explicit provisional bounds requiring hardware measurement.

### Classification

An **expected** boot requires all of the following:

- schedule and persisted armed intent are complete and consistent;
- lifecycle phase is `shutdown-requested`;
- wake kind is absolute RTC;
- the persisted intended and verified wake timestamps agree;
- persisted and observed relative wake values are zero;
- observed `RTC_WAKE_TIME` is zero, consistent with consumption by the board;
- board time is no earlier than the intended wake minus two seconds; and
- classification completes no later than five minutes after intended wake.

An **early/manual safe** boot is positively identified when the exact future
absolute wake remains armed before its expected boundary, or when a relative
countdown is unexpectedly still active.

Everything else is **ambiguous**: changed or prematurely cleared wake values,
due-but-not-cleared wake, late startup, unreadable registers, reversed/slow RTC
sampling, unsupported relative intent, missing state, or wrong lifecycle phase.

Only `expected` can proceed. Both `manual-safe` and `ambiguous` perform the same
conservative recovery. `inactive` means no duty cycle was armed and therefore
never starts automatic work.

### One-shot transition and recovery

On an expected result, the controller must first atomically change persisted
phase to `expected-boot-observed`. Only after that save succeeds may it start
the run list. A controller restart therefore cannot classify and execute the
same occurrence twice; a non-shutdown phase becomes ambiguous and stays on.

For manual-safe or ambiguous results, the controller:

1. atomically disables the schedule and persists manual/safe mode;
2. suppresses automatic jobs, shutdown, and reboot immediately;
3. under the single mutation coordinator, writes zero to both wake registers;
4. reads both values back;
5. records whether disarming was verified or uncertain; and
6. leaves the Pi powered on until the owner explicitly reviews and re-enables
   the schedule.

Failure to disarm does not restore automation. The prominent UI warning and
activity record explain the observation and recovery status.

## Alternatives considered

- **Compare Linux time with the schedule only:** network/RTC corrections and
  service delays can make an unrelated boot appear expected.
- **Treat zero wake as sufficient:** external clearing or stale persisted state
  could rerun jobs after a manual boot or crash.
- **Accept any boot after scheduled time:** weakens the physical escape and can
  execute a stale occurrence long after it was intended.
- **Support relative and absolute wakes immediately:** the relative countdown's
  minute-boundary behaviour lacks hardware evidence.
- **Guess expected on read failure:** violates the rule that uncertainty leaves
  the host powered on.

## Consequences

Initial schedules require a trustworthy board RTC and fit the 32-bit timestamp
range. Relative countdown scheduling is deferred. A slow boot taking more than
five minutes enters safe mode rather than running unattended; the owner can
diagnose and deliberately adjust a measured policy later.

The classifier is a pure deterministic function with boundary tests. Hardware
I/O and safe-mode writes remain separate, allowing all logical cases to be
tested without a board.
