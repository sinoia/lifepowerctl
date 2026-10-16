//go:build linux

package jobs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

func testSupervisor(t *testing.T, jobLimits limits.Jobs) (*Supervisor, *Registry) {
	t.Helper()
	registry, err := NewPersistentRegistry(t.TempDir(), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	supervisor, err := NewSupervisor(registry, jobLimits, IdentityEnvironment{
		Home: t.TempDir(), User: "runner-test", Logname: "runner-test", Path: "/usr/bin:/bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	return supervisor, registry
}

func shellRequest(t *testing.T, command string) Request {
	t.Helper()
	request := validStartRequest()
	request.Start.Mode = ModeShell
	request.Start.Executable = ""
	request.Start.Arguments = nil
	request.Start.Command = command
	request.Start.WorkingDirectory = t.TempDir()
	return request
}

func acceptRequest(t *testing.T, registry *Registry, request Request) {
	t.Helper()
	if _, created, err := registry.Start(request); err != nil || !created {
		t.Fatalf("Start() created = %v, error = %v", created, err)
	}
}

func TestSupervisorSuccessAndSeparateOutput(t *testing.T) {
	jobLimits := limits.Default().Jobs
	supervisor, registry := testSupervisor(t, jobLimits)
	request := shellRequest(t, `printf 'standard output'; printf 'standard error' >&2`)
	acceptRequest(t, registry, request)
	execution, err := supervisor.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Record.State != StateSucceeded || string(execution.Stdout) != "standard output" || string(execution.Stderr) != "standard error" {
		t.Fatalf("Execute() = %+v, stdout=%q, stderr=%q", execution.Record, execution.Stdout, execution.Stderr)
	}
	if execution.Record.ExitCode == nil || *execution.Record.ExitCode != 0 {
		t.Fatalf("exit code = %v", execution.Record.ExitCode)
	}
}

func TestSupervisorFailureAndTruncation(t *testing.T) {
	jobLimits := limits.Default().Jobs
	supervisor, registry := testSupervisor(t, jobLimits)
	request := shellRequest(t, `printf '123456'; printf 'abcdef' >&2; exit 7`)
	request.Start.StdoutBytes = 4
	request.Start.StderrBytes = 4
	acceptRequest(t, registry, request)
	execution, err := supervisor.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Record.State != StateFailed || execution.Record.ExitCode == nil || *execution.Record.ExitCode != 7 {
		t.Fatalf("record = %+v", execution.Record)
	}
	if string(execution.Stdout) != "1234" || string(execution.Stderr) != "abcd" || execution.Record.StdoutTotal != 6 || execution.Record.StderrTotal != 6 || !execution.Record.StdoutTruncated || !execution.Record.StderrTruncated {
		t.Fatalf("truncation record=%+v stdout=%q stderr=%q", execution.Record, execution.Stdout, execution.Stderr)
	}
}

func TestSupervisorTimeout(t *testing.T) {
	jobLimits := limits.Default().Jobs
	jobLimits.TerminationGrace = 20 * time.Millisecond
	supervisor, registry := testSupervisor(t, jobLimits)
	request := shellRequest(t, `trap '' TERM; sleep 5`)
	request.Start.TimeoutSeconds = 1
	acceptRequest(t, registry, request)
	started := time.Now()
	execution, err := supervisor.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Record.State != StateTimedOut || execution.Record.Signal == "" || time.Since(started) > 3*time.Second {
		t.Fatalf("timeout execution = %+v after %s", execution.Record, time.Since(started))
	}
}

func TestSupervisorCancellation(t *testing.T) {
	jobLimits := limits.Default().Jobs
	jobLimits.TerminationGrace = 20 * time.Millisecond
	supervisor, registry := testSupervisor(t, jobLimits)
	request := shellRequest(t, `trap '' TERM; sleep 5`)
	request.Start.TimeoutSeconds = 60
	acceptRequest(t, registry, request)
	result := make(chan Execution, 1)
	errors := make(chan error, 1)
	go func() {
		execution, err := supervisor.Execute(context.Background(), request)
		result <- execution
		errors <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		record, err := registry.Status(request.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if record.State == StateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not enter running state")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := supervisor.Cancel(request.RunID); err != nil {
		t.Fatal(err)
	}
	execution := <-result
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	if execution.Record.State != StateCancelled || execution.Record.Signal == "" {
		t.Fatalf("cancelled execution = %+v", execution.Record)
	}
}

func TestSelectedUserHomeScriptAndVirtualEnvironmentPath(t *testing.T) {
	if os.Getenv("LIFEPOWERCTL_LIVE_HOME_TEST") != "1" {
		t.Skip("set LIFEPOWERCTL_LIVE_HOME_TEST=1 for selected-user home execution")
	}
	identity, err := CurrentExecutionIdentity()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(identity.Home, ".lifepowerctl-job-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	script := filepath.Join(root, "home-script.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s:%s' \"$USER\" \"$HOME\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	venvBin := filepath.Join(root, ".venv", "bin")
	if err := os.MkdirAll(venvBin, 0700); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(venvBin, "python")
	if err := os.Symlink("/usr/bin/python3", python); err != nil {
		t.Fatal(err)
	}

	jobLimits := limits.Default().Jobs
	supervisor, registry := testSupervisor(t, jobLimits)
	supervisor.identity = identity.Environment()
	request := validStartRequest()
	request.Start.JobID = "home-script"
	request.Start.Executable = script
	request.Start.Arguments = nil
	request.Start.WorkingDirectory = root
	acceptRequest(t, registry, request)
	execution, err := supervisor.Execute(context.Background(), request)
	if err != nil || execution.Record.State != StateSucceeded || string(execution.Stdout) != identity.User+":"+identity.Home {
		t.Fatalf("home script = %+v, %q, %v", execution.Record, execution.Stdout, err)
	}

	request.RunID = "11112222333344445555666677778888"
	request.Start.JobID = "venv-script"
	request.Start.Executable = python
	request.Start.Arguments = []string{"-c", "print('venv-ok', end='')"}
	acceptRequest(t, registry, request)
	execution, err = supervisor.Execute(context.Background(), request)
	if err != nil || execution.Record.State != StateSucceeded || string(execution.Stdout) != "venv-ok" {
		t.Fatalf("venv path = %+v, %q, %v", execution.Record, execution.Stdout, err)
	}
}
