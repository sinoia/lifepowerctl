# Threat model

Status: accepted for the first public release scope

## Objective

LifePower Control can inspect and change a power board, run owner commands,
schedule powered-off wake, and request host shutdown. A failure can corrupt
state, prevent boot, drain a battery, interrupt work, or create a repeating
power cycle. Therefore missing, inconsistent, stale, unauthenticated, or
unverified evidence never authorizes a hardware write, automatic job, wake,
shutdown, or reboot.

## In-scope threats

- Unauthenticated network clients and malicious sites opened by the owner.
- Other unprivileged local processes or users.
- Defective or malicious saved jobs and untrusted job output.
- Malformed, slow, or unexpectedly large vendor-command behavior.
- Concurrent manual/vendor board access.
- Interrupted writes, power loss, full/corrupt storage, and process restart.
- Compromised downloads, hostile archives, interrupted privileged install,
  and owner mistakes.

Local root, the selected job user's deliberately retained OS authority, the
kernel/systemd base, and the reviewed installed vendor/application binaries
are trusted boundaries. Protecting against malicious root, hostile multi-
tenant use, physical storage/wiring attacks, Internet exposure, or life-
critical use is out of scope.

## Principal controls

- Loopback networking by default; exact external origin; authentication,
  bounded sessions, POST/CSRF/origin checks, rate limits, CSP, and escaped
  templates.
- Separate locked controller/web and selected-user runner identities;
  peer-authenticated bounded Unix protocols; no application-added sudo for
  jobs.
- Fixed absolute vendor executable, no shell at the hardware boundary, exact
  identity/catalogue allowlists, serialized operations, bounded output and
  time, and mandatory read-back.
- Persist-before-action duty-cycle transitions; consumed expected wake;
  sequential successful jobs before next wake or shutdown; failure leaves the
  Pi on.
- Versioned, bounded, atomic state; private credentials; bounded Activity and
  output; symlink/path checks.
- Exact-version, path-safe release assets; checksums, SBOM, provenance,
  attestations, immutable releases, least-privilege CI, and transactional
  privileged installation.

## Residual risk

The selected job account may already have powerful permissions and jobs are
not a hostile-code sandbox. Manual vendor tools, root, or another I2C process
can bypass application serialization. Plain HTTP is protected only by the
loopback boundary; non-loopback deployment requires a separately operated
HTTPS proxy. Bounded history may omit old evidence, and stored job output may
contain secrets. Owners must retain backups and physical or independent
recovery access.

Security controls must be tested with deterministic failure injection. Real
hardware mutation and power tests require an approved recoverable procedure;
ordinary automated tests never access attached hardware.
