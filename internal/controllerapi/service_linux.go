//go:build linux

package controllerapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/controllerstate"
	"github.com/sinoia/lifepowerctl/internal/dutycycle"
	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

const ioTimeout = 5 * time.Second
const simulatedMinimumLead = schedule.SimulatedMinimumLead

type State interface {
	Revision() uint64
	Schema() int
	Recovered() bool
	SaveDisabledConfiguration(string, uint64, schedule.Configuration) (controllerstate.ConfigurationResult, error)
	ConfigurationProposalResult(string) (controllerstate.ConfigurationResult, error)
	DutyCycleConfiguration() (schedule.Configuration, bool)
	DutyCycleLifecycle() schedule.Lifecycle
	DutyCycleRunListMaximum() (int, uint64, error)
	EnableSimulation(string, uint64, uint64, string, uint32) (controllerstate.ConfigurationResult, error)
	SimulationResult(string, uint64, uint64, string, uint32) (controllerstate.ConfigurationResult, error)
	Disable(string, uint64, uint64) (controllerstate.ConfigurationResult, error)
	Jobs() []jobs.Definition
	Create(jobs.DefinitionInput) (jobs.Definition, error)
	Update(string, uint64, jobs.DefinitionInput) (jobs.Definition, error)
	Delete(string, uint64) error
	BeginRun(jobs.Request, string, bool) (controllerstate.Run, error)
	UpdateRun(jobs.Response) (controllerstate.Run, bool, error)
	MarkRunObserved(string, jobs.RunState) error
	MarkRunLost(string) (controllerstate.Run, error)
	Runs(int, string) []controllerstate.Run
	Run(string) (controllerstate.Run, error)
	ActiveRunCount() int
}

type Simulator interface {
	ObserveExpectedSimulatedBoot() error
	RunObserved(context.Context) error
}

type RealActivator interface {
	Preview(context.Context, uint64, int) (schedule.Review, error)
	Enable(context.Context, string, uint64, uint64, schedule.Review) (controllerstate.ConfigurationResult, error)
}

type Service struct {
	state        State
	allowedUID   uint32
	closeOnce    sync.Once
	now          func() time.Time
	minimumLead  time.Duration
	simulator    Simulator
	activator    RealActivator
	serveContext context.Context
	audit        *activity.Audit
	build        BuildIdentity
}

func (service *Service) SetSimulator(simulator Simulator)         { service.simulator = simulator }
func (service *Service) SetRealActivator(activator RealActivator) { service.activator = activator }
func (service *Service) SetAudit(audit *activity.Audit)           { service.audit = audit }

func NewService(state State, allowedUID uint32, build BuildIdentity) (*Service, error) {
	if state == nil {
		return nil, fmt.Errorf("controller state is required")
	}
	if err := build.Validate(); err != nil {
		return nil, err
	}
	return &Service{state: state, allowedUID: allowedUID, build: build, now: time.Now, minimumLead: simulatedMinimumLead}, nil
}

func ListenUnix(path string) (*net.UnixListener, error) {
	if path == "" || path[0] != '/' {
		return nil, fmt.Errorf("controller socket path must be absolute")
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("controller socket path exists and is not a socket")
		}
		connection, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return nil, fmt.Errorf("controller socket is already active")
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("inspect existing controller socket: %w", dialErr)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale controller socket: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect controller socket path: %w", statErr)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		listener.Close()
		return nil, fmt.Errorf("set controller socket permissions: %w", err)
	}
	return listener, nil
}

func (service *Service) Serve(ctx context.Context, listener *net.UnixListener) error {
	if listener == nil {
		return fmt.Errorf("controller listener is required")
	}
	service.serveContext = ctx
	go func() {
		<-ctx.Done()
		service.closeOnce.Do(func() { _ = listener.Close() })
	}()
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept controller connection: %w", err)
		}
		service.handle(connection)
	}
}

