# Installation

Install only a version shown as published on this repository's GitHub Releases
page. Drafts, workflow artifacts, and assets presented as official releases
from another location are not supported installation sources.

## Before you start

You need:

- a supported LiFePO4wered/Pi+ connected as documented by its manufacturer;
- Raspberry Pi OS Lite 64-bit Trixie, or the supported legacy 64-bit Bookworm
  image;
- the manufacturer's daemon and `/usr/local/bin/lifepo4wered-cli` installed
  and working;
- `systemd`, `wget` or `curl`, `sha256sum`, `tar`, and ordinary Debian account
  tools; and
- an ordinary, non-root Linux account under which saved jobs may run.

Back up the Pi before installing software that can schedule shutdown and wake.
Do not begin while another process is changing the board or while the Pi is
performing important work.

## Download and verify an exact release

On the selected GitHub release page, save `install.sh` and `checksums.txt` for
the same immutable tag. Do not pipe a network response into `sudo sh`.

```console
wget -O install.sh https://github.com/sinoia/lifepowerctl/releases/download/v0.3.0/install.sh
wget -O checksums.txt https://github.com/sinoia/lifepowerctl/releases/download/v0.3.0/checksums.txt
chmod 0600 install.sh
grep ' install.sh$' checksums.txt
sha256sum install.sh
sh -n install.sh
sh ./install.sh --release-info
less install.sh
```

For a later release, replace `v0.3.0` in both URLs with that exact published
tag. If an exact URL does not resolve to a published release, stop. Compare the
calculated digest with the single `install.sh` entry in `checksums.txt`. Then follow
[Release verification](release-verification.md) to verify the GitHub
attestation and source identity before granting root privileges.

For an offline or separately staged installation, also download the release
archive and `uninstall.sh`, verify all four entries in `checksums.txt`, and
place the three assets consumed by the installer in a protected root-owned
directory:

```console
sudo install -d -o root -g root -m 0700 /root/lifepowerctl-assets-v0.3.0
sudo install -o root -g root -m 0600 checksums.txt \
  lifepowerctl_0.3.0_linux_arm64.tar.gz uninstall.sh \
  /root/lifepowerctl-assets-v0.3.0/
```

Keep using the exact `install.sh` downloaded and verified for the same tag.
The protected directory must not be a symlink or be group- or other-writable.

## Run the installer

Interactive installation is preferred:

```console
sudo sh ./install.sh
```

To consume the protected local assets prepared above without another network
download, use:

```console
sudo sh ./install.sh --asset-dir /root/lifepowerctl-assets-v0.3.0
```

Local assets pass the same size, checksum, archive, uninstaller, and embedded
binary-identity checks as downloaded assets.

The installer reports the fixed release, detected platform and board
prerequisite, selected job account, proposed web-service lifecycle and
loopback port, and every system location it will change. Review the summary
before confirming. Port `8080` is used by default. To select another
unprivileged loopback port, use `--port PORT`, for example:

```console
sudo sh ./install.sh --port 18081
```

The accepted range is `1024` through `65535`. The selected port must be free.

For unattended provisioning, both choices must be explicit:

```console
sudo sh ./install.sh --non-interactive --job-user pi --web=enabled --port 18081
```

Replace `pi` with the ordinary account that should own saved jobs. Use
`--web=on-demand` if the web interface should remain stopped until you start
it manually. The controller and job runner remain enabled because an active
duty cycle must not depend on the web process.

The installer creates a locked `lifepowerctl` service account and a narrowly
shared `lifepowerctl-jobs` group. It does not grant the selected job account
sudo, I2C, or board access. It preserves any authority that account already
has, so choose it deliberately.

## First login

Fresh installation prints a one-time setup URL only to the controlling
terminal. It is a secret and expires. From another computer, first create an
SSH tunnel, using the installation port printed by the installer:

```console
ssh -N -L 18081:127.0.0.1:18081 pi@raspberrypi.local
```

Replace the example account and hostname with those configured on your Pi.
Open the printed setup URL using `127.0.0.1:18081` in that browser. Replace
`18081` with the selected installation port. Choose the
owner password, then sign in. Do not paste the setup URL into chat, logs, issue
reports, or screenshots.

If the link expires or is lost, follow [Authentication recovery](recovery.md#authentication-recovery).

## Verify the installation

```console
systemctl --no-pager --full status lifepowerctl-controller.service
systemctl --no-pager --full status lifepowerctl-job-runner.service
systemctl --no-pager --full status lifepowerctl.service
sudo -u lifepowerctl /usr/lib/lifepowerctl/lifepowerctl \
  admin status -config /etc/lifepowerctl/config.json
```

An on-demand web installation is expected to show `lifepowerctl.service` as
inactive until it is started. Check the dashboard's board identity and
telemetry before attempting any change. Do not enable a duty cycle during the
initial verification.
