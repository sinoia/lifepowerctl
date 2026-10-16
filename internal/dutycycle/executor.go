// Package dutycycle coordinates automatic run lists and their power boundary.
package dutycycle

import (
	"context"
	"fmt"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/controllerstate"
	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type State interface {
	ObserveExpectedSimulatedBoot() error
	PrepareScheduledJob(string) (controllerstate.ScheduledJob, error)
	MarkScheduledJobRunning(string) error
	CompleteScheduledJob(jobs.Response) (bool, error)
	FailScheduledRun(string) error
	DutyCycleLifecycle() schedule.Lifecycle
	DutyCycleConfiguration() (schedule.Configuration, bool)
	RecordNextSimulatedDecision(uint64, uint32) error
	CompleteOneTimeSimulation(uint64) error
}

type Runner interface {
	Do(context.Context, jobs.Request) (jobs.Response, error)
}

type Executor struct {
	state        State
	runner       Runner
	pollInterval time.Duration
	newRunID     func() (string, error)
	wait         func(context.Context, time.Duration) error
	now          func() time.Time
	minimumLead  time.Duration
	audit        *activity.Audit
}

func NewExecutor(state State, runner Runner, pollInterval time.Duration) (*Executor, error) {
	if state == nil || runner == nil || pollInterval <= 0 {
		return nil, fmt.Errorf("invalid duty-cycle executor configuration")
	}
	return &Executor{state: state, runner: runner, pollInterval: pollInterval, newRunID: jobs.NewRunID, wait: waitContext, now: time.Now, minimumLead: schedule.SimulatedMinimumLead}, nil
}

func (executor *Executor) ObserveExpectedSimulatedBoot() error {
	if err := executor.state.ObserveExpectedSimulatedBoot(); err != nil {
		return err
	}
	executor.recordTransition(schedule.PhaseExpectedBootObserved, "", "")
	return nil
}

func (executor *Executor) SetAudit(audit *activity.Audit) { executor.audit = audit }

// RunExpectedSimulatedBoot consumes one injected occurrence and runs its saved
// jobs strictly in order. It never calculates a wake or requests power.
func (executor *Executor) RunExpectedSimulatedBoot(ctx context.Context) error {
	if err := executor.state.ObserveExpectedSimulatedBoot(); err != nil {
		return err
	}
	return executor.RunObserved(ctx)
}

// RunObserved starts only after expected-boot-observed was durably persisted.
// It is separated so the controller protocol can acknowledge consumption
// before any potentially long-running job begins.
func (executor *Executor) RunObserved(ctx context.Context) error {
	observed := executor.state.DutyCycleLifecycle()
	if observed.Phase != schedule.PhaseExpectedBootObserved {
		return fmt.Errorf("expected boot has not been durably observed")
	}
	simulated := observed.Simulated
	for executor.state.DutyCycleLifecycle().Phase != schedule.PhaseRunListSucceeded {
		runID, err := executor.newRunID()
		if err != nil {
			return executor.fail("could not generate scheduled run identity", err)
		}
		prepared, err := executor.state.PrepareScheduledJob(runID)
		if err != nil {
			return executor.fail("could not prepare scheduled job", err)
		}
		executor.recordTransition(schedule.PhasePreparingRun, prepared.Definition.ID, runID)
		response, err := executor.runner.Do(ctx, prepared.Definition.Start(runID))
		if err != nil {
			return executor.fail("scheduled job start outcome was not trustworthy", err)
		}
		if err := executor.state.MarkScheduledJobRunning(runID); err != nil {
			return executor.fail("could not persist running scheduled job", err)
		}
		executor.recordTransition(schedule.PhaseRunningJob, prepared.Definition.ID, runID)
		for response.State == jobs.StateQueued || response.State == jobs.StateRunning {
			if err := executor.wait(ctx, executor.pollInterval); err != nil {
				_, _ = executor.runner.Do(context.Background(), jobs.Request{Version: jobs.ProtocolVersion, Action: jobs.ActionCancel, RunID: runID})
				return executor.fail("scheduled job monitoring was interrupted", err)
			}
			response, err = executor.runner.Do(ctx, jobs.Request{Version: jobs.ProtocolVersion, Action: jobs.ActionStatus, RunID: runID})
			if err != nil {
				return executor.fail("scheduled job status became unavailable", err)
			}
		}
		succeeded, err := executor.state.CompleteScheduledJob(response)
		if err != nil {
			return executor.fail("could not persist scheduled job result", err)
		}
		phase := executor.state.DutyCycleLifecycle().Phase
		executor.recordTransition(phase, prepared.Definition.ID, runID)
		if !succeeded {
			return fmt.Errorf("scheduled job %s did not succeed", prepared.Definition.Name)
		}
	}
	if !simulated {
		return nil
	}
	return executor.finishSuccessfulSimulation()
}

func (executor *Executor) finishSuccessfulSimulation() error {
	configuration, ok := executor.state.DutyCycleConfiguration()
	if !ok || !configuration.Enabled {
		return executor.fail("successful run lost its enabled configuration", fmt.Errorf("configuration unavailable"))
	}
	if configuration.Schedule.Kind == schedule.KindOneTime {
		if err := executor.state.CompleteOneTimeSimulation(configuration.Revision); err != nil {
			return executor.fail("could not persist one-time schedule completion", err)
		}
		executor.recordTransition(schedule.PhaseCompleted, "", "")
		return nil
	}
	now := executor.now().Unix()
	if now < 0 || uint64(now) > uint64(^uint32(0)) {
		return executor.fail("fresh simulated board time was outside the supported range", fmt.Errorf("time %d", now))
	}
	occurrences, err := schedule.Preview(configuration.Schedule, uint32(now), executor.minimumLead, 1)
	if err != nil || len(occurrences) != 1 {
		if err == nil {
			err = schedule.ErrNoOccurrence
		}
		return executor.fail("could not calculate the next simulated occurrence", err)
	}
	if err := executor.state.RecordNextSimulatedDecision(configuration.Revision, occurrences[0].Unix); err != nil {
		return executor.fail("could not persist the next simulated decision", err)
	}
	executor.recordTransition(schedule.PhaseSimulatedShutdownIntended, "", "")
	return nil
}

func (executor *Executor) fail(reason string, cause error) error {
	if err := executor.state.FailScheduledRun(reason); err != nil {
		return fmt.Errorf("%s: %v; persist manual-safe state: %w", reason, cause, err)
	}
	executor.recordTransition(schedule.PhaseManualSafe, "", "")
	return fmt.Errorf("%s: %w", reason, cause)
}

func (executor *Executor) recordTransition(phase schedule.Phase, jobID, runID string) {
	state := executor.state.DutyCycleLifecycle()
	executor.audit.Record(activity.DutyCycleTransition, activity.ActorSystem, "duty-cycle", struct {
		Phase                 schedule.Phase `json:"phase"`
		ConfigurationRevision uint64         `json:"configuration_revision"`
		JobID                 string         `json:"job_id,omitempty"`
		RunID                 string         `json:"run_id,omitempty"`
		Simulated             bool           `json:"simulated"`
	}{Phase: phase, ConfigurationRevision: state.ConfigurationRevision, JobID: jobID, RunID: runID, Simulated: state.Simulated})
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// RecoverRunner is called only after startup reconciliation has durably
// entered manual-safe. It best-effort cancels the one run whose launch or
// completion was uncertain; errors are returned for diagnostics only.
func RecoverRunner(ctx context.Context, state schedule.Lifecycle, runner Runner) error {
	if runner == nil || state.Phase != schedule.PhaseManualSafe {
		return nil
	}
	for _, entry := range state.RunList {
		if entry.State != schedule.RunFailed || entry.RunID == "" {
			continue
		}
		_, err := runner.Do(ctx, jobs.Request{Version: jobs.ProtocolVersion, Action: jobs.ActionCancel, RunID: entry.RunID})
		if err != nil {
			return fmt.Errorf("cancel uncertain scheduled run %s: %w", entry.RunID, err)
		}
		return nil
	}
	return nil
}