func (service *Service) handle(connection *net.UnixConn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(ioTimeout))
	uid, err := peerUID(connection)
	if err != nil || uid != service.allowedUID {
		return
	}
	request, err := ReadRequest(connection)
	if err != nil {
		return
	}
	response := Response{Version: ProtocolVersion, Action: request.Action}
	switch request.Action {
	case ActionHealth:
		response.Health = HealthReady
		if service.state.Recovered() {
			response.Health = HealthRecovered
		}
		response.StateRevision = service.state.Revision()
		response.StateSchema = service.state.Schema()
		build := service.build
		response.Build = &build
	case ActionStatus:
		lifecycle := service.state.DutyCycleLifecycle()
		response.StateRevision = service.state.Revision()
		response.StateSchema = service.state.Schema()
		response.Lifecycle = &lifecycle
		response.ActiveRuns = service.state.ActiveRunCount()
		if configuration, ok := service.state.DutyCycleConfiguration(); ok {
			response.ConfigurationRevision = configuration.Revision
			response.Configuration = &configuration
		}
	case ActionSaveDisabled:
		result, applyErr := service.state.SaveDisabledConfiguration(request.ProposalID, request.BaseRevision, *request.Configuration)
		response = configurationResponse(request.Action, result, applyErr)
	case ActionProposal:
		result, lookupErr := service.state.ConfigurationProposalResult(request.ProposalID)
		response = configurationResponse(request.Action, result, lookupErr)
	case ActionPreview:
		response = service.previewResponse(request)
	case ActionEnableSimulation:
		response = service.enableSimulationResponse(request)
	case ActionPreviewReal:
		response = service.previewRealResponse(request)
	case ActionEnableReal:
		response = service.enableRealResponse(request)
	case ActionDisable:
		result, disableErr := service.state.Disable(request.ProposalID, request.BaseRevision, request.ConfigurationRevision)
		response = configurationResponse(request.Action, result, disableErr)
	case ActionSimulateBoot:
		response = service.simulateBootResponse(request)
	case ActionJobList:
		response.StateRevision = service.state.Revision()
		response.Jobs = service.state.Jobs()
	case ActionJobCreate:
		definition, createErr := service.state.Create(*request.JobInput)
		response = jobResponse(request.Action, service.state.Revision(), definition, createErr)
	case ActionJobUpdate:
		definition, updateErr := service.state.Update(request.JobID, request.JobRevision, *request.JobInput)
		response = jobResponse(request.Action, service.state.Revision(), definition, updateErr)
	case ActionJobDelete:
		deleteErr := service.state.Delete(request.JobID, request.JobRevision)
		response.StateRevision = service.state.Revision()
		if deleteErr != nil {
			response.ErrorCode, response.Message = jobError(deleteErr)
		}
	case ActionRunBegin:
		run, runErr := service.state.BeginRun(*request.RunRequest, request.DisplayJobID, request.AdHoc)
		response = runResponse(request.Action, service.state.Revision(), run, false, runErr)
	case ActionRunUpdate:
		run, changed, runErr := service.state.UpdateRun(*request.RunResponse)
		response = runResponse(request.Action, service.state.Revision(), run, changed, runErr)
	case ActionRunObserve:
		runErr := service.state.MarkRunObserved(request.RunID, request.RunState)
		response.StateRevision = service.state.Revision()
		if runErr != nil {
			response.ErrorCode, response.Message = runError(runErr)
		}
	case ActionRunLost:
		run, runErr := service.state.MarkRunLost(request.RunID)
		response = runResponse(request.Action, service.state.Revision(), run, true, runErr)
	case ActionRunList:
		response.StateRevision = service.state.Revision()
		response.Runs = service.state.Runs(request.RunLimit, request.RunFilterJobID)
	case ActionRunGet:
		run, runErr := service.state.Run(request.RunID)
		response = runResponse(request.Action, service.state.Revision(), run, false, runErr)
	}
	if response.ErrorCode == "" {
		switch request.Action {
		case ActionSaveDisabled, ActionEnableSimulation, ActionDisable:
			service.audit.Record(activity.DutyCycleConfiguration, activity.ActorSystem, "duty-cycle", struct {
				Action                Action `json:"action"`
				StateRevision         uint64 `json:"state_revision"`
				ConfigurationRevision uint64 `json:"configuration_revision"`
				Simulated             bool   `json:"simulated"`
			}{Action: request.Action, StateRevision: response.StateRevision, ConfigurationRevision: response.ConfigurationRevision, Simulated: request.Action == ActionEnableSimulation})
		case ActionSimulateBoot:
			service.audit.Record(activity.DutyCycleTransition, activity.ActorSystem, "duty-cycle", struct {
				Phase                 schedule.Phase `json:"phase"`
				ConfigurationRevision uint64         `json:"configuration_revision"`
				Simulated             bool           `json:"simulated"`
			}{Phase: schedule.PhaseExpectedBootObserved, ConfigurationRevision: response.ConfigurationRevision, Simulated: true})
		case ActionJobCreate, ActionJobUpdate:
			kind := activity.JobCreated
			if request.Action == ActionJobUpdate {
				kind = activity.JobUpdated
			}
			service.audit.Record(kind, activity.ActorSystem, response.Job.ID, struct {
				Revision uint64             `json:"revision"`
				Enabled  bool               `json:"enabled"`
				Mode     jobs.ExecutionMode `json:"mode"`
			}{Revision: response.Job.Revision, Enabled: response.Job.Enabled, Mode: response.Job.Mode})
		case ActionJobDelete:
			service.audit.Record(activity.JobDeleted, activity.ActorSystem, request.JobID, struct {
				Revision uint64 `json:"revision"`
			}{Revision: request.JobRevision})
		}
	}
	_ = WriteResponse(connection, response)
}

