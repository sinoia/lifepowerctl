package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/adminlock"
	"github.com/sinoia/lifepowerctl/internal/auth"
	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/config"
	"github.com/sinoia/lifepowerctl/internal/controllerapi"
	"github.com/sinoia/lifepowerctl/internal/controllerstate"
	"github.com/sinoia/lifepowerctl/internal/dutycycle"
	"github.com/sinoia/lifepowerctl/internal/hostimage"
	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/overview"
	"github.com/sinoia/lifepowerctl/internal/power"
	"github.com/sinoia/lifepowerctl/internal/registeredit"
	"github.com/sinoia/lifepowerctl/internal/registers"
	"github.com/sinoia/lifepowerctl/internal/schedule"
	"github.com/sinoia/lifepowerctl/internal/server"
	"github.com/sinoia/lifepowerctl/internal/storage"
)

var (
	version     = "development"
	commit      = "unknown"
	sourceState = "development"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("lifepowerctl: %v", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: lifepowerctl <controller|serve|job-runner|admin|version>")
	}

	switch args[0] {
	case "version", "--version":
		return writeVersion(os.Stdout, args[1:])
	case "controller":
		return controller(args[1:])
	case "serve":
		return serve(args[1:])
	case "job-runner":
		return jobRunner(args[1:])
	case "admin":
		return admin(args[1:], defaultAdminEnvironment())
	default:
		return fmt.Errorf("unknown command %q; usage: lifepowerctl <controller|serve|job-runner|admin|version>", args[0])
	}
}

func writeVersion(output io.Writer, args []string) error {
	if len(args) > 1 || len(args) == 1 && args[0] != "--machine" {
		return errors.New("usage: lifepowerctl version [--machine]")
	}
	if len(args) == 1 {
		_, err := fmt.Fprintf(output, "format=1\nproduct=lifepowerctl\nversion=%s\ncommit=%s\nsource_state=%s\ngo=%s\ntarget_os=%s\ntarget_arch=%s\nstate_schema_current=%d\nstate_schema_min_readable=%d\n",
			version, commit, sourceState, runtime.Version(), runtime.GOOS, runtime.GOARCH, storage.CurrentSchema, storage.MinimumReadableSchema)
		return err
	}
	_, err := fmt.Fprintf(output, "lifepowerctl %s\ncommit: %s\nsource: %s\ngo: %s\ntarget: %s/%s\nstate schema: %d (reads %d-%d)\n",
		version, commit, sourceState, runtime.Version(), runtime.GOOS, runtime.GOARCH, storage.CurrentSchema, storage.MinimumReadableSchema, storage.CurrentSchema)
	return err
}

