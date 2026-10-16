# ADR-0003: Manufacturer source text is reference-only

- Status: accepted
- Date: 2026-10-10

## Context

Product Brief revision 13 is the canonical register source. The project has no
written permission establishing that its prose or PDF may be redistributed.
The register catalogue still needs traceable facts and application-authored
guidance.

## Decision

Unless and until the rights holder grants compatible permission, the repository
will not contain the Product Brief PDF, extracted prose, screenshots, or
substantial verbatim passages. Register metadata may contain independently
authored names, numeric facts, conversions, constraints, classifications, and
page references needed for interoperability. Guidance will be newly authored
and visibly labelled as application guidance.

Local source documents used for review must remain outside Git. Tests use
authored fixtures rather than copied prose.

## Alternatives considered

Bundling the PDF would improve offline convenience but has no established
permission. Copying descriptions while awaiting an answer would create the
same publication problem in a harder-to-audit form. Omitting source references
would make review and correction needlessly difficult.

## Consequences

The application can ship a reviewable catalogue without claiming ownership of
manufacturer documentation. Users may need to obtain the canonical brief
separately. Any later permission can broaden distribution through a new ADR;
it is not required for safe read-only implementation.
