// Package registeredit owns server-authoritative temporary register changes.
package registeredit

import (
	"errors"
	"fmt"

	"github.com/sinoia/lifepowerctl/internal/board"
)

var (
	ErrNotEditable     = errors.New("register is not eligible for generic temporary editing")
	ErrInvalidProposal = errors.New("invalid register proposal")
)

const (
	ShutdownDelayTicksPerSecond = int64(8)
	ShutdownDelayMinimumTicks   = int64(40)
	ShutdownDelayMaximumTicks   = int64(120)
)

// Eligibility is derived from trusted catalogue metadata, never browser
// fields. Editing begins narrowly and expands only when advanced constraints
// have executable validation and reviewed failure behaviour.
type Eligibility struct {
	Editable bool
	Reason   string
}

func EditEligibility(definition board.Definition) Eligibility {
	switch {
	case definition.Access != board.AccessReadWrite:
		return Eligibility{Reason: "read-only register"}
	case definition.Owner != board.OwnerNone:
		return Eligibility{Reason: "managed by " + string(definition.Owner)}
	case definition.Register == "AUTO_BOOT" && definition.SafetyClass == board.SafetyAdvanced:
		return Eligibility{Editable: true}
	case definition.SafetyClass != board.SafetyOrdinary:
		return Eligibility{Reason: "requires a specialised " + string(definition.SafetyClass) + " workflow"}
	default:
		return Eligibility{Editable: true}
	}
}

// Proposal contains only user intent plus the exact raw value that the user
// observed. Address, width, conversion, access, ownership, and safety policy
// are always recovered from the immutable catalogue.
type Proposal struct {
	Register            board.Register
	ExpectedRaw         int64
	ProposedEngineering int64
}

type ValidatedProposal struct {
	Definition          board.Definition
	ExpectedRaw         int64
	ProposedRaw         int64
	ProposedEngineering int64
}

func ValidateProposal(catalogue board.Catalog, registerSet int, proposal Proposal) (ValidatedProposal, error) {
	definition, ok := catalogue.Find(registerSet, proposal.Register)
	if !ok {
		return ValidatedProposal{}, fmt.Errorf("%w: unknown register %s for register set %d", ErrInvalidProposal, proposal.Register, registerSet)
	}
	eligibility := EditEligibility(definition)
	if !eligibility.Editable {
		return ValidatedProposal{}, fmt.Errorf("%w: %s: %s", ErrNotEditable, proposal.Register, eligibility.Reason)
	}
	if _, err := definition.EngineeringValue(proposal.ExpectedRaw); err != nil {
		return ValidatedProposal{}, fmt.Errorf("%w: expected raw value: %v", ErrInvalidProposal, err)
	}
	proposedRaw, err := definition.RawValue(proposal.ProposedEngineering)
	if err != nil {
		return ValidatedProposal{}, fmt.Errorf("%w: proposed value: %v", ErrInvalidProposal, err)
	}
	roundTrip, err := definition.EngineeringValue(proposedRaw)
	if err != nil || roundTrip != proposal.ProposedEngineering {
		return ValidatedProposal{}, fmt.Errorf("%w: proposed value cannot be represented exactly", ErrInvalidProposal)
	}
	if len(definition.Choices) > 0 && !containsChoice(definition.Choices, proposedRaw) {
		return ValidatedProposal{}, fmt.Errorf("%w: proposed value is not a documented choice", ErrInvalidProposal)
	}
	if proposedRaw == proposal.ExpectedRaw {
		return ValidatedProposal{}, fmt.Errorf("%w: proposed value is unchanged", ErrInvalidProposal)
	}
	return ValidatedProposal{
		Definition: definition, ExpectedRaw: proposal.ExpectedRaw,
		ProposedRaw: proposedRaw, ProposedEngineering: proposal.ProposedEngineering,
	}, nil
}

// ValidateShutdownDelayProposal is the purpose-specific shutdown-delay policy. The
// owner-facing workflow accepts whole approximate seconds from 5 through 15,
// represented exactly as eight board task ticks per second. This deliberately
// does not widen generic edit eligibility for daemon-owned registers.
func ValidateShutdownDelayProposal(catalogue board.Catalog, registerSet int, proposal Proposal) (ValidatedProposal, error) {
	if proposal.Register != "SHDN_DELAY" {
		return ValidatedProposal{}, fmt.Errorf("%w: shutdown-delay workflow cannot edit %s", ErrNotEditable, proposal.Register)
	}
	definition, ok := catalogue.Find(registerSet, proposal.Register)
	if !ok || definition.Access != board.AccessReadWrite || definition.Owner != board.OwnerDaemon || definition.SafetyClass != board.SafetyAdvanced {
		return ValidatedProposal{}, fmt.Errorf("%w: SHDN_DELAY metadata does not match the reviewed workflow", ErrNotEditable)
	}
	if proposal.ExpectedRaw < *definition.RawMinimum || proposal.ExpectedRaw > *definition.RawMaximum {
		return ValidatedProposal{}, fmt.Errorf("%w: expected tick value is outside catalogue bounds", ErrInvalidProposal)
	}
	proposed := proposal.ProposedEngineering
	if proposed < ShutdownDelayMinimumTicks || proposed > ShutdownDelayMaximumTicks || proposed%ShutdownDelayTicksPerSecond != 0 {
		return ValidatedProposal{}, fmt.Errorf("%w: shutdown delay must be a whole second from 5 through 15 seconds", ErrInvalidProposal)
	}
	if proposed == proposal.ExpectedRaw {
		return ValidatedProposal{}, fmt.Errorf("%w: proposed value is unchanged", ErrInvalidProposal)
	}
	return ValidatedProposal{Definition: definition, ExpectedRaw: proposal.ExpectedRaw, ProposedRaw: proposed, ProposedEngineering: proposed}, nil
}

func containsChoice(choices []board.Choice, raw int64) bool {
	for _, choice := range choices {
		if choice.Value == raw {
			return true
		}
	}
	return false
}
