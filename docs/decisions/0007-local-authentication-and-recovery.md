# ADR-0007: Single-owner local authentication and recovery

- Status: accepted
- Date: 2026-10-10

## Context

LifePower Control can change power hardware, execute arbitrary commands as a
selected OS user, schedule unattended work, and shut down the host. Browser
access therefore needs authentication and request-forgery protection even for
a single-owner maker system. The normal user should not need an identity
provider, email service, client certificate, role hierarchy, or manual database
editing.

The service binds to loopback by default. A browser normally reaches it locally
or through an SSH tunnel. Direct LAN or Internet exposure changes the transport
and proxy threat model and must not happen accidentally.

## Decision

### One owner account

The initial product has one application owner account named `admin`. It has no
roles or per-page permission ceremony: an authenticated owner can use all
application features. Linux job identity is a separate, installation-time
choice and is always shown where jobs are configured or run.

There is no default password. Before setup completes, normal application pages
remain unavailable and no register write, job, schedule, wake, or power action
can run.

### First-run setup

Installation asks the controller to create a cryptographically random,
single-use setup token with a 30-minute lifetime. The installer prints:

- the loopback URL;
- a copy/paste SSH tunnel command when the installer is running remotely;
- the one-time setup URL/token; and
- the local recovery command for generating a replacement token.

The token is displayed once, stored only as a digest, invalidated immediately
after use, and never written to activity history or ordinary logs. The setup
page asks for a password twice, explains that this is the sole web owner
credential, and then creates a session. It does not ask makers to configure an
email address, security questions, OAuth application, or TLS certificate.

An expired or lost token can be replaced only through a local privileged
administrative command. Creating a replacement invalidates the previous token.

### Password storage

Passwords are stored using Argon2id with a unique random salt and a versioned
parameter envelope. The initial target is 19 MiB memory, two iterations, one
lane, a 16-byte salt, and a 32-byte result. Those parameters must be measured on
a Pi 3A+/3B and adjusted to a documented sub-second target without dropping
below the accepted project minimum. Authentication performs a constant-time
comparison and can rehash after login when parameters increase.

The password has a minimum length of 12 Unicode characters, permits spaces and
long passphrases, and has a 256-byte encoded limit to bound work. It has no
composition rules or forced periodic expiry. Known-default or empty passwords
are never installed automatically.

Using Argon2id introduces the narrowly scoped pure-Go `golang.org/x/crypto`
dependency. Its version, licence, binary impact, and Pi memory/time measurements
must be reviewed before authentication implementation is considered ready.

### Sessions and cookies

Successful login creates a 32-byte random session identifier. Only its SHA-256
digest is stored. Sessions have a 12-hour idle lifetime and seven-day absolute
lifetime; both are checked server-side. Login, password change, authentication
recovery, and privilege-relevant configuration changes rotate or invalidate
sessions as appropriate.

The cookie is host-only, `HttpOnly`, `SameSite=Lax`, scoped to `/`, and has no
user content in its value. `Secure` is mandatory under HTTPS. Loopback HTTP is
supported for local/SSH-tunnel use; the UI and documentation must not describe
an HTTP LAN bind as secure transport.

### CSRF and request origins

Every state-changing browser action uses POST and requires a per-session
cryptographically random CSRF token compared in constant time. The server also
checks `Origin` against its configured external origin; a strictly parsed
same-origin `Referer` is the fallback for clients that omit `Origin`. Missing or
mismatched evidence rejects the request. GET and HEAD handlers never mutate
state.

Login, setup, and recovery forms use purpose-specific anti-forgery tokens even
before an authenticated session exists. Request bodies, field counts, header
sizes, and parsing time are bounded. Application-generated forms work without
JavaScript.

### Binding, proxying, and health

The default and documented setup is `127.0.0.1`. Binding to a non-loopback
address requires explicit configuration and an acknowledgement that HTTPS is
provided by a reviewed reverse proxy or other secure tunnel. The application
does not trust `Forwarded` or `X-Forwarded-*` headers by default. Proxy trust
requires an explicit proxy address and one canonical external HTTPS origin.

