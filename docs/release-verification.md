# Release verification

Official releases are published only from this repository under an immutable
`vMAJOR.MINOR.PATCH` tag. The release page provides:

- the ARM64 archive;
- exact-version `install.sh` and `uninstall.sh`;
- `checksums.txt` with one SHA-256 entry per downloadable asset;
- an SPDX software bill of materials;
- build provenance; and
- GitHub artifact attestations.

Use an exact published tag rather than a `latest` URL when verifying identity.
Drafts and workflow artifacts are candidates, not supported releases.

## Checksums

Download assets and `checksums.txt` from the same immutable release, then run:

```console
sha256sum --check checksums.txt
```

The checksum detects corruption and mismatched assets. Because it is served
through the same channel, it is not by itself independent proof of publisher
identity.

## Attestation and provenance

Use GitHub's documented `gh attestation verify` flow against
`sinoia/lifepowerctl`, selecting the downloaded asset and its accompanying
attestation. Verify that the workflow, repository, commit, tag, and digest
match the release being installed. Review the published provenance rather
than accepting only a successful checksum.

## Binary identity

After extraction or installation:

```console
/usr/lib/lifepowerctl/lifepowerctl version --machine
```

The closed output includes product, version, commit, source state, Go version,
target OS/architecture, and state-schema range. It must agree with the release
tag and provenance. Refuse `development`, `dirty`, an unexpected commit, or a
non-`linux/arm64` production asset.

The release archive itself uses a closed member allowlist and the installer
rejects links, devices, traversal, duplicates, oversized members, or files not
bound to its exact release.