func controller(args []string) error {
	runtimeLimits := limits.Default()
	if err := runtimeLimits.Validate(); err != nil {
		return fmt.Errorf("validate runtime limits: %w", err)
	}
	debug.SetMemoryLimit(runtimeLimits.ControllerSoftMemory)
	settings, err := parseControllerConfig(args)
	if err != nil {
		return err
	}
	controllerLock, err := adminlock.Acquire(settings.StateDirectory)
	if err != nil {
		if errors.Is(err, adminlock.ErrHeld) {
			return errors.New("another controller or administrative command is already using the state directory")
		}
		return fmt.Errorf("acquire controller lock: %w", err)
	}
	defer controllerLock.Close()
	state, err := controllerstate.Open(settings.StateDirectory, runtimeLimits.Jobs)
	if err != nil {
		return fmt.Errorf("open controller state: %w", err)
	}
	journal, err := activity.Open(settings.ActivityDirectory, activity.Options{
		SegmentBytes: runtimeLimits.Persistence.ActivitySegmentBytes, MaxSegments: runtimeLimits.Persistence.ActivitySegments, MaxEventBytes: runtimeLimits.Persistence.ActivityEventBytes,
	})
	if err != nil {
		return fmt.Errorf("open controller activity journal: %w", err)
	}
	audit := activity.NewAudit(journal)
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		return fmt.Errorf("create register catalogue: %w", err)
	}
	coordinator, err := board.NewProcessCoordinator(filepath.Join(settings.StateDirectory, "hardware.lock"))
	if err != nil {
		return fmt.Errorf("create process hardware coordinator: %w", err)
	}
	wakeDevice, err := board.NewVendorWakeDevice(runtimeLimits.Vendor, catalogue)
	if err != nil {
		return fmt.Errorf("create wake device: %w", err)
	}
	wakeController, err := dutycycle.NewWakeController(wakeDevice, coordinator)
	if err != nil {
		return fmt.Errorf("create wake controller: %w", err)
	}
	expectedBoot := false
	reconciled := false
	if state.DutyCycleLifecycle().Phase == schedule.PhaseShutdownRequested {
		classificationCtx, cancel := context.WithTimeout(context.Background(), runtimeLimits.Polling.RegisterSnapshotTimeout)
		result, classifyErr := dutycycle.ClassifyStartupWithAudit(classificationCtx, state, wakeController, schedule.DefaultBootPolicy(), audit)
		cancel()
		expectedBoot = result.Classification.Class == schedule.BootExpected && classifyErr == nil
		if classifyErr != nil {
			if state.DutyCycleLifecycle().Phase != schedule.PhaseManualSafe {
				return fmt.Errorf("classify controller startup: %w", classifyErr)
			}
			log.Printf("lifepowerctl: startup classification entered manual-safe: %v", classifyErr)
		}
	} else {
		reconciled, err = state.ReconcileStartup()
		if err != nil {
			return fmt.Errorf("reconcile controller startup: %w", err)
		}
	}
	if reconciled {
		disarmCtx, cancel := context.WithTimeout(context.Background(), runtimeLimits.Polling.RegisterSnapshotTimeout)
		_, disarmErr, persistErr := dutycycle.ReconcilePendingDisarm(disarmCtx, state, wakeController)
		cancel()
		if persistErr != nil {
			return fmt.Errorf("reconcile pending wake disarm: %w", persistErr)
		}
		if disarmErr != nil {
			log.Printf("lifepowerctl: reconciled wake disarm remains uncertain: %v", disarmErr)
		}
		lifecycle := state.DutyCycleLifecycle()
		audit.Record(activity.DutyCycleTransition, activity.ActorSystem, "duty-cycle", struct {
			Phase     schedule.Phase `json:"phase"`
			Reason    string         `json:"reason"`
			Simulated bool           `json:"simulated"`
		}{Phase: lifecycle.Phase, Reason: lifecycle.Reason, Simulated: lifecycle.Simulated})
		runnerClient, clientErr := jobs.NewClient(settings.RunnerSocket, runtimeLimits.Jobs)
		if clientErr == nil {
			recoveryCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			if recoveryErr := dutycycle.RecoverRunner(recoveryCtx, state.DutyCycleLifecycle(), runnerClient); recoveryErr != nil {
				log.Printf("lifepowerctl: scheduled runner recovery: %v", recoveryErr)
			}
			cancel()
		}
	}
	service, err := controllerapi.NewService(state, uint32(os.Getuid()), runningBuildIdentity())
	if err != nil {
		return fmt.Errorf("create controller service: %w", err)
	}
	dutyRunner, err := jobs.NewClient(settings.RunnerSocket, runtimeLimits.Jobs)
	if err != nil {
		return fmt.Errorf("create duty-cycle runner client: %w", err)
	}
	executor, err := dutycycle.NewExecutor(state, dutyRunner, runtimeLimits.Polling.MinimumInterval)
	if err != nil {
		return fmt.Errorf("create duty-cycle executor: %w", err)
	}
	executor.SetAudit(audit)
	continuation, err := dutycycle.NewContinuation(state, wakeController)
	if err != nil {
		return fmt.Errorf("create duty-cycle continuation: %w", err)
	}
	continuation.SetAudit(audit)
	powerRequester, err := power.NewRequester(runtimeLimits.Power)
	if err != nil {
		return fmt.Errorf("create fixed power requester: %w", err)
	}
	shutdownController, err := dutycycle.NewShutdownController(state, wakeController, coordinator, powerRequester)
	if err != nil {
		return fmt.Errorf("create shutdown controller: %w", err)
	}
	shutdownController.SetAudit(audit)
	activator, err := dutycycle.NewActivator(state, wakeController, shutdownController)
	if err != nil {
		return fmt.Errorf("create real duty-cycle activator: %w", err)
	}
	activator.SetAudit(audit)
	service.SetSimulator(executor)
	service.SetRealActivator(activator)
	service.SetAudit(audit)
	listener, err := controllerapi.ListenUnix(settings.ControllerSocket)
	if err != nil {
		return fmt.Errorf("listen on controller socket: %w", err)
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go monitorJobRuns(ctx, state, dutyRunner, audit, runtimeLimits.Polling.MinimumInterval)
	if expectedBoot {
		go func() {
			if runErr := executor.RunObserved(ctx); runErr != nil {
				log.Printf("lifepowerctl: real duty-cycle run: %v", runErr)
				return
			}
			occurrence, continuationErr := continuation.ArmNext(ctx)
			if continuationErr != nil {
				log.Printf("lifepowerctl: real duty-cycle continuation: %v", continuationErr)
				return
			}
			if occurrence == 0 {
				return
			}
			if shutdownErr := shutdownController.Request(ctx); shutdownErr != nil {
				log.Printf("lifepowerctl: orderly shutdown request: %v", shutdownErr)
			}
		}()
	}
	log.Printf("LifePower Control controller listening on %s", settings.ControllerSocket)
	return service.Serve(ctx, listener)
}

func runningBuildIdentity() controllerapi.BuildIdentity {
	return controllerapi.BuildIdentity{
		Product: "lifepowerctl", Version: version, Commit: commit, SourceState: sourceState,
		TargetOS: runtime.GOOS, TargetArch: runtime.GOARCH,
		StateSchemaCurrent: storage.CurrentSchema, StateSchemaMinReadable: storage.MinimumReadableSchema,
	}
}

func parseControllerConfig(args []string) (config.Config, error) {
	flags := flag.NewFlagSet("controller", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "optional strict JSON configuration file")
	if err := flags.Parse(args); err != nil {
		return config.Config{}, err
	}
	if flags.NArg() != 0 {
		return config.Config{}, fmt.Errorf("controller: unexpected argument %q", flags.Arg(0))
	}
	return config.Load(*configPath)
}

type jobRunnerConfig struct {
	SocketPath     string
	SpoolDirectory string
	ControllerUID  uint32
}

