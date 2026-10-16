package registeredit

import (
	"fmt"
	"slices"

	"github.com/sinoia/lifepowerctl/internal/board"
)

type ConstraintProvenance string

const (
	ProvenanceManufacturerExplicit ConstraintProvenance = "manufacturer-explicit"
	ProvenanceManufacturerDerived  ConstraintProvenance = "manufacturer-derived"
	ProvenanceApplicationPolicy    ConstraintProvenance = "application-policy"
)

type ConstraintDefinition struct {
	ID         string
	Registers  []board.Register
	Inputs     []board.Register
	Provenance ConstraintProvenance
	Summary    string
	Executable bool
}

// RegisterSet7Constraints is the reviewed vocabulary attached to catalogue
// entries. A listed but non-executable relationship keeps its advanced
// registers locked; it is never treated as validation merely because it has a
// descriptive label.
func RegisterSet7Constraints() []ConstraintDefinition {
	return []ConstraintDefinition{
		{ID: "auto-boot-input-threshold", Registers: []board.Register{"AUTO_BOOT", "VIN_THRESHOLD", "VBAT_BOOT"}, Provenance: ProvenanceManufacturerExplicit, Summary: "Input-dependent automatic boot modes use VIN_THRESHOLD and still require sufficient battery voltage."},
		{ID: "auto-shutdown-input-threshold", Registers: []board.Register{"AUTO_SHDN_TIME", "VIN_THRESHOLD"}, Provenance: ProvenanceManufacturerExplicit, Summary: "Input-loss shutdown timing depends on the same VIN_THRESHOLD used to decide whether input is present."},
		{ID: "battery-threshold-order", Registers: []board.Register{"VBAT_MIN", "VBAT_SHDN", "VBAT_BOOT", "AUTO_BOOT"}, Inputs: []board.Register{"VBAT_MIN", "VBAT_SHDN", "VBAT_BOOT"}, Provenance: ProvenanceManufacturerDerived, Summary: "Emergency cutoff must be below orderly shutdown, which must be below the boot threshold.", Executable: true},
		{ID: "boot-classification", Registers: []board.Register{"WAKE_TIME", "RTC_WAKE_TIME"}, Provenance: ProvenanceApplicationPolicy, Summary: "Wake state contributes to conservative boot classification and must remain scheduler-owned."},
		{ID: "i2c-address-host-match", Registers: []board.Register{"I2C_ADDRESS"}, Provenance: ProvenanceApplicationPolicy, Summary: "Board and host addresses must migrate together through a dedicated connectivity workflow."},
		{ID: "rtc-monotonicity", Registers: []board.Register{"RTC_TIME", "RTC_WAKE_TIME"}, Provenance: ProvenanceManufacturerDerived, Summary: "Absolute wake interpretation depends on a valid current RTC value and ordering in time."},
		{ID: "shutdown-detection-mode", Registers: []board.Register{"IOUT_SHDN_THRESHOLD", "SHDN_DELAY"}, Provenance: ProvenanceManufacturerExplicit, Summary: "Zero selects UART shutdown detection; a nonzero current threshold selects current-based detection."},
		{ID: "shutdown-timeout", Registers: []board.Register{"IOUT_SHDN_THRESHOLD", "SHDN_DELAY", "PI_SHDN_TO"}, Provenance: ProvenanceManufacturerDerived, Summary: "Detection method, post-detection delay, and fallback timeout jointly determine when host power can be removed."},
		{ID: "single-wake-owner", Registers: []board.Register{"WAKE_TIME", "RTC_WAKE_TIME"}, Provenance: ProvenanceApplicationPolicy, Summary: "Only the duty-cycle controller may program or disarm wake registers."},
		{ID: "touch-mode-parameters", Registers: []board.Register{"TOUCH_CAP_CYCLES", "TOUCH_THRESHOLD", "TOUCH_HYSTERESIS"}, Provenance: ProvenanceManufacturerExplicit, Summary: "Zero charge cycles selects mechanical-switch mode; nonzero cycles enable capacitive mode with matching threshold and hysteresis."},
		{ID: "watchdog-timing", Registers: []board.Register{"WATCHDOG_CFG", "WATCHDOG_GRACE", "WATCHDOG_TIMER"}, Provenance: ProvenanceManufacturerExplicit, Summary: "Watchdog action, startup grace, and the live timer form one owner-managed workflow."},
	}
}

