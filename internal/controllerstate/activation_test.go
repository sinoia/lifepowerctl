package controllerstate

import (
	"bytes"
	"errors"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

func TestSimulatedActivationPersistsIntentWithoutPowerAction(t *testing.T) {
	directory := t.TempDir()
	manager, err := Open(directory, limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	manager.random = bytes.NewReader(make([]byte, 16))
	job, err := manager.Create(testInput("Camera"))
	if err != nil {
		t.Fatal(err)
	}
	configuration := schedule.Configuration{Revision: 1, Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15}, RunList: []schedule.RunListEntry{{JobID: job.ID, JobRevision: job.Revision}}}
	if _, err := manager.SaveDisabledConfiguration(testProposalID, manager.Revision(), configuration); err != nil {
		t.Fatal(err)
	}
	const enableID = "ffeeddccbbaa99887766554433221100"
	result, err := manager.EnableSimulation(enableID, manager.Revision(), 1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1_800_000_900)
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfigurationRevision != 1 {
		t.Fatalf("result = %+v", result)
	}
	stored, ok := manager.DutyCycleConfiguration()
	state := manager.DutyCycleLifecycle()
	if !ok || !stored.Enabled || state.Phase != schedule.PhaseSimulatedShutdownIntended || !state.Simulated || state.IntendedOccurrence != 1_800_000_900 {
		t.Fatalf("configuration = %+v, lifecycle = %+v", stored, state)
	}
	repeated, err := manager.EnableSimulation(enableID, result.StateRevision-1, 1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1_800_000_900)
	if err != nil || !repeated.Existing {
		t.Fatalf("repeated result = %+v, %v", repeated, err)
	}
	if _, err := manager.SimulationResult(enableID, result.StateRevision-1, 1, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 1_800_000_900); !errors.Is(err, ErrProposalIDConflict) {
		t.Fatalf("altered simulation lookup error = %v", err)
	}
	reopened, err := Open(directory, limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.DutyCycleLifecycle().Phase != schedule.PhaseSimulatedShutdownIntended {
		t.Fatalf("reopened lifecycle = %+v", reopened.DutyCycleLifecycle())
	}
}

func TestRealActivationPersistsCalculatedWakeWithoutClaimingVerification(t *testing.T) {
	manager, _, directory := activatedTestManager(t)
	if _, err := manager.Disable("11111111111111111111111111111111", manager.Revision(), 1); err != nil {
		t.Fatal(err)
	}
	const proposalID = "22222222222222222222222222222222"
	const review = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const occurrence = uint32(1_800_001_800)
	result, err := manager.EnableReal(proposalID, manager.Revision(), 1, review, occurrence, occurrence)
	if err != nil {
		t.Fatal(err)
	}
	state := manager.DutyCycleLifecycle()
	if state.Phase != schedule.PhaseNextWakeCalculated || state.Simulated || state.IntendedOccurrence != occurrence || state.WakeKind != "" || state.ProgrammedRTCWake != 0 {
		t.Fatalf("real activation lifecycle = %+v", state)
	}
	repeated, err := manager.EnableReal(proposalID, result.StateRevision-1, 1, review, occurrence, occurrence)
	if err != nil || !repeated.Existing {
		t.Fatalf("repeated activation = %+v, %v", repeated, err)
	}
	if _, err := manager.SimulationResult(proposalID, result.StateRevision-1, 1, review, occurrence); !errors.Is(err, ErrProposalIDConflict) {
		t.Fatalf("cross-mode proposal lookup error = %v", err)
	}
	reopened, err := Open(directory, limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	if lifecycle := reopened.DutyCycleLifecycle(); lifecycle.Phase != schedule.PhaseNextWakeCalculated || lifecycle.ProgrammedRTCWake != 0 {
		t.Fatalf("reopened lifecycle = %+v", lifecycle)
	}
}

func TestSimulatedActivationRequiresEnabledCurrentRunListJobs(t *testing.T) {
	manager, err := Open(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	manager.random = bytes.NewReader(make([]byte, 16))
	input := testInput("Disabled")
	input.Enabled = false
	job, err := manager.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	configuration := schedule.Configuration{Revision: 1, Schedule: schedule.Specification{Kind: schedule.KindDaily, Timezone: "UTC", LocalMinute: 60}, RunList: []schedule.RunListEntry{{JobID: job.ID, JobRevision: job.Revision}}}
	if _, err := manager.SaveDisabledConfiguration(testProposalID, manager.Revision(), configuration); err != nil {
		t.Fatal(err)
	}
	_, err = manager.EnableSimulation("ffeeddccbbaa99887766554433221100", manager.Revision(), 1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1_800_000_900)
	if !errors.Is(err, ErrActivation) {
		t.Fatalf("activation error = %v", err)
	}
	if manager.DutyCycleLifecycle().Phase != schedule.PhaseInactive {
		t.Fatalf("failed activation changed lifecycle = %+v", manager.DutyCycleLifecycle())
	}
}

func TestExplicitDisableIsRequiredBeforeEditing(t *testing.T) {
	manager, job, _ := activatedTestManager(t)
	configured, _ := manager.DutyCycleConfiguration()
	configured.Enabled = false
	configured.Revision++
	if _, err := manager.SaveDisabledConfiguration("11111111111111111111111111111111", manager.Revision(), configured); !errors.Is(err, ErrDutyCycleEnabled) {
		t.Fatalf("edit while enabled error = %v", err)
	}
	result, err := manager.Disable("22222222222222222222222222222222", manager.Revision(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if manager.DutyCycleLifecycle().Phase != schedule.PhaseInactive {
		t.Fatalf("disabled lifecycle = %+v", manager.DutyCycleLifecycle())
	}
	repeated, err := manager.Disable("22222222222222222222222222222222", result.StateRevision-1, 1)
	if err != nil || !repeated.Existing {
		t.Fatalf("repeated disable = %+v, %v", repeated, err)
	}
	if _, err := manager.Update(job.ID, job.Revision, testInput("Still referenced")); !errors.Is(err, ErrJobInRunList) {
		t.Fatalf("referenced job update error = %v", err)
	}
}

func TestStartupReconciliationEntersDurableManualSafe(t *testing.T) {
	manager, _, directory := activatedTestManager(t)
	changed, err := manager.ReconcileStartup()
	if err != nil || !changed {
		t.Fatalf("ReconcileStartup() = %v, %v", changed, err)
	}
	configuration, _ := manager.DutyCycleConfiguration()
	state := manager.DutyCycleLifecycle()
	if configuration.Enabled || state.Phase != schedule.PhaseManualSafe || !state.Simulated || state.Reason == "" {
		t.Fatalf("configuration = %+v, lifecycle = %+v", configuration, state)
	}
	reopened, err := Open(directory, limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.DutyCycleLifecycle().Phase != schedule.PhaseManualSafe {
		t.Fatalf("reopened lifecycle = %+v", reopened.DutyCycleLifecycle())
	}
	changed, err = manager.ReconcileStartup()
	if err != nil || changed {
		t.Fatalf("second ReconcileStartup() = %v, %v", changed, err)
	}
}

func activatedTestManager(t *testing.T) (*Manager, jobs.Definition, string) {
	t.Helper()
	directory := t.TempDir()
	manager, err := Open(directory, limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	manager.random = bytes.NewReader(make([]byte, 16))
	job, err := manager.Create(testInput("Camera"))
	if err != nil {
		t.Fatal(err)
	}
	configuration := schedule.Configuration{Revision: 1, Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15}, RunList: []schedule.RunListEntry{{JobID: job.ID, JobRevision: job.Revision}}}
	if _, err := manager.SaveDisabledConfiguration(testProposalID, manager.Revision(), configuration); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnableSimulation("ffeeddccbbaa99887766554433221100", manager.Revision(), 1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1_800_000_900); err != nil {
		t.Fatal(err)
	}
	return manager, job, directory
}