func parseJobRunnerConfig(args []string) (jobRunnerConfig, error) {
	flags := flag.NewFlagSet("job-runner", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socketPath := flags.String("socket", "/run/lifepowerctl/jobs.sock", "fixed Unix socket path")
	spoolDirectory := flags.String("spool", "/var/lib/lifepowerctl-jobs", "runner spool directory")
	controllerUID := flags.Uint("controller-uid", 0, "allowed controller service UID")
	if err := flags.Parse(args); err != nil {
		return jobRunnerConfig{}, err
	}
	if flags.NArg() != 0 {
		return jobRunnerConfig{}, fmt.Errorf("job-runner: unexpected argument %q", flags.Arg(0))
	}
	for name, path := range map[string]string{"socket": *socketPath, "spool": *spoolDirectory} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return jobRunnerConfig{}, fmt.Errorf("job-runner %s path must be clean and absolute", name)
		}
	}
	if *controllerUID == 0 || uint64(*controllerUID) > uint64(^uint32(0)) {
		return jobRunnerConfig{}, errors.New("job-runner requires a nonzero -controller-uid")
	}
	return jobRunnerConfig{SocketPath: *socketPath, SpoolDirectory: *spoolDirectory, ControllerUID: uint32(*controllerUID)}, nil
}

func jobRunner(args []string) error {
	runtimeLimits := limits.Default()
	if err := runtimeLimits.Validate(); err != nil {
		return fmt.Errorf("validate runtime limits: %w", err)
	}
	debug.SetMemoryLimit(runtimeLimits.RunnerMemory)
	settings, err := parseJobRunnerConfig(args)
	if err != nil {
		return err
	}
	identity, err := jobs.CurrentExecutionIdentity()
	if err != nil {
		return fmt.Errorf("resolve job-runner identity: %w", err)
	}
	registry, err := jobs.NewPersistentRegistry(settings.SpoolDirectory, runtimeLimits.Jobs)
	if err != nil {
		return fmt.Errorf("open job-runner registry: %w", err)
	}
	supervisor, err := jobs.NewSupervisor(registry, runtimeLimits.Jobs, identity.Environment())
	if err != nil {
		return fmt.Errorf("create job supervisor: %w", err)
	}
	output, err := jobs.NewOutputStore(filepath.Join(settings.SpoolDirectory, "output"), runtimeLimits.Jobs)
	if err != nil {
		return fmt.Errorf("open job output store: %w", err)
	}
	service, err := jobs.NewService(registry, supervisor, output, settings.ControllerUID, runtimeLimits.Jobs)
	if err != nil {
		return fmt.Errorf("create job-runner service: %w", err)
	}
	listener, err := jobs.ListenUnix(settings.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on job-runner socket: %w", err)
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("LifePower Control job runner listening on %s as %s (UID %d)", settings.SocketPath, identity.User, identity.UID)
	return service.Serve(ctx, listener)
}

type runStatusClient interface {
	Do(context.Context, jobs.Request) (jobs.Response, error)
}

// combinedDutyCycleClient keeps the legacy combined development server useful
// while the web process is being split out. The standalone web process will
// satisfy the same interface with controllerapi.Client.
type combinedDutyCycleClient struct {
	state     *controllerstate.Manager
	audit     *activity.Audit
	simulator *dutycycle.Executor
	context   context.Context
}

func (source combinedDutyCycleClient) Status(context.Context) (controllerapi.Response, error) {
	lifecycle := source.state.DutyCycleLifecycle()
	response := controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionStatus, StateRevision: source.state.Revision(), Lifecycle: &lifecycle}
	if configuration, ok := source.state.DutyCycleConfiguration(); ok {
		response.ConfigurationRevision = configuration.Revision
		response.Configuration = &configuration
	}
	return response, nil
}

func (source combinedDutyCycleClient) SaveDisabled(_ context.Context, proposalID string, baseRevision uint64, configuration schedule.Configuration) (controllerapi.Response, error) {
	result, err := source.state.SaveDisabledConfiguration(proposalID, baseRevision, configuration)
	if err != nil {
		return controllerapi.Response{}, err
	}
	source.audit.Record(activity.DutyCycleConfiguration, activity.ActorAdmin, "duty-cycle", struct {
		Action                controllerapi.Action `json:"action"`
		StateRevision         uint64               `json:"state_revision"`
		ConfigurationRevision uint64               `json:"configuration_revision"`
		Simulated             bool                 `json:"simulated"`
	}{Action: controllerapi.ActionSaveDisabled, StateRevision: result.StateRevision, ConfigurationRevision: result.ConfigurationRevision})
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionSaveDisabled, StateRevision: result.StateRevision, ConfigurationRevision: result.ConfigurationRevision, Existing: result.Existing}, nil
}

func (source combinedDutyCycleClient) ProposalStatus(_ context.Context, proposalID string) (controllerapi.Response, error) {
	result, err := source.state.ConfigurationProposalResult(proposalID)
	if err != nil {
		return controllerapi.Response{}, err
	}
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionProposal, StateRevision: result.StateRevision, ConfigurationRevision: result.ConfigurationRevision, Existing: true}, nil
}

func (source combinedDutyCycleClient) Preview(_ context.Context, configurationRevision uint64, count int) (controllerapi.Response, error) {
	configuration, ok := source.state.DutyCycleConfiguration()
	if !ok || configuration.Revision != configurationRevision {
		return controllerapi.Response{}, controllerstate.ErrConfigurationRevision
	}
	now := time.Now().Unix()
	if now < 0 || uint64(now) > uint64(^uint32(0)) {
		return controllerapi.Response{}, errors.New("controller time is outside the supported range")
	}
	review, err := schedule.BuildReview(configuration, uint32(now), schedule.SimulatedMinimumLead, count)
	if err != nil {
		return controllerapi.Response{}, err
	}
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionPreview, StateRevision: source.state.Revision(), ConfigurationRevision: configurationRevision, Review: &review}, nil
}

