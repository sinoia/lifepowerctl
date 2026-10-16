// Package limits owns measurable runtime bounds shared across subsystems.
package limits

import (
	"fmt"
	"time"
)

const (
	DefaultMaxSnapshotBytes     = 256 << 10
	DefaultActivitySegmentBytes = 64 << 10
	DefaultActivitySegments     = 16
	DefaultActivityEventBytes   = 8 << 10
)

type Limits struct {
	HTTP                 HTTP
	Vendor               Command
	Power                Command
	Jobs                 Jobs
	Polling              Polling
	Persistence          Persistence
	Authentication       Authentication
	ControllerSoftMemory int64
	ControllerMemory     int64
	RunnerMemory         int64
	RunnerTasks          int
}

type HTTP struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
	MaxBodyBytes      int64
	MaxConnections    int
	MaxConcurrent     int
}

type Command struct {
	Timeout        time.Duration
	MaxStdoutBytes int64
	MaxStderrBytes int64
}

type Jobs struct {
	MaxSavedJobs           int
	DefaultTimeout         time.Duration
	MaximumTimeout         time.Duration
	TerminationGrace       time.Duration
	MaxStdoutBytes         int64
	MaxStderrBytes         int64
	MaximumStreamBytes     int64
	MaxRunnerFrameBytes    int64
	MaxIdentifierBytes     int
	MaxPathBytes           int
	MaxCommandBytes        int
	MaxArguments           int
	MaxArgumentBytes       int
	MaxEnvironment         int
	MaxEnvironmentBytes    int
	MaxQueued              int
	MaxConcurrent          int
	RetainedRunsPerJob     int
	RetainedRunsTotal      int
	MaxRetainedOutputBytes int64
}

type Polling struct {
	BoardInterval           time.Duration
	MinimumInterval         time.Duration
	TelemetryStale          time.Duration
	BrowserInterval         time.Duration
	RegisterSnapshotTimeout time.Duration
}

type Persistence struct {
	MaxSnapshotBytes     int
	ActivitySegmentBytes int64
	ActivitySegments     int
	ActivityEventBytes   int
}

type Authentication struct {
	MaxSessions      int
	IdleLifetime     time.Duration
	AbsoluteLifetime time.Duration
	SetupLifetime    time.Duration
}

func Default() Limits {
	return Limits{
		HTTP: HTTP{
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			ShutdownTimeout:   5 * time.Second,
			MaxHeaderBytes:    16 << 10,
			MaxBodyBytes:      64 << 10,
			MaxConnections:    32,
			MaxConcurrent:     16,
		},
		Vendor: Command{Timeout: 3 * time.Second, MaxStdoutBytes: 4 << 10, MaxStderrBytes: 4 << 10},
		Power:  Command{Timeout: 10 * time.Second, MaxStdoutBytes: 4 << 10, MaxStderrBytes: 4 << 10},
		Jobs: Jobs{
			MaxSavedJobs:           64,
			DefaultTimeout:         15 * time.Minute,
			MaximumTimeout:         24 * time.Hour,
			TerminationGrace:       5 * time.Second,
			MaxStdoutBytes:         1 << 20,
			MaxStderrBytes:         1 << 20,
			MaximumStreamBytes:     8 << 20,
			MaxRunnerFrameBytes:    64 << 10,
			MaxIdentifierBytes:     128,
			MaxPathBytes:           4 << 10,
			MaxCommandBytes:        16 << 10,
			MaxArguments:           128,
			MaxArgumentBytes:       16 << 10,
			MaxEnvironment:         64,
			MaxEnvironmentBytes:    32 << 10,
			MaxQueued:              8,
			MaxConcurrent:          1,
			RetainedRunsPerJob:     20,
			RetainedRunsTotal:      200,
			MaxRetainedOutputBytes: 64 << 20,
		},
		Polling: Polling{
			BoardInterval:           10 * time.Second,
			MinimumInterval:         2 * time.Second,
			TelemetryStale:          30 * time.Second,
			BrowserInterval:         10 * time.Second,
			RegisterSnapshotTimeout: 10 * time.Second,
		},
		Persistence: Persistence{
			MaxSnapshotBytes:     DefaultMaxSnapshotBytes,
			ActivitySegmentBytes: DefaultActivitySegmentBytes,
			ActivitySegments:     DefaultActivitySegments,
			ActivityEventBytes:   DefaultActivityEventBytes,
		},
		Authentication: Authentication{
			MaxSessions:      16,
			IdleLifetime:     12 * time.Hour,
			AbsoluteLifetime: 7 * 24 * time.Hour,
			SetupLifetime:    30 * time.Minute,
		},
		ControllerSoftMemory: 64 << 20,
		ControllerMemory:     128 << 20,
		RunnerMemory:         256 << 20,
		RunnerTasks:          64,
	}
}

