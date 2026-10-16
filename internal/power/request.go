// Package power provides the fixed privileged boundary for orderly host power
// requests. Callers cannot select an executable, unit, or argument.
package power

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

const requestPath = "/run/lifepowerctl-power/poweroff.request"

var (
	ErrTimeout  = errors.New("power request timed out")
	ErrRejected = errors.New("power request rejected")
)

type Requester struct {
	path     string
	timeout  time.Duration
	interval time.Duration
}

func NewRequester(commandLimits limits.Command) (*Requester, error) {
	if commandLimits.Timeout <= 0 || commandLimits.MaxStdoutBytes <= 0 || commandLimits.MaxStderrBytes <= 0 {
		return nil, fmt.Errorf("invalid power command limits")
	}
	return newRequester(requestPath, commandLimits.Timeout), nil
}

func newRequester(path string, timeout time.Duration) *Requester {
	return &Requester{path: path, timeout: timeout, interval: 20 * time.Millisecond}
}

func (requester *Requester) RequestPoweroff(parent context.Context) error {
	file, err := os.OpenFile(requester.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0200)
	if err != nil {
		return fmt.Errorf("%w: fixed request could not be created: %v", ErrRejected, err)
	}
	created := true
	defer func() {
		if created {
			_ = os.Remove(requester.path)
		}
	}()
	if _, err := file.WriteString("poweroff\n"); err != nil {
		_ = file.Close()
		return fmt.Errorf("%w: fixed request could not be written: %v", ErrRejected, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("%w: fixed request could not be closed: %v", ErrRejected, err)
	}

	ctx, cancel := context.WithTimeout(parent, requester.timeout)
	defer cancel()
	ticker := time.NewTicker(requester.interval)
	defer ticker.Stop()
	for {
		_, err := os.Lstat(requester.path)
		if errors.Is(err, os.ErrNotExist) {
			created = false
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: fixed request admission could not be verified: %v", ErrRejected, err)
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return ErrTimeout
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