func (source combinedDutyCycleClient) EnableSimulation(_ context.Context, proposalID string, baseRevision, configurationRevision uint64, review schedule.Review) (controllerapi.Response, error) {
	response := controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionEnableSimulation, StateRevision: source.state.Revision(), ConfigurationRevision: configurationRevision}
	configuration, ok := source.state.DutyCycleConfiguration()
	if !ok || configuration.Revision != configurationRevision || source.state.Revision() != baseRevision {
		return response, controllerstate.ErrConfigurationRevision
	}
	lead := time.Duration(review.MinimumLeadSeconds) * time.Second
	reviewed, err := schedule.BuildReview(configuration, review.Reference, lead, len(review.Occurrences))
	if err != nil || reviewed.Identity != review.Identity || lead != schedule.SimulatedMinimumLead {
		return response, errors.New("invalid occurrence review")
	}
	now := time.Now().Unix()
	if now < 0 || uint64(now) > uint64(^uint32(0)) {
		return response, errors.New("controller time is outside the supported range")
	}
	fresh, err := schedule.BuildReview(configuration, uint32(now), schedule.SimulatedMinimumLead, len(review.Occurrences))
	if err != nil {
		return response, err
	}
	if fresh.Occurrences[0].Unix != review.Occurrences[0].Unix {
		response.Review = &fresh
		return response, &controllerapi.ControllerError{Code: "confirmation_required", Message: "The first occurrence changed and requires another review."}
	}
	result, err := source.state.EnableSimulation(proposalID, baseRevision, configurationRevision, review.Identity, fresh.Occurrences[0].Unix)
	if err != nil {
		return response, err
	}
	response.StateRevision = result.StateRevision
	source.audit.Record(activity.DutyCycleConfiguration, activity.ActorAdmin, "duty-cycle", struct {
		Action                controllerapi.Action `json:"action"`
		StateRevision         uint64               `json:"state_revision"`
		ConfigurationRevision uint64               `json:"configuration_revision"`
		Simulated             bool                 `json:"simulated"`
	}{Action: controllerapi.ActionEnableSimulation, StateRevision: result.StateRevision, ConfigurationRevision: result.ConfigurationRevision, Simulated: true})
	return response, nil
}

func (source combinedDutyCycleClient) Disable(_ context.Context, proposalID string, baseRevision, configurationRevision uint64) (controllerapi.Response, error) {
	result, err := source.state.Disable(proposalID, baseRevision, configurationRevision)
	if err != nil {
		return controllerapi.Response{}, err
	}
	source.audit.Record(activity.DutyCycleConfiguration, activity.ActorAdmin, "duty-cycle", struct {
		Action                controllerapi.Action `json:"action"`
		StateRevision         uint64               `json:"state_revision"`
		ConfigurationRevision uint64               `json:"configuration_revision"`
	}{Action: controllerapi.ActionDisable, StateRevision: result.StateRevision, ConfigurationRevision: result.ConfigurationRevision})
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionDisable, StateRevision: result.StateRevision, ConfigurationRevision: result.ConfigurationRevision, Existing: result.Existing}, nil
}

func (source combinedDutyCycleClient) SimulateExpectedBoot(_ context.Context, baseRevision, configurationRevision uint64) (controllerapi.Response, error) {
	if source.simulator == nil || source.state.Revision() != baseRevision {
		return controllerapi.Response{}, errors.New("simulated boot is unavailable or stale")
	}
	configuration, ok := source.state.DutyCycleConfiguration()
	if !ok || configuration.Revision != configurationRevision {
		return controllerapi.Response{}, controllerstate.ErrConfigurationRevision
	}
	if err := source.simulator.ObserveExpectedSimulatedBoot(); err != nil {
		return controllerapi.Response{}, err
	}
	stateRevision := source.state.Revision()
	source.audit.Record(activity.DutyCycleTransition, activity.ActorAdmin, "duty-cycle", struct {
		Phase                 schedule.Phase `json:"phase"`
		ConfigurationRevision uint64         `json:"configuration_revision"`
		Simulated             bool           `json:"simulated"`
	}{Phase: schedule.PhaseExpectedBootObserved, ConfigurationRevision: configurationRevision, Simulated: true})
	go func() {
		if err := source.simulator.RunObserved(source.context); err != nil {
			log.Printf("lifepowerctl: combined simulated duty cycle: %v", err)
		}
	}()
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionSimulateBoot, StateRevision: stateRevision, ConfigurationRevision: configurationRevision}, nil
}

func (source combinedDutyCycleClient) ListJobs(context.Context) (controllerapi.Response, error) {
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionJobList, StateRevision: source.state.Revision(), Jobs: source.state.Jobs()}, nil
}

func (source combinedDutyCycleClient) CreateJob(_ context.Context, input jobs.DefinitionInput) (controllerapi.Response, error) {
	definition, err := source.state.Create(input)
	if err != nil {
		return controllerapi.Response{}, err
	}
	source.auditSavedJob(activity.JobCreated, definition)
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionJobCreate, StateRevision: source.state.Revision(), Job: &definition}, nil
}

func (source combinedDutyCycleClient) UpdateJob(_ context.Context, id string, revision uint64, input jobs.DefinitionInput) (controllerapi.Response, error) {
	definition, err := source.state.Update(id, revision, input)
	if err != nil {
		return controllerapi.Response{}, err
	}
	source.auditSavedJob(activity.JobUpdated, definition)
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionJobUpdate, StateRevision: source.state.Revision(), Job: &definition}, nil
}

