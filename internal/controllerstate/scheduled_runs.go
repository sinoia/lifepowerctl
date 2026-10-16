package controllerstate

import (
	"fmt"

	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type ScheduledJob struct {
	Index      int
	RunID      string
	Definition jobs.Definition
}

// ObserveExpectedSimulatedBoot consumes the occurrence before any job may be
// prepared. This is an injected boundary, not real board evidence.
func (manager *Manager) ObserveExpectedSimulatedBoot() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := manager.state.DutyCycleLifecycle
	if manager.state.DutyCycle == nil || !manager.state.DutyCycle.Enabled || state.Phase != schedule.PhaseSimulatedShutdownIntended || len(manager.state.DutyCycle.RunList) == 0 {
		return fmt.Errorf("%w: no simulated occurrence is ready", ErrActivation)
	}
	previous := cloneLifecycle(state)
	entries := make([]schedule.RunEntry, len(manager.state.DutyCycle.RunList))
	for index, entry := range manager.state.DutyCycle.RunList {
		entries[index] = schedule.RunEntry{JobID: entry.JobID, JobRevision: entry.JobRevision, State: schedule.RunPending}
	}
	manager.state.DutyCycleLifecycle = schedule.Lifecycle{
		Phase: schedule.PhaseExpectedBootObserved, ConfigurationRevision: manager.state.DutyCycle.Revision,
		IntendedOccurrence: state.IntendedOccurrence, Simulated: true, RunList: entries,
	}
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycleLifecycle = previous
		return fmt.Errorf("persist expected simulated boot: %w", err)
	}
	return nil
}

// PrepareScheduledJob durably records the stable run ID before the runner is
// contacted. A restart from this point is uncertain and cannot launch again.
func (manager *Manager) PrepareScheduledJob(runID string) (ScheduledJob, error) {
	if !validProposalID(runID) {
		return ScheduledJob{}, fmt.Errorf("invalid scheduled run ID")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := &manager.state.DutyCycleLifecycle
	if state.Phase != schedule.PhaseExpectedBootObserved {
		return ScheduledJob{}, fmt.Errorf("scheduled run is not ready to prepare")
	}
	index := -1
	for candidate := range state.RunList {
		if state.RunList[candidate].State == schedule.RunPending {
			index = candidate
			break
		}
	}
	if index < 0 {
		return ScheduledJob{}, fmt.Errorf("scheduled run list has no pending job")
	}
	entry := &state.RunList[index]
	jobIndex := manager.indexLocked(entry.JobID)
	if jobIndex < 0 || manager.state.Jobs[jobIndex].Revision != entry.JobRevision || !manager.state.Jobs[jobIndex].Enabled {
		return ScheduledJob{}, fmt.Errorf("%w: scheduled job snapshot is unavailable", ErrJobReference)
	}
	previous := cloneLifecycle(*state)
	previousRuns := append([]Run(nil), manager.state.Runs...)
	entry.RunID = runID
	entry.State = schedule.RunPreparing
	state.Phase = schedule.PhasePreparingRun
	now := manager.now().UTC()
	definition := manager.state.Jobs[jobIndex]
	manager.state.Runs = append(manager.state.Runs, Run{
		RunID: runID, JobID: definition.ID, DisplayJobID: definition.ID, JobRevision: definition.Revision,
		Trigger: RunTriggerScheduled, State: jobs.StateQueued, CreatedAt: now, UpdatedAt: now,
		Occurrence: state.IntendedOccurrence, RunListPosition: index + 1,
	})
	manager.pruneRunsLocked()
	if len(manager.state.Runs) > manager.limits.RetainedRunsTotal {
		manager.state.DutyCycleLifecycle = previous
		manager.state.Runs = previousRuns
		return ScheduledJob{}, ErrRunLimit
	}
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycleLifecycle = previous
		manager.state.Runs = previousRuns
		return ScheduledJob{}, fmt.Errorf("persist scheduled job preparation: %w", err)
	}
	return ScheduledJob{Index: index, RunID: runID, Definition: jobs.CloneDefinition(definition)}, nil
}

func (manager *Manager) MarkScheduledJobRunning(runID string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := &manager.state.DutyCycleLifecycle
	index := scheduledRunIndex(state.RunList, runID)
	if state.Phase != schedule.PhasePreparingRun || index < 0 || state.RunList[index].State != schedule.RunPreparing {
		return fmt.Errorf("scheduled run is not prepared")
	}
	previous := cloneLifecycle(*state)
	runIndex := manager.runIndexLocked(runID)
	if runIndex < 0 {
		return fmt.Errorf("scheduled run history is unavailable")
	}
	previousRun := cloneRun(manager.state.Runs[runIndex])
	state.RunList[index].State = schedule.RunRunning
	state.Phase = schedule.PhaseRunningJob
	manager.state.Runs[runIndex].State = jobs.StateRunning
	manager.state.Runs[runIndex].UpdatedAt = manager.now().UTC()
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycleLifecycle = previous
		manager.state.Runs[runIndex] = previousRun
		return fmt.Errorf("persist running scheduled job: %w", err)
	}
	return nil
}

