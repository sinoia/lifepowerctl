//go:build linux

package controllerapi

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/controllerstate"
	"github.com/sinoia/lifepowerctl/internal/dutycycle"
	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type testState struct {
	revision      uint64
	recovered     bool
	configuration *schedule.Configuration
}

var testBuild = BuildIdentity{
	Product: "lifepowerctl", Version: "v0.1.0-test", Commit: "0123456789abcdef", SourceState: "clean",
	TargetOS: "linux", TargetArch: "arm64", StateSchemaCurrent: 1, StateSchemaMinReadable: 1,
}

type testSimulator struct {
	observeErr error
	observed   bool
	ran        chan struct{}
}

type testRealActivator struct {
	review     schedule.Review
	result     controllerstate.ConfigurationResult
	previewErr error
	enableErr  error
}

func (activator *testRealActivator) Preview(context.Context, uint64, int) (schedule.Review, error) {
	return activator.review, activator.previewErr
}

func (activator *testRealActivator) Enable(context.Context, string, uint64, uint64, schedule.Review) (controllerstate.ConfigurationResult, error) {
	return activator.result, activator.enableErr
}

func (simulator *testSimulator) ObserveExpectedSimulatedBoot() error {
	simulator.observed = true
	return simulator.observeErr
}

func (simulator *testSimulator) RunObserved(context.Context) error {
	close(simulator.ran)
	return nil
}

func (state testState) Revision() uint64 { return state.revision }
func (state testState) Schema() int {
	if state.revision == 0 {
		return 0
	}
	return 1
}
func (state testState) Recovered() bool { return state.recovered }
func (state testState) SaveDisabledConfiguration(string, uint64, schedule.Configuration) (controllerstate.ConfigurationResult, error) {
	return controllerstate.ConfigurationResult{}, nil
}
func (state testState) ConfigurationProposalResult(string) (controllerstate.ConfigurationResult, error) {
	return controllerstate.ConfigurationResult{}, controllerstate.ErrProposalNotFound
}
func (state testState) DutyCycleConfiguration() (schedule.Configuration, bool) {
	if state.configuration == nil {
		return schedule.Configuration{}, false
	}
	return schedule.CloneConfiguration(*state.configuration), true
}
func (state testState) DutyCycleLifecycle() schedule.Lifecycle {
	if state.configuration == nil {
		return schedule.Lifecycle{Phase: schedule.PhaseInactive}
	}
	return schedule.Lifecycle{Phase: schedule.PhaseSimulatedShutdownIntended, ConfigurationRevision: state.configuration.Revision, IntendedOccurrence: 1, Simulated: true}
}
func (state testState) DutyCycleRunListMaximum() (int, uint64, error) {
	if state.configuration == nil {
		return 0, 0, controllerstate.ErrActivation
	}
	return len(state.configuration.RunList), uint64(len(state.configuration.RunList) * 65), nil
}
func (state testState) EnableSimulation(string, uint64, uint64, string, uint32) (controllerstate.ConfigurationResult, error) {
	return controllerstate.ConfigurationResult{StateRevision: state.revision + 1, ConfigurationRevision: state.configuration.Revision}, nil
}
func (state testState) SimulationResult(string, uint64, uint64, string, uint32) (controllerstate.ConfigurationResult, error) {
	return controllerstate.ConfigurationResult{}, controllerstate.ErrProposalNotFound
}
func (state testState) Disable(string, uint64, uint64) (controllerstate.ConfigurationResult, error) {
	return controllerstate.ConfigurationResult{StateRevision: state.revision + 1, ConfigurationRevision: state.configuration.Revision}, nil
}
func (state testState) Jobs() []jobs.Definition { return nil }
func (state testState) Create(input jobs.DefinitionInput) (jobs.Definition, error) {
	return jobs.Definition{ID: "00112233445566778899aabbccddeeff", Revision: 1, DefinitionInput: input}, nil
}
func (state testState) Update(id string, revision uint64, input jobs.DefinitionInput) (jobs.Definition, error) {
	return jobs.Definition{ID: id, Revision: revision + 1, DefinitionInput: input}, nil
}
func (state testState) Delete(string, uint64) error { return nil }
func (state testState) BeginRun(request jobs.Request, display string, adHoc bool) (controllerstate.Run, error) {
	return controllerstate.Run{RunID: request.RunID, DisplayJobID: display, AdHoc: adHoc}, nil
}
func (state testState) UpdateRun(response jobs.Response) (controllerstate.Run, bool, error) {
	return controllerstate.Run{RunID: response.RunID, State: response.State}, true, nil
}
func (state testState) MarkRunObserved(string, jobs.RunState) error { return nil }
func (state testState) MarkRunLost(runID string) (controllerstate.Run, error) {
	return controllerstate.Run{RunID: runID, State: jobs.StateLost}, nil
}
func (state testState) Runs(int, string) []controllerstate.Run { return nil }
func (state testState) ActiveRunCount() int                    { return 0 }
func (state testState) Run(runID string) (controllerstate.Run, error) {
	return controllerstate.Run{RunID: runID, State: jobs.StateSucceeded}, nil
}

