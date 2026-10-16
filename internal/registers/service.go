// Package registers owns bounded, read-only catalogue snapshots for the UI.
package registers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/limits"
)

type Detector interface {
	Detect(context.Context) (board.Identity, error)
}

type Reader interface {
	Read(context.Context, int, board.Register) (board.Reading, error)
}

type State string

const (
	StateUnknown     State = "unknown"
	StateSupported   State = "supported"
	StatePartial     State = "partial"
	StateUnsupported State = "unsupported"
	StateUnavailable State = "unavailable"
)

type Failure string

const (
	FailureNone          Failure = ""
	FailureTimeout       Failure = "timeout"
	FailureOutputLimit   Failure = "output-limit"
	FailureInvalidOutput Failure = "invalid-output"
	FailureCommand       Failure = "command-failed"
	FailureCommunication Failure = "communication"
	FailureNotAttempted  Failure = "not-attempted"
)

type Item struct {
	Definition board.Definition
	Reading    *board.Reading
	Failure    Failure
}

type Snapshot struct {
	State      State
	Identity   board.Identity
	Failure    Failure
	CheckedAt  time.Time
	Items      []Item
	Successful int
	Failed     int
}

type Service struct {
	mu          sync.Mutex
	detector    Detector
	reader      Reader
	catalogue   board.Catalog
	coordinator *board.Coordinator
	minimumAge  time.Duration
	timeout     time.Duration
	now         func() time.Time
	snapshot    Snapshot
}

func New(detector Detector, reader Reader, catalogue board.Catalog, coordinator *board.Coordinator, polling limits.Polling) *Service {
	return &Service{
		detector: detector, reader: reader, catalogue: catalogue, coordinator: coordinator,
		minimumAge: polling.MinimumInterval, timeout: polling.RegisterSnapshotTimeout,
		now: time.Now, snapshot: Snapshot{State: StateUnknown},
	}
}

// Refresh coalesces concurrent callers and holds one hardware lease across
// identity detection and every read. The snapshot deadline includes time spent
// waiting for that lease, so a page request cannot create an unbounded queue.
func (service *Service) Refresh(ctx context.Context) Snapshot {
	service.mu.Lock()
	defer service.mu.Unlock()

	now := service.now().UTC()
	age := now.Sub(service.snapshot.CheckedAt)
	if !service.snapshot.CheckedAt.IsZero() && age >= 0 && age < service.minimumAge {
		return cloneSnapshot(service.snapshot)
	}

	snapshotCtx, cancel := context.WithTimeout(ctx, service.timeout)
	defer cancel()
	snapshot := Snapshot{State: StateUnavailable}
	var identity board.Identity
	var detectErr error
	err := service.coordinator.WithLease(snapshotCtx, board.OperationRead, "register snapshot", func() error {
		identity, detectErr = service.detector.Detect(snapshotCtx)
		if detectErr != nil {
			return detectErr
		}
		snapshot.Identity = identity
		for _, definition := range service.catalogue.Definitions() {
			if definition.RegisterSet != identity.RegisterSet {
				continue
			}
			item := Item{Definition: definition}
			if snapshotCtx.Err() != nil {
				item.Failure = FailureNotAttempted
				snapshot.Failed++
				snapshot.Items = append(snapshot.Items, item)
				continue
			}
			reading, readErr := service.reader.Read(snapshotCtx, identity.RegisterSet, definition.Register)
			if readErr != nil {
				item.Failure = classifyFailure(readErr)
				snapshot.Failed++
			} else {
				item.Reading = &reading
				snapshot.Successful++
			}
			snapshot.Items = append(snapshot.Items, item)
		}
		return nil
	})
	snapshot.CheckedAt = service.now().UTC()
	if err != nil {
		snapshot.Identity = identity
		snapshot.Failure = classifyFailure(err)
		if errors.Is(detectErr, board.ErrUnsupportedRegisterSet) {
			snapshot.State = StateUnsupported
			snapshot.Failure = FailureNone
		}
	} else if snapshot.Successful == len(snapshot.Items) && snapshot.Successful > 0 {
		snapshot.State = StateSupported
	} else if snapshot.Successful > 0 {
		snapshot.State = StatePartial
	} else {
		snapshot.State = StateUnavailable
		if snapshotCtx.Err() != nil {
			snapshot.Failure = classifyFailure(snapshotCtx.Err())
		} else if len(snapshot.Items) > 0 {
			snapshot.Failure = snapshot.Items[0].Failure
		}
	}
	service.snapshot = snapshot
	return cloneSnapshot(snapshot)
}

func (service *Service) Current() Snapshot {
	service.mu.Lock()
	defer service.mu.Unlock()
	return cloneSnapshot(service.snapshot)
}

// Invalidate forces the next Refresh to read the board rather than reuse the
// short-lived snapshot. Register mutation paths call this for every attempted
// apply, including uncertain outcomes.
func (service *Service) Invalidate() {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.snapshot.CheckedAt = time.Time{}
}

func (service *Service) Definition(register board.Register) (board.Definition, bool) {
	return service.catalogue.Find(7, register)
}

func classifyFailure(err error) Failure {
	switch {
	case errors.Is(err, board.ErrVendorTimeout), errors.Is(err, context.DeadlineExceeded):
		return FailureTimeout
	case errors.Is(err, board.ErrVendorOutputLimit):
		return FailureOutputLimit
	case errors.Is(err, board.ErrInvalidVendorOutput):
		return FailureInvalidOutput
	case errors.Is(err, board.ErrVendorCommand):
		return FailureCommand
	default:
		return FailureCommunication
	}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	clone := snapshot
	clone.Items = make([]Item, len(snapshot.Items))
	for index, item := range snapshot.Items {
		clone.Items[index] = item
		if item.Reading != nil {
			reading := *item.Reading
			if item.Reading.RawValue != nil {
				raw := *item.Reading.RawValue
				reading.RawValue = &raw
			}
			clone.Items[index].Reading = &reading
		}
	}
	return clone
}
