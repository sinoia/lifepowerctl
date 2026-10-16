//go:build linux

package jobs

import (
	"fmt"
	"net"
	"syscall"
)

// PeerUID returns the kernel-reported UID at the other end of a connected
// Unix socket. Path permissions alone are not accepted as caller identity.
func PeerUID(connection *net.UnixConn) (uint32, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("access Unix connection: %w", err)
	}
	var credentials *syscall.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("inspect Unix peer: %w", err)
	}
	if socketErr != nil {
		return 0, fmt.Errorf("read Unix peer credentials: %w", socketErr)
	}
	if credentials == nil {
		return 0, fmt.Errorf("read Unix peer credentials: no credentials returned")
	}
	return credentials.Uid, nil
}
