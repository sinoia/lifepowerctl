package board

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

var ErrCoordinatorQuiesced = errors.New("hardware coordinator is quiesced")

type OperationKind string

const (
	OperationRead          OperationKind = "read"
	OperationRegisterWrite OperationKind = "register-write"
	OperationWake          OperationKind = "wake"
	OperationFlash         OperationKind = "flash"
	OperationShutdown      OperationKind = "shutdown"
)

type ActiveOperation struct {
	Kind    OperationKind
	Subject string
	Since   time.Time
}

// Coordinator serializes every application-owned board transaction. A lease
// may cover multiple vendor CLI calls so no application read can interleave
// with a mutation workflow.
type Coordinator struct {
	gate        chan struct{}
	mu          sync.Mutex
	now         func() time.Time
	active      *ActiveOperation
	quiesced    bool
	processFile *os.File
}

func NewCoordinator() *Coordinator {
	coordinator := &Coordinator{gate: make(chan struct{}, 1), now: time.Now}
	coordinator.gate <- struct{}{}
	return coordinator
}

// NewProcessCoordinator adds an advisory process-wide lease to the ordinary
// in-process gate. Every process using the same absolute path participates in
// one hardware transaction boundary.
func NewProcessCoordinator(path string) (*Coordinator, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("hardware lock path must be clean and absolute")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open hardware coordinator lock: %w", err)
	}
	coordinator := NewCoordinator()
	coordinator.processFile = file
	return coordinator, nil
}

type Lease struct {
	coordinator *Coordinator
	once        sync.Once
}

func (c *Coordinator) Acquire(ctx context.Context, kind OperationKind, subject string) (*Lease, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.gate:
	}
	c.mu.Lock()
	if c.quiesced {
		c.mu.Unlock()
		c.gate <- struct{}{}
		return nil, ErrCoordinatorQuiesced
	}
	c.mu.Unlock()
	if c.processFile != nil {
		for {
			err := syscall.Flock(int(c.processFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if err == nil {
				break
			}
			if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
				c.gate <- struct{}{}
				return nil, fmt.Errorf("acquire process hardware lease: %w", err)
			}
			select {
			case <-ctx.Done():
				c.gate <- struct{}{}
				return nil, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if err := ctx.Err(); err != nil {
		if c.processFile != nil {
			_ = syscall.Flock(int(c.processFile.Fd()), syscall.LOCK_UN)
		}
		c.gate <- struct{}{}
		return nil, err
	}
	c.mu.Lock()
	c.active = &ActiveOperation{Kind: kind, Subject: subject, Since: c.now().UTC()}
	c.mu.Unlock()
	return &Lease{coordinator: c}, nil
}

func (c *Coordinator) Active() (ActiveOperation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		return ActiveOperation{}, false
	}
	return *c.active, true
}

// WithLease runs operation while holding one ordinary hardware lease. The
// deferred release also runs when operation panics. Shutdown uses its separate
// commit/abort API because its quiesced state must be resolved explicitly.
func (c *Coordinator) WithLease(ctx context.Context, kind OperationKind, subject string, operation func() error) error {
	lease, err := c.Acquire(ctx, kind, subject)
	if err != nil {
		return err
	}
	defer lease.Release()
	return operation()
}

func (l *Lease) Release() {
	l.release(true)
}

func (l *Lease) release(unlockProcess bool) {
	if l == nil || l.coordinator == nil {
		return
	}
	l.once.Do(func() {
		l.coordinator.mu.Lock()
		l.coordinator.active = nil
		l.coordinator.mu.Unlock()
		if unlockProcess && l.coordinator.processFile != nil {
			_ = syscall.Flock(int(l.coordinator.processFile.Fd()), syscall.LOCK_UN)
		}
		l.coordinator.gate <- struct{}{}
	})
}

// BeginShutdown waits for all board work, blocks future acquisitions, and
// returns the exclusive shutdown lease. Callers must defer Abort immediately;
// a later Commit makes that deferred abort a no-op. Abort reopens the
// coordinator when preparation or the power request fails, while Commit keeps
// it quiesced as shutdown proceeds.
func (c *Coordinator) BeginShutdown(ctx context.Context, subject string) (*ShutdownLease, error) {
	lease, err := c.Acquire(ctx, OperationShutdown, subject)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.quiesced = true
	c.mu.Unlock()
	return &ShutdownLease{lease: lease}, nil
}

type ShutdownLease struct {
	lease *Lease
	once  sync.Once
}

func (l *ShutdownLease) Abort() {
	if l == nil || l.lease == nil {
		return
	}
	l.once.Do(func() {
		coordinator := l.lease.coordinator
		coordinator.mu.Lock()
		coordinator.quiesced = false
		coordinator.mu.Unlock()
		l.lease.Release()
	})
}

func (l *ShutdownLease) Commit() {
	if l == nil || l.lease == nil {
		return
	}
	l.once.Do(func() { l.lease.release(false) })
}
