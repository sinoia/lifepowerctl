//go:build linux

package jobs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

const runnerIOTimeout = 5 * time.Second

// Client exchanges one bounded request and response per Unix connection.
type Client struct {
	socketPath string
	limits     limits.Jobs
	dial       func(context.Context, string, string) (net.Conn, error)
}

func NewClient(socketPath string, jobLimits limits.Jobs) (*Client, error) {
	if socketPath == "" || socketPath[0] != '/' {
		return nil, fmt.Errorf("runner socket path must be absolute")
	}
	dialer := &net.Dialer{Timeout: runnerIOTimeout}
	return &Client{socketPath: socketPath, limits: jobLimits, dial: dialer.DialContext}, nil
}

func (client *Client) Do(ctx context.Context, request Request) (Response, error) {
	connection, err := client.dial(ctx, "unix", client.socketPath)
	if err != nil {
		return Response{}, fmt.Errorf("connect to job runner: %w", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(runnerIOTimeout)); err != nil {
		return Response{}, fmt.Errorf("set job runner deadline: %w", err)
	}
	if err := WriteRequest(connection, request, client.limits); err != nil {
		return Response{}, err
	}
	response, err := ReadResponse(connection, client.limits)
	if err != nil {
		return Response{}, err
	}
	if response.RunID != request.RunID {
		return Response{}, fmt.Errorf("%w: runner response ID does not match request", ErrInvalidRequest)
	}
	if response.ErrorCode != "" {
		return response, &RunnerError{Code: response.ErrorCode, Message: response.Message}
	}
	return response, nil
}

type RunnerError struct {
	Code    string
	Message string
}

func (err *RunnerError) Error() string {
	if err.Message == "" {
		return "job runner: " + err.Code
	}
	return "job runner: " + err.Code + ": " + err.Message
}

// Service owns one worker and accepts only the configured controller UID.
// Closing the context closes its listener; accepted runs remain represented in
// the durable registry and are conservatively recovered as lost after restart.
type Service struct {
	registry      *Registry
	supervisor    *Supervisor
	limits        limits.Jobs
	controllerUID uint32
	queue         chan Request
	closeOnce     sync.Once
}

func NewService(registry *Registry, supervisor *Supervisor, output *OutputStore, controllerUID uint32, jobLimits limits.Jobs) (*Service, error) {
	if registry == nil || supervisor == nil || output == nil || jobLimits.MaxQueued < 1 {
		return nil, fmt.Errorf("invalid job runner service configuration")
	}
	supervisor.output = output
	return &Service{registry: registry, supervisor: supervisor, limits: jobLimits, controllerUID: controllerUID, queue: make(chan Request, jobLimits.MaxQueued)}, nil
}

func (service *Service) Serve(ctx context.Context, listener *net.UnixListener) error {
	if listener == nil {
		return fmt.Errorf("job runner listener is required")
	}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		service.work(ctx)
	}()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	defer func() {
		service.closeOnce.Do(func() { close(service.queue) })
		<-workerDone
	}()

	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept job runner connection: %w", err)
		}
		service.handle(connection)
	}
}

func (service *Service) handle(connection *net.UnixConn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(runnerIOTimeout))
	uid, err := PeerUID(connection)
	if err != nil || uid != service.controllerUID {
		return
	}
	request, err := ReadRequest(connection, service.limits)
	if err != nil {
		return
	}
	response := service.dispatch(request)
	_ = WriteResponse(connection, response, service.limits)
}

func (service *Service) dispatch(request Request) Response {
	switch request.Action {
	case ActionStart:
		record, accepted, err := service.registry.Start(request)
		if err != nil {
			return errorResponse(request.RunID, err)
		}
		if accepted {
			service.queue <- request
		}
		response := responseFromRecord(record)
		response.Existing = !accepted
		return response
	case ActionStatus:
		record, err := service.registry.Status(request.RunID)
		if err != nil {
			return errorResponse(request.RunID, err)
		}
		return responseFromRecord(record)
	case ActionCancel:
		record, err := service.supervisor.Cancel(request.RunID)
		if err != nil {
			return errorResponse(request.RunID, err)
		}
		return responseFromRecord(record)
	default:
		return errorResponse(request.RunID, ErrInvalidRequest)
	}
}

func (service *Service) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case request, ok := <-service.queue:
			if !ok {
				return
			}
			_, _ = service.supervisor.Execute(ctx, request)
		}
	}
}

func responseFromRecord(record RunRecord) Response {
	return Response{
		Version: ProtocolVersion, RunID: record.RunID, State: record.State,
		QueuedUnixMilli: record.QueuedUnixMilli, StartedUnixMilli: record.StartedUnixMilli, FinishedUnixMilli: record.FinishedUnixMilli,
		ExitCode: record.ExitCode, Signal: record.Signal, StdoutTruncated: record.StdoutTruncated, StderrTruncated: record.StderrTruncated,
		StdoutTotal: record.StdoutTotal, StderrTotal: record.StderrTotal,
	}
}

func errorResponse(runID string, err error) Response {
	code := "internal"
	message := "The job runner could not complete the request."
	switch {
	case errors.Is(err, ErrRunNotFound):
		code, message = "not_found", "The run is not known to this runner."
	case errors.Is(err, ErrRunQueueFull):
		code, message = "queue_full", "The job queue is full."
	case errors.Is(err, ErrRunIDConflict):
		code, message = "run_id_conflict", "The run ID was already used with different settings."
	case errors.Is(err, ErrJobActive):
		code, message = "job_active", "This saved job already has a queued or running instance."
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, ErrRunState):
		code, message = "invalid_request", "The request is not valid in its current state."
	}
	return Response{Version: ProtocolVersion, RunID: runID, State: StateLost, ErrorCode: code, Message: message}
}

// ListenUnix creates the fixed local endpoint without replacing an existing
// filesystem object. Packaging is responsible for its parent directory.
func ListenUnix(path string) (*net.UnixListener, error) {
	if path == "" || path[0] != '/' {
		return nil, fmt.Errorf("runner socket path must be absolute")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0660); err != nil {
		listener.Close()
		return nil, fmt.Errorf("set runner socket permissions: %w", err)
	}
	return listener, nil
}
