package board

import (
	"errors"
	"os"
	"testing"
)

func TestVendorReadBoundary(t *testing.T) {
	args, err := VendorReadArgs("I2C_REG_VER")
	if err != nil || len(args) != 2 || args[0] != "GET" || args[1] != "I2C_REG_VER" {
		t.Fatalf("VendorReadArgs() = %v, %v", args, err)
	}
	if _, err := VendorReadArgs("RTC_TIME"); err == nil {
		t.Fatal("VendorReadArgs accepted a register outside the identity allowlist")
	}
}

func TestCatalogVendorReadBoundary(t *testing.T) {
	catalog, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	args, definition, err := CatalogVendorReadArgs(catalog, 7, "VBAT")
	if err != nil || len(args) != 2 || args[0] != "GET" || args[1] != "VBAT" || definition.Address != 0x32 {
		t.Fatalf("CatalogVendorReadArgs() = %v, %+v, %v", args, definition, err)
	}
	if _, _, err := CatalogVendorReadArgs(catalog, 8, "VBAT"); !errors.Is(err, ErrRegisterNotCatalogued) {
		t.Fatalf("unsupported set error = %v", err)
	}
	if _, _, err := CatalogVendorReadArgs(catalog, 7, "NOT_A_REGISTER"); !errors.Is(err, ErrRegisterNotCatalogued) {
		t.Fatalf("unknown register error = %v", err)
	}
}

func TestParseVendorFixtures(t *testing.T) {
	tests := []struct {
		name    string
		want    int64
		wantErr bool
	}{
		{name: "success", want: 7},
		{name: "busy", wantErr: true},
		{name: "io-permission", wantErr: true},
		{name: "empty", wantErr: true},
		{name: "malformed", wantErr: true},
		{name: "multiple-lines", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := os.ReadFile("../../testdata/vendor/" + test.name + ".stdout")
			if err != nil {
				t.Fatal(err)
			}
			got, err := ParseVendorDecimal(output)
			if test.wantErr {
				if !errors.Is(err, ErrInvalidVendorOutput) {
					t.Fatalf("ParseVendorDecimal() error = %v", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("ParseVendorDecimal() = %d, %v", got, err)
			}
		})
	}
}

func TestParseVendorIntegerAllowsReviewedSignedValues(t *testing.T) {
	value, err := ParseVendorInteger([]byte("-25\n"))
	if err != nil || value != -25 {
		t.Fatalf("ParseVendorInteger() = %d, %v", value, err)
	}
	if _, err := ParseVendorDecimal([]byte("-25\n")); !errors.Is(err, ErrInvalidVendorOutput) {
		t.Fatalf("ParseVendorDecimal() error = %v", err)
	}
}

func TestDetectVendorIdentity(t *testing.T) {
	identity, err := DetectVendorIdentity([]byte("7\n"))
	if err != nil || identity.RegisterSet != 7 || identity.Product != "" {
		t.Fatalf("DetectVendorIdentity() = %+v, %v", identity, err)
	}
	output, readErr := os.ReadFile("../../testdata/vendor/unsupported.stdout")
	if readErr != nil {
		t.Fatal(readErr)
	}
	identity, err = DetectVendorIdentity(output)
	if !errors.Is(err, ErrUnsupportedRegisterSet) || identity.RegisterSet != 8 {
		t.Fatalf("unsupported DetectVendorIdentity() = %+v, %v", identity, err)
	}
}
