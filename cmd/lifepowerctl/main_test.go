package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/adminlock"
	"github.com/sinoia/lifepowerctl/internal/auth"
	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/config"
	"github.com/sinoia/lifepowerctl/internal/controllerapi"
	"github.com/sinoia/lifepowerctl/internal/controllerstate"
	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type completedRunClient struct{}

func (completedRunClient) Do(_ context.Context, request jobs.Request) (jobs.Response, error) {
	exit := 0
	return jobs.Response{Version: jobs.ProtocolVersion, RunID: request.RunID, State: jobs.StateSucceeded, ExitCode: &exit, StdoutTotal: 7}, nil
}

func TestReconcileJobRunsPersistsAndAuditsTerminalState(t *testing.T) {
	jobLimits := limits.Default().Jobs
	state, err := controllerstate.Open(t.TempDir(), jobLimits)
	if err != nil {
		t.Fatal(err)
	}
	request := jobs.Request{Version: jobs.ProtocolVersion, Action: jobs.ActionStart, RunID: "00112233445566778899aabbccddeeff", Start: &jobs.StartRequest{
		JobID: "test-job", JobRevision: 1, Mode: jobs.ModeDirect, Executable: "/bin/true", WorkingDirectory: "/tmp", TimeoutSeconds: 30, StdoutBytes: 1024, StderrBytes: 1024,
	}}
	if _, err := state.BeginRun(request, "test-job", false); err != nil {
		t.Fatal(err)
	}
	journal, err := activity.Open(t.TempDir(), activity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	reconcileJobRuns(context.Background(), state, completedRunClient{}, activity.NewAudit(journal))
	if len(state.RunsNeedingPoll()) != 0 || len(state.RunsNeedingObservation()) != 0 {
		t.Fatal("terminal run was not persisted and marked observed")
	}
	events, err := journal.Events()
	if err != nil || len(events) != 1 || events[0].Type != string(activity.JobRunObserved) {
		t.Fatalf("events = %+v, %v", events, err)
	}
}

func TestServeConfigurationDefaultsToLoopback(t *testing.T) {
	value, err := parseServeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if value.ListenAddress != "127.0.0.1:8080" || value.ExternalOrigin != "http://127.0.0.1:8080" {
		t.Fatalf("parseServeConfig() = %+v", value)
	}
}

func TestWebListeningMessageUsesProductionServiceName(t *testing.T) {
	got := webListeningMessage("127.0.0.1:8080")
	want := "LifePower Control web administration service listening on http://127.0.0.1:8080"
	if got != want {
		t.Fatalf("web listening message = %q, want %q", got, want)
	}
	if strings.Contains(got, "development shell") {
		t.Fatalf("web listening message retains development wording: %q", got)
	}
}

func TestVersionOutputHasHumanAndMachineContracts(t *testing.T) {
	originalVersion, originalCommit, originalSourceState := version, commit, sourceState
	version, commit, sourceState = "v0.1.0-rc.1", "0123456789abcdef0123456789abcdef01234567", "clean"
	t.Cleanup(func() {
		version, commit, sourceState = originalVersion, originalCommit, originalSourceState
	})

	var human bytes.Buffer
	if err := writeVersion(&human, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lifepowerctl v0.1.0-rc.1", "commit: 0123456789abcdef0123456789abcdef01234567", "source: clean", "state schema: 1 (reads 1-1)"} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("human version output missing %q:\n%s", want, human.String())
		}
	}

	var machine bytes.Buffer
	if err := writeVersion(&machine, []string{"--machine"}); err != nil {
		t.Fatal(err)
	}
	wantMachine := "format=1\nproduct=lifepowerctl\nversion=v0.1.0-rc.1\ncommit=0123456789abcdef0123456789abcdef01234567\nsource_state=clean\n"
	if !strings.HasPrefix(machine.String(), wantMachine) {
		t.Fatalf("machine version output =\n%s", machine.String())
	}
	for _, want := range []string{"target_os=", "target_arch=", "state_schema_current=1\n", "state_schema_min_readable=1\n"} {
		if !strings.Contains(machine.String(), want) {
			t.Fatalf("machine version output missing %q:\n%s", want, machine.String())
		}
	}
	if err := writeVersion(io.Discard, []string{"--json"}); err == nil {
		t.Fatal("version accepted an unknown format")
	}
}

