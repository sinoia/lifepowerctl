# Support

LifePower Control is community-maintained software. There is no guaranteed
response time or commercial support agreement.

Before asking for help, use the current patch release on a supported ARM64
Raspberry Pi OS version, read [Troubleshooting](docs/troubleshooting.md), and
collect read-only evidence. Search existing GitHub issues before opening a new
one.

A useful issue includes:

- exact LifePower Control version and commit from the UI or `version` command;
- Raspberry Pi model, `arm64` OS release, and whether it is Trixie or Bookworm;
- LiFePO4wered/Pi+ product/register-set identity;
- expected and observed behavior with minimal reproduction steps;
- relevant service state and a short, redacted journal excerpt; and
- whether automation was enabled, a job active, or the lifecycle non-idle.

Do not post credentials, setup/session/CSRF tokens, private keys, job output or
file contents, private hostnames/addresses, complete configuration, or
unreviewed diagnostic archives. Use GitHub private vulnerability reporting for
security issues as described in [SECURITY.md](SECURITY.md).

Unsupported platforms and modified builds may still receive informal advice,
but maintainers may ask for reproduction on the current supported release.
Hardware-affecting troubleshooting will default to read-only checks and may
require the owner to arrange a safe recovery environment.
