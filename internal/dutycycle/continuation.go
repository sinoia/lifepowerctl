package dutycycle

import (
	"context"
	"fmt"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type ContinuationState interface {
	DutyCycleLifecycle() schedule.Lifecycle
	DutyCycleConfiguration() (schedule.Configuration, bool)
	RecordNextWakeCalculated(uint64, uint32) error
	BeginWakeProgramming(uint64) error
	RecordWakeVerified(uint64, board.WakeProgrammingResult) error
	FailScheduledRun(string) error
	RecordWakeDisarm(schedule.DisarmStatus) error
	CompleteOneTimeDutyCycle(uint64) error
}

type Continuation struct {
	state ContinuationState
	wake  *WakeController
	audit *activity.Audit
}

func (continuation *Continuation) SetAudit(audit *activity.Audit) { continuation.audit = audit }

func NewContinuation(state ContinuationState, wake *WakeController) (*Continuation, error) {
	if state == nil || wake == nil {
		return nil, fmt.Errorf("continuation state and wake controller are required")
	}
	return &Continuation{state: state, wake: wake}, nil
}

// ArmNext advances a successful real run list through calculated, programming,
// and verified wake. It deliberately stops before shutdown-requested.
func (continuation *Continuation) ArmNext(ctx context.Context) (uint32, error) {
	lifecycle := continuation.state.DutyCycleLifecycle()
	configuration, ok := continuation.state.DutyCycleConfiguration()
	if !ok || !configuration.Enabled || lifecycle.Phase != schedule.PhaseRunListSucceeded || lifecycle.Simulated {
		return 0, fmt.Errorf("real successful run list is not ready for continuation")
	}
	if configuration.Schedule.Kind == schedule.KindOneTime {
		if err := continuation.state.CompleteOneTimeDutyCycle(configuration.Revision); err != nil {
			return 0, continuation.fail(ctx, "could not persist one-time duty-cycle completion", err)
		}
		recordLifecycleTransition(continuation.audit, continuation.state.DutyCycleLifecycle())
		return 0, nil
	}
	reference, err := continuation.wake.ReadRTC(ctx)
	if err != nil {
		return 0, continuation.fail(ctx, "could not read board clock for next wake", err)
	}
	occurrences, err := schedule.Preview(configuration.Schedule, reference, schedule.VerifiedWakeMinimumLead, 1)
	if err != nil || len(occurrences) != 1 {
		if err == nil {
			err = schedule.ErrNoOccurrence
		}
		return 0, continuation.fail(ctx, "could not calculate next verified wake", err)
	}
	occurrence := occurrences[0].Unix
	if err := continuation.state.RecordNextWakeCalculated(configuration.Revision, occurrence); err != nil {
		return 0, continuation.fail(ctx, "could not persist calculated wake", err)
	}
	recordLifecycleTransition(continuation.audit, continuation.state.DutyCycleLifecycle())
	if err := continuation.state.BeginWakeProgramming(configuration.Revision); err != nil {
		return 0, continuation.fail(ctx, "could not persist wake programming intent", err)
	}
	recordLifecycleTransition(continuation.audit, continuation.state.DutyCycleLifecycle())
	result, err := continuation.wake.ProgramAbsolute(ctx, occurrence)
	if err != nil {
		return 0, continuation.fail(ctx, "could not verify programmed wake", err)
	}
	if err := continuation.state.RecordWakeVerified(configuration.Revision, result); err != nil {
		return 0, continuation.fail(ctx, "could not persist verified wake", err)
	}
	recordLifecycleTransition(continuation.audit, continuation.state.DutyCycleLifecycle())
	return occurrence, nil
}

func (continuation *Continuation) fail(ctx context.Context, reason string, cause error) error {
	if err := continuation.state.FailScheduledRun(reason); err != nil {
		return fmt.Errorf("%s: %v; persist manual-safe state: %w", reason, cause, err)
	}
	result, disarmErr := continuation.wake.Disarm(ctx)
	status := schedule.DisarmUncertain
	if disarmErr == nil && result.Verified() {
		status = schedule.DisarmVerified
	}
	if err := continuation.state.RecordWakeDisarm(status); err != nil {
		return fmt.Errorf("%s: %v; persist %s disarm: %w", reason, cause, status, err)
	}
	recordLifecycleTransition(continuation.audit, continuation.state.DutyCycleLifecycle())
	if disarmErr != nil {
		return fmt.Errorf("%s: %v; disarm wake registers: %w", reason, cause, disarmErr)
	}
	return fmt.Errorf("%s: %w", reason, cause)
}