func TestControllerConfigurationUsesBoundedLocalSocket(t *testing.T) {
	value, err := parseControllerConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if value.ControllerSocket != "/run/lifepowerctl/controller.sock" {
		t.Fatalf("controller socket = %q", value.ControllerSocket)
	}
	if _, err := parseControllerConfig([]string{"unexpected"}); err == nil {
		t.Fatal("controller accepted an unexpected argument")
	}
}

func TestServeRejectsUnexpectedArgument(t *testing.T) {
	_, err := parseServeConfig([]string{"-listen", "127.0.0.1:8080", "unexpected"})
	if err == nil {
		t.Fatal("serve with an unexpected argument succeeded")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("error = %q, want unexpected-argument guidance", err)
	}
}

func TestServeListenOverrideUpdatesLoopbackOrigin(t *testing.T) {
	value, err := parseServeConfig([]string{"-listen", "127.0.0.1:18081"})
	if err != nil {
		t.Fatal(err)
	}
	if value.ListenAddress != "127.0.0.1:18081" || value.ExternalOrigin != "http://127.0.0.1:18081" {
		t.Fatalf("parseServeConfig() = %+v", value)
	}
}

func TestServeRejectsUnsafeNonLoopbackOverride(t *testing.T) {
	_, err := parseServeConfig([]string{"-listen", "0.0.0.0:8080"})
	if err == nil || !strings.Contains(err.Error(), "non-loopback") {
		t.Fatalf("error = %v, want non-loopback rejection", err)
	}
}

func TestParseJobRunnerConfig(t *testing.T) {
	value, err := parseJobRunnerConfig([]string{"-socket", "/tmp/lifepowerctl-test.sock", "-spool", "/tmp/lifepowerctl-spool", "-controller-uid", "998"})
	if err != nil {
		t.Fatal(err)
	}
	if value.SocketPath != "/tmp/lifepowerctl-test.sock" || value.SpoolDirectory != "/tmp/lifepowerctl-spool" || value.ControllerUID != 998 {
		t.Fatalf("parseJobRunnerConfig() = %+v", value)
	}
}

func TestParseJobRunnerConfigRejectsUnsafeSettings(t *testing.T) {
	for _, args := range [][]string{
		{"-controller-uid", "0"},
		{"-controller-uid", "998", "-socket", "runner.sock"},
		{"-controller-uid", "998", "-spool", "/tmp/../tmp/spool"},
		{"-controller-uid", "998", "unexpected"},
	} {
		if _, err := parseJobRunnerConfig(args); err == nil {
			t.Fatalf("accepted unsafe arguments: %v", args)
		}
	}
}

type testWriteCloser struct{ io.Writer }

func (testWriteCloser) Close() error { return nil }

func adminTestConfig(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	activity := filepath.Join(root, "activity")
	path := filepath.Join(root, "config.json")
	content := `{"state_directory":` + strconvQuote(state) + `,"activity_directory":` + strconvQuote(activity) + `}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, state
}

func strconvQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `\`, `\\`) + `"`
}

func TestAdminResetAuthRequiresRootAndTerminal(t *testing.T) {
	environment := adminEnvironment{euid: func() int { return 1000 }, openTTY: func() (io.WriteCloser, error) {
		t.Fatal("terminal checked before root")
		return nil, nil
	}}
	if err := admin([]string{"reset-auth"}, environment); err == nil || !strings.Contains(err.Error(), "requires root") {
		t.Fatalf("non-root error = %v", err)
	}
	environment.euid = func() int { return 0 }
	environment.openTTY = func() (io.WriteCloser, error) { return nil, errors.New("no terminal") }
	if err := admin([]string{"reset-auth"}, environment); err == nil || !strings.Contains(err.Error(), "controlling terminal") {
		t.Fatalf("no-terminal error = %v", err)
	}
}

