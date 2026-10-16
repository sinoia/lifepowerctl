package board

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

type expectedCommand struct {
	arguments []string
	result    commandResult
	err       error
}

type sequenceRunner struct {
	t        *testing.T
	commands []expectedCommand
	next     int
}

func (runner *sequenceRunner) Run(_ context.Context, path string, arguments []string, _ limits.Command) (commandResult, error) {
	runner.t.Helper()
	if path != VendorCLIPath || runner.next >= len(runner.commands) {
		runner.t.Fatalf("unexpected command %q %q", path, arguments)
	}
	expected := runner.commands[runner.next]
	runner.next++
	if !reflect.DeepEqual(arguments, expected.arguments) {
		runner.t.Fatalf("arguments = %q, want %q", arguments, expected.arguments)
	}
	return expected.result, expected.err
}

func testVendorDevice(t *testing.T, commands ...expectedCommand) (*VendorDevice, *sequenceRunner) {
	t.Helper()
	catalogue, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	runner := &sequenceRunner{t: t, commands: commands}
	return newVendorDevice(runner, limits.Default().Vendor, catalogue), runner
}

func TestVendorDeviceReadsRawHex(t *testing.T) {
	device, runner := testVendorDevice(t, expectedCommand{
		arguments: []string{"GETHEX", "LED_STATE"}, result: commandResult{stdout: []byte("0x0002\n")},
	})
	values, err := device.Read(context.Background(), "LED_STATE")
	if err != nil || values["LED_STATE"] != 2 || runner.next != 1 {
		t.Fatalf("Read() = %v, %v", values, err)
	}
}

func TestVendorDeviceSetsEngineeringValueAndVerifiesRaw(t *testing.T) {
	device, runner := testVendorDevice(t,
		expectedCommand{arguments: []string{"SET", "LED_STATE", "2"}},
		expectedCommand{arguments: []string{"GETHEX", "LED_STATE"}, result: commandResult{stdout: []byte("0x0002\n")}},
	)
	result, err := device.Apply(context.Background(), []Change{{Register: "LED_STATE", Value: 2}})
	if err != nil || len(result.Changes) != 1 || !result.Changes[0].Applied || !result.Changes[0].Verified || runner.next != 2 {
		t.Fatalf("Apply() = %+v, %v", result, err)
	}
}

func TestVendorDeviceRejectsEveryNonOrdinaryWriteAtBoundary(t *testing.T) {
	device, runner := testVendorDevice(t)
	for _, register := range []Register{"VOUT_MAX", "PI_BOOT_TO", "I2C_ADDRESS", "CFG_WRITE", "VBAT"} {
		_, err := device.Apply(context.Background(), []Change{{Register: register, Value: 0}})
		if err == nil {
			t.Errorf("Apply(%s) succeeded", register)
		}
	}
	if runner.next != 0 {
		t.Fatalf("rejected writes invoked %d commands", runner.next)
	}
}

func TestVendorOrdinaryDeviceAppliesAutoBootEnumeration(t *testing.T) {
	catalogue, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	runner := &sequenceRunner{t: t, commands: []expectedCommand{
		{arguments: []string{"SET", "AUTO_BOOT", "2"}},
		{arguments: []string{"GETHEX", "AUTO_BOOT"}, result: commandResult{stdout: []byte("0x02\n")}},
	}}
	device := newVendorDevice(runner, limits.Default().Vendor, catalogue)
	result, err := device.Apply(context.Background(), []Change{{Register: "AUTO_BOOT", Value: 2}})
	if err != nil || len(result.Changes) != 1 || !result.Changes[0].Verified {
		t.Fatalf("Apply(AUTO_BOOT) = %+v, %v", result, err)
	}
}

func TestVendorDeviceReturnsAppliedButUnverifiedWhenReadBackFails(t *testing.T) {
	device, _ := testVendorDevice(t,
		expectedCommand{arguments: []string{"SET", "LED_STATE", "2"}},
		expectedCommand{arguments: []string{"GETHEX", "LED_STATE"}, err: ErrVendorTimeout},
	)
	result, err := device.Apply(context.Background(), []Change{{Register: "LED_STATE", Value: 2}})
	if !errors.Is(err, ErrVendorTimeout) || len(result.Changes) != 1 || !result.Changes[0].Applied || result.Changes[0].Verified {
		t.Fatalf("Apply() = %+v, %v", result, err)
	}
}

func TestParseVendorRaw(t *testing.T) {
	catalogue, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	led, _ := catalogue.Find(7, "LED_STATE")
	offset, _ := catalogue.Find(7, "VBAT_OFFSET")
	tests := []struct {
		name       string
		value      string
		definition Definition
		want       int64
		wantErr    bool
	}{
		{name: "unsigned", value: "0x0003\n", definition: led, want: 3},
		{name: "signed", value: "0xFFFF", definition: offset, want: -1},
		{name: "width", value: "0x0100", definition: led, wantErr: true},
		{name: "decimal", value: "3", definition: led, wantErr: true},
		{name: "junk", value: "0x03 trailing", definition: led, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseVendorRaw([]byte(test.value), test.definition)
			if (err != nil) != test.wantErr || got != test.want {
				t.Fatalf("ParseVendorRaw(%q) = %d, %v; want %d, error=%t", test.value, got, err, test.want, test.wantErr)
			}
		})
	}
}

