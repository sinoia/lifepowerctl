package dutycycle

import (
	"context"
	"fmt"

	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

// WakeDevice is intentionally narrower than the general board interface. The
// implementation fixes the registers and command order at the hardware edge.
type WakeDevice interface {
	ProgramAbsolute(context.Context, uint32) (board.WakeProgrammingResult, error)
	Disarm(context.Context) (board.WakeProgrammingResult, error)
	ObserveBoot(context.Context) (schedule.Observation, error)
	ReadRTC(context.Context) (uint32, error)
}

// WakeController serializes every scheduler-owned wake transaction with all
// other application hardware work.
type WakeController struct {
	device      WakeDevice
	coordinator *board.Coordinator
}

func NewWakeController(device WakeDevice, coordinator *board.Coordinator) (*WakeController, error) {
	if device == nil || coordinator == nil {
		return nil, fmt.Errorf("wake device and hardware coordinator are required")
	}
	return &WakeController{device: device, coordinator: coordinator}, nil
}

func (controller *WakeController) ProgramAbsolute(ctx context.Context, intended uint32) (board.WakeProgrammingResult, error) {
	var result board.WakeProgrammingResult
	err := controller.coordinator.WithLease(ctx, board.OperationWake, "program absolute duty-cycle wake", func() error {
		var err error
		result, err = controller.device.ProgramAbsolute(ctx, intended)
		return err
	})
	return result, err
}

func (controller *WakeController) Disarm(ctx context.Context) (board.WakeProgrammingResult, error) {
	var result board.WakeProgrammingResult
	err := controller.coordinator.WithLease(ctx, board.OperationWake, "disarm duty-cycle wake", func() error {
		var err error
		result, err = controller.device.Disarm(ctx)
		return err
	})
	return result, err
}

func (controller *WakeController) ObserveBoot(ctx context.Context) (schedule.Observation, error) {
	var result schedule.Observation
	err := controller.coordinator.WithLease(ctx, board.OperationWake, "observe duty-cycle boot", func() error {
		var err error
		result, err = controller.device.ObserveBoot(ctx)
		return err
	})
	return result, err
}

func (controller *WakeController) ReadRTC(ctx context.Context) (uint32, error) {
	var result uint32
	err := controller.coordinator.WithLease(ctx, board.OperationWake, "read board clock for duty cycle", func() error {
		var err error
		result, err = controller.device.ReadRTC(ctx)
		return err
	})
	return result, err
}
