# ADR-0006: Controller, selected job user, and fixed power authority

- Status: accepted
- Date: 2026-10-10
- Amended: 2026-10-15 after packaged fixed-path power acceptance

## Context

LifePower Control intentionally runs arbitrary commands supplied by the Pi
owner. The controller also needs board access, private state, and a narrow way
to request shutdown or reboot. Running the web service as root, giving its jobs
the controller identity, or relying on UI validation would collapse these
trust boundaries.

The installed vendor CLI opens `/dev/i2c-1` directly. Its non-blocking file
lock coordinates individual I2C transactions rather than complete application
workflows. Local users with independent I2C permission can still mutate the
board outside this application.

## Decision

### Accounts and files

Installation creates one locked controller account with no interactive shell
and selects an existing ordinary account for jobs:

| Account | Purpose | Permitted resources |
| --- | --- | --- |
| `lifepowerctl` | Separate web and controller service processes | controller configuration and state, activity history, vendor CLI, I2C device, job-runner socket, fixed power requests |
| selected ordinary user, commonly `pi` | Arbitrary job-runner service and child processes | that user's home, projects, virtual environments, devices and existing OS permissions; bounded output directory and job-runner socket |

The selected job user can run scripts and applications from their home
directory with the same ordinary filesystem and device access they have at a
terminal. The user is not added to the controller's private group and cannot
read controller state, credentials, session keys, wake state, or board
configuration through permissions granted by LifePower Control. The controller
account cannot write installed binaries, systemd units, sudo policy, or the
vendor CLI.

The `lifepowerctl-jobs` group contains the controller and selected job user and
owns only the runner socket and bounded job-output area. It grants no access to
controller configuration or state.

ADR-0012 separates the always-on duty-cycle controller process from the
independently stoppable web process. They initially share this locked service
identity; that lifecycle split reduces unattended memory and failure coupling,
but is not represented as an OS-account security boundary. The controller
remains the sole owner of active automation and power decisions.

An optional hardened installation may instead create the locked
`lifepowerctl-job` account described in the original proposal. This is useful
for appliances whose jobs use dedicated directories and need no existing user
environment, but it is not the default maker workflow.

### Installer selection

The installer never selects root as the job user. When invoked through `sudo`
by an unambiguous ordinary user, it proposes that account (normally from the
validated invoking-user context), displays the account name, UID, home,
primary group, and shell, and asks for confirmation. `--job-user <name>` makes
the choice explicit. Non-interactive installation requires the explicit
option; it does not silently infer an identity.

The selected account must already exist, have a non-system UID, a valid home
directory, and not be locked solely for service use. The installer writes a
root-owned systemd drop-in fixing the runner's `User`, `Group`, `HOME`, and
working-directory defaults. Changing the job user is an explicit
administrative reconfiguration, not a web request.

### Job execution boundary

A small `lifepowerctl-job-runner` mode of the same reviewed binary runs as the
selected job user under a separate fixed systemd unit. It accepts requests
only over a local Unix socket whose group permits the controller to connect. It verifies
the peer UID using Unix peer credentials and rejects every other caller.

The protocol is length-bounded and versioned. It carries either an executable
plus argument vector or an explicitly selected shell command, along with a
bounded environment, working directory, timeout, and output limits. The runner
creates and supervises the process group, performs graceful then forced
termination, and returns structured status. It never accepts board operations,
power requests, filesystem ownership changes, user IDs, capabilities, or
systemd unit names.

The runner is not a controller privilege escalation mechanism: it executes
with the selected user's existing authority. A compromised controller can run
arbitrary code as that user, which is already an intentional product
capability, but a job cannot cross back into the controller through the
socket or inherit controller-only permission.

Direct jobs receive a deliberately small environment plus the selected user's
`HOME`, `USER`, `LOGNAME`, and a documented `PATH`; they do not depend on an
interactive shell profile. Explicit shell jobs use the fixed shell and login
semantics shown in the UI, allowing the user's normal shell setup where
requested. A job's default working directory is the selected user's home, and
saved jobs may select another directory that user can access.

