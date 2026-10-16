package controllerstate

import (
	"fmt"

	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

func (manager *Manager) DutyCycleArmedIntent() schedule.ArmedIntent {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := manager.state.DutyCycleLifecycle
	intent := schedule.ArmedIntent{
		Phase: stateArmedPhase(state.Phase), WakeKind: state.WakeKind,
		ExpectedRTC: state.IntendedOccurrence, ProgrammedRTCWake: state.ProgrammedRTCWake,
		ProgrammedWakeMins: state.ProgrammedWakeMinutes,
	}
	intent.Enabled = manager.state.DutyCycle != nil && manager.state.DutyCycle.Enabled
	return intent
}

func stateArmedPhase(phase schedule.Phase) schedule.ArmedPhase {
	switch phase {
	case schedule.PhaseWakeVerified:
		return schedule.ArmedWakeVerified
	case schedule.PhaseShutdownRequested:
		return schedule.ArmedShutdownRequested
	case schedule.PhaseExpectedBootObserved:
		return schedule.ArmedExpectedObserved
	default:
		return ""
	}
}

func (manager *Manager) ObserveExpectedBoot() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := &manager.state.DutyCycleLifecycle
	if manager.state.DutyCycle == nil || !manager.state.DutyCycle.Enabled || state.Phase != schedule.PhaseShutdownRequested || state.WakeKind != schedule.WakeAbsoluteRTC || state.ProgrammedRTCWake != state.IntendedOccurrence || state.ProgrammedWakeMinutes != 0 {
		return fmt.Errorf("verified shutdown intent is not ready to consume")
	}
	previous := cloneLifecycle(*state)
	entries := make([]schedule.RunEntry, len(manager.state.DutyCycle.RunList))
	for index, entry := range manager.state.DutyCycle.RunList {
		entries[index] = schedule.RunEntry{JobID: entry.JobID, JobRevision: entry.JobRevision, State: schedule.RunPending}
	}
	*state = schedule.Lifecycle{
		Phase: schedule.PhaseExpectedBootObserved, ConfigurationRevision: manager.state.DutyCycle.Revision,
		IntendedOccurrence: previous.IntendedOccurrence, Simulated: false, RunList: entries,
	}
	if err := manager.saveLocked(); err != nil {
		*state = previous
		return fmt.Errorf("persist expected boot observation: %w", err)
	}
	return nil
}

func (manager *Manager) EnterBootManualSafe(reason string) error {
	if reason == "" || len(reason) > schedule.MaxSafeReasonBytes {
		return fmt.Errorf("invalid boot classification reason")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.state.DutyCycle == nil || !manager.state.DutyCycle.Enabled {
		return fmt.Errorf("duty cycle is not armed")
	}
	previous := cloneSnapshotConfiguration(manager.state)
	manager.enterRunFailureLocked(reason)
	manager.state.DutyCycleLifecycle.ConfigurationRevision = manager.state.DutyCycle.Revision
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycle = previous.DutyCycle
		manager.state.DutyCycleLifecycle = previous.DutyCycleLifecycle
		return fmt.Errorf("persist boot manual-safe state: %w", err)
	}
	return nil
}

func (manager *Manager) RecordNextWakeCalculated(configurationRevision uint64, occurrence uint32) error {
	if occurrence == 0 {
		return fmt.Errorf("next wake occurrence must be positive")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := &manager.state.DutyCycleLifecycle
	if manager.state.DutyCycle == nil || !manager.state.DutyCycle.Enabled || manager.state.DutyCycle.Revision != configurationRevision || state.Phase != schedule.PhaseRunListSucceeded {
		return fmt.Errorf("successful run list is not ready to calculate a wake")
	}
	previous := cloneLifecycle(*state)
	state.Phase = schedule.PhaseNextWakeCalculated
	state.IntendedOccurrence = occurrence
	state.Simulated = false
	state.WakeKind = ""
	state.ProgrammedRTCWake = 0
	state.ProgrammedWakeMinutes = 0
	state.WakeDisarmStatus = ""
	if err := manager.saveLocked(); err != nil {
		*state = previous
		return fmt.Errorf("persist calculated wake: %w", err)
	}
	return nil
}

func (manager *Manager) BeginWakeProgramming(configurationRevision uint64) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := &manager.state.DutyCycleLifecycle
	if manager.state.DutyCycle == nil || !manager.state.DutyCycle.Enabled || manager.state.DutyCycle.Revision != configurationRevision || state.Phase != schedule.PhaseNextWakeCalculated {
		return fmt.Errorf("calculated wake is not ready for programming")
	}
	previous := cloneLifecycle(*state)
	state.Phase = schedule.PhaseWakeProgramming
	if err := manager.saveLocked(); err != nil {
		*state = previous
		return fmt.Errorf("persist wake programming intent: %w", err)
	}
	return nil
}

