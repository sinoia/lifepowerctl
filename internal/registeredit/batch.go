package registeredit

import (
	"errors"
	"fmt"
	"slices"

	"github.com/sinoia/lifepowerctl/internal/board"
)

var ErrInvalidBatch = errors.New("invalid multi-register proposal")

var batteryThresholdRegisters = []board.Register{"VBAT_MIN", "VBAT_SHDN", "VBAT_BOOT"}

type BatchProposal struct {
	RegisterSet    int
	ExpectedValues map[board.Register]int64
	ProposedValues map[board.Register]int64
}

type BatchPlan struct {
	RegisterSet int
	Before      map[board.Register]int64
	After       map[board.Register]int64
	Changes     []board.Change
	Constraint  string
}

type BatchPlanner struct {
	catalogue   board.Catalog
	constraints *ConstraintSet
}

func NewBatchPlanner(catalogue board.Catalog) (*BatchPlanner, error) {
	constraints, err := NewConstraintSet(catalogue)
	if err != nil {
		return nil, err
	}
	return &BatchPlanner{catalogue: catalogue, constraints: constraints}, nil
}

// PlanBatteryThresholds plans only the reviewed three-threshold group. The
// Values are the exact scaled millivolt integers exposed by the vendor CLI;
// raw register bytes are intentionally not inferred. Complete before and after
// snapshots are required even when one value changes.
// Each selected intermediate state must satisfy the executable ordering rule.
func (planner *BatchPlanner) PlanBatteryThresholds(proposal BatchProposal) (BatchPlan, error) {
	if proposal.RegisterSet != 7 {
		return BatchPlan{}, fmt.Errorf("%w: unsupported register set %d", ErrInvalidBatch, proposal.RegisterSet)
	}
	before, err := planner.validateBatterySnapshot("expected", proposal.ExpectedValues)
	if err != nil {
		return BatchPlan{}, err
	}
	beforeViolations, err := planner.constraints.ValidateComplete(before)
	if err != nil {
		return BatchPlan{}, fmt.Errorf("%w: validate expected configuration: %v", ErrInvalidBatch, err)
	}
	if len(beforeViolations) != 0 {
		return BatchPlan{}, fmt.Errorf("%w: expected configuration violates %s", ErrInvalidBatch, beforeViolations[0].ID)
	}
	after, err := planner.validateBatterySnapshot("proposed", proposal.ProposedValues)
	if err != nil {
		return BatchPlan{}, err
	}
	violations, err := planner.constraints.ValidateComplete(after)
	if err != nil {
		return BatchPlan{}, fmt.Errorf("%w: validate proposed configuration: %v", ErrInvalidBatch, err)
	}
	if len(violations) != 0 {
		return BatchPlan{}, fmt.Errorf("%w: proposed configuration violates %s", ErrInvalidBatch, violations[0].ID)
	}

	pending := make([]board.Register, 0, len(batteryThresholdRegisters))
	for _, register := range batteryThresholdRegisters {
		if before[register] != after[register] {
			pending = append(pending, register)
		}
	}
	if len(pending) == 0 {
		return BatchPlan{}, fmt.Errorf("%w: proposed configuration is unchanged", ErrInvalidBatch)
	}
	slices.SortFunc(pending, func(left, right board.Register) int {
		leftDefinition, _ := planner.catalogue.Find(7, left)
		rightDefinition, _ := planner.catalogue.Find(7, right)
		return int(leftDefinition.Address) - int(rightDefinition.Address)
	})

	current := cloneValues(before)
	changes := make([]board.Change, 0, len(pending))
	for len(pending) > 0 {
		selected := -1
		for index, register := range pending {
			candidate := cloneValues(current)
			candidate[register] = after[register]
			candidateViolations, validationErr := planner.constraints.ValidateComplete(candidate)
			if validationErr == nil && len(candidateViolations) == 0 {
				selected = index
				break
			}
		}
		if selected < 0 {
			return BatchPlan{}, fmt.Errorf("%w: no safe intermediate ordering exists", ErrInvalidBatch)
		}
		register := pending[selected]
		current[register] = after[register]
		changes = append(changes, board.Change{Register: register, Value: after[register]})
		pending = append(pending[:selected], pending[selected+1:]...)
	}
	return BatchPlan{RegisterSet: 7, Before: before, After: after, Changes: changes, Constraint: "battery-threshold-order"}, nil
}

func (planner *BatchPlanner) validateBatterySnapshot(label string, values map[board.Register]int64) (map[board.Register]int64, error) {
	if len(values) != len(batteryThresholdRegisters) {
		return nil, fmt.Errorf("%w: %s snapshot must contain exactly three battery thresholds", ErrInvalidBatch, label)
	}
	result := make(map[board.Register]int64, len(values))
	for _, register := range batteryThresholdRegisters {
		value, ok := values[register]
		if !ok {
			return nil, fmt.Errorf("%w: %s snapshot is missing %s", ErrInvalidBatch, label, register)
		}
		definition, _ := planner.catalogue.Find(7, register)
		minimum, minimumErr := definition.EngineeringValue(*definition.RawMinimum)
		maximum, maximumErr := definition.EngineeringValue(*definition.RawMaximum)
		if minimumErr != nil || maximumErr != nil || value < minimum || value > maximum {
			return nil, fmt.Errorf("%w: %s %s scaled value is outside catalogue bounds", ErrInvalidBatch, label, register)
		}
		result[register] = value
	}
	return result, nil
}

func cloneValues(values map[board.Register]int64) map[board.Register]int64 {
	result := make(map[board.Register]int64, len(values))
	for register, value := range values {
		result[register] = value
	}
	return result
}
