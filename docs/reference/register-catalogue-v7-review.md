# Register-set 7 catalogue review sheet

Status: accepted after project-owner independent technical review
Catalogue source: `internal/board/catalog_v7.go`
Primary reference: LiFePO4wered/Pi+ Product Brief, revision 13
Secondary reference: `xorbit/LiFePO4wered-Pi@25e816145910af7cced5b8f2c61cb4512b8f2263`

This is a compact review index, not an independent data source. Titles are
application-authored. Defaults are raw register values; `factory`, `dynamic`,
and `—` mean there is no fixed numeric default. Scale is the exact engineering
unit multiplier applied per raw unit. All multi-byte values are little-endian.

| Register | Address | Bytes | Access / persistence | Raw default | Scale / unit | Safety / owner | Brief page |
| --- | ---: | ---: | --- | ---: | --- | --- | ---: |
| `I2C_REG_VER` | `0x00` | 1 | RO / volatile | 7 | 1 version | read-only / none | 19 |
| `I2C_ADDRESS` | `0x01` | 1 | RW / flash | 67 | 1 address | connectivity-critical / none | 20 |
| `LED_STATE` | `0x02` | 1 | RW / flash | 1 | 1 enum | ordinary / none | 20 |
| `TOUCH_CAP_CYCLES` | `0x03` | 1 | RW / flash | 0 | 1 cycle | advanced / none | 20–21 |
| `TOUCH_THRESHOLD` | `0x04` | 1 | RW / flash | 12 | 1 count | advanced / none | 21 |
| `TOUCH_HYSTERESIS` | `0x05` | 1 | RW / flash | 2 | 1 count | advanced / none | 21 |
| `DCO_RSEL` | `0x06` | 1 | RW / flash | factory | 1 raw | factory-calibration / none | 21–22 |
| `DCO_DCOMOD` | `0x07` | 1 | RW / flash | factory | 1 raw | factory-calibration / none | 22 |
| `VBAT_MIN` | `0x08` | 2 | RW / flash | 4665 | 625/1023 mV | advanced / none | 23 |
| `VBAT_SHDN` | `0x0A` | 2 | RW / flash | 4829 | 625/1023 mV | advanced / none | 23 |
| `VBAT_BOOT` | `0x0C` | 2 | RW / flash | 5156 | 625/1023 mV | advanced / none | 24 |
| `VOUT_MAX` | `0x0E` | 2 | RW / flash | 5449 | 65705/102300 mV | advanced / none | 24 |
| `VIN_THRESHOLD` | `0x10` | 2 | RW / flash | 1451 | 317154/102300 mV | advanced / none | 25 |
| `VBAT_OFFSET` | `0x12` | 2 | RW / flash | factory | 5000/1023 mV | factory-calibration / none | 26 |
| `VOUT_OFFSET` | `0x14` | 2 | RW / flash | factory | 525641/102300 mV | factory-calibration / none | 26 |
| `VIN_OFFSET` | `0x16` | 2 | RW / flash | factory | 253723/10230 mV | factory-calibration / none | 26 |
| `IOUT_OFFSET` | `0x18` | 2 | RW / flash | 0 | 581395/102300 mA | advanced / none | 26 |
| `IOUT_SHDN_THRESHOLD` | `0x1A` | 2 | RW / flash | 0 | 72674/102300 mA | advanced / none | 25 |
| `SHDN_DELAY` | `0x1C` | 2 | RW / flash | 40 | 1 tick | advanced / vendor-daemon | 28 |
| `AUTO_SHDN_TIME` | `0x1E` | 2 | RW / flash | 65535 | 1 minute | advanced / none | 29 |
| `AUTO_BOOT` | `0x20` | 1 | RW / flash | 0 | 1 enum | advanced / none | 27 |
| `PI_BOOT_TO` | `0x21` | 1 | RW / flash | 30 | 10 seconds | advanced / vendor-daemon | 30 |
| `PI_SHDN_TO` | `0x22` | 1 | RW / flash | 12 | 10 seconds | advanced / vendor-daemon | 30 |
| `WATCHDOG_CFG` | `0x23` | 1 | RW / flash | 0 | 1 enum | advanced / user-application | 31 |
| `WATCHDOG_GRACE` | `0x24` | 1 | RW / flash | 2 | 10 seconds | advanced / user-application | 32 |
| `CFG_WRITE` | `0x25` | 1 | RW / volatile | 0 | 1 action | operational-action / none | 34 |
| `WAKE_TIME` | `0x26` | 2 | RW / volatile | 0 | 1 minute | advanced / duty-cycle | 28 |
| `RTC_TIME` | `0x28` | 4 | RW / volatile | dynamic | 1 Unix second | operational-action / vendor-daemon | 30 |
| `RTC_WAKE_TIME` | `0x2C` | 4 | RW / volatile | 0 | 1 Unix second | advanced / duty-cycle | 31 |
| `WATCHDOG_TIMER` | `0x30` | 1 | RW / volatile | dynamic | 10 seconds | operational-action / user-application | 33 |
| `PI_RUNNING` | `0x31` | 1 | RW / volatile | dynamic | 1 boolean | operational-action / vendor-daemon | 33 |
| `VBAT` | `0x32` | 2 | RO / volatile | — | 625/1023 mV | read-only / none | 22 |
| `VOUT` | `0x34` | 2 | RO / volatile | — | 65705/102300 mV | read-only / none | 22–23 |
| `VIN` | `0x36` | 2 | RO / volatile | — | 317154/102300 mV | read-only / none | 22 |
| `IOUT` | `0x38` | 2 | RO / volatile | — | 72674/102300 mA | read-only / none | 23 |
| `TOUCH_STATE` | `0x3A` | 1 | RO / volatile | — | 1 bitfield | read-only / none | 20 |