func TestServiceReportsAuthoritativeStateHealth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.sock")
	listener, err := ListenUnix(path)
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skip("Unix sockets are unavailable in this test sandbox")
		}
		t.Fatal(err)
	}
	service, err := NewService(testState{revision: 9, recovered: true}, uint32(os.Getuid()), testBuild)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Serve(ctx, listener) }()
	client, err := NewClient(path)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if response.Health != HealthRecovered || response.StateRevision != 9 || response.StateSchema != 1 || response.Build == nil || *response.Build != testBuild {
		t.Fatalf("Health() = %+v", response)
	}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.StateRevision != 9 || status.StateSchema != 1 || status.Configuration != nil || status.Lifecycle == nil || status.Lifecycle.Phase != schedule.PhaseInactive {
		t.Fatalf("Status() = %+v", status)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestListenUnixReclaimsOnlyStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skip("Unix sockets are unavailable in this test sandbox")
		}
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := ListenUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reclaimed.Close()
	if _, err := ListenUnix(path); err == nil {
		t.Fatal("replaced active controller socket")
	}
}

func TestListenUnixDoesNotReplaceRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.sock")
	if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenUnix(path); err == nil {
		t.Fatal("replaced regular file at controller socket path")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("regular file = %q, %v", data, err)
	}
}

func TestDisconnectedMalformedClientDoesNotStopController(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.sock")
	listener, err := ListenUnix(path)
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skip("Unix sockets are unavailable in this test sandbox")
		}
		t.Fatal(err)
	}
	service, err := NewService(testState{revision: 9}, uint32(os.Getuid()), testBuild)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Serve(ctx, listener) }()

	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, 100)
	if _, err := connection.Write(append(header, '{')); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}

	client, err := NewClient(path)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Health(context.Background())
	if err != nil || response.Health != HealthReady || response.StateRevision != 9 {
		t.Fatalf("Health() after malformed disconnect = %+v, %v", response, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServiceReportsAuthoritativeDutyCycleStatus(t *testing.T) {
	configuration := schedule.Configuration{Revision: 3, Enabled: true, Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15}}
	service, err := NewService(testState{revision: 9, configuration: &configuration}, uint32(os.Getuid()), testBuild)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := service.state.DutyCycleLifecycle()
	response := Response{Version: ProtocolVersion, Action: ActionStatus, StateRevision: service.state.Revision(), StateSchema: service.state.Schema(), ConfigurationRevision: configuration.Revision, Configuration: &configuration, Lifecycle: &lifecycle}
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	if response.Configuration == nil || response.Lifecycle.Phase != schedule.PhaseSimulatedShutdownIntended {
		t.Fatalf("status = %+v", response)
	}
}

func TestPreviewResponseUsesControllerTimeAndSavedRevision(t *testing.T) {
	configuration := schedule.Configuration{Revision: 3, Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15}}
	service, err := NewService(testState{revision: 8, configuration: &configuration}, uint32(os.Getuid()), testBuild)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, time.October, 11, 10, 0, 0, 0, time.UTC) }
	response := service.previewResponse(Request{Version: ProtocolVersion, Action: ActionPreview, ConfigurationRevision: 3, PreviewCount: 5})
	if response.ErrorCode != "" || response.Review == nil || len(response.Review.Occurrences) != 5 || response.Review.Occurrences[0].Local != "2026-10-11 10:15:00" {
		t.Fatalf("preview response = %+v", response)
	}
	stale := service.previewResponse(Request{Version: ProtocolVersion, Action: ActionPreview, ConfigurationRevision: 2, PreviewCount: 5})
	if stale.ErrorCode != "revision_conflict" || stale.Review != nil {
		t.Fatalf("stale response = %+v", stale)
	}
}

