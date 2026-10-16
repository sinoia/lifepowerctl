package registeredit

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
)

var (
	ErrBatchStale     = errors.New("battery thresholds changed since review")
	ErrBatchFailed    = errors.New("battery-threshold change did not complete cleanly")
	ErrBatchUncertain = errors.New("battery-threshold outcome is uncertain")
)

type BatchOutcome string

const (
	BatchOutcomeApplied           BatchOutcome = "applied"
	BatchOutcomeRejectedStale     BatchOutcome = "rejected-stale"
	BatchOutcomeFailedNoChange    BatchOutcome = "failed-no-change"
	BatchOutcomeReconciledApplied BatchOutcome = "reconciled-applied"
	BatchOutcomeRolledBack        BatchOutcome = "rolled-back"
	BatchOutcomePartialKnown      BatchOutcome = "partial-known"
	BatchOutcomeUncertain         BatchOutcome = "uncertain"
	BatchOutcomeFailedBeforeWrite BatchOutcome = "failed-before-write"
)

type BatchResult struct {
	Outcome           BatchOutcome
	Identity          board.Identity
	Plan              BatchPlan
	Fresh             map[board.Register]int64
	Actual            map[board.Register]int64
	Forward           board.ApplyResult
	RollbackAttempted bool
	RollbackPlan      []board.Change
	Rollback          board.ApplyResult
}

// BatchService owns the purpose-specific three-threshold workflow. Its safety
// logic is shared by deterministic fake tests and the narrowly scoped vendor
// adapter used by the web review flow.
type BatchService struct {
	device      Device
	planner     *BatchPlanner
	coordinator *board.Coordinator
	audit       activityRecorder
}

func NewBatchService(device Device, catalogue board.Catalog, coordinator *board.Coordinator, audit activityRecorder) (*BatchService, error) {
	if device == nil || coordinator == nil {
		return nil, fmt.Errorf("batch register editor requires a device and coordinator")
	}
	planner, err := NewBatchPlanner(catalogue)
	if err != nil {
		return nil, err
	}
	return &BatchService{device: device, planner: planner, coordinator: coordinator, audit: audit}, nil
}

// ReviewBatteryThresholds validates a complete staged configuration and
// returns its safe deterministic write order without touching hardware.
func (service *BatchService) ReviewBatteryThresholds(proposal BatchProposal) (BatchPlan, error) {
	return service.planner.PlanBatteryThresholds(proposal)
}

// ReadBatteryThresholds returns a complete exact snapshot in the scaled
// millivolt integers exposed by the vendor CLI. It does not claim raw bytes.
func (service *BatchService) ReadBatteryThresholds(ctx context.Context) (map[board.Register]int64, error) {
	var values map[board.Register]int64
	err := service.coordinator.WithLease(ctx, board.OperationRead, "battery threshold snapshot", func() error {
		identity, err := service.device.Detect(ctx)
		if err != nil {
			return err
		}
		if identity.RegisterSet != 7 {
			return fmt.Errorf("%w: register set %d", ErrUnsupportedBoard, identity.RegisterSet)
		}
		values, err = service.readThresholds(ctx)
		return err
	})
	return values, err
}

func (service *BatchService) ApplyBatteryThresholds(ctx context.Context, proposal BatchProposal) (BatchResult, error) {
	plan, err := service.planner.PlanBatteryThresholds(proposal)
	if err != nil {
		return BatchResult{}, err
	}
	result := BatchResult{Plan: plan}
	err = service.coordinator.WithLease(ctx, board.OperationRegisterWrite, "battery thresholds", func() error {
		service.recordBatchAttempt(plan)
		defer service.recordBatchFinal(&result)
		identity, detectErr := service.device.Detect(ctx)
		result.Identity = identity
		if detectErr != nil {
			result.Outcome = BatchOutcomeFailedBeforeWrite
			return fmt.Errorf("detect board before battery-threshold change: %w", detectErr)
		}
		if identity.RegisterSet != plan.RegisterSet {
			result.Outcome = BatchOutcomeFailedBeforeWrite
			return fmt.Errorf("%w: got register set %d, want %d", ErrUnsupportedBoard, identity.RegisterSet, plan.RegisterSet)
		}
		fresh, readErr := service.readThresholds(ctx)
		if readErr != nil {
			result.Outcome = BatchOutcomeFailedBeforeWrite
			return fmt.Errorf("read fresh battery thresholds: %w", readErr)
		}
		result.Fresh = fresh
		if !maps.Equal(fresh, plan.Before) {
			result.Outcome = BatchOutcomeRejectedStale
			result.Actual = cloneValues(fresh)
			return ErrBatchStale
		}
		forward, applyErr := service.device.Apply(ctx, plan.Changes)
		result.Forward = forward
		service.recordWriteResults("forward", forward)
		if applyErr == nil && verifiedBatch(forward, plan.Changes) {
			result.Outcome = BatchOutcomeApplied
			result.Actual = cloneValues(plan.After)
			return nil
		}
		if applyErr == nil {
			applyErr = fmt.Errorf("device returned an invalid batch verification result")
		}
		return service.reconcileAndRollback(ctx, &result, applyErr)
	})
	if err != nil && result.Outcome == "" {
		result.Outcome = BatchOutcomeFailedBeforeWrite
	}
	return result, err
}