func (manager *Manager) CompleteScheduledJob(response jobs.Response) (bool, error) {
	if err := response.Validate(); err != nil {
		return false, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := &manager.state.DutyCycleLifecycle
	index := scheduledRunIndex(state.RunList, response.RunID)
	if state.Phase != schedule.PhaseRunningJob || index < 0 || state.RunList[index].State != schedule.RunRunning {
		return false, fmt.Errorf("scheduled run is not running")
	}
	previousLifecycle := cloneLifecycle(*state)
	previousEnabled := manager.state.DutyCycle.Enabled
	runIndex := manager.runIndexLocked(response.RunID)
	if runIndex < 0 {
		return false, fmt.Errorf("scheduled run history is unavailable")
	}
	previousRun := cloneRun(manager.state.Runs[runIndex])
	applyRunResponse(&manager.state.Runs[runIndex], response, manager.now().UTC())
	if response.State == jobs.StateSucceeded {
		state.RunList[index].State = schedule.RunSucceeded
		if allScheduledSucceeded(state.RunList) {
			state.Phase = schedule.PhaseRunListSucceeded
		} else {
			state.Phase = schedule.PhaseExpectedBootObserved
		}
	} else {
		state.RunList[index].State = schedule.RunFailed
		manager.enterRunFailureLocked("scheduled job did not succeed")
	}
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycleLifecycle = previousLifecycle
		manager.state.DutyCycle.Enabled = previousEnabled
		manager.state.Runs[runIndex] = previousRun
		return false, fmt.Errorf("persist scheduled job result: %w", err)
	}
	return response.State == jobs.StateSucceeded, nil
}

// RecordNextSimulatedDecision persists the calculated intent only. It does not
// program a wake register or invoke a power boundary.
func (manager *Manager) RecordNextSimulatedDecision(configurationRevision uint64, occurrence uint32) error {
	if occurrence == 0 {
		return fmt.Errorf("next simulated occurrence must be positive")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.state.DutyCycle == nil || !manager.state.DutyCycle.Enabled || manager.state.DutyCycle.Revision != configurationRevision || manager.state.DutyCycleLifecycle.Phase != schedule.PhaseRunListSucceeded {
		return fmt.Errorf("successful run list is not ready for its next decision")
	}
	previous := cloneLifecycle(manager.state.DutyCycleLifecycle)
	manager.state.DutyCycleLifecycle = schedule.Lifecycle{
		Phase: schedule.PhaseSimulatedShutdownIntended, ConfigurationRevision: configurationRevision,
		IntendedOccurrence: occurrence, Simulated: true,
	}
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycleLifecycle = previous
		return fmt.Errorf("persist next simulated occurrence: %w", err)
	}
	return nil
}

func (manager *Manager) CompleteOneTimeSimulation(configurationRevision uint64) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.state.DutyCycle == nil || !manager.state.DutyCycle.Enabled || manager.state.DutyCycle.Revision != configurationRevision || manager.state.DutyCycle.Schedule.Kind != schedule.KindOneTime || manager.state.DutyCycleLifecycle.Phase != schedule.PhaseRunListSucceeded {
		return fmt.Errorf("one-time duty cycle is not ready to complete")
	}
	previousLifecycle := cloneLifecycle(manager.state.DutyCycleLifecycle)
	manager.state.DutyCycle.Enabled = false
	manager.state.DutyCycleLifecycle.Phase = schedule.PhaseCompleted
	manager.state.DutyCycleLifecycle.IntendedOccurrence = 0
	manager.state.DutyCycleLifecycle.Simulated = true
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycle.Enabled = true
		manager.state.DutyCycleLifecycle = previousLifecycle
		return fmt.Errorf("persist one-time duty-cycle completion: %w", err)
	}
	return nil
}

func (manager *Manager) FailScheduledRun(reason string) error {
	if reason == "" || len(reason) > schedule.MaxSafeReasonBytes {
		return fmt.Errorf("invalid scheduled-run failure reason")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	previousLifecycle := cloneLifecycle(manager.state.DutyCycleLifecycle)
	previousEnabled := manager.state.DutyCycle.Enabled
	manager.enterRunFailureLocked(reason)
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycleLifecycle = previousLifecycle
		manager.state.DutyCycle.Enabled = previousEnabled
		return err
	}
	return nil
}

func (manager *Manager) enterRunFailureLocked(reason string) {
	state := &manager.state.DutyCycleLifecycle
	wasSimulated := state.Simulated
	for index := range state.RunList {
		switch state.RunList[index].State {
		case schedule.RunPreparing, schedule.RunRunning:
			state.RunList[index].State = schedule.RunFailed
		case schedule.RunPending:
			state.RunList[index].State = schedule.RunNotRun
		}
	}
	manager.state.DutyCycle.Enabled = false
	state.Phase = schedule.PhaseManualSafe
	state.IntendedOccurrence = 0
	state.Reason = reason
	state.Simulated = wasSimulated
	state.WakeKind = ""
	state.ProgrammedRTCWake = 0
	state.ProgrammedWakeMinutes = 0
	if wasSimulated {
		state.WakeDisarmStatus = ""
	} else {
		state.WakeDisarmStatus = schedule.DisarmPending
	}
}

func scheduledRunIndex(entries []schedule.RunEntry, runID string) int {
	for index := range entries {
		if entries[index].RunID == runID {
			return index
		}
	}
	return -1
}

func allScheduledSucceeded(entries []schedule.RunEntry) bool {
	for _, entry := range entries {
		if entry.State != schedule.RunSucceeded {
			return false
		}
	}
	return true
}

func cloneLifecycle(value schedule.Lifecycle) schedule.Lifecycle {
	value.RunList = append([]schedule.RunEntry(nil), value.RunList...)
	return value
}
