# ADR-0002: Read-only vendor command boundary

- Status: accepted
- Date: 2026-10-10

## Context

LifePower Control must discover a board without becoming responsible for
installing or compiling privileged manufacturer software. The manufacturer
CLI has no version command and locally built installations may have no
package-manager version metadata.

The checked-out vendor source is
`xorbit/LiFePO4wered-Pi@25e816145910af7cced5b8f2c61cb4512b8f2263`. Its CLI
accepts `GET`, `GETHEX`, and `SET`; it exits 6 when the library reports an
unsupported variable or exhausted I2C reads. The library opens `/dev/i2c-1`
for each transaction and takes a non-blocking exclusive `flock` while that
descriptor is open. It retries internally, and a successful write performs a
read-back. Those behaviours do not distinguish every failure and do not
serialize a multi-command application operation.

The standard CLI path is `/usr/local/bin/lifepo4wered-cli`. Runtime
compatibility is established by exact register-set identity and strict command
behavior rather than a locally observed binary hash.

## Decision

- Vendor host software is a detected prerequisite. LifePower Control will not
  build or install it.
- The initial adapter uses the absolute executable path
  `/usr/local/bin/lifepo4wered-cli` and constructs arguments from an internal
  allowlist. Configuration and HTTP input cannot supply executable paths,
  operations, register names, or extra arguments.
- Identity detection permits only `GET I2C_REG_VER`. Read-only access extends
  that allowlist to registers selected from the accepted version-specific
  compiled catalogue; unknown names and register sets are rejected before
  process launch. Purpose-specific write adapters use separate closed
  allowlists and are never selected from raw HTTP or configuration input.
- Accepted stdout is exactly one signed base-10 32-bit integer with an optional
  trailing newline. Identity detection additionally requires a non-negative
  value. Signed output is required for calibration offsets. Stderr is
  diagnostic only. Output, elapsed time, exit status, and launch errors are
  bounded and classified by the process runner.
- Supported vendor provenance records a reviewed source commit and the binary
  hashes actually tested. Locally built artifacts from the same commit need
  not be byte-identical, so hashes are evidence and diagnostics rather than a
  universal compatibility allowlist. Runtime compatibility is gated by the
  detected register set and strict command behavior; a matching path alone is
  not provenance evidence and cannot silently expand capabilities.
- The application-wide mutation coordinator remains necessary. Vendor `flock`
  covers only one I2C file descriptor and cannot make a sequence atomic.
- Missing, unidentifiable, busy, timed-out, malformed, unsupported, or
  unreachable vendor tooling makes the board unavailable and cannot enable a
  write, job, wake, or shutdown.

## Alternatives considered

Linking the GPL vendor shared library would add cgo, couple the process to the
vendor ABI, and complicate release builds. Direct Go I2C would duplicate
vendor retry, scaling, and compatibility behaviour before it is fully
characterised. Invoking a shell would add unnecessary parsing and injection
risk. All three are rejected for the initial implementation.

## Consequences

Read-only detection has a deliberately tiny attack and compatibility surface.
The adapter may require a future supported-version manifest because upstream
does not expose a machine-readable version. CLI exit 6 is necessarily a broad
vendor-access failure until stderr/fixture evidence supports finer diagnosis.

The CLI returns scaled integer engineering values rather than physical raw
register bytes. The application reports a raw value only when the reviewed
scale makes reconstruction exact and round-trips to the observed output.
Fractional or otherwise non-reversible values explicitly have no observed raw
value; the application must not imply that an estimate came from the device.
