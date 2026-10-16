# ADR-0004: Immutable versioned register catalogue

- Status: accepted
- Date: 2026-10-10

## Context

Register addresses and scaling differ across register-set versions. Safety,
ownership, and persistence are independent properties, and a physical product
must not be inferred solely from one version byte. Mutable or partially sourced
metadata could cause the UI to present an unsafe control.

## Decision

The catalogue is compiled Go data validated at process initialization. One
definition describes one register in one register-set version; unavailable
registers have no definition. Construction and retrieval make deep copies,
keys are unique, and ordering is deterministic.

Every definition requires an application-authored label, raw representation,
access, persistence, exact rational conversion, safety class, runtime owner,
constraint identifiers, and revision/page provenance. Product compatibility,
ranges, defaults, sentinels, and enumerations must be represented before the
full catalogue is accepted.

Detection treats `I2C_REG_VER` as register-set evidence only. Product identity
remains unknown unless independently established. Unknown versions are
read-only unsupported results and do not fall back to the closest catalogue.

## Alternatives considered

Runtime YAML is easy to edit but permits unreviewed metadata changes without a
binary review and complicates immutable startup validation. Scraping the PDF at
runtime is fragile and would entangle distribution rights. Using vendor source
tables alone omits safety classification, source pages, constraints, defaults,
and manufacturer semantics.

## Consequences

Catalogue changes receive ordinary code review and exhaustive tests. Supporting
another register set may repeat definitions, but it makes compatibility
explicit and reviewable. The binary must be rebuilt for catalogue corrections.
