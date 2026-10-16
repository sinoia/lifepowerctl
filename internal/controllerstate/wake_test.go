package controllerstate

import (
	"testing"

	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

func successfulScheduledRun(t *testing.T) *Manager {
	t.Helper()
	manager := scheduledTestManager(t)
	for _, runID := range []string{"11111111111111111111111111111111", "22222222222222222222222222222222"} {
		if _, err := manager.PrepareScheduledJob(runID); err != nil {
			t.Fatal(err)
		}
		if err := manager.MarkScheduledJobRunning(runID); err != nil {
			t.Fatal(err)
		}
		exit := 0
		if _, err := manager.CompleteScheduledJob(jobs.Response{Version: jobs.ProtocolVersion, RunID: runID, State: jobs.StateSucceeded, ExitCode: &exit}); err != nil {
			t.Fatal(err)
		}
	}
	return manager
}

func verifiedWakeResult(occurrence uint32) board.WakeProgrammingResult {
	return board.WakeProgrammingResult{
		IntendedRTC: occurrence, RelativeWritten: true, RelativeVerified: true,
		AbsoluteWritten: true, AbsoluteReadBack: occurrence, AbsoluteVerified: true,
	}
}

func TestWakeLifecyclePersistsEvidenceInStrictOrder(t *testing.T) {
	manager := successfulScheduledRun(t)
	const occurrence = uint32(1_800_001_800)
	if err := manager.RecordNextWakeCalculated(1, occurrence); err != nil {
		t.Fatal(err)
	}
	state := manager.DutyCycleLifecycle()
	if state.Phase != schedule.PhaseNextWakeCalculated || state.Simulated || state.IntendedOccurrence != occurrence || state.WakeKind != "" {
		t.Fatalf("calculated lifecycle = %+v", state)
	}
	if err := manager.RecordWakeVerified(1, verifiedWakeResult(occurrence)); err == nil {
		t.Fatal("recorded verified wake before programming intent")
	}
	if err := manager.BeginWakeProgramming(1); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecordWakeVerified(1, verifiedWakeResult(occurrence)); err != nil {
		t.Fatal(err)
	}
	state = manager.DutyCycleLifecycle()
	if state.Phase != schedule.PhaseWakeVerified || state.WakeKind != schedule.WakeAbsoluteRTC || state.ProgrammedRTCWake != occurrence || state.ProgrammedWakeMinutes != 0 {
		t.Fatalf("verified lifecycle = %+v", state)
	}
	if err := manager.RecordShutdownRequested(1); err != nil {
		t.Fatal(err)
	}
	if state = manager.DutyCycleLifecycle(); state.Phase != schedule.PhaseShutdownRequested || state.ProgrammedRTCWake != occurrence {
		t.Fatalf("shutdown lifecycle = %+v", state)
	}
}

func TestExpectedBootConsumesVerifiedWakeBeforeRunCanStart(t *testing.T) {
	manager := successfulScheduledRun(t)
	const occurrence = uint32(1_800_001_800)
	if err := manager.RecordNextWakeCalculated(1, occurrence); err != nil {
		t.Fatal(err)
	}
	if err := manager.BeginWakeProgramming(1); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecordWakeVerified(1, verifiedWakeResult(occurrence)); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecordShutdownRequested(1); err != nil {
		t.Fatal(err)
	}
	intent := manager.DutyCycleArmedIntent()
	if !intent.Enabled || intent.Phase != schedule.ArmedShutdownRequested || intent.ExpectedRTC != occurrence || intent.ProgrammedRTCWake != occurrence {
		t.Fatalf("armed intent = %+v", intent)
	}
	if err := manager.ObserveExpectedBoot(); err != nil {
		t.Fatal(err)
	}
	state := manager.DutyCycleLifecycle()
	if state.Phase != schedule.PhaseExpectedBootObserved || state.Simulated || state.WakeKind != "" || state.ProgrammedRTCWake != 0 || len(state.RunList) != 2 {
		t.Fatalf("observed lifecycle = %+v", state)
	}
	if err := manager.ObserveExpectedBoot(); err == nil {
		t.Fatal("consumed the same expected boot twice")
	}
}

func TestWakeLifecycleRejectsIncompleteOrMismatchedEvidence(t *testing.T) {
	manager := successfulScheduledRun(t)
	const occurrence = uint32(1_800_001_800)
	if err := manager.RecordNextWakeCalculated(1, occurrence); err != nil {
		t.Fatal(err)
	}
	if err := manager.BeginWakeProgramming(1); err != nil {
		t.Fatal(err)
	}
	incomplete := verifiedWakeResult(occurrence)
	incomplete.AbsoluteVerified = false
	if err := manager.RecordWakeVerified(1, incomplete); err == nil {
		t.Fatal("accepted incomplete wake evidence")
	}
	mismatched := verifiedWakeResult(occurrence + 1)
	if err := manager.RecordWakeVerified(1, mismatched); err == nil {
		t.Fatal("accepted mismatched wake evidence")
	}
	if err := manager.RecordShutdownRequested(1); err == nil {
		t.Fatal("recorded shutdown without verified wake")
	}
	if phase := manager.DutyCycleLifecycle().Phase; phase != schedule.PhaseWakeProgramming {
		t.Fatalf("phase after rejected evidence = %s", phase)
	}
}

func TestRestartFromRealWakePhaseRequiresDisarmEvidence(t *testing.T) {
	manager := successfulScheduledRun(t)
	const occurrence = uint32(1_800_001_800)
	if err := manager.RecordNextWakeCalculated(1, occurrence); err != nil {
		t.Fatal(err)
	}
	if changed, err := manager.ReconcileStartup(); err != nil || !changed {
		t.Fatalf("ReconcileStartup() = %v, %v", changed, err)
	}
	state := manager.DutyCycleLifecycle()
	if state.Phase != schedule.PhaseManualSafe || state.Simulated || state.WakeDisarmStatus != schedule.DisarmPending || state.IntendedOccurrence != 0 {
		t.Fatalf("manual-safe lifecycle = %+v", state)
	}
	if err := manager.RecordWakeDisarm(schedule.DisarmVerified); err != nil {
		t.Fatal(err)
	}
	if state = manager.DutyCycleLifecycle(); state.WakeDisarmStatus != schedule.DisarmVerified {
		t.Fatalf("disarmed lifecycle = %+v", state)
	}
}

func TestRestartFromEveryRealWakePhaseEntersManualSafe(t *testing.T) {
	tests := []struct {
		name    string
		advance func(*testing.T, *Manager, uint32)
	}{
		{name: "calculated"},
		{name: "programming", advance: func(t *testing.T, manager *Manager, _ uint32) {
			if err := manager.BeginWakeProgramming(1); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "verified", advance: func(t *testing.T, manager *Manager, occurrence uint32) {
			if err := manager.BeginWakeProgramming(1); err != nil {
				t.Fatal(err)
			}
			if err := manager.RecordWakeVerified(1, verifiedWakeResult(occurrence)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "shutdown requested", advance: func(t *testing.T, manager *Manager, occurrence uint32) {
			if err := manager.BeginWakeProgramming(1); err != nil {
				t.Fatal(err)
			}
			if err := manager.RecordWakeVerified(1, verifiedWakeResult(occurrence)); err != nil {
				t.Fatal(err)
			}
			if err := manager.RecordShutdownRequested(1); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := successfulScheduledRun(t)
			const occurrence = uint32(1_800_001_800)
			if err := manager.RecordNextWakeCalculated(1, occurrence); err != nil {
				t.Fatal(err)
			}
			if test.advance != nil {
				test.advance(t, manager, occurrence)
			}
			if changed, err := manager.ReconcileStartup(); err != nil || !changed {
				t.Fatalf("ReconcileStartup() = %v, %v", changed, err)
			}
			state := manager.DutyCycleLifecycle()
			if state.Phase != schedule.PhaseManualSafe || state.WakeDisarmStatus != schedule.DisarmPending || state.ProgrammedRTCWake != 0 || state.WakeKind != "" {
				t.Fatalf("manual-safe lifecycle = %+v", state)
			}
		})
	}
}

func TestPhaseSixSnapshotWithoutWakeFieldsRemainsValid(t *testing.T) {
	value, err := decodeSnapshot([]byte(`{"jobs":[],"duty_cycle_lifecycle":{"phase":"inactive","simulated":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSnapshot(value, limits.Default().Jobs); err != nil {
		t.Fatalf("old snapshot validation = %v", err)
	}
}
