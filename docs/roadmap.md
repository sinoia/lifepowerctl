# Roadmap

This is directional rather than a promise of dates or inclusion. Current work
is tracked in the public issue tracker once the repository is published.

## First public release

- Complete the clean public documentation and licence review.
- Add pinned, least-privilege CI for formatting, tests, vet, build, packaging,
  shell checks, secret scanning, and dependency review.
- Produce the final ARM64 candidate only from committed public source.
- Verify the candidate on supported clean Raspberry Pi OS images and constrained
  target hardware.
- Publish immutable exact-version assets, SHA-256 checksums, SPDX SBOM,
  provenance, and GitHub artifact attestations.

## Later candidates

- A separately designed, explicit, recoverable board-flash persistence
  workflow. This must never turn temporary edits into implicit `CFG_WRITE`.
- ARMv7/32-bit support, but only after its own build, clean-image, resource,
  installer, and hardware acceptance evidence.
- Optional stronger job isolation that remains understandable and affordable
  on small Pi systems.
- Additional supported hardware or operating systems only with exact identity,
  safety catalogue, packaging, and recovery evidence.
- Usability and accessibility improvements that preserve dependency-free
  progressive enhancement.

Security fixes, data compatibility, safe recovery, and supported-platform
maintenance take priority over expanding scope.
