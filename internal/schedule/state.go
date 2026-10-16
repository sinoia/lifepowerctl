package schedule

import "fmt"

type Phase string

const (
	PhaseInactive                  Phase = "inactive"
	PhaseSimulatedShutdownIntended Phase = "simulated-shutdown-intended"
	PhaseManualSafe                Phase = "manual-safe"
	PhaseExpectedBootObserved      Phase = "expected-boot-observed"
	PhasePreparingRun              Phase = "preparing-run"
	PhaseRunningJob                Phase = "running-job"
	PhaseRunListSucceeded          Phase = "run-list-succeeded"
	PhaseNextWakeCalculated        Phase = "next-wake-calculated"
	PhaseWakeProgramming           Phase = "wake-programming"
	PhaseWakeVerified              Phase = "wake-verified"
	PhaseShutdownRequested         Phase = "shutdown-requested"
	PhaseCompleted                 Phase = "completed"
)

const MaxSafeReasonBytes = 512

type Lifecycle struct {
	Phase                 Phase        `json:"phase"`
	ConfigurationRevision uint64       `json:"configuration_revision,omitempty"`
	IntendedOccurrence    uint32       `json:"intended_occurrence,omitempty"`
	Simulated             bool         `json:"simulated"`
	Reason                string       `json:"reason,omitempty"`
	RunList               []RunEntry   `json:"run_list,omitempty"`
	WakeKind              WakeKind     `json:"wake_kind,omitempty"`
	ProgrammedRTCWake     uint32       `json:"programmed_rtc_wake,omitempty"`
	ProgrammedWakeMinutes uint16       `json:"programmed_wake_minutes,omitempty"`
	WakeDisarmStatus      DisarmStatus `json:"wake_disarm_status,omitempty"`
}

type DisarmStatus string

const (
	DisarmPending   DisarmStatus = "pending"
	DisarmVerified  DisarmStatus = "verified"
	DisarmUncertain DisarmStatus = "uncertain"
)

type RunEntryState string

const (
	RunPending   RunEntryState = "pending"
	RunPreparing RunEntryState = "preparing"
	RunRunning   RunEntryState = "running"
	RunSucceeded RunEntryState = "succeeded"
	RunFailed    RunEntryState = "failed"
	RunNotRun    RunEntryState = "not-run"
)

type RunEntry struct {
	JobID       string        `json:"job_id"`
	JobRevision uint64        `json:"job_revision"`
	RunID       string        `json:"run_id,omitempty"`
	State       RunEntryState `json:"state"`
}

func (state Lifecycle) Validate(configuration *Configuration) error {
	switch state.Phase {
	case "", PhaseInactive:
		if state.ConfigurationRevision != 0 || state.IntendedOccurrence != 0 || state.Simulated || state.Reason != "" || len(state.RunList) != 0 || state.hasWakeEvidence() {
			return fmt.Errorf("inactive lifecycle contains active evidence")
		}
	case PhaseSimulatedShutdownIntended:
		if configuration == nil || !configuration.Enabled || state.ConfigurationRevision != configuration.Revision || state.IntendedOccurrence == 0 || !state.Simulated || state.Reason != "" || len(state.RunList) != 0 || state.hasWakeEvidence() {
			return fmt.Errorf("simulated lifecycle is inconsistent with enabled configuration")
		}
	case PhaseManualSafe:
		validDisarm := state.Simulated && state.WakeDisarmStatus == "" || !state.Simulated && (state.WakeDisarmStatus == DisarmPending || state.WakeDisarmStatus == DisarmVerified || state.WakeDisarmStatus == DisarmUncertain)
		if configuration == nil || configuration.Enabled || state.ConfigurationRevision != configuration.Revision || state.IntendedOccurrence != 0 || state.Reason == "" || len(state.Reason) > MaxSafeReasonBytes || !validRunEntries(state.RunList, false) || !validDisarm || state.WakeKind != "" || state.ProgrammedRTCWake != 0 || state.ProgrammedWakeMinutes != 0 {
			return fmt.Errorf("manual-safe lifecycle is inconsistent with disabled configuration")
		}
	case PhaseCompleted:
		if configuration == nil || configuration.Enabled || state.ConfigurationRevision != configuration.Revision || state.IntendedOccurrence != 0 || state.Reason != "" || !allEntriesSucceeded(state.RunList) || state.hasWakeEvidence() {
			return fmt.Errorf("completed lifecycle is inconsistent with disabled configuration")
		}
	case PhaseExpectedBootObserved, PhasePreparingRun, PhaseRunningJob, PhaseRunListSucceeded:
		if configuration == nil || !configuration.Enabled || state.ConfigurationRevision != configuration.Revision || state.IntendedOccurrence == 0 || state.Reason != "" || !validRunEntries(state.RunList, true) || state.hasWakeEvidence() {
			return fmt.Errorf("automatic run lifecycle is inconsistent with enabled configuration")
		}
	case PhaseNextWakeCalculated, PhaseWakeProgramming:
		if configuration == nil || !configuration.Enabled || state.ConfigurationRevision != configuration.Revision || state.IntendedOccurrence == 0 || state.Simulated || state.Reason != "" || !validRunEntries(state.RunList, false) || state.hasWakeEvidence() {
			return fmt.Errorf("wake preparation lifecycle is inconsistent with enabled configuration")
		}
	case PhaseWakeVerified, PhaseShutdownRequested:
		if configuration == nil || !configuration.Enabled || state.ConfigurationRevision != configuration.Revision || state.IntendedOccurrence == 0 || state.Simulated || state.Reason != "" || !validRunEntries(state.RunList, false) || state.WakeKind != WakeAbsoluteRTC || state.ProgrammedRTCWake != state.IntendedOccurrence || state.ProgrammedWakeMinutes != 0 || state.WakeDisarmStatus != "" {
			return fmt.Errorf("verified wake lifecycle is inconsistent with enabled configuration")
		}
	default:
		return fmt.Errorf("unknown duty-cycle lifecycle phase %q", state.Phase)
	}
	return nil
}

func (state Lifecycle) hasWakeEvidence() bool {
	return state.WakeKind != "" || state.ProgrammedRTCWake != 0 || state.ProgrammedWakeMinutes != 0 || state.WakeDisarmStatus != ""
}

func allEntriesSucceeded(entries []RunEntry) bool {
	if len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		if entry.State != RunSucceeded || entry.RunID == "" {
			return false
		}
	}
	return true
}

func validRunEntries(entries []RunEntry, requireEntries bool) bool {
	if requireEntries && len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		if entry.JobID == "" || entry.JobRevision == 0 {
			return false
		}
		switch entry.State {
		case RunPending, RunNotRun:
			if entry.RunID != "" {
				return false
			}
		case RunPreparing, RunRunning, RunSucceeded, RunFailed:
			if len(entry.RunID) != 32 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
