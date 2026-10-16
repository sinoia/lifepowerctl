package dutycycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/controllerstate"
	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type scriptedRunner struct {
	requests  []jobs.Request
	responses []jobs.Response
	errors    []error
}

func (runner *scriptedRunner) Do(_ context.Context, request jobs.Request) (jobs.Response, error) {
	runner.requests = append(runner.requests, request)
	index := len(runner.requests) - 1
	if index < len(runner.errors) && runner.errors[index] != nil {
		return jobs.Response{}, runner.errors[index]
	}
	return runner.responses[index], nil
}

func executorState(t *testing.T) *controllerstate.Manager {
	return executorStateWithSchedule(t, schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15})
}

func executorStateWithSchedule(t *testing.T, specification schedule.Specification) *controllerstate.Manager {
	t.Helper()
	manager, err := controllerstate.Open(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Create(testDefinition("First"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(testDefinition("Second"))
	if err != nil {
		t.Fatal(err)
	}
	configuration := schedule.Configuration{Revision: 1, Schedule: specification, RunList: []schedule.RunListEntry{{JobID: first.ID, JobRevision: 1}, {JobID: second.ID, JobRevision: 1}}}
	if _, err := manager.SaveDisabledConfiguration("00112233445566778899aabbccddeeff", manager.Revision(), configuration); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnableSimulation("ffeeddccbbaa99887766554433221100", manager.Revision(), 1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1_800_000_900); err != nil {
		t.Fatal(err)
	}
	return manager
}

func testDefinition(name string) jobs.DefinitionInput {
	return jobs.DefinitionInput{Name: name, Enabled: true, Mode: jobs.ModeDirect, Executable: "/bin/true", WorkingDirectory: "/tmp", TimeoutSeconds: 30, StdoutBytes: 1024, StderrBytes: 1024}
}

func TestExecutorRunsJobsSequentiallyToCompletion(t *testing.T) {
	state := executorState(t)
	exit := 0
	runner := &scriptedRunner{responses: []jobs.Response{
		{Version: jobs.ProtocolVersion, RunID: "11111111111111111111111111111111", State: jobs.StateRunning},
		{Version: jobs.ProtocolVersion, RunID: "11111111111111111111111111111111", State: jobs.StateSucceeded, ExitCode: &exit},
		{Version: jobs.ProtocolVersion, RunID: "22222222222222222222222222222222", State: jobs.StateSucceeded, ExitCode: &exit},
	}}
	executor, err := NewExecutor(state, runner, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ids := bytes.NewBufferString("1111111111111111111111111111111122222222222222222222222222222222")
	executor.newRunID = func() (string, error) {
		value := make([]byte, 32)
		_, err := ids.Read(value)
		return string(value), err
	}
	executor.wait = func(context.Context, time.Duration) error { return nil }
	executor.now = func() time.Time { return time.Date(2026, time.October, 11, 10, 0, 0, 0, time.UTC) }
	if err := executor.RunExpectedSimulatedBoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.requests) != 3 || runner.requests[0].Action != jobs.ActionStart || runner.requests[1].Action != jobs.ActionStatus || runner.requests[2].Action != jobs.ActionStart {
		t.Fatalf("runner requests = %+v", runner.requests)
	}
	if state.DutyCycleLifecycle().Phase != schedule.PhaseSimulatedShutdownIntended || state.DutyCycleLifecycle().IntendedOccurrence != uint32(time.Date(2026, time.October, 11, 10, 15, 0, 0, time.UTC).Unix()) {
		t.Fatalf("lifecycle = %+v", state.DutyCycleLifecycle())
	}
}

func TestExecutorRealBootStopsAtSuccessfulRunList(t *testing.T) {
	state := executorState(t)
	if err := state.ObserveExpectedSimulatedBoot(); err != nil {
		t.Fatal(err)
	}
	for index, runID := range []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"} {
		if _, err := state.PrepareScheduledJob(runID); err != nil {
			t.Fatal(err)
		}
		if err := state.MarkScheduledJobRunning(runID); err != nil {
			t.Fatal(err)
		}
		exit := 0
		if _, err := state.CompleteScheduledJob(jobs.Response{Version: jobs.ProtocolVersion, RunID: runID, State: jobs.StateSucceeded, ExitCode: &exit}); err != nil {
			t.Fatalf("initial completion %d: %v", index, err)
		}
	}
	const occurrence = uint32(1_800_001_800)
	if err := state.RecordNextWakeCalculated(1, occurrence); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginWakeProgramming(1); err != nil {
		t.Fatal(err)
	}
	evidence := board.WakeProgrammingResult{IntendedRTC: occurrence, RelativeWritten: true, RelativeVerified: true, AbsoluteWritten: true, AbsoluteReadBack: occurrence, AbsoluteVerified: true}
	if err := state.RecordWakeVerified(1, evidence); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordShutdownRequested(1); err != nil {
		t.Fatal(err)
	}
	if err := state.ObserveExpectedBoot(); err != nil {
		t.Fatal(err)
	}
	exit := 0
	runner := &scriptedRunner{responses: []jobs.Response{
		{Version: jobs.ProtocolVersion, RunID: "11111111111111111111111111111111", State: jobs.StateSucceeded, ExitCode: &exit},
		{Version: jobs.ProtocolVersion, RunID: "22222222222222222222222222222222", State: jobs.StateSucceeded, ExitCode: &exit},
	}}
	executor, err := NewExecutor(state, runner, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := activity.Open(t.TempDir(), activity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	executor.SetAudit(activity.NewAudit(journal))
	ids := bytes.NewBufferString("1111111111111111111111111111111122222222222222222222222222222222")
	executor.newRunID = func() (string, error) {
		value := make([]byte, 32)
		_, err := ids.Read(value)
		return string(value), err
	}
	if err := executor.RunObserved(context.Background()); err != nil {
		t.Fatal(err)
	}
	lifecycle := state.DutyCycleLifecycle()
	if lifecycle.Phase != schedule.PhaseRunListSucceeded || lifecycle.Simulated {
		t.Fatalf("real lifecycle = %+v", lifecycle)
	}
	events, err := journal.Events()
	if err != nil || len(events) != 6 {
		t.Fatalf("real transition events = %+v, %v", events, err)
	}
	for _, event := range events {
		var details struct {
			Simulated bool `json:"simulated"`
		}
		if err := json.Unmarshal(event.Details, &details); err != nil || details.Simulated {
			t.Fatalf("real transition details = %s, %v", event.Details, err)
		}
	}
}

func TestExecutorObservesExpectedBootExactlyOnce(t *testing.T) {
	state := executorState(t)
	runner := &scriptedRunner{}
	executor, err := NewExecutor(state, runner, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.ObserveExpectedSimulatedBoot(); err != nil {
		t.Fatal(err)
	}
	if lifecycle := state.DutyCycleLifecycle(); lifecycle.Phase != schedule.PhaseExpectedBootObserved {
		t.Fatalf("lifecycle = %+v", lifecycle)
	}
	if err := executor.ObserveExpectedSimulatedBoot(); err == nil {
		t.Fatal("observed the same simulated occurrence twice")
	}
}

func TestExecutorStartUncertaintyStopsWithoutRetry(t *testing.T) {
	state := executorState(t)
	runner := &scriptedRunner{responses: []jobs.Response{{}}, errors: []error{errors.New("connection lost")}}
	executor, err := NewExecutor(state, runner, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	executor.newRunID = func() (string, error) { return "11111111111111111111111111111111", nil }
	if err := executor.RunExpectedSimulatedBoot(context.Background()); err == nil {
		t.Fatal("uncertain start succeeded")
	}
	stateValue := state.DutyCycleLifecycle()
	if len(runner.requests) != 1 || stateValue.Phase != schedule.PhaseManualSafe || stateValue.RunList[0].State != schedule.RunFailed || stateValue.RunList[1].State != schedule.RunNotRun {
		t.Fatalf("requests = %+v, lifecycle = %+v", runner.requests, stateValue)
	}
}

func TestExecutorOneTimeCompletionLeavesAutomationDisabled(t *testing.T) {
	state := executorStateWithSchedule(t, schedule.Specification{Kind: schedule.KindOneTime, Timezone: "UTC", OneTimeDate: "2026-10-11", LocalMinute: 10 * 60})
	exit := 0
	runner := &scriptedRunner{responses: []jobs.Response{
		{Version: jobs.ProtocolVersion, RunID: "11111111111111111111111111111111", State: jobs.StateSucceeded, ExitCode: &exit},
		{Version: jobs.ProtocolVersion, RunID: "22222222222222222222222222222222", State: jobs.StateSucceeded, ExitCode: &exit},
	}}
	executor, err := NewExecutor(state, runner, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ids := bytes.NewBufferString("1111111111111111111111111111111122222222222222222222222222222222")
	executor.newRunID = func() (string, error) {
		value := make([]byte, 32)
		_, err := ids.Read(value)
		return string(value), err
	}
	if err := executor.RunExpectedSimulatedBoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	configuration, _ := state.DutyCycleConfiguration()
	if configuration.Enabled || state.DutyCycleLifecycle().Phase != schedule.PhaseCompleted || state.DutyCycleLifecycle().IntendedOccurrence != 0 {
		t.Fatalf("configuration = %+v, lifecycle = %+v", configuration, state.DutyCycleLifecycle())
	}
}

func TestExecutorPostRunCalculationFailureEntersManualSafe(t *testing.T) {
	state := executorStateWithSchedule(t, schedule.Specification{Kind: schedule.KindDaily, Timezone: "UTC", LocalMinute: 23 * 60, QuietPeriod: &schedule.QuietPeriod{StartMinute: 22 * 60, EndMinute: 6 * 60}})
	exit := 0
	runner := &scriptedRunner{responses: []jobs.Response{
		{Version: jobs.ProtocolVersion, RunID: "11111111111111111111111111111111", State: jobs.StateSucceeded, ExitCode: &exit},
		{Version: jobs.ProtocolVersion, RunID: "22222222222222222222222222222222", State: jobs.StateSucceeded, ExitCode: &exit},
	}}
	executor, err := NewExecutor(state, runner, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ids := bytes.NewBufferString("1111111111111111111111111111111122222222222222222222222222222222")
	executor.newRunID = func() (string, error) {
		value := make([]byte, 32)
		_, err := ids.Read(value)
		return string(value), err
	}
	executor.now = func() time.Time { return time.Date(2026, time.October, 11, 10, 0, 0, 0, time.UTC) }
	if err := executor.RunExpectedSimulatedBoot(context.Background()); err == nil {
		t.Fatal("excluded recurrence produced a next decision")
	}
	configuration, _ := state.DutyCycleConfiguration()
	if configuration.Enabled || state.DutyCycleLifecycle().Phase != schedule.PhaseManualSafe {
		t.Fatalf("configuration = %+v, lifecycle = %+v", configuration, state.DutyCycleLifecycle())
	}
}

func TestRecoverRunnerCancelsOnlyAfterManualSafe(t *testing.T) {
	runner := &scriptedRunner{responses: []jobs.Response{{Version: jobs.ProtocolVersion, RunID: "11111111111111111111111111111111", State: jobs.StateCancelled}}}
	state := schedule.Lifecycle{Phase: schedule.PhaseManualSafe, RunList: []schedule.RunEntry{{RunID: "11111111111111111111111111111111", State: schedule.RunFailed}}}
	if err := RecoverRunner(context.Background(), state, runner); err != nil {
		t.Fatal(err)
	}
	if len(runner.requests) != 1 || runner.requests[0].Action != jobs.ActionCancel {
		t.Fatalf("requests = %+v", runner.requests)
	}
}
