package server

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// HostStatus supplies small, read-only host facts for owner context. Keeping
// it behind an interface makes the web presentation deterministic in tests
// and leaves room for later host-specific status without coupling it to board
// detection.
type HostStatus interface {
	Hostname() (string, error)
	Uptime() (time.Duration, error)
}

type systemHostStatus struct{}

func (systemHostStatus) Hostname() (string, error) {
	return os.Hostname()
}

func (systemHostStatus) Uptime() (time.Duration, error) {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0, err
	}
	return time.Duration(info.Uptime) * time.Second, nil
}
