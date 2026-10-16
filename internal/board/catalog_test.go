package board

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestCatalogOwnsAndReturnsCopies(t *testing.T) {
	input := []Definition{registerVersionDefinition()}
	catalog, err := NewCatalog(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].Title = "changed input"
	input[0].Source.Pages[0] = 99

	first, ok := catalog.Find(7, "I2C_REG_VER")
	if !ok || first.Title != "Register-set version" || first.Source.Pages[0] != 19 {
		t.Fatalf("Find() = %+v, %v", first, ok)
	}
	first.Title = "changed output"
	first.Source.Pages[0] = 88
	second, _ := catalog.Find(7, "I2C_REG_VER")
	if second.Title != "Register-set version" || second.Source.Pages[0] != 19 {
		t.Fatalf("catalog was mutated through result: %+v", second)
	}
}

func TestCatalogRejectsIncompleteDefinition(t *testing.T) {
	definition := registerVersionDefinition()
	definition.Source.Pages = nil
	if _, err := NewCatalog([]Definition{definition}); err == nil {
		t.Fatal("NewCatalog accepted a definition without a source page")
	}
}

func TestCatalogRejectsUnsafeMetadata(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Definition)
		match  string
	}{
		{name: "missing bounds", change: func(value *Definition) { value.RawMaximum = nil }, match: "raw minimum and maximum"},
		{name: "representation overflow", change: func(value *Definition) { *value.RawMaximum = 256 }, match: "representation"},
		{name: "writable without guidance", change: func(value *Definition) {
			value.Access = AccessReadWrite
			value.SafetyClass = SafetyOrdinary
			value.SafetyGuidance = ""
		}, match: "safety guidance"},
		{name: "choice out of range", change: func(value *Definition) { value.Choices = []Choice{{Value: 256, Name: "future"}} }, match: "choice value"},
		{name: "duplicate sentinel", change: func(value *Definition) { value.SentinelValues = []int64{0, 0} }, match: "duplicate sentinel"},
		{name: "empty constraint", change: func(value *Definition) { value.ConstraintIDs = []string{""} }, match: "constraint identifier"},
		{name: "invalid source page", change: func(value *Definition) { value.Source.Pages = []int{0} }, match: "source pages"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := registerVersionDefinition()
			test.change(&definition)
			_, err := NewCatalog([]Definition{definition})
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("NewCatalog() error = %v, want %q", err, test.match)
			}
		})
	}
}

func TestCatalogRejectsOverlappingAddresses(t *testing.T) {
	first := registerVersionDefinition()
	first.WidthBytes = 2
	first.ByteOrder = ByteOrderLittleEndian
	second := registerVersionDefinition()
	second.Register = "SECOND"
	second.Address = 1
	if _, err := NewCatalog([]Definition{first, second}); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("NewCatalog() overlap error = %v", err)
	}
}

func TestRegisterSet7CatalogIsComplete(t *testing.T) {
	catalog, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	definitions := catalog.Definitions()
	if len(definitions) != 36 {
		t.Fatalf("definition count = %d, want 36", len(definitions))
	}
	want := map[Register]uint8{
		"I2C_REG_VER": 0x00, "I2C_ADDRESS": 0x01, "LED_STATE": 0x02,
		"TOUCH_CAP_CYCLES": 0x03, "TOUCH_THRESHOLD": 0x04, "TOUCH_HYSTERESIS": 0x05,
		"DCO_RSEL": 0x06, "DCO_DCOMOD": 0x07, "VBAT_MIN": 0x08,
		"VBAT_SHDN": 0x0a, "VBAT_BOOT": 0x0c, "VOUT_MAX": 0x0e,
		"VIN_THRESHOLD": 0x10, "VBAT_OFFSET": 0x12, "VOUT_OFFSET": 0x14,
		"VIN_OFFSET": 0x16, "IOUT_OFFSET": 0x18, "IOUT_SHDN_THRESHOLD": 0x1a,
		"SHDN_DELAY": 0x1c, "AUTO_SHDN_TIME": 0x1e, "AUTO_BOOT": 0x20,
		"PI_BOOT_TO": 0x21, "PI_SHDN_TO": 0x22, "WATCHDOG_CFG": 0x23,
		"WATCHDOG_GRACE": 0x24, "CFG_WRITE": 0x25, "WAKE_TIME": 0x26,
		"RTC_TIME": 0x28, "RTC_WAKE_TIME": 0x2c, "WATCHDOG_TIMER": 0x30,
		"PI_RUNNING": 0x31, "VBAT": 0x32, "VOUT": 0x34, "VIN": 0x36,
		"IOUT": 0x38, "TOUCH_STATE": 0x3a,
	}
	for _, definition := range definitions {
		address, ok := want[definition.Register]
		if !ok {
			t.Errorf("unexpected register %s", definition.Register)
			continue
		}
		if definition.Address != address {
			t.Errorf("%s address = %#x, want %#x", definition.Register, definition.Address, address)
		}
		delete(want, definition.Register)
	}
	if len(want) != 0 {
		t.Errorf("missing registers: %v", want)
	}
}