func (service *Service) previewRealResponse(request Request) Response {
	response := Response{Version: ProtocolVersion, Action: ActionPreviewReal, StateRevision: service.state.Revision(), ConfigurationRevision: request.ConfigurationRevision}
	if service.activator == nil {
		response.ErrorCode, response.Message = "unavailable", "Real duty-cycle activation is unavailable."
		return response
	}
	requestContext := service.serveContext
	if requestContext == nil {
		requestContext = context.Background()
	}
	review, err := service.activator.Preview(requestContext, request.ConfigurationRevision, request.PreviewCount)
	if err != nil {
		response.ErrorCode, response.Message = "preview_failed", "The controller could not calculate a board-clock occurrence preview."
		if errors.Is(err, controllerstate.ErrConfigurationRevision) {
			response.ErrorCode = "revision_conflict"
		} else if errors.Is(err, schedule.ErrNoOccurrence) {
			response.ErrorCode, response.Message = "no_occurrence", "No eligible occurrence satisfies the verified wake lead."
		}
		return response
	}
	response.Review = &review
	return response
}

func (service *Service) enableRealResponse(request Request) Response {
	response := Response{Version: ProtocolVersion, Action: ActionEnableReal, StateRevision: service.state.Revision(), ConfigurationRevision: request.ConfigurationRevision}
	if service.activator == nil {
		response.ErrorCode, response.Message = "unavailable", "Real duty-cycle activation is unavailable."
		return response
	}
	requestContext := service.serveContext
	if requestContext == nil {
		requestContext = context.Background()
	}
	result, err := service.activator.Enable(requestContext, request.ProposalID, request.BaseRevision, request.ConfigurationRevision, *request.Review)
	response = configurationResponse(ActionEnableReal, result, err)
	if err == nil {
		response.IntendedOccurrence = service.state.DutyCycleLifecycle().IntendedOccurrence
	}
	var confirmation *dutycycle.ConfirmationRequiredError
	if errors.As(err, &confirmation) {
		response.StateRevision = service.state.Revision()
		response.ConfigurationRevision = request.ConfigurationRevision
		response.ErrorCode = "confirmation_required"
		response.Message = "The first board-clock occurrence changed and requires another review."
		response.Review = &confirmation.Review
	} else if errors.Is(err, schedule.ErrNoOccurrence) {
		response.StateRevision = service.state.Revision()
		response.ConfigurationRevision = request.ConfigurationRevision
		response.ErrorCode = "no_occurrence"
		response.Message = "The reviewed occurrence is now too close or no longer eligible."
	} else if err != nil && response.ErrorCode == "invalid_request" {
		response.ErrorCode = "activation_failed"
		response.Message = "Real activation failed and automatic operation was inhibited."
	}
	if err != nil && response.ErrorCode != "confirmation_required" && response.ErrorCode != "no_occurrence" {
		log.Printf("lifepowerctl: real duty-cycle activation failed: %v", err)
	}
	return response
}

func jobResponse(action Action, stateRevision uint64, definition jobs.Definition, err error) Response {
	response := Response{Version: ProtocolVersion, Action: action, StateRevision: stateRevision}
	if err != nil {
		response.ErrorCode, response.Message = jobError(err)
		return response
	}
	response.Job = &definition
	return response
}

