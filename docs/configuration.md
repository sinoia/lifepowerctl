# Configuration

The installer writes `/etc/lifepowerctl/config.json`, owned by root and
readable by the `lifepowerctl` service group. Upgrades preserve it. The parser
rejects unknown fields, trailing data, files larger than 64 KiB, unsafe paths,
and inconsistent network settings.

The installed defaults are:

```json
{
  "listen_address": "127.0.0.1:8080",
  "state_directory": "/var/lib/lifepowerctl",
  "activity_directory": "/var/lib/lifepowerctl/activity",
  "external_origin": "http://127.0.0.1:8080",
  "acknowledge_non_loopback": false,
  "job_identity": {
    "user": "exampleuser",
    "uid": 1000,
    "home": "/home/exampleuser",
    "path": "/usr/local/bin:/usr/bin:/bin"
  },
  "runner_socket": "/run/lifepowerctl/jobs.sock",
  "controller_socket": "/run/lifepowerctl/controller.sock",
  "job_output_directory": "/var/lib/lifepowerctl-jobs/output"
}
```

The installer substitutes the actual selected account, UID, and installation
port. Port `8080` is the default; `sudo sh ./install.sh --port 18081` creates a
fresh loopback configuration using port `18081`. Do not copy the example over
an installed configuration.

## Network settings

`listen_address` must contain a literal IP address and port. Loopback is the
safe default. `external_origin` is the exact browser origin used for host,
origin, CSRF, and cookie decisions; it may not contain a path, query, user, or
fragment.

Non-loopback listening requires both `acknowledge_non_loopback: true` and an
HTTPS external origin. This acknowledgement does not provide TLS. You must
operate and secure an HTTPS reverse proxy separately. `trusted_proxy`, when
used, names one specific proxy IP and also requires an HTTPS origin. Do not
enable broad forwarded-header trust.

The recommended remote-access method remains the loopback listener through an
SSH tunnel.

## Paths and identities

State, Activity, socket, and output fields must be clean absolute paths. The
production systemd policy and installer are designed around the shipped
paths; changing them can conflict with service sandboxing and is unsupported
unless the corresponding units and ownership have been deliberately reviewed.

`job_identity` is an installed security boundary, not a convenient way to
switch users. Re-run a reviewed installation procedure rather than editing it
by hand. The vendor executable is fixed internally at
`/usr/local/bin/lifepo4wered-cli` and cannot be redirected in JSON.

## Applying a change

Before editing, back up the file while preserving its permissions. Validate
JSON and inspect the affected units. Restart the web service for listener and
origin changes; controller-related paths require a carefully planned service
restart while automation is disabled and idle.

The release lifecycle records the installation port for bounded health and
recovery checks. Do not change `listen_address` or its matching
`external_origin` port by hand after installation; doing so leaves installer
metadata inconsistent. A `v0.1.0` installation that was already customized by
hand can adopt that existing port during its first upgrade by passing the same
`--port PORT` value to the new installer.

```console
sudo systemctl restart lifepowerctl.service
journalctl -u lifepowerctl.service -n 100 --no-pager
```

If validation fails, restore the previous configuration rather than weakening
the parser or service sandbox.
