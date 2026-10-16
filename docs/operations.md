# Operations

## Services

LifePower Control installs three long-running services and a fixed poweroff
path/helper pair:

| Unit | Purpose |
| --- | --- |
| `lifepowerctl-controller.service` | Owns authoritative state, board access, scheduling, and power requests |
| `lifepowerctl-job-runner.service` | Runs jobs as the selected ordinary user |
| `lifepowerctl.service` | Serves the authenticated web interface; may be on demand |
| `lifepowerctl-poweroff.path` | Root-owned watcher for the controller's fixed request |
| `lifepowerctl-poweroff.service` | Fixed delayed orderly-poweroff helper |

The controller and runner normally remain enabled. The web service can be
started and stopped independently:

```console
sudo systemctl start lifepowerctl.service
sudo systemctl stop lifepowerctl.service
```

## Health and logs

```console
systemctl --no-pager --full status lifepowerctl-controller.service
systemctl --no-pager --full status lifepowerctl-job-runner.service
systemctl --no-pager --full status lifepowerctl.service
journalctl -u lifepowerctl-controller.service -n 100 --no-pager
journalctl -u lifepowerctl-job-runner.service -n 100 --no-pager
journalctl -u lifepowerctl.service -n 100 --no-pager
sudo -u lifepowerctl /usr/lib/lifepowerctl/lifepowerctl \
  admin status -config /etc/lifepowerctl/config.json
```

The local status report classifies controller/runner/board availability,
automation, lifecycle, jobs, and maintenance as safe, unsafe, or unknown. An
unknown result is not permission to continue maintenance.

## Files and data

| Path | Contents |
| --- | --- |
| `/usr/lib/lifepowerctl` | Installed binary, uninstaller, and package manifest |
| `/etc/lifepowerctl/config.json` | Preserved configuration |
| `/var/lib/lifepowerctl` | Credentials, controller state, schedules, jobs, and Activity |
| `/var/lib/lifepowerctl-jobs` | Runner metadata and bounded output |
| `/var/lib/lifepowerctl-installer` | Root-only install/rollback metadata |
| `/run/lifepowerctl` | Runtime sockets |
| `/run/lifepowerctl-power` | Fixed power-request boundary |

Do not change ownership or modes to work around an error. The separation keeps
the selected job user away from authentication, controller state, I2C access,
and power authority.

## Backup

For a consistent manual backup, first disable automation in the web interface
and wait for jobs and the lifecycle to become idle. Confirm `maintenance=safe`
with the local status command. Stop the web, controller, and runner services,
then back up these preserved roots with ownership, modes, and timestamps:

```text
/etc/lifepowerctl
/var/lib/lifepowerctl
/var/lib/lifepowerctl-jobs
```

The backup contains authentication material, job definitions, output, and
possibly secrets. Encrypt it and restrict access. Restart the controller and
runner after the copy, and the web service if its selected lifecycle is
enabled. Restoring to a different release or host requires compatible state
schema, identities, ownership, and manufacturer software; do not improvise a
partial restore on a production Pi.

## Routine maintenance

OS updates, reboots, upgrades, and uninstall should occur only when automation
is disabled, jobs are idle, the lifecycle is inactive, and status reports
maintenance safe. LifePower Control refuses supported package operations when
it cannot establish those facts.
