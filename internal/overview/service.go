// Package overview owns the read-only board status shown by the web UI.
package overview

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/limits"
)

type Detector interface {
	Detect(context.Context) (board.Identity, error)
}

type BoardState string

const (
	StateUnknown     BoardState = "unknown"
	StateSupported   BoardState = "supported"
	StateUnsupported BoardState = "unsupported"
	StateUnavailable BoardState = "unavailable"
	StateStale       BoardState = "stale"
)

type Failure string

const (
	FailureNone          Failure = ""
	FailureTimeout       Failure = "timeout"
	FailureOutputLimit   Failure = "output-limit"
	FailureInvalidOutput Failure = "invalid-output"
	FailureCommand       Failure = "command-failed"
	FailureCommunication Failure = "communication"
)

type Snapshot struct {
	State       BoardState
	Identity    board.Identity
	Failure     Failure
	CheckedAt   time.Time
	LastSuccess time.Time
}

type Service struct {
	mu          sync.Mutex
	detector    Detector
	coordinator *board.Coordinator
	minimumAge  time.Duration
	staleAfter  time.Duration
	now         func() time.Time
	snapshot    Snapshot
	audit       *activity.Audit
}

func (service *Service) SetAudit(audit *activity.Audit) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.audit = audit
}

func New(detector Detector, coordinator *board.Coordinator, polling limits.Polling) *Service {
	return &Service{
		detector: detector, coordinator: coordinator, minimumAge: polling.MinimumInterval,
		staleAfter: polling.TelemetryStale, now: time.Now, snapshot: Snapshot{State: StateUnknown},
	}
}

// Refresh returns a recent result when another caller has just completed a
// probe. Holding the service mutex across detection coalesces concurrent page
// loads into one bounded vendor process.
func (service *Service) Refresh(ctx context.Context) Snapshot {
	service.mu.Lock()
	defer service.mu.Unlock()
	now := service.now().UTC()
	age := now.Sub(service.snapshot.CheckedAt)
	if !service.snapshot.CheckedAt.IsZero() && age >= 0 && age < service.minimumAge {
		return service.atTime(now)
	}

	var identity board.Identity
	var detectErr error
	detectErr = service.coordinator.WithLease(ctx, board.OperationRead, "board identity", func() error {
		identity, detectErr = service.detector.Detect(ctx)
		return detectErr
	})
	now = service.now().UTC()
	previousState := service.snapshot.State
	service.snapshot.CheckedAt = now
	switch {
	case detectErr == nil:
		service.snapshot.State = StateSupported
		service.snapshot.Identity = identity
		service.snapshot.Failure = FailureNone
		service.snapshot.LastSuccess = now
	case errors.Is(detectErr, board.ErrUnsupportedRegisterSet):
		service.snapshot.State = StateUnsupported
		service.snapshot.Identity = identity
		service.snapshot.Failure = FailureNone
	case !service.snapshot.LastSuccess.IsZero():
		service.snapshot.State = StateStale
		service.snapshot.Failure = classifyFailure(detectErr)
	default:
		service.snapshot.State = StateUnavailable
		service.snapshot.Identity = board.Identity{}
		service.snapshot.Failure = classifyFailure(detectErr)
	}
	if service.snapshot.State != previousState {
		if previousState == StateUnknown {
			service.audit.Record(activity.BoardObserved, activity.ActorSystem, "board", struct {
				State       BoardState `json:"state"`
				RegisterSet int        `json:"register_set,omitempty"`
				Failure     Failure    `json:"failure,omitempty"`
			}{State: service.snapshot.State, RegisterSet: service.snapshot.Identity.RegisterSet, Failure: service.snapshot.Failure})
		} else {
			service.audit.Record(activity.BoardStateChanged, activity.ActorSystem, "board", struct {
				PreviousState  BoardState `json:"previous_state"`
				ResultingState BoardState `json:"resulting_state"`
				RegisterSet    int        `json:"register_set,omitempty"`
				Failure        Failure    `json:"failure,omitempty"`
			}{PreviousState: previousState, ResultingState: service.snapshot.State, RegisterSet: service.snapshot.Identity.RegisterSet, Failure: service.snapshot.Failure})
		}
	}
	return service.atTime(now)
}

func (service *Service) Current() Snapshot {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.atTime(service.now().UTC())
}

func (service *Service) atTime(now time.Time) Snapshot {
	result := service.snapshot
	if result.State == StateSupported && now.Sub(result.LastSuccess) >= service.staleAfter {
		result.State = StateStale
	}
	return result
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
