package registeredit

import (
	"context"
	"errors"
	"fmt"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
)

var (
	ErrStaleState       = errors.New("register changed since it was reviewed")
	ErrUnsupportedBoard = errors.New("board identity does not match the reviewed proposal")
	ErrMutationFailed   = errors.New("temporary register change failed")
	ErrUncertain        = errors.New("temporary register change outcome is uncertain")
)

type Device interface {
	Detect(context.Context) (board.Identity, error)
	Read(context.Context, ...board.Register) (map[board.Register]int64, error)
	Apply(context.Context, []board.Change) (board.ApplyResult, error)
}

type Outcome string

const (
	OutcomeApplied             Outcome = "applied"
	OutcomeRejectedStale       Outcome = "rejected-stale"
	OutcomeFailedBeforeWrite   Outcome = "failed-before-write"
	OutcomeReconciledProposed  Outcome = "reconciled-proposed"
	OutcomeReconciledDifferent Outcome = "reconciled-different"
	OutcomeRolledBack          Outcome = "rolled-back"
	OutcomeUncertain           Outcome = "uncertain"
)

type Result struct {
	Outcome           Outcome
	Identity          board.Identity
	Register          board.Register
	ExpectedRaw       int64
	ProposedRaw       int64
	FreshRaw          *int64
	ActualRaw         *int64
	ApplyResult       board.ApplyResult
	RollbackAttempted bool
	RollbackResult    board.ApplyResult
}

type Service struct {
	device      Device
	catalogue   board.Catalog
	coordinator *board.Coordinator
	audit       activityRecorder
	constraints *ConstraintSet
}

type activityRecorder interface {
	Record(activity.Kind, string, string, any)
}

func NewService(device Device, catalogue board.Catalog, coordinator *board.Coordinator, audit activityRecorder) (*Service, error) {
	if device == nil || coordinator == nil || len(catalogue.Definitions()) == 0 {
		return nil, fmt.Errorf("register editor requires a device, catalogue, and coordinator")
	}
	constraints, err := NewConstraintSet(catalogue)
	if err != nil {
		return nil, fmt.Errorf("register editor constraints: %w", err)
	}
	return &Service{device: device, catalogue: catalogue, coordinator: coordinator, audit: audit, constraints: constraints}, nil
}

func (service *Service) Validate(registerSet int, proposal Proposal) (ValidatedProposal, error) {
	return ValidateProposal(service.catalogue, registerSet, proposal)
}

func (service *Service) ValidateShutdownDelay(registerSet int, proposal Proposal) (ValidatedProposal, error) {
	return ValidateShutdownDelayProposal(service.catalogue, registerSet, proposal)
}

// ApplyTemporary holds one hardware lease across fresh validation, mutation,
// read-back, and any reconciliation read. It never invokes flash persistence.
func (service *Service) ApplyTemporary(ctx context.Context, registerSet int, proposal Proposal) (Result, error) {
	validated, err := ValidateProposal(service.catalogue, registerSet, proposal)
	if err != nil {
		return Result{}, err
	}
	return service.applyValidated(ctx, registerSet, validated, false)
}

func (service *Service) ApplyShutdownDelayTemporary(ctx context.Context, registerSet int, proposal Proposal) (Result, error) {
	validated, err := ValidateShutdownDelayProposal(service.catalogue, registerSet, proposal)
	if err != nil {
		return Result{}, err
	}
	return service.applyValidated(ctx, registerSet, validated, true)
}

