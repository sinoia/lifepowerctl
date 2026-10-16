package board

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

// VendorDevice is the narrowly write-capable vendor CLI adapter. Its mutation
// policy deliberately duplicates the initial register-editor boundary as
// defence in depth at the hardware edge.
type VendorDevice struct {
	runner  commandRunner
	limits  limits.Command
	catalog Catalog
	policy  vendorWritePolicy
}

type vendorWritePolicy uint8

const (
	vendorWriteOrdinary vendorWritePolicy = iota
	vendorWriteBatteryThresholds
	vendorWriteShutdownDelay
)

func NewVendorDevice(commandLimits limits.Command, catalog Catalog) (*VendorDevice, error) {
	if commandLimits.Timeout <= 0 || commandLimits.MaxStdoutBytes <= 0 || commandLimits.MaxStderrBytes <= 0 {
		return nil, fmt.Errorf("invalid vendor command limits")
	}
	if len(catalog.Definitions()) == 0 {
		return nil, fmt.Errorf("empty register catalogue")
	}
	return &VendorDevice{runner: execRunner{}, limits: commandLimits, catalog: catalog, policy: vendorWriteOrdinary}, nil
}

// NewVendorShutdownDelayDevice creates the purpose-specific shutdown-delay hardware
// edge. It permits exactly one SHDN_DELAY write and cannot select CFG_WRITE or
// any other register.
func NewVendorShutdownDelayDevice(commandLimits limits.Command, catalog Catalog) (*VendorDevice, error) {
	device, err := NewVendorDevice(commandLimits, catalog)
	if err != nil {
		return nil, err
	}
	device.policy = vendorWriteShutdownDelay
	return device, nil
}

// NewVendorBatteryThresholdDevice creates the purpose-specific hardware edge
// used only by the reviewed three-register battery-threshold workflow.
func NewVendorBatteryThresholdDevice(commandLimits limits.Command, catalog Catalog) (*VendorDevice, error) {
	device, err := NewVendorDevice(commandLimits, catalog)
	if err != nil {
		return nil, err
	}
	device.policy = vendorWriteBatteryThresholds
	return device, nil
}

func newVendorDevice(runner commandRunner, commandLimits limits.Command, catalog Catalog) *VendorDevice {
	return &VendorDevice{runner: runner, limits: commandLimits, catalog: catalog}
}

func (device *VendorDevice) Detect(ctx context.Context) (Identity, error) {
	arguments, err := VendorReadArgs("I2C_REG_VER")
	if err != nil {
		return Identity{}, err
	}
	result, err := device.runner.Run(ctx, VendorCLIPath, arguments, device.limits)
	if err != nil {
		return Identity{}, err
	}
	return DetectVendorIdentity(result.stdout)
}

func (device *VendorDevice) Read(ctx context.Context, registers ...Register) (map[Register]int64, error) {
	result := make(map[Register]int64, len(registers))
	for _, register := range registers {
		definition, ok := device.catalog.Find(7, register)
		if !ok {
			return nil, fmt.Errorf("%w: register set 7 register %s", ErrRegisterNotCatalogued, register)
		}
		operation := "GETHEX"
		if device.policy == vendorWriteBatteryThresholds {
			operation = "GET"
		}
		command, err := device.runner.Run(ctx, VendorCLIPath, []string{operation, string(register)}, device.limits)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", register, err)
		}
		if device.policy == vendorWriteBatteryThresholds {
			value, parseErr := ParseVendorInteger(command.stdout)
			minimum, minimumErr := definition.EngineeringValue(*definition.RawMinimum)
			maximum, maximumErr := definition.EngineeringValue(*definition.RawMaximum)
			if parseErr != nil || minimumErr != nil || maximumErr != nil || value < minimum || value > maximum {
				return nil, fmt.Errorf("read %s: %w", register, ErrInvalidVendorOutput)
			}
			result[register] = value
			continue
		}
		raw, parseErr := ParseVendorRaw(command.stdout, definition)
		if parseErr != nil {
			return nil, fmt.Errorf("read raw %s: %w", register, parseErr)
		}
		result[register] = raw
	}
	return result, nil
}

