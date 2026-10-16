package controllerstate

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
)

func testInput(name string) jobs.DefinitionInput {
	return jobs.DefinitionInput{
		Name: name, Enabled: true, Mode: jobs.ModeDirect, Executable: "/home/pi/bin/capture",
		Arguments: []string{"--once"}, WorkingDirectory: "/home/pi", Environment: map[string]string{"CAMERA": "front"},
		TimeoutSeconds: 60, StdoutBytes: 1 << 20, StderrBytes: 1 << 20,
	}
}

func TestSavedJobLifecyclePersistsRevisionsAndOwnsValues(t *testing.T) {
	directory := t.TempDir()
	manager, err := Open(directory, limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	if manager.Schema() != 0 {
		t.Fatalf("new manager schema = %d, want 0", manager.Schema())
	}
	now := time.Date(2026, time.October, 11, 9, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	manager.random = bytes.NewReader(make([]byte, 16))
	input := testInput("Take photograph")
	created, err := manager.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if manager.Schema() != 1 {
		t.Fatalf("saved manager schema = %d, want 1", manager.Schema())
	}
	if created.ID != "00000000000000000000000000000000" || created.Revision != 1 || !created.CreatedAt.Equal(now) {
		t.Fatalf("created job = %+v", created)
	}
	input.Arguments[0] = "changed"
	input.Environment["CAMERA"] = "changed"
	if stored := manager.Jobs()[0]; stored.Arguments[0] != "--once" || stored.Environment["CAMERA"] != "front" {
		t.Fatalf("stored job followed caller mutation: %+v", stored)
	}

	now = now.Add(time.Minute)
	update := testInput("Take photograph nightly")
	updated, err := manager.Update(created.ID, 1, update)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Name != update.Name || !updated.UpdatedAt.Equal(now) || !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("updated job = %+v", updated)
	}
	if _, err := manager.Update(created.ID, 1, update); !errors.Is(err, ErrJobRevisionConflict) {
		t.Fatalf("stale update error = %v", err)
	}

	reopened, err := Open(directory, limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Schema() != 1 {
		t.Fatalf("reopened manager schema = %d, want 1", reopened.Schema())
	}
	jobs := reopened.Jobs()
	if len(jobs) != 1 || jobs[0].Revision != 2 || jobs[0].Name != update.Name {
		t.Fatalf("reopened jobs = %+v", jobs)
	}
	if err := reopened.Delete(created.ID, 1); !errors.Is(err, ErrJobRevisionConflict) {
		t.Fatalf("stale delete error = %v", err)
	}
	if err := reopened.Delete(created.ID, 2); err != nil {
		t.Fatal(err)
	}
	if len(reopened.Jobs()) != 0 {
		t.Fatal("deleted job remains")
	}
}

func TestSavedJobsEnforceNameAndCountBounds(t *testing.T) {
	jobLimits := limits.Default().Jobs
	jobLimits.MaxSavedJobs = 1
	manager, err := Open(t.TempDir(), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	manager.random = bytes.NewReader(make([]byte, 32))
	if _, err := manager.Create(testInput("Camera")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(testInput("camera")); !errors.Is(err, ErrJobLimit) {
		t.Fatalf("limit error = %v", err)
	}
}

func TestSavedJobsRejectCaseInsensitiveDuplicateNames(t *testing.T) {
	manager, err := Open(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	manager.random = bytes.NewReader(make([]byte, 32))
	if _, err := manager.Create(testInput("Camera")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(testInput("camera")); !errors.Is(err, ErrJobNameConflict) {
		t.Fatalf("name conflict error = %v", err)
	}
}

func TestSavedJobsRetryRandomIDCollision(t *testing.T) {
	manager, err := Open(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	manager.random = bytes.NewReader(make([]byte, 16))
	if _, err := manager.Create(testInput("First")); err != nil {
		t.Fatal(err)
	}
	collidingThenUnique := append(make([]byte, 16), bytes.Repeat([]byte{1}, 16)...)
	manager.random = bytes.NewReader(collidingThenUnique)
	second, err := manager.Create(testInput("Second"))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != "01010101010101010101010101010101" {
		t.Fatalf("second ID = %q", second.ID)
	}
}
