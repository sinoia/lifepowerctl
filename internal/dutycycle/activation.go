package dutycycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/controllerstate"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type ActivationState interface {
	Revision() uint64
	DutyCycleConfiguration() (schedule.Configuration, bool)
	DutyCycleLifecycle() schedule.Lifecycle
	DutyCycleRunListMaximum() (int, uint64, error)
	RealActivationResult(string, uint64, uint64, string, uint32) (controllerstate.ConfigurationResult, error)
	EnableReal(string, uint64, uint64, string, uint32, uint32) (controllerstate.ConfigurationResult, error)
	BeginWakeProgramming(uint64) error
	RecordWakeVerified(uint64, board.WakeProgrammingResult) error
	FailScheduledRun(string) error
	RecordWakeDisarm(schedule.DisarmStatus) error
}

type ShutdownRequester interface {
	Request(context.Context) error
}

type ConfirmationRequiredError struct {
	Review schedule.Review
}

func (err *ConfirmationRequiredError) Error() string {
	return "the first board-clock occurrence changed and requires confirmation"
}

type Activator struct {
	state    ActivationState
	wake     *WakeController
	shutdown ShutdownRequester
	audit    *activity.Audit
}

func (activator *Activator) SetAudit(audit *activity.Audit) { activator.audit = audit }

func NewActivator(state ActivationState, wake *WakeController, shutdown ShutdownRequester) (*Activator, error) {
	if state == nil || wake == nil || shutdown == nil {
		return nil, fmt.Errorf("activation state, wake controller, and shutdown requester are required")
	}
	return &Activator{state: state, wake: wake, shutdown: shutdown}, nil
}

func (activator *Activator) Preview(ctx context.Context, configurationRevision uint64, count int) (schedule.Review, error) {
	configuration, ok := activator.state.DutyCycleConfiguration()
	if !ok || configuration.Enabled || configuration.Revision != configurationRevision {
		return schedule.Review{}, controllerstate.ErrConfigurationRevision
	}
	reference, err := activator.wake.ReadRTC(ctx)
	if err != nil {
		return schedule.Review{}, fmt.Errorf("read board clock: %w", err)
	}
	review, err := schedule.BuildReview(configuration, reference, schedule.VerifiedWakeMinimumLead, count)
	if err != nil {
		return schedule.Review{}, err
	}
	jobs, seconds, err := activator.state.DutyCycleRunListMaximum()
	if err != nil {
		return schedule.Review{}, err
	}
	review.RunListJobs, review.MaximumRunSeconds = jobs, seconds
	return review, nil
}

func (activator *Activator) Enable(ctx context.Context, proposalID string, baseRevision, configurationRevision uint64, reviewed schedule.Review) (controllerstate.ConfigurationResult, error) {
	if len(reviewed.Occurrences) == 0 {
		return controllerstate.ConfigurationResult{}, fmt.Errorf("real activation requires occurrence review")
	}
	reviewedOccurrence := reviewed.Occurrences[0].Unix
	if existing, err := activator.state.RealActivationResult(proposalID, baseRevision, configurationRevision, reviewed.Identity, reviewedOccurrence); err == nil {
		return existing, nil
	} else if !errors.Is(err, controllerstate.ErrProposalNotFound) {
		return controllerstate.ConfigurationResult{}, err
	}
	configuration, ok := activator.state.DutyCycleConfiguration()
	if !ok || configuration.Enabled || configuration.Revision != configurationRevision {
		return controllerstate.ConfigurationResult{}, controllerstate.ErrConfigurationRevision
	}
	want, err := schedule.BuildReview(configuration, reviewed.Reference, schedule.VerifiedWakeMinimumLead, len(reviewed.Occurrences))
	if err == nil {
		jobs, seconds, maximumErr := activator.state.DutyCycleRunListMaximum()
		if maximumErr != nil {
			err = maximumErr
		} else {
			want.RunListJobs, want.MaximumRunSeconds = jobs, seconds
		}
	}
	if err != nil || reviewed.MinimumLeadSeconds != uint32(schedule.VerifiedWakeMinimumLead.Seconds()) || want.Identity != reviewed.Identity || want.RunListJobs != reviewed.RunListJobs || want.MaximumRunSeconds != reviewed.MaximumRunSeconds {
		return controllerstate.ConfigurationResult{}, fmt.Errorf("real activation review is invalid")
	}
	fresh, err := activator.Preview(ctx, configurationRevision, len(reviewed.Occurrences))
	if err != nil {
		return controllerstate.ConfigurationResult{}, err
	}
	occurrence := fresh.Occurrences[0].Unix
	if configuration.Schedule.Kind != schedule.KindAfterCompletion && occurrence != reviewedOccurrence {
		return controllerstate.ConfigurationResult{}, &ConfirmationRequiredError{Review: fresh}
	}
	result, err := activator.state.EnableReal(proposalID, baseRevision, configurationRevision, reviewed.Identity, reviewedOccurrence, occurrence)
	if err != nil {
		return controllerstate.ConfigurationResult{}, err
	}
	if activator.audit != nil {
		activator.audit.Record(activity.DutyCycleConfiguration, activity.ActorSystem, "duty-cycle", struct {
			Action                string `json:"action"`
			StateRevision         uint64 `json:"state_revision"`
			ConfigurationRevision uint64 `json:"configuration_revision"`
			Simulated             bool   `json:"simulated"`
		}{Action: "enable-real", StateRevision: result.StateRevision, ConfigurationRevision: result.ConfigurationRevision, Simulated: false})
	}
	recordLifecycleTransition(activator.audit, activator.state.DutyCycleLifecycle())
	if err := activator.state.BeginWakeProgramming(configurationRevision); err != nil {
		return result, activator.fail(ctx, "could not persist initial wake programming intent", err)
	}
	recordLifecycleTransition(activator.audit, activator.state.DutyCycleLifecycle())
	hardware, err := activator.wake.ProgramAbsolute(ctx, occurrence)
	if err != nil {
		return result, activator.fail(ctx, "could not verify initial programmed wake", err)
	}
	if err := activator.state.RecordWakeVerified(configurationRevision, hardware); err != nil {
		return result, activator.fail(ctx, "could not persist initial verified wake", err)
	}
	recordLifecycleTransition(activator.audit, activator.state.DutyCycleLifecycle())
	if err := activator.shutdown.Request(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func (activator *Activator) fail(ctx context.Context, reason string, cause error) error {
	if err := activator.state.FailScheduledRun(reason); err != nil {
		return fmt.Errorf("%s: %v; persist manual-safe state: %w", reason, cause, err)
	}
	result, disarmErr := activator.wake.Disarm(ctx)
	status := schedule.DisarmUncertain
	if disarmErr == nil && result.Verified() {
		status = schedule.DisarmVerified
	}
	if err := activator.state.RecordWakeDisarm(status); err != nil {
		return fmt.Errorf("%s: %v; persist %s disarm: %w", reason, cause, status, err)
	}
	recordLifecycleTransition(activator.audit, activator.state.DutyCycleLifecycle())
	if disarmErr != nil {
		return fmt.Errorf("%s: %v; disarm wake registers: %w", reason, cause, disarmErr)
	}
	return fmt.Errorf("%s: %w", reason, cause)
}
