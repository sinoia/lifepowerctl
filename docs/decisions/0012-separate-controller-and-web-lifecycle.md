# ADR-0012: Separate always-on controller and on-demand web lifecycle

- Status: accepted
- Date: 2026-10-10

## Context

The duty-cycle controller must remain available whenever the Pi is powered. It
restores durable automation state, classifies the boot, supervises the ordered
job run, calculates and verifies the next wake, and reaches a conservative
power decision. None of those responsibilities may depend on a browser or on
the administration UI remaining available.

The web application has a different lifecycle. Owners need it while reviewing
status and Activity or editing registers, jobs, and the schedule, but an
unattended duty-cycle deployment does not need to keep its HTTP server,
templates, authentication sessions, or password-hashing working memory
resident. This distinction matters on 512 MiB Raspberry Pi targets.

Early measurements placed the combined read-only web/controller shell at approximately
10 MiB RSS after startup and 31 MiB after authentication. Those measurements
are acceptable for an interactive service but should not become the permanent
unattended cost merely for implementation convenience.

## Decision

Production packaging will provide two independently managed service modes:

- a minimal, always-on duty-cycle controller; and
- an on-demand web administration application that can be stopped without
  disabling, pausing, or invalidating an enabled duty cycle.

The services may be modes of the same installed Go binary, but they are
separate processes and systemd units. The controller owns authoritative
automation state, hardware mutation coordination, job-run requests, wake
programming, and power decisions. The web application owns browser concerns
and submits validated configuration proposals through a bounded local
interface or atomic handoff. A proposal becomes active only after controller
validation and durable acknowledgement.

Stopping, crashing, restarting, or upgrading the web application must not
interrupt an active run or erase controller state. Conversely, failure of the
controller must be shown clearly when the web application is available; the
web process must not silently assume automation authority.

The selected-user job runner remains a third, separately identified service.
It need not execute a job when idle, and job-process memory is accounted
separately from both controller and web memory.

Initial Pi 3A+ engineering targets are:

| Process or combination | RSS target |
| --- | ---: |
| Always-on controller, idle | below 20 MiB |
| Web application before authentication | below 20 MiB |
| Controller plus web during ordinary administration | below 50 MiB |
| Controller during ordinary operation | below the 64 MiB soft target |

These are acceptance targets, not reservations or substitutes for the hard
systemd containment limits in ADR-0010. Packaging will leave the controller
enabled and allow the owner to choose whether the web unit is continuously
enabled or started only for administration.

## Alternatives considered

- **One permanently running combined process:** avoids a second Go runtime
  while the UI is active, but retains the larger HTTP/authentication/template
  footprint throughout unattended operation and couples UI failure to duty
  cycling.
- **Start the duty-cycle engine only from the web application:** makes safe
  automation depend on an administrative interface and cannot satisfy the
  required unattended lifecycle.
- **Keep both processes permanently enabled:** preserves failure separation but
  wastes memory on constrained installations that rarely use the UI.
- **Run every concern in a separate daemon:** increases operational ceremony
  and duplicates runtime overhead without a demonstrated boundary benefit.

## Consequences

The normal unattended footprint is the small controller rather than the full
web application. UI restarts and upgrades no longer disturb active automation,
and a maker can stop the web unit after configuration.

The implementation gains an explicit controller/web protocol or handoff,
revision conflict handling, and two health/lifecycle states. Documentation and
the installer must make starting, enabling, stopping, and diagnosing the web
unit approachable. Shared state cannot be casually mutated by both processes.

When both services run, two Go runtimes may use slightly more memory than a
single combined process. This is accepted because concurrent administration is
temporary on constrained deployments and lifecycle independence is the main
requirement.