func (device *VendorDevice) Apply(ctx context.Context, changes []Change) (ApplyResult, error) {
	result := ApplyResult{Changes: make([]ChangeResult, 0, len(changes))}
	if err := device.validateChanges(changes); err != nil {
		return result, err
	}
	for _, change := range changes {
		definition, _ := device.catalog.Find(7, change.Register)
		engineering := change.Value
		if device.policy == vendorWriteOrdinary {
			var err error
			engineering, err = definition.EngineeringValue(change.Value)
			if err != nil {
				return result, err
			}
			if roundTrip, conversionErr := definition.RawValue(engineering); conversionErr != nil || roundTrip != change.Value {
				return result, fmt.Errorf("register %s raw value cannot be represented exactly by the vendor CLI", change.Register)
			}
		} else {
			minimum, minimumErr := definition.EngineeringValue(*definition.RawMinimum)
			maximum, maximumErr := definition.EngineeringValue(*definition.RawMaximum)
			if minimumErr != nil || maximumErr != nil || engineering < minimum || engineering > maximum {
				return result, fmt.Errorf("register %s scaled value is outside catalogue bounds", change.Register)
			}
		}
		_, err := device.runner.Run(ctx, VendorCLIPath, []string{"SET", string(change.Register), strconv.FormatInt(engineering, 10)}, device.limits)
		if err != nil {
			return result, fmt.Errorf("write %s: %w", change.Register, err)
		}
		item := ChangeResult{Change: change, Applied: true}
		result.Changes = append(result.Changes, item)
		values, err := device.Read(ctx, change.Register)
		if err != nil {
			return result, err
		}
		item.ReadBack = values[change.Register]
		item.Verified = item.ReadBack == change.Value
		result.Changes[len(result.Changes)-1] = item
		if !item.Verified {
			return result, fmt.Errorf("verify %s: %w", change.Register, ErrReadback)
		}
	}
	return result, nil
}

func (device *VendorDevice) validateChanges(changes []Change) error {
	if len(changes) == 0 || device.policy == vendorWriteOrdinary && len(changes) != 1 || device.policy == vendorWriteBatteryThresholds && len(changes) > 3 || device.policy == vendorWriteShutdownDelay && len(changes) != 1 {
		return fmt.Errorf("invalid temporary-write change count")
	}
	seen := make(map[Register]struct{}, len(changes))
	for _, change := range changes {
		definition, ok := device.catalog.Find(7, change.Register)
		if !ok {
			return fmt.Errorf("%w: register set 7 register %s", ErrRegisterNotCatalogued, change.Register)
		}
		allowed := initialTemporaryWritable(definition)
		if device.policy == vendorWriteBatteryThresholds {
			allowed = change.Register == "VBAT_MIN" || change.Register == "VBAT_SHDN" || change.Register == "VBAT_BOOT"
		} else if device.policy == vendorWriteShutdownDelay {
			allowed = change.Register == "SHDN_DELAY"
		}
		if !allowed {
			return fmt.Errorf("register %s is not allowed by this temporary-write adapter", change.Register)
		}
		if _, exists := seen[change.Register]; exists {
			return fmt.Errorf("register %s appears more than once", change.Register)
		}
		seen[change.Register] = struct{}{}
	}
	return nil
}

func initialTemporaryWritable(definition Definition) bool {
	if definition.Access != AccessReadWrite || definition.Owner != OwnerNone {
		return false
	}
	return definition.SafetyClass == SafetyOrdinary || definition.Register == "AUTO_BOOT" && definition.SafetyClass == SafetyAdvanced
}

func ParseVendorRaw(output []byte, definition Definition) (int64, error) {
	value := strings.TrimSpace(string(output))
	if len(value) < 3 || !strings.HasPrefix(value, "0x") || strings.ContainsAny(value, "\r\n \t") {
		return 0, fmt.Errorf("%w: expected one hexadecimal value", ErrInvalidVendorOutput)
	}
	unsigned, err := strconv.ParseUint(value[2:], 16, int(definition.WidthBytes)*8)
	if err != nil {
		return 0, fmt.Errorf("%w: hexadecimal value exceeds register width", ErrInvalidVendorOutput)
	}
	raw := int64(unsigned)
	if definition.Signed {
		bits := uint(definition.WidthBytes) * 8
		sign := uint64(1) << (bits - 1)
		if unsigned&sign != 0 {
			raw = int64(unsigned - (uint64(1) << bits))
		}
	}
	if definition.RawMinimum == nil || definition.RawMaximum == nil || raw < *definition.RawMinimum || raw > *definition.RawMaximum {
		return 0, fmt.Errorf("%w: raw value is outside catalogue bounds", ErrInvalidVendorOutput)
	}
	return raw, nil
}
