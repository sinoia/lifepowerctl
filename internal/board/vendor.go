package board

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const VendorCLIPath = "/usr/local/bin/lifepo4wered-cli"

var ErrInvalidVendorOutput = errors.New("invalid vendor CLI output")
var ErrUnsupportedRegisterSet = errors.New("unsupported register set")
var ErrRegisterNotCatalogued = errors.New("register is not in the accepted catalogue")

// VendorReadArgs is the complete permitted argument shape for the identity
// read-only spike. Callers cannot select an operation or pass a raw argument.
func VendorReadArgs(register Register) ([]string, error) {
	if register != "I2C_REG_VER" {
		return nil, fmt.Errorf("vendor read %s: register is not allowed", register)
	}
	return []string{"GET", string(register)}, nil
}

// CatalogVendorReadArgs permits only a register selected from the immutable,
// version-specific catalogue. The operation remains fixed to GET.
func CatalogVendorReadArgs(catalog Catalog, registerSet int, register Register) ([]string, Definition, error) {
	definition, ok := catalog.Find(registerSet, register)
	if !ok {
		return nil, Definition{}, fmt.Errorf("%w: register set %d register %s", ErrRegisterNotCatalogued, registerSet, register)
	}
	return []string{"GET", string(definition.Register)}, definition, nil
}

func ParseVendorInteger(output []byte) (int64, error) {
	value := strings.TrimSpace(string(output))
	if value == "" || strings.ContainsAny(value, "\r\n \t") {
		return 0, ErrInvalidVendorOutput
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: expected one signed decimal integer", ErrInvalidVendorOutput)
	}
	return parsed, nil
}

func ParseVendorDecimal(output []byte) (int64, error) {
	parsed, err := ParseVendorInteger(output)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%w: expected one non-negative decimal integer", ErrInvalidVendorOutput)
	}
	return parsed, nil
}

// DetectVendorIdentity classifies only register-set compatibility. The CLI
// does not provide independent evidence of a physical product model, so the
// Product field intentionally remains empty.
func DetectVendorIdentity(output []byte) (Identity, error) {
	value, err := ParseVendorDecimal(output)
	if err != nil {
		return Identity{}, err
	}
	if value != 7 {
		return Identity{RegisterSet: int(value)}, fmt.Errorf("%w: %d", ErrUnsupportedRegisterSet, value)
	}
	return Identity{RegisterSet: int(value)}, nil
}
