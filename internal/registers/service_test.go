package registers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/limits"
)

type testDetector struct {
	identity board.Identity
	err      error
	calls    int
}

func (detector *testDetector) Detect(context.Context) (board.Identity, error) {
	detector.calls++
	return detector.identity, detector.err
}

type testReader struct {
	mu       sync.Mutex
	failures map[board.Register]error
	block    bool
	calls    int
}

func (reader *testReader) Read(ctx context.Context, _ int, register board.Register) (board.Reading, error) {
	reader.mu.Lock()
	reader.calls++
	reader.mu.Unlock()
	if reader.block {
		<-ctx.Done()
		return board.Reading{}, ctx.Err()
	}
	if err := reader.failures[register]; err != nil {
		return board.Reading{}, err
	}
	value := int64(1)
	return board.Reading{EngineeringValue: value, RawValue: &value}, nil
}

func newTestService(t *testing.T, detector Detector, reader Reader, polling limits.Polling) (*Service, int) {
	t.Helper()
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	return New(detector, reader, catalogue, board.NewCoordinator(), polling), len(catalogue.Definitions())
}

func TestRefreshProducesCompleteAndCachedSnapshot(t *testing.T) {
	polling := limits.Default().Polling
	detector := &testDetector{identity: board.Identity{Product: "LiFePO4wered/Pi+", RegisterSet: 7}}
	reader := &testReader{}
	service, count := newTestService(t, detector, reader, polling)

	first := service.Refresh(context.Background())
	if first.State != StateSupported || first.Successful != count || first.Failed != 0 || len(first.Items) != count {
		t.Fatalf("unexpected complete snapshot: %+v", first)
	}
	first.Items[0].Reading.EngineeringValue = 99
	second := service.Refresh(context.Background())
	if detector.calls != 1 || reader.calls != count {
		t.Fatalf("recent snapshot was not cached: detect=%d read=%d", detector.calls, reader.calls)
	}
	if second.Items[0].Reading.EngineeringValue == 99 {
		t.Fatal("caller mutated the stored snapshot")
	}
}

func TestRefreshPreservesSuccessesOnIndividualFailure(t *testing.T) {
	polling := limits.Default().Polling
	detector := &testDetector{identity: board.Identity{RegisterSet: 7}}
	reader := &testReader{failures: map[board.Register]error{"I2C_ADDRESS": board.ErrVendorOutputLimit}}
	service, count := newTestService(t, detector, reader, polling)

	snapshot := service.Refresh(context.Background())
	if snapshot.State != StatePartial || snapshot.Successful != count-1 || snapshot.Failed != 1 {
		t.Fatalf("unexpected partial snapshot: %+v", snapshot)
	}
	found := false
	for _, item := range snapshot.Items {
		if item.Definition.Register == "I2C_ADDRESS" {
			found = item.Reading == nil && item.Failure == FailureOutputLimit
		}
	}
	if !found {
		t.Fatal("failed register did not retain its typed failure")
	}
}

func TestRefreshStopsCleanlyForUnsupportedRegisterSet(t *testing.T) {
	polling := limits.Default().Polling
	detector := &testDetector{identity: board.Identity{RegisterSet: 8}, err: board.ErrUnsupportedRegisterSet}
	reader := &testReader{}
	service, _ := newTestService(t, detector, reader, polling)

	snapshot := service.Refresh(context.Background())
	if snapshot.State != StateUnsupported || snapshot.Identity.RegisterSet != 8 || snapshot.Failure != FailureNone || reader.calls != 0 {
		t.Fatalf("unexpected unsupported snapshot: %+v, reads=%d", snapshot, reader.calls)
	}
}

func TestRefreshBoundsWholeScanAndMarksRemainingItems(t *testing.T) {
	polling := limits.Default().Polling
	polling.RegisterSnapshotTimeout = 20 * time.Millisecond
	detector := &testDetector{identity: board.Identity{RegisterSet: 7}}
	reader := &testReader{block: true}
	service, count := newTestService(t, detector, reader, polling)

	started := time.Now()
	snapshot := service.Refresh(context.Background())
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("snapshot exceeded its total budget: %s", elapsed)
	}
	if snapshot.State != StateUnavailable || snapshot.Successful != 0 || snapshot.Failed != count || reader.calls != 1 {
		t.Fatalf("unexpected timed-out snapshot: %+v, reads=%d", snapshot, reader.calls)
	}
	if snapshot.Items[0].Failure != FailureTimeout {
		t.Fatalf("first read failure = %q, want timeout", snapshot.Items[0].Failure)
	}
	for _, item := range snapshot.Items[1:] {
		if item.Failure != FailureNotAttempted {
			t.Fatalf("remaining read failure = %q, want not-attempted", item.Failure)
		}
	}
}

func TestRefreshClassifiesDetectionFailure(t *testing.T) {
	polling := limits.Default().Polling
	detector := &testDetector{err: errors.New("i2c unavailable")}
	service, _ := newTestService(t, detector, &testReader{}, polling)
	snapshot := service.Refresh(context.Background())
	if snapshot.State != StateUnavailable || snapshot.Failure != FailureCommunication || len(snapshot.Items) != 0 {
		t.Fatalf("unexpected detection failure: %+v", snapshot)
	}
}
