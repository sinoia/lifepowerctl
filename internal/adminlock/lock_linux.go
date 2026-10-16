// Package adminlock serializes the controller with offline administrative
// commands that modify its state.
package adminlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var ErrHeld = errors.New("controller administrative lock is held")

type Lock struct {
	file *os.File
}

// Acquire takes the state directory's process-wide lock without waiting.
func Acquire(stateDirectory string) (*Lock, error) {
	if !filepath.IsAbs(stateDirectory) || filepath.Clean(stateDirectory) != stateDirectory {
		return nil, fmt.Errorf("state directory must be a clean absolute path")
	}
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	if err := os.Chmod(stateDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("secure state directory: %w", err)
	}
	path := filepath.Join(stateDirectory, "controller.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open administrative lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure administrative lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("acquire administrative lock: %w", err)
	}
	return &Lock{file: file}, nil
}

func (lock *Lock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
		_ = file.Close()
		return fmt.Errorf("release administrative lock: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close administrative lock: %w", err)
	}
	return nil
}
