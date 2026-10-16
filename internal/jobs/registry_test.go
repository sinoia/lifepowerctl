package jobs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

func TestRegistryStartIsIdempotent(t *testing.T) {
	registry, err := NewRegistry(limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	registry.now = func() time.Time { return time.UnixMilli(1234) }
	request := validStartRequest()
	first, created, err := registry.Start(request)
	if err != nil || !created {
		t.Fatalf("first Start() = %+v, %v, %v", first, created, err)
	}
	second, created, err := registry.Start(request)
	if err != nil || created || second != first {
		t.Fatalf("retry Start() = %+v, %v, %v", second, created, err)
	}
	if first.State != StateQueued || first.QueuedUnixMilli != 1234 {
		t.Fatalf("first record = %+v", first)
	}
}

func TestPersistentRegistryMarksQueuedRunLost(t *testing.T) {
	dir := t.TempDir()
	jobLimits := limits.Default().Jobs
	registry, err := NewPersistentRegistry(dir, jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	request := validStartRequest()
	accepted, created, err := registry.Start(request)
	if err != nil || !created || accepted.State != StateQueued {
		t.Fatalf("Start() = %+v, %v, %v", accepted, created, err)
	}

	restarted, err := NewPersistentRegistry(dir, jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	lost, err := restarted.Status(request.RunID)
	if err != nil || lost.State != StateLost {
		t.Fatalf("restarted Status() = %+v, %v", lost, err)
	}
	retried, created, err := restarted.Start(request)
	if err != nil || created || retried.State != StateLost {
		t.Fatalf("restarted Start() = %+v, %v, %v", retried, created, err)
	}

	secondRestart, err := NewPersistentRegistry(dir, jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	stillLost, err := secondRestart.Status(request.RunID)
	if err != nil || stillLost.State != StateLost {
		t.Fatalf("second restart Status() = %+v, %v", stillLost, err)
	}
	info, err := os.Stat(filepath.Join(dir, "runs.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("runner registry permissions = %v, %v", info.Mode().Perm(), err)
	}
	directory, err := os.Stat(dir)
	if err != nil || directory.Mode().Perm() != 0o750 || directory.Mode()&os.ModeSetgid == 0 {
		t.Fatalf("runner spool permissions = %v, %v", directory.Mode(), err)
	}
}

func TestPersistentRegistryKeepsCancelledRunTerminal(t *testing.T) {
	dir := t.TempDir()
	jobLimits := limits.Default().Jobs
	registry, err := NewPersistentRegistry(dir, jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	request := validStartRequest()
	if _, _, err := registry.Start(request); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Cancel(request.RunID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentRegistry(dir, jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	record, err := restarted.Status(request.RunID)
	if err != nil || record.State != StateCancelled {
		t.Fatalf("restarted Status() = %+v, %v", record, err)
	}
}

func TestPersistentRegistryReconcilesRunningAndKeepsFinished(t *testing.T) {
	jobLimits := limits.Default().Jobs

	runningDir := t.TempDir()
	running, err := NewPersistentRegistry(runningDir, jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	runningRequest := validStartRequest()
	if _, _, err := running.Start(runningRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := running.MarkRunning(runningRequest); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPersistentRegistry(runningDir, jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	reconciled, err := restarted.Status(runningRequest.RunID)
	if err != nil || reconciled.State != StateLost {
		t.Fatalf("running restart Status() = %+v, %v", reconciled, err)
	}

	finishedDir := t.TempDir()
	finished, err := NewPersistentRegistry(finishedDir, jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	finishedRequest := validStartRequest()
	if _, _, err := finished.Start(finishedRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := finished.MarkRunning(finishedRequest); err != nil {
		t.Fatal(err)
	}
	exitCode := 0
	if _, err := finished.Finish(finishedRequest.RunID, RunResult{State: StateSucceeded, ExitCode: &exitCode, StdoutTotal: 12, StderrTotal: 3}); err != nil {
		t.Fatal(err)
	}
	finishedRestart, err := NewPersistentRegistry(finishedDir, jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := finishedRestart.Status(finishedRequest.RunID)
	if err != nil || terminal.State != StateSucceeded || terminal.ExitCode == nil || *terminal.ExitCode != 0 || terminal.StdoutTotal != 12 || terminal.StderrTotal != 3 {
		t.Fatalf("finished restart Status() = %+v, %v", terminal, err)
	}
}

func TestRegistryRejectsRunIDReuseWithDifferentSettings(t *testing.T) {
	registry, err := NewRegistry(limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	request := validStartRequest()
	if _, _, err := registry.Start(request); err != nil {
		t.Fatal(err)
	}
	request.Start.Arguments = []string{"--different"}
	if _, _, err := registry.Start(request); !errors.Is(err, ErrRunIDConflict) {
		t.Fatalf("conflicting Start() error = %v", err)
	}
	if _, err := registry.MarkRunning(request); !errors.Is(err, ErrRunIDConflict) {
		t.Fatalf("conflicting MarkRunning() error = %v", err)
	}
}

func TestRegistryQueueAndCancel(t *testing.T) {
	jobLimits := limits.Default().Jobs
	jobLimits.MaxQueued = 1
	registry, err := NewRegistry(jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	firstRequest := validStartRequest()
	first, _, err := registry.Start(firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := validStartRequest()
	secondRequest.RunID = "11112222333344445555666677778888"
	secondRequest.Start.JobID = "second-job"
	if _, _, err := registry.Start(secondRequest); !errors.Is(err, ErrRunQueueFull) {
		t.Fatalf("full Start() error = %v", err)
	}
	cancelled, err := registry.Cancel(first.RunID)
	if err != nil || cancelled.State != StateCancelled {
		t.Fatalf("Cancel() = %+v, %v", cancelled, err)
	}
	if _, created, err := registry.Start(secondRequest); err != nil || !created {
		t.Fatalf("Start() after cancel created = %v, error = %v", created, err)
	}
	status, err := registry.Status(secondRequest.RunID)
	if err != nil || status.State != StateQueued {
		t.Fatalf("Status() = %+v, %v", status, err)
	}
	if _, err := registry.Status("ffffffffffffffffffffffffffffffff"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("missing Status() error = %v", err)
	}
}

func TestRegistryAllowsOnlyOneActiveRunPerSavedJob(t *testing.T) {
	registry, err := NewRegistry(limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	first := validStartRequest()
	if _, _, err := registry.Start(first); err != nil {
		t.Fatal(err)
	}
	second := validStartRequest()
	second.RunID = "11112222333344445555666677778888"
	if _, _, err := registry.Start(second); !errors.Is(err, ErrJobActive) {
		t.Fatalf("second active Start() error = %v", err)
	}
	if _, err := registry.Cancel(first.RunID); err != nil {
		t.Fatal(err)
	}
	if _, created, err := registry.Start(second); err != nil || !created {
		t.Fatalf("Start() after terminal state created = %v, error = %v", created, err)
	}
}
