# Privilege boundary

LifePower Control uses separate identities so web administration, authoritative
controller state, owner jobs, and root power policy do not collapse into one
process.

| Capability | Controller/web account | Selected-user jobs | Root packaging/helper |
| --- | :---: | :---: | :---: |
| Read public configuration | yes | no | yes |
| Read credentials/session state | yes | no | installation/recovery only |
| Write controller state and Activity | controller only | no | installation/recovery only |
| Read/write bounded job output | read | yes | installation/recovery only |
| Open `/dev/i2c-1` | controller only | only if already permitted | packaging checks only |
| Invoke the fixed vendor CLI | yes | only with existing user authority | packaging checks only |
| Run owner-selected commands | request only | yes | no |
| Select an arbitrary process UID | no | no | no runtime interface |
| Request the fixed poweroff action | controller only | no | fixed helper executes it |
| Run arbitrary root commands | no | only if the user already has sudo | no runtime interface |
| Modify binary, units, or policy | no | no | package operations only |

The installer creates a locked `lifepowerctl` account and a
`lifepowerctl-jobs` group containing only that account and the explicitly
selected ordinary job user. The shared group owns the runner socket and
bounded output area, not controller credentials or state. Kernel peer
credentials restrict runner requests to the configured controller UID.

The selected user is deliberately not added to I2C, sudo, systemd, or another
administrative group. However, LifePower Control neither removes nor contains
permissions the account already holds. A job account with passwordless sudo or
I2C access can bypass application safeguards. The installer reports this as a
residual risk rather than silently rewriting local policy.

Direct jobs receive an executable and argument vector; shell jobs are explicit
and use one fixed shell. The runner validates working directory, environment,
runtime, process count, and output bounds and supervises a process group.
These controls protect availability and cleanup but do not make same-UID jobs
hostile-code-safe.

The controller can create only one fixed request under a root-owned runtime
directory. Root-owned systemd path/helper units select the command and delay;
neither controller nor job input selects an executable, argument, unit, or
arbitrary privileged action. The compatibility sudoers file is intentionally
empty.

Release acceptance verifies accounts, groups, modes, sockets, cross-account
access, peer rejection, runner behavior, fixed helper ownership, empty sudo
authority, and recovery on supported clean images without triggering power in
ordinary automated tests.
