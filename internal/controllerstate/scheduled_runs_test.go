package controllerstate

import (
	"bytes"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

func scheduledTestManager(t *testing.T) *Manager {
	t.Helper()
	manager, err := Open(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	manager.random = bytes.NewReader(make([]byte, 16))
	first, err := manager.Create(testInput("First"))
	if err != nil {
		t.Fatal(err)
	}
	manager.random = bytes.NewReader(bytes.Repeat([]byte{1}, 16))
	second, err := manager.Create(testInput("Second"))
	if err != nil {
		t.Fatal(err)
	}
	configuration := schedule.Configuration{
		Revision: 1,
		Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15},
		RunList: []schedule.RunListEntry{
			{JobID: first.ID, JobRevision: first.Revision},
			{JobID: second.ID, JobRevision: second.Revision},
		},
	}
	if _, err := manager.SaveDisabledConfiguration(testProposalID, manager.Revision(), configuration); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnableSimulation("ffeeddccbbaa99887766554433221100", manager.Revision(), 1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1_800_000_900); err != nil {
		t.Fatal(err)
	}
	if err := manager.ObserveExpectedSimulatedBoot(); err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestScheduledRunListExecutesStrictlySequentially(t *testing.T) {
	manager := scheduledTestManager(t)
	const firstRun = "11111111111111111111111111111111"
	first, err := manager.PrepareScheduledJob(firstRun)
	if err != nil {
		t.Fatal(err)
	}
	if first.Index != 0 || first.Definition.Name != "First" || manager.DutyCycleLifecycle().Phase != schedule.PhasePreparingRun {
		t.Fatalf("first = %+v, lifecycle = %+v", first, manager.DutyCycleLifecycle())
	}
	if _, err := manager.PrepareScheduledJob("22222222222222222222222222222222"); err == nil {
		t.Fatal("prepared a second job while the first was uncertain")
	}
	if err := manager.MarkScheduledJobRunning(firstRun); err != nil {
		t.Fatal(err)
	}
	exit := 0
	succeeded, err := manager.CompleteScheduledJob(jobs.Response{Version: jobs.ProtocolVersion, RunID: firstRun, State: jobs.StateSucceeded, ExitCode: &exit, QueuedUnixMilli: 1000, StartedUnixMilli: 2000, FinishedUnixMilli: 3000, StdoutTotal: 12})
	if err != nil || !succeeded {
		t.Fatalf("first completion = %v, %v", succeeded, err)
	}
	const secondRun = "22222222222222222222222222222222"
	second, err := manager.PrepareScheduledJob(secondRun)
	if err != nil {
		t.Fatal(err)
	}
	if second.Index != 1 || second.Definition.Name != "Second" {
		t.Fatalf("second = %+v", second)
	}
	if err := manager.MarkScheduledJobRunning(secondRun); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CompleteScheduledJob(jobs.Response{Version: jobs.ProtocolVersion, RunID: secondRun, State: jobs.StateSucceeded, ExitCode: &exit}); err != nil {
		t.Fatal(err)
	}
	state := manager.DutyCycleLifecycle()
	if state.Phase != schedule.PhaseRunListSucceeded || state.RunList[0].State != schedule.RunSucceeded || state.RunList[1].State != schedule.RunSucceeded {
		t.Fatalf("completed lifecycle = %+v", state)
	}
	runs := manager.Runs(10, "")
	if len(runs) != 2 || runs[1].RunID != firstRun || runs[1].Trigger != RunTriggerScheduled || runs[1].Occurrence != 1_800_000_900 || runs[1].RunListPosition != 1 || runs[1].FinishedAt.IsZero() || runs[1].StdoutTotal != 12 {
		t.Fatalf("scheduled run history = %+v", runs)
	}
}

func TestScheduledFailureStopsListAndMarksRemainingNotRun(t *testing.T) {
	manager := scheduledTestManager(t)
	const runID = "11111111111111111111111111111111"
	if _, err := manager.PrepareScheduledJob(runID); err != nil {
		t.Fatal(err)
	}
	if err := manager.MarkScheduledJobRunning(runID); err != nil {
		t.Fatal(err)
	}
	exit := 2
	succeeded, err := manager.CompleteScheduledJob(jobs.Response{Version: jobs.ProtocolVersion, RunID: runID, State: jobs.StateFailed, ExitCode: &exit})
	if err != nil || succeeded {
		t.Fatalf("completion = %v, %v", succeeded, err)
	}
	configuration, _ := manager.DutyCycleConfiguration()
	state := manager.DutyCycleLifecycle()
	if configuration.Enabled || state.Phase != schedule.PhaseManualSafe || state.RunList[0].State != schedule.RunFailed || state.RunList[1].State != schedule.RunNotRun {
		t.Fatalf("configuration = %+v, lifecycle = %+v", configuration, state)
	}
}

func TestRestartAfterPreparationNeverRerunsJob(t *testing.T) {
	manager := scheduledTestManager(t)
	const runID = "11111111111111111111111111111111"
	if _, err := manager.PrepareScheduledJob(runID); err != nil {
		t.Fatal(err)
	}
	changed, err := manager.ReconcileStartup()
	if err != nil || !changed {
		t.Fatalf("ReconcileStartup() = %v, %v", changed, err)
	}
	state := manager.DutyCycleLifecycle()
	if state.Phase != schedule.PhaseManualSafe || state.RunList[0].State != schedule.RunFailed || state.RunList[1].State != schedule.RunNotRun {
		t.Fatalf("reconciled lifecycle = %+v", state)
	}
	if _, err := manager.PrepareScheduledJob(runID); err == nil {
		t.Fatal("restart reconciliation allowed job rerun")
	}
}

func TestStartupReconciliationMakesEveryActivePhaseManualSafe(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T) *Manager
	}{
		{
			name: "shutdown intended",
			setup: func(t *testing.T) *Manager {
				manager, _, _ := activatedTestManager(t)
				return manager
			},
		},
		{
			name:  "expected boot observed",
			setup: func(t *testing.T) *Manager { return scheduledTestManager(t) },
		},
		{
			name: "preparing run",
			setup: func(t *testing.T) *Manager {
				manager := scheduledTestManager(t)
				if _, err := manager.PrepareScheduledJob("11111111111111111111111111111111"); err != nil {
					t.Fatal(err)
				}
				return manager
			},
		},
		{
			name: "running job",
			setup: func(t *testing.T) *Manager {
				manager := scheduledTestManager(t)
				const runID = "11111111111111111111111111111111"
				if _, err := manager.PrepareScheduledJob(runID); err != nil {
					t.Fatal(err)
				}
				if err := manager.MarkScheduledJobRunning(runID); err != nil {
					t.Fatal(err)
				}
				return manager
			},
		},
		{
			name: "run list succeeded",
			setup: func(t *testing.T) *Manager {
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
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := test.setup(t)
			if changed, err := manager.ReconcileStartup(); err != nil || !changed {
				t.Fatalf("ReconcileStartup() = %v, %v", changed, err)
			}
			configuration, _ := manager.DutyCycleConfiguration()
			state := manager.DutyCycleLifecycle()
			if configuration.Enabled || state.Phase != schedule.PhaseManualSafe || state.Reason == "" {
				t.Fatalf("configuration = %+v, lifecycle = %+v", configuration, state)
			}
			for _, entry := range state.RunList {
				if entry.State == schedule.RunPending || entry.State == schedule.RunPreparing || entry.State == schedule.RunRunning {
					t.Fatalf("reconciliation left runnable entry: %+v", entry)
				}
			}
			if changed, err := manager.ReconcileStartup(); err != nil || changed {
				t.Fatalf("second ReconcileStartup() = %v, %v", changed, err)
			}
		})
	}
}
