package dutycycle

import (
	"context"
	"fmt"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type StartupState interface {
	DutyCycleArmedIntent() schedule.ArmedIntent
	DutyCycleLifecycle() schedule.Lifecycle
	ObserveExpectedBoot() error
	EnterBootManualSafe(string) error
	RecordWakeDisarm(schedule.DisarmStatus) error
}

type StartupResult struct {
	Classification schedule.Classification
	DisarmStatus   schedule.DisarmStatus
}

type PendingDisarmState interface {
	DutyCycleLifecycle() schedule.Lifecycle
	RecordWakeDisarm(schedule.DisarmStatus) error
}

// ReconcilePendingDisarm completes the hardware half of conservative restart
// recovery after state has entered real manual-safe with a pending disarm.
func ReconcilePendingDisarm(ctx context.Context, state PendingDisarmState, wake *WakeController) (bool, error, error) {
	if state == nil || wake == nil {
		return false, nil, fmt.Errorf("pending disarm state and wake controller are required")
	}
	lifecycle := state.DutyCycleLifecycle()
	if lifecycle.Phase != schedule.PhaseManualSafe || lifecycle.Simulated || lifecycle.WakeDisarmStatus != schedule.DisarmPending {
		return false, nil, nil
	}
	disarm, disarmErr := wake.Disarm(ctx)
	status := schedule.DisarmUncertain
	if disarmErr == nil && disarm.Verified() {
		status = schedule.DisarmVerified
	}
	if err := state.RecordWakeDisarm(status); err != nil {
		return true, disarmErr, fmt.Errorf("persist reconciled wake disarm status: %w", err)
	}
	return true, disarmErr, nil
}

// ClassifyStartup consumes an expected wake exactly once. Every non-expected
// armed result is durably made manual-safe before disarm is attempted.
func ClassifyStartup(ctx context.Context, state StartupState, wake *WakeController, policy schedule.BootPolicy) (StartupResult, error) {
	return ClassifyStartupWithAudit(ctx, state, wake, policy, nil)
}

func ClassifyStartupWithAudit(ctx context.Context, state StartupState, wake *WakeController, policy schedule.BootPolicy, audit *activity.Audit) (StartupResult, error) {
	if state == nil || wake == nil {
		return StartupResult{}, fmt.Errorf("startup state and wake controller are required")
	}
	intent := state.DutyCycleArmedIntent()
	if !intent.Enabled {
		return StartupResult{Classification: schedule.ClassifyBoot(intent, schedule.Observation{}, policy)}, nil
	}
	observed, observeErr := wake.ObserveBoot(ctx)
	classification := schedule.ClassifyBoot(intent, observed, policy)
	if observeErr != nil {
		classification = schedule.Classification{Class: schedule.BootAmbiguous, Reason: "wake registers could not be read during startup classification"}
	}
	if classification.Class == schedule.BootExpected {
		if err := state.ObserveExpectedBoot(); err != nil {
			return StartupResult{Classification: classification}, fmt.Errorf("persist expected boot: %w", err)
		}
		recordLifecycleTransition(audit, state.DutyCycleLifecycle())
		return StartupResult{Classification: classification}, nil
	}
	if err := state.EnterBootManualSafe(classification.Reason); err != nil {
		return StartupResult{Classification: classification}, fmt.Errorf("persist startup manual-safe state: %w", err)
	}
	disarm, disarmErr := wake.Disarm(ctx)
	status := schedule.DisarmUncertain
	if disarmErr == nil && disarm.Verified() {
		status = schedule.DisarmVerified
	}
	if err := state.RecordWakeDisarm(status); err != nil {
		return StartupResult{Classification: classification, DisarmStatus: status}, fmt.Errorf("persist wake disarm status: %w", err)
	}
	recordLifecycleTransition(audit, state.DutyCycleLifecycle())
	if disarmErr != nil {
		return StartupResult{Classification: classification, DisarmStatus: status}, fmt.Errorf("disarm wake registers: %w", disarmErr)
	}
	return StartupResult{Classification: classification, DisarmStatus: status}, nil
}
