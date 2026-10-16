package diagnostic

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/registers"
)

func TestGenerateIsBoundedNewestFirstAndExcludesEventDetails(t *testing.T) {
	definition := board.Definition{Register: "RTC_TIME", Address: 0x28, Unit: "unix-seconds"}
	value := int64(1_791_666_092)
	snapshot := registers.Snapshot{
		State: registers.StatePartial, Identity: board.Identity{RegisterSet: 7}, CheckedAt: time.Unix(10, 0), Successful: 1, Failed: 1,
		Items: []registers.Item{
			{Definition: definition, Reading: &board.Reading{Definition: definition, EngineeringValue: value, RawValue: &value}},
			{Definition: board.Definition{Register: "VBAT", Address: 0x32, Unit: "mV"}, Failure: registers.FailureTimeout},
		},
	}
	events := []activity.Event{
		{ID: 1, Time: time.Unix(1, 0), Type: string(activity.AuthenticationLoginSucceeded), Actor: activity.ActorAdmin, Details: json.RawMessage(`{"password":"must-not-export"}`)},
		{ID: 2, Time: time.Unix(2, 0), Type: string(activity.BoardObserved), Actor: activity.ActorSystem, Subject: "board", Details: json.RawMessage(`{"token":"must-not-export"}`)},
	}
	encoded, err := Generate(time.Unix(20, 0), Build{Version: "test", Commit: "abc"}, snapshot, events, 1, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("must-not-export")) || bytes.Contains(encoded, []byte(`"details"`)) || bytes.Contains(encoded, []byte(`"subject"`)) {
		t.Fatalf("report exported Activity details: %s", encoded)
	}
	var report Report
	if err := json.Unmarshal(encoded, &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 1 || report.Activity.Retained != 2 || report.Activity.Included != 1 || !report.Activity.Truncated || report.Activity.Events[0].ID != 2 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.Board.Registers) != 2 || report.Board.Registers[0].EngineeringValue == nil || *report.Board.Registers[0].EngineeringValue != value || report.Board.Registers[1].Failure != registers.FailureTimeout {
		t.Fatalf("unexpected register report: %+v", report.Board.Registers)
	}
}

func TestGenerateRejectsOversizedReport(t *testing.T) {
	_, err := Generate(time.Now(), Build{}, registers.Snapshot{}, nil, 0, 16)
	if err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("oversized report error = %v", err)
	}
}
