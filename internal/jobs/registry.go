package jobs

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/storage"
)

var ErrRunIDConflict = errors.New("run ID already has different start settings")
var ErrRunNotFound = errors.New("run not found")
var ErrRunQueueFull = errors.New("job runner queue is full")
var ErrRunState = errors.New("invalid run state transition")
var ErrJobActive = errors.New("saved job already has an active run")

type RunRecord struct {
	RunID             string            `json:"run_id"`
	JobID             string            `json:"job_id"`
	JobRevision       uint64            `json:"job_revision"`
	State             RunState          `json:"state"`
	QueuedUnixMilli   int64             `json:"queued_unix_milli"`
	StartedUnixMilli  int64             `json:"started_unix_milli,omitempty"`
	FinishedUnixMilli int64             `json:"finished_unix_milli,omitempty"`
	ExitCode          *int              `json:"exit_code,omitempty"`
	Signal            string            `json:"signal,omitempty"`
	StdoutTruncated   bool              `json:"stdout_truncated,omitempty"`
	StderrTruncated   bool              `json:"stderr_truncated,omitempty"`
	StdoutTotal       int64             `json:"stdout_total,omitempty"`
	StderrTotal       int64             `json:"stderr_total,omitempty"`
	StartDigest       [sha256.Size]byte `json:"start_digest"`
}

type RunResult struct {
	State           RunState
	ExitCode        *int
	Signal          string
	StdoutTruncated bool
	StderrTruncated bool
	StdoutTotal     int64
	StderrTotal     int64
}

// Registry owns accepted run identities. It makes start retry-safe before a
// future executor is allowed to dequeue and launch work.
type Registry struct {
	mu       sync.Mutex
	limits   limits.Jobs
	queued   int
	now      func() time.Time
	runs     map[string]RunRecord
	store    *storage.Store
	revision uint64
}

func NewRegistry(jobLimits limits.Jobs) (*Registry, error) {
	if jobLimits.MaxQueued < 1 {
		return nil, fmt.Errorf("max queued runs must be positive")
	}
	return &Registry{limits: jobLimits, now: time.Now, runs: make(map[string]RunRecord)}, nil
}

type registrySnapshot struct {
	Runs []RunRecord `json:"runs"`
}

// NewPersistentRegistry loads the crash-safe runner registry. Any non-terminal
// record from a previous process is persisted as lost before the registry is
// returned; it is never queued again automatically.
func NewPersistentRegistry(dir string, jobLimits limits.Jobs) (*Registry, error) {
	registry, err := NewRegistry(jobLimits)
	if err != nil {
		return nil, err
	}
	// The spool root is a setgid directory shared with the controller solely so
	// it can traverse into bounded output. Registry snapshots remain 0600, but
	// saving them must not collapse the spool root back to a private 0700 mode.
	registry.store, err = storage.NewWithDirectoryMode(dir, "runs", 0o750|os.ModeSetgid)
	if err != nil {
		return nil, err
	}
	snapshot, err := registry.store.Load()
	if errors.Is(err, storage.ErrNotFound) {
		return registry, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load runner registry: %w", err)
	}
	stored, err := decodeRegistrySnapshot(snapshot.Payload)
	if err != nil {
		return nil, err
	}
	if len(stored.Runs) > jobLimits.RetainedRunsTotal {
		return nil, fmt.Errorf("runner registry has %d records; maximum is %d", len(stored.Runs), jobLimits.RetainedRunsTotal)
	}
	changed := false
	for _, record := range stored.Runs {
		if !validRunID(record.RunID) || record.JobID == "" || record.JobRevision == 0 || !validRunState(record.State) || record.QueuedUnixMilli <= 0 || record.StartDigest == [sha256.Size]byte{} {
			return nil, fmt.Errorf("invalid persisted runner record %q", record.RunID)
		}
		if _, duplicate := registry.runs[record.RunID]; duplicate {
			return nil, fmt.Errorf("duplicate persisted runner record %q", record.RunID)
		}
		if record.State == StateQueued || record.State == StateRunning {
			record.State = StateLost
			changed = true
		}
		registry.runs[record.RunID] = record
	}
	registry.revision = snapshot.Revision
	if changed {
		if err := registry.saveLocked(); err != nil {
			return nil, fmt.Errorf("persist lost runner records: %w", err)
		}
	}
	return registry, nil
}

