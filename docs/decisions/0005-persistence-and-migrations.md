# ADR-0005: Versioned atomic JSON snapshots

- Status: accepted
- Date: 2026-10-10

## Context

LifePower Control needs durable configuration, job definitions, schedule state,
and recovery evidence on a memory-constrained Pi. Writes are expected to be
infrequent. The system must detect stale writers, survive interruption during
replacement, support rollback, and reject unknown future formats. Job output
and activity history have different growth and retention characteristics from
controller state.

SQLite would provide transactions and mature migrations, but a cgo driver
would complicate releases and a pure-Go SQLite implementation would add
significant code, compilation, binary, and runtime cost before the workload
justifies it. A database can be reconsidered if measured query or transaction
requirements exceed the bounded file design.

## Decision

Use standard-library JSON with three storage classes:

1. One versioned aggregate controller snapshot for configuration, saved jobs,
   duty-cycle state, and retention policy. Its encoded payload is capped at
   256 KiB.
2. Immutable, bounded activity segments, initially 64 KiB each with at most 16
   segments (approximately 1 MiB before record-boundary overhead). Individual
   records are capped at 8 KiB. Segments rotate by size and only the retention
   owner removes old segments. A truncated final record is discarded during
   startup recovery; earlier complete records remain immutable.
3. Separate bounded files for job stdout/stderr. Output contents never enter
   the controller snapshot.

Snapshot replacement writes a mode-0600 temporary file in a mode-0700 state
directory, syncs it, rotates the current file to a previous snapshot, renames
the temporary file into place, and syncs the directory. Loading validates the
envelope and falls back to the previous snapshot when the current file is
missing or invalid. Recovery is reported explicitly; it is not silently called
normal state. Revisions provide optimistic concurrency, while service
ownership ensures there is only one controller process.

The write policy assumes SD-card deployment even when development uses NVMe:
telemetry is not persisted by default, unchanged state is not rewritten,
activity and output retention is bounded, cleanup is batched, and periodic
polling must not cause durable writes. Safety transitions may sync immediately;
non-safety history may be buffered only within an explicitly bounded loss
window.

Every snapshot has a positive schema version and revision. Migrations are
ordered pure transformations from one schema to the next. Startup validates
and transforms completely before saving; the atomic save retains the
pre-migration snapshot as the previous version. Unknown future schemas are
never interpreted. A migration failure leaves the existing files untouched
and prevents normal service readiness.

The installer does not rewrite application state. Backup, rollback, and
retention operations must use the same controller-owned interfaces rather than
editing JSON in place.

## Alternatives considered

- **SQLite with cgo:** mature but conflicts with simple static cross-builds and
  introduces a native toolchain dependency.
- **Pure-Go SQLite:** capable, but its compile, binary-size, and memory costs are
  disproportionate to this low-write workload until measurement proves a need.
- **One ever-growing JSON file:** simple, but rewrites unbounded history and job
  output on every state change.
- **Unversioned JSON or in-place writes:** cannot provide safe migration,
  rollback, or interruption recovery.

## Consequences

The initial implementation stays dependency-free and inspectable. Cross-file
updates are not atomic, so the aggregate snapshot owns all state that must
change transactionally. Activity records and output use durable identifiers and
may lag the aggregate after a crash; recovery must classify and surface that
condition. Filtering large histories will require bounded segment scans rather
than SQL queries.

The implementation covers snapshot replacement, permissions, revision checks,
future-schema rejection, recovery from the previous snapshot, bounded activity
rotation, retention, and truncated-tail repair. Activity events are operational
history rather than telemetry and are synced individually. If SD-card evidence
requires batching later, the permitted loss window must be explicit and safety
transitions must remain synchronous.
