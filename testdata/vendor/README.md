# Sanitised vendor CLI fixtures

These files specify the bounded stdout grammar accepted from
`lifepo4wered-cli GET I2C_REG_VER`: exactly one non-negative decimal integer and
one optional trailing newline. They are authored fixtures and contain no board
identifier or manufacturer prose.

`busy.stdout` is a sanitized real-hardware capture from a Pi 3A+ while another
process held the vendor library's advisory lock on `/dev/i2c-1`. The CLI
returned `-2`, exit status 6, and empty stderr without performing the requested
read. After lock release, the daemon remained active and the same read returned
`7` with exit status zero. The negative value is deliberately rejected by the
non-negative decimal parser.

`io-permission.stdout` is a separate sanitized real-host capture with identical
bytes. The fixed read-only command was run as `nobody`, which had no permission
to open `/dev/i2c-1`; it returned `-2`, exit status 6, and empty stderr in 144
ms. The daemon stayed active and an immediate normal read returned `7`. Keeping
the fixtures separate records their different provenance and demonstrates that
the vendor CLI does not distinguish bus contention from this I/O failure.

`unsupported.stdout` is authored rather than captured from the deployment
board. It represents a syntactically valid future register-set value and makes
the conservative unsupported-version path executable. A real capture requires
different or future hardware and must not be simulated by altering the live
board.

Exit status, timeout, signal, missing-executable, stderr, and oversized-output
cases belong to the future process runner; except for the documented busy
capture above, they cannot be represented by a stdout file alone.