// Start accepts one previously validated start request. An identical retry is
// returned as existing even when the queue is currently full.
func (registry *Registry) Start(request Request) (RunRecord, bool, error) {
	if err := request.Validate(registry.limits); err != nil {
		return RunRecord{}, false, err
	}
	if request.Action != ActionStart {
		return RunRecord{}, false, fmt.Errorf("%w: registry start requires start action", ErrInvalidRequest)
	}
	digest, err := startDigest(*request.Start)
	if err != nil {
		return RunRecord{}, false, err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if existing, ok := registry.runs[request.RunID]; ok {
		if existing.StartDigest != digest {
			return RunRecord{}, false, ErrRunIDConflict
		}
		return existing, false, nil
	}
	for _, existing := range registry.runs {
		if existing.JobID == request.Start.JobID && !terminalState(existing.State) {
			return RunRecord{}, false, ErrJobActive
		}
	}
	if registry.queued >= registry.limits.MaxQueued {
		return RunRecord{}, false, ErrRunQueueFull
	}
	if len(registry.runs) >= registry.limits.RetainedRunsTotal {
		return RunRecord{}, false, fmt.Errorf("runner registry retention limit reached")
	}
	record := RunRecord{
		RunID:           request.RunID,
		JobID:           request.Start.JobID,
		JobRevision:     request.Start.JobRevision,
		State:           StateQueued,
		QueuedUnixMilli: registry.now().UTC().UnixMilli(),
		StartDigest:     digest,
	}
	registry.runs[request.RunID] = record
	registry.queued++
	if err := registry.saveLocked(); err != nil {
		delete(registry.runs, request.RunID)
		registry.queued--
		return RunRecord{}, false, fmt.Errorf("persist accepted run: %w", err)
	}
	return record, true, nil
}

func (registry *Registry) Status(runID string) (RunRecord, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	record, ok := registry.runs[runID]
	if !ok {
		return RunRecord{}, ErrRunNotFound
	}
	return record, nil
}

func (registry *Registry) Cancel(runID string) (RunRecord, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	record, ok := registry.runs[runID]
	if !ok {
		return RunRecord{}, ErrRunNotFound
	}
	if record.State == StateQueued {
		previous := record
		record.State = StateCancelled
		registry.runs[runID] = record
		registry.queued--
		if err := registry.saveLocked(); err != nil {
			registry.runs[runID] = previous
			registry.queued++
			return RunRecord{}, fmt.Errorf("persist cancelled run: %w", err)
		}
	}
	return record, nil
}

func (registry *Registry) MarkRunning(request Request) (RunRecord, error) {
	if err := request.Validate(registry.limits); err != nil {
		return RunRecord{}, err
	}
	if request.Action != ActionStart {
		return RunRecord{}, fmt.Errorf("%w: running transition requires start action", ErrInvalidRequest)
	}
	digest, err := startDigest(*request.Start)
	if err != nil {
		return RunRecord{}, err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	record, ok := registry.runs[request.RunID]
	if !ok {
		return RunRecord{}, ErrRunNotFound
	}
	if record.StartDigest != digest {
		return RunRecord{}, ErrRunIDConflict
	}
	if record.State != StateQueued {
		return RunRecord{}, fmt.Errorf("%w: cannot run from %s", ErrRunState, record.State)
	}
	previous := record
	record.State = StateRunning
	record.StartedUnixMilli = registry.now().UTC().UnixMilli()
	registry.runs[request.RunID] = record
	registry.queued--
	if err := registry.saveLocked(); err != nil {
		registry.runs[request.RunID] = previous
		registry.queued++
		return RunRecord{}, fmt.Errorf("persist running run: %w", err)
	}
	return record, nil
}

func (registry *Registry) Finish(runID string, result RunResult) (RunRecord, error) {
	if !terminalState(result.State) || result.StdoutTotal < 0 || result.StderrTotal < 0 {
		return RunRecord{}, fmt.Errorf("%w: invalid final result", ErrRunState)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	record, ok := registry.runs[runID]
	if !ok {
		return RunRecord{}, ErrRunNotFound
	}
	if record.State != StateRunning {
		return RunRecord{}, fmt.Errorf("%w: cannot finish from %s", ErrRunState, record.State)
	}
	previous := record
	record.State = result.State
	record.FinishedUnixMilli = registry.now().UTC().UnixMilli()
	record.ExitCode = result.ExitCode
	record.Signal = result.Signal
	record.StdoutTruncated = result.StdoutTruncated
	record.StderrTruncated = result.StderrTruncated
	record.StdoutTotal = result.StdoutTotal
	record.StderrTotal = result.StderrTotal
	registry.runs[runID] = record
	if err := registry.saveLocked(); err != nil {
		registry.runs[runID] = previous
		return RunRecord{}, fmt.Errorf("persist final run: %w", err)
	}
	return record, nil
}

func terminalState(state RunState) bool {
	switch state {
	case StateSucceeded, StateFailed, StateTimedOut, StateCancelled, StateLost:
		return true
	default:
		return false
	}
}

func (registry *Registry) saveLocked() error {
	if registry.store == nil {
		return nil
	}
	runIDs := make([]string, 0, len(registry.runs))
	for runID := range registry.runs {
		runIDs = append(runIDs, runID)
	}
	sort.Strings(runIDs)
	snapshot := registrySnapshot{Runs: make([]RunRecord, 0, len(runIDs))}
	for _, runID := range runIDs {
		snapshot.Runs = append(snapshot.Runs, registry.runs[runID])
	}
	saved, err := registry.store.Save(registry.revision, snapshot)
	if err != nil {
		return err
	}
	registry.revision = saved.Revision
	return nil
}

func decodeRegistrySnapshot(payload []byte) (registrySnapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var snapshot registrySnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return registrySnapshot{}, fmt.Errorf("decode runner registry: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return registrySnapshot{}, fmt.Errorf("decode runner registry: trailing data")
	}
	return snapshot, nil
}

func startDigest(request StartRequest) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode start settings: %w", err)
	}
	return sha256.Sum256(payload), nil
}