func (source combinedDutyCycleClient) DeleteJob(_ context.Context, id string, revision uint64) (controllerapi.Response, error) {
	if err := source.state.Delete(id, revision); err != nil {
		return controllerapi.Response{}, err
	}
	source.audit.Record(activity.JobDeleted, activity.ActorAdmin, id, struct {
		Revision uint64 `json:"revision"`
	}{Revision: revision})
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionJobDelete, StateRevision: source.state.Revision()}, nil
}

func (source combinedDutyCycleClient) auditSavedJob(kind activity.Kind, definition jobs.Definition) {
	source.audit.Record(kind, activity.ActorAdmin, definition.ID, struct {
		Revision uint64             `json:"revision"`
		Enabled  bool               `json:"enabled"`
		Mode     jobs.ExecutionMode `json:"mode"`
	}{Revision: definition.Revision, Enabled: definition.Enabled, Mode: definition.Mode})
}

func (source combinedDutyCycleClient) BeginRun(_ context.Context, request jobs.Request, display string, adHoc bool) (controllerapi.Response, error) {
	run, err := source.state.BeginRun(request, display, adHoc)
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionRunBegin, StateRevision: source.state.Revision(), Run: &run}, err
}
func (source combinedDutyCycleClient) UpdateRun(_ context.Context, result jobs.Response) (controllerapi.Response, error) {
	run, changed, err := source.state.UpdateRun(result)
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionRunUpdate, StateRevision: source.state.Revision(), Run: &run, Changed: changed}, err
}
func (source combinedDutyCycleClient) MarkRunObserved(_ context.Context, runID string, state jobs.RunState) (controllerapi.Response, error) {
	err := source.state.MarkRunObserved(runID, state)
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionRunObserve, StateRevision: source.state.Revision()}, err
}
func (source combinedDutyCycleClient) MarkRunLost(_ context.Context, runID string) (controllerapi.Response, error) {
	run, err := source.state.MarkRunLost(runID)
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionRunLost, StateRevision: source.state.Revision(), Run: &run, Changed: true}, err
}
func (source combinedDutyCycleClient) ListRuns(_ context.Context, limit int, jobID string) (controllerapi.Response, error) {
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionRunList, StateRevision: source.state.Revision(), Runs: source.state.Runs(limit, jobID)}, nil
}
func (source combinedDutyCycleClient) GetRun(_ context.Context, runID string) (controllerapi.Response, error) {
	run, err := source.state.Run(runID)
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionRunGet, StateRevision: source.state.Revision(), Run: &run}, err
}

func monitorJobRuns(ctx context.Context, state *controllerstate.Manager, runner runStatusClient, audit *activity.Audit, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	reconcileJobRuns(ctx, state, runner, audit)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcileJobRuns(ctx, state, runner, audit)
		}
	}
}

func reconcileJobRuns(ctx context.Context, state *controllerstate.Manager, runner runStatusClient, audit *activity.Audit) {
	for _, tracked := range state.RunsNeedingPoll() {
		result, err := runner.Do(ctx, jobs.Request{Version: jobs.ProtocolVersion, Action: jobs.ActionStatus, RunID: tracked.RunID})
		if err != nil {
			var runnerErr *jobs.RunnerError
			if errors.As(err, &runnerErr) && runnerErr.Code == "not_found" {
				_, _ = state.MarkRunLost(tracked.RunID)
			}
			continue
		}
		_, _, _ = state.UpdateRun(result)
	}
	for _, tracked := range state.RunsNeedingObservation() {
		audit.Record(activity.JobRunObserved, activity.ActorSystem, tracked.RunID, struct {
			JobID       string        `json:"job_id"`
			State       jobs.RunState `json:"state"`
			ExitCode    *int          `json:"exit_code,omitempty"`
			Signal      string        `json:"signal,omitempty"`
			StdoutTotal int64         `json:"stdout_total"`
			StderrTotal int64         `json:"stderr_total"`
		}{JobID: tracked.JobID, State: tracked.State, ExitCode: tracked.ExitCode, Signal: tracked.Signal, StdoutTotal: tracked.StdoutTotal, StderrTotal: tracked.StderrTotal})
		_ = state.MarkRunObserved(tracked.RunID, tracked.State)
	}
}

