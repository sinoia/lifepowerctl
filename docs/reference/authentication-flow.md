# Authentication and recovery review

This is the expected single-owner flow from ADR-0007.

## First installation

```text
sudo install.sh
  -> validates and confirms ordinary job user
  -> installs and starts controller
  -> prints loopback URL, SSH tunnel example, and one-time setup URL

owner opens setup URL
  -> chooses one passphrase
  -> setup token is consumed
  -> authenticated session starts
  -> Overview becomes available
```

Before the passphrase is created, no operational feature is available. Losing
or refreshing the setup page does not consume the token until a valid setup
submission succeeds.

## Normal use

```text
open local or SSH-forwarded URL
  -> enter admin passphrase
  -> use all application features
  -> logout explicitly, or session expires
```

There is one web owner rather than a user-management screen. Job pages clearly
show the separately selected Linux execution user.

## Lost password or setup token

```text
SSH or local terminal
  -> stop the lifepowerctl service
  -> sudo lifepowerctl admin reset-auth
  -> all browser sessions are invalidated
  -> command prints one short-lived setup URL
  -> owner chooses a replacement passphrase in the browser
```

Jobs, schedules, activity, register metadata, and controller configuration are
preserved. No password is accepted in command arguments or environment
variables.

An installation using a non-default configuration passes the same file used by
the service: `sudo lifepowerctl admin reset-auth -config /path/to/config.json`.
The command refuses redirected/non-terminal execution and refuses to modify
state while the controller holds its administrative lock.
The state directory must already have been created with the controller service
identity; root-run recovery preserves that owner on the replaced private auth
files so the unprivileged service can restart.

## Reviewer checks

- Setup output is understandable without reading source code or configuring an
  identity provider.
- Setup and recovery tokens never appear in logs or activity.
- Forms work through an SSH tunnel and without JavaScript.
- Backoff gives a legitimate owner useful feedback without disclosing secrets.
- The UI distinguishes web identity (`admin`) from the Linux job user.
- Non-loopback binding cannot accidentally operate as trusted plaintext HTTP.
- Recovery requires local root authority and does not depend on a working web
  session.
