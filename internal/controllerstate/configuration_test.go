package controllerstate

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

const testProposalID = "00112233445566778899aabbccddeeff"

func TestDisabledConfigurationProposalIsRevisionBoundAndIdempotent(t *testing.T) {
	directory := t.TempDir()
	manager, err := Open(directory, limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	manager.random = bytes.NewReader(make([]byte, 16))
	job, err := manager.Create(testInput("Camera"))
	if err != nil {
		t.Fatal(err)
	}
	base := manager.Revision()
	configuration := schedule.Configuration{Revision: 1, Schedule: schedule.Specification{Kind: schedule.KindDisabled}, RunList: []schedule.RunListEntry{{JobID: job.ID, JobRevision: job.Revision}}}
	result, err := manager.SaveDisabledConfiguration(testProposalID, base, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if result.Existing || result.StateRevision != base+1 || result.ConfigurationRevision != 1 {
		t.Fatalf("result = %+v", result)
	}
	repeated, err := manager.SaveDisabledConfiguration(testProposalID, base, configuration)
	if err != nil || !repeated.Existing || repeated.StateRevision != result.StateRevision {
		t.Fatalf("repeated = %+v, %v", repeated, err)
	}
	status, err := manager.ConfigurationProposalResult(testProposalID)
	if err != nil || !status.Existing || status.ConfigurationRevision != 1 {
		t.Fatalf("status = %+v, %v", status, err)
	}

	reopened, err := Open(directory, limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := reopened.DutyCycleConfiguration()
	if !ok || stored.Revision != 1 || len(stored.RunList) != 1 {
		t.Fatalf("stored configuration = %+v, %v", stored, ok)
	}
	status, err = reopened.ConfigurationProposalResult(testProposalID)
	if err != nil || status.StateRevision != result.StateRevision {
		t.Fatalf("reopened status = %+v, %v", status, err)
	}
}

func TestDisabledConfigurationRejectsStaleOrConflictingProposal(t *testing.T) {
	manager, err := Open(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	configuration := schedule.Configuration{Revision: 1, Schedule: schedule.Specification{Kind: schedule.KindDisabled}}
	if _, err := manager.SaveDisabledConfiguration(testProposalID, 1, configuration); !errors.Is(err, ErrConfigurationRevision) {
		t.Fatalf("stale base error = %v", err)
	}
	result, err := manager.SaveDisabledConfiguration(testProposalID, 0, configuration)
	if err != nil {
		t.Fatal(err)
	}
	changed := configuration
	changed.RunList = []schedule.RunListEntry{{JobID: "missing", JobRevision: 1}}
	if _, err := manager.SaveDisabledConfiguration(testProposalID, 0, changed); !errors.Is(err, ErrProposalIDConflict) {
		t.Fatalf("proposal reuse error = %v", err)
	}
	if result.StateRevision != manager.Revision() {
		t.Fatalf("result revision %d, manager revision %d", result.StateRevision, manager.Revision())
	}
}

func TestDisabledConfigurationRejectsStaleJobReference(t *testing.T) {
	manager, err := Open(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	configuration := schedule.Configuration{Revision: 1, Schedule: schedule.Specification{Kind: schedule.KindDisabled}, RunList: []schedule.RunListEntry{{JobID: "00112233445566778899aabbccddeeff", JobRevision: 1}}}
	if _, err := manager.SaveDisabledConfiguration(testProposalID, 0, configuration); !errors.Is(err, ErrJobReference) {
		t.Fatalf("job reference error = %v", err)
	}
	if manager.Revision() != 0 {
		t.Fatalf("rejected proposal changed revision to %d", manager.Revision())
	}
}

func TestReferencedJobCannotChangeBehindConfigurationSnapshot(t *testing.T) {
	manager, err := Open(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	manager.random = bytes.NewReader(make([]byte, 16))
	job, err := manager.Create(testInput("Camera"))
	if err != nil {
		t.Fatal(err)
	}
	configuration := schedule.Configuration{Revision: 1, Schedule: schedule.Specification{Kind: schedule.KindDisabled}, RunList: []schedule.RunListEntry{{JobID: job.ID, JobRevision: job.Revision}}}
	if _, err := manager.SaveDisabledConfiguration(testProposalID, manager.Revision(), configuration); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Update(job.ID, job.Revision, testInput("Changed")); !errors.Is(err, ErrJobInRunList) {
		t.Fatalf("update error = %v", err)
	}
	if err := manager.Delete(job.ID, job.Revision); !errors.Is(err, ErrJobInRunList) {
		t.Fatalf("delete error = %v", err)
	}
}

func TestDutyCycleMaximumRuntimeUsesReferencedTimeoutsAndGrace(t *testing.T) {
	manager, err := Open(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	manager.random = bytes.NewReader(append(make([]byte, 16), bytes.Repeat([]byte{1}, 16)...))
	first, err := manager.Create(testInput("First"))
	if err != nil {
		t.Fatal(err)
	}
	secondInput := testInput("Second")
	secondInput.TimeoutSeconds = 90
	second, err := manager.Create(secondInput)
	if err != nil {
		t.Fatal(err)
	}
	configuration := schedule.Configuration{Revision: 1, Schedule: schedule.Specification{Kind: schedule.KindDaily, Timezone: "UTC", LocalMinute: 60}, RunList: []schedule.RunListEntry{{JobID: first.ID, JobRevision: first.Revision}, {JobID: second.ID, JobRevision: second.Revision}}}
	if _, err := manager.SaveDisabledConfiguration(testProposalID, manager.Revision(), configuration); err != nil {
		t.Fatal(err)
	}
	count, seconds, err := manager.DutyCycleRunListMaximum()
	if err != nil {
		t.Fatal(err)
	}
	want := uint64(60 + 90 + 2*int64(limits.Default().Jobs.TerminationGrace/time.Second))
	if count != 2 || seconds != want {
		t.Fatalf("maximum = %d jobs, %d seconds; want 2, %d", count, seconds, want)
	}
}