func serve(args []string) error {
	runtimeLimits := limits.Default()
	if err := runtimeLimits.Validate(); err != nil {
		return fmt.Errorf("validate runtime limits: %w", err)
	}
	debug.SetMemoryLimit(runtimeLimits.ControllerSoftMemory)
	serviceConfig, err := parseServeConfig(args)
	if err != nil {
		return err
	}
	serveContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	authentication, err := auth.NewManager(serviceConfig.StateDirectory, runtimeLimits.Authentication)
	if err != nil {
		return fmt.Errorf("create authentication manager: %w", err)
	}
	journal, err := activity.Open(serviceConfig.ActivityDirectory, activity.Options{
		SegmentBytes:  runtimeLimits.Persistence.ActivitySegmentBytes,
		MaxSegments:   runtimeLimits.Persistence.ActivitySegments,
		MaxEventBytes: runtimeLimits.Persistence.ActivityEventBytes,
	})
	if err != nil {
		return fmt.Errorf("open activity journal: %w", err)
	}
	audit := activity.NewAudit(journal)
	authentication.SetAudit(audit)
	detector, err := board.NewVendorDetector(runtimeLimits.Vendor)
	if err != nil {
		return fmt.Errorf("create vendor detector: %w", err)
	}
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		return fmt.Errorf("create register catalogue: %w", err)
	}
	reader, err := board.NewVendorReader(runtimeLimits.Vendor, catalogue)
	if err != nil {
		return fmt.Errorf("create vendor register reader: %w", err)
	}
	coordinator, err := board.NewProcessCoordinator(filepath.Join(serviceConfig.StateDirectory, "hardware.lock"))
	if err != nil {
		return fmt.Errorf("create process hardware coordinator: %w", err)
	}
	overviewService := overview.New(detector, coordinator, runtimeLimits.Polling)
	overviewService.SetAudit(audit)
	registerService := registers.New(detector, reader, catalogue, coordinator, runtimeLimits.Polling)
	registerDevice, err := board.NewVendorDevice(runtimeLimits.Vendor, catalogue)
	if err != nil {
		return fmt.Errorf("create vendor register editor: %w", err)
	}
	registerEditor, err := registeredit.NewService(registerDevice, catalogue, coordinator, audit)
	if err != nil {
		return fmt.Errorf("create register editor: %w", err)
	}
	batteryDevice, err := board.NewVendorBatteryThresholdDevice(runtimeLimits.Vendor, catalogue)
	if err != nil {
		return fmt.Errorf("create vendor battery-threshold editor: %w", err)
	}
	batchRegisterEditor, err := registeredit.NewBatchService(batteryDevice, catalogue, coordinator, audit)
	if err != nil {
		return fmt.Errorf("create battery-threshold editor: %w", err)
	}
	shutdownDelayDevice, err := board.NewVendorShutdownDelayDevice(runtimeLimits.Vendor, catalogue)
	if err != nil {
		return fmt.Errorf("create vendor shutdown-delay editor: %w", err)
	}
	shutdownDelayEditor, err := registeredit.NewService(shutdownDelayDevice, catalogue, coordinator, audit)
	if err != nil {
		return fmt.Errorf("create shutdown-delay editor: %w", err)
	}
	runnerClient, err := jobs.NewClient(serviceConfig.RunnerSocket, runtimeLimits.Jobs)
	if err != nil {
		return fmt.Errorf("create job-runner client: %w", err)
	}
	controllerClient, err := controllerapi.NewClient(serviceConfig.ControllerSocket)
	if err != nil {
		return fmt.Errorf("create controller client: %w", err)
	}
	outputReader, err := jobs.NewOutputReader(serviceConfig.JobOutputDirectory, runtimeLimits.Jobs)
	if err != nil {
		return fmt.Errorf("create job-output reader: %w", err)
	}
	hostImageStore, err := hostimage.New(serviceConfig.StateDirectory, runtimeLimits.HostImage)
	if err != nil {
		return fmt.Errorf("create host image store: %w", err)
	}
	handler, err := server.New(server.Options{
		Build: server.BuildInfo{Version: version, Commit: commit}, Authentication: authentication, Overview: overviewService, Registers: registerService, RegisterEditor: registerEditor, BatchRegisterEditor: batchRegisterEditor, ShutdownDelayEditor: shutdownDelayEditor, Audit: audit, Activity: journal,
		ExternalOrigin: serviceConfig.ExternalOrigin, TrustedProxy: serviceConfig.TrustedProxy,
		MaxBodyBytes: runtimeLimits.HTTP.MaxBodyBytes, DiagnosticMaxBytes: runtimeLimits.Persistence.MaxSnapshotBytes,
		JobDefinitions: controllerClient, RunState: controllerClient, DutyCycle: controllerClient, JobLimits: runtimeLimits.Jobs, JobIdentity: serviceConfig.JobIdentity, JobRunner: runnerClient, JobOutput: outputReader, HostImage: hostImageStore,
	})
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	httpServer := &http.Server{
		Addr:              serviceConfig.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: runtimeLimits.HTTP.ReadHeaderTimeout,
		ReadTimeout:       runtimeLimits.HTTP.ReadTimeout,
		WriteTimeout:      runtimeLimits.HTTP.WriteTimeout,
		IdleTimeout:       runtimeLimits.HTTP.IdleTimeout,
		MaxHeaderBytes:    runtimeLimits.HTTP.MaxHeaderBytes,
	}
	listener, err := net.Listen("tcp", serviceConfig.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", serviceConfig.ListenAddress, err)
	}
	defer listener.Close()
	audit.Record(activity.ServiceStarted, activity.ActorSystem, "web", struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}{Version: version, Commit: commit})

	serveResult := make(chan error, 1)
	go func() {
		log.Printf("LifePower Control development shell listening on http://%s", listener.Addr())
		serveResult <- httpServer.Serve(listener)
	}()

	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-serveContext.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), runtimeLimits.HTTP.ShutdownTimeout)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

type adminEnvironment struct {
	euid        func() int
	openTTY     func() (io.WriteCloser, error)
	output      io.Writer
	detectBoard func(context.Context, config.Config) (board.Identity, error)
}

func defaultAdminEnvironment() adminEnvironment {
	return adminEnvironment{
		euid: os.Geteuid,
		openTTY: func() (io.WriteCloser, error) {
			return os.OpenFile("/dev/tty", os.O_RDWR, 0)
		},
		output:      os.Stdout,
		detectBoard: detectBoardForStatus,
	}
}

