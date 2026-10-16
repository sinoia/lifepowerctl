//go:build linux

package jobs

import (
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestPeerUID(t *testing.T) {
	sockets, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	firstFile := os.NewFile(uintptr(sockets[0]), "peer-test-first")
	secondFile := os.NewFile(uintptr(sockets[1]), "peer-test-second")
	defer firstFile.Close()
	defer secondFile.Close()
	connection, err := net.FileConn(firstFile)
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox blocks Unix socket inspection")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		t.Fatalf("FileConn() returned %T", connection)
	}
	uid, err := PeerUID(unixConnection)
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox blocks SO_PEERCRED")
	}
	if err != nil {
		t.Fatal(err)
	}
	if uid != uint32(os.Getuid()) {
		t.Fatalf("PeerUID() = %d, want %d", uid, os.Getuid())
	}
}
