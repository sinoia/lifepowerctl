package dutycycle

import (
	"context"
	"fmt"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type ShutdownState interface {
	DutyCycleLifecycle() schedule.Lifecycle
	DutyCycleConfiguration() (schedule.Configuration, bool)
	RecordShutdownRequested(uint64) error
	FailScheduledRun(string) error
	RecordWakeDisarm(schedule.DisarmStatus) error
}

type PoweroffRequester interface {
	RequestPoweroff(context.Context) error
}

type ShutdownController struct {
	state       ShutdownState
	wake        *WakeController
	coordinator *board.Coordinator
	power       PoweroffRequester
	audit       *activity.Audit
}

func (controller *ShutdownController) SetAudit(audit *activity.Audit) { controller.audit = audit }

func NewShutdownController(state ShutdownState, wake *WakeController, coordinator *board.Coordinator, power PoweroffRequester) (*ShutdownController, error) {
	if state == nil || wake == nil || coordinator == nil || power == nil {
		return nil, fmt.Errorf("shutdown state, wake controller, coordinator, and power requester are required")
	}
	return &ShutdownController{state: state, wake: wake, coordinator: coordinator, power: power}, nil
}

func (controller *ShutdownController) Request(ctx context.Context) error {
	configuration, lifecycle, err := controller.verifiedState()
	if err != nil {
		return err
	}
	shutdown, err := controller.coordinator.BeginShutdown(ctx, "verified duty-cycle poweroff")
	if err != nil {
		return err
	}
	defer shutdown.Abort()
	currentConfiguration, currentLifecycle, err := controller.verifiedState()
	if err != nil || currentConfiguration.Revision != configuration.Revision || currentLifecycle.IntendedOccurrence != lifecycle.IntendedOccurrence {
		if err == nil {
			err = fmt.Errorf("verified wake changed while waiting for shutdown lease")
		}
		return err
	}
	if err := controller.state.RecordShutdownRequested(configuration.Revision); err != nil {
		return fmt.Errorf("persist shutdown request: %w", err)
	}
	recordLifecycleTransition(controller.audit, controller.state.DutyCycleLifecycle())
	if err := controller.power.RequestPoweroff(ctx); err != nil {
		shutdown.Abort()
		return controller.failAfterRequest(ctx, err)
	}
	shutdown.Commit()
	return nil
}

func (controller *ShutdownController) verifiedState() (schedule.Configuration, schedule.Lifecycle, error) {
	configuration, ok := controller.state.DutyCycleConfiguration()
	lifecycle := controller.state.DutyCycleLifecycle()
	if !ok || !configuration.Enabled || lifecycle.Phase != schedule.PhaseWakeVerified || lifecycle.Simulated || lifecycle.ConfigurationRevision != configuration.Revision || lifecycle.WakeKind != schedule.WakeAbsoluteRTC || lifecycle.IntendedOccurrence == 0 || lifecycle.ProgrammedRTCWake != lifecycle.IntendedOccurrence || lifecycle.ProgrammedWakeMinutes != 0 {
		return schedule.Configuration{}, lifecycle, fmt.Errorf("exact verified wake is required before shutdown")
	}
	return configuration, lifecycle, nil
}

func (controller *ShutdownController) failAfterRequest(ctx context.Context, cause error) error {
	const reason = "orderly shutdown request was not accepted"
	if err := controller.state.FailScheduledRun(reason); err != nil {
		return fmt.Errorf("%s: %v; persist manual-safe state: %w", reason, cause, err)
	}
	result, disarmErr := controller.wake.Disarm(ctx)
	status := schedule.DisarmUncertain
	if disarmErr == nil && result.Verified() {
		status = schedule.DisarmVerified
	}
	if err := controller.state.RecordWakeDisarm(status); err != nil {
		return fmt.Errorf("%s: %v; persist %s disarm: %w", reason, cause, status, err)
	}
	recordLifecycleTransition(controller.audit, controller.state.DutyCycleLifecycle())
	if disarmErr != nil {
		return fmt.Errorf("%s: %v; disarm wake registers: %w", reason, cause, disarmErr)
	}
	return fmt.Errorf("%s: %w", reason, cause)
}
