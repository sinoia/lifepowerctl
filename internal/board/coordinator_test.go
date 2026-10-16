package board

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCoordinatorSerializesAndCancels(t *testing.T) {
	coordinator := NewCoordinator()
	first, err := coordinator.Acquire(context.Background(), OperationWake, "next wake")
	if err != nil {
		t.Fatal(err)
	}
	active, ok := coordinator.Active()
	if !ok || active.Kind != OperationWake || active.Subject != "next wake" {
		t.Fatalf("Active() = %+v, %v", active, ok)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := coordinator.Acquire(ctx, OperationRead, "telemetry"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked Acquire() error = %v", err)
	}
	first.Release()
	second, err := coordinator.Acquire(context.Background(), OperationRead, "telemetry")
	if err != nil {
		t.Fatal(err)
	}
	second.Release()
}

func TestCoordinatorShutdownCommitAndAbort(t *testing.T) {
	committed := NewCoordinator()
	shutdown, err := committed.BeginShutdown(context.Background(), "automatic duty cycle")
	if err != nil {
		t.Fatal(err)
	}
	shutdown.Commit()
	if _, err := committed.Acquire(context.Background(), OperationRead, "after commit"); !errors.Is(err, ErrCoordinatorQuiesced) {
		t.Fatalf("Acquire() after Commit error = %v", err)
	}

	aborted := NewCoordinator()
	shutdown, err = aborted.BeginShutdown(context.Background(), "failed request")
	if err != nil {
		t.Fatal(err)
	}
	shutdown.Abort()
	lease, err := aborted.Acquire(context.Background(), OperationRead, "after abort")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

func TestCoordinatorReleaseIsIdempotent(t *testing.T) {
	coordinator := NewCoordinator()
	lease, err := coordinator.Acquire(context.Background(), OperationRead, "identity")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	lease.Release()

	next, err := coordinator.Acquire(context.Background(), OperationRead, "telemetry")
	if err != nil {
		t.Fatal(err)
	}
	next.Release()
}

func TestCoordinatorWithLeaseReleasesAfterPanic(t *testing.T) {
	coordinator := NewCoordinator()
	func() {
		defer func() {
			if recovered := recover(); recovered != "injected panic" {
				t.Fatalf("recover() = %v", recovered)
			}
		}()
		_ = coordinator.WithLease(context.Background(), OperationRead, "panicking read", func() error {
			panic("injected panic")
		})
	}()

	if _, active := coordinator.Active(); active {
		t.Fatal("coordinator remained active after panic")
	}
	lease, err := coordinator.Acquire(context.Background(), OperationRead, "after panic")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

func TestCoordinatorShutdownDeferredAbortAfterPanic(t *testing.T) {
	coordinator := NewCoordinator()
	func() {
		shutdown, err := coordinator.BeginShutdown(context.Background(), "panicking preparation")
		if err != nil {
			t.Fatal(err)
		}
		defer shutdown.Abort()
		defer func() {
			if recovered := recover(); recovered != "injected panic" {
				t.Fatalf("recover() = %v", recovered)
			}
		}()
		panic("injected panic")
	}()

	lease, err := coordinator.Acquire(context.Background(), OperationRead, "after shutdown panic")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

func TestProcessCoordinatorsSerializeAcrossIndependentInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hardware.lock")
	first, err := NewProcessCoordinator(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewProcessCoordinator(path)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := first.Acquire(context.Background(), OperationWake, "wake programming")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := second.Acquire(ctx, OperationRegisterWrite, "web register write"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second process coordinator error = %v", err)
	}
	lease.Release()
	secondLease, err := second.Acquire(context.Background(), OperationRegisterWrite, "web register write")
	if err != nil {
		t.Fatal(err)
	}
	secondLease.Release()
}

func TestProcessShutdownCommitKeepsOtherProcessesBlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hardware.lock")
	controller, err := NewProcessCoordinator(path)
	if err != nil {
		t.Fatal(err)
	}
	web, err := NewProcessCoordinator(path)
	if err != nil {
		t.Fatal(err)
	}
	shutdown, err := controller.BeginShutdown(context.Background(), "accepted shutdown")
	if err != nil {
		t.Fatal(err)
	}
	shutdown.Commit()
	if _, err := controller.Acquire(context.Background(), OperationRead, "controller after commit"); !errors.Is(err, ErrCoordinatorQuiesced) {
		t.Fatalf("controller acquire after commit = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := web.Acquire(ctx, OperationRead, "web during shutdown"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("web acquire after shutdown commit = %v", err)
	}
}
