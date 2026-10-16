// Package board defines the hardware boundary used by LifePower Control.
//
// Implementations must serialize mutations. A caller must treat an incomplete
// result or a lost connection as an unknown hardware outcome, never success.
package board

import (
	"context"
	"errors"
	"time"
)

var (
	ErrCommunication = errors.New("board communication lost")
	ErrReadback      = errors.New("register read-back did not match")
)

type Register string

type Identity struct {
	Product     string
	RegisterSet int
}

type Change struct {
	Register Register
	Value    int64
}

type ChangeResult struct {
	Change   Change
	ReadBack int64
	Applied  bool
	Verified bool
}

type ApplyResult struct {
	Changes []ChangeResult
}

type BootReason string

const (
	BootExpected  BootReason = "expected"
	BootManual    BootReason = "manual"
	BootAmbiguous BootReason = "ambiguous"
)

type PowerAction string

const (
	PowerShutdown PowerAction = "shutdown"
	PowerReboot   PowerAction = "reboot"
)

// Board is deliberately richer than the first read-only adapter. It makes the
// failure semantics required of future mutation and duty-cycle code explicit.
type Board interface {
	Detect(context.Context) (Identity, error)
	Read(context.Context, ...Register) (map[Register]int64, error)
	Apply(context.Context, []Change) (ApplyResult, error)
	Countdown(context.Context) (time.Duration, error)
	BootReason(context.Context) (BootReason, error)
	RequestPower(context.Context, PowerAction) error
}
