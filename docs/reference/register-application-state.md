# Register application state review

```text
idle
  -> acquire hardware-operation lease
  -> fresh identity and state read
     -> unsupported / changed / invalid
        -> failed-before-change
        -> record and release
     -> valid
        -> write next value
           -> definite write failure
              -> record completed prefix
              -> rollback only when reviewed and safe
           -> connection lost / timeout
              -> outcome-unknown
              -> no blind retry
           -> write returned
              -> read back
                 -> exact match: verified
                 -> mismatch: partial or externally changed
                 -> unreadable: outcome-unknown
        -> persist result and activity evidence
        -> release lease
```

For a multi-register operation, every applied and verified item is retained in
the result. The first unverified step stops forward progress. A workflow may
attempt rollback only when its definition specifies the reverse order and the
current board remains readable and compatible. Failed or uncertain rollback
leaves the overall result uncertain and the Pi powered on.

Flash commit is not a generic final step and is not implemented in the first
public release. Any future persistence feature requires its own isolated
workflow, fresh full snapshot, explicit confirmation, action write, complete
read-back, recovery record, and durable design review while holding the same
global lease. Temporary editing must never gain an implicit `CFG_WRITE` step.
