# Register catalogue review

The catalogue contains interoperability facts and application-authored labels;
it does not contain Product Brief prose. Its canonical manufacturer reference
is the **LiFePO4wered/Pi+ Product Brief, revision 13**. The vendor implementation
at `xorbit/LiFePO4wered-Pi@25e816145910af7cced5b8f2c61cb4512b8f2263`
is a secondary implementation reference, not a substitute for the brief.

## Immutable format

`internal/board.Definition` records one register in one register-set version.
`NewCatalog` validates required provenance and safety fields, takes a deep copy,
rejects duplicate `(register set, register)` keys, and stores definitions in a
deterministic order. Retrieval also returns a deep copy. Application code must
not expose a mutable global catalogue.

Each reviewed definition records:

- stable CLI/register identifier and an application-authored title;
- register-set version, address, width, and signedness;
- read/write and volatile/flash behaviour;
- exact rational raw-to-engineering scaling and unit;
- safety class, runtime owner, and named cross-register constraints; and
- source document, revision, and printed page number.

Every definition also carries application-authored purpose and behavior text.
Every writable definition requires separate safety guidance. These fields are
UI content owned by this project, not quotations from the Product Brief.

Ranges, defaults, sentinel values, enumerations, product compatibility, and
version-specific scale variants are represented explicitly. Catalogue
construction rejects missing or unrepresentable bounds, overlapping addresses,
unsafe access/classification combinations, writable entries without authored
safety guidance, invalid choices or sentinels, duplicate constraint metadata,
and invalid source pages.

## Per-definition review checklist

- [ ] Identifier agrees with the fixed vendor CLI allowlist.
- [ ] Address, width, byte order, signedness, access, and persistence agree with
  revision 13.
- [ ] Availability and address are checked independently for every supported
  register-set version; absence is explicit rather than represented as an
  address.
- [ ] Scaling is compared with both the Product Brief and vendor source, with
  boundary and rounding fixtures.
- [ ] Default, minimum, maximum, enumerations, sentinel values, and units are
  checked where applicable.
- [ ] Safety class and owner are reviewed independently of read/write access.
- [ ] Every local and cross-register rule has a stable constraint identifier.
- [ ] Manufacturer facts have printed page references; authored guidance is
  visibly distinct and contains no copied prose.
- [ ] Read-only, factory, connectivity, daemon-owned, scheduler-owned, and
  operational registers cannot accidentally receive an ordinary edit policy.
- [ ] A second reviewer records their name, date, source revision/hash, and any
  discrepancy resolution.

## Catalogue-level acceptance

- Every revision-13 register appears exactly once for register set 7.
- Detection never infers a physical product model solely from register-set
  version; product identity requires separate evidence.
- Unknown register sets expose only an unsupported identity result.
- Tests cover every definition, conversion edge, constraint, and source page.
- A generated review report contains no descriptions copied from the brief.

The accepted register-set 7 catalogue is indexed in
[register-catalogue-v7-review.md](register-catalogue-v7-review.md). Its
independent reviewer record and exact source hashes are complete. Tests lock a
canonical fingerprint of every reviewed interoperability fact while allowing
application-authored guidance to improve through ordinary review.
