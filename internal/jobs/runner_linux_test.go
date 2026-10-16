//go:build linux

package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

func TestRunnerServiceClientLifecycle(t *testing.T) {
	jobLimits := limits.Default().Jobs
	root := t.TempDir()
	registry, err := NewPersistentRegistry(filepath.Join(root, "spool"), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	supervisor, err := NewSupervisor(registry, jobLimits, IdentityEnvironment{Home: root, User: "test-user", Logname: "test-user", Path: "/usr/bin:/bin"})
	if err != nil {
		t.Fatal(err)
	}
	output, err := NewOutputStore(filepath.Join(root, "output"), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(registry, supervisor, output, uint32(os.Getuid()), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(root, "runner.sock")
	listener, err := ListenUnix(socketPath)
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skip("sandbox blocks Unix sockets")
		}
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- service.Serve(ctx, listener) }()

	client, err := NewClient(socketPath, jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	request := validStartRequest()
	request.Start.JobID = "integration-job"
	request.Start.Mode = ModeShell
	request.Start.Executable = ""
	request.Start.Arguments = nil
	request.Start.Command = "printf runner-ok; printf runner-error >&2"
	request.Start.WorkingDirectory = root
	started, err := client.Do(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if started.RunID != request.RunID || started.State != StateQueued || started.Existing {
		t.Fatalf("start response = %+v", started)
	}

	var final Response
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		final, err = client.Do(ctx, Request{Version: ProtocolVersion, Action: ActionStatus, RunID: request.RunID})
		if err != nil {
			t.Fatal(err)
		}
		if terminalState(final.State) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final.State != StateSucceeded || final.StdoutTotal != int64(len("runner-ok")) {
		t.Fatalf("final response = %+v", final)
	}
	stdout, err := output.Read(request.RunID, OutputStdout)
	if err != nil || string(stdout) != "runner-ok" {
		t.Fatalf("stdout = %q, %v", stdout, err)
	}
	stderr, err := output.Read(request.RunID, OutputStderr)
	if err != nil || string(stderr) != "runner-error" {
		t.Fatalf("stderr = %q, %v", stderr, err)
	}

	repeated, err := client.Do(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !repeated.Existing || repeated.State != StateSucceeded {
		t.Fatalf("idempotent response = %+v", repeated)
	}

	_, err = client.Do(ctx, Request{Version: ProtocolVersion, Action: ActionStatus, RunID: "11112222333344445555666677778888"})
	var runnerErr *RunnerError
	if !errors.As(err, &runnerErr) || runnerErr.Code != "not_found" {
		t.Fatalf("unknown run error = %v", err)
	}

	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner service did not stop")
	}
}

func TestRunnerRejectsUnexpectedPeerUID(t *testing.T) {
	jobLimits := limits.Default().Jobs
	registry, err := NewRegistry(jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	supervisor, err := NewSupervisor(registry, jobLimits, IdentityEnvironment{Home: root, User: "test", Logname: "test", Path: "/usr/bin:/bin"})
	if err != nil {
		t.Fatal(err)
	}
	output, err := NewOutputStore(filepath.Join(root, "output"), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(registry, supervisor, output, uint32(os.Getuid()+1), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := ListenUnix(filepath.Join(root, "runner.sock"))
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skip("sandbox blocks Unix sockets")
		}
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- service.Serve(ctx, listener) }()
	client, err := NewClient(listener.Addr().String(), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(ctx, validStartRequest())
	if err == nil {
		t.Fatal("unexpected peer UID was accepted")
	}
	cancel()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}
