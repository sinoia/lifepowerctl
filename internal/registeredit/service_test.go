package registeredit

import (
	"context"
	"errors"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
)

func newTestService(t *testing.T, device Device) *Service {
	t.Helper()
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(device, catalogue, board.NewCoordinator(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func ledProposal() Proposal {
	return Proposal{Register: "LED_STATE", ExpectedRaw: 1, ProposedEngineering: 2}
}

func TestApplyTemporaryVerifiesOneChange(t *testing.T) {
	fake := board.NewFake(board.Identity{RegisterSet: 7}, map[board.Register]int64{"LED_STATE": 1})
	result, err := newTestService(t, fake).ApplyTemporary(context.Background(), 7, ledProposal())
	if err != nil || result.Outcome != OutcomeApplied || result.ActualRaw == nil || *result.ActualRaw != 2 {
		t.Fatalf("ApplyTemporary() = %+v, %v", result, err)
	}
	values, _ := fake.Read(context.Background(), "LED_STATE")
	if values["LED_STATE"] != 2 {
		t.Fatalf("stored LED_STATE = %d", values["LED_STATE"])
	}
}

func TestApplyTemporaryVerifiesAutoBootChange(t *testing.T) {
	fake := board.NewFake(board.Identity{RegisterSet: 7}, map[board.Register]int64{"AUTO_BOOT": 0})
	result, err := newTestService(t, fake).ApplyTemporary(context.Background(), 7, Proposal{Register: "AUTO_BOOT", ExpectedRaw: 0, ProposedEngineering: 2})
	if err != nil || result.Outcome != OutcomeApplied || result.ActualRaw == nil || *result.ActualRaw != 2 {
		t.Fatalf("ApplyTemporary(AUTO_BOOT) = %+v, %v", result, err)
	}
}

func TestApplyShutdownDelayTemporaryVerified(t *testing.T) {
	fake := board.NewFake(board.Identity{RegisterSet: 7}, map[board.Register]int64{"SHDN_DELAY": 40})
	result, err := newTestService(t, fake).ApplyShutdownDelayTemporary(context.Background(), 7, Proposal{Register: "SHDN_DELAY", ExpectedRaw: 40, ProposedEngineering: 96})
	if err != nil || result.Outcome != OutcomeApplied || result.ActualRaw == nil || *result.ActualRaw != 96 {
		t.Fatalf("ApplyShutdownDelayTemporary() = %+v, %v", result, err)
	}
	values, _ := fake.Read(context.Background(), "SHDN_DELAY")
	if values["SHDN_DELAY"] != 96 {
		t.Fatalf("stored SHDN_DELAY = %d", values["SHDN_DELAY"])
	}
}

func TestApplyShutdownDelayTemporaryFailureMatrix(t *testing.T) {
	tests := []struct {
		name    string
		faults  board.Faults
		outcome Outcome
		actual  int64
	}{
		{name: "definite write failure", faults: board.Faults{FailWriteAt: 0, LoseConnectionAt: -1}, outcome: OutcomeReconciledDifferent, actual: 40},
		{name: "read-back mismatch", faults: board.Faults{FailWriteAt: -1, LoseConnectionAt: -1, CorruptReadBack: map[board.Register]int64{"SHDN_DELAY": 80}}, outcome: OutcomeReconciledProposed, actual: 96},
		{name: "connection loss", faults: board.Faults{FailWriteAt: -1, LoseConnectionAt: 0}, outcome: OutcomeReconciledDifferent, actual: 40},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := board.NewFake(board.Identity{RegisterSet: 7}, map[board.Register]int64{"SHDN_DELAY": 40})
			fake.SetFaults(test.faults)
			result, err := newTestService(t, fake).ApplyShutdownDelayTemporary(context.Background(), 7, Proposal{Register: "SHDN_DELAY", ExpectedRaw: 40, ProposedEngineering: 96})
			if !errors.Is(err, ErrMutationFailed) || result.Outcome != test.outcome || result.ActualRaw == nil || *result.ActualRaw != test.actual {
				t.Fatalf("ApplyShutdownDelayTemporary() = %+v, %v", result, err)
			}
		})
	}
}

type shutdownDelayPartialDevice struct {
	value        int64
	applies      int
	failRollback bool
}

func (device *shutdownDelayPartialDevice) Detect(context.Context) (board.Identity, error) {
	return board.Identity{RegisterSet: 7}, nil
}

func (device *shutdownDelayPartialDevice) Read(context.Context, ...board.Register) (map[board.Register]int64, error) {
	return map[board.Register]int64{"SHDN_DELAY": device.value}, nil
}

func (device *shutdownDelayPartialDevice) Apply(_ context.Context, changes []board.Change) (board.ApplyResult, error) {
	device.applies++
	change := changes[0]
	if device.applies == 1 {
		device.value = 88
		return board.ApplyResult{}, board.ErrCommunication
	}
	if device.failRollback {
		return board.ApplyResult{}, board.ErrCommunication
	}
	device.value = change.Value
	return board.ApplyResult{Changes: []board.ChangeResult{{Change: change, Applied: true, Verified: true, ReadBack: change.Value}}}, nil
}

func TestApplyShutdownDelayRollsBackUnexpectedKnownValue(t *testing.T) {
	device := &shutdownDelayPartialDevice{value: 40}
	result, err := newTestService(t, device).ApplyShutdownDelayTemporary(context.Background(), 7, Proposal{Register: "SHDN_DELAY", ExpectedRaw: 40, ProposedEngineering: 96})
	if !errors.Is(err, ErrMutationFailed) || result.Outcome != OutcomeRolledBack || !result.RollbackAttempted || result.ActualRaw == nil || *result.ActualRaw != 40 || device.applies != 2 {
		t.Fatalf("ApplyShutdownDelayTemporary() = %+v, %v; applies=%d", result, err, device.applies)
	}
}

func TestApplyShutdownDelayReportsFailedRollbackActualValue(t *testing.T) {
	device := &shutdownDelayPartialDevice{value: 40, failRollback: true}
	result, err := newTestService(t, device).ApplyShutdownDelayTemporary(context.Background(), 7, Proposal{Register: "SHDN_DELAY", ExpectedRaw: 40, ProposedEngineering: 96})
	if !errors.Is(err, ErrMutationFailed) || result.Outcome != OutcomeReconciledDifferent || !result.RollbackAttempted || result.ActualRaw == nil || *result.ActualRaw != 88 || device.applies != 2 {
		t.Fatalf("ApplyShutdownDelayTemporary() = %+v, %v; applies=%d", result, err, device.applies)
	}
}

func TestApplyTemporaryRejectsStaleStateWithoutWriting(t *testing.T) {
	fake := board.NewFake(board.Identity{RegisterSet: 7}, map[board.Register]int64{"LED_STATE": 1})
	fake.ExternalSet("LED_STATE", 3)
	result, err := newTestService(t, fake).ApplyTemporary(context.Background(), 7, ledProposal())
	if !errors.Is(err, ErrStaleState) || result.Outcome != OutcomeRejectedStale || result.ActualRaw == nil || *result.ActualRaw != 3 {
		t.Fatalf("ApplyTemporary() = %+v, %v", result, err)
	}
	values, _ := fake.Read(context.Background(), "LED_STATE")
	if values["LED_STATE"] != 3 {
		t.Fatalf("stale rejection wrote LED_STATE = %d", values["LED_STATE"])
	}
}

func TestApplyTemporaryRejectsChangedIdentityWithoutWriting(t *testing.T) {
	fake := board.NewFake(board.Identity{RegisterSet: 8}, map[board.Register]int64{"LED_STATE": 1})
	result, err := newTestService(t, fake).ApplyTemporary(context.Background(), 7, ledProposal())
	if !errors.Is(err, ErrUnsupportedBoard) || result.Outcome != OutcomeFailedBeforeWrite {
		t.Fatalf("ApplyTemporary() = %+v, %v", result, err)
	}
}

func TestApplyTemporaryReconcilesWriteFailure(t *testing.T) {
	tests := []struct {
		name    string
		faults  board.Faults
		outcome Outcome
		actual  int64
	}{
		{name: "failed before write", faults: board.Faults{FailWriteAt: 0, LoseConnectionAt: -1}, outcome: OutcomeReconciledDifferent, actual: 1},
		{name: "verification mismatch", faults: board.Faults{FailWriteAt: -1, LoseConnectionAt: -1, CorruptReadBack: map[board.Register]int64{"LED_STATE": 3}}, outcome: OutcomeReconciledProposed, actual: 2},
		{name: "connection loss", faults: board.Faults{FailWriteAt: -1, LoseConnectionAt: 0}, outcome: OutcomeReconciledDifferent, actual: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := board.NewFake(board.Identity{RegisterSet: 7}, map[board.Register]int64{"LED_STATE": 1})
			fake.SetFaults(test.faults)
			result, err := newTestService(t, fake).ApplyTemporary(context.Background(), 7, ledProposal())
			if !errors.Is(err, ErrMutationFailed) || result.Outcome != test.outcome || result.ActualRaw == nil || *result.ActualRaw != test.actual {
				t.Fatalf("ApplyTemporary() = %+v, %v", result, err)
			}
		})
	}
}