func jobError(err error) (string, string) {
	switch {
	case errors.Is(err, controllerstate.ErrJobNotFound):
		return "job_not_found", "The saved job does not exist."
	case errors.Is(err, controllerstate.ErrJobRevisionConflict):
		return "revision_conflict", "The saved job changed before this request."
	case errors.Is(err, controllerstate.ErrJobNameConflict):
		return "name_conflict", "Another saved job already uses that name."
	case errors.Is(err, controllerstate.ErrJobLimit):
		return "job_limit", "The saved-job limit has been reached."
	case errors.Is(err, controllerstate.ErrJobInRunList):
		return "job_in_run_list", "The saved job is referenced by the duty-cycle run list."
	default:
		return "invalid_job", "The controller rejected the saved-job request."
	}
}

func runResponse(action Action, stateRevision uint64, run controllerstate.Run, changed bool, err error) Response {
	response := Response{Version: ProtocolVersion, Action: action, StateRevision: stateRevision, Changed: changed}
	if err != nil {
		response.ErrorCode, response.Message = runError(err)
		response.Changed = false
		return response
	}
	response.Run = &run
	return response
}

func runError(err error) (string, string) {
	switch {
	case errors.Is(err, controllerstate.ErrRunNotFound):
		return "run_not_found", "The controller run does not exist."
	case errors.Is(err, controllerstate.ErrRunLimit):
		return "run_limit", "The controller run-retention limit has been reached."
	default:
		return "invalid_run", "The controller rejected the run-state request."
	}
}

func (service *Service) simulateBootResponse(request Request) Response {
	response := Response{Version: ProtocolVersion, Action: ActionSimulateBoot, StateRevision: service.state.Revision(), ConfigurationRevision: request.ConfigurationRevision}
	if service.simulator == nil {
		response.ErrorCode = "simulation_unavailable"
		response.Message = "The simulated boot executor is unavailable."
		return response
	}
	configuration, ok := service.state.DutyCycleConfiguration()
	if service.state.Revision() != request.BaseRevision || !ok || configuration.Revision != request.ConfigurationRevision {
		response.ErrorCode = "revision_conflict"
		response.Message = "The duty-cycle state changed before simulated boot."
		return response
	}
	if err := service.simulator.ObserveExpectedSimulatedBoot(); err != nil {
		response.ErrorCode = "not_ready"
		response.Message = "No simulated occurrence is ready to run."
		return response
	}
	response.StateRevision = service.state.Revision()
	ctx := service.serveContext
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		if err := service.simulator.RunObserved(ctx); err != nil {
			log.Printf("lifepowerctl: simulated duty cycle: %v", err)
		}
	}()
	return response
}

func (service *Service) enableSimulationResponse(request Request) Response {
	if existing, err := service.state.SimulationResult(request.ProposalID, request.BaseRevision, request.ConfigurationRevision, request.Review.Identity, request.Review.Occurrences[0].Unix); err == nil {
		return configurationResponse(ActionEnableSimulation, existing, nil)
	} else if !errors.Is(err, controllerstate.ErrProposalNotFound) {
		return configurationResponse(ActionEnableSimulation, controllerstate.ConfigurationResult{}, err)
	}
	response := Response{Version: ProtocolVersion, Action: ActionEnableSimulation, ConfigurationRevision: request.ConfigurationRevision}
	configuration, ok := service.state.DutyCycleConfiguration()
	if !ok || configuration.Revision != request.ConfigurationRevision {
		response.ErrorCode = "revision_conflict"
		response.Message = "The saved duty-cycle configuration changed before enablement."
		return response
	}
	configuration.Enabled = false
	lead := time.Duration(request.Review.MinimumLeadSeconds) * time.Second
	reviewed, err := schedule.BuildReview(configuration, request.Review.Reference, lead, len(request.Review.Occurrences))
	if err == nil {
		err = service.addRunListMaximum(&reviewed)
	}
	if err != nil || reviewed.Identity != request.Review.Identity || reviewed.RunListJobs != request.Review.RunListJobs || reviewed.MaximumRunSeconds != request.Review.MaximumRunSeconds || lead != service.minimumLead {
		response.ErrorCode = "invalid_review"
		response.Message = "The occurrence review is invalid or no longer matches controller policy."
		return response
	}
	now := service.now().Unix()
	if now < 0 || uint64(now) > uint64(^uint32(0)) {
		response.ErrorCode = "clock_range"
		response.Message = "The simulated board time is outside the supported range."
		return response
	}
	fresh, err := schedule.BuildReview(configuration, uint32(now), service.minimumLead, len(request.Review.Occurrences))
	if err == nil {
		err = service.addRunListMaximum(&fresh)
	}
	if err != nil {
		response.ErrorCode = "no_occurrence"
		response.Message = "The controller could not calculate a fresh occurrence."
		return response
	}
	if fresh.Occurrences[0].Unix != request.Review.Occurrences[0].Unix {
		response.StateRevision = service.state.Revision()
		response.ErrorCode = "confirmation_required"
		response.Message = "The first occurrence changed and requires another review."
		response.Review = &fresh
		return response
	}
	result, enableErr := service.state.EnableSimulation(request.ProposalID, request.BaseRevision, request.ConfigurationRevision, request.Review.Identity, fresh.Occurrences[0].Unix)
	return configurationResponse(ActionEnableSimulation, result, enableErr)
}

