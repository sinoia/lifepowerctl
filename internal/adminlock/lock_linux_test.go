package adminlock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireIsExclusiveAndRecoverable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	first, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir); !errors.Is(err, ErrHeld) {
		t.Fatalf("second Acquire() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "controller.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %o", info.Mode().Perm())
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireRejectsRelativeDirectory(t *testing.T) {
	if _, err := Acquire("relative"); err == nil {
		t.Fatal("Acquire accepted a relative directory")
	}
}