Liveness may remain unauthenticated but contains no board, account, version,
schedule, or job details. Readiness and diagnostic detail require
authentication or local administrative access.

### Login resistance and audit

Failed login and setup attempts use bounded per-source and global backoff. The
response does not reveal whether setup state, account data, or a particular
credential component matched. Successful login, logout, password change,
recovery, session invalidation, and bounded failure summaries become activity
events without recording passwords, tokens, cookie values, or request bodies.

The initial backoff permits four consecutive per-source failures, then starts
at one second and doubles to a 32-second cap. A global backoff uses the same
curve after 19 consecutive failures to resist distributed attempts without
making a few household clients contend. Failure counts decay after one quiet
minute; stale source entries are removed after 15 minutes and the source table
is bounded. Delayed requests receive `429` and `Retry-After` rather than tying
up a handler goroutine with a sleep.

### Local recovery

`sudo lifepowerctl admin reset-auth` is the supported recovery path. It requires
a controlling terminal, coordinates exclusively with the stopped controller or
its administrative lock, invalidates all sessions and setup tokens, preserves
jobs/schedules/history, and prints one new short-lived setup token. It does not
accept a password on the command line or through an environment variable.

Recovery authority comes from local root access. The browser cannot invoke the
command, and the controller's fixed-file power admission boundary does not
authorize it or grant sudo access. Recovery steps remain usable over an
ordinary SSH session.

The controller holds a non-blocking exclusive `flock` in its state directory
for its lifetime. The recovery command takes the same lock and fails with a
clear instruction when the service is running; it never races the controller's
atomic snapshot writer. A leftover lock file after a crash is harmless because
the kernel releases the lock with the process. The command verifies `/dev/tty`
rather than trusting redirected standard input, and prints the new token only
to its invoking terminal output.

## Alternatives considered

- **No authentication on loopback:** local processes, forwarded ports, browser
  requests, and accidental rebinding would reach command and power controls.
- **A default password:** easy to document and dangerous to leave unchanged.
- **Unauthenticated first-request setup:** another local process or browser
  request could win the setup race.
- **Linux/PAM password login:** couples the web UI to system credentials and
  exposes a higher-value password to the application.
- **OAuth/OIDC, email reset, multiple roles, or client certificates:** useful
  in multi-user services but disproportionate for the initial single-owner Pi
  workflow.
- **Password in installer arguments:** leaks through shell history or process
  inspection and makes unattended defaults tempting.
- **Trust all proxy headers:** lets direct clients spoof scheme, host, or source
  information used by security checks.

## Consequences

Normal installation adds one short browser setup step. Thereafter the product
behaves like a conventional local web application. A maker needs only their
chosen password and local/SSH access for recovery.

The project must carry and update one small cryptographic dependency. Direct
LAN access requires additional HTTPS/proxy configuration; convenience is not
allowed to silently expose job execution and power control over plaintext.

Losing both the web password and local root/SSH access is intentionally not
recoverable through a remote bypass.

## Argon2id evidence

The project pins `golang.org/x/crypto` v0.48.0, the newest tested release whose
module retains the project's Go 1.24 baseline; v0.49.0 and v0.50.0 require Go
1.25. The module uses the three-clause BSD licence. `argon2.IDKey` is exercised
with the decided 19 MiB, two-pass, one-lane, 16-byte-salt, 32-byte-result
parameters.

Three derivations on a 2 GB Raspberry Pi 5 averaged 66.08 ms and allocated
19,927,544 bytes per operation. On the 425 MiB Pi 3 Model A+, five derivations
averaged 320.41 ms. A ten-derivation run under `GOMAXPROCS=1`,
`GOMEMLIMIT=64MiB`, and `GOGC=5` averaged 327.14 ms, allocated 19,927,534 bytes
per operation, and reached a sampled process peak RSS of 42,880 KiB. A separate
twenty-derivation default-runtime sample reached 43,468 KiB.

The target is comfortably below one second and the constrained test process
remained below the 64 MiB soft target, so the initial parameters are retained.
The complete test suite also passes on the Pi 3A+ with the pinned dependency.