func TestRegisterSet7DocumentedConversions(t *testing.T) {
	catalog, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		register    Register
		raw         int64
		engineering int64
	}{
		{register: "VBAT_MIN", raw: 4665, engineering: 2850},
		{register: "VBAT_SHDN", raw: 4829, engineering: 2950},
		{register: "VBAT_BOOT", raw: 5156, engineering: 3150},
		{register: "VOUT_MAX", raw: 5449, engineering: 3500},
		{register: "VIN_THRESHOLD", raw: 1451, engineering: 4498},
		{register: "PI_BOOT_TO", raw: 30, engineering: 300},
		{register: "PI_SHDN_TO", raw: 12, engineering: 120},
		{register: "WATCHDOG_GRACE", raw: 2, engineering: 20},
	}
	for _, test := range tests {
		definition, ok := catalog.Find(7, test.register)
		if !ok {
			t.Fatalf("missing %s", test.register)
		}
		got, err := definition.EngineeringValue(test.raw)
		if err != nil || got != test.engineering {
			t.Errorf("%s EngineeringValue(%d) = %d, %v; want %d", test.register, test.raw, got, err, test.engineering)
		}
		raw, err := definition.RawValue(test.engineering)
		if err != nil || raw != test.raw {
			t.Errorf("%s RawValue(%d) = %d, %v; want %d", test.register, test.engineering, raw, err, test.raw)
		}
	}
}

func TestRegisterSet7ConversionBounds(t *testing.T) {
	catalog, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range catalog.Definitions() {
		for _, raw := range []int64{*definition.RawMinimum, *definition.RawMaximum} {
			if _, err := definition.EngineeringValue(raw); err != nil {
				t.Errorf("%s boundary %d: %v", definition.Register, raw, err)
			}
		}
		if _, err := definition.EngineeringValue(*definition.RawMinimum - 1); err == nil {
			t.Errorf("%s accepted raw value below minimum", definition.Register)
		}
		if _, err := definition.EngineeringValue(*definition.RawMaximum + 1); err == nil {
			t.Errorf("%s accepted raw value above maximum", definition.Register)
		}
		if _, err := definition.RawValue(-1_000_000_000_000); err == nil {
			t.Errorf("%s accepted engineering value far below its raw representation", definition.Register)
		}
		if _, err := definition.RawValue(1_000_000_000_000); err == nil {
			t.Errorf("%s accepted engineering value far above its raw representation", definition.Register)
		}
	}
}

func TestRegisterSet7HasAuthoredGuidance(t *testing.T) {
	catalog, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range catalog.Definitions() {
		if definition.Purpose == "" || definition.Behavior == "" {
			t.Errorf("%s is missing authored guidance", definition.Register)
		}
		if definition.Access == AccessReadWrite && definition.SafetyGuidance == "" {
			t.Errorf("writable %s is missing safety guidance", definition.Register)
		}
	}
}

func TestRegisterSet7TimeInterpretationsAreExplicit(t *testing.T) {
	catalog, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range catalog.Definitions() {
		want := InterpretationNone
		if definition.Register == "RTC_TIME" || definition.Register == "RTC_WAKE_TIME" {
			want = InterpretationUnixTime
		}
		if definition.Interpretation != want {
			t.Errorf("%s interpretation = %q, want %q", definition.Register, definition.Interpretation, want)
		}
	}
}

