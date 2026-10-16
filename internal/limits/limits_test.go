package limits

import "testing"

func TestDefaultsValidate(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsUnsafeLimitRelationships(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Limits)
	}{
		{name: "concurrency", change: func(value *Limits) { value.HTTP.MaxConcurrent = value.HTTP.MaxConnections + 1 }},
		{name: "body size", change: func(value *Limits) { value.HTTP.MaxBodyBytes = 0 }},
		{name: "vendor command", change: func(value *Limits) { value.Vendor.MaxStdoutBytes = 0 }},
		{name: "power command", change: func(value *Limits) { value.Power.Timeout = 0 }},
		{name: "job timeout", change: func(value *Limits) { value.Jobs.DefaultTimeout = value.Jobs.MaximumTimeout + 1 }},
		{name: "saved jobs", change: func(value *Limits) { value.Jobs.MaxSavedJobs = 0 }},
		{name: "job parallelism", change: func(value *Limits) { value.Jobs.MaxConcurrent = 2 }},
		{name: "job output", change: func(value *Limits) { value.Jobs.MaximumStreamBytes = value.Jobs.MaxStdoutBytes - 1 }},
		{name: "job frame", change: func(value *Limits) { value.Jobs.MaxRunnerFrameBytes = 1 }},
		{name: "job argument bytes", change: func(value *Limits) { value.Jobs.MaxArgumentBytes = 0 }},
		{name: "job queue", change: func(value *Limits) { value.Jobs.MaxQueued = 0 }},
		{name: "polling", change: func(value *Limits) { value.Polling.BoardInterval = value.Polling.MinimumInterval - 1 }},
		{name: "snapshot", change: func(value *Limits) { value.Persistence.MaxSnapshotBytes = 0 }},
		{name: "activity event", change: func(value *Limits) {
			value.Persistence.ActivityEventBytes = int(value.Persistence.ActivitySegmentBytes + 1)
		}},
		{name: "session", change: func(value *Limits) { value.Authentication.IdleLifetime = value.Authentication.AbsoluteLifetime + 1 }},
		{name: "session count", change: func(value *Limits) { value.Authentication.MaxSessions = 0 }},
		{name: "controller soft memory", change: func(value *Limits) { value.ControllerSoftMemory = value.ControllerMemory + 1 }},
		{name: "memory", change: func(value *Limits) { value.RunnerMemory = value.ControllerMemory - 1 }},
		{name: "tasks", change: func(value *Limits) { value.RunnerTasks = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := Default()
			test.change(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("Validate accepted invalid limits")
			}
		})
	}
}
