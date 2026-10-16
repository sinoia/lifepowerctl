# Recovery

Recovery procedures are intentionally narrow. Preserve the first error and
logs, avoid repeated writes or service restarts, and do not remove transaction
metadata by hand.

## Authentication recovery

To create a missing initial setup link, stop the controller, run the bounded
setup command from a real terminal, then restart it:

```console
sudo systemctl stop lifepowerctl-controller.service
sudo /usr/lib/lifepowerctl/lifepowerctl \
  admin ensure-setup -config /etc/lifepowerctl/config.json
sudo systemctl start lifepowerctl-controller.service
```

The command reports `configured` without changing existing credentials,
`setup-pending` without replacing a still-valid secret, or `setup-created` and
writes a one-time URL only to the terminal.

If the password is lost, `reset-auth` deliberately invalidates the password,
sessions, and prior setup token and creates a new one:

```console
sudo systemctl stop lifepowerctl-controller.service
sudo /usr/lib/lifepowerctl/lifepowerctl \
  admin reset-auth -config /etc/lifepowerctl/config.json
sudo systemctl start lifepowerctl-controller.service
```

Do not use `reset-auth` as a general troubleshooting step.

## Interrupted installation

Use the exact same saved installer that began the operation:

```console
sudo sh ./install.sh --recover
```

It verifies its release binding and the closed root-only transaction before
restoring or completing the supported state. A different version must refuse.

## Interrupted uninstall

Use the root-only saved uninstaller named in the original error, normally:

```console
sudo sh /var/lib/lifepowerctl-installer/uninstall-previous/uninstall.sh --recover
```

Do not substitute a downloaded or currently installed script.

## Unexpected or early boot

An unexpected boot should enter a conservative/manual-safe state: automation
is disabled, wake state is disarmed where it can be verified, and the Pi stays
on. Inspect the web interface's duty-cycle status, Activity, controller logs,
and local status output. Resolve the reason before re-enabling automation.

## Uncertain board write or wake

Keep the Pi powered. Stop initiating changes, capture the Activity entry and
controller logs, and use read-only manufacturer diagnostics to inspect board
identity and current values. A timeout can mean that a write happened but its
confirmation was lost. Repeating it without current evidence can make recovery
harder.

## State or storage errors

Do not delete the current or previous snapshot. Copy the preserved directories
and logs while keeping ownership and modes. Filesystem repair, disk replacement,
or restoration should happen outside active automation. If the supported
procedure cannot classify the state, ask for help without posting credentials,
setup links, full job output, or private network details.
