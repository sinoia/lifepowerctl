package registeredit

import (
	"testing"

	"github.com/sinoia/lifepowerctl/internal/board"
)

func testConstraintSet(t *testing.T) (*ConstraintSet, board.Catalog) {
	t.Helper()
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	set, err := NewConstraintSet(catalogue)
	if err != nil {
		t.Fatal(err)
	}
	return set, catalogue
}

func TestConstraintVocabularyCoversEveryCatalogueAttachment(t *testing.T) {
	set, catalogue := testConstraintSet(t)
	want := make(map[string]bool)
	for _, definition := range catalogue.Definitions() {
		for _, id := range definition.ConstraintIDs {
			want[id] = true
		}
	}
	for _, rule := range set.Definitions() {
		if !want[rule.ID] {
			t.Errorf("constraint %s is not attached to the catalogue", rule.ID)
		}
		delete(want, rule.ID)
	}
	if len(want) != 0 {
		t.Fatalf("catalogue constraints without definitions: %v", want)
	}
}

func TestOnlyBatteryOrderingIsInitiallyExecutable(t *testing.T) {
	set, _ := testConstraintSet(t)
	for _, rule := range set.Definitions() {
		if rule.Executable != (rule.ID == "battery-threshold-order") {
			t.Errorf("constraint %s executable = %t", rule.ID, rule.Executable)
		}
	}
}

func TestBatteryThresholdOrdering(t *testing.T) {
	set, catalogue := testConstraintSet(t)
	values := make(map[board.Register]int64)
	for _, register := range []board.Register{"VBAT_MIN", "VBAT_SHDN", "VBAT_BOOT"} {
		definition, _ := catalogue.Find(7, register)
		values[register] = *definition.RawDefault
	}
	violations, err := set.ValidateComplete(values)
	if err != nil || len(violations) != 0 {
		t.Fatalf("defaults = %+v, %v", violations, err)
	}

	tests := []struct {
		name     string
		register board.Register
		value    int64
	}{
		{name: "minimum equals shutdown", register: "VBAT_MIN", value: values["VBAT_SHDN"]},
		{name: "shutdown below minimum", register: "VBAT_SHDN", value: values["VBAT_MIN"] - 1},
		{name: "boot equals shutdown", register: "VBAT_BOOT", value: values["VBAT_SHDN"]},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := make(map[board.Register]int64, len(values))
			for register, value := range values {
				changed[register] = value
			}
			changed[test.register] = test.value
			violations, err := set.ValidateComplete(changed)
			if err != nil || len(violations) != 1 || violations[0].ID != "battery-threshold-order" || violations[0].Provenance != ProvenanceManufacturerDerived {
				t.Fatalf("ValidateComplete() = %+v, %v", violations, err)
			}
		})
	}
}

func TestConstraintValidationRefusesMissingOrOutOfRangeDependencies(t *testing.T) {
	set, _ := testConstraintSet(t)
	if _, err := set.ValidateComplete(map[board.Register]int64{}); err == nil {
		t.Fatal("missing configuration was accepted")
	}
	values := map[board.Register]int64{"VBAT_MIN": 4665, "VBAT_SHDN": 4829, "VBAT_BOOT": 9000}
	if _, err := set.ValidateComplete(values); err == nil {
		t.Fatal("out-of-range dependency was accepted")
	}
}
