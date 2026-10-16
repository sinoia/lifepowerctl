package board

import (
	"fmt"
	"slices"
	"strings"
)

type Access string

const (
	AccessReadOnly  Access = "read-only"
	AccessReadWrite Access = "read-write"
)

type Persistence string

const (
	PersistenceVolatile Persistence = "volatile"
	PersistenceFlash    Persistence = "flash-backed"
)

type ByteOrder string

const (
	ByteOrderNotApplicable ByteOrder = "not-applicable"
	ByteOrderLittleEndian  ByteOrder = "little-endian"
)

type SafetyClass string

const (
	SafetyReadOnly     SafetyClass = "read-only"
	SafetyOrdinary     SafetyClass = "ordinary"
	SafetyAdvanced     SafetyClass = "advanced"
	SafetyConnectivity SafetyClass = "connectivity-critical"
	SafetyOperational  SafetyClass = "operational-action"
	SafetyFactory      SafetyClass = "factory-calibration"
)

type Owner string

const (
	OwnerNone      Owner = "none"
	OwnerDaemon    Owner = "vendor-daemon"
	OwnerScheduler Owner = "duty-cycle"
	OwnerUserApp   Owner = "user-application"
)

type DefaultKind string

const (
	DefaultNumeric     DefaultKind = "numeric"
	DefaultFactory     DefaultKind = "factory-calibrated"
	DefaultDynamic     DefaultKind = "dynamic"
	DefaultUnspecified DefaultKind = "unspecified"
)

type Interpretation string

const (
	InterpretationNone     Interpretation = ""
	InterpretationUnixTime Interpretation = "unix-time"
)

type Choice struct {
	Value int64
	Name  string
}

type SourceReference struct {
	Document string
	Revision string
	Pages    []int
}

// Definition contains authored metadata and interoperable facts, not copied
// manufacturer prose. ScaleNumerator/ScaleDenominator describe the vendor
// engineering value per raw unit.
type Definition struct {
	Register         Register
	Title            string
	Purpose          string
	Behavior         string
	SafetyGuidance   string
	ProductVariants  []string
	RegisterSet      int
	Address          uint8
	WidthBytes       uint8
	ByteOrder        ByteOrder
	Signed           bool
	Access           Access
	Persistence      Persistence
	ScaleNumerator   int64
	ScaleDenominator int64
	Unit             string
	Interpretation   Interpretation `json:",omitempty"`
	SafetyClass      SafetyClass
	Owner            Owner
	DefaultKind      DefaultKind
	RawDefault       *int64
	RawMinimum       *int64
	RawMaximum       *int64
	SentinelValues   []int64
	Choices          []Choice
	ConstraintIDs    []string
	Source           SourceReference
}

type Catalog struct {
	definitions []Definition
}

func NewCatalog(definitions []Definition) (Catalog, error) {
	owned := cloneDefinitions(definitions)
	seen := make(map[string]struct{}, len(owned))
	occupied := make(map[string]Register)
	for index, definition := range owned {
		if err := validateDefinition(definition); err != nil {
			return Catalog{}, fmt.Errorf("definition %d: %w", index, err)
		}
		key := fmt.Sprintf("%d/%s", definition.RegisterSet, definition.Register)
		if _, exists := seen[key]; exists {
			return Catalog{}, fmt.Errorf("definition %d: duplicate %s", index, key)
		}
		seen[key] = struct{}{}
		for offset := 0; offset < int(definition.WidthBytes); offset++ {
			address := int(definition.Address) + offset
			if address > 0xff {
				return Catalog{}, fmt.Errorf("definition %d: %s extends beyond the register address space", index, definition.Register)
			}
			addressKey := fmt.Sprintf("%d/%02x", definition.RegisterSet, address)
			if existing, exists := occupied[addressKey]; exists {
				return Catalog{}, fmt.Errorf("definition %d: %s overlaps %s at address 0x%02x", index, definition.Register, existing, address)
			}
			occupied[addressKey] = definition.Register
		}
	}
	slices.SortFunc(owned, func(a, b Definition) int {
		if a.RegisterSet != b.RegisterSet {
			return a.RegisterSet - b.RegisterSet
		}
		if a.Address != b.Address {
			return int(a.Address) - int(b.Address)
		}
		if a.Register < b.Register {
			return -1
		}
		if a.Register > b.Register {
			return 1
		}
		return 0
	})
	return Catalog{definitions: owned}, nil
}

func (c Catalog) Definitions() []Definition {
	return cloneDefinitions(c.definitions)
}

func (c Catalog) Find(registerSet int, register Register) (Definition, bool) {
	for _, definition := range c.definitions {
		if definition.RegisterSet == registerSet && definition.Register == register {
			return cloneDefinition(definition), true
		}
	}
	return Definition{}, false
}