### Board access and coordination

The controller receives access to the required I2C device through the standard
`i2c` group or an equally narrow device rule. It invokes only the fixed vendor
CLI path and internally constructed arguments. The application mutation
coordinator serializes its own board changes; fresh reads and optimistic
checks detect external changes. Vendor-daemon coordination must be resolved
before writes are enabled because its per-transaction lock does not make an
application workflow atomic.

### Orderly poweroff

The controller does not receive root, `CAP_SYS_BOOT`, sudo authority, or
permission to choose a command or systemd unit. It may create one exclusive
mode-0200 file at the fixed path
`/run/lifepowerctl-power/poweroff.request`. The parent directory is root-owned,
group-writable only by `lifepowerctl`, mode 0730, and inaccessible to the
selected job user and jobs group.

A root-owned `lifepowerctl-poweroff.path` unit watches only that pathname and
starts the static root-owned helper. The helper first removes the request,
waits the fixed eight-second UI/disconnect interval, and invokes
`systemctl --no-block poweroff`. The controller treats removal by root as
admission; it cannot select the executable, unit, argument, delay, or another
power action. The controller retains `NoNewPrivileges=yes`, empty ambient and
bounding capability sets, and its other sandbox controls. The installed
compatibility sudoers file is intentionally empty and grants no authority.

This replaces the earlier exact-command sudo design. Real packaged testing
showed that retained systemd sandbox controls correctly implied
`no_new_privs`, so a sudo/PAM privilege transition could not be both functional
and preserve the intended controller sandbox. Reboot is not exposed through
the current runtime boundary; adding it would require a separate fixed path,
unit, state-machine use case, and acceptance decision.

## Alternatives considered

- **Run the controller as root and drop credentials in job children:** a web
  compromise would begin with root authority, and credential dropping is easy
  to get subtly wrong.
- **Run jobs as the controller:** jobs could inspect
  credentials and schedules, alter state, or make power requests.
- **Always use a new isolated job account:** strongest default separation, but
  breaks the common maker expectation that existing home-directory scripts,
  projects, virtual environments, and user-owned devices work immediately.
- **Transient systemd units for every job:** adds D-Bus/policy complexity and
  splits supervision from the application without improving the intended
  arbitrary-command boundary.
- **Setuid helper:** creates a custom privileged executable and difficult audit
  surface where a separately identified runner service is sufficient.
- **Give the controller broad sudo/systemctl access:** turns command or argument
  mistakes into root-equivalent authority.
- **Use exact-command sudo:** conflicts with the controller's required
  `no_new_privs` sandbox on the supported systemd baseline.

## Consequences

Installation gains fixed controller, web, and job-runner units but normally
only one new account: `lifepowerctl`. Existing home-directory scripts work
without copying or changing ownership. The UI always shows the OS identity
used by a job.

Jobs inherit permissions the selected user already has. On many maker systems
that includes I2C access and may include broad or passwordless sudo. Such a job
can bypass the controller's hardware coordinator or obtain any authority that
the user could obtain at a terminal. LifePower Control does not add those
permissions and cannot remove them without changing the expected workflow. The
installer reports relevant I2C and non-interactive sudo access, and the UI warns
against changing controller-managed wake registers from a scheduled job.

The Pi 3A+ deployment audit confirms this is a concrete rather than theoretical
risk: its ordinary `pi` account already belongs to `i2c` and has unrestricted
`NOPASSWD: ALL`. A default selected-user job on that image can directly access
the board or become root. This remains accepted because matching the maker's
existing login environment is the chosen default; the installer warning and
optional isolated-account path are required controls.

The optional isolated account is the recommended hardened choice when jobs do
not require an existing user's environment. Isolation is not represented as a
complete sandbox: jobs still receive whatever files and devices the
administrator grants that account.

The controller can request host power actions, so controller compromise can
cause availability loss. It cannot obtain arbitrary root execution through the
approved power boundary. Jobs may access files and devices already available
to the selected user.
