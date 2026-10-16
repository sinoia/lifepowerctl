package registeredit

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
)

func newBatchTestService(t *testing.T, device Device) *BatchService {
	return newBatchTestServiceWithAudit(t, device, nil)
}

func newBatchTestServiceWithAudit(t *testing.T, device Device, audit activityRecorder) *BatchService {
	t.Helper()
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	coordinator := board.NewCoordinator()
	if spy, ok := audit.(*activitySpy); ok {
		spy.coordinator = coordinator
	}
	service, err := NewBatchService(device, catalogue, coordinator, audit)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestBatchServiceRecordsSuccessfulWritesAndFinalStateInsideLease(t *testing.T) {
	before, after := batteryValues(2850, 2950, 3150), batteryValues(2860, 2960, 3160)
	spy := &activitySpy{}
	result, err := newBatchTestServiceWithAudit(t, batteryFake(before), spy).ApplyBatteryThresholds(context.Background(), BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
	if err != nil || result.Outcome != BatchOutcomeApplied {
		t.Fatalf("ApplyBatteryThresholds() = %+v, %v", result, err)
	}
	want := []activity.Kind{activity.RegisterBatchAttempted, activity.RegisterBatchWriteObserved, activity.RegisterBatchWriteObserved, activity.RegisterBatchWriteObserved, activity.RegisterBatchObserved}
	assertBatchActivity(t, spy.events, want)
	for _, event := range spy.events {
		encoded, marshalErr := json.Marshal(event.details)
		if marshalErr != nil || strings.Contains(string(encoded), `"raw`) || !strings.Contains(string(encoded), `"unit":"mV"`) {
			t.Fatalf("batch evidence must use labelled scaled values: %s, %v", encoded, marshalErr)
		}
	}
}

func TestBatchServiceRecordsRollbackDecisionAndResult(t *testing.T) {
	before, after := batteryValues(2850, 2950, 3150), batteryValues(2860, 2960, 3160)
	fake := batteryFake(before)
	fake.SetFaults(board.Faults{FailWriteAt: 1, LoseConnectionAt: -1})
	spy := &activitySpy{}
	result, err := newBatchTestServiceWithAudit(t, fake, spy).ApplyBatteryThresholds(context.Background(), BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
	if !errors.Is(err, ErrBatchFailed) || result.Outcome != BatchOutcomeRolledBack {
		t.Fatalf("ApplyBatteryThresholds() = %+v, %v", result, err)
	}
	want := []activity.Kind{activity.RegisterBatchAttempted, activity.RegisterBatchWriteObserved, activity.RegisterBatchRollbackObserved, activity.RegisterBatchObserved}
	assertBatchActivity(t, spy.events, want)
	details, ok := spy.events[2].details.(struct {
		Decision string           `json:"decision"`
		Plan     []changeEvidence `json:"plan"`
		Results  []writeEvidence  `json:"results"`
	})
	if !ok || details.Decision != "attempted" || len(details.Plan) == 0 || len(details.Results) == 0 {
		t.Fatalf("rollback details = %#v", spy.events[2].details)
	}
}

func TestBatchServiceRecordsUnknownStateRollbackDecision(t *testing.T) {
	before, after := batteryValues(2850, 2950, 3150), batteryValues(2860, 2960, 3160)
	spy := &activitySpy{}
	result, err := newBatchTestServiceWithAudit(t, &uncertainBatchDevice{}, spy).ApplyBatteryThresholds(context.Background(), BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
	if !errors.Is(err, ErrBatchUncertain) || result.Outcome != BatchOutcomeUncertain {
		t.Fatalf("ApplyBatteryThresholds() = %+v, %v", result, err)
	}
	want := []activity.Kind{activity.RegisterBatchAttempted, activity.RegisterBatchRollbackObserved, activity.RegisterBatchObserved}
	assertBatchActivity(t, spy.events, want)
}

func assertBatchActivity(t *testing.T, events []recordedActivity, want []activity.Kind) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(want), events)
	}
	for index, event := range events {
		if event.kind != want[index] || event.actor != activity.ActorAdmin || event.subject != "battery-thresholds" || !event.leaseActive {
			t.Fatalf("event %d = %+v, want kind %q inside lease", index, event, want[index])
		}
	}
}

func batteryFake(values map[board.Register]int64) *board.Fake {
	return board.NewFake(board.Identity{RegisterSet: 7}, values)
}

func readFakeThresholds(t *testing.T, fake *board.Fake) map[board.Register]int64 {
	t.Helper()
	values, err := fake.Read(context.Background(), batteryThresholdRegisters...)
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func TestBatchServiceAppliesVerifiedSafePlan(t *testing.T) {
	before, after := batteryValues(2850, 2950, 3150), batteryValues(2860, 2960, 3160)
	fake := batteryFake(before)
	result, err := newBatchTestService(t, fake).ApplyBatteryThresholds(context.Background(), BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
	if err != nil || result.Outcome != BatchOutcomeApplied || !maps.Equal(result.Actual, after) || !maps.Equal(readFakeThresholds(t, fake), after) {
		t.Fatalf("ApplyBatteryThresholds() = %+v, %v", result, err)
	}
}

func TestBatchServiceRejectsStaleCompleteSnapshotWithoutWriting(t *testing.T) {
	before, after := batteryValues(2850, 2950, 3150), batteryValues(2850, 2950, 3160)
	fake := batteryFake(before)
	fake.ExternalSet("VBAT_SHDN", 2955)
	result, err := newBatchTestService(t, fake).ApplyBatteryThresholds(context.Background(), BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
	if !errors.Is(err, ErrBatchStale) || result.Outcome != BatchOutcomeRejectedStale || readFakeThresholds(t, fake)["VBAT_SHDN"] != 2955 {
		t.Fatalf("ApplyBatteryThresholds() = %+v, %v", result, err)
	}
}

func TestBatchServiceRollsBackEveryPartialWritePrefix(t *testing.T) {
	before, after := batteryValues(2850, 2950, 3150), batteryValues(2860, 2960, 3160)
	for failAt := 1; failAt < 3; failAt++ {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			fake := batteryFake(before)
			fake.SetFaults(board.Faults{FailWriteAt: failAt, LoseConnectionAt: -1})
			result, err := newBatchTestService(t, fake).ApplyBatteryThresholds(context.Background(), BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
			if !errors.Is(err, ErrBatchFailed) || result.Outcome != BatchOutcomeRolledBack || !result.RollbackAttempted || !maps.Equal(result.Actual, before) || !maps.Equal(readFakeThresholds(t, fake), before) {
				t.Fatalf("failAt=%d result=%+v error=%v", failAt, result, err)
			}
		})
	}
}

func TestBatchServiceReconcilesConnectionLossAtEveryWriteBoundary(t *testing.T) {
	before, after := batteryValues(2850, 2950, 3150), batteryValues(2860, 2960, 3160)
	for loseAt := 0; loseAt < 3; loseAt++ {
		t.Run(string(rune('0'+loseAt)), func(t *testing.T) {
			fake := batteryFake(before)
			fake.SetFaults(board.Faults{FailWriteAt: -1, LoseConnectionAt: loseAt})
			result, err := newBatchTestService(t, fake).ApplyBatteryThresholds(context.Background(), BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
			wantOutcome := BatchOutcomeFailedNoChange
			wantRollback := false
			if loseAt > 0 {
				wantOutcome = BatchOutcomeRolledBack
				wantRollback = true
			}
			if !errors.Is(err, ErrBatchFailed) || result.Outcome != wantOutcome || result.RollbackAttempted != wantRollback || !maps.Equal(result.Actual, before) || !maps.Equal(readFakeThresholds(t, fake), before) {
				t.Fatalf("loseAt=%d result=%+v error=%v", loseAt, result, err)
			}
		})
	}
}

func TestBatchServiceReconcilesReadBackMismatchAfterEveryWrite(t *testing.T) {
	before, after := batteryValues(2850, 2950, 3150), batteryValues(2860, 2960, 3160)
	writeOrder := []board.Register{"VBAT_MIN", "VBAT_SHDN", "VBAT_BOOT"}
	for index, register := range writeOrder {
		t.Run(string(register), func(t *testing.T) {
			fake := batteryFake(before)
			device := &oneShotReadBackMismatchDevice{Fake: fake, register: register}
			result, err := newBatchTestService(t, device).ApplyBatteryThresholds(context.Background(), BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
			if index == len(writeOrder)-1 {
				if !errors.Is(err, ErrBatchFailed) || result.Outcome != BatchOutcomeReconciledApplied || result.RollbackAttempted || !maps.Equal(result.Actual, after) {
					t.Fatalf("register=%s result=%+v error=%v", register, result, err)
				}
				return
			}
			if !errors.Is(err, ErrBatchFailed) || result.Outcome != BatchOutcomeRolledBack || !result.RollbackAttempted || !maps.Equal(result.Actual, before) || !maps.Equal(readFakeThresholds(t, fake), before) {
				t.Fatalf("register=%s result=%+v error=%v", register, result, err)
			}
		})
	}
}

type oneShotReadBackMismatchDevice struct {
	*board.Fake
	register board.Register
	applies  int
}

func (device *oneShotReadBackMismatchDevice) Apply(ctx context.Context, changes []board.Change) (board.ApplyResult, error) {
	device.applies++
	if device.applies == 1 {
		device.SetFaults(board.Faults{FailWriteAt: -1, LoseConnectionAt: -1, CorruptReadBack: map[board.Register]int64{device.register: -1}})
	}
	result, err := device.Fake.Apply(ctx, changes)
	device.SetFaults(board.Faults{FailWriteAt: -1, LoseConnectionAt: -1})
	return result, err
}

type errorAfterVerifiedBatchDevice struct {
	*board.Fake
}

func (device *errorAfterVerifiedBatchDevice) Apply(ctx context.Context, changes []board.Change) (board.ApplyResult, error) {
	result, err := device.Fake.Apply(ctx, changes)
	if err != nil {
		return result, err
	}
	return result, errors.New("simulated error after final verified write")
}

func TestBatchServiceReconcilesErrorAfterFinalVerifiedWrite(t *testing.T) {
	before, after := batteryValues(2850, 2950, 3150), batteryValues(2860, 2960, 3160)
	device := &errorAfterVerifiedBatchDevice{Fake: batteryFake(before)}
	result, err := newBatchTestService(t, device).ApplyBatteryThresholds(context.Background(), BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
	if !errors.Is(err, ErrBatchFailed) || result.Outcome != BatchOutcomeReconciledApplied || result.RollbackAttempted || !maps.Equal(result.Actual, after) {
		t.Fatalf("ApplyBatteryThresholds() = %+v, %v", result, err)
	}
}

func TestBatchServiceClassifiesFailureBeforeFirstWrite(t *testing.T) {
	before, after := batteryValues(2850, 2950, 3150), batteryValues(2860, 2960, 3160)
	fake := batteryFake(before)
	fake.SetFaults(board.Faults{FailWriteAt: 0, LoseConnectionAt: -1})
	result, err := newBatchTestService(t, fake).ApplyBatteryThresholds(context.Background(), BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
	if !errors.Is(err, ErrBatchFailed) || result.Outcome != BatchOutcomeFailedNoChange || result.RollbackAttempted || !maps.Equal(result.Actual, before) {
		t.Fatalf("ApplyBatteryThresholds() = %+v, %v", result, err)
	}
}

type uncertainBatchDevice struct {
	reads int
}

func (device *uncertainBatchDevice) Detect(context.Context) (board.Identity, error) {
	return board.Identity{RegisterSet: 7}, nil
}

func (device *uncertainBatchDevice) Read(context.Context, ...board.Register) (map[board.Register]int64, error) {
	device.reads++
	if device.reads == 1 {
		return batteryValues(2850, 2950, 3150), nil
	}
	return nil, board.ErrCommunication
}

func (device *uncertainBatchDevice) Apply(context.Context, []board.Change) (board.ApplyResult, error) {
	return board.ApplyResult{}, board.ErrCommunication
}

func TestBatchServicePreservesUncertainOutcomeWhenReconciliationFails(t *testing.T) {
	before, after := batteryValues(2850, 2950, 3150), batteryValues(2860, 2960, 3160)
	result, err := newBatchTestService(t, &uncertainBatchDevice{}).ApplyBatteryThresholds(context.Background(), BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
	if !errors.Is(err, ErrBatchUncertain) || result.Outcome != BatchOutcomeUncertain || result.Actual != nil || result.RollbackAttempted {
		t.Fatalf("ApplyBatteryThresholds() = %+v, %v", result, err)
	}
}

type rollbackFailureDevice struct {
	state   map[board.Register]int64
	applies int
}

func (device *rollbackFailureDevice) Detect(context.Context) (board.Identity, error) {
	return board.Identity{RegisterSet: 7}, nil
}

func (device *rollbackFailureDevice) Read(context.Context, ...board.Register) (map[board.Register]int64, error) {
	return cloneValues(device.state), nil
}

func (device *rollbackFailureDevice) Apply(_ context.Context, changes []board.Change) (board.ApplyResult, error) {
	device.applies++
	if device.applies == 1 {
		change := changes[0]
		device.state[change.Register] = change.Value
		return board.ApplyResult{Changes: []board.ChangeResult{{Change: change, ReadBack: change.Value, Applied: true, Verified: true}}}, errors.New("forward failure")
	}
	return board.ApplyResult{}, errors.New("rollback failure")
}

func TestBatchServiceReportsKnownPartialStateWhenRollbackFails(t *testing.T) {
	before, after := batteryValues(2850, 2950, 3150), batteryValues(2860, 2960, 3160)
	device := &rollbackFailureDevice{state: cloneValues(before)}
	result, err := newBatchTestService(t, device).ApplyBatteryThresholds(context.Background(), BatchProposal{RegisterSet: 7, ExpectedValues: before, ProposedValues: after})
	if !errors.Is(err, ErrBatchFailed) || result.Outcome != BatchOutcomePartialKnown || !result.RollbackAttempted || maps.Equal(result.Actual, before) || device.applies != 2 {
		t.Fatalf("ApplyBatteryThresholds() = %+v, %v; applies=%d", result, err, device.applies)
	}
	if result.Actual["VBAT_BOOT"] != 3150 || result.Actual["VBAT_MIN"] != 2860 || result.Actual["VBAT_SHDN"] != 2950 {
		t.Fatalf("known partial state = %v", result.Actual)
	}
}
