package dutycycle

import (
	"context"
	"errors"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type continuationState struct {
	configuration schedule.Configuration
	lifecycle     schedule.Lifecycle
	transitions   []schedule.Phase
	disarm        schedule.DisarmStatus
}

func (state *continuationState) DutyCycleLifecycle() schedule.Lifecycle { return state.lifecycle }
func (state *continuationState) DutyCycleConfiguration() (schedule.Configuration, bool) {
	return state.configuration, true
}
func (state *continuationState) RecordNextWakeCalculated(_ uint64, occurrence uint32) error {
	state.lifecycle.Phase = schedule.PhaseNextWakeCalculated
	state.lifecycle.IntendedOccurrence = occurrence
	state.transitions = append(state.transitions, state.lifecycle.Phase)
	return nil
}
func (state *continuationState) BeginWakeProgramming(uint64) error {
	state.lifecycle.Phase = schedule.PhaseWakeProgramming
	state.transitions = append(state.transitions, state.lifecycle.Phase)
	return nil
}
func (state *continuationState) RecordWakeVerified(_ uint64, result board.WakeProgrammingResult) error {
	if !result.Verified() {
		return errors.New("unverified")
	}
	state.lifecycle.Phase = schedule.PhaseWakeVerified
	state.transitions = append(state.transitions, state.lifecycle.Phase)
	return nil
}
func (state *continuationState) FailScheduledRun(string) error {
	state.lifecycle.Phase = schedule.PhaseManualSafe
	state.transitions = append(state.transitions, state.lifecycle.Phase)
	return nil
}
func (state *continuationState) RecordWakeDisarm(status schedule.DisarmStatus) error {
	state.disarm = status
	return nil
}
func (state *continuationState) CompleteOneTimeDutyCycle(uint64) error {
	state.lifecycle.Phase = schedule.PhaseCompleted
	state.transitions = append(state.transitions, state.lifecycle.Phase)
	return nil
}

type continuationWakeDevice struct {
	state       *continuationState
	reference   uint32
	programErr  error
	programmed  uint32
	disarmCalls int
}

func (device *continuationWakeDevice) ProgramAbsolute(_ context.Context, occurrence uint32) (board.WakeProgrammingResult, error) {
	if device.state.lifecycle.Phase != schedule.PhaseWakeProgramming {
		return board.WakeProgrammingResult{}, errors.New("hardware called before programming intent persisted")
	}
	device.programmed = occurrence
	if device.programErr != nil {
		return board.WakeProgrammingResult{IntendedRTC: occurrence}, device.programErr
	}
	return board.WakeProgrammingResult{IntendedRTC: occurrence, RelativeWritten: true, RelativeVerified: true, AbsoluteWritten: true, AbsoluteReadBack: occurrence, AbsoluteVerified: true}, nil
}
func (device *continuationWakeDevice) Disarm(context.Context) (board.WakeProgrammingResult, error) {
	if device.state.lifecycle.Phase != schedule.PhaseManualSafe {
		return board.WakeProgrammingResult{}, errors.New("disarm called before manual-safe")
	}
	device.disarmCalls++
	return board.WakeProgrammingResult{RelativeWritten: true, RelativeVerified: true, AbsoluteWritten: true, AbsoluteVerified: true}, nil
}
func (device *continuationWakeDevice) ObserveBoot(context.Context) (schedule.Observation, error) {
	return schedule.Observation{}, errors.New("unexpected observation")
}
func (device *continuationWakeDevice) ReadRTC(context.Context) (uint32, error) {
	return device.reference, nil
}

func recurringContinuationState() *continuationState {
	return &continuationState{
		configuration: schedule.Configuration{Revision: 1, Enabled: true, Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15}},
		lifecycle:     schedule.Lifecycle{Phase: schedule.PhaseRunListSucceeded, ConfigurationRevision: 1, IntendedOccurrence: 1_800_000_000, Simulated: false},
	}
}

func newTestContinuation(t *testing.T, state *continuationState, device *continuationWakeDevice) *Continuation {
	t.Helper()
	wake, err := NewWakeController(device, board.NewCoordinator())
	if err != nil {
		t.Fatal(err)
	}
	continuation, err := NewContinuation(state, wake)
	if err != nil {
		t.Fatal(err)
	}
	return continuation
}

func TestContinuationPersistsIntentBeforeProgrammingAndStopsVerified(t *testing.T) {
	state := recurringContinuationState()
	device := &continuationWakeDevice{state: state, reference: 1_800_000_000}
	occurrence, err := newTestContinuation(t, state, device).ArmNext(context.Background())
	if err != nil || occurrence == 0 || device.programmed != occurrence || state.lifecycle.Phase != schedule.PhaseWakeVerified || state.disarm != "" {
		t.Fatalf("ArmNext() = %d, %v; state=%+v device=%+v", occurrence, err, state, device)
	}
	want := []schedule.Phase{schedule.PhaseNextWakeCalculated, schedule.PhaseWakeProgramming, schedule.PhaseWakeVerified}
	if len(state.transitions) != len(want) {
		t.Fatalf("transitions = %v", state.transitions)
	}
	for index := range want {
		if state.transitions[index] != want[index] {
			t.Fatalf("transitions = %v, want %v", state.transitions, want)
		}
	}
}

func TestContinuationProgrammingFailureEntersSafeThenDisarms(t *testing.T) {
	state := recurringContinuationState()
	device := &continuationWakeDevice{state: state, reference: 1_800_000_000, programErr: board.ErrCommunication}
	if _, err := newTestContinuation(t, state, device).ArmNext(context.Background()); !errors.Is(err, board.ErrCommunication) {
		t.Fatalf("ArmNext() error = %v", err)
	}
	if state.lifecycle.Phase != schedule.PhaseManualSafe || state.disarm != schedule.DisarmVerified || device.disarmCalls != 1 {
		t.Fatalf("state=%+v device=%+v", state, device)
	}
}

func TestContinuationCompletesOneTimeWithoutHardwareAccess(t *testing.T) {
	state := recurringContinuationState()
	state.configuration.Schedule = schedule.Specification{Kind: schedule.KindOneTime, Timezone: "UTC", OneTimeDate: "2026-10-11", LocalMinute: 600}
	device := &continuationWakeDevice{state: state, reference: 1_800_000_000}
	occurrence, err := newTestContinuation(t, state, device).ArmNext(context.Background())
	if err != nil || occurrence != 0 || state.lifecycle.Phase != schedule.PhaseCompleted || device.programmed != 0 || device.disarmCalls != 0 {
		t.Fatalf("ArmNext() = %d, %v; state=%+v device=%+v", occurrence, err, state, device)
	}
}
