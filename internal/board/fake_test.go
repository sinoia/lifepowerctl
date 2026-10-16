package board

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFakeExternalMutationAndState(t *testing.T) {
	fake := NewFake(Identity{Product: "test", RegisterSet: 7}, map[Register]int64{"A": 1})
	fake.ExternalSet("A", 2)
	fake.SetCountdown(5 * time.Minute)
	fake.SetBootReason(BootManual)

	values, err := fake.Read(context.Background(), "A")
	if err != nil || values["A"] != 2 {
		t.Fatalf("Read() = %v, %v", values, err)
	}
	if countdown, _ := fake.Countdown(context.Background()); countdown != 5*time.Minute {
		t.Fatalf("Countdown() = %v", countdown)
	}
	if reason, _ := fake.BootReason(context.Background()); reason != BootManual {
		t.Fatalf("BootReason() = %q", reason)
	}
	if err := fake.RequestPower(context.Background(), PowerShutdown); err != nil {
		t.Fatal(err)
	}
	if got := fake.PowerActions(); len(got) != 1 || got[0] != PowerShutdown {
		t.Fatalf("PowerActions() = %v", got)
	}
}

func TestFakePartialWrite(t *testing.T) {
	fake := NewFake(Identity{}, map[Register]int64{"A": 1, "B": 2})
	fake.SetFaults(Faults{FailWriteAt: 1, LoseConnectionAt: -1})
	result, err := fake.Apply(context.Background(), []Change{{Register: "A", Value: 3}, {Register: "B", Value: 4}})
	if err == nil || len(result.Changes) != 1 || !result.Changes[0].Verified {
		t.Fatalf("Apply() = %+v, %v", result, err)
	}
	values, _ := fake.Read(context.Background(), "A", "B")
	if values["A"] != 3 || values["B"] != 2 {
		t.Fatalf("partial values = %v", values)
	}
}

func TestFakeReadBackCorruptionAndConnectionLoss(t *testing.T) {
	fake := NewFake(Identity{}, map[Register]int64{"A": 1})
	fake.SetFaults(Faults{FailWriteAt: -1, LoseConnectionAt: -1, CorruptReadBack: map[Register]int64{"A": 9}})
	result, err := fake.Apply(context.Background(), []Change{{Register: "A", Value: 2}})
	if !errors.Is(err, ErrReadback) || result.Changes[0].Verified {
		t.Fatalf("corrupt Apply() = %+v, %v", result, err)
	}

	fake.SetFaults(Faults{FailWriteAt: -1, LoseConnectionAt: 0})
	result, err = fake.Apply(context.Background(), []Change{{Register: "A", Value: 3}})
	if !errors.Is(err, ErrCommunication) || len(result.Changes) != 0 {
		t.Fatalf("lost Apply() = %+v, %v", result, err)
	}
}