func TestAdminResetAuthPrintsUsableOneTimeLink(t *testing.T) {
	configPath, stateDirectory := adminTestConfig(t)
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(stateDirectory, "jobs-preserved")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	previousManager, err := auth.NewManager(stateDirectory, limits.Default().Authentication)
	if err != nil {
		t.Fatal(err)
	}
	previousSetup, err := previousManager.ResetSetup()
	if err != nil {
		t.Fatal(err)
	}
	previousSession, err := previousManager.CompleteSetup(previousSetup.Token, "the previous long passphrase")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	environment := adminEnvironment{euid: func() int { return 0 }, openTTY: func() (io.WriteCloser, error) {
		return testWriteCloser{Writer: &output}, nil
	}}
	if err := admin([]string{"reset-auth", "-config", configPath}, environment); err != nil {
		t.Fatal(err)
	}
	const prefix = "http://127.0.0.1:8080/setup?token="
	index := strings.Index(output.String(), prefix)
	if index < 0 {
		t.Fatalf("output has no setup link: %q", output.String())
	}
	rawURL := strings.TrimSpace(strings.Split(output.String()[index:], "\n")[0])
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	token := parsed.Query().Get("token")
	if token == "" {
		t.Fatal("setup link has no token")
	}
	stored, err := os.ReadFile(filepath.Join(stateDirectory, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(token)) {
		t.Fatal("authentication state contains plaintext setup token")
	}
	manager, err := auth.NewManager(stateDirectory, limits.Default().Authentication)
	if err != nil {
		t.Fatal(err)
	}
	if manager.IsConfigured() {
		t.Fatal("reset preserved the previous password credential")
	}
	if _, err := manager.Authenticate(previousSession.Token); !errors.Is(err, auth.ErrSession) {
		t.Fatalf("reset preserved an old session: %v", err)
	}
	if _, err := manager.CompleteSetup(token, "a correct long passphrase"); err != nil {
		t.Fatalf("printed setup token is unusable: %v", err)
	}
	if value, err := os.ReadFile(marker); err != nil || string(value) != "unchanged" {
		t.Fatalf("unrelated state changed: %q, %v", value, err)
	}
}

func TestAdminResetAuthRefusesRunningController(t *testing.T) {
	configPath, stateDirectory := adminTestConfig(t)
	lock, err := adminlock.Acquire(stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	environment := adminEnvironment{euid: func() int { return 0 }, openTTY: func() (io.WriteCloser, error) {
		return testWriteCloser{Writer: io.Discard}, nil
	}}
	err = admin([]string{"reset-auth", "-config", configPath}, environment)
	if err == nil || !strings.Contains(err.Error(), "controller is running") {
		t.Fatalf("locked reset error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDirectory, "auth.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("locked reset created authentication state: %v", err)
	}
}

func TestAdminEnsureSetupCreatesOnceWithoutReplacingPendingToken(t *testing.T) {
	configPath, stateDirectory := adminTestConfig(t)
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	environment := adminEnvironment{euid: func() int { return 0 }, openTTY: func() (io.WriteCloser, error) {
		return testWriteCloser{Writer: &output}, nil
	}}
	if err := admin([]string{"ensure-setup", "-config", configPath}, environment); err != nil {
		t.Fatal(err)
	}
	const prefix = "http://127.0.0.1:8080/setup?token="
	index := strings.Index(output.String(), prefix)
	if index < 0 {
		t.Fatalf("output has no setup link: %q", output.String())
	}
	setupURL := strings.TrimSpace(strings.Split(output.String()[index:], "\n")[0])
	parsed, err := url.Parse(setupURL)
	if err != nil {
		t.Fatal(err)
	}
	var status bytes.Buffer
	environment.output = &status
	if err := admin([]string{"ensure-setup", "-config", configPath}, environment); err != nil || !strings.Contains(status.String(), "authentication=setup-pending\n") {
		t.Fatalf("second ensure = %q, %v", status.String(), err)
	}
	manager, err := auth.NewManager(stateDirectory, limits.Default().Authentication)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CompleteSetup(parsed.Query().Get("token"), "a correct long passphrase"); err != nil {
		t.Fatalf("first token was replaced: %v", err)
	}
	status.Reset()
	if err := admin([]string{"ensure-setup", "-config", configPath}, environment); err != nil || !strings.Contains(status.String(), "authentication=configured\n") {
		t.Fatalf("configured ensure = %q, %v", status.String(), err)
	}
}

func TestAdminStatusKeepsUnavailableEvidenceUnknown(t *testing.T) {
	configPath, stateDirectory := adminTestConfig(t)
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := admin([]string{"status", "-config", configPath}, adminEnvironment{output: &output}); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"format=1\n", "controller=unavailable\n", "runner=unavailable\n", "maintenance=unknown\n"} {
		if !strings.Contains(output.String(), required) {
			t.Fatalf("status missing %q:\n%s", required, output.String())
		}
	}
}

func TestAdminStatusReportsRunningBuildIdleRunnerAndReadOnlyBoard(t *testing.T) {
	root := t.TempDir()
	stateDirectory := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	controllerSocket := filepath.Join(root, "controller.sock")
	runnerSocket := filepath.Join(root, "runner.sock")
	configPath := filepath.Join(root, "config.json")
	content := `{"state_directory":` + strconvQuote(stateDirectory) +
		`,"activity_directory":` + strconvQuote(filepath.Join(root, "activity")) +
		`,"controller_socket":` + strconvQuote(controllerSocket) +
		`,"runner_socket":` + strconvQuote(runnerSocket) +
		`,"job_output_directory":` + strconvQuote(filepath.Join(root, "output")) + `}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := controllerstate.Open(stateDirectory, limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	controllerListener, err := controllerapi.ListenUnix(controllerSocket)
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skip("Unix sockets are unavailable in this test sandbox")
		}
		t.Fatal(err)
	}
	controllerService, err := controllerapi.NewService(state, uint32(os.Getuid()), runningBuildIdentity())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	controllerDone := make(chan error, 1)
	go func() { controllerDone <- controllerService.Serve(ctx, controllerListener) }()
	defer func() {
		cancel()
		if err := <-controllerDone; err != nil {
			t.Errorf("controller stop: %v", err)
		}
	}()

	runnerListener, err := net.ListenUnix("unix", &net.UnixAddr{Name: runnerSocket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	runnerDone := make(chan error, 1)
	go func() {
		connection, acceptErr := runnerListener.AcceptUnix()
		if acceptErr != nil {
			runnerDone <- acceptErr
			return
		}
		defer connection.Close()
		request, readErr := jobs.ReadRequest(connection, limits.Default().Jobs)
		if readErr != nil {
			runnerDone <- readErr
			return
		}
		runnerDone <- jobs.WriteResponse(connection, jobs.Response{Version: jobs.ProtocolVersion, RunID: request.RunID, State: jobs.StateLost, ErrorCode: "not_found", Message: "The run is not known to this runner."}, limits.Default().Jobs)
	}()
	defer runnerListener.Close()

	var output bytes.Buffer
	boardReads := 0
	environment := adminEnvironment{output: &output, detectBoard: func(context.Context, config.Config) (board.Identity, error) {
		boardReads++
		return board.Identity{RegisterSet: 7}, nil
	}}
	if err := admin([]string{"status", "-config", configPath}, environment); err != nil {
		t.Fatal(err)
	}
	if err := <-runnerDone; err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"controller=ready\n", "running_version=development\n", "state_schema=0\n", "state_revision=0\n", "automation=absent\n", "lifecycle=inactive\n", "jobs=idle\n", "runner=ready\n", "board=ready\n", "register_set=7\n", "maintenance=safe\n"} {
		if !strings.Contains(output.String(), required) {
			t.Fatalf("status missing %q:\n%s", required, output.String())
		}
	}
	if boardReads != 1 {
		t.Fatalf("board reads = %d", boardReads)
	}
}

func TestMaintenanceSafeRequiresDisabledIdleVerifiedState(t *testing.T) {
	inactive := schedule.Lifecycle{Phase: schedule.PhaseInactive}
	if !maintenanceSafe(controllerapi.Response{Lifecycle: &inactive}) {
		t.Fatal("inactive controller was not safe")
	}
	enabled := schedule.Configuration{Enabled: true}
	if maintenanceSafe(controllerapi.Response{Lifecycle: &inactive, Configuration: &enabled}) {
		t.Fatal("enabled automation was safe")
	}
	if maintenanceSafe(controllerapi.Response{Lifecycle: &inactive, ActiveRuns: 1}) {
		t.Fatal("active job was safe")
	}
	manual := schedule.Lifecycle{Phase: schedule.PhaseManualSafe, WakeDisarmStatus: schedule.DisarmUncertain}
	if maintenanceSafe(controllerapi.Response{Lifecycle: &manual}) {
		t.Fatal("uncertain wake disarm was safe")
	}
	manual.WakeDisarmStatus = schedule.DisarmVerified
	if !maintenanceSafe(controllerapi.Response{Lifecycle: &manual}) {
		t.Fatal("verified manual-safe state was unsafe")
	}
}
