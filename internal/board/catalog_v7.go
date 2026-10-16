package board

import "fmt"

const productPiPlus = "LiFePO4wered/Pi+"

// RegisterSet7Catalog is the revision-13 catalogue under review. Its labels
// are application-authored; source references point to printed brief pages.
func RegisterSet7Catalog() (Catalog, error) {
	definitions := []Definition{
		v7("I2C_REG_VER", "Register-set version", 0x00, 1, false, AccessReadOnly, PersistenceVolatile, 1, 1, "version", SafetyReadOnly, OwnerNone, numeric(7), bounds(0, 255), nil, nil, 19),
		v7("I2C_ADDRESS", "I2C address", 0x01, 1, false, AccessReadWrite, PersistenceFlash, 1, 1, "address", SafetyConnectivity, OwnerNone, numeric(0x43), bounds(0, 0x7f), nil, []string{"i2c-address-host-match"}, 20),
		v7("LED_STATE", "Power LED state", 0x02, 1, false, AccessReadWrite, PersistenceFlash, 1, 1, "enum", SafetyOrdinary, OwnerNone, numeric(1), bounds(0, 3), choices(0, "off", 1, "on", 2, "pulsing", 3, "fast-flash"), nil, 20),
		v7("TOUCH_CAP_CYCLES", "Touch charge cycles", 0x03, 1, false, AccessReadWrite, PersistenceFlash, 1, 1, "cycles", SafetyAdvanced, OwnerNone, numeric(0), bounds(0, 255), nil, []string{"touch-mode-parameters"}, 20, 21),
		v7("TOUCH_THRESHOLD", "Touch threshold", 0x04, 1, false, AccessReadWrite, PersistenceFlash, 1, 1, "count", SafetyAdvanced, OwnerNone, numeric(12), bounds(0, 255), nil, []string{"touch-mode-parameters"}, 21),
		v7("TOUCH_HYSTERESIS", "Touch hysteresis", 0x05, 1, false, AccessReadWrite, PersistenceFlash, 1, 1, "count", SafetyAdvanced, OwnerNone, numeric(2), bounds(0, 255), nil, []string{"touch-mode-parameters"}, 21),
		v7("DCO_RSEL", "Clock range calibration", 0x06, 1, false, AccessReadWrite, PersistenceFlash, 1, 1, "raw", SafetyFactory, OwnerNone, factory(), bounds(0, 255), nil, nil, 21, 22),
		v7("DCO_DCOMOD", "Clock modulation calibration", 0x07, 1, false, AccessReadWrite, PersistenceFlash, 1, 1, "raw", SafetyFactory, OwnerNone, factory(), bounds(0, 255), nil, nil, 22),
		v7("VBAT_MIN", "Emergency battery cutoff", 0x08, 2, true, AccessReadWrite, PersistenceFlash, 625, 1023, "mV", SafetyAdvanced, OwnerNone, numeric(4665), bounds(0, 8191), nil, []string{"battery-threshold-order"}, 23),
		v7("VBAT_SHDN", "Battery shutdown threshold", 0x0a, 2, true, AccessReadWrite, PersistenceFlash, 625, 1023, "mV", SafetyAdvanced, OwnerNone, numeric(4829), bounds(0, 8191), nil, []string{"battery-threshold-order"}, 23),
		v7("VBAT_BOOT", "Battery boot threshold", 0x0c, 2, true, AccessReadWrite, PersistenceFlash, 625, 1023, "mV", SafetyAdvanced, OwnerNone, numeric(5156), bounds(0, 8191), nil, []string{"battery-threshold-order"}, 24),
		v7("VOUT_MAX", "External-output boot guard", 0x0e, 2, true, AccessReadWrite, PersistenceFlash, 65705, 102300, "mV", SafetyAdvanced, OwnerNone, numeric(5449), bounds(0, 8191), nil, nil, 24),
		v7("VIN_THRESHOLD", "Input-voltage threshold", 0x10, 2, true, AccessReadWrite, PersistenceFlash, 317154, 102300, "mV", SafetyAdvanced, OwnerNone, numeric(1451), bounds(0, 8191), nil, []string{"auto-boot-input-threshold", "auto-shutdown-input-threshold"}, 25),
		v7("VBAT_OFFSET", "Battery-voltage calibration offset", 0x12, 2, true, AccessReadWrite, PersistenceFlash, 5000, 1023, "mV", SafetyFactory, OwnerNone, factory(), bounds(-32768, 32767), nil, nil, 26),
		v7("VOUT_OFFSET", "Output-voltage calibration offset", 0x14, 2, true, AccessReadWrite, PersistenceFlash, 525641, 102300, "mV", SafetyFactory, OwnerNone, factory(), bounds(-32768, 32767), nil, nil, 26),
		v7("VIN_OFFSET", "Input-voltage calibration offset", 0x16, 2, true, AccessReadWrite, PersistenceFlash, 253723, 10230, "mV", SafetyFactory, OwnerNone, factory(), bounds(-32768, 32767), nil, nil, 26),
		v7("IOUT_OFFSET", "Output-current calibration offset", 0x18, 2, true, AccessReadWrite, PersistenceFlash, 581395, 102300, "mA", SafetyAdvanced, OwnerNone, numeric(0), bounds(-32768, 32767), nil, nil, 26),
		v7("IOUT_SHDN_THRESHOLD", "Shutdown-current threshold", 0x1a, 2, true, AccessReadWrite, PersistenceFlash, 72674, 102300, "mA", SafetyAdvanced, OwnerNone, numeric(0), bounds(0, 8191), nil, []string{"shutdown-detection-mode", "shutdown-timeout"}, 25),
		v7("SHDN_DELAY", "Post-shutdown power delay", 0x1c, 2, false, AccessReadWrite, PersistenceFlash, 1, 1, "ticks", SafetyAdvanced, OwnerDaemon, numeric(40), bounds(0, 65535), nil, []string{"shutdown-detection-mode", "shutdown-timeout"}, 28),
		v7("AUTO_SHDN_TIME", "Input-loss shutdown delay", 0x1e, 2, false, AccessReadWrite, PersistenceFlash, 1, 1, "minutes", SafetyAdvanced, OwnerNone, numeric(0xffff), bounds(0, 65535), nil, []string{"auto-shutdown-input-threshold"}, 29),
		v7("AUTO_BOOT", "Automatic boot mode", 0x20, 1, false, AccessReadWrite, PersistenceFlash, 1, 1, "enum", SafetyAdvanced, OwnerNone, numeric(0), bounds(0, 7), choices(0, "off", 1, "battery", 2, "battery-smart", 3, "input", 4, "input-smart", 5, "no-input", 6, "no-input-smart", 7, "input-smart-armed"), []string{"auto-boot-input-threshold", "battery-threshold-order"}, 27),
		v7("PI_BOOT_TO", "Boot timeout", 0x21, 1, false, AccessReadWrite, PersistenceFlash, 10, 1, "seconds", SafetyAdvanced, OwnerDaemon, numeric(30), bounds(0, 255), nil, nil, 30),
		v7("PI_SHDN_TO", "Shutdown timeout", 0x22, 1, false, AccessReadWrite, PersistenceFlash, 10, 1, "seconds", SafetyAdvanced, OwnerDaemon, numeric(12), bounds(0, 255), nil, []string{"shutdown-timeout"}, 30),
		v7("WATCHDOG_CFG", "Watchdog action", 0x23, 1, false, AccessReadWrite, PersistenceFlash, 1, 1, "enum", SafetyAdvanced, OwnerUserApp, numeric(0), bounds(0, 2), choices(0, "off", 1, "alert", 2, "shutdown"), []string{"watchdog-timing"}, 31),
		v7("WATCHDOG_GRACE", "Watchdog startup grace", 0x24, 1, false, AccessReadWrite, PersistenceFlash, 10, 1, "seconds", SafetyAdvanced, OwnerUserApp, numeric(2), bounds(0, 255), nil, []string{"watchdog-timing"}, 32),
		v7("CFG_WRITE", "Flash configuration action", 0x25, 1, false, AccessReadWrite, PersistenceVolatile, 1, 1, "action", SafetyOperational, OwnerNone, numeric(0), bounds(0, 255), choices(0x46, "commit-configuration"), nil, 34),
		v7("WAKE_TIME", "Relative wake countdown", 0x26, 2, false, AccessReadWrite, PersistenceVolatile, 1, 1, "minutes", SafetyAdvanced, OwnerScheduler, numeric(0), bounds(0, 65535), nil, []string{"single-wake-owner", "boot-classification"}, 28),
		v7("RTC_TIME", "Board real-time clock", 0x28, 4, false, AccessReadWrite, PersistenceVolatile, 1, 1, "unix-seconds", SafetyOperational, OwnerDaemon, dynamic(), bounds(0, 4294967295), nil, []string{"rtc-monotonicity"}, 30),
		v7("RTC_WAKE_TIME", "Absolute wake time", 0x2c, 4, false, AccessReadWrite, PersistenceVolatile, 1, 1, "unix-seconds", SafetyAdvanced, OwnerScheduler, numeric(0), bounds(0, 4294967295), nil, []string{"single-wake-owner", "boot-classification", "rtc-monotonicity"}, 31),
		v7("WATCHDOG_TIMER", "Live watchdog countdown", 0x30, 1, false, AccessReadWrite, PersistenceVolatile, 10, 1, "seconds", SafetyOperational, OwnerUserApp, dynamic(), bounds(0, 255), nil, []string{"watchdog-timing"}, 33),
		v7("PI_RUNNING", "Host-running handshake", 0x31, 1, false, AccessReadWrite, PersistenceVolatile, 1, 1, "boolean", SafetyOperational, OwnerDaemon, dynamic(), bounds(0, 1), choices(0, "not-running", 1, "running"), nil, 33),
		v7("VBAT", "Battery voltage", 0x32, 2, true, AccessReadOnly, PersistenceVolatile, 625, 1023, "mV", SafetyReadOnly, OwnerNone, unspecified(), bounds(0, 8191), nil, nil, 22),
		v7("VOUT", "Output voltage", 0x34, 2, true, AccessReadOnly, PersistenceVolatile, 65705, 102300, "mV", SafetyReadOnly, OwnerNone, unspecified(), bounds(0, 8191), nil, nil, 22, 23),
		v7("VIN", "Input voltage", 0x36, 2, true, AccessReadOnly, PersistenceVolatile, 317154, 102300, "mV", SafetyReadOnly, OwnerNone, unspecified(), bounds(0, 8191), nil, nil, 22),
		v7("IOUT", "Output current", 0x38, 2, true, AccessReadOnly, PersistenceVolatile, 72674, 102300, "mA", SafetyReadOnly, OwnerNone, unspecified(), bounds(0, 8191), nil, nil, 23),
		v7("TOUCH_STATE", "Button sample state", 0x3a, 1, false, AccessReadOnly, PersistenceVolatile, 1, 1, "bitfield", SafetyReadOnly, OwnerNone, unspecified(), bounds(0, 15), nil, nil, 20),
	}
	for index := range definitions {
		guidance, ok := registerSet7Guidance[definitions[index].Register]
		if !ok {
			return Catalog{}, fmt.Errorf("missing authored guidance for %s", definitions[index].Register)
		}
		definitions[index].Purpose = guidance.purpose
		definitions[index].Behavior = guidance.behavior
		definitions[index].SafetyGuidance = guidance.safety
		switch definitions[index].Register {
		case "IOUT_SHDN_THRESHOLD", "PI_BOOT_TO", "PI_SHDN_TO", "WAKE_TIME", "RTC_WAKE_TIME":
			definitions[index].SentinelValues = []int64{0}
		case "AUTO_SHDN_TIME":
			definitions[index].SentinelValues = []int64{0xffff}
		}
		switch definitions[index].Register {
		case "RTC_TIME", "RTC_WAKE_TIME":
			definitions[index].Interpretation = InterpretationUnixTime
		}
	}
	return NewCatalog(definitions)
}

