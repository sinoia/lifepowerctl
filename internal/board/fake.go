package board

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Faults controls one Apply call. Write indexes are zero based. A negative
// index disables that fault. CorruptReadBack substitutes only the verification
// value; the stored value remains the value that was written.
type Faults struct {
	FailWriteAt      int
	LoseConnectionAt int
	CorruptReadBack  map[Register]int64
}

type Fake struct {
	mu         sync.Mutex
	identity   Identity
	registers  map[Register]int64
	countdown  time.Duration
	bootReason BootReason
	faults     Faults
	power      []PowerAction
}

func NewFake(identity Identity, initial map[Register]int64) *Fake {
	registers := make(map[Register]int64, len(initial))
	for register, value := range initial {
		registers[register] = value
	}
	return &Fake{
		identity:   identity,
		registers:  registers,
		bootReason: BootAmbiguous,
		faults: Faults{
			FailWriteAt:      -1,
			LoseConnectionAt: -1,
		},
	}
}

func (f *Fake) Detect(context.Context) (Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.identity, nil
}

func (f *Fake) Read(ctx context.Context, wanted ...Register) (map[Register]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make(map[Register]int64, len(wanted))
	for _, register := range wanted {
		value, ok := f.registers[register]
		if !ok {
			return nil, fmt.Errorf("read %s: unknown register", register)
		}
		result[register] = value
	}
	return result, nil
}

func (f *Fake) Apply(ctx context.Context, changes []Change) (ApplyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := ApplyResult{Changes: make([]ChangeResult, 0, len(changes))}
	for index, change := range changes {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if index == f.faults.LoseConnectionAt {
			return result, ErrCommunication
		}
		if index == f.faults.FailWriteAt {
			return result, fmt.Errorf("write %s: simulated failure", change.Register)
		}
		if _, ok := f.registers[change.Register]; !ok {
			return result, fmt.Errorf("write %s: unknown register", change.Register)
		}
		f.registers[change.Register] = change.Value
		readBack := change.Value
		if corrupt, ok := f.faults.CorruptReadBack[change.Register]; ok {
			readBack = corrupt
		}
		item := ChangeResult{Change: change, ReadBack: readBack, Applied: true, Verified: readBack == change.Value}
		result.Changes = append(result.Changes, item)
		if !item.Verified {
			return result, fmt.Errorf("verify %s: %w", change.Register, ErrReadback)
		}
	}
	return result, nil
}

func (f *Fake) Countdown(context.Context) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.countdown, nil
}

func (f *Fake) BootReason(context.Context) (BootReason, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bootReason, nil
}

func (f *Fake) RequestPower(ctx context.Context, action PowerAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.power = append(f.power, action)
	return nil
}

// ExternalSet simulates another process or physical device changing a value.
func (f *Fake) ExternalSet(register Register, value int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registers[register] = value
}

func (f *Fake) SetFaults(faults Faults) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.faults = faults
}

func (f *Fake) SetCountdown(countdown time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.countdown = countdown
}

func (f *Fake) SetBootReason(reason BootReason) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bootReason = reason
}

func (f *Fake) PowerActions() []PowerAction {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]PowerAction(nil), f.power...)
}