type uncertainDevice struct {
	readCount int
}

func (device *uncertainDevice) Detect(context.Context) (board.Identity, error) {
	return board.Identity{RegisterSet: 7}, nil
}

func (device *uncertainDevice) Read(context.Context, ...board.Register) (map[board.Register]int64, error) {
	device.readCount++
	if device.readCount == 1 {
		return map[board.Register]int64{"LED_STATE": 1}, nil
	}
	return nil, board.ErrCommunication
}

func (device *uncertainDevice) Apply(context.Context, []board.Change) (board.ApplyResult, error) {
	return board.ApplyResult{}, board.ErrCommunication
}

func TestApplyTemporaryPreservesUncertainOutcome(t *testing.T) {
	result, err := newTestService(t, &uncertainDevice{}).ApplyTemporary(context.Background(), 7, ledProposal())
	if !errors.Is(err, ErrUncertain) || result.Outcome != OutcomeUncertain || result.ActualRaw != nil {
		t.Fatalf("ApplyTemporary() = %+v, %v", result, err)
	}
}

type stageFailureDevice struct {
	detectErr error
	readErr   error
	reads     int
	applies   int
	malformed bool
}

func (device *stageFailureDevice) Detect(context.Context) (board.Identity, error) {
	return board.Identity{RegisterSet: 7}, device.detectErr
}