// EngineeringValue applies the same integer rounding rule as the reviewed
// vendor host implementation.
func (d Definition) EngineeringValue(raw int64) (int64, error) {
	if d.ScaleNumerator <= 0 || d.ScaleDenominator <= 0 {
		return 0, fmt.Errorf("%s: invalid scale", d.Register)
	}
	if d.RawMinimum != nil && raw < *d.RawMinimum || d.RawMaximum != nil && raw > *d.RawMaximum {
		return 0, fmt.Errorf("%s: raw value %d is outside catalogue bounds", d.Register, raw)
	}
	return (raw*d.ScaleNumerator + d.ScaleDenominator/2) / d.ScaleDenominator, nil
}

// RawValue converts an engineering value to the raw representation using the
// reviewed vendor rounding rule and rejects a result outside catalogue bounds.
func (d Definition) RawValue(engineering int64) (int64, error) {
	if d.ScaleNumerator <= 0 || d.ScaleDenominator <= 0 {
		return 0, fmt.Errorf("%s: invalid scale", d.Register)
	}
	raw := (engineering*d.ScaleDenominator + d.ScaleNumerator/2) / d.ScaleNumerator
	if d.RawMinimum != nil && raw < *d.RawMinimum || d.RawMaximum != nil && raw > *d.RawMaximum {
		return 0, fmt.Errorf("%s: converted raw value %d is outside catalogue bounds", d.Register, raw)
	}
	return raw, nil
}

