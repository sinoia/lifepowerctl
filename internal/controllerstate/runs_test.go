package controllerstate

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
)

func TestControllerRunsPersistAndRequireObservation(t *testing.T) {
	root := t.TempDir()
	jobLimits := limits.Default().Jobs
	manager, err := Open(filepath.Join(root, "state"), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	request := jobs.Request{Version: jobs.ProtocolVersion, Action: jobs.ActionStart, RunID: "00112233445566778899aabbccddeeff", Start: &jobs.StartRequest{
		JobID: "adhoc-00112233445566778899aabbccddeeff", JobRevision: 1, Mode: jobs.ModeDirect, Executable: "/bin/true", WorkingDirectory: "/tmp", TimeoutSeconds: 30, StdoutBytes: 1024, StderrBytes: 1024,
	}}
	if _, err := manager.BeginRun(request, "adhoc", true); err != nil {
		t.Fatal(err)
	}
	if len(manager.RunsNeedingPoll()) != 1 || len(manager.RunsNeedingObservation()) != 1 {
		t.Fatal("new run was not pending poll and observation")
	}
	exit := 0
	response := jobs.Response{Version: jobs.ProtocolVersion, RunID: request.RunID, State: jobs.StateSucceeded, ExitCode: &exit, StdoutTotal: 4}
	if _, changed, err := manager.UpdateRun(response); err != nil || !changed {
		t.Fatalf("UpdateRun changed = %v, error = %v", changed, err)
	}
	if err := manager.MarkRunObserved(request.RunID, jobs.StateSucceeded); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filepath.Join(root, "state"), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.RunsNeedingPoll()) != 0 || len(reopened.RunsNeedingObservation()) != 0 {
		t.Fatal("terminal observed run was not recovered correctly")
	}
}

func TestControllerRunHistoryEnforcesPerJobRetention(t *testing.T) {
	jobLimits := limits.Default().Jobs
	jobLimits.RetainedRunsPerJob = 2
	jobLimits.RetainedRunsTotal = 3
	manager, err := Open(t.TempDir(), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		runID := fmt.Sprintf("%032x", index+1)
		request := jobs.Request{Version: jobs.ProtocolVersion, Action: jobs.ActionStart, RunID: runID, Start: &jobs.StartRequest{JobID: "job", JobRevision: 1, Mode: jobs.ModeDirect, Executable: "/bin/true", WorkingDirectory: "/tmp", TimeoutSeconds: 30, StdoutBytes: 1024, StderrBytes: 1024}}
		if _, err := manager.BeginRun(request, "job", false); err != nil {
			t.Fatal(err)
		}
		exit := 0
		if _, _, err := manager.UpdateRun(jobs.Response{Version: jobs.ProtocolVersion, RunID: runID, State: jobs.StateSucceeded, ExitCode: &exit}); err != nil {
			t.Fatal(err)
		}
	}
	runs := manager.Runs(3, "job")
	if len(runs) != 2 || runs[0].RunID != fmt.Sprintf("%032x", 3) || runs[1].RunID != fmt.Sprintf("%032x", 2) {
		t.Fatalf("retained runs = %+v", runs)
	}
}

func TestControllerRunHistoryNormalizesLegacyTriggers(t *testing.T) {
	manual := cloneRun(Run{})
	adHoc := cloneRun(Run{AdHoc: true})
	if manual.Trigger != RunTriggerManual || adHoc.Trigger != RunTriggerAdHoc {
		t.Fatalf("legacy triggers = %q, %q", manual.Trigger, adHoc.Trigger)
	}
}