func TestVendorDeviceRejectsInvalidChangeCount(t *testing.T) {
	device, _ := testVendorDevice(t)
	for _, changes := range [][]Change{nil, {{Register: "LED_STATE", Value: 1}, {Register: "LED_STATE", Value: 2}}} {
		if _, err := device.Apply(context.Background(), changes); err == nil {
			t.Fatal(fmt.Sprintf("Apply(%v) succeeded", changes))
		}
	}
}

func TestVendorBatteryThresholdDeviceAppliesOnlyReviewedThresholdSet(t *testing.T) {
	catalogue, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	engineering := map[Register]int64{"VBAT_BOOT": 4900, "VBAT_SHDN": 4600, "VBAT_MIN": 4300}
	changes := make([]Change, 0, 3)
	commands := make([]expectedCommand, 0, 6)
	for _, register := range []Register{"VBAT_BOOT", "VBAT_SHDN", "VBAT_MIN"} {
		changes = append(changes, Change{Register: register, Value: engineering[register]})
		commands = append(commands,
			expectedCommand{arguments: []string{"SET", string(register), strconv.FormatInt(engineering[register], 10)}},
			expectedCommand{arguments: []string{"GET", string(register)}, result: commandResult{stdout: []byte(fmt.Sprintf("%d\n", engineering[register]))}},
		)
	}
	runner := &sequenceRunner{t: t, commands: commands}
	device := newVendorDevice(runner, limits.Default().Vendor, catalogue)
	device.policy = vendorWriteBatteryThresholds
	result, err := device.Apply(context.Background(), changes)
	if err != nil || len(result.Changes) != 3 || runner.next != len(commands) {
		t.Fatalf("Apply() = %+v, %v; commands=%d", result, err, runner.next)
	}
	for _, observed := range result.Changes {
		if !observed.Applied || !observed.Verified || observed.ReadBack != observed.Change.Value {
			t.Fatalf("unverified result: %+v", observed)
		}
	}
}

func TestVendorWritePoliciesRemainSeparate(t *testing.T) {
	catalogue, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	runner := &sequenceRunner{t: t}
	ordinary := newVendorDevice(runner, limits.Default().Vendor, catalogue)
	if _, err := ordinary.Apply(context.Background(), []Change{{Register: "VBAT_MIN", Value: 7000}}); err == nil {
		t.Fatal("ordinary adapter accepted an advanced battery threshold")
	}
	battery := newVendorDevice(runner, limits.Default().Vendor, catalogue)
	battery.policy = vendorWriteBatteryThresholds
	for _, changes := range [][]Change{
		{{Register: "LED_STATE", Value: 1}},
		{{Register: "VBAT_MIN", Value: 7000}, {Register: "VBAT_MIN", Value: 7100}},
		{{Register: "VBAT_MIN", Value: 7000}, {Register: "VBAT_SHDN", Value: 7500}, {Register: "VBAT_BOOT", Value: 8000}, {Register: "VBAT_MIN", Value: 7100}},
	} {
		if _, err := battery.Apply(context.Background(), changes); err == nil {
			t.Fatalf("battery adapter accepted %v", changes)
		}
	}
	if runner.next != 0 {
		t.Fatalf("rejected changes invoked %d commands", runner.next)
	}
}

func TestVendorShutdownDelayDeviceAllowsOnlyShutdownDelay(t *testing.T) {
	catalogue, err := RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	runner := &sequenceRunner{t: t, commands: []expectedCommand{
		{arguments: []string{"SET", "SHDN_DELAY", "96"}},
		{arguments: []string{"GETHEX", "SHDN_DELAY"}, result: commandResult{stdout: []byte("0x0060\n")}},
	}}
	device := newVendorDevice(runner, limits.Default().Vendor, catalogue)
	device.policy = vendorWriteShutdownDelay
	result, err := device.Apply(context.Background(), []Change{{Register: "SHDN_DELAY", Value: 96}})
	if err != nil || len(result.Changes) != 1 || !result.Changes[0].Verified {
		t.Fatalf("Apply(SHDN_DELAY) = %+v, %v", result, err)
	}
	rejecting := newVendorDevice(&sequenceRunner{t: t}, limits.Default().Vendor, catalogue)
	rejecting.policy = vendorWriteShutdownDelay
	for _, register := range []Register{"LED_STATE", "PI_SHDN_TO", "CFG_WRITE"} {
		if _, err := rejecting.Apply(context.Background(), []Change{{Register: register, Value: 1}}); err == nil {
			t.Fatalf("shutdown-delay adapter accepted %s", register)
		}
	}
}
