# Troubleshooting

Start with read-only evidence. Do not change board registers, delete state, or
restart every service at once.

## The web page does not open

Check the web service and listener:

```console
systemctl --no-pager --full status lifepowerctl.service
journalctl -u lifepowerctl.service -n 100 --no-pager
```

If installed in on-demand mode, start it with
`sudo systemctl start lifepowerctl.service`. For an SSH tunnel, “connection
refused” commonly means SSH reached the Pi but nothing is listening on the
forwarded Pi port. Confirm the service before changing firewall or bind
settings. Use the loopback port printed by the installer; it is also recorded
in the root-only installer metadata. A manually edited port that differs from
that metadata causes upgrade and uninstall health checks to refuse safely.

## The setup link is missing or expired

Follow [Authentication recovery](recovery.md#authentication-recovery).
`ensure-setup` does not replace an existing password or pending setup token.

## The board is unavailable or unsupported

Confirm the manufacturer daemon and fixed CLI are installed and can perform a
read-only identity query according to the manufacturer's documentation. Check
the controller and web journals. Do not change permissions broadly or make the
selected job user an I2C member as a workaround.

## A register change is uncertain

A timeout or lost connection may have occurred after the physical write. Do
not retry. Keep the Pi powered, record the Activity entry, and inspect the
value with a read-only operation. See [Safety](safety.md#temporary-register-changes).

## A job failed or output is truncated

Review the run's exit status, signal, timeout, stdout/stderr totals, and the
job-runner journal. Truncation is an intentional storage bound. Reproduce the
command manually as the selected job user, not as root. A failed duty-cycle
job leaves the Pi on and disables automation.

## Automation is disabled or manual-safe

This is the expected response to an unexpected boot, failed job, runner loss,
uncertain wake, invalid schedule/clock, or persistence error. Review Duty
Cycle, Activity, and controller logs. Resolve the cause and simulate again
before re-enabling.

## Upgrade or uninstall refuses

Run local status as the service user and inspect every classification:

```console
sudo -u lifepowerctl /usr/lib/lifepowerctl/lifepowerctl \
  admin status -config /etc/lifepowerctl/config.json
```

Disable automation through the application and wait for jobs/lifecycle work to
finish. Do not edit snapshots or transaction records to manufacture
`maintenance=safe`.

## Asking for help

Include the exact application version, Raspberry Pi model, OS release and
architecture, board/product version, relevant unit state, and a short redacted
log excerpt. Remove passwords, setup/session/CSRF tokens, private hostnames and
addresses, job output, file contents, and personal paths. See [Support](../SUPPORT.md).