func (service *Service) previewResponse(request Request) Response {
	response := Response{Version: ProtocolVersion, Action: ActionPreview, StateRevision: service.state.Revision(), ConfigurationRevision: request.ConfigurationRevision}
	configuration, ok := service.state.DutyCycleConfiguration()
	if !ok || configuration.Revision != request.ConfigurationRevision {
		response.ErrorCode = "revision_conflict"
		response.Message = "The saved duty-cycle configuration changed before preview."
		return response
	}
	now := service.now().Unix()
	if now < 0 || uint64(now) > uint64(^uint32(0)) {
		response.ErrorCode = "clock_range"
		response.Message = "The simulated board time is outside the supported range."
		return response
	}
	review, err := schedule.BuildReview(configuration, uint32(now), service.minimumLead, request.PreviewCount)
	if err == nil {
		err = service.addRunListMaximum(&review)
	}
	if err != nil {
		response.ErrorCode = "no_occurrence"
		response.Message = "The controller could not calculate the requested occurrence preview."
		return response
	}
	response.Review = &review
	return response
}

func (service *Service) addRunListMaximum(review *schedule.Review) error {
	jobs, seconds, err := service.state.DutyCycleRunListMaximum()
	if err != nil {
		return err
	}
	review.RunListJobs, review.MaximumRunSeconds = jobs, seconds
	return nil
}

func configurationResponse(action Action, result controllerstate.ConfigurationResult, err error) Response {
	response := Response{Version: ProtocolVersion, Action: action, StateRevision: result.StateRevision, ConfigurationRevision: result.ConfigurationRevision, Existing: result.Existing}
	if err == nil {
		return response
	}
	response.Message = "The controller rejected the configuration request."
	switch {
	case errors.Is(err, controllerstate.ErrConfigurationRevision):
		response.ErrorCode = "revision_conflict"
	case errors.Is(err, controllerstate.ErrProposalIDConflict):
		response.ErrorCode = "proposal_id_conflict"
	case errors.Is(err, controllerstate.ErrProposalNotFound):
		response.ErrorCode = "not_found"
	case errors.Is(err, controllerstate.ErrJobReference):
		response.ErrorCode = "job_reference"
	case errors.Is(err, controllerstate.ErrActivation):
		response.ErrorCode = "activation_invalid"
	case errors.Is(err, controllerstate.ErrDutyCycleEnabled):
		response.ErrorCode = "disable_required"
	case errors.Is(err, controllerstate.ErrTransitionActive):
		response.ErrorCode = "transition_active"
	default:
		response.ErrorCode = "invalid_request"
	}
	return response
}

func peerUID(connection *net.UnixConn) (uint32, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credentials *syscall.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if socketErr != nil || credentials == nil {
		return 0, fmt.Errorf("read controller peer credentials: %v", socketErr)
	}
	return credentials.Uid, nil
}

type Client struct {
	socketPath string
}

func NewClient(socketPath string) (*Client, error) {
	if socketPath == "" || socketPath[0] != '/' {
		return nil, fmt.Errorf("controller socket path must be absolute")
	}
	return &Client{socketPath: socketPath}, nil
}

func (client *Client) Health(ctx context.Context) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionHealth})
}