func admin(args []string, environment adminEnvironment) error {
	if len(args) == 0 {
		return errors.New("usage: lifepowerctl admin <status|ensure-setup|reset-auth> [-config path]")
	}
	switch args[0] {
	case "status":
		return adminStatus(args[1:], environment)
	case "ensure-setup":
		return adminSetup(args[1:], environment, false)
	case "reset-auth":
		return adminSetup(args[1:], environment, true)
	default:
		return errors.New("usage: lifepowerctl admin <status|ensure-setup|reset-auth> [-config path]")
	}
}

func adminSetup(args []string, environment adminEnvironment, reset bool) error {
	action := "ensure-setup"
	verb := "creating initial authentication"
	if reset {
		action = "reset-auth"
		verb = "resetting authentication"
	}
	if environment.euid() != 0 {
		return fmt.Errorf("admin %s requires root; run it with sudo", action)
	}
	terminal, err := environment.openTTY()
	if err != nil {
		return fmt.Errorf("admin %s requires a controlling terminal", action)
	}
	defer terminal.Close()

	flags := flag.NewFlagSet("admin "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "optional strict JSON configuration file")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("admin %s: %w", action, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("admin %s: unexpected argument %q", action, flags.Arg(0))
	}
	serviceConfig, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	stateOwner, err := directoryOwner(serviceConfig.StateDirectory)
	if err != nil {
		return fmt.Errorf("inspect state directory ownership: %w", err)
	}
	controllerLock, err := adminlock.Acquire(serviceConfig.StateDirectory)
	if err != nil {
		if errors.Is(err, adminlock.ErrHeld) {
			return fmt.Errorf("controller is running; stop lifepowerctl-controller.service before %s", verb)
		}
		return fmt.Errorf("acquire controller lock: %w", err)
	}
	defer controllerLock.Close()

	runtimeLimits := limits.Default()
	authentication, err := auth.NewManager(serviceConfig.StateDirectory, runtimeLimits.Authentication)
	if err != nil {
		return fmt.Errorf("open authentication state: %w", err)
	}
	var setup auth.Setup
	if reset {
		setup, err = authentication.ResetSetup()
	} else {
		setup, err = authentication.EnsureSetup()
	}
	if err != nil {
		if !reset && errors.Is(err, auth.ErrAlreadyConfigured) {
			return writeAdminSetupStatus(environment.output, "configured")
		}
		if !reset && errors.Is(err, auth.ErrSetupPending) {
			return writeAdminSetupStatus(environment.output, "setup-pending")
		}
		return fmt.Errorf("%s: %w", verb, err)
	}
	if err := stateOwner.apply(
		filepath.Join(serviceConfig.StateDirectory, "controller.lock"),
		filepath.Join(serviceConfig.StateDirectory, "auth.json"),
		filepath.Join(serviceConfig.StateDirectory, "auth.previous.json"),
	); err != nil {
		return fmt.Errorf("restore authentication ownership: %w", err)
	}
	if reset {
		if journal, journalErr := activity.Open(serviceConfig.ActivityDirectory, activity.Options{
			SegmentBytes:  runtimeLimits.Persistence.ActivitySegmentBytes,
			MaxSegments:   runtimeLimits.Persistence.ActivitySegments,
			MaxEventBytes: runtimeLimits.Persistence.ActivityEventBytes,
		}); journalErr == nil {
			activity.NewAudit(journal).Record(activity.AuthenticationReset, activity.ActorLocal, "authentication", nil)
			if activityOwner, ownerErr := directoryOwner(serviceConfig.ActivityDirectory); ownerErr == nil {
				segments, _ := filepath.Glob(filepath.Join(serviceConfig.ActivityDirectory, "activity-*.jsonl"))
				_ = activityOwner.apply(segments...)
			}
		}
	}
	setupURL := serviceConfig.ExternalOrigin + "/setup?token=" + url.QueryEscape(setup.Token)
	message := "Initial authentication setup created."
	if reset {
		message = "Authentication reset. Existing browser sessions are no longer valid."
	}
	if _, err := fmt.Fprintf(terminal, "%s\n\nOne-time setup link (expires %s):\n%s\n", message, setup.ExpiresAt.Local().Format(time.RFC1123), setupURL); err != nil {
		return fmt.Errorf("write setup link: %w", err)
	}
	if !reset {
		return writeAdminSetupStatus(environment.output, "setup-created")
	}
	return nil
}

func writeAdminSetupStatus(output io.Writer, status string) error {
	if output == nil {
		output = io.Discard
	}
	_, err := fmt.Fprintf(output, "format=1\nauthentication=%s\n", status)
	return err
}

