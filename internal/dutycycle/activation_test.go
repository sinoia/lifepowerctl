package dutycycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/controllerstate"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type activationState struct {
	revision      uint64
	configuration schedule.Configuration
	lifecycle     schedule.Lifecycle
	receipt       *controllerstate.ConfigurationResult
	proposalID    string
	disarm        schedule.DisarmStatus
}

func newActivationState() *activationState {
	return &activationState{revision: 8, configuration: schedule.Configuration{
		Revision: 3, Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15},
		RunList: []schedule.RunListEntry{{JobID: "job", JobRevision: 1}},
	}}
}

func (state *activationState) Revision() uint64 { return state.revision }
func (state *activationState) DutyCycleConfiguration() (schedule.Configuration, bool) {
	return state.configuration, true
}
func (state *activationState) DutyCycleLifecycle() schedule.Lifecycle        { return state.lifecycle }
func (state *activationState) DutyCycleRunListMaximum() (int, uint64, error) { return 1, 35, nil }
func (state *activationState) RealActivationResult(proposalID string, _, _ uint64, _ string, _ uint32) (controllerstate.ConfigurationResult, error) {
	if state.receipt != nil && proposalID == state.proposalID {
		result := *state.receipt
		result.Existing = true
		return result, nil
	}
	return controllerstate.ConfigurationResult{}, controllerstate.ErrProposalNotFound
}
func (state *activationState) EnableReal(proposalID string, base, revision uint64, _ string, _, occurrence uint32) (controllerstate.ConfigurationResult, error) {
	if base != state.revision || revision != state.configuration.Revision || state.configuration.Enabled {
		return controllerstate.ConfigurationResult{}, controllerstate.ErrConfigurationRevision
	}
	state.configuration.Enabled = true
	state.revision++
	state.lifecycle = schedule.Lifecycle{Phase: schedule.PhaseNextWakeCalculated, ConfigurationRevision: revision, IntendedOccurrence: occurrence}
	result := controllerstate.ConfigurationResult{StateRevision: state.revision, ConfigurationRevision: revision}
	state.receipt, state.proposalID = &result, proposalID
	return result, nil
}
func (state *activationState) BeginWakeProgramming(uint64) error {
	if state.lifecycle.Phase != schedule.PhaseNextWakeCalculated {
		return errors.New("wake was not calculated")
	}
	state.lifecycle.Phase = schedule.PhaseWakeProgramming
	return nil
}
func (state *activationState) RecordWakeVerified(_ uint64, result board.WakeProgrammingResult) error {
	if state.lifecycle.Phase != schedule.PhaseWakeProgramming || !result.Verified() {
		return errors.New("wake was not verified")
	}
	state.lifecycle.Phase = schedule.PhaseWakeVerified
	return nil
}
func (state *activationState) FailScheduledRun(string) error {
	state.configuration.Enabled = false
	state.lifecycle.Phase = schedule.PhaseManualSafe
	return nil
}
func (state *activationState) RecordWakeDisarm(status schedule.DisarmStatus) error {
	state.disarm = status
	return nil
}

type activationWake struct {
	state        *activationState
	references   []uint32
	reads        int
	programs     int
	disarms      int
	programError error
}

func (device *activationWake) ReadRTC(context.Context) (uint32, error) {
	value := device.references[device.reads]
	device.reads++
	return value, nil
}
func (device *activationWake) ProgramAbsolute(_ context.Context, occurrence uint32) (board.WakeProgrammingResult, error) {
	if device.state.lifecycle.Phase != schedule.PhaseWakeProgramming {
		return board.WakeProgrammingResult{}, errors.New("program before durable intent")
	}
	device.programs++
	if device.programError != nil {
		return board.WakeProgrammingResult{}, device.programError
	}
	return board.WakeProgrammingResult{IntendedRTC: occurrence, RelativeWritten: true, RelativeVerified: true, AbsoluteWritten: true, AbsoluteReadBack: occurrence, AbsoluteVerified: true}, nil
}
func (device *activationWake) Disarm(context.Context) (board.WakeProgrammingResult, error) {
	if device.state.lifecycle.Phase != schedule.PhaseManualSafe {
		return board.WakeProgrammingResult{}, errors.New("disarm before manual-safe")
	}
	device.disarms++
	return board.WakeProgrammingResult{RelativeWritten: true, RelativeVerified: true, AbsoluteWritten: true, AbsoluteVerified: true}, nil
}
func (device *activationWake) ObserveBoot(context.Context) (schedule.Observation, error) {
	return schedule.Observation{}, errors.New("unexpected boot observation")
}

type activationShutdown struct {
	state *activationState
	calls int
}

func (shutdown *activationShutdown) Request(context.Context) error {
	if shutdown.state.lifecycle.Phase != schedule.PhaseWakeVerified {
		return errors.New("shutdown before verified wake")
	}
	shutdown.calls++
	shutdown.state.lifecycle.Phase = schedule.PhaseShutdownRequested
	return nil
}

func testActivator(t *testing.T, state *activationState, device *activationWake, shutdown *activationShutdown) *Activator {
	t.Helper()
	wake, err := NewWakeController(device, board.NewCoordinator())
	if err != nil {
		t.Fatal(err)
	}
	activator, err := NewActivator(state, wake, shutdown)
	if err != nil {
		t.Fatal(err)
	}
	return activator
}