func (service *Service) applyValidated(ctx context.Context, registerSet int, validated ValidatedProposal, rollbackUnexpected bool) (Result, error) {
	result := Result{
		Register: validated.Definition.Register, ExpectedRaw: validated.ExpectedRaw,
		ProposedRaw: validated.ProposedRaw,
	}
	err := service.coordinator.WithLease(ctx, board.OperationRegisterWrite, string(validated.Definition.Register), func() error {
		service.recordAttempt(validated)
		defer service.recordObservation(&result, validated)
		identity, detectErr := service.device.Detect(ctx)
		result.Identity = identity
		if detectErr != nil {
			result.Outcome = OutcomeFailedBeforeWrite
			return fmt.Errorf("detect board before temporary change: %w", detectErr)
		}
		if identity.RegisterSet != registerSet {
			result.Outcome = OutcomeFailedBeforeWrite
			return fmt.Errorf("%w: got register set %d, want %d", ErrUnsupportedBoard, identity.RegisterSet, registerSet)
		}
		values, readErr := service.device.Read(ctx, validated.Definition.Register)
		if readErr != nil {
			result.Outcome = OutcomeFailedBeforeWrite
			return fmt.Errorf("read fresh value before temporary change: %w", readErr)
		}
		fresh, ok := values[validated.Definition.Register]
		if !ok {
			result.Outcome = OutcomeFailedBeforeWrite
			return fmt.Errorf("read fresh value before temporary change: register missing from response")
		}
		result.FreshRaw = int64Pointer(fresh)
		if fresh != validated.ExpectedRaw {
			result.Outcome = OutcomeRejectedStale
			result.ActualRaw = int64Pointer(fresh)
			return fmt.Errorf("%w: expected %d, found %d", ErrStaleState, validated.ExpectedRaw, fresh)
		}

		applyResult, applyErr := service.device.Apply(ctx, []board.Change{{Register: validated.Definition.Register, Value: validated.ProposedRaw}})
		result.ApplyResult = applyResult
		if applyErr == nil && verifiedSingleChange(applyResult, validated.Definition.Register, validated.ProposedRaw) {
			result.Outcome = OutcomeApplied
			result.ActualRaw = int64Pointer(validated.ProposedRaw)
			return nil
		}
		if applyErr == nil {
			applyErr = fmt.Errorf("device returned an invalid verification result")
		}
		if rollbackUnexpected {
			return service.reconcileShutdownDelay(ctx, &result, validated, applyErr)
		}
		return service.reconcile(ctx, &result, validated.Definition.Register, validated.ProposedRaw, applyErr)
	})
	if err != nil && result.Outcome == "" {
		result.Outcome = OutcomeFailedBeforeWrite
	}
	return result, err
}

type attemptDetails struct {
	Register            board.Register `json:"register"`
	Address             string         `json:"address"`
	RegisterSet         int            `json:"register_set"`
	SourceRevision      string         `json:"source_revision"`
	ExpectedRaw         int64          `json:"expected_raw"`
	ProposedRaw         int64          `json:"proposed_raw"`
	ProposedEngineering int64          `json:"proposed_engineering"`
	Persistence         string         `json:"persistence"`
}

type observationDetails struct {
	Register          board.Register `json:"register"`
	Address           string         `json:"address"`
	RegisterSet       int            `json:"register_set"`
	SourceRevision    string         `json:"source_revision"`
	ExpectedRaw       int64          `json:"expected_raw"`
	ProposedRaw       int64          `json:"proposed_raw"`
	FreshRaw          *int64         `json:"fresh_raw,omitempty"`
	ActualRaw         *int64         `json:"actual_raw,omitempty"`
	Outcome           Outcome        `json:"outcome"`
	WriteResults      int            `json:"write_results"`
	AppliedResults    int            `json:"applied_results"`
	VerifiedResults   int            `json:"verified_results"`
	RollbackAttempted bool           `json:"rollback_attempted"`
	RollbackVerified  bool           `json:"rollback_verified"`
	Persistence       string         `json:"persistence"`
}

func (service *Service) recordAttempt(proposal ValidatedProposal) {
	if service.audit == nil {
		return
	}
	service.audit.Record(activity.RegisterChangeAttempted, activity.ActorAdmin, string(proposal.Definition.Register), attemptDetails{
		Register: proposal.Definition.Register, Address: fmt.Sprintf("0x%02x", proposal.Definition.Address),
		RegisterSet: proposal.Definition.RegisterSet, SourceRevision: proposal.Definition.Source.Revision,
		ExpectedRaw: proposal.ExpectedRaw, ProposedRaw: proposal.ProposedRaw,
		ProposedEngineering: proposal.ProposedEngineering, Persistence: "temporary-only",
	})
}

func (service *Service) recordObservation(result *Result, proposal ValidatedProposal) {
	if service.audit == nil {
		return
	}
	applied, verified := 0, 0
	for _, change := range result.ApplyResult.Changes {
		if change.Applied {
			applied++
		}
		if change.Verified {
			verified++
		}
	}
	service.audit.Record(activity.RegisterChangeObserved, activity.ActorAdmin, string(proposal.Definition.Register), observationDetails{
		Register: proposal.Definition.Register, Address: fmt.Sprintf("0x%02x", proposal.Definition.Address),
		RegisterSet: proposal.Definition.RegisterSet, SourceRevision: proposal.Definition.Source.Revision,
		ExpectedRaw: proposal.ExpectedRaw, ProposedRaw: proposal.ProposedRaw, FreshRaw: result.FreshRaw,
		ActualRaw: result.ActualRaw, Outcome: result.Outcome, WriteResults: len(result.ApplyResult.Changes),
		AppliedResults: applied, VerifiedResults: verified, Persistence: "temporary-only",
		RollbackAttempted: result.RollbackAttempted, RollbackVerified: verifiedSingleChange(result.RollbackResult, proposal.Definition.Register, proposal.ExpectedRaw),
	})
}

