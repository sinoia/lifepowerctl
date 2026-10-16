//go:build linux

package jobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

type Execution struct {
	Record RunRecord
	Stdout []byte
	Stderr []byte
}

type Supervisor struct {
	registry *Registry
	limits   limits.Jobs
	identity IdentityEnvironment
	gate     chan struct{}
	mu       sync.Mutex
	active   map[string]context.CancelFunc
	output   *OutputStore
}

func NewSupervisor(registry *Registry, jobLimits limits.Jobs, identity IdentityEnvironment) (*Supervisor, error) {
	if registry == nil || identity.Home == "" || identity.User == "" || identity.Logname == "" || identity.Path == "" {
		return nil, fmt.Errorf("invalid job supervisor configuration")
	}
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &Supervisor{registry: registry, limits: jobLimits, identity: identity, gate: gate, active: make(map[string]context.CancelFunc)}, nil
}

// Execute runs one already accepted request. The registry is moved to running
// durably before process launch and to a terminal state before success returns.
func (supervisor *Supervisor) Execute(ctx context.Context, request Request) (Execution, error) {
	select {
	case <-ctx.Done():
		_, _ = supervisor.registry.Cancel(request.RunID)
		return Execution{}, ctx.Err()
	case <-supervisor.gate:
	}
	defer func() { supervisor.gate <- struct{}{} }()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	supervisor.mu.Lock()
	_, err := supervisor.registry.MarkRunning(request)
	if err == nil {
		supervisor.active[request.RunID] = cancel
	}
	supervisor.mu.Unlock()
	if err != nil {
		return Execution{}, err
	}
	defer func() {
		supervisor.mu.Lock()
		delete(supervisor.active, request.RunID)
		supervisor.mu.Unlock()
	}()

	stdout := &cappedOutput{limit: request.Start.StdoutBytes}
	stderr := &cappedOutput{limit: request.Start.StderrBytes}
	command := supervisor.command(*request.Start)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		outputErr := supervisor.saveOutput(request.RunID, stdout, stderr)
		final, finishErr := supervisor.registry.Finish(request.RunID, outputResult(StateFailed, nil, "", stdout, stderr))
		if finishErr != nil {
			return Execution{}, errors.Join(fmt.Errorf("start job: %w", err), outputErr, finishErr)
		}
		return Execution{Record: final, Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, errors.Join(fmt.Errorf("start job: %w", err), outputErr)
	}

	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	timer := time.NewTimer(time.Duration(request.Start.TimeoutSeconds) * time.Second)
	defer timer.Stop()

	var waitErr error
	state := StateFailed
	select {
	case waitErr = <-wait:
		if waitErr == nil {
			state = StateSucceeded
		}
	case <-timer.C:
		state = StateTimedOut
		waitErr = terminateProcessGroup(command.Process.Pid, wait, supervisor.limits.TerminationGrace)
	case <-runCtx.Done():
		state = StateCancelled
		waitErr = terminateProcessGroup(command.Process.Pid, wait, supervisor.limits.TerminationGrace)
	}

	exitCode, signal := processResult(command, waitErr)
	outputErr := supervisor.saveOutput(request.RunID, stdout, stderr)
	if outputErr != nil {
		state = StateFailed
	}
	final, err := supervisor.registry.Finish(request.RunID, outputResult(state, exitCode, signal, stdout, stderr))
	if err != nil {
		return Execution{}, errors.Join(outputErr, err)
	}
	return Execution{Record: final, Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, outputErr
}

func (supervisor *Supervisor) saveOutput(runID string, stdout, stderr *cappedOutput) error {
	if supervisor.output == nil {
		return nil
	}
	return supervisor.output.Save(runID, stdout.Bytes(), stderr.Bytes())
}

func (supervisor *Supervisor) Cancel(runID string) (RunRecord, error) {
	supervisor.mu.Lock()
	cancel := supervisor.active[runID]
	supervisor.mu.Unlock()
	if cancel != nil {
		cancel()
		return supervisor.registry.Status(runID)
	}
	return supervisor.registry.Cancel(runID)
}

func (supervisor *Supervisor) command(request StartRequest) *exec.Cmd {
	var command *exec.Cmd
	if request.Mode == ModeShell {
		command = exec.Command("/bin/bash", "-lc", request.Command)
	} else {
		command = exec.Command(request.Executable, request.Arguments...)
	}
	command.Dir = request.WorkingDirectory
	environment := []string{
		"HOME=" + supervisor.identity.Home,
		"USER=" + supervisor.identity.User,
		"LOGNAME=" + supervisor.identity.Logname,
		"PATH=" + supervisor.identity.Path,
	}
	names := make([]string, 0, len(request.Environment))
	for name := range request.Environment {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		environment = append(environment, name+"="+request.Environment[name])
	}
	command.Env = environment
	return command
}

func terminateProcessGroup(pid int, wait <-chan error, grace time.Duration) error {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-wait:
		return err
	case <-timer.C:
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return <-wait
}

func processResult(command *exec.Cmd, waitErr error) (*int, string) {
	if command.ProcessState == nil {
		return nil, ""
	}
	code := command.ProcessState.ExitCode()
	status, ok := command.ProcessState.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		return nil, status.Signal().String()
	}
	if waitErr == nil || code >= 0 {
		return &code, ""
	}
	return nil, ""
}

func outputResult(state RunState, exitCode *int, signal string, stdout, stderr *cappedOutput) RunResult {
	return RunResult{
		State:           state,
		ExitCode:        exitCode,
		Signal:          signal,
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
		StdoutTotal:     stdout.Total(),
		StderrTotal:     stderr.Total(),
	}
}

type cappedOutput struct {
	buffer bytes.Buffer
	limit  int64
	total  int64
}

func (output *cappedOutput) Write(value []byte) (int, error) {
	output.total += int64(len(value))
	remaining := output.limit - int64(output.buffer.Len())
	if remaining > 0 {
		keep := int64(len(value))
		if keep > remaining {
			keep = remaining
		}
		_, _ = output.buffer.Write(value[:int(keep)])
	}
	return len(value), nil
}

func (output *cappedOutput) Bytes() []byte { return bytes.Clone(output.buffer.Bytes()) }
func (output *cappedOutput) Total() int64  { return output.total }
func (output *cappedOutput) Truncated() bool {
	return output.total > int64(output.buffer.Len())
}
