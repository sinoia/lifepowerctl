package board

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

func newTestVendorReader(t *testing.T, output string) (*VendorReader, *recordingRunner) {
	t.Helper()
	catalog, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{result: commandResult{stdout: []byte(output)}}
	return newVendorReader(runner, limits.Default().Vendor, catalog), runner
}

func TestVendorReaderReturnsTypedExactValue(t *testing.T) {
	reader, runner := newTestVendorReader(t, "300\n")
	reading, err := reader.Read(context.Background(), 7, "PI_BOOT_TO")
	if err != nil {
		t.Fatal(err)
	}
	if reading.EngineeringValue != 300 || reading.RawValue == nil || *reading.RawValue != 30 || reading.Definition.Unit != "seconds" || reading.Sentinel {
		t.Fatalf("reading = %+v", reading)
	}
	if runner.path != VendorCLIPath || !reflect.DeepEqual(runner.arguments, []string{"GET", "PI_BOOT_TO"}) {
		t.Fatalf("command = %q %q", runner.path, runner.arguments)
	}
}

func TestVendorReaderDoesNotInventLossyRawValue(t *testing.T) {
	reader, _ := newTestVendorReader(t, "3606\n")
	reading, err := reader.Read(context.Background(), 7, "VBAT")
	if err != nil {
		t.Fatal(err)
	}
	if reading.EngineeringValue != 3606 || reading.RawValue != nil || reading.IsRawExact() {
		t.Fatalf("lossy reading = %+v", reading)
	}
}

func TestVendorReaderClassifiesChoicesAndSentinels(t *testing.T) {
	reader, _ := newTestVendorReader(t, "2\n")
	choice, err := reader.Read(context.Background(), 7, "LED_STATE")
	if err != nil || choice.Choice != "pulsing" || choice.Sentinel {
		t.Fatalf("choice reading = %+v, %v", choice, err)
	}

	reader, _ = newTestVendorReader(t, "0\n")
	sentinel, err := reader.Read(context.Background(), 7, "IOUT_SHDN_THRESHOLD")
	if err != nil || !sentinel.Sentinel || sentinel.RawValue != nil {
		t.Fatalf("sentinel reading = %+v, %v", sentinel, err)
	}
}

func TestVendorReaderRecoversSignedInjectiveRawValue(t *testing.T) {
	reader, _ := newTestVendorReader(t, "-4\n")
	reading, err := reader.Read(context.Background(), 7, "VBAT_OFFSET")
	if err != nil || reading.RawValue == nil || *reading.RawValue != -1 {
		t.Fatalf("signed reading = %+v, %v", reading, err)
	}
}

func TestVendorReaderRejectsUncataloguedMalformedAndOutOfRangeValues(t *testing.T) {
	reader, runner := newTestVendorReader(t, "1\n")
	if _, err := reader.Read(context.Background(), 8, "VBAT"); !errors.Is(err, ErrRegisterNotCatalogued) {
		t.Fatalf("uncatalogued error = %v", err)
	}
	if runner.path != "" {
		t.Fatal("uncatalogued read executed the vendor command")
	}

	reader, _ = newTestVendorReader(t, "not-a-number\n")
	if _, err := reader.Read(context.Background(), 7, "VBAT"); !errors.Is(err, ErrInvalidVendorOutput) {
		t.Fatalf("malformed error = %v", err)
	}

	reader, _ = newTestVendorReader(t, "999999\n")
	if _, err := reader.Read(context.Background(), 7, "LED_STATE"); !errors.Is(err, ErrInvalidVendorOutput) {
		t.Fatalf("out-of-range error = %v", err)
	}

	reader, _ = newTestVendorReader(t, "3\n")
	nonReversible, err := reader.Read(context.Background(), 7, "VBAT_OFFSET")
	if err != nil || nonReversible.RawValue != nil {
		t.Fatalf("non-reversible reading = %+v, %v", nonReversible, err)
	}
}

func TestVendorReaderPreservesCommandFailures(t *testing.T) {
	reader, runner := newTestVendorReader(t, "")
	runner.err = ErrVendorTimeout
	if _, err := reader.Read(context.Background(), 7, "RTC_TIME"); !errors.Is(err, ErrVendorTimeout) {
		t.Fatalf("timeout error = %v", err)
	}
	runner.err = ErrVendorOutputLimit
	if _, err := reader.Read(context.Background(), 7, "RTC_TIME"); !errors.Is(err, ErrVendorOutputLimit) {
		t.Fatalf("output limit error = %v", err)
	}
}

func TestVendorReaderLiveReadOnlyCatalogue(t *testing.T) {
	if os.Getenv("LIFEPOWERCTL_TEST_LIVE_VENDOR") != "1" {
		t.Skip("set LIFEPOWERCTL_TEST_LIVE_VENDOR=1 on a reviewed read-only hardware target")
	}
	catalog, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewVendorReader(limits.Default().Vendor, catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range catalog.Definitions() {
		t.Run(string(definition.Register), func(t *testing.T) {
			reading, readErr := reader.Read(context.Background(), 7, definition.Register)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if reading.Definition.Register != definition.Register {
				t.Fatalf("reading definition = %s", reading.Definition.Register)
			}
		})
	}
}
