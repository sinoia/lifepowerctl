package board

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

func TestVendorWakeProgramsAbsoluteInFixedVerifiedOrder(t *testing.T) {
	runner := &sequenceRunner{t: t, commands: []expectedCommand{
		{arguments: []string{"SET", "WAKE_TIME", "0"}},
		{arguments: []string{"GET", "WAKE_TIME"}, result: commandResult{stdout: []byte("0\n")}},
		{arguments: []string{"SET", "RTC_WAKE_TIME", "1800000900"}},
		{arguments: []string{"GET", "RTC_WAKE_TIME"}, result: commandResult{stdout: []byte("1800000900\n")}},
	}}
	device := newVendorWakeDevice(runner, limits.Default().Vendor)
	result, err := device.ProgramAbsolute(context.Background(), 1_800_000_900)
	if err != nil || !result.Verified() || result.AbsoluteReadBack != 1_800_000_900 || runner.next != 4 {
		t.Fatalf("ProgramAbsolute() = %+v, %v; commands=%d", result, err, runner.next)
	}
}

func TestVendorWakeNeverClaimsVerificationAfterBoundaryFailure(t *testing.T) {
	commands := []expectedCommand{
		{arguments: []string{"SET", "WAKE_TIME", "0"}},
		{arguments: []string{"GET", "WAKE_TIME"}, result: commandResult{stdout: []byte("0\n")}},
		{arguments: []string{"SET", "RTC_WAKE_TIME", "1800000900"}},
		{arguments: []string{"GET", "RTC_WAKE_TIME"}, result: commandResult{stdout: []byte("1800000900\n")}},
	}
	for failure := range commands {
		t.Run(commands[failure].arguments[0]+"_"+commands[failure].arguments[1], func(t *testing.T) {
			script := append([]expectedCommand(nil), commands[:failure+1]...)
			script[failure].err = ErrVendorTimeout
			runner := &sequenceRunner{t: t, commands: script}
			result, err := newVendorWakeDevice(runner, limits.Default().Vendor).ProgramAbsolute(context.Background(), 1_800_000_900)
			if !errors.Is(err, ErrVendorTimeout) || result.Verified() || runner.next != failure+1 {
				t.Fatalf("failure %d = %+v, %v; commands=%d", failure, result, err, runner.next)
			}
		})
	}
}

func TestVendorWakeRejectsReadBackMismatch(t *testing.T) {
	runner := &sequenceRunner{t: t, commands: []expectedCommand{
		{arguments: []string{"SET", "WAKE_TIME", "0"}},
		{arguments: []string{"GET", "WAKE_TIME"}, result: commandResult{stdout: []byte("0\n")}},
		{arguments: []string{"SET", "RTC_WAKE_TIME", "1800000900"}},
		{arguments: []string{"GET", "RTC_WAKE_TIME"}, result: commandResult{stdout: []byte("1800000901\n")}},
	}}
	result, err := newVendorWakeDevice(runner, limits.Default().Vendor).ProgramAbsolute(context.Background(), 1_800_000_900)
	if !errors.Is(err, ErrReadback) || result.Verified() || !result.AbsoluteWritten || result.AbsoluteVerified {
		t.Fatalf("ProgramAbsolute() = %+v, %v", result, err)
	}
}

func TestVendorWakeDisarmsBothRegisters(t *testing.T) {
	runner := &sequenceRunner{t: t, commands: []expectedCommand{
		{arguments: []string{"SET", "WAKE_TIME", "0"}},
		{arguments: []string{"GET", "WAKE_TIME"}, result: commandResult{stdout: []byte("0\n")}},
		{arguments: []string{"SET", "RTC_WAKE_TIME", "0"}},
		{arguments: []string{"GET", "RTC_WAKE_TIME"}, result: commandResult{stdout: []byte("0\n")}},
	}}
	result, err := newVendorWakeDevice(runner, limits.Default().Vendor).Disarm(context.Background())
	if err != nil || !result.Verified() {
		t.Fatalf("Disarm() = %+v, %v", result, err)
	}
}

func TestVendorWakeDisarmAttemptsAbsoluteAfterRelativeFailure(t *testing.T) {
	runner := &sequenceRunner{t: t, commands: []expectedCommand{
		{arguments: []string{"SET", "WAKE_TIME", "0"}, err: ErrVendorTimeout},
		{arguments: []string{"SET", "RTC_WAKE_TIME", "0"}},
		{arguments: []string{"GET", "RTC_WAKE_TIME"}, result: commandResult{stdout: []byte("0\n")}},
	}}
	result, err := newVendorWakeDevice(runner, limits.Default().Vendor).Disarm(context.Background())
	if !errors.Is(err, ErrVendorTimeout) || result.Verified() || !result.AbsoluteVerified || runner.next != 3 {
		t.Fatalf("Disarm() = %+v, %v; commands=%d", result, err, runner.next)
	}
}

func TestVendorWakeObservesBootInBracketedOrder(t *testing.T) {
	runner := &sequenceRunner{t: t, commands: []expectedCommand{
		{arguments: []string{"GET", "RTC_TIME"}, result: commandResult{stdout: []byte("1800000900\n")}},
		{arguments: []string{"GET", "RTC_WAKE_TIME"}, result: commandResult{stdout: []byte("0\n")}},
		{arguments: []string{"GET", "WAKE_TIME"}, result: commandResult{stdout: []byte("0\n")}},
		{arguments: []string{"GET", "RTC_TIME"}, result: commandResult{stdout: []byte("1800000901\n")}},
	}}
	observed, err := newVendorWakeDevice(runner, limits.Default().Vendor).ObserveBoot(context.Background())
	want := struct{ before, after, wake uint32 }{1_800_000_900, 1_800_000_901, 0}
	got := struct{ before, after, wake uint32 }{observed.RTCBefore, observed.RTCAfter, observed.RTCWake}
	if err != nil || !reflect.DeepEqual(got, want) || observed.WakeMinutes != 0 {
		t.Fatalf("ObserveBoot() = %+v, %v", observed, err)
	}
}

func TestVendorWakeReadsBoardClock(t *testing.T) {
	runner := &sequenceRunner{t: t, commands: []expectedCommand{
		{arguments: []string{"GET", "RTC_TIME"}, result: commandResult{stdout: []byte("1800000900\n")}},
	}}
	value, err := newVendorWakeDevice(runner, limits.Default().Vendor).ReadRTC(context.Background())
	if err != nil || value != 1_800_000_900 {
		t.Fatalf("ReadRTC() = %d, %v", value, err)
	}
}

func TestVendorWakeRejectsZeroAndMalformedValuesWithoutWritingAbsolute(t *testing.T) {
	device := newVendorWakeDevice(&sequenceRunner{t: t}, limits.Default().Vendor)
	if _, err := device.ProgramAbsolute(context.Background(), 0); err == nil {
		t.Fatal("zero absolute wake succeeded")
	}
	runner := &sequenceRunner{t: t, commands: []expectedCommand{
		{arguments: []string{"SET", "WAKE_TIME", "0"}},
		{arguments: []string{"GET", "WAKE_TIME"}, result: commandResult{stdout: []byte("65536\n")}},
	}}
	result, err := newVendorWakeDevice(runner, limits.Default().Vendor).ProgramAbsolute(context.Background(), 1_800_000_900)
	if !errors.Is(err, ErrInvalidVendorOutput) || result.AbsoluteWritten {
		t.Fatalf("ProgramAbsolute() = %+v, %v", result, err)
	}
}