func (manager *Manager) RecordWakeVerified(configurationRevision uint64, result board.WakeProgrammingResult) error {
	if !result.Verified() {
		return fmt.Errorf("wake hardware evidence is not verified")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := &manager.state.DutyCycleLifecycle
	if manager.state.DutyCycle == nil || !manager.state.DutyCycle.Enabled || manager.state.DutyCycle.Revision != configurationRevision || state.Phase != schedule.PhaseWakeProgramming || result.IntendedRTC != state.IntendedOccurrence || result.AbsoluteReadBack != state.IntendedOccurrence || result.RelativeReadBack != 0 {
		return fmt.Errorf("wake verification does not match calculated intent")
	}
	previous := cloneLifecycle(*state)
	state.Phase = schedule.PhaseWakeVerified
	state.WakeKind = schedule.WakeAbsoluteRTC
	state.ProgrammedRTCWake = result.AbsoluteReadBack
	state.ProgrammedWakeMinutes = result.RelativeReadBack
	if err := manager.saveLocked(); err != nil {
		*state = previous
		return fmt.Errorf("persist verified wake: %w", err)
	}
	return nil
}

func (manager *Manager) RecordShutdownRequested(configurationRevision uint64) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := &manager.state.DutyCycleLifecycle
	if manager.state.DutyCycle == nil || !manager.state.DutyCycle.Enabled || manager.state.DutyCycle.Revision != configurationRevision || state.Phase != schedule.PhaseWakeVerified {
		return fmt.Errorf("verified wake is not ready for shutdown")
	}
	previous := cloneLifecycle(*state)
	state.Phase = schedule.PhaseShutdownRequested
	if err := manager.saveLocked(); err != nil {
		*state = previous
		return fmt.Errorf("persist shutdown request: %w", err)
	}
	return nil
}

func (manager *Manager) RecordWakeDisarm(status schedule.DisarmStatus) error {
	if status != schedule.DisarmVerified && status != schedule.DisarmUncertain {
		return fmt.Errorf("invalid wake disarm result")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := &manager.state.DutyCycleLifecycle
	if state.Phase != schedule.PhaseManualSafe || state.Simulated || state.WakeDisarmStatus != schedule.DisarmPending {
		return fmt.Errorf("manual-safe state is not awaiting wake disarm evidence")
	}
	previous := cloneLifecycle(*state)
	state.WakeDisarmStatus = status
	if err := manager.saveLocked(); err != nil {
		*state = previous
		return fmt.Errorf("persist wake disarm result: %w", err)
	}
	return nil
}

func (manager *Manager) CompleteOneTimeDutyCycle(configurationRevision uint64) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := &manager.state.DutyCycleLifecycle
	if manager.state.DutyCycle == nil || !manager.state.DutyCycle.Enabled || manager.state.DutyCycle.Revision != configurationRevision || manager.state.DutyCycle.Schedule.Kind != schedule.KindOneTime || state.Phase != schedule.PhaseRunListSucceeded || state.Simulated {
		return fmt.Errorf("one-time duty cycle is not ready to complete")
	}
	previous := cloneSnapshotConfiguration(manager.state)
	manager.state.DutyCycle.Enabled = false
	state.Phase = schedule.PhaseCompleted
	state.IntendedOccurrence = 0
	state.WakeKind = ""
	state.ProgrammedRTCWake = 0
	state.ProgrammedWakeMinutes = 0
	state.WakeDisarmStatus = ""
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycle = previous.DutyCycle
		manager.state.DutyCycleLifecycle = previous.DutyCycleLifecycle
		return fmt.Errorf("persist one-time duty-cycle completion: %w", err)
	}
	return nil
}
