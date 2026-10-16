# Boot classification state review

```text
No armed schedule
    -> inactive
    -> remain on; no automatic work

Verified absolute wake + persisted shutdown request
    -> read RTC / wake registers / RTC
       -> consumed wake inside timing window
          -> persist expected-boot-observed
          -> automatic run may begin
       -> exact future wake still armed
          -> manual-safe
       -> any missing, changed, late, or incoherent evidence
          -> ambiguous

manual-safe or ambiguous
    -> persist schedule disabled + safe mode first
    -> suppress jobs and all automatic power actions
    -> attempt to clear both wake registers
    -> verify or record uncertain disarm
    -> remain powered on for owner review
```

## Boundary examples

For an intended board time of `10,000` seconds:

| Observation | Result |
| --- | --- |
| absolute wake `0`, RTC `9,998` through `10,300` | expected, if every other invariant holds |
| absolute wake `10,000`, RTC before `9,998` | manual-safe |
| absolute wake `10,000`, RTC at or after `9,998` | ambiguous |
| absolute wake `0`, RTC before `9,998` | ambiguous |
| absolute wake differs from `0` and `10,000` | ambiguous |
| relative countdown nonzero | manual-safe and conflicting state |
| RTC sample reversed, over two seconds, or later than `10,300` | ambiguous |

The two-second and five-minute bounds are provisional hardware-test values,
not claims about measured Pi 3 startup or RTC performance.
