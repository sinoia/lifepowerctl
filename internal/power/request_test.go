package power

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRequesterUsesOnlyFixedAdmissionFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "poweroff.request")
	requester := newRequester(path, time.Second)
	admitted := make(chan os.FileMode, 1)
	go func() {
		for {
			info, err := os.Lstat(path)
			if err == nil {
				admitted <- info.Mode().Perm()
				_ = os.Remove(path)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	if err := requester.RequestPoweroff(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := <-admitted; got != 0200 {
		t.Fatalf("request mode = %o", got)
	}
}

func TestRequesterRejectsExistingRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "poweroff.request")
	if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := newRequester(path, time.Second).RequestPoweroff(context.Background()); !errors.Is(err, ErrRejected) {
		t.Fatalf("existing request error = %v", err)
	}
}

func TestRequesterTimesOutAndRemovesUnadmittedRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "poweroff.request")
	requester := newRequester(path, 30*time.Millisecond)
	requester.interval = time.Millisecond
	if err := requester.RequestPoweroff(context.Background()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("RequestPoweroff() error = %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("request remained after timeout: %v", err)
	}
}
