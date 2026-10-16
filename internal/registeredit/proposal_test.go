package registeredit

import (
	"errors"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/board"
)

func TestSingleRegisterEditEligibilityIsExplicitlyBounded(t *testing.T) {
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	var editable []board.Register
	for _, definition := range catalogue.Definitions() {
		if EditEligibility(definition).Editable {
			editable = append(editable, definition.Register)
		}
	}
	if len(editable) != 2 || editable[0] != "LED_STATE" || editable[1] != "AUTO_BOOT" {
		t.Fatalf("editable registers = %v, want [LED_STATE AUTO_BOOT]", editable)
	}
}

func TestValidateProposalRecoversTrustedDefinition(t *testing.T) {
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	validated, err := ValidateProposal(catalogue, 7, Proposal{
		Register: "LED_STATE", ExpectedRaw: 1, ProposedEngineering: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if validated.Definition.Address != 0x02 || validated.ProposedRaw != 2 || validated.ExpectedRaw != 1 {
		t.Fatalf("validated proposal = %+v", validated)
	}
}

func TestValidateProposalRejectsUnsafeOrInvalidInput(t *testing.T) {
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		proposal Proposal
		want     error
	}{
		{name: "unknown", proposal: Proposal{Register: "NOPE", ProposedEngineering: 1}, want: ErrInvalidProposal},
		{name: "read only", proposal: Proposal{Register: "VBAT", ExpectedRaw: 1, ProposedEngineering: 2}, want: ErrNotEditable},
		{name: "other advanced", proposal: Proposal{Register: "VOUT_MAX", ExpectedRaw: 5449, ProposedEngineering: 5450}, want: ErrNotEditable},
		{name: "owned", proposal: Proposal{Register: "PI_BOOT_TO", ExpectedRaw: 30, ProposedEngineering: 300}, want: ErrNotEditable},
		{name: "operational", proposal: Proposal{Register: "CFG_WRITE", ExpectedRaw: 0, ProposedEngineering: 70}, want: ErrNotEditable},
		{name: "invalid choice", proposal: Proposal{Register: "LED_STATE", ExpectedRaw: 1, ProposedEngineering: 4}, want: ErrInvalidProposal},
		{name: "unchanged", proposal: Proposal{Register: "LED_STATE", ExpectedRaw: 1, ProposedEngineering: 1}, want: ErrInvalidProposal},
		{name: "wrong set", proposal: Proposal{Register: "LED_STATE", ExpectedRaw: 1, ProposedEngineering: 2}, want: ErrInvalidProposal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registerSet := 7
			if test.name == "wrong set" {
				registerSet = 8
			}
			_, err := ValidateProposal(catalogue, registerSet, test.proposal)
			if !errors.Is(err, test.want) {
				t.Fatalf("ValidateProposal() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateAutoBootProposalUsesOnlyReviewedChoices(t *testing.T) {
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	validated, err := ValidateProposal(catalogue, 7, Proposal{Register: "AUTO_BOOT", ExpectedRaw: 0, ProposedEngineering: 4})
	if err != nil || validated.ProposedRaw != 4 {
		t.Fatalf("ValidateProposal(AUTO_BOOT) = %+v, %v", validated, err)
	}
	if _, err := ValidateProposal(catalogue, 7, Proposal{Register: "AUTO_BOOT", ExpectedRaw: 0, ProposedEngineering: 8}); !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("invalid AUTO_BOOT choice error = %v", err)
	}
}

func TestShutdownDelayProposalPolicy(t *testing.T) {
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	valid, err := ValidateShutdownDelayProposal(catalogue, 7, Proposal{Register: "SHDN_DELAY", ExpectedRaw: 40, ProposedEngineering: 96})
	if err != nil || valid.ProposedRaw != 96 {
		t.Fatalf("ValidateShutdownDelayProposal() = %+v, %v", valid, err)
	}
	for _, proposal := range []Proposal{
		{Register: "CFG_WRITE", ExpectedRaw: 0, ProposedEngineering: 96},
		{Register: "SHDN_DELAY", ExpectedRaw: 40, ProposedEngineering: 39},
		{Register: "SHDN_DELAY", ExpectedRaw: 40, ProposedEngineering: 97},
		{Register: "SHDN_DELAY", ExpectedRaw: 40, ProposedEngineering: 128},
		{Register: "SHDN_DELAY", ExpectedRaw: 40, ProposedEngineering: 248},
		{Register: "SHDN_DELAY", ExpectedRaw: 40, ProposedEngineering: 40},
	} {
		if _, err := ValidateShutdownDelayProposal(catalogue, 7, proposal); err == nil {
			t.Fatalf("ValidateShutdownDelayProposal(%+v) unexpectedly succeeded", proposal)
		}
	}
}
