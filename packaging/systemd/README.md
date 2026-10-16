# systemd packaging templates

These files define the controller, independently stoppable web, and selected-
user runner deployment boundaries. They are installer inputs rather than files
an operator copies by hand. The installer must validate account data, replace
every `@...@` token atomically, verify the rendered policy, and only then place
it under root-owned system locations.

## Required identities

- `lifepowerctl`: locked controller account; owns `/var/lib/lifepowerctl`.
- the explicitly selected ordinary job account, commonly `pi`; the runner and
  every job execute as this account with its normal primary group.
- `lifepowerctl-jobs`: contains only the controller and selected job account.
- `i2c`: granted to the controller account, not added by these templates to the
  selected job account.

The installer must reject root, UID 0, system/service-only accounts, missing or
relative homes, locked-only accounts, and ambiguous invoking-user inference.
It must display and confirm the selected user, UID, primary group, home, and
shell. A non-interactive install requires an explicit job user.

On a fresh install the installer creates the `lifepowerctl` system group, a
locked `lifepowerctl` system user with `/usr/sbin/nologin`, and the
`lifepowerctl-jobs` system group. It does not assign fixed numeric IDs. It adds
only the service user to `i2c`, and adds exactly the service user and selected
job user to `lifepowerctl-jobs`. Existing names are accepted only when their
type, primary group, home/shell, and membership are compatible; a collision is
a preflight failure, never a reason to repurpose an account. The selected job
user is not added to `i2c` or sudo by LifePower Control.

## Shared paths

`lifepowerctl-tmpfiles.conf.in` creates setgid directories:

| Path | Owner | Group | Mode | Purpose |
|---|---|---|---:|---|
| `/run/lifepowerctl` | selected job user | `lifepowerctl-jobs` | `2770` | runner-created socket inherits the shared group |
| `/var/lib/lifepowerctl-jobs` | selected job user | `lifepowerctl-jobs` | `2750` | private runner registry and spool root |
| `/var/lib/lifepowerctl-jobs/output` | selected job user | `lifepowerctl-jobs` | `2750` | bounded `0640` stdout/stderr files readable by controller |

The controller state directory is shared only by the controller and web
processes running under the locked `lifepowerctl` identity. The web process no
longer opens the aggregate controller snapshot or holds its writer lock. The
selected user receives no
access to controller authentication, configuration, Activity, I2C, or future
power authority through `lifepowerctl-jobs`.

`install-layout.txt.in` is the closed filesystem lifecycle manifest. `remove`
entries are package machinery; `preserve` entries are configuration and user
data retained by ordinary uninstall; both `runtime` roots are removed by
ordinary uninstall and recreated by tmpfiles; and `metadata` is the root-only
transaction/rollback area. The installer renders
the selected job user into the installed copy at
`/usr/lib/lifepowerctl/install-manifest`. It rejects links and unexpected
owners or types at every destination. It creates a fresh configuration with
exclusive-create semantics, then validates and preserves that exact file on
reinstall or upgrade rather than silently replacing local settings.

## Template substitutions

- `@JOB_USER@`: selected ordinary login name.
- `@JOB_UID@`: selected user's numeric UID for UI disclosure.
- `@JOB_GROUP@`: selected user's normal primary group.
- `@JOB_HOME@`: clean absolute home directory.
- `@CONTROLLER_UID@`: numeric UID of the locked `lifepowerctl` account; the
  runner compares this with Linux `SO_PEERCRED` on every connection.

The runner intentionally has no `NoNewPrivileges=yes`: jobs are meant to have
the selected user's pre-existing authority, which may include deliberately
configured `sudo`. It receives no new sudo, I2C, or power permission from this
application.