func adminStatus(args []string, environment adminEnvironment) error {
	flags := flag.NewFlagSet("admin status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "optional strict JSON configuration file")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("admin status: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("admin status: unexpected argument %q", flags.Arg(0))
	}
	settings, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	output := environment.output
	if output == nil {
		output = io.Discard
	}

	report := localStatusReport{
		Controller: "unavailable", Runner: "unavailable", Board: "unavailable",
		Automation: "unknown", Lifecycle: "unknown", Jobs: "unknown", Maintenance: "unknown",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client, clientErr := controllerapi.NewClient(settings.ControllerSocket)
	if clientErr == nil {
		health, healthErr := client.Health(ctx)
		if healthErr == nil && health.Build != nil {
			report.Controller = string(health.Health)
			report.StateRevision = health.StateRevision
			report.StateSchema = health.StateSchema
			report.Build = *health.Build
			status, statusErr := client.Status(ctx)
			if statusErr == nil && status.Lifecycle != nil {
				report.Lifecycle = string(status.Lifecycle.Phase)
				report.Automation = "absent"
				if status.Configuration != nil {
					report.Automation = "disabled"
					if status.Configuration.Enabled {
						report.Automation = "enabled"
					}
				}
				report.Jobs = "idle"
				if status.ActiveRuns != 0 {
					report.Jobs = "active"
				}
				if health.Health == controllerapi.HealthReady && maintenanceSafe(status) {
					report.Maintenance = "safe"
				} else {
					report.Maintenance = "unsafe"
				}
			}
		}
	}

	runner, runnerErr := jobs.NewClient(settings.RunnerSocket, limits.Default().Jobs)
	if runnerErr == nil {
		probeID, idErr := jobs.NewRunID()
		if idErr == nil {
			_, probeErr := runner.Do(ctx, jobs.Request{Version: jobs.ProtocolVersion, Action: jobs.ActionStatus, RunID: probeID})
			var remoteError *jobs.RunnerError
			if errors.As(probeErr, &remoteError) && remoteError.Code == "not_found" {
				report.Runner = "ready"
			}
		}
	}

	if environment.detectBoard != nil {
		identity, detectErr := environment.detectBoard(ctx, settings)
		if detectErr == nil {
			report.Board = "ready"
			report.BoardProduct = identity.Product
			report.RegisterSet = identity.RegisterSet
		}
	}
	return report.write(output)
}

func detectBoardForStatus(ctx context.Context, settings config.Config) (board.Identity, error) {
	coordinator, err := board.NewProcessCoordinator(filepath.Join(settings.StateDirectory, "hardware.lock"))
	if err != nil {
		return board.Identity{}, err
	}
	detector, err := board.NewVendorDetector(limits.Default().Vendor)
	if err != nil {
		return board.Identity{}, err
	}
	var identity board.Identity
	err = coordinator.WithLease(ctx, board.OperationRead, "packaging preflight identity", func() error {
		var detectErr error
		identity, detectErr = detector.Detect(ctx)
		return detectErr
	})
	return identity, err
}

type localStatusReport struct {
	Controller, Runner, Board, Automation, Lifecycle, Jobs, Maintenance string
	StateRevision                                                       uint64
	StateSchema                                                         int
	Build                                                               controllerapi.BuildIdentity
	BoardProduct                                                        string
	RegisterSet                                                         int
}

func maintenanceSafe(response controllerapi.Response) bool {
	if response.Lifecycle == nil || response.ActiveRuns != 0 || response.Configuration != nil && response.Configuration.Enabled {
		return false
	}
	switch response.Lifecycle.Phase {
	case schedule.PhaseInactive, schedule.PhaseCompleted:
		return true
	case schedule.PhaseManualSafe:
		return response.Lifecycle.Simulated || response.Lifecycle.WakeDisarmStatus == schedule.DisarmVerified
	default:
		return false
	}
}

func (report localStatusReport) write(output io.Writer) error {
	_, err := fmt.Fprintf(output, "format=1\nproduct=lifepowerctl\ncontroller=%s\nrunning_version=%s\nrunning_commit=%s\nrunning_source_state=%s\nrunning_target_os=%s\nrunning_target_arch=%s\nstate_schema_current=%d\nstate_schema_min_readable=%d\nstate_schema=%d\nstate_revision=%d\nautomation=%s\nlifecycle=%s\njobs=%s\nrunner=%s\nboard=%s\nboard_product=%s\nregister_set=%d\nmaintenance=%s\n",
		report.Controller, report.Build.Version, report.Build.Commit, report.Build.SourceState,
		report.Build.TargetOS, report.Build.TargetArch, report.Build.StateSchemaCurrent,
		report.Build.StateSchemaMinReadable, report.StateSchema, report.StateRevision, report.Automation,
		report.Lifecycle, report.Jobs, report.Runner, report.Board, report.BoardProduct,
		report.RegisterSet, report.Maintenance)
	return err
}

type fileOwner struct {
	uid int
	gid int
}

func directoryOwner(path string) (fileOwner, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileOwner{}, err
	}
	if !info.IsDir() {
		return fileOwner{}, fmt.Errorf("%s is not a directory", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileOwner{}, fmt.Errorf("ownership is unavailable")
	}
	return fileOwner{uid: int(stat.Uid), gid: int(stat.Gid)}, nil
}

func (owner fileOwner) apply(paths ...string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	for _, path := range paths {
		if err := os.Chown(path, owner.uid, owner.gid); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func parseServeConfig(args []string) (config.Config, error) {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := flags.String("config", "", "optional strict JSON configuration file")
	listen := flags.String("listen", "", "development listen-address override")
	externalOrigin := flags.String("external-origin", "", "external-origin override used with -listen")
	if err := flags.Parse(args); err != nil {
		return config.Config{}, err
	}
	if flags.NArg() != 0 {
		return config.Config{}, fmt.Errorf("serve: unexpected argument %q", flags.Arg(0))
	}
	value, err := config.Load(*configPath)
	if err != nil {
		return config.Config{}, err
	}
	if *listen != "" {
		value.ListenAddress = *listen
		if *externalOrigin == "" {
			value.ExternalOrigin = "http://" + *listen
		}
	}
	if *externalOrigin != "" {
		value.ExternalOrigin = *externalOrigin
	}
	if err := value.Validate(); err != nil {
		return config.Config{}, err
	}
	return value, nil
}
