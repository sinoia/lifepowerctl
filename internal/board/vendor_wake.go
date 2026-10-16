package board

import (
	"context"
	"fmt"
	"strconv"

	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

// WakeProgrammingResult records only evidence actually observed at the
// hardware boundary. Verified is true only after both fixed writes have been
// read back exactly.
type WakeProgrammingResult struct {
	IntendedRTC      uint32
	RelativeWritten  bool
	RelativeReadBack uint16
	RelativeVerified bool
	AbsoluteWritten  bool
	AbsoluteReadBack uint32
	AbsoluteVerified bool
}

func (result WakeProgrammingResult) Verified() bool {
	return result.RelativeVerified && result.AbsoluteVerified
}

// VendorWakeDevice is the purpose-specific scheduler-owned hardware edge. It
// cannot select arbitrary operations or registers.
type VendorWakeDevice struct {
	runner commandRunner
	limits limits.Command
}

func NewVendorWakeDevice(commandLimits limits.Command, catalog Catalog) (*VendorWakeDevice, error) {
	if commandLimits.Timeout <= 0 || commandLimits.MaxStdoutBytes <= 0 || commandLimits.MaxStderrBytes <= 0 {
		return nil, fmt.Errorf("invalid vendor command limits")
	}
	for _, register := range []Register{"RTC_TIME", "WAKE_TIME", "RTC_WAKE_TIME"} {
		definition, ok := catalog.Find(7, register)
		if !ok || definition.Owner != OwnerScheduler && register != "RTC_TIME" {
			return nil, fmt.Errorf("invalid scheduler wake catalogue boundary")
		}
	}
	return &VendorWakeDevice{runner: execRunner{}, limits: commandLimits}, nil
}

func newVendorWakeDevice(runner commandRunner, commandLimits limits.Command) *VendorWakeDevice {
	return &VendorWakeDevice{runner: runner, limits: commandLimits}
}

// ProgramAbsolute first clears and verifies the unsupported relative wake,
// then writes and verifies the exact absolute wake.
func (device *VendorWakeDevice) ProgramAbsolute(ctx context.Context, intended uint32) (WakeProgrammingResult, error) {
	result := WakeProgrammingResult{IntendedRTC: intended}
	if intended == 0 {
		return result, fmt.Errorf("absolute wake must be nonzero")
	}
	if err := device.write(ctx, "WAKE_TIME", 0); err != nil {
		return result, err
	}
	result.RelativeWritten = true
	relative, err := device.readUint(ctx, "WAKE_TIME", 16)
	if err != nil {
		return result, err
	}
	result.RelativeReadBack = uint16(relative)
	result.RelativeVerified = relative == 0
	if !result.RelativeVerified {
		return result, fmt.Errorf("verify WAKE_TIME: %w", ErrReadback)
	}
	if err := device.write(ctx, "RTC_WAKE_TIME", uint64(intended)); err != nil {
		return result, err
	}
	result.AbsoluteWritten = true
	absolute, err := device.readUint(ctx, "RTC_WAKE_TIME", 32)
	if err != nil {
		return result, err
	}
	result.AbsoluteReadBack = uint32(absolute)
	result.AbsoluteVerified = absolute == uint64(intended)
	if !result.AbsoluteVerified {
		return result, fmt.Errorf("verify RTC_WAKE_TIME: %w", ErrReadback)
	}
	return result, nil
}

// Disarm clears and verifies both wake mechanisms. Recovery attempts both
// registers even when the first operation is uncertain, while retaining the
// first error and never claiming complete verification.
func (device *VendorWakeDevice) Disarm(ctx context.Context) (WakeProgrammingResult, error) {
	result := WakeProgrammingResult{}
	var firstErr error
	if err := device.write(ctx, "WAKE_TIME", 0); err != nil {
		firstErr = err
	} else {
		result.RelativeWritten = true
		relative, err := device.readUint(ctx, "WAKE_TIME", 16)
		if err != nil {
			firstErr = err
		} else {
			result.RelativeReadBack = uint16(relative)
			result.RelativeVerified = relative == 0
			if !result.RelativeVerified {
				firstErr = fmt.Errorf("verify WAKE_TIME: %w", ErrReadback)
			}
		}
	}
	if err := device.write(ctx, "RTC_WAKE_TIME", 0); err != nil {
		if firstErr == nil {
			firstErr = err
		}
		return result, firstErr
	}
	result.AbsoluteWritten = true
	absolute, err := device.readUint(ctx, "RTC_WAKE_TIME", 32)
	if err != nil {
		if firstErr == nil {
			firstErr = err
		}
		return result, firstErr
	}
	result.AbsoluteReadBack = uint32(absolute)
	result.AbsoluteVerified = absolute == 0
	if !result.AbsoluteVerified {
		if firstErr == nil {
			firstErr = fmt.Errorf("verify RTC_WAKE_TIME: %w", ErrReadback)
		}
	}
	return result, firstErr
}

// ObserveBoot uses the exact bracketed read order required by boot
// classification and performs no write.
func (device *VendorWakeDevice) ObserveBoot(ctx context.Context) (schedule.Observation, error) {
	before, err := device.readUint(ctx, "RTC_TIME", 32)
	if err != nil {
		return schedule.Observation{}, err
	}
	absolute, err := device.readUint(ctx, "RTC_WAKE_TIME", 32)
	if err != nil {
		return schedule.Observation{}, err
	}
	relative, err := device.readUint(ctx, "WAKE_TIME", 16)
	if err != nil {
		return schedule.Observation{}, err
	}
	after, err := device.readUint(ctx, "RTC_TIME", 32)
	if err != nil {
		return schedule.Observation{}, err
	}
	return schedule.Observation{RTCBefore: uint32(before), RTCAfter: uint32(after), RTCWake: uint32(absolute), WakeMinutes: uint16(relative)}, nil
}

func (device *VendorWakeDevice) ReadRTC(ctx context.Context) (uint32, error) {
	value, err := device.readUint(ctx, "RTC_TIME", 32)
	return uint32(value), err
}

func (device *VendorWakeDevice) write(ctx context.Context, register Register, value uint64) error {
	if _, err := device.runner.Run(ctx, VendorCLIPath, []string{"SET", string(register), strconv.FormatUint(value, 10)}, device.limits); err != nil {
		return fmt.Errorf("write %s: %w", register, err)
	}
	return nil
}

func (device *VendorWakeDevice) readUint(ctx context.Context, register Register, bits int) (uint64, error) {
	command, err := device.runner.Run(ctx, VendorCLIPath, []string{"GET", string(register)}, device.limits)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", register, err)
	}
	value, err := ParseVendorInteger(command.stdout)
	maximum := uint64(1)<<bits - 1
	if err != nil || value < 0 || uint64(value) > maximum {
		return 0, fmt.Errorf("read %s: %w", register, ErrInvalidVendorOutput)
	}
	return uint64(value), nil
}