func TestEnableSimulationRecalculatesAndRequiresChangedOccurrenceReview(t *testing.T) {
	configuration := schedule.Configuration{Revision: 3, Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15}, RunList: []schedule.RunListEntry{{JobID: "job", JobRevision: 1}}}
	service, err := NewService(testState{revision: 8, configuration: &configuration}, uint32(os.Getuid()), testBuild)
	if err != nil {
		t.Fatal(err)
	}
	review, err := schedule.BuildReview(configuration, uint32(time.Date(2026, time.October, 11, 10, 0, 0, 0, time.UTC).Unix()), simulatedMinimumLead, 5)
	if err != nil {
		t.Fatal(err)
	}
	review.RunListJobs, review.MaximumRunSeconds = 1, 65
	request := Request{Version: ProtocolVersion, Action: ActionEnableSimulation, ProposalID: "00112233445566778899aabbccddeeff", BaseRevision: 8, ConfigurationRevision: 3, Review: &review}
	service.now = func() time.Time { return time.Date(2026, time.October, 11, 10, 1, 0, 0, time.UTC) }
	response := service.enableSimulationResponse(request)
	if response.ErrorCode != "" || response.StateRevision != 9 {
		t.Fatalf("enable response = %+v", response)
	}
	tampered := request
	tampered.ProposalID = "11112233445566778899aabbccddeeff"
	tampered.Review = &review
	tampered.Review.MaximumRunSeconds++
	if rejected := service.enableSimulationResponse(tampered); rejected.ErrorCode != "invalid_review" {
		t.Fatalf("tampered duration response = %+v", rejected)
	}
	review.MaximumRunSeconds--
	service.now = func() time.Time { return time.Date(2026, time.October, 11, 10, 15, 0, 0, time.UTC) }
	response = service.enableSimulationResponse(request)
	if response.ErrorCode != "confirmation_required" || response.Review == nil || response.Review.Occurrences[0].Local != "2026-10-11 11:15:00" {
		t.Fatalf("changed response = %+v", response)
	}
}

func TestRealActivationResponsesExposeBoardReviewAndConfirmation(t *testing.T) {
	configuration := schedule.Configuration{Revision: 3, Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15}}
	review, err := schedule.BuildReview(configuration, 1_800_000_000, schedule.VerifiedWakeMinimumLead, 5)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(testState{revision: 8, configuration: &configuration}, uint32(os.Getuid()), testBuild)
	if err != nil {
		t.Fatal(err)
	}
	activator := &testRealActivator{review: review, result: controllerstate.ConfigurationResult{StateRevision: 9, ConfigurationRevision: 3}}
	service.SetRealActivator(activator)
	preview := service.previewRealResponse(Request{Version: ProtocolVersion, Action: ActionPreviewReal, ConfigurationRevision: 3, PreviewCount: 5})
	if preview.ErrorCode != "" || preview.Review == nil || preview.Review.Reference != review.Reference {
		t.Fatalf("real preview = %+v", preview)
	}
	activator.previewErr = schedule.ErrNoOccurrence
	noOccurrence := service.previewRealResponse(Request{Version: ProtocolVersion, Action: ActionPreviewReal, ConfigurationRevision: 3, PreviewCount: 5})
	if noOccurrence.ErrorCode != "no_occurrence" || noOccurrence.Review != nil {
		t.Fatalf("no-occurrence preview = %+v", noOccurrence)
	}
	activator.previewErr = nil
	enabled := service.enableRealResponse(Request{Version: ProtocolVersion, Action: ActionEnableReal, ProposalID: "00112233445566778899aabbccddeeff", BaseRevision: 8, ConfigurationRevision: 3, Review: &review})
	if enabled.ErrorCode != "" || enabled.StateRevision != 9 {
		t.Fatalf("real enable = %+v", enabled)
	}
	activator.enableErr = schedule.ErrNoOccurrence
	expired := service.enableRealResponse(Request{Version: ProtocolVersion, Action: ActionEnableReal, ProposalID: "22222233445566778899aabbccddeeff", BaseRevision: 8, ConfigurationRevision: 3, Review: &review})
	if expired.ErrorCode != "no_occurrence" || expired.Review != nil || expired.StateRevision != 8 {
		t.Fatalf("expired confirmation = %+v", expired)
	}
	changed := review
	changed.Reference++
	activator.enableErr = &dutycycle.ConfirmationRequiredError{Review: changed}
	confirmation := service.enableRealResponse(Request{Version: ProtocolVersion, Action: ActionEnableReal, ProposalID: "11112233445566778899aabbccddeeff", BaseRevision: 8, ConfigurationRevision: 3, Review: &review})
	if confirmation.ErrorCode != "confirmation_required" || confirmation.Review == nil || confirmation.Review.Reference != changed.Reference {
		t.Fatalf("real confirmation = %+v", confirmation)
	}
	if err := confirmation.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSimulatedBootIsConsumedBeforeBackgroundRun(t *testing.T) {
	configuration := schedule.Configuration{Revision: 3, Enabled: true, Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15}}
	service, err := NewService(testState{revision: 8, configuration: &configuration}, uint32(os.Getuid()), testBuild)
	if err != nil {
		t.Fatal(err)
	}
	simulator := &testSimulator{ran: make(chan struct{})}
	service.SetSimulator(simulator)
	response := service.simulateBootResponse(Request{Version: ProtocolVersion, Action: ActionSimulateBoot, BaseRevision: 8, ConfigurationRevision: 3})
	if response.ErrorCode != "" || !simulator.observed {
		t.Fatalf("response = %+v, observed = %v", response, simulator.observed)
	}
	select {
	case <-simulator.ran:
	case <-time.After(time.Second):
		t.Fatal("background simulated run did not start")
	}
}