func (device *stageFailureDevice) Read(context.Context, ...board.Register) (map[board.Register]int64, error) {
	device.reads++
	if device.readErr != nil {
		return nil, device.readErr
	}
	return map[board.Register]int64{"LED_STATE": 1}, nil
}

func (device *stageFailureDevice) Apply(_ context.Context, changes []board.Change) (board.ApplyResult, error) {
	device.applies++
	if device.malformed {
		return board.ApplyResult{}, nil
	}
	change := changes[0]
	return board.ApplyResult{Changes: []board.ChangeResult{{Change: change, ReadBack: change.Value, Applied: true, Verified: true}}}, nil
}

func TestApplyTemporaryFailureBeforeWriteMatrix(t *testing.T) {
	tests := []struct {
		name   string
		device *stageFailureDevice
	}{
		{name: "identity", device: &stageFailureDevice{detectErr: board.ErrCommunication}},
		{name: "fresh read", device: &stageFailureDevice{readErr: board.ErrCommunication}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := newTestService(t, test.device).ApplyTemporary(context.Background(), 7, ledProposal())
			if err == nil || result.Outcome != OutcomeFailedBeforeWrite || test.device.applies != 0 {
				t.Fatalf("ApplyTemporary() = %+v, %v; applies=%d", result, err, test.device.applies)
			}
		})
	}
}

func TestApplyTemporaryReconcilesMalformedVerificationResult(t *testing.T) {
	device := &stageFailureDevice{malformed: true}
	result, err := newTestService(t, device).ApplyTemporary(context.Background(), 7, ledProposal())
	if !errors.Is(err, ErrMutationFailed) || result.Outcome != OutcomeReconciledDifferent || result.ActualRaw == nil || *result.ActualRaw != 1 || device.applies != 1 || device.reads != 2 {
		t.Fatalf("ApplyTemporary() = %+v, %v; reads=%d applies=%d", result, err, device.reads, device.applies)
	}
}

