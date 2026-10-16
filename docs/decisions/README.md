# Architecture decisions

Architecture decision records (ADRs) explain durable choices that are not
obvious from code or owner documentation. Accepted decisions cover repository
identity, the fixed vendor-command boundary, manufacturer-source handling,
register catalogue and constraints, crash-safe persistence, privilege
separation, authentication/recovery, conservative boot classification,
hardware-operation coordination, runtime bounds, the job-runner protocol,
separate controller/web lifecycles, configuration handoff, production
packaging, and public release policy.

Accepted records:

- [ADR-0001](0001-repository-identity-and-remotes.md) — repository identity
  and authority, superseded in detail by ADR-0015;
- [ADR-0002](0002-vendor-command-boundary.md) — fixed, bounded manufacturer
  command boundary;
- [ADR-0003](0003-manufacturer-source-rights.md) — manufacturer-source rights
  and authored catalogue summaries;
- [ADR-0004](0004-register-catalogue-format.md) — compiled immutable register
  catalogues;
- [ADR-0005](0005-persistence-and-migrations.md) — crash-safe bounded state
  and migrations;
- [ADR-0006](0006-privilege-separation.md) — controller, job user, and fixed
  root power authority;
- [ADR-0007](0007-local-authentication-and-recovery.md) — local owner
  authentication and terminal recovery;
- [ADR-0008](0008-boot-classification.md) — absolute-wake classification and
  manual escape;
- [ADR-0009](0009-hardware-operation-coordinator.md) — serialized hardware and
  power operations;
- [ADR-0010](0010-runtime-bounds.md) — centralized resource bounds;
- [ADR-0011](0011-job-runner-protocol.md) — bounded peer-authenticated job RPC;
- [ADR-0012](0012-separate-controller-and-web-lifecycle.md) — always-on
  controller and independently stoppable web UI;
- [ADR-0013](0013-controller-web-configuration-handoff.md) — single-writer
  controller state and revision-bound handoff;
- [ADR-0014](0014-production-packaging-and-installer-contract.md) — release,
  install, maintenance, rollback, and uninstall contract; and
- [ADR-0015](0015-public-repository-and-release-policy.md) — clean public
  history, immutable releases, security reporting, and provenance.

New ADRs use `0000-template.md`, receive the next number, and are linked here
when accepted or superseded. An ADR records context, decision, alternatives,
and consequences; current implementation status belongs in
[`../project-state.md`](../project-state.md).
