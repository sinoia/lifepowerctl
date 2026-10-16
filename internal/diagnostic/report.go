// Package diagnostic builds bounded, deliberately narrow support reports.
package diagnostic

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/registers"
)

const SchemaVersion = 1

type Build struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

type Report struct {
	SchemaVersion int              `json:"schema_version"`
	GeneratedAt   time.Time        `json:"generated_at"`
	Build         Build            `json:"build"`
	Board         BoardSnapshot    `json:"board"`
	Activity      ActivitySnapshot `json:"activity"`
}

type BoardSnapshot struct {
	State       registers.State   `json:"state"`
	RegisterSet int               `json:"register_set,omitempty"`
	Failure     registers.Failure `json:"failure,omitempty"`
	CheckedAt   time.Time         `json:"checked_at,omitempty"`
	Successful  int               `json:"successful"`
	Failed      int               `json:"failed"`
	Registers   []RegisterValue   `json:"registers,omitempty"`
}

type RegisterValue struct {
	Name             string            `json:"name"`
	Address          string            `json:"address"`
	EngineeringValue *int64            `json:"engineering_value,omitempty"`
	Unit             string            `json:"unit,omitempty"`
	RawValue         *int64            `json:"raw_value,omitempty"`
	Choice           string            `json:"choice,omitempty"`
	Sentinel         bool              `json:"sentinel,omitempty"`
	Failure          registers.Failure `json:"failure,omitempty"`
}

type ActivitySnapshot struct {
	Retained  int             `json:"retained"`
	Included  int             `json:"included"`
	Truncated bool            `json:"truncated"`
	Events    []ActivityEvent `json:"events,omitempty"`
}

// ActivityEvent intentionally excludes Details and Subject. They are useful in
// the authenticated UI, but excluding user-influenced payloads gives exported
// reports a structural guarantee against accidentally disclosing secrets.
type ActivityEvent struct {
	ID    uint64    `json:"id"`
	Time  time.Time `json:"time"`
	Type  string    `json:"type"`
	Actor string    `json:"actor"`
}

func Generate(generatedAt time.Time, build Build, snapshot registers.Snapshot, events []activity.Event, maximumEvents, maximumBytes int) ([]byte, error) {
	if generatedAt.IsZero() || maximumEvents < 0 || maximumBytes <= 0 {
		return nil, fmt.Errorf("invalid diagnostic report limits")
	}
	report := Report{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   generatedAt.UTC(),
		Build:         build,
		Board: BoardSnapshot{
			State: snapshot.State, RegisterSet: snapshot.Identity.RegisterSet, Failure: snapshot.Failure,
			CheckedAt: snapshot.CheckedAt, Successful: snapshot.Successful, Failed: snapshot.Failed,
		},
		Activity: ActivitySnapshot{Retained: len(events)},
	}
	for _, item := range snapshot.Items {
		value := RegisterValue{Name: string(item.Definition.Register), Address: fmt.Sprintf("0x%02X", item.Definition.Address), Unit: item.Definition.Unit, Failure: item.Failure}
		if item.Reading != nil {
			engineering := item.Reading.EngineeringValue
			value.EngineeringValue = &engineering
			if item.Reading.RawValue != nil {
				raw := *item.Reading.RawValue
				value.RawValue = &raw
			}
			value.Choice = item.Reading.Choice
			value.Sentinel = item.Reading.Sentinel
		}
		report.Board.Registers = append(report.Board.Registers, value)
	}
	start := 0
	if len(events) > maximumEvents {
		start = len(events) - maximumEvents
		report.Activity.Truncated = true
	}
	for index := len(events) - 1; index >= start; index-- {
		event := events[index]
		report.Activity.Events = append(report.Activity.Events, ActivityEvent{ID: event.ID, Time: event.Time, Type: event.Type, Actor: event.Actor})
	}
	report.Activity.Included = len(report.Activity.Events)
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode diagnostic report: %w", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maximumBytes {
		return nil, fmt.Errorf("diagnostic report is %d bytes; maximum is %d", len(encoded), maximumBytes)
	}
	return encoded, nil
}