func TestSavedJobDefinitionsRoundTripThroughController(t *testing.T) {
	state, err := controllerstate.Open(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "controller.sock")
	listener, err := ListenUnix(path)
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skip("Unix sockets are unavailable in this test sandbox")
		}
		t.Fatal(err)
	}
	service, err := NewService(state, uint32(os.Getuid()), testBuild)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Serve(ctx, listener) }()
	client, err := NewClient(path)
	if err != nil {
		t.Fatal(err)
	}
	input := jobs.DefinitionInput{Name: "Camera", Enabled: true, Mode: jobs.ModeDirect, Executable: "/bin/true", WorkingDirectory: "/tmp", TimeoutSeconds: 30, StdoutBytes: 1024, StderrBytes: 1024}
	created, err := client.CreateJob(context.Background(), input)
	if err != nil || created.Job == nil || created.Job.Revision != 1 {
		t.Fatalf("CreateJob() = %+v, %v", created, err)
	}
	listed, err := client.ListJobs(context.Background())
	if err != nil || len(listed.Jobs) != 1 || listed.Jobs[0].ID != created.Job.ID {
		t.Fatalf("ListJobs() = %+v, %v", listed, err)
	}
	input.Name = "Updated camera"
	updated, err := client.UpdateJob(context.Background(), created.Job.ID, 1, input)
	if err != nil || updated.Job == nil || updated.Job.Revision != 2 {
		t.Fatalf("UpdateJob() = %+v, %v", updated, err)
	}
	runID := "ffeeddccbbaa99887766554433221100"
	started, err := client.BeginRun(context.Background(), updated.Job.Start(runID), updated.Job.ID, false)
	if err != nil || started.Run == nil || started.Run.State != jobs.StateQueued {
		t.Fatalf("BeginRun() = %+v, %v", started, err)
	}
	running, err := client.UpdateRun(context.Background(), jobs.Response{Version: jobs.ProtocolVersion, RunID: runID, State: jobs.StateRunning})
	if err != nil || running.Run == nil || !running.Changed || running.Run.State != jobs.StateRunning {
		t.Fatalf("UpdateRun() = %+v, %v", running, err)
	}
	if _, err := client.MarkRunObserved(context.Background(), runID, jobs.StateRunning); err != nil {
		t.Fatal(err)
	}
	history, err := client.ListRuns(context.Background(), 10, updated.Job.ID)
	if err != nil || len(history.Runs) != 1 || history.Runs[0].RunID != runID {
		t.Fatalf("ListRuns() = %+v, %v", history, err)
	}
	detail, err := client.GetRun(context.Background(), runID)
	if err != nil || detail.Run == nil || detail.Run.RunID != runID {
		t.Fatalf("GetRun() = %+v, %v", detail, err)
	}
	lost, err := client.MarkRunLost(context.Background(), runID)
	if err != nil || lost.Run == nil || lost.Run.State != jobs.StateLost {
		t.Fatalf("MarkRunLost() = %+v, %v", lost, err)
	}
	if _, err := client.DeleteJob(context.Background(), created.Job.ID, 2); err != nil {
		t.Fatal(err)
	}
	listed, err = client.ListJobs(context.Background())
	if err != nil || len(listed.Jobs) != 0 {
		t.Fatalf("final ListJobs() = %+v, %v", listed, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
