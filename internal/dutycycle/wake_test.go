package dutycycle

import (
	"context"
	"errors"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type checkingWakeDevice struct {
	t                *testing.T
	coordinator      *board.Coordinator
	programCalls     int
	disarmCalls      int
	observationCalls int
	clockCalls       int
	err              error
}

func (device *checkingWakeDevice) assertLease() {
	device.t.Helper()
	active, ok := device.coordinator.Active()
	if !ok || active.Kind != board.OperationWake {
		device.t.Fatalf("wake device called without wake lease: %+v, %v", active, ok)
	}
}

func (device *checkingWakeDevice) ProgramAbsolute(_ context.Context, intended uint32) (board.WakeProgrammingResult, error) {
	device.assertLease()
	device.programCalls++
	return board.WakeProgrammingResult{IntendedRTC: intended, RelativeWritten: true, RelativeVerified: true, AbsoluteWritten: true, AbsoluteReadBack: intended, AbsoluteVerified: true}, device.err
}

func (device *checkingWakeDevice) Disarm(context.Context) (board.WakeProgrammingResult, error) {
	device.assertLease()
	device.disarmCalls++
	return board.WakeProgrammingResult{RelativeWritten: true, RelativeVerified: true, AbsoluteWritten: true, AbsoluteVerified: true}, device.err
}

func (device *checkingWakeDevice) ObserveBoot(context.Context) (schedule.Observation, error) {
	device.assertLease()
	device.observationCalls++
	return schedule.Observation{RTCBefore: 10, RTCAfter: 11}, device.err
}

func (device *checkingWakeDevice) ReadRTC(context.Context) (uint32, error) {
	device.assertLease()
	device.clockCalls++
	return 12, device.err
}

func TestWakeControllerHoldsCoordinatorAcrossEveryDeviceTransaction(t *testing.T) {
	coordinator := board.NewCoordinator()
	device := &checkingWakeDevice{t: t, coordinator: coordinator}
	controller, err := NewWakeController(device, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	programmed, err := controller.ProgramAbsolute(context.Background(), 100)
	if err != nil || !programmed.Verified() {
		t.Fatalf("ProgramAbsolute() = %+v, %v", programmed, err)
	}
	disarmed, err := controller.Disarm(context.Background())
	if err != nil || !disarmed.Verified() {
		t.Fatalf("Disarm() = %+v, %v", disarmed, err)
	}
	observed, err := controller.ObserveBoot(context.Background())
	if err != nil || observed.RTCBefore != 10 || observed.RTCAfter != 11 {
		t.Fatalf("ObserveBoot() = %+v, %v", observed, err)
	}
	clock, err := controller.ReadRTC(context.Background())
	if err != nil || clock != 12 {
		t.Fatalf("ReadRTC() = %d, %v", clock, err)
	}
	if device.programCalls != 1 || device.disarmCalls != 1 || device.observationCalls != 1 || device.clockCalls != 1 {
		t.Fatalf("device calls = program %d, disarm %d, observe %d", device.programCalls, device.disarmCalls, device.observationCalls)
	}
	if _, active := coordinator.Active(); active {
		t.Fatal("coordinator lease remained active")
	}
}

func TestWakeControllerReleasesCoordinatorAfterDeviceFailure(t *testing.T) {
	coordinator := board.NewCoordinator()
	device := &checkingWakeDevice{t: t, coordinator: coordinator, err: board.ErrCommunication}
	controller, err := NewWakeController(device, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ProgramAbsolute(context.Background(), 100); !errors.Is(err, board.ErrCommunication) {
		t.Fatalf("ProgramAbsolute() error = %v", err)
	}
	lease, err := coordinator.Acquire(context.Background(), board.OperationRead, "after failed wake")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

func TestWakeControllerCannotOperateAfterShutdownQuiescence(t *testing.T) {
	coordinator := board.NewCoordinator()
	device := &checkingWakeDevice{t: t, coordinator: coordinator}
	controller, err := NewWakeController(device, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	shutdown, err := coordinator.BeginShutdown(context.Background(), "test shutdown")
	if err != nil {
		t.Fatal(err)
	}
	shutdown.Commit()
	if _, err := controller.Disarm(context.Background()); !errors.Is(err, board.ErrCoordinatorQuiesced) {
		t.Fatalf("Disarm() error = %v", err)
	}
	if device.disarmCalls != 0 {
		t.Fatal("wake device called after coordinator quiesced")
	}
}
