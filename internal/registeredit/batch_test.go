package registeredit

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/board"
)

func testBatchPlanner(t *testing.T) (*BatchPlanner, *ConstraintSet) {
	t.Helper()
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	planner, err := NewBatchPlanner(catalogue)
	if err != nil {
		t.Fatal(err)
	}
	return planner, planner.constraints
}

func batteryValues(minimum, shutdown, boot int64) map[board.Register]int64 {
	return map[board.Register]int64{"VBAT_MIN": minimum, "VBAT_SHDN": shutdown, "VBAT_BOOT": boot}
}

func TestBatteryPlannerChoosesSafeRisingAndFallingOrders(t *testing.T) {
	planner, _ := testBatchPlanner(t)
	tests := []struct {
		name   string
		before map[board.Register]int64
		after  map[board.Register]int64
		want   []board.Register
	}{
		{name: "rising", before: batteryValues(1, 2, 3), after: batteryValues(4, 5, 6), want: []board.Register{"VBAT_BOOT", "VBAT_SHDN", "VBAT_MIN"}},
		{name: "falling", before: batteryValues(4, 5, 6), after: batteryValues(1, 2, 3), want: []board.Register{"VBAT_MIN", "VBAT_SHDN", "VBAT_BOOT"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := planner.PlanBatteryThresholds(BatchProposal{RegisterSet: 7, ExpectedValues: test.before, ProposedValues: test.after})
			if err != nil {
				t.Fatal(err)
			}
			got := make([]board.Register, len(plan.Changes))
			for index, change := range plan.Changes {
				got[index] = change.Register
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("write order = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBatteryPlannerRejectsIncompleteInvalidOrUnchangedProposal(t *testing.T) {
	planner, _ := testBatchPlanner(t)
	tests := []BatchProposal{
		{RegisterSet: 8, ExpectedValues: batteryValues(1, 2, 3), ProposedValues: batteryValues(1, 2, 4)},
		{RegisterSet: 7, ExpectedValues: map[board.Register]int64{"VBAT_MIN": 1}, ProposedValues: batteryValues(1, 2, 4)},
		{RegisterSet: 7, ExpectedValues: batteryValues(1, 2, 3), ProposedValues: batteryValues(1, 1, 3)},
		{RegisterSet: 7, ExpectedValues: batteryValues(1, 2, 3), ProposedValues: batteryValues(1, 2, 3)},
	}
	for _, proposal := range tests {
		if _, err := planner.PlanBatteryThresholds(proposal); !errors.Is(err, ErrInvalidBatch) {
			t.Fatalf("PlanBatteryThresholds(%+v) error = %v", proposal, err)
		}
	}
}

func TestBatteryPlannerExhaustivelyPreservesEveryIntermediateOrdering(t *testing.T) {
	planner, constraints := testBatchPlanner(t)
	var valid []map[board.Register]int64
	for minimum := int64(0); minimum <= 5; minimum++ {
		for shutdown := int64(0); shutdown <= 5; shutdown++ {
			for boot := int64(0); boot <= 5; boot++ {
				if minimum < shutdown && shutdown < boot {
					valid = append(valid, batteryValues(minimum, shutdown, boot))
				}
			}
		}
	}
	for _, before := range valid {
		for _, after := range valid {
			if reflect.DeepEqual(before, after) {
				continue
			}
			plan, err := planner.PlanBatteryThresholds(BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
			if err != nil {
				t.Fatalf("plan %v -> %v: %v", before, after, err)
			}
			current := cloneValues(before)
			for _, change := range plan.Changes {
				current[change.Register] = change.Value
				violations, validationErr := constraints.ValidateComplete(current)
				if validationErr != nil || len(violations) != 0 {
					t.Fatalf("unsafe intermediate %v in %v -> %v: %+v, %v", current, before, after, violations, validationErr)
				}
			}
			if !reflect.DeepEqual(current, after) {
				t.Fatalf("final state = %v, want %v", current, after)
			}
		}
	}
}
