# Repository scripts

Keep scripts small, reviewable, and optional. Standard Go commands remain
visible through the Makefile.

`build-release.sh` performs one constrained pure-Go ARM64 build and renders the
exact-version candidate asset set described in
[`packaging/release/README.md`](../packaging/release/README.md). It refuses a
dirty tracked/untracked source tree other than the intentional `.review/`
directory, refuses to overwrite an existing version directory, and publishes
nothing. Use it through:

```console
make release VERSION=v0.2.0-rc.1
```

`render-installer.sh` injects the reviewed config, systemd, tmpfiles, empty
compatibility sudoers file, and owned-layout policy into an installer bound to
one version, commit, archive, and fixed release origin. Production release
builds give it the exact GitHub tag URL; local fixture tests use a separately
rendered loopback URL.
The generated installer has no runtime release-origin override.

The installer and uninstaller are generated from reviewed templates. Generated
`dist/` output is ignored, never authoritative source, and is not an official
release unless the protected GitHub workflow reproduces and attests it from an
approved committed revision.
