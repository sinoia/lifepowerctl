# Register constraint registry

This registry maps every constraint identifier in the reviewed register-set-7
catalogue to its affected registers, provenance, and implementation state. The
canonical manufacturer source remains the LiFePO4wered/Pi+ Product Brief,
revision 13 dated 2024-02-01. ADR-0003 applies: this document contains original
application summaries and references, not redistributed manufacturer prose.

Provenance has three explicit meanings:

- **Manufacturer explicit**: one described behaviour directly connects the
  listed settings.
- **Manufacturer derived**: the relationship follows from multiple described
  behaviours, but the summary is application-authored.
- **Application policy**: LifePower Control adds the restriction to preserve
  ownership, recovery, or conservative automation behaviour.

| Constraint | Affected registers | Provenance | Executable status |
| --- | --- | --- | --- |
| `battery-threshold-order` | `VBAT_MIN`, `VBAT_SHDN`, `VBAT_BOOT`, `AUTO_BOOT` | Manufacturer derived, pages 23–24 | Implemented: `VBAT_MIN < VBAT_SHDN < VBAT_BOOT`; no invented fixed hysteresis margin |
| `auto-boot-input-threshold` | `AUTO_BOOT`, `VIN_THRESHOLD`, `VBAT_BOOT` | Manufacturer explicit, pages 25 and 27–28 | Described only; controls remain locked |
| `auto-shutdown-input-threshold` | `AUTO_SHDN_TIME`, `VIN_THRESHOLD` | Manufacturer explicit, pages 25 and 29 | Described only; controls remain locked |
| `touch-mode-parameters` | `TOUCH_CAP_CYCLES`, `TOUCH_THRESHOLD`, `TOUCH_HYSTERESIS` | Manufacturer explicit, pages 20–21 | Described only; controls remain locked pending mode-specific rules and hardware recovery review |
| `shutdown-detection-mode` | `IOUT_SHDN_THRESHOLD`, `SHDN_DELAY` | Manufacturer explicit, pages 25 and 29 | Described only; controls remain locked |
| `shutdown-timeout` | `IOUT_SHDN_THRESHOLD`, `SHDN_DELAY`, `PI_SHDN_TO` | Manufacturer derived, pages 25, 29, and 30 | Described only; daemon-owned dependencies remain locked |
| `watchdog-timing` | `WATCHDOG_CFG`, `WATCHDOG_GRACE`, `WATCHDOG_TIMER` | Manufacturer explicit, pages 31–33 | Described only; user-application-owned workflow remains locked |
| `rtc-monotonicity` | `RTC_TIME`, `RTC_WAKE_TIME` | Manufacturer derived, pages 30–32 | Described only; daemon/scheduler-owned workflow remains locked |
| `single-wake-owner` | `WAKE_TIME`, `RTC_WAKE_TIME` | Application policy | Enforced by the generic-editor ownership lock and dedicated duty-cycle workflow |
| `boot-classification` | `WAKE_TIME`, `RTC_WAKE_TIME` | Application policy | Defined by ADR-0008; generic editing remains locked |
| `i2c-address-host-match` | `I2C_ADDRESS` | Application policy | Enforced by connectivity-critical lock; no generic editor planned |

An advanced register does not become editable merely because one relationship
is executable. Every constraint attached to it must be executable, its complete
dependency snapshot must be freshly readable, its write order and failure
behaviour must be reviewed, and the hardware boundary must separately allow
the operation.