func (client *Client) Status(ctx context.Context) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionStatus})
}

func (client *Client) SaveDisabled(ctx context.Context, proposalID string, baseRevision uint64, configuration schedule.Configuration) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionSaveDisabled, ProposalID: proposalID, BaseRevision: baseRevision, Configuration: &configuration})
}

func (client *Client) ProposalStatus(ctx context.Context, proposalID string) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionProposal, ProposalID: proposalID})
}

func (client *Client) Preview(ctx context.Context, configurationRevision uint64, count int) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionPreview, ConfigurationRevision: configurationRevision, PreviewCount: count})
}

func (client *Client) EnableSimulation(ctx context.Context, proposalID string, baseRevision, configurationRevision uint64, review schedule.Review) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionEnableSimulation, ProposalID: proposalID, BaseRevision: baseRevision, ConfigurationRevision: configurationRevision, Review: &review})
}

func (client *Client) PreviewReal(ctx context.Context, configurationRevision uint64, count int) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionPreviewReal, ConfigurationRevision: configurationRevision, PreviewCount: count})
}

func (client *Client) EnableReal(ctx context.Context, proposalID string, baseRevision, configurationRevision uint64, review schedule.Review) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionEnableReal, ProposalID: proposalID, BaseRevision: baseRevision, ConfigurationRevision: configurationRevision, Review: &review})
}

func (client *Client) Disable(ctx context.Context, proposalID string, baseRevision, configurationRevision uint64) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionDisable, ProposalID: proposalID, BaseRevision: baseRevision, ConfigurationRevision: configurationRevision})
}

func (client *Client) SimulateExpectedBoot(ctx context.Context, baseRevision, configurationRevision uint64) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionSimulateBoot, BaseRevision: baseRevision, ConfigurationRevision: configurationRevision})
}

func (client *Client) ListJobs(ctx context.Context) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionJobList})
}

func (client *Client) CreateJob(ctx context.Context, input jobs.DefinitionInput) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionJobCreate, JobInput: &input})
}

func (client *Client) UpdateJob(ctx context.Context, id string, revision uint64, input jobs.DefinitionInput) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionJobUpdate, JobID: id, JobRevision: revision, JobInput: &input})
}

func (client *Client) DeleteJob(ctx context.Context, id string, revision uint64) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionJobDelete, JobID: id, JobRevision: revision})
}

func (client *Client) BeginRun(ctx context.Context, request jobs.Request, displayJobID string, adHoc bool) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionRunBegin, RunRequest: &request, DisplayJobID: displayJobID, AdHoc: adHoc})
}

func (client *Client) UpdateRun(ctx context.Context, result jobs.Response) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionRunUpdate, RunResponse: &result})
}

func (client *Client) MarkRunObserved(ctx context.Context, runID string, state jobs.RunState) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionRunObserve, RunID: runID, RunState: state})
}

func (client *Client) MarkRunLost(ctx context.Context, runID string) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionRunLost, RunID: runID})
}

func (client *Client) ListRuns(ctx context.Context, limit int, jobID string) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionRunList, RunLimit: limit, RunFilterJobID: jobID})
}

func (client *Client) GetRun(ctx context.Context, runID string) (Response, error) {
	return client.do(ctx, Request{Version: ProtocolVersion, Action: ActionRunGet, RunID: runID})
}

func (client *Client) do(ctx context.Context, request Request) (Response, error) {
	dialer := net.Dialer{Timeout: ioTimeout}
	connection, err := dialer.DialContext(ctx, "unix", client.socketPath)
	if err != nil {
		return Response{}, fmt.Errorf("connect to controller: %w", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(ioTimeout)); err != nil {
		return Response{}, err
	}
	if err := WriteRequest(connection, request); err != nil {
		return Response{}, err
	}
	response, err := ReadResponse(connection)
	if err != nil {
		return Response{}, err
	}
	if response.Action != request.Action {
		return Response{}, fmt.Errorf("%w: controller response action does not match request", ErrInvalidMessage)
	}
	if response.ErrorCode != "" {
		return response, &ControllerError{Code: response.ErrorCode, Message: response.Message}
	}
	return response, nil
}

type ControllerError struct {
	Code    string
	Message string
}

func (err *ControllerError) Error() string { return "controller: " + err.Code + ": " + err.Message }
