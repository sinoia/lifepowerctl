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

type shutdownState struct {
	configuration schedule.Configuration
	lifecycle     schedule.Lifecycle
	disarm        schedule.DisarmStatus
	recordErr     error
	failErr       error
}

func verifiedShutdownState() *shutdownState {
	return &shutdownState{
		configuration: schedule.Configuration{Revision: 3, Enabled: true},
		lifecycle: schedule.Lifecycle{
			Phase: schedule.PhaseWakeVerified, ConfigurationRevision: 3, IntendedOccurrence: 10_000,
			WakeKind: schedule.WakeAbsoluteRTC, ProgrammedRTCWake: 10_000,
		},
	}
}

func (state *shutdownState) DutyCycleLifecycle() schedule.Lifecycle { return state.lifecycle }
func (state *shutdownState) DutyCycleConfiguration() (schedule.Configuration, bool) {
	return state.configuration, true
}
func (state *shutdownState) RecordShutdownRequested(uint64) error {
	if state.recordErr != nil {
		return state.recordErr
	}
	state.lifecycle.Phase = schedule.PhaseShutdownRequested
	return nil
}
func (state *shutdownState) FailScheduledRun(string) error {
	if state.failErr != nil {
		return state.failErr
	}
	state.configuration.Enabled = false
	state.lifecycle.Phase = schedule.PhaseManualSafe
	return nil
}
func (state *shutdownState) RecordWakeDisarm(status schedule.DisarmStatus) error {
	state.disarm = status
	return nil
}

type shutdownWakeDevice struct {
	state       *shutdownState
	disarmCalls int
}

func (device *shutdownWakeDevice) ProgramAbsolute(context.Context, uint32) (board.WakeProgrammingResult, error) {
	return board.WakeProgrammingResult{}, errors.New("unexpected program")
}
func (device *shutdownWakeDevice) ObserveBoot(context.Context) (schedule.Observation, error) {
	return schedule.Observation{}, errors.New("unexpected observe")
}
func (device *shutdownWakeDevice) ReadRTC(context.Context) (uint32, error) {
	return 0, errors.New("unexpected clock read")
}
func (device *shutdownWakeDevice) Disarm(context.Context) (board.WakeProgrammingResult, error) {
	if device.state.lifecycle.Phase != schedule.PhaseManualSafe {
		return board.WakeProgrammingResult{}, errors.New("disarm before manual-safe")
	}
	device.disarmCalls++
	return board.WakeProgrammingResult{RelativeWritten: true, RelativeVerified: true, AbsoluteWritten: true, AbsoluteVerified: true}, nil
}

type shutdownPower struct {
	t           *testing.T
	state       *shutdownState
	coordinator *board.Coordinator
	err         error
	calls       int
}

func (power *shutdownPower) RequestPoweroff(context.Context) error {
	power.t.Helper()
	active, ok := power.coordinator.Active()
	if !ok || active.Kind != board.OperationShutdown || power.state.lifecycle.Phase != schedule.PhaseShutdownRequested {
		power.t.Fatalf("power request before durable shutdown under lease: active=%+v, ok=%v, phase=%s", active, ok, power.state.lifecycle.Phase)
	}
	power.calls++
	return power.err
}

func testShutdownController(t *testing.T, state *shutdownState, powerErr error) (*ShutdownController, *board.Coordinator, *shutdownWakeDevice, *shutdownPower) {
	t.Helper()
	coordinator := board.NewCoordinator()
	device := &shutdownWakeDevice{state: state}
	wake, err := NewWakeController(device, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	power := &shutdownPower{t: t, state: state, coordinator: coordinator, err: powerErr}
	controller, err := NewShutdownController(state, wake, coordinator, power)
	if err != nil {
		t.Fatal(err)
	}
	return controller, coordinator, device, power
}

func TestShutdownPersistsRequestUnderLeaseBeforeFixedPowerBoundary(t *testing.T) {
	state := verifiedShutdownState()
	controller, coordinator, device, power := testShutdownController(t, state, nil)
	journal, err := activity.Open(t.TempDir(), activity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	controller.SetAudit(activity.NewAudit(journal))
	if err := controller.Request(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, err := journal.Events()
	if err != nil || len(events) != 1 || !strings.Contains(string(events[0].Details), `"phase":"shutdown-requested"`) {
		t.Fatalf("shutdown Activity = %+v, %v", events, err)
	}
	if power.calls != 1 || device.disarmCalls != 0 || state.lifecycle.Phase != schedule.PhaseShutdownRequested {
		t.Fatalf("state=%+v power=%d disarms=%d", state, power.calls, device.disarmCalls)
	}
	if _, err := coordinator.Acquire(context.Background(), board.OperationRead, "after accepted shutdown"); !errors.Is(err, board.ErrCoordinatorQuiesced) {
		t.Fatalf("coordinator after accepted shutdown = %v", err)
	}
}

func TestShutdownRejectionReopensCoordinatorThenEntersSafeAndDisarms(t *testing.T) {
	state := verifiedShutdownState()
	controller, coordinator, device, power := testShutdownController(t, state, errors.New("rejected"))
	if err := controller.Request(context.Background()); err == nil {
		t.Fatal("rejected power request succeeded")
	}
	if power.calls != 1 || device.disarmCalls != 1 || state.lifecycle.Phase != schedule.PhaseManualSafe || state.disarm != schedule.DisarmVerified {
		t.Fatalf("state=%+v power=%d disarms=%d", state, power.calls, device.disarmCalls)
	}
	lease, err := coordinator.Acquire(context.Background(), board.OperationRead, "after rejected shutdown")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

func TestShutdownPersistenceFailureNeverCallsPower(t *testing.T) {
	state := verifiedShutdownState()
	state.recordErr = errors.New("disk full")
	controller, coordinator, device, power := testShutdownController(t, state, nil)
	if err := controller.Request(context.Background()); err == nil {
		t.Fatal("shutdown persistence failure succeeded")
	}
	if power.calls != 0 || device.disarmCalls != 0 || state.lifecycle.Phase != schedule.PhaseWakeVerified {
		t.Fatalf("state=%+v power=%d disarms=%d", state, power.calls, device.disarmCalls)
	}
	lease, err := coordinator.Acquire(context.Background(), board.OperationRead, "after persistence failure")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

func TestShutdownRejectsIncompleteEvidenceBeforeQuiescing(t *testing.T) {
	state := verifiedShutdownState()
	state.lifecycle.ProgrammedRTCWake++
	controller, coordinator, _, power := testShutdownController(t, state, nil)
	if err := controller.Request(context.Background()); err == nil {
		t.Fatal("incomplete evidence authorized shutdown")
	}
	if power.calls != 0 {
		t.Fatal("power boundary was called")
	}
	lease, err := coordinator.Acquire(context.Background(), board.OperationRead, "after rejected evidence")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}