type ConstraintSet struct {
	catalogue board.Catalog
	rules     []ConstraintDefinition
}

func NewConstraintSet(catalogue board.Catalog) (*ConstraintSet, error) {
	rules := RegisterSet7Constraints()
	known := make(map[string]ConstraintDefinition, len(rules))
	for _, rule := range rules {
		if rule.ID == "" || rule.Summary == "" || len(rule.Registers) == 0 {
			return nil, fmt.Errorf("constraint definitions require an ID, summary, and registers")
		}
		if _, duplicate := known[rule.ID]; duplicate {
			return nil, fmt.Errorf("duplicate constraint %s", rule.ID)
		}
		if rule.Provenance != ProvenanceManufacturerExplicit && rule.Provenance != ProvenanceManufacturerDerived && rule.Provenance != ProvenanceApplicationPolicy {
			return nil, fmt.Errorf("constraint %s has invalid provenance", rule.ID)
		}
		for _, register := range rule.Registers {
			if _, ok := catalogue.Find(7, register); !ok {
				return nil, fmt.Errorf("constraint %s references unknown register %s", rule.ID, register)
			}
		}
		for _, input := range rule.Inputs {
			if !slices.Contains(rule.Registers, input) {
				return nil, fmt.Errorf("constraint %s input %s is not an affected register", rule.ID, input)
			}
		}
		known[rule.ID] = rule
	}
	for _, definition := range catalogue.Definitions() {
		for _, id := range definition.ConstraintIDs {
			rule, ok := known[id]
			if !ok {
				return nil, fmt.Errorf("register %s references undefined constraint %s", definition.Register, id)
			}
			if !slices.Contains(rule.Registers, definition.Register) {
				return nil, fmt.Errorf("constraint %s omits attached register %s", id, definition.Register)
			}
		}
	}
	return &ConstraintSet{catalogue: catalogue, rules: rules}, nil
}

func (set *ConstraintSet) Definitions() []ConstraintDefinition {
	result := make([]ConstraintDefinition, len(set.rules))
	for index, rule := range set.rules {
		result[index] = rule
		result[index].Registers = append([]board.Register(nil), rule.Registers...)
		result[index].Inputs = append([]board.Register(nil), rule.Inputs...)
	}
	return result
}

type ConstraintViolation struct {
	ID         string
	Provenance ConstraintProvenance
	Summary    string
	Registers  []board.Register
}

// ValidateComplete evaluates every executable relationship against a complete
// raw configuration. Missing dependencies are errors rather than silently
// skipped checks.
func (set *ConstraintSet) ValidateComplete(values map[board.Register]int64) ([]ConstraintViolation, error) {
	for _, rule := range set.rules {
		if !rule.Executable {
			continue
		}
		inputs := rule.Inputs
		if len(inputs) == 0 {
			inputs = rule.Registers
		}
		for _, register := range inputs {
			value, ok := values[register]
			if !ok {
				return nil, fmt.Errorf("constraint %s requires %s", rule.ID, register)
			}
			definition, _ := set.catalogue.Find(7, register)
			if _, err := definition.EngineeringValue(value); err != nil {
				return nil, fmt.Errorf("constraint %s has invalid %s value: %w", rule.ID, register, err)
			}
		}
	}
	var violations []ConstraintViolation
	minimum, shutdown, boot := values["VBAT_MIN"], values["VBAT_SHDN"], values["VBAT_BOOT"]
	if !(minimum < shutdown && shutdown < boot) {
		rule := set.rule("battery-threshold-order")
		violations = append(violations, ConstraintViolation{ID: rule.ID, Provenance: rule.Provenance, Summary: rule.Summary, Registers: append([]board.Register(nil), rule.Registers...)})
	}
	return violations, nil
}

func (set *ConstraintSet) rule(id string) ConstraintDefinition {
	for _, rule := range set.rules {
		if rule.ID == id {
			return rule
		}
	}
	return ConstraintDefinition{}
}