func (limits Limits) Validate() error {
	if limits.HTTP.ReadHeaderTimeout <= 0 || limits.HTTP.ReadTimeout < limits.HTTP.ReadHeaderTimeout || limits.HTTP.WriteTimeout <= 0 || limits.HTTP.IdleTimeout <= 0 || limits.HTTP.ShutdownTimeout <= 0 {
		return fmt.Errorf("invalid HTTP timeouts")
	}
	if limits.HTTP.MaxHeaderBytes < 8<<10 || limits.HTTP.MaxBodyBytes < 8<<10 || limits.HTTP.MaxConnections < 1 || limits.HTTP.MaxConcurrent < 1 || limits.HTTP.MaxConcurrent > limits.HTTP.MaxConnections {
		return fmt.Errorf("invalid HTTP size or concurrency limits")
	}
	if err := validateCommand("vendor", limits.Vendor); err != nil {
		return err
	}
	if err := validateCommand("power", limits.Power); err != nil {
		return err
	}
	if limits.Jobs.DefaultTimeout <= 0 || limits.Jobs.DefaultTimeout > limits.Jobs.MaximumTimeout || limits.Jobs.MaximumTimeout > 24*time.Hour || limits.Jobs.TerminationGrace <= 0 {
		return fmt.Errorf("invalid job timeouts")
	}
	if limits.Jobs.MaxStdoutBytes <= 0 || limits.Jobs.MaxStderrBytes <= 0 || limits.Jobs.MaximumStreamBytes < limits.Jobs.MaxStdoutBytes || limits.Jobs.MaximumStreamBytes < limits.Jobs.MaxStderrBytes || limits.Jobs.MaxRetainedOutputBytes <= 0 {
		return fmt.Errorf("invalid job output limits")
	}
	if limits.Jobs.MaxSavedJobs < 1 || limits.Jobs.MaxRunnerFrameBytes <= 0 || limits.Jobs.MaxIdentifierBytes <= 0 || limits.Jobs.MaxPathBytes <= 0 || limits.Jobs.MaxCommandBytes <= 0 || limits.Jobs.MaxArguments <= 0 || limits.Jobs.MaxArgumentBytes <= 0 || limits.Jobs.MaxEnvironment <= 0 || limits.Jobs.MaxEnvironmentBytes <= 0 || limits.Jobs.MaxQueued < 1 || limits.Jobs.MaxConcurrent != 1 || limits.Jobs.RetainedRunsPerJob < 1 || limits.Jobs.RetainedRunsTotal < limits.Jobs.RetainedRunsPerJob {
		return fmt.Errorf("invalid job execution or retention limits")
	}
	minimumFrame := int64(limits.Jobs.MaxEnvironmentBytes + limits.Jobs.MaxArgumentBytes + limits.Jobs.MaxPathBytes + (8 << 10))
	if limits.Jobs.MaxRunnerFrameBytes < minimumFrame {
		return fmt.Errorf("job runner frame is too small for bounded request fields")
	}
	if limits.Polling.MinimumInterval <= 0 || limits.Polling.BoardInterval < limits.Polling.MinimumInterval || limits.Polling.BrowserInterval < limits.Polling.MinimumInterval || limits.Polling.TelemetryStale < limits.Polling.BoardInterval || limits.Polling.RegisterSnapshotTimeout <= 0 || limits.Polling.RegisterSnapshotTimeout > limits.HTTP.WriteTimeout {
		return fmt.Errorf("invalid polling limits")
	}
	if limits.Persistence.MaxSnapshotBytes <= 0 || limits.Persistence.ActivitySegmentBytes <= 0 || limits.Persistence.ActivitySegments < 2 || limits.Persistence.ActivityEventBytes <= 0 || int64(limits.Persistence.ActivityEventBytes) > limits.Persistence.ActivitySegmentBytes {
		return fmt.Errorf("invalid persistence limits")
	}
	if limits.Authentication.MaxSessions < 1 || limits.Authentication.IdleLifetime <= 0 || limits.Authentication.AbsoluteLifetime < limits.Authentication.IdleLifetime || limits.Authentication.SetupLifetime <= 0 {
		return fmt.Errorf("invalid authentication limits")
	}
	if limits.ControllerSoftMemory <= 0 || limits.ControllerSoftMemory > limits.ControllerMemory || limits.ControllerMemory < 64<<20 || limits.RunnerMemory < limits.ControllerMemory || limits.RunnerTasks < 8 {
		return fmt.Errorf("invalid service resource limits")
	}
	return nil
}

func validateCommand(name string, command Command) error {
	if command.Timeout <= 0 || command.MaxStdoutBytes <= 0 || command.MaxStderrBytes <= 0 {
		return fmt.Errorf("invalid %s command limits", name)
	}
	return nil
}
