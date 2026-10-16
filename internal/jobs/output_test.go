package jobs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

func TestOutputStoreSeparateBoundedStreams(t *testing.T) {
	jobLimits := limits.Default().Jobs
	store, err := NewOutputStore(t.TempDir(), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(testRunID, []byte("standard output\n"), []byte("standard error\n")); err != nil {
		t.Fatal(err)
	}
	stdout, err := store.Read(testRunID, OutputStdout)
	if err != nil || string(stdout) != "standard output\n" {
		t.Fatalf("stdout = %q, %v", stdout, err)
	}
	stderr, err := store.Read(testRunID, OutputStderr)
	if err != nil || string(stderr) != "standard error\n" {
		t.Fatalf("stderr = %q, %v", stderr, err)
	}
	if exists, err := store.Exists(testRunID, OutputStdout); err != nil || !exists {
		t.Fatalf("stdout existence = %v, %v", exists, err)
	}
	if exists, err := store.Exists("11112222333344445555666677778888", OutputStdout); err != nil || exists {
		t.Fatalf("missing output existence = %v, %v", exists, err)
	}
	info, err := os.Stat(filepath.Join(store.directory, testRunID+".stdout"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("stdout permissions = %v", info.Mode().Perm())
	}
	if _, err := store.Read("../../etc/passwd", OutputStdout); err == nil {
		t.Fatal("path traversal run ID accepted")
	}
	if _, err := store.Read(testRunID, "combined"); err == nil {
		t.Fatal("unknown stream accepted")
	}
	symlinkRun := "11112222333344445555666677778888"
	if err := os.Symlink("/etc/passwd", filepath.Join(store.directory, symlinkRun+".stdout")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(symlinkRun, OutputStdout); err == nil {
		t.Fatal("symlink output accepted")
	}
}

func TestOutputStorePrunesOldestCompleteRun(t *testing.T) {
	jobLimits := limits.Default().Jobs
	jobLimits.RetainedRunsTotal = 1
	store, err := NewOutputStore(t.TempDir(), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(testRunID, []byte("old"), nil); err != nil {
		t.Fatal(err)
	}
	second := "11112222333344445555666677778888"
	if err := store.Save(second, []byte("new"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(testRunID, OutputStdout); !errors.Is(err, ErrOutputNotFound) {
		t.Fatalf("old output error = %v", err)
	}
	if value, err := store.Read(second, OutputStdout); err != nil || string(value) != "new" {
		t.Fatalf("new output = %q, %v", value, err)
	}
}
