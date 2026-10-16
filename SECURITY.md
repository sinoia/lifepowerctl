# Security policy

## Supported versions

Only the latest published patch release is supported with security updates.
Development snapshots, older patch releases, locally modified builds, and
unsupported operating systems or architectures are not covered.

Reports against the current source are welcome. The supported-version policy
applies to published releases beginning with `v0.1.0`; unreleased source and
locally modified builds do not receive production support.

## Reporting a vulnerability

Use GitHub's **Report a vulnerability** link in the Security tab to open a
private vulnerability report. Do not open a public issue or discussion for a
suspected vulnerability.

Include the affected version or commit, operating system and architecture,
board/register-set version when relevant, impact, minimal reproduction, and a
known recovery path. Remove passwords, setup/session/CSRF tokens, private job
output, hostnames, addresses, and identifying hardware data.

Issues involving authentication, arbitrary job execution, register writes,
wake programming, shutdown authority, installation/recovery, or release
integrity are both security- and safety-sensitive. If the system may be in an
uncertain hardware state, keep the Pi powered and avoid repeating the action.

Receipt and next steps will be acknowledged as soon as practical. Disclosure
timing will be coordinated through the private report. Public advisories and
fixed releases will be used when appropriate.

## Security boundary

LifePower Control is a single-owner local application. The default loopback
listener should be accessed locally or through SSH. Internet-facing use,
hostile multi-user hosting, and containment of malicious commands already
authorized to the selected job account are outside the supported boundary.
See [Safety](docs/safety.md) and the [threat model](docs/reference/threat-model.md).
