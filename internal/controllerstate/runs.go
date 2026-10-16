package controllerstate

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
)

var ErrRunNotFound = errors.New("controller run not found")
var ErrRunLimit = errors.New("controller run retention limit reached")

type RunTrigger string

const (
	RunTriggerManual    RunTrigger = "manual"
	RunTriggerAdHoc     RunTrigger = "ad-hoc"
	RunTriggerScheduled RunTrigger = "scheduled"
)

type Run struct {
	RunID           string        `json:"run_id"`
	JobID           string        `json:"job_id"`
	DisplayJobID    string        `json:"display_job_id"`
	JobRevision     uint64        `json:"job_revision"`
	AdHoc           bool          `json:"ad_hoc"`
	Trigger         RunTrigger    `json:"trigger,omitempty"`
	State           jobs.RunState `json:"state"`
	ObservedState   jobs.RunState `json:"observed_state,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
	QueuedAt        time.Time     `json:"queued_at,omitempty"`
	StartedAt       time.Time     `json:"started_at,omitempty"`
	FinishedAt      time.Time     `json:"finished_at,omitempty"`
	Occurrence      uint32        `json:"occurrence,omitempty"`
	RunListPosition int           `json:"run_list_position,omitempty"`
	ExitCode        *int          `json:"exit_code,omitempty"`
	Signal          string        `json:"signal,omitempty"`
	StdoutTruncated bool          `json:"stdout_truncated,omitempty"`
	StderrTruncated bool          `json:"stderr_truncated,omitempty"`
	StdoutTotal     int64         `json:"stdout_total"`
	StderrTotal     int64         `json:"stderr_total"`
}

func (manager *Manager) BeginRun(request jobs.Request, displayJobID string, adHoc bool) (Run, error) {
	if request.Action != jobs.ActionStart || request.Start == nil || request.Validate(manager.limits) != nil || displayJobID == "" {
		return Run{}, fmt.Errorf("invalid controller run")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.runIndexLocked(request.RunID) >= 0 {
		return Run{}, fmt.Errorf("duplicate controller run ID")
	}
	previousRuns := append([]Run(nil), manager.state.Runs...)
	now := manager.now().UTC()
	trigger := RunTriggerManual
	if adHoc {
		trigger = RunTriggerAdHoc
	}
	run := Run{RunID: request.RunID, JobID: request.Start.JobID, DisplayJobID: displayJobID, JobRevision: request.Start.JobRevision, AdHoc: adHoc, Trigger: trigger, State: jobs.StateQueued, CreatedAt: now, UpdatedAt: now}
	manager.state.Runs = append(manager.state.Runs, run)
	manager.pruneRunsLocked()
	if len(manager.state.Runs) > manager.limits.RetainedRunsTotal {
		manager.state.Runs = previousRuns
		return Run{}, ErrRunLimit
	}
	if err := manager.saveLocked(); err != nil {
		manager.state.Runs = previousRuns
		return Run{}, fmt.Errorf("persist controller run: %w", err)
	}
	return run, nil
}

func (manager *Manager) UpdateRun(response jobs.Response) (Run, bool, error) {
	if err := response.Validate(); err != nil {
		return Run{}, false, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	index := manager.runIndexLocked(response.RunID)
	if index < 0 {
		return Run{}, false, ErrRunNotFound
	}
	previous := manager.state.Runs[index]
	updated := previous
	applyRunResponse(&updated, response, manager.now().UTC())
	changed := !runsEqual(previous, updated)
	if !changed {
		return previous, false, nil
	}
	manager.state.Runs[index] = updated
	if err := manager.saveLocked(); err != nil {
		manager.state.Runs[index] = previous
		return Run{}, false, fmt.Errorf("persist controller run state: %w", err)
	}
	return updated, true, nil
}

func (manager *Manager) Runs(limit int, jobID string) []Run {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if limit <= 0 || limit > manager.limits.RetainedRunsTotal {
		return nil
	}
	result := make([]Run, 0, min(limit, len(manager.state.Runs)))
	for index := len(manager.state.Runs) - 1; index >= 0 && len(result) < limit; index-- {
		run := manager.state.Runs[index]
		if jobID != "" && run.DisplayJobID != jobID {
			continue
		}
		result = append(result, cloneRun(run))
	}
	return result
}

func (manager *Manager) ActiveRunCount() int {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	count := 0
	for _, run := range manager.state.Runs {
		if !terminalRunState(run.State) {
			count++
		}
	}
	return count
}

func (manager *Manager) Run(runID string) (Run, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	index := manager.runIndexLocked(runID)
	if index < 0 {
		return Run{}, ErrRunNotFound
	}
	return cloneRun(manager.state.Runs[index]), nil
}

func (manager *Manager) MarkRunLost(runID string) (Run, error) {
	run, _, err := manager.UpdateRun(jobs.Response{Version: jobs.ProtocolVersion, RunID: runID, State: jobs.StateLost})
	return run, err
}

func (manager *Manager) MarkRunObserved(runID string, state jobs.RunState) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	index := manager.runIndexLocked(runID)
	if index < 0 {
		return ErrRunNotFound
	}
	if manager.state.Runs[index].State != state {
		return fmt.Errorf("controller run state changed before observation")
	}
	if manager.state.Runs[index].ObservedState == state {
		return nil
	}
	previous := manager.state.Runs[index]
	manager.state.Runs[index].ObservedState = state
	if err := manager.saveLocked(); err != nil {
		manager.state.Runs[index] = previous
		return err
	}
	return nil
}

func (manager *Manager) RunsNeedingPoll() []Run {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	result := make([]Run, 0)
	for _, run := range manager.state.Runs {
		if !terminalRunState(run.State) {
			result = append(result, run)
		}
	}
	return result
}

func (manager *Manager) RunsNeedingObservation() []Run {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	result := make([]Run, 0)
	for _, run := range manager.state.Runs {
		if run.ObservedState != run.State {
			result = append(result, run)
		}
	}
	return result
}

func (manager *Manager) runIndexLocked(runID string) int {
	for index := range manager.state.Runs {
		if manager.state.Runs[index].RunID == runID {
			return index
		}
	}
	return -1
}

func (manager *Manager) pruneRunsLocked() {
	counts := make(map[string]int)
	for index := len(manager.state.Runs) - 1; index >= 0; index-- {
		run := manager.state.Runs[index]
		counts[run.DisplayJobID]++
		if counts[run.DisplayJobID] > manager.limits.RetainedRunsPerJob && terminalRunState(run.State) {
			manager.state.Runs = slices.Delete(manager.state.Runs, index, index+1)
		}
	}
	for len(manager.state.Runs) > manager.limits.RetainedRunsTotal {
		index := slices.IndexFunc(manager.state.Runs, func(run Run) bool { return terminalRunState(run.State) })
		if index < 0 {
			return
		}
		manager.state.Runs = slices.Delete(manager.state.Runs, index, index+1)
	}
}

func applyRunResponse(run *Run, response jobs.Response, now time.Time) {
	run.State, run.UpdatedAt = response.State, now
	run.ExitCode, run.Signal = response.ExitCode, response.Signal
	run.StdoutTruncated, run.StderrTruncated = response.StdoutTruncated, response.StderrTruncated
	run.StdoutTotal, run.StderrTotal = response.StdoutTotal, response.StderrTotal
	if response.QueuedUnixMilli > 0 {
		run.QueuedAt = time.UnixMilli(response.QueuedUnixMilli).UTC()
	}
	if response.StartedUnixMilli > 0 {
		run.StartedAt = time.UnixMilli(response.StartedUnixMilli).UTC()
	}
	if response.FinishedUnixMilli > 0 {
		run.FinishedAt = time.UnixMilli(response.FinishedUnixMilli).UTC()
	}
}

func runsEqual(first, second Run) bool {
	first.UpdatedAt, second.UpdatedAt = time.Time{}, time.Time{}
	return first.RunID == second.RunID && first.State == second.State && first.QueuedAt.Equal(second.QueuedAt) && first.StartedAt.Equal(second.StartedAt) && first.FinishedAt.Equal(second.FinishedAt) && first.Signal == second.Signal && equalExitCode(first.ExitCode, second.ExitCode) && first.StdoutTruncated == second.StdoutTruncated && first.StderrTruncated == second.StderrTruncated && first.StdoutTotal == second.StdoutTotal && first.StderrTotal == second.StderrTotal
}

func equalExitCode(first, second *int) bool {
	return first == nil && second == nil || first != nil && second != nil && *first == *second
}

func cloneRun(run Run) Run {
	if run.Trigger == "" {
		run.Trigger = RunTriggerManual
		if run.AdHoc {
			run.Trigger = RunTriggerAdHoc
		}
	}
	if run.ExitCode != nil {
		value := *run.ExitCode
		run.ExitCode = &value
	}
	return run
}

func terminalRunState(state jobs.RunState) bool {
	switch state {
	case jobs.StateSucceeded, jobs.StateFailed, jobs.StateTimedOut, jobs.StateCancelled, jobs.StateLost:
		return true
	default:
		return false
	}
}

func validateRuns(runs []Run, jobLimits limits.Jobs) error {
	if len(runs) > jobLimits.RetainedRunsTotal {
		return fmt.Errorf("controller state has too many retained runs")
	}
	ids := make(map[string]struct{}, len(runs))
	for _, run := range runs {
		request := jobs.Request{Version: jobs.ProtocolVersion, Action: jobs.ActionStatus, RunID: run.RunID}
		response := jobs.Response{Version: jobs.ProtocolVersion, RunID: run.RunID, State: run.State}
		observedValid := run.ObservedState == "" || (jobs.Response{Version: jobs.ProtocolVersion, RunID: run.RunID, State: run.ObservedState}).Validate() == nil
		if run.Trigger == "" {
			if run.AdHoc {
				run.Trigger = RunTriggerAdHoc
			} else {
				run.Trigger = RunTriggerManual
			}
		}
		validTrigger := run.Trigger == RunTriggerManual || run.Trigger == RunTriggerAdHoc || run.Trigger == RunTriggerScheduled
		validScheduled := run.Trigger != RunTriggerScheduled || run.Occurrence > 0 && run.RunListPosition > 0
		if request.Validate(jobLimits) != nil || response.Validate() != nil || !observedValid || !validTrigger || !validScheduled || run.JobID == "" || run.DisplayJobID == "" || run.JobRevision == 0 || run.CreatedAt.IsZero() || run.UpdatedAt.Before(run.CreatedAt) || run.StdoutTotal < 0 || run.StderrTotal < 0 {
			return fmt.Errorf("invalid controller run %q", run.RunID)
		}
		if _, duplicate := ids[run.RunID]; duplicate {
			return fmt.Errorf("duplicate controller run %q", run.RunID)
		}
		ids[run.RunID] = struct{}{}
	}
	return nil
}