## Enumeration review

- `LED_STATE`: 0 off, 1 on, 2 pulsing, 3 fast flash.
- `AUTO_BOOT`: 0 off, 1 battery, 2 battery-smart, 3 input,
  4 input-smart, 5 no-input, 6 no-input-smart, 7 input-smart-armed.
- `WATCHDOG_CFG`: 0 off, 1 alert, 2 shutdown.
- `PI_RUNNING`: 0 not running, 1 running.
- `CFG_WRITE`: 70 is the only flash-commit action; other values must never be
  presented as equivalent actions.

## Sentinel review

- Zero disables `IOUT_SHDN_THRESHOLD`, `PI_BOOT_TO`, `PI_SHDN_TO`,
  `WAKE_TIME`, and `RTC_WAKE_TIME`.
- `AUTO_SHDN_TIME` value 65535 disables input-loss automatic shutdown; zero
  instead requests immediate shutdown on input loss.

## Reviewer record

- Reviewer: project owner
- Review date: 2026-10-11
- Product Brief copy/hash: official revision 13 PDF dated 2024-02-01,
  SHA-256 `7f97fdbe3ec37d807888947fb9d6d1cfe4d1759d5a78a2de1c09a6b73969b485`
- Vendor source commit checked:
  `xorbit/LiFePO4wered-Pi@25e816145910af7cced5b8f2c61cb4512b8f2263`;
  `lifepo4wered-data.c` SHA-256
  `7b77983b53f4c082b5eac2b58c033d04a5e98aa648aa1a2d111d5dfd484adf8b`;
  `lifepo4wered-data.h` SHA-256
  `a3f74a8ed3758042e057e8e72ee264cfaf90e3da900d44d4f8bed2f4ba351df4`
- Review scope: all 36 identifiers, addresses, widths, byte order, signed
  decoding, access, persistence, defaults, raw ranges, enumerations, sentinel
  values, scale factors, units, source pages, classifications, ownership, and
  constraint associations
- Discrepancies found and resolution: none; the pinned vendor tables agree
  with the catalogue's register-set 7 addresses, widths, access behavior,
  signed decoding flags, and scale factors
- Result: accepted as the register-set 7 catalogue. Catalogue acceptance alone
  does not authorize writes or flash operations; purpose-specific mutation
  services apply separate executable constraints and safety gates.

The reviewed interoperability fields have canonical test fingerprint
`2c358e2a1360180529baecc705ae4170d9528926c4317082e77d724fa6bf2f40`.
Authored titles and guidance are intentionally excluded from that fingerprint.

The detailed checklist in
[register-catalogue-review.md](register-catalogue-review.md) was applied to
every definition. Mutation policy and executable constraints remain separate
from catalogue correctness.