The controller creates one exclusive fixed request file in
`/run/lifepowerctl-power`. That directory is root-owned and writable only by
the `lifepowerctl` group; the selected job user and jobs group have no access.
A root-owned systemd path unit consumes the file and starts the fixed helper,
which waits eight seconds before requesting poweroff. The controller verifies
admission by observing root remove the request, but controls no executable,
unit, argument, or delay. It retains `NoNewPrivileges=yes`, an empty capability
bound, and its other sandbox controls. The compatibility sudoers file grants
no authority.

## Measured hardening baseline

The templates verify on the oldest candidate baseline used for local work,
systemd 252 on Debian 12. `systemd-analyze security --offline=yes` currently
reports 3.5 for web, 3.2 for controller, 4.9 for the fixed poweroff helper, and
7.8 for the selected-user runner. The path unit's reported 9.8 is not a process
sandbox score: it executes no program and only activates the separately scored
helper when the fixed root-owned path appears. The runner's higher score is deliberate: its
children must retain the selected user's existing home, network, `/tmp`, sudo,
and process capabilities. It is bounded by its selected UID, cgroup memory,
task and descriptor limits, writable paths, and the controller-authenticated
socket protocol instead of pretending arbitrary owner jobs are a narrow
sandbox.

The web and controller have explicit address-family allowlists, `/proc` hiding,
namespace, writable-path, ambient-capability, executable-memory, kernel, home,
memory, task, and descriptor controls. Their closed device policy admits only
`/dev/i2c-1` because the accepted application boundary reads the board
directly; the runner retains the selected user's ordinary device view. The
controller and poweroff helper retain no ambient capabilities.
Disposable-image tests must still prove fixed-directory access, empty sudo
authority, path enablement, and fixed-unit ownership with the actual service
identities; static security scoring is not runtime acceptance evidence.

## Local packaging commands

The installed binary supplies two installer-facing commands. They do not
change board registers, wake configuration, or power state:

```console
sudo -u lifepowerctl /usr/lib/lifepowerctl/lifepowerctl \
  admin status -config /etc/lifepowerctl/config.json
sudo /usr/lib/lifepowerctl/lifepowerctl \
  admin ensure-setup -config /etc/lifepowerctl/config.json
```

`admin status` emits a closed line-oriented format containing the running
controller build and state-schema identity, controller health, automation and
lifecycle state, active-job classification, runner reachability, read-only
board identity result, and `safe`, `unsafe`, or `unknown` maintenance result.
It must run as the service identity because both local sockets authenticate
their peer UID. A recovered controller, enabled automation, an active run, an
active duty-cycle phase, or uncertain wake disarm is never maintenance-safe.
Unavailable evidence stays `unknown`.
`state_schema` is the schema of the durable controller envelope, or zero when
no snapshot exists. It is distinct from the running binary's readable schema
range and lets the installer prove both forward readability and compatible
rollback before replacing a payload.

`admin ensure-setup` requires root and a controlling terminal because a new
bearer link is secret. It takes the controller lock and returns one of
`setup-created`, `setup-pending`, or `configured` on standard output without
putting the token there. The one-time link is written only to `/dev/tty` and a
pending token, password, or session is never replaced. The deliberately
destructive recovery command remains `admin reset-auth` and is not an
installer operation.

## Disposable-image acceptance checks

Release acceptance runs `systemd-analyze verify` on rendered units where
available, create accounts/groups before verification, execute tmpfiles, and
check exact ownership/modes. It must then prove:

1. the controller can connect and read retained output;
2. an unrelated UID is rejected even if it can reach the socket path;
3. the selected user cannot traverse controller state;
4. representative home scripts and virtual environments run normally; and
5. stopping/restarting the web service leaves the controller operating;
6. controller restart produces conservative run recovery; and
7. the web service cannot start controller-owned automation without the
   bounded Unix-socket protocol.

The installer must additionally validate the intentionally empty
`lifepowerctl-sudoers.in` with `visudo -c`, install it root-owned mode `0440`,
install both poweroff units root-owned and non-writable by service identities,
enable the path watcher across boot, and verify the root-owned mode-0730
request directory excludes the selected job user.