func (service *BatchService) reconcileAndRollback(ctx context.Context, result *BatchResult, applyErr error) error {
	actual, readErr := service.readThresholds(ctx)
	if readErr != nil {
		result.Outcome = BatchOutcomeUncertain
		service.recordRollback("not-attempted-state-unknown", nil, board.ApplyResult{})
		return fmt.Errorf("%w: apply error: %v; reconciliation read: %v", ErrBatchUncertain, applyErr, readErr)
	}
	result.Actual = actual
	if maps.Equal(actual, result.Plan.After) {
		result.Outcome = BatchOutcomeReconciledApplied
		service.recordRollback("not-needed-proposed-observed", nil, board.ApplyResult{})
		return fmt.Errorf("%w: %v", ErrBatchFailed, applyErr)
	}
	if maps.Equal(actual, result.Plan.Before) {
		result.Outcome = BatchOutcomeFailedNoChange
		service.recordRollback("not-needed-no-change", nil, board.ApplyResult{})
		return fmt.Errorf("%w: %v", ErrBatchFailed, applyErr)
	}
	rollback, planErr := service.planner.PlanBatteryThresholds(BatchProposal{
		RegisterSet: result.Plan.RegisterSet, ExpectedValues: actual, ProposedValues: result.Plan.Before,
	})
	if planErr != nil {
		result.Outcome = BatchOutcomePartialKnown
		service.recordRollback("not-safe", nil, board.ApplyResult{})
		return fmt.Errorf("%w: %v; no safe rollback plan: %v", ErrBatchFailed, applyErr, planErr)
	}
	result.RollbackAttempted = true
	result.RollbackPlan = append([]board.Change(nil), rollback.Changes...)
	rollbackResult, rollbackErr := service.device.Apply(ctx, rollback.Changes)
	result.Rollback = rollbackResult
	service.recordRollback("attempted", rollback.Changes, rollbackResult)
	final, finalErr := service.readThresholds(ctx)
	if finalErr != nil {
		result.Actual = nil
		result.Outcome = BatchOutcomeUncertain
		return fmt.Errorf("%w: apply error: %v; rollback error: %v; final read: %v", ErrBatchUncertain, applyErr, rollbackErr, finalErr)
	}
	result.Actual = final
	if maps.Equal(final, result.Plan.Before) {
		result.Outcome = BatchOutcomeRolledBack
		return fmt.Errorf("%w: %v", ErrBatchFailed, applyErr)
	}
	result.Outcome = BatchOutcomePartialKnown
	return fmt.Errorf("%w: apply error: %v; rollback error: %v", ErrBatchFailed, applyErr, rollbackErr)
}

type valueEvidence struct {
	Register board.Register `json:"register"`
	Value    int64          `json:"value"`
	Unit     string         `json:"unit"`
}

type changeEvidence struct {
	Register board.Register `json:"register"`
	Value    int64          `json:"value"`
	Unit     string         `json:"unit"`
}

type writeEvidence struct {
	Stage    string         `json:"stage"`
	Index    int            `json:"index"`
	Register board.Register `json:"register"`
	Value    int64          `json:"value"`
	Unit     string         `json:"unit"`
	Applied  bool           `json:"applied"`
	Verified bool           `json:"verified"`
	ReadBack int64          `json:"read_back_value"`
}

func (service *BatchService) recordBatchAttempt(plan BatchPlan) {
	if service.audit == nil {
		return
	}
	service.audit.Record(activity.RegisterBatchAttempted, activity.ActorAdmin, "battery-thresholds", struct {
		RegisterSet int              `json:"register_set"`
		Constraint  string           `json:"constraint"`
		Before      []valueEvidence  `json:"before"`
		After       []valueEvidence  `json:"after"`
		WriteOrder  []changeEvidence `json:"write_order"`
		Persistence string           `json:"persistence"`
	}{RegisterSet: plan.RegisterSet, Constraint: plan.Constraint, Before: valueEvidenceFor(plan.Before), After: valueEvidenceFor(plan.After), WriteOrder: changeEvidenceFor(plan.Changes), Persistence: "temporary-only"})
}

