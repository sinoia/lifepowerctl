package dutycycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type startupState struct {
	intent       schedule.ArmedIntent
	lifecycle    schedule.Lifecycle
	expected     bool
	manualSafe   bool
	reason       string
	disarmStatus schedule.DisarmStatus
	err          error
}

func (state *startupState) DutyCycleArmedIntent() schedule.ArmedIntent { return state.intent }
func (state *startupState) DutyCycleLifecycle() schedule.Lifecycle     { return state.lifecycle }
func (state *startupState) ObserveExpectedBoot() error {
	state.expected = true
	state.lifecycle.Phase = schedule.PhaseExpectedBootObserved
	return state.err
}
func (state *startupState) EnterBootManualSafe(reason string) error {
	state.manualSafe, state.reason = true, reason
	state.lifecycle.Phase, state.lifecycle.Reason, state.lifecycle.WakeDisarmStatus = schedule.PhaseManualSafe, reason, schedule.DisarmPending
	return state.err
}
func (state *startupState) RecordWakeDisarm(status schedule.DisarmStatus) error {
	state.disarmStatus = status
	state.lifecycle.WakeDisarmStatus = status
	return state.err
}

type startupWakeDevice struct {
	state        *startupState
	observation  schedule.Observation
	observeErr   error
	disarmResult board.WakeProgrammingResult
	disarmErr    error
	disarmCalls  int
}

func (device *startupWakeDevice) ProgramAbsolute(context.Context, uint32) (board.WakeProgrammingResult, error) {
	return board.WakeProgrammingResult{}, errors.New("unexpected program")
}
func (device *startupWakeDevice) ObserveBoot(context.Context) (schedule.Observation, error) {
	return device.observation, device.observeErr
}
func (device *startupWakeDevice) ReadRTC(context.Context) (uint32, error) {
	return device.observation.RTCAfter, device.observeErr
}
func (device *startupWakeDevice) Disarm(context.Context) (board.WakeProgrammingResult, error) {
	if !device.state.manualSafe {
		return board.WakeProgrammingResult{}, errors.New("disarm occurred before manual-safe persistence")
	}
	device.disarmCalls++
	return device.disarmResult, device.disarmErr
}

func armedStartupState() *startupState {
	return &startupState{intent: schedule.ArmedIntent{
		Enabled: true, Phase: schedule.ArmedShutdownRequested, WakeKind: schedule.WakeAbsoluteRTC,
		ExpectedRTC: 10_000, ProgrammedRTCWake: 10_000,
	}}
}

func startupController(t *testing.T, device WakeDevice) *WakeController {
	t.Helper()
	controller, err := NewWakeController(device, board.NewCoordinator())
	if err != nil {
		t.Fatal(err)
	}
	return controller
}

func TestClassifyStartupConsumesOnlyExpectedBoot(t *testing.T) {
	state := armedStartupState()
	device := &startupWakeDevice{state: state, observation: schedule.Observation{RTCBefore: 10_020, RTCAfter: 10_021}}
	result, err := ClassifyStartup(context.Background(), state, startupController(t, device), schedule.DefaultBootPolicy())
	if err != nil || result.Classification.Class != schedule.BootExpected || !state.expected || state.manualSafe || device.disarmCalls != 0 {
		t.Fatalf("ClassifyStartup() = %+v, %v; state=%+v disarms=%d", result, err, state, device.disarmCalls)
	}
}

func TestClassifyStartupPersistsManualSafeBeforeVerifiedDisarm(t *testing.T) {
	state := armedStartupState()
	device := &startupWakeDevice{
		state: state, observation: schedule.Observation{RTCBefore: 9_000, RTCAfter: 9_001, RTCWake: 10_000},
		disarmResult: board.WakeProgrammingResult{RelativeWritten: true, RelativeVerified: true, AbsoluteWritten: true, AbsoluteVerified: true},
	}
	journal, err := activity.Open(t.TempDir(), activity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ClassifyStartupWithAudit(context.Background(), state, startupController(t, device), schedule.DefaultBootPolicy(), activity.NewAudit(journal))
	if err != nil || result.Classification.Class != schedule.BootManualSafe || !state.manualSafe || state.expected || state.disarmStatus != schedule.DisarmVerified || device.disarmCalls != 1 {
		t.Fatalf("ClassifyStartup() = %+v, %v; state=%+v disarms=%d", result, err, state, device.disarmCalls)
	}
	events, eventErr := journal.Events()
	if eventErr != nil || len(events) != 1 || !strings.Contains(string(events[0].Details), `"phase":"manual-safe"`) || !strings.Contains(string(events[0].Details), `"wake_disarm_status":"verified"`) {
		t.Fatalf("startup Activity = %+v, %v", events, eventErr)
	}
}

func TestClassifyStartupReadFailureDisablesAndRecordsUncertainDisarm(t *testing.T) {
	state := armedStartupState()
	device := &startupWakeDevice{state: state, observeErr: board.ErrCommunication, disarmErr: board.ErrCommunication}
	result, err := ClassifyStartup(context.Background(), state, startupController(t, device), schedule.DefaultBootPolicy())
	if !errors.Is(err, board.ErrCommunication) || result.Classification.Class != schedule.BootAmbiguous || !state.manualSafe || state.disarmStatus != schedule.DisarmUncertain || device.disarmCalls != 1 {
		t.Fatalf("ClassifyStartup() = %+v, %v; state=%+v disarms=%d", result, err, state, device.disarmCalls)
	}
}

func TestClassifyStartupDoesNotTouchHardwareWhenInactive(t *testing.T) {
	state := &startupState{}
	device := &startupWakeDevice{state: state, observeErr: errors.New("must not observe")}
	result, err := ClassifyStartup(context.Background(), state, startupController(t, device), schedule.DefaultBootPolicy())
	if err != nil || result.Classification.Class != schedule.BootInactive || state.manualSafe || device.disarmCalls != 0 {
		t.Fatalf("ClassifyStartup() = %+v, %v", result, err)
	}
}

func TestClassifyStartupDoesNotDisarmIfManualSafePersistenceFails(t *testing.T) {
	state := armedStartupState()
	state.err = errors.New("storage unavailable")
	device := &startupWakeDevice{state: state, observation: schedule.Observation{RTCBefore: 9_000, RTCAfter: 9_001, RTCWake: 10_000}}
	if _, err := ClassifyStartup(context.Background(), state, startupController(t, device), schedule.DefaultBootPolicy()); err == nil || device.disarmCalls != 0 {
		t.Fatalf("ClassifyStartup() error = %v; disarms=%d", err, device.disarmCalls)
	}
}

func TestReconcilePendingDisarmCompletesRealRestartRecovery(t *testing.T) {
	state := &startupState{
		manualSafe: true,
		lifecycle:  schedule.Lifecycle{Phase: schedule.PhaseManualSafe, Reason: "controller restarted", WakeDisarmStatus: schedule.DisarmPending},
	}
	device := &startupWakeDevice{
		state: state,
		disarmResult: board.WakeProgrammingResult{
			RelativeWritten: true, RelativeVerified: true, AbsoluteWritten: true, AbsoluteVerified: true,
		},
	}
	attempted, disarmErr, err := ReconcilePendingDisarm(context.Background(), state, startupController(t, device))
	if err != nil || disarmErr != nil || !attempted || state.disarmStatus != schedule.DisarmVerified || device.disarmCalls != 1 {
		t.Fatalf("ReconcilePendingDisarm() = %v, %v, %v; state=%+v disarms=%d", attempted, disarmErr, err, state, device.disarmCalls)
	}
}