func TestActivatorReviewsBoardTimeAndCompletesStrictRealActivation(t *testing.T) {
	state := newActivationState()
	device := &activationWake{state: state, references: []uint32{1_800_000_000, 1_800_000_000}}
	shutdown := &activationShutdown{state: state}
	activator := testActivator(t, state, device, shutdown)
	review, err := activator.Preview(context.Background(), 3, 5)
	if err != nil || review.Reference != 1_800_000_000 || review.MinimumLeadSeconds != 300 || review.RunListJobs != 1 || review.MaximumRunSeconds != 35 {
		t.Fatalf("Preview() = %+v, %v", review, err)
	}
	result, err := activator.Enable(context.Background(), "00112233445566778899aabbccddeeff", 8, 3, review)
	if err != nil || result.StateRevision != 9 || device.programs != 1 || device.disarms != 0 || shutdown.calls != 1 || state.lifecycle.Phase != schedule.PhaseShutdownRequested {
		t.Fatalf("Enable() = %+v, %v; state=%+v device=%+v shutdown=%+v", result, err, state, device, shutdown)
	}
}

func TestActivatorChangedBoardOccurrenceRequiresConfirmationWithoutMutation(t *testing.T) {
	state := newActivationState()
	device := &activationWake{state: state, references: []uint32{1_800_000_000, 1_800_004_000}}
	shutdown := &activationShutdown{state: state}
	activator := testActivator(t, state, device, shutdown)
	review, err := activator.Preview(context.Background(), 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	_, err = activator.Enable(context.Background(), "00112233445566778899aabbccddeeff", 8, 3, review)
	var changed *ConfirmationRequiredError
	if !errors.As(err, &changed) || state.configuration.Enabled || device.programs != 0 || shutdown.calls != 0 {
		t.Fatalf("Enable() error = %v; state=%+v device=%+v", err, state, device)
	}
}

func TestActivatorAfterCompletionConfirmsRuleAndUsesFreshBoardTime(t *testing.T) {
	state := newActivationState()
	state.configuration.Schedule = schedule.Specification{Kind: schedule.KindAfterCompletion, Timezone: "UTC", DelaySeconds: 15 * 60}
	device := &activationWake{state: state, references: []uint32{1_800_000_000, 1_800_000_037}}
	shutdown := &activationShutdown{state: state}
	activator := testActivator(t, state, device, shutdown)
	review, err := activator.Preview(context.Background(), 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	result, err := activator.Enable(context.Background(), "00112233445566778899aabbccddeeff", 8, 3, review)
	wantOccurrence := uint32(1_800_000_937)
	if err != nil || result.StateRevision != 9 || state.lifecycle.IntendedOccurrence != wantOccurrence || device.programs != 1 || shutdown.calls != 1 {
		t.Fatalf("Enable() = %+v, %v; lifecycle=%+v device=%+v shutdown=%+v", result, err, state.lifecycle, device, shutdown)
	}
}

func TestActivatorRecordsDurableRealWakeTransitions(t *testing.T) {
	state := newActivationState()
	device := &activationWake{state: state, references: []uint32{1_800_000_000, 1_800_000_000}}
	shutdown := &activationShutdown{state: state}
	activator := testActivator(t, state, device, shutdown)
	journal, err := activity.Open(t.TempDir(), activity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	audit := activity.NewAudit(journal)
	activator.SetAudit(audit)
	review, err := activator.Preview(context.Background(), 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := activator.Enable(context.Background(), "00112233445566778899aabbccddeeff", 8, 3, review); err != nil {
		t.Fatal(err)
	}
	events, err := journal.Events()
	if err != nil {
		t.Fatal(err)
	}
	want := []schedule.Phase{schedule.PhaseNextWakeCalculated, schedule.PhaseWakeProgramming, schedule.PhaseWakeVerified}
	if len(events) != len(want)+1 || events[0].Type != string(activity.DutyCycleConfiguration) || !strings.Contains(string(events[0].Details), `"action":"enable-real"`) {
		t.Fatalf("events = %+v", events)
	}
	for index, phase := range want {
		event := events[index+1]
		if event.Type != string(activity.DutyCycleTransition) || !strings.Contains(string(event.Details), `"phase":"`+string(phase)+`"`) || !strings.Contains(string(event.Details), `"simulated":false`) {
			t.Fatalf("event[%d] = %+v", index+1, event)
		}
	}
}

func TestActivatorProgrammingFailureEntersSafeAndDisarms(t *testing.T) {
	state := newActivationState()
	device := &activationWake{state: state, references: []uint32{1_800_000_000, 1_800_000_000}, programError: board.ErrCommunication}
	shutdown := &activationShutdown{state: state}
	activator := testActivator(t, state, device, shutdown)
	review, err := activator.Preview(context.Background(), 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	_, err = activator.Enable(context.Background(), "00112233445566778899aabbccddeeff", 8, 3, review)
	if !errors.Is(err, board.ErrCommunication) || state.lifecycle.Phase != schedule.PhaseManualSafe || state.disarm != schedule.DisarmVerified || device.disarms != 1 || shutdown.calls != 0 {
		t.Fatalf("Enable() error = %v; state=%+v device=%+v", err, state, device)
	}
}

func TestActivatorDuplicateProposalDoesNotReplayHardware(t *testing.T) {
	state := newActivationState()
	stored := controllerstate.ConfigurationResult{StateRevision: 9, ConfigurationRevision: 3}
	state.receipt, state.proposalID = &stored, "00112233445566778899aabbccddeeff"
	device := &activationWake{state: state}
	shutdown := &activationShutdown{state: state}
	review := schedule.Review{Identity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Occurrences: []schedule.Occurrence{{Unix: 1_800_000_900}}}
	result, err := testActivator(t, state, device, shutdown).Enable(context.Background(), state.proposalID, 8, 3, review)
	if err != nil || !result.Existing || device.reads != 0 || device.programs != 0 || shutdown.calls != 0 {
		t.Fatalf("Enable() = %+v, %v; device=%+v shutdown=%+v", result, err, device, shutdown)
	}
}