func (service *BatchService) recordWriteResults(stage string, result board.ApplyResult) {
	if service.audit == nil {
		return
	}
	for index, change := range result.Changes {
		service.audit.Record(activity.RegisterBatchWriteObserved, activity.ActorAdmin, "battery-thresholds", writeEvidence{
			Stage: stage, Index: index, Register: change.Change.Register, Value: change.Change.Value, Unit: "mV",
			Applied: change.Applied, Verified: change.Verified, ReadBack: change.ReadBack,
		})
	}
}

func (service *BatchService) recordRollback(decision string, plan []board.Change, result board.ApplyResult) {
	if service.audit == nil {
		return
	}
	service.audit.Record(activity.RegisterBatchRollbackObserved, activity.ActorAdmin, "battery-thresholds", struct {
		Decision string           `json:"decision"`
		Plan     []changeEvidence `json:"plan"`
		Results  []writeEvidence  `json:"results"`
	}{Decision: decision, Plan: changeEvidenceFor(plan), Results: writeEvidenceFor("rollback", result)})
}

func (service *BatchService) recordBatchFinal(result *BatchResult) {
	if service.audit == nil {
		return
	}
	service.audit.Record(activity.RegisterBatchObserved, activity.ActorAdmin, "battery-thresholds", struct {
		Outcome           BatchOutcome    `json:"outcome"`
		Fresh             []valueEvidence `json:"fresh,omitempty"`
		Actual            []valueEvidence `json:"actual,omitempty"`
		ForwardResults    int             `json:"forward_results"`
		RollbackAttempted bool            `json:"rollback_attempted"`
		RollbackResults   int             `json:"rollback_results"`
		Persistence       string          `json:"persistence"`
	}{Outcome: result.Outcome, Fresh: valueEvidenceFor(result.Fresh), Actual: valueEvidenceFor(result.Actual), ForwardResults: len(result.Forward.Changes), RollbackAttempted: result.RollbackAttempted, RollbackResults: len(result.Rollback.Changes), Persistence: "temporary-only"})
}

func valueEvidenceFor(values map[board.Register]int64) []valueEvidence {
	if values == nil {
		return nil
	}
	result := make([]valueEvidence, 0, len(batteryThresholdRegisters))
	for _, register := range batteryThresholdRegisters {
		if value, ok := values[register]; ok {
			result = append(result, valueEvidence{Register: register, Value: value, Unit: "mV"})
		}
	}
	return result
}

func changeEvidenceFor(changes []board.Change) []changeEvidence {
	result := make([]changeEvidence, len(changes))
	for index, change := range changes {
		result[index] = changeEvidence{Register: change.Register, Value: change.Value, Unit: "mV"}
	}
	return result
}

func writeEvidenceFor(stage string, result board.ApplyResult) []writeEvidence {
	values := make([]writeEvidence, len(result.Changes))
	for index, change := range result.Changes {
		values[index] = writeEvidence{Stage: stage, Index: index, Register: change.Change.Register, Value: change.Change.Value, Unit: "mV", Applied: change.Applied, Verified: change.Verified, ReadBack: change.ReadBack}
	}
	return values
}

func (service *BatchService) readThresholds(ctx context.Context) (map[board.Register]int64, error) {
	values, err := service.device.Read(ctx, batteryThresholdRegisters...)
	if err != nil {
		return nil, err
	}
	if len(values) != len(batteryThresholdRegisters) {
		return nil, fmt.Errorf("battery-threshold response is incomplete")
	}
	result := make(map[board.Register]int64, len(values))
	for _, register := range batteryThresholdRegisters {
		value, ok := values[register]
		if !ok {
			return nil, fmt.Errorf("battery-threshold response omitted %s", register)
		}
		result[register] = value
	}
	return result, nil
}

func verifiedBatch(result board.ApplyResult, changes []board.Change) bool {
	if len(result.Changes) != len(changes) {
		return false
	}
	for index, change := range changes {
		observed := result.Changes[index]
		if observed.Change != change || !observed.Applied || !observed.Verified || observed.ReadBack != change.Value {
			return false
		}
	}
	return true
}
