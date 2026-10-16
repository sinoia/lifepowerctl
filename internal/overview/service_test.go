package overview

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/limits"
)

type detection struct {
	identity board.Identity
	err      error
}

type fakeDetector struct {
	results []detection
	calls   int
}

func (detector *fakeDetector) Detect(context.Context) (board.Identity, error) {
	index := detector.calls
	detector.calls++
	if index >= len(detector.results) {
		index = len(detector.results) - 1
	}
	return detector.results[index].identity, detector.results[index].err
}

func testService(detector Detector) (*Service, *time.Time) {
	now := time.Date(2026, time.October, 10, 12, 0, 0, 0, time.UTC)
	polling := limits.Default().Polling
	service := New(detector, board.NewCoordinator(), polling)
	service.now = func() time.Time { return now }
	return service, &now
}

func TestRefreshSupportedAndCoalesced(t *testing.T) {
	detector := &fakeDetector{results: []detection{{identity: board.Identity{RegisterSet: 7}}}}
	service, now := testService(detector)
	first := service.Refresh(context.Background())
	if first.State != StateSupported || first.Identity.RegisterSet != 7 || first.LastSuccess != *now {
		t.Fatalf("first snapshot = %+v", first)
	}
	second := service.Refresh(context.Background())
	if second != first || detector.calls != 1 {
		t.Fatalf("coalesced snapshot = %+v, calls = %d", second, detector.calls)
	}
}

func TestRefreshClassifiesUnsupportedAndUnavailable(t *testing.T) {
	detector := &fakeDetector{results: []detection{
		{identity: board.Identity{RegisterSet: 8}, err: board.ErrUnsupportedRegisterSet},
		{err: board.ErrVendorTimeout},
	}}
	service, now := testService(detector)
	unsupported := service.Refresh(context.Background())
	if unsupported.State != StateUnsupported || unsupported.Identity.RegisterSet != 8 {
		t.Fatalf("unsupported snapshot = %+v", unsupported)
	}
	*now = now.Add(service.minimumAge)
	unavailable := service.Refresh(context.Background())
	if unavailable.State != StateUnavailable || unavailable.Failure != FailureTimeout {
		t.Fatalf("unavailable snapshot = %+v", unavailable)
	}
}

func TestFailedRefreshRetainsLastSuccessAsStale(t *testing.T) {
	detector := &fakeDetector{results: []detection{
		{identity: board.Identity{RegisterSet: 7}},
		{err: board.ErrVendorCommand},
	}}
	service, now := testService(detector)
	service.Refresh(context.Background())
	*now = now.Add(service.minimumAge)
	stale := service.Refresh(context.Background())
	if stale.State != StateStale || stale.Identity.RegisterSet != 7 || stale.Failure != FailureCommand || stale.LastSuccess.IsZero() {
		t.Fatalf("stale snapshot = %+v", stale)
	}
}

func TestCurrentAgesSuccessfulSnapshot(t *testing.T) {
	detector := &fakeDetector{results: []detection{{identity: board.Identity{RegisterSet: 7}}}}
	service, now := testService(detector)
	service.Refresh(context.Background())
	*now = now.Add(service.staleAfter)
	if current := service.Current(); current.State != StateStale {
		t.Fatalf("aged snapshot = %+v", current)
	}
}

func TestRefreshClassifiesCoordinatorCancellation(t *testing.T) {
	detector := &fakeDetector{results: []detection{{err: errors.New("must not be reached")}}}
	service, _ := testService(detector)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	snapshot := service.Refresh(ctx)
	if snapshot.State != StateUnavailable || snapshot.Failure != FailureCommunication || detector.calls != 0 {
		t.Fatalf("cancelled snapshot = %+v, calls = %d", snapshot, detector.calls)
	}
}

func TestObservationAndStateTransitionsEmitDistinctActivity(t *testing.T) {
	detector := &fakeDetector{results: []detection{{identity: board.Identity{RegisterSet: 7}}, {err: board.ErrVendorCommand}}}
	service, now := testService(detector)
	journal, err := activity.Open(t.TempDir(), activity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	service.SetAudit(activity.NewAudit(journal))
	service.Refresh(context.Background())
	*now = now.Add(service.minimumAge)
	service.Refresh(context.Background())
	events, err := journal.Events()
	if err != nil || len(events) != 2 {
		t.Fatalf("Events() = %+v, %v", events, err)
	}
	if events[0].Type != string(activity.BoardObserved) || events[0].Actor != activity.ActorSystem || string(events[0].Details) != `{"state":"supported","register_set":7}` {
		t.Fatalf("observation event = %+v", events[0])
	}
	if events[1].Type != string(activity.BoardStateChanged) || string(events[1].Details) != `{"previous_state":"supported","resulting_state":"stale","register_set":7,"failure":"command-failed"}` {
		t.Fatalf("transition event = %+v", events[1])
	}
}