func (service *Service) reconcileShutdownDelay(ctx context.Context, result *Result, proposal ValidatedProposal, applyErr error) error {
	values, readErr := service.device.Read(ctx, proposal.Definition.Register)
	if readErr != nil {
		result.Outcome = OutcomeUncertain
		return fmt.Errorf("%w: apply error: %v; reconciliation read: %v", ErrUncertain, applyErr, readErr)
	}
	actual, ok := values[proposal.Definition.Register]
	if !ok {
		result.Outcome = OutcomeUncertain
		return fmt.Errorf("%w: reconciliation response omitted %s", ErrUncertain, proposal.Definition.Register)
	}
	result.ActualRaw = int64Pointer(actual)
	if actual == proposal.ProposedRaw {
		result.Outcome = OutcomeReconciledProposed
		return fmt.Errorf("%w: %v", ErrMutationFailed, applyErr)
	}
	if actual == proposal.ExpectedRaw {
		result.Outcome = OutcomeReconciledDifferent
		return fmt.Errorf("%w: %v", ErrMutationFailed, applyErr)
	}
	result.RollbackAttempted = true
	rollback, rollbackErr := service.device.Apply(ctx, []board.Change{{Register: proposal.Definition.Register, Value: proposal.ExpectedRaw}})
	result.RollbackResult = rollback
	if rollbackErr == nil && verifiedSingleChange(rollback, proposal.Definition.Register, proposal.ExpectedRaw) {
		result.Outcome = OutcomeRolledBack
		result.ActualRaw = int64Pointer(proposal.ExpectedRaw)
		return fmt.Errorf("%w: %v; unexpected value %d restored", ErrMutationFailed, applyErr, actual)
	}
	final, finalErr := service.device.Read(ctx, proposal.Definition.Register)
	if finalErr != nil {
		result.Outcome = OutcomeUncertain
		result.ActualRaw = nil
		return fmt.Errorf("%w: apply error: %v; rollback error: %v; final read: %v", ErrUncertain, applyErr, rollbackErr, finalErr)
	}
	finalValue, ok := final[proposal.Definition.Register]
	if !ok {
		result.Outcome = OutcomeUncertain
		result.ActualRaw = nil
		return fmt.Errorf("%w: final response omitted %s", ErrUncertain, proposal.Definition.Register)
	}
	result.ActualRaw = int64Pointer(finalValue)
	result.Outcome = OutcomeReconciledDifferent
	if finalValue == proposal.ProposedRaw {
		result.Outcome = OutcomeReconciledProposed
	}
	return fmt.Errorf("%w: apply error: %v; rollback error: %v", ErrMutationFailed, applyErr, rollbackErr)
}

func (service *Service) reconcile(ctx context.Context, result *Result, register board.Register, proposedRaw int64, applyErr error) error {
	values, readErr := service.device.Read(ctx, register)
	if readErr != nil {
		result.Outcome = OutcomeUncertain
		return fmt.Errorf("%w: apply error: %v; reconciliation read: %v", ErrUncertain, applyErr, readErr)
	}
	actual, ok := values[register]
	if !ok {
		result.Outcome = OutcomeUncertain
		return fmt.Errorf("%w: apply error: %v; reconciliation response omitted %s", ErrUncertain, applyErr, register)
	}
	result.ActualRaw = int64Pointer(actual)
	if actual == proposedRaw {
		result.Outcome = OutcomeReconciledProposed
	} else {
		result.Outcome = OutcomeReconciledDifferent
	}
	return fmt.Errorf("%w: %v", ErrMutationFailed, applyErr)
}

func verifiedSingleChange(result board.ApplyResult, register board.Register, raw int64) bool {
	if len(result.Changes) != 1 {
		return false
	}
	change := result.Changes[0]
	return change.Change.Register == register && change.Change.Value == raw &&
		change.Applied && change.Verified && change.ReadBack == raw
}

func int64Pointer(value int64) *int64 { return &value }
