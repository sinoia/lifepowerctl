# Duty-cycle schedule specification

Status: accepted by project owner, 2026-10-10

This document defines the initial user-visible schedule language and the rules
for calculating concrete board wake times. Scheduling belongs only to the
singleton Duty Cycle; saved jobs never contain or alter a schedule.

## Initial schedule choices

The first implementation offers these meanings:

| UI choice | Required input | Meaning |
| --- | --- | --- |
| Hourly | Minute `00`–`59` | Once each local clock hour at that minute. |
| Daily | Local time | Once each local calendar day at that time. |
| Selected weekdays | One or more weekdays and local time | Once on each selected local weekday at that time. |
| After completion | Positive duration | That duration after the preceding successful duty-cycle run finishes. On initial enablement, the duration starts at enablement. |
| One time | Local date and time | One occurrence only. After its successful run, the schedule completes and the Pi remains on because no further wake exists. |
| Disabled | None | No automatic job, wake programming, or shutdown. |

The labels shown in the UI must include the timing meaning. In particular,
“Hourly at :00” and “60 minutes after completion” must not both be shortened
to “Every hour”. More complex calendar expressions, arbitrary cron text,
multiple schedules, per-job schedules, monthly rules, and exclusion calendars
are outside the initial version.

## Quiet period

Every automatic schedule may optionally have one daily quiet period expressed
as local start and end times. During this period the controller does not create
an occurrence, start the run list, wake the Pi, or shut it down in preparation
for a quiet-period occurrence.

The interval is start-inclusive and end-exclusive:

```text
Quiet from 22:00 until 06:00
    21:59  eligible
    22:00  quiet
    05:59  quiet
    06:00  eligible
```

If the start is later than the end, the interval crosses midnight. If the
start is earlier, it is within one calendar day. Equal start and end is
rejected rather than being interpreted ambiguously as either no quiet period
or a full quiet day. The user disables the option when no quiet period is
wanted.

Quiet periods filter occurrences; they do not move them. With an hourly
schedule at `:00` and quiet time `22:00–06:00`, the last evening occurrence is
21:00 and the next is 06:00. A daily 23:00 schedule with the same quiet period
has no eligible occurrence and cannot be enabled. The occurrence preview must
make this conflict obvious.

For an after-completion schedule, calculation considers the series
`completion + duration`, `completion + 2*duration`, and so on, selecting the
first result outside the quiet period. For example, completion at 21:30 with a
60-minute delay and quiet time `22:00–06:00` produces 06:30, not 06:00. This
preserves the stated delay cadence rather than silently converting it to a
wall-clock schedule.

A one-time occurrence inside the quiet period is rejected. The owner may
change the date/time or turn off quiet time; the UI does not add a hidden
per-occurrence override.

The initial daily quiet period deliberately covers the common overnight use
case without introducing a calendar editor. Multiple quiet periods and
weekday-specific quiet periods may be added later without changing the rule
that exclusions filter base occurrences.

Quiet time controls occurrence starts; it does not terminate a job that began
at an eligible time. If a run extends into quiet time, it is allowed to finish.
The controller then selects the next eligible occurrence, verifies that wake,
and may shut down during the quiet period so the Pi remains off until that
future occurrence.

## Time basis

- Wall-clock schedules and quiet periods use a named IANA timezone stored with
  the schedule, initially defaulting to the Pi's configured timezone.
- The UI always displays the timezone name alongside schedule times and
  previews. It must not persist only a numeric UTC offset.
- Concrete wake values are Unix timestamps written to the board as
  `RTC_WAKE_TIME`; board RTC readings are the authority when selecting a future
  occurrence during an active duty cycle.
- Changing the Pi's timezone does not silently reinterpret an existing
  schedule. The owner must edit the stored timezone and re-enable the cycle.
- An unavailable timezone or failed conversion prevents enablement or
  continuation and leaves the Pi on.

### Daylight-saving transitions

A local time that does not exist because clocks move forward is skipped. A
local time that occurs twice because clocks move backward produces one
occurrence, using the first chronological instance. The durable occurrence ID
and consumed-state transition prevent that local label from running twice.

Hourly recurrence means each distinct local clock hour at the selected minute.
It therefore skips a nonexistent spring-forward hour and runs only once in a
repeated autumn hour. The preview must show the UTC offset so these boundaries
are visible rather than surprising.

## Selecting the next occurrence

The calculator is a pure deterministic function of:

- the immutable schedule revision;
- a reference board timestamp;
- the stored timezone rules available on the system; and
- the minimum safe programming lead time established by hardware tests.

It performs these steps:

1. Generate base occurrences strictly after `reference + minimum lead`.
2. Reject nonexistent local times and collapse repeated local times as above.
3. Remove candidates inside the quiet period.
4. Select the earliest remaining representable 32-bit board timestamp.
5. Return both the absolute timestamp and its local-time explanation.

Fixed wall-clock occurrences missed while the Pi was running, unavailable, or
late are skipped, never replayed. Calculation chooses the next future base
occurrence. Delay-after-completion uses the fresh board time sampled after all
jobs succeed; initial enablement uses its fresh activation-time sample.

The calculator must have a finite search bound. If it cannot find an eligible
occurrence within the supported horizon, it returns an error; it never loops,
guesses, or requests shutdown. Exact duration limits and minimum lead time
remain subject to Pi 3 and board measurement before the UI is enabled.

## Preview and enablement

Editing shows a plain-language summary, timezone, quiet-period summary, and at
least the next five calculated occurrences. A one-time schedule shows its one
occurrence. After-completion previews are explicitly labelled as examples
based on the displayed reference time because actual job completion can vary.

The owner confirms **Enable duty cycle** only after reviewing the preview. The
controller then takes a fresh board-time sample and recalculates. For fixed
wall-clock and one-time schedules, if the first actual occurrence differs
from the confirmed preview, the UI shows the new value and requires
confirmation again; it does not arm a changed time in the background.

For delay-after-completion, the owner confirms the relative delay rule rather
than an example absolute timestamp. Initial activation applies that reviewed
rule to the fresh activation-time board sample, programs and verifies the
resulting exact wake, and shows that exact verified wake on the accepted
shutdown page. This avoids an impossible same-second confirmation race while
preserving the rule that the initial delay starts at activation. Subsequent
occurrences apply the same delay to a fresh board-time sample after the
complete run list succeeds.

An enabled schedule is immutable. To change its recurrence, timezone, quiet
period, or run list, the owner disables and disarms it, edits it, reviews a new
preview, and explicitly enables it again.

## Validation and safe failure

Enablement fails without a board write when:

- required fields or timezone rules are invalid;
- no eligible candidate can be found;
- a one-time occurrence is past, too near, quiet, or outside the board range;
- the board RTC is unreadable or unsuitable;
- the schedule or run-list revision changes during review; or
- the concrete wake cannot be represented safely by the board.

After an automatic run, any calculation failure disables the duty cycle and
leaves the Pi on. Wake programming and shutdown then follow the separate
[duty-cycle lifecycle](duty-cycle-state.md) and
[boot-classification](boot-classification-state.md) safety rules.

## Required calculation tests

- Every initial schedule choice across minute, hour, day, week, month, and
  year boundaries.
- Overnight and same-day quiet periods, including exact start/end boundaries.
- A recurrence completely excluded by quiet time.
- Delay candidates skipped repeatedly through an overnight quiet period.
- Spring-forward gaps and autumn repeated times in representative timezones.
- Missed occurrences, clock correction, lead-time boundaries, Unix timestamp
  overflow, and finite-search failure.
- Preview-to-enable clock movement and schedule-revision conflicts.