func TestApplyTemporaryCancellationWhileWaitingDoesNotTouchDevice(t *testing.T) {
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	coordinator := board.NewCoordinator()
	lease, err := coordinator.Acquire(context.Background(), board.OperationRead, "held for test")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	device := &stageFailureDevice{}
	service, err := NewService(device, catalogue, coordinator, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := service.ApplyTemporary(ctx, 7, ledProposal())
	if !errors.Is(err, context.Canceled) || result.Outcome != OutcomeFailedBeforeWrite || device.reads != 0 || device.applies != 0 {
		t.Fatalf("ApplyTemporary() = %+v, %v; reads=%d applies=%d", result, err, device.reads, device.applies)
	}
}

func TestInvalidProposalNeverTouchesHardware(t *testing.T) {
	device := &uncertainDevice{}
	proposal := ledProposal()
	proposal.ProposedEngineering = 9
	_, err := newTestService(t, device).ApplyTemporary(context.Background(), 7, proposal)
	if !errors.Is(err, ErrInvalidProposal) || device.readCount != 0 {
		t.Fatalf("ApplyTemporary() error = %v, read count = %d", err, device.readCount)
	}
}

type recordedActivity struct {
	kind        activity.Kind
	actor       string
	subject     string
	details     any
	leaseActive bool
}

type activitySpy struct {
	coordinator *board.Coordinator
	events      []recordedActivity
}

func (spy *activitySpy) Record(kind activity.Kind, actor, subject string, details any) {
	active, ok := spy.coordinator.Active()
	spy.events = append(spy.events, recordedActivity{
		kind: kind, actor: actor, subject: subject, details: details,
		leaseActive: ok && active.Kind == board.OperationRegisterWrite,
	})
}

func TestApplyTemporaryRecordsAttemptAndObservationInsideLease(t *testing.T) {
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	coordinator := board.NewCoordinator()
	spy := &activitySpy{coordinator: coordinator}
	fake := board.NewFake(board.Identity{RegisterSet: 7}, map[board.Register]int64{"LED_STATE": 1})
	service, err := NewService(fake, catalogue, coordinator, spy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyTemporary(context.Background(), 7, ledProposal()); err != nil {
		t.Fatal(err)
	}
	if len(spy.events) != 2 {
		t.Fatalf("events = %+v", spy.events)
	}
	if spy.events[0].kind != activity.RegisterChangeAttempted || spy.events[1].kind != activity.RegisterChangeObserved {
		t.Fatalf("event kinds = %q, %q", spy.events[0].kind, spy.events[1].kind)
	}
	for _, event := range spy.events {
		if event.actor != activity.ActorAdmin || event.subject != "LED_STATE" || !event.leaseActive {
			t.Fatalf("event = %+v", event)
		}
	}
	attempt, ok := spy.events[0].details.(attemptDetails)
	if !ok || attempt.Address != "0x02" || attempt.ExpectedRaw != 1 || attempt.ProposedRaw != 2 || attempt.Persistence != "temporary-only" {
		t.Fatalf("attempt details = %+v", spy.events[0].details)
	}
	observed, ok := spy.events[1].details.(observationDetails)
	if !ok || observed.Outcome != OutcomeApplied || observed.ActualRaw == nil || *observed.ActualRaw != 2 || observed.VerifiedResults != 1 {
		t.Fatalf("observation details = %+v", spy.events[1].details)
	}
}

func TestApplyTemporaryRecordsUncertainObservationWithoutInventedActualValue(t *testing.T) {
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	coordinator := board.NewCoordinator()
	spy := &activitySpy{coordinator: coordinator}
	service, err := NewService(&uncertainDevice{}, catalogue, coordinator, spy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyTemporary(context.Background(), 7, ledProposal()); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	observed := spy.events[1].details.(observationDetails)
	if observed.Outcome != OutcomeUncertain || observed.ActualRaw != nil {
		t.Fatalf("uncertain observation = %+v", observed)
	}
}