func validateDefinition(definition Definition) error {
	if definition.Register == "" || definition.Title == "" || definition.Purpose == "" || definition.Behavior == "" {
		return fmt.Errorf("register, authored title, purpose, and behavior are required")
	}
	if len(definition.ProductVariants) == 0 {
		return fmt.Errorf("at least one product variant is required")
	}
	if err := validateUniqueStrings("product variant", definition.ProductVariants); err != nil {
		return err
	}
	if definition.RegisterSet <= 0 {
		return fmt.Errorf("register set must be positive")
	}
	if definition.WidthBytes != 1 && definition.WidthBytes != 2 && definition.WidthBytes != 4 {
		return fmt.Errorf("width must be 1, 2, or 4 bytes")
	}
	if definition.WidthBytes == 1 && definition.ByteOrder != ByteOrderNotApplicable {
		return fmt.Errorf("one-byte register must use not-applicable byte order")
	}
	if definition.WidthBytes > 1 && definition.ByteOrder != ByteOrderLittleEndian {
		return fmt.Errorf("multi-byte register must use little-endian byte order")
	}
	if definition.Access != AccessReadOnly && definition.Access != AccessReadWrite {
		return fmt.Errorf("invalid access %q", definition.Access)
	}
	if definition.Persistence != PersistenceVolatile && definition.Persistence != PersistenceFlash {
		return fmt.Errorf("invalid persistence %q", definition.Persistence)
	}
	if definition.RawMinimum == nil || definition.RawMaximum == nil {
		return fmt.Errorf("raw minimum and maximum are required")
	}
	representationMinimum, representationMaximum := rawRepresentationBounds(definition.WidthBytes, definition.Signed)
	if *definition.RawMinimum < representationMinimum || *definition.RawMaximum > representationMaximum {
		return fmt.Errorf("raw bounds exceed the %d-byte signed=%t representation", definition.WidthBytes, definition.Signed)
	}
	if definition.ScaleDenominator <= 0 || definition.ScaleNumerator <= 0 {
		return fmt.Errorf("scale must contain positive numerator and denominator")
	}
	switch definition.Interpretation {
	case InterpretationNone:
	case InterpretationUnixTime:
		if definition.Unit != "unix-seconds" || definition.Signed || definition.ScaleNumerator != 1 || definition.ScaleDenominator != 1 {
			return fmt.Errorf("unix-time interpretation requires unsigned, unscaled unix-seconds")
		}
	default:
		return fmt.Errorf("invalid interpretation %q", definition.Interpretation)
	}
	switch definition.SafetyClass {
	case SafetyReadOnly, SafetyOrdinary, SafetyAdvanced, SafetyConnectivity, SafetyOperational, SafetyFactory:
	default:
		return fmt.Errorf("invalid safety class %q", definition.SafetyClass)
	}
	if definition.Access == AccessReadOnly && definition.SafetyClass != SafetyReadOnly {
		return fmt.Errorf("read-only access requires the read-only safety class")
	}
	if definition.Access == AccessReadWrite && definition.SafetyClass == SafetyReadOnly {
		return fmt.Errorf("writable access cannot use the read-only safety class")
	}
	if definition.Access == AccessReadWrite && strings.TrimSpace(definition.SafetyGuidance) == "" {
		return fmt.Errorf("writable definition requires safety guidance")
	}
	switch definition.Owner {
	case OwnerNone, OwnerDaemon, OwnerScheduler, OwnerUserApp:
	default:
		return fmt.Errorf("invalid owner %q", definition.Owner)
	}
	switch definition.DefaultKind {
	case DefaultNumeric:
		if definition.RawDefault == nil {
			return fmt.Errorf("numeric default requires a raw value")
		}
	case DefaultFactory, DefaultDynamic, DefaultUnspecified:
		if definition.RawDefault != nil {
			return fmt.Errorf("non-numeric default cannot have a raw value")
		}
	default:
		return fmt.Errorf("invalid default kind %q", definition.DefaultKind)
	}
	if definition.RawMinimum != nil && definition.RawMaximum != nil && *definition.RawMinimum > *definition.RawMaximum {
		return fmt.Errorf("raw minimum exceeds maximum")
	}
	if definition.RawDefault != nil {
		if definition.RawMinimum != nil && *definition.RawDefault < *definition.RawMinimum {
			return fmt.Errorf("raw default is below minimum")
		}
		if definition.RawMaximum != nil && *definition.RawDefault > *definition.RawMaximum {
			return fmt.Errorf("raw default exceeds maximum")
		}
	}
	choiceValues := make(map[int64]struct{}, len(definition.Choices))
	choiceNames := make(map[string]struct{}, len(definition.Choices))
	for _, choice := range definition.Choices {
		if strings.TrimSpace(choice.Name) == "" {
			return fmt.Errorf("choice name is required")
		}
		if choice.Value < *definition.RawMinimum || choice.Value > *definition.RawMaximum {
			return fmt.Errorf("choice value %d is outside raw bounds", choice.Value)
		}
		if _, exists := choiceValues[choice.Value]; exists {
			return fmt.Errorf("duplicate choice value %d", choice.Value)
		}
		if _, exists := choiceNames[choice.Name]; exists {
			return fmt.Errorf("duplicate choice name %q", choice.Name)
		}
		choiceValues[choice.Value] = struct{}{}
		choiceNames[choice.Name] = struct{}{}
	}
	sentinels := make(map[int64]struct{}, len(definition.SentinelValues))
	for _, sentinel := range definition.SentinelValues {
		if sentinel < *definition.RawMinimum || sentinel > *definition.RawMaximum {
			return fmt.Errorf("sentinel value %d is outside raw bounds", sentinel)
		}
		if _, exists := sentinels[sentinel]; exists {
			return fmt.Errorf("duplicate sentinel value %d", sentinel)
		}
		sentinels[sentinel] = struct{}{}
	}
	if err := validateUniqueStrings("constraint identifier", definition.ConstraintIDs); err != nil {
		return err
	}
	if definition.Source.Document == "" || definition.Source.Revision == "" || len(definition.Source.Pages) == 0 {
		return fmt.Errorf("document, revision, and source page are required")
	}
	pages := make(map[int]struct{}, len(definition.Source.Pages))
	for _, page := range definition.Source.Pages {
		if page <= 0 {
			return fmt.Errorf("source pages must be positive")
		}
		if _, exists := pages[page]; exists {
			return fmt.Errorf("duplicate source page %d", page)
		}
		pages[page] = struct{}{}
	}
	return nil
}

func rawRepresentationBounds(width uint8, signed bool) (int64, int64) {
	bits := uint(width) * 8
	if signed {
		maximum := int64(1<<(bits-1)) - 1
		return -maximum - 1, maximum
	}
	return 0, int64(1<<bits) - 1
}

func validateUniqueStrings(kind string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s cannot be empty", kind)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s %q", kind, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func cloneDefinitions(definitions []Definition) []Definition {
	cloned := make([]Definition, len(definitions))
	for index, definition := range definitions {
		cloned[index] = cloneDefinition(definition)
	}
	return cloned
}

func cloneDefinition(definition Definition) Definition {
	definition.ConstraintIDs = slices.Clone(definition.ConstraintIDs)
	definition.ProductVariants = slices.Clone(definition.ProductVariants)
	definition.SentinelValues = slices.Clone(definition.SentinelValues)
	definition.Choices = slices.Clone(definition.Choices)
	definition.RawDefault = cloneInt64(definition.RawDefault)
	definition.RawMinimum = cloneInt64(definition.RawMinimum)
	definition.RawMaximum = cloneInt64(definition.RawMaximum)
	definition.Source.Pages = slices.Clone(definition.Source.Pages)
	return definition
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
