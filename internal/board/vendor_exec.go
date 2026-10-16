package board

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

var (
	ErrVendorTimeout     = errors.New("vendor command timed out")
	ErrVendorOutputLimit = errors.New("vendor command output exceeded its limit")
	ErrVendorCommand     = errors.New("vendor command failed")
)

type commandResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

type commandRunner interface {
	Run(context.Context, string, []string, limits.Command) (commandResult, error)
}

type execRunner struct{}

type boundedBuffer struct {
	buffer   bytes.Buffer
	maximum  int64
	exceeded bool
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	remaining := buffer.maximum - int64(buffer.buffer.Len())
	if remaining > 0 {
		keep := int64(len(value))
		if keep > remaining {
			keep = remaining
		}
		_, _ = buffer.buffer.Write(value[:keep])
	}
	if int64(len(value)) > remaining {
		buffer.exceeded = true
	}
	return len(value), nil
}

func (execRunner) Run(parent context.Context, executable string, arguments []string, commandLimits limits.Command) (commandResult, error) {
	ctx, cancel := context.WithTimeout(parent, commandLimits.Timeout)
	defer cancel()
	stdout := boundedBuffer{maximum: commandLimits.MaxStdoutBytes}
	stderr := boundedBuffer{maximum: commandLimits.MaxStderrBytes}
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := commandResult{stdout: stdout.buffer.Bytes(), stderr: stderr.buffer.Bytes(), exitCode: -1}
	if command.ProcessState != nil {
		result.exitCode = command.ProcessState.ExitCode()
	}
	if stdout.exceeded || stderr.exceeded {
		return result, ErrVendorOutputLimit
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result, ErrVendorTimeout
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err != nil {
		return result, fmt.Errorf("%w: exit code %d", ErrVendorCommand, result.exitCode)
	}
	return result, nil
}

// VendorDetector exposes only the fixed read-only identity operation accepted
// for read-only board access. Neither callers nor configuration can select an executable,
// operation, register, or argument.
type VendorDetector struct {
	runner commandRunner
	limits limits.Command
}

func NewVendorDetector(commandLimits limits.Command) (*VendorDetector, error) {
	if commandLimits.Timeout <= 0 || commandLimits.MaxStdoutBytes <= 0 || commandLimits.MaxStderrBytes <= 0 {
		return nil, fmt.Errorf("invalid vendor command limits")
	}
	return &VendorDetector{runner: execRunner{}, limits: commandLimits}, nil
}

func newVendorDetector(runner commandRunner, commandLimits limits.Command) *VendorDetector {
	return &VendorDetector{runner: runner, limits: commandLimits}
}

func (detector *VendorDetector) Detect(ctx context.Context) (Identity, error) {
	arguments, err := VendorReadArgs("I2C_REG_VER")
	if err != nil {
		return Identity{}, err
	}
	result, err := detector.runner.Run(ctx, VendorCLIPath, arguments, detector.limits)
	if err != nil {
		return Identity{}, err
	}
	return DetectVendorIdentity(result.stdout)
}