type defaultSpec struct {
	kind DefaultKind
	raw  *int64
}

type boundSpec struct{ minimum, maximum *int64 }

func v7(register Register, title string, address uint8, width uint8, signed bool, access Access, persistence Persistence, numerator, denominator int64, unit string, safety SafetyClass, owner Owner, defaultValue defaultSpec, limits boundSpec, enum []Choice, constraints []string, pages ...int) Definition {
	order := ByteOrderLittleEndian
	if width == 1 {
		order = ByteOrderNotApplicable
	}
	return Definition{Register: register, Title: title, ProductVariants: []string{productPiPlus}, RegisterSet: 7, Address: address, WidthBytes: width, ByteOrder: order, Signed: signed, Access: access, Persistence: persistence, ScaleNumerator: numerator, ScaleDenominator: denominator, Unit: unit, SafetyClass: safety, Owner: owner, DefaultKind: defaultValue.kind, RawDefault: defaultValue.raw, RawMinimum: limits.minimum, RawMaximum: limits.maximum, Choices: enum, ConstraintIDs: constraints, Source: SourceReference{Document: "LiFePO4wered/Pi+ Product Brief", Revision: "13", Pages: pages}}
}

func numeric(value int64) defaultSpec {
	return defaultSpec{kind: DefaultNumeric, raw: int64Pointer(value)}
}
func factory() defaultSpec     { return defaultSpec{kind: DefaultFactory} }
func dynamic() defaultSpec     { return defaultSpec{kind: DefaultDynamic} }
func unspecified() defaultSpec { return defaultSpec{kind: DefaultUnspecified} }
func bounds(minimum, maximum int64) boundSpec {
	return boundSpec{minimum: int64Pointer(minimum), maximum: int64Pointer(maximum)}
}
func int64Pointer(value int64) *int64 { return &value }

func choices(valuesAndNames ...any) []Choice {
	result := make([]Choice, 0, len(valuesAndNames)/2)
	for index := 0; index < len(valuesAndNames); index += 2 {
		result = append(result, Choice{Value: int64(valuesAndNames[index].(int)), Name: valuesAndNames[index+1].(string)})
	}
	return result
}
