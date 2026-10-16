package board

import (
	"context"
	"fmt"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

// Reading is one value returned by the vendor CLI. EngineeringValue is the
// scaled integer printed by that CLI. RawValue is present only when the
// reviewed scale makes the printed value an unambiguous raw-register value.
type Reading struct {
	Definition       Definition
	EngineeringValue int64
	RawValue         *int64
	Choice           string
	Sentinel         bool
}

// VendorReader exposes only catalogue-approved, fixed GET operations.
type VendorReader struct {
	runner  commandRunner
	limits  limits.Command
	catalog Catalog
}

func NewVendorReader(commandLimits limits.Command, catalog Catalog) (*VendorReader, error) {
	if commandLimits.Timeout <= 0 || commandLimits.MaxStdoutBytes <= 0 || commandLimits.MaxStderrBytes <= 0 {
		return nil, fmt.Errorf("invalid vendor command limits")
	}
	if len(catalog.Definitions()) == 0 {
		return nil, fmt.Errorf("empty register catalogue")
	}
	return &VendorReader{runner: execRunner{}, limits: commandLimits, catalog: catalog}, nil
}

func newVendorReader(runner commandRunner, commandLimits limits.Command, catalog Catalog) *VendorReader {
	return &VendorReader{runner: runner, limits: commandLimits, catalog: catalog}
}

func (reader *VendorReader) Read(ctx context.Context, registerSet int, register Register) (Reading, error) {
	arguments, definition, err := CatalogVendorReadArgs(reader.catalog, registerSet, register)
	if err != nil {
		return Reading{}, err
	}
	result, err := reader.runner.Run(ctx, VendorCLIPath, arguments, reader.limits)
	if err != nil {
		return Reading{}, fmt.Errorf("read %s: %w", register, err)
	}
	value, err := ParseVendorInteger(result.stdout)
	if err != nil {
		return Reading{}, fmt.Errorf("read %s: %w", register, err)
	}
	minimum, minimumErr := definition.EngineeringValue(*definition.RawMinimum)
	maximum, maximumErr := definition.EngineeringValue(*definition.RawMaximum)
	if minimumErr != nil || maximumErr != nil || value < minimum || value > maximum {
		return Reading{}, fmt.Errorf("read %s: %w: value is outside catalogue bounds", register, ErrInvalidVendorOutput)
	}
	reading := Reading{Definition: definition, EngineeringValue: value}
	if raw, exact := exactRawValue(definition, value); exact {
		reading.RawValue = &raw
	}
	for _, choice := range definition.Choices {
		engineering, conversionErr := definition.EngineeringValue(choice.Value)
		if conversionErr == nil && engineering == value {
			reading.Choice = choice.Name
			break
		}
	}
	for _, sentinel := range definition.SentinelValues {
		engineering, conversionErr := definition.EngineeringValue(sentinel)
		if conversionErr == nil && engineering == value {
			reading.Sentinel = true
			break
		}
	}
	return reading, nil
}

func exactRawValue(definition Definition, engineering int64) (int64, bool) {
	// A slope below one maps multiple adjacent raw values to the same integer
	// output after vendor rounding, so the original register bytes are unknown.
	if definition.ScaleNumerator < definition.ScaleDenominator {
		return 0, false
	}
	center, err := definition.RawValue(engineering)
	if err != nil {
		return 0, false
	}
	var match int64
	found := false
	for candidate := center - 2; candidate <= center+2; candidate++ {
		roundTrip, conversionErr := definition.EngineeringValue(candidate)
		if conversionErr != nil || roundTrip != engineering {
			continue
		}
		if found {
			return 0, false
		}
		match = candidate
		found = true
	}
	return match, found
}

func (reading Reading) IsRawExact() bool {
	return reading.RawValue != nil
}