func TestRegisterSet7SentinelsAndSourceReferencesAreExhaustive(t *testing.T) {
	catalog, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	wantSentinels := map[Register]int64{
		"IOUT_SHDN_THRESHOLD": 0, "AUTO_SHDN_TIME": 0xffff, "PI_BOOT_TO": 0,
		"PI_SHDN_TO": 0, "WAKE_TIME": 0, "RTC_WAKE_TIME": 0,
	}
	for _, definition := range catalog.Definitions() {
		if definition.Source.Document != "LiFePO4wered/Pi+ Product Brief" || definition.Source.Revision != "13" || len(definition.Source.Pages) == 0 {
			t.Errorf("%s source = %+v", definition.Register, definition.Source)
		}
		want, hasSentinel := wantSentinels[definition.Register]
		if !hasSentinel {
			if len(definition.SentinelValues) != 0 {
				t.Errorf("%s has unexpected sentinels %v", definition.Register, definition.SentinelValues)
			}
			continue
		}
		if len(definition.SentinelValues) != 1 || definition.SentinelValues[0] != want {
			t.Errorf("%s sentinels = %v, want [%d]", definition.Register, definition.SentinelValues, want)
		}
		delete(wantSentinels, definition.Register)
	}
	if len(wantSentinels) != 0 {
		t.Errorf("missing sentinels: %v", wantSentinels)
	}
}

func TestRegisterSet7ReviewedFactsFingerprint(t *testing.T) {
	catalog, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	definitions := catalog.Definitions()
	for index := range definitions {
		definitions[index].Title = ""
		definitions[index].Purpose = ""
		definitions[index].Behavior = ""
		definitions[index].SafetyGuidance = ""
		definitions[index].Interpretation = InterpretationNone
	}
	encoded, err := json.Marshal(definitions)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	const reviewed = "2c358e2a1360180529baecc705ae4170d9528926c4317082e77d724fa6bf2f40"
	if got := hex.EncodeToString(digest[:]); got != reviewed {
		t.Fatalf("reviewed fact fingerprint = %s, want %s", got, reviewed)
	}
}

func TestRegisterSet7ConstraintVocabulary(t *testing.T) {
	catalog, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"auto-boot-input-threshold": false, "auto-shutdown-input-threshold": false,
		"battery-threshold-order": false, "boot-classification": false,
		"i2c-address-host-match": false, "rtc-monotonicity": false,
		"shutdown-detection-mode": false, "shutdown-timeout": false,
		"single-wake-owner": false, "touch-mode-parameters": false,
		"watchdog-timing": false,
	}
	for _, definition := range catalog.Definitions() {
		for _, constraint := range definition.ConstraintIDs {
			if _, ok := want[constraint]; !ok {
				t.Errorf("%s uses unknown constraint %q", definition.Register, constraint)
				continue
			}
			want[constraint] = true
		}
	}
	for constraint, used := range want {
		if !used {
			t.Errorf("reviewed constraint %q is unused", constraint)
		}
	}
}

func registerVersionDefinition() Definition {
	return Definition{
		Register:         "I2C_REG_VER",
		Title:            "Register-set version",
		Purpose:          "Identifies the active register layout.",
		Behavior:         "Read-only compatibility value.",
		ProductVariants:  []string{"LiFePO4wered/Pi+"},
		RegisterSet:      7,
		Address:          0,
		WidthBytes:       1,
		ByteOrder:        ByteOrderNotApplicable,
		Access:           AccessReadOnly,
		Persistence:      PersistenceVolatile,
		ScaleNumerator:   1,
		ScaleDenominator: 1,
		Unit:             "version",
		SafetyClass:      SafetyReadOnly,
		Owner:            OwnerNone,
		DefaultKind:      DefaultNumeric,
		RawDefault:       int64Pointer(7),
		RawMinimum:       int64Pointer(0),
		RawMaximum:       int64Pointer(255),
		Source: SourceReference{
			Document: "LiFePO4wered/Pi+ Product Brief",
			Revision: "13",
			Pages:    []int{19},
		},
	}
}
