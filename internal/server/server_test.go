package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/auth"
	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/controllerapi"
	"github.com/sinoia/lifepowerctl/internal/controllerstate"
	"github.com/sinoia/lifepowerctl/internal/hostimage"
	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/overview"
	"github.com/sinoia/lifepowerctl/internal/registeredit"
	"github.com/sinoia/lifepowerctl/internal/registers"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

const testOrigin = "http://power.test"
const testPassword = "a correct long passphrase"

type serverTestDetector struct{}

type serverTestHostStatus struct{}

func (serverTestHostStatus) Hostname() (string, error) { return "demo-pi", nil }
func (serverTestHostStatus) Uptime() (time.Duration, error) {
	return 49*time.Hour + 7*time.Minute, nil
}

type serverTestDutyCycle struct {
	response controllerapi.Response
	err      error
}

type noOccurrenceDutyCycle struct{ serverTestDutyCycle }

func (source noOccurrenceDutyCycle) PreviewReal(_ context.Context, revision uint64, _ int) (controllerapi.Response, error) {
	response := controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionPreviewReal, StateRevision: source.response.StateRevision + 1, ConfigurationRevision: revision, ErrorCode: "no_occurrence", Message: "No eligible occurrence satisfies the verified wake lead."}
	return response, &controllerapi.ControllerError{Code: response.ErrorCode, Message: response.Message}
}

type expiredConfirmationDutyCycle struct{ serverTestDutyCycle }

func (source expiredConfirmationDutyCycle) EnableReal(_ context.Context, _ string, _ uint64, revision uint64, _ schedule.Review) (controllerapi.Response, error) {
	response := controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionEnableReal, StateRevision: source.response.StateRevision, ConfigurationRevision: revision, ErrorCode: "no_occurrence", Message: "The reviewed occurrence is now too close or no longer eligible."}
	return response, &controllerapi.ControllerError{Code: response.ErrorCode, Message: response.Message}
}

func (source serverTestDutyCycle) Status(context.Context) (controllerapi.Response, error) {
	return source.response, source.err
}
func (source serverTestDutyCycle) SaveDisabled(_ context.Context, _ string, _ uint64, configuration schedule.Configuration) (controllerapi.Response, error) {
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionSaveDisabled, StateRevision: source.response.StateRevision + 1, ConfigurationRevision: configuration.Revision}, source.err
}
func (source serverTestDutyCycle) ProposalStatus(context.Context, string) (controllerapi.Response, error) {
	return source.response, source.err
}
func (source serverTestDutyCycle) Preview(_ context.Context, revision uint64, _ int) (controllerapi.Response, error) {
	review := schedule.Review{ConfigurationRevision: revision, Reference: 1_800_000_000, MinimumLeadSeconds: 60, Identity: strings.Repeat("a", 64), RunListJobs: 1, MaximumRunSeconds: 65, Occurrences: []schedule.Occurrence{{Unix: 1_800_003_600, Local: "2027-01-15 09:00:00", UTCOffset: "+00:00", Timezone: "UTC"}}}
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionPreview, StateRevision: source.response.StateRevision + 1, ConfigurationRevision: revision, Review: &review}, source.err
}
func (source serverTestDutyCycle) EnableSimulation(_ context.Context, _ string, _ uint64, revision uint64, _ schedule.Review) (controllerapi.Response, error) {
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionEnableSimulation, StateRevision: source.response.StateRevision + 2, ConfigurationRevision: revision}, source.err
}
func (source serverTestDutyCycle) PreviewReal(_ context.Context, revision uint64, _ int) (controllerapi.Response, error) {
	review := schedule.Review{ConfigurationRevision: revision, Reference: 1_800_000_000, MinimumLeadSeconds: uint32(schedule.VerifiedWakeMinimumLead.Seconds()), Identity: strings.Repeat("b", 64), RunListJobs: 1, MaximumRunSeconds: 65, Occurrences: []schedule.Occurrence{{Unix: 1_800_003_600, Local: "2027-01-15 09:00:00", UTCOffset: "+00:00", Timezone: "UTC"}}}
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionPreviewReal, StateRevision: source.response.StateRevision + 1, ConfigurationRevision: revision, Review: &review}, source.err
}
func (source serverTestDutyCycle) EnableReal(_ context.Context, _ string, _ uint64, revision uint64, _ schedule.Review) (controllerapi.Response, error) {
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionEnableReal, StateRevision: source.response.StateRevision + 2, ConfigurationRevision: revision}, source.err
}
func (source serverTestDutyCycle) Disable(_ context.Context, _ string, _ uint64, revision uint64) (controllerapi.Response, error) {
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionDisable, StateRevision: source.response.StateRevision + 1, ConfigurationRevision: revision}, source.err
}
func (source serverTestDutyCycle) SimulateExpectedBoot(_ context.Context, _ uint64, revision uint64) (controllerapi.Response, error) {
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionSimulateBoot, StateRevision: source.response.StateRevision + 1, ConfigurationRevision: revision}, source.err
}

func (serverTestDetector) Detect(context.Context) (board.Identity, error) {
	return board.Identity{RegisterSet: 7}, nil
}

type serverTestReader struct {
	catalogue board.Catalog
}

type serverTestJobRunner struct {
	requests []jobs.Request
	response func(jobs.Request) (jobs.Response, error)
	output   *jobs.OutputStore
}

func (runner *serverTestJobRunner) Do(_ context.Context, request jobs.Request) (jobs.Response, error) {
	runner.requests = append(runner.requests, request)
	if runner.response != nil {
		return runner.response(request)
	}
	if request.Action == jobs.ActionStart && runner.output != nil {
		_ = runner.output.Save(request.RunID, []byte("test stdout\n"), []byte("test stderr\n"))
	}
	state := jobs.StateQueued
	result := jobs.Response{Version: jobs.ProtocolVersion, RunID: request.RunID, State: state}
	if request.Action == jobs.ActionStatus {
		result.State = jobs.StateSucceeded
		result.QueuedUnixMilli, result.StartedUnixMilli, result.FinishedUnixMilli = 1, 2, 3
		result.StdoutTotal, result.StderrTotal = 12, 12
	} else if request.Action == jobs.ActionCancel {
		result.State = jobs.StateCancelled
	}
	return result, nil
}

func (reader serverTestReader) Read(_ context.Context, registerSet int, register board.Register) (board.Reading, error) {
	definition, _ := reader.catalogue.Find(registerSet, register)
	raw := int64(1)
	switch register {
	case "SHDN_DELAY":
		raw = 40
	case "VBAT_MIN":
		raw = 6875
	case "VBAT_SHDN":
		raw = 7366
	case "VBAT_BOOT":
		raw = 7857
	}
	value, _ := definition.EngineeringValue(raw)
	return board.Reading{Definition: definition, EngineeringValue: value, RawValue: &raw}, nil
}

func serverTestServices(t *testing.T, audit *activity.Audit) (*overview.Service, *registers.Service, *registeredit.Service, *registeredit.BatchService) {
	t.Helper()
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	coordinator := board.NewCoordinator()
	detector := serverTestDetector{}
	fake := board.NewFake(board.Identity{RegisterSet: 7}, map[board.Register]int64{"LED_STATE": 1, "AUTO_BOOT": 1, "SHDN_DELAY": 40, "VBAT_MIN": 4200, "VBAT_SHDN": 4500, "VBAT_BOOT": 4800})
	editor, err := registeredit.NewService(fake, catalogue, coordinator, audit)
	if err != nil {
		t.Fatal(err)
	}
	batchEditor, err := registeredit.NewBatchService(fake, catalogue, coordinator, audit)
	if err != nil {
		t.Fatal(err)
	}
	return overview.New(detector, coordinator, limits.Default().Polling), registers.New(detector, serverTestReader{catalogue: catalogue}, catalogue, coordinator, limits.Default().Polling), editor, batchEditor
}

func newTestServer(t *testing.T, configured bool) (http.Handler, *auth.Manager, auth.Session, auth.Setup) {
	lifecycle := schedule.Lifecycle{Phase: schedule.PhaseInactive}
	duty := serverTestDutyCycle{response: controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionStatus, StateRevision: 1, Lifecycle: &lifecycle}}
	return newTestServerWithDuty(t, configured, duty)
}

func newTestServerWithDuty(t *testing.T, configured bool, duty DutyCycleController) (http.Handler, *auth.Manager, auth.Session, auth.Setup) {
	t.Helper()
	manager, err := auth.NewManager(t.TempDir(), limits.Default().Authentication)
	if err != nil {
		t.Fatal(err)
	}
	setup, err := manager.ResetSetup()
	if err != nil {
		t.Fatal(err)
	}
	var session auth.Session
	if configured {
		session, err = manager.CompleteSetup(setup.Token, testPassword)
		if err != nil {
			t.Fatal(err)
		}
	}
	journal, err := activity.Open(t.TempDir(), activity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Append(activity.Event{Time: time.Date(2026, time.October, 10, 12, 0, 0, 0, time.UTC), Type: string(activity.BoardObserved), Actor: activity.ActorSystem, Subject: "board", Details: json.RawMessage(`{"state":"supported","register_set":7}`)}); err != nil {
		t.Fatal(err)
	}
	audit := activity.NewAudit(journal)
	overviewService, registerService, registerEditor, batchRegisterEditor := serverTestServices(t, audit)
	jobManager, err := controllerstate.Open(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	jobOutput, err := jobs.NewOutputStore(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	hostImageStore, err := hostimage.New(t.TempDir(), limits.Default().HostImage)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Options{
		Build: BuildInfo{Version: "v0.1.0-preview", Commit: "0000000"}, HostStatus: serverTestHostStatus{}, Authentication: manager,
		Overview: overviewService, Registers: registerService, RegisterEditor: registerEditor, BatchRegisterEditor: batchRegisterEditor, Activity: journal, Audit: audit,
		ExternalOrigin: testOrigin, MaxBodyBytes: limits.Default().HTTP.MaxBodyBytes, DiagnosticMaxBytes: limits.Default().Persistence.MaxSnapshotBytes,
		Jobs: jobManager, DutyCycle: duty, JobLimits: limits.Default().Jobs, JobIdentity: jobs.ExecutionIdentity{User: "pi", UID: 1000, Home: "/home/pi", Path: jobs.DefaultPath}, JobRunner: &serverTestJobRunner{output: jobOutput}, JobOutput: jobOutput, HostImage: hostImageStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, manager, session, setup
}

func request(handler http.Handler, method, path string, body io.Reader, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, testOrigin+path, body)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func cookieNamed(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response has no %s cookie", name)
	return nil
}

func TestPublicAndAuthenticatedRoutes(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)

	tests := []struct {
		path     string
		cookie   *http.Cookie
		status   int
		location string
		contains string
	}{
		{path: "/healthz", status: http.StatusOK, contains: "ok"},
		{path: "/static/theme.js", status: http.StatusOK, contains: "lifepowerctl-theme"},
		{path: "/", status: http.StatusSeeOther, location: "/login"},
		{path: "/", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "Register set 7 is supported"},
		{path: "/registers", status: http.StatusSeeOther, location: "/login"},
		{path: "/registers", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "PI_BOOT_TO"},
		{path: "/registers/battery-thresholds/edit", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "raw bytes are not recoverable"},
		{path: "/registers/PI_BOOT_TO", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "Product Brief, revision 13, page 30"},
		{path: "/registers/SHDN_DELAY", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "40 ticks"},
		{path: "/registers/NOT_A_REGISTER", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusNotFound, contains: "not in the reviewed catalogue"},
		{path: "/activity", status: http.StatusSeeOther, location: "/login"},
		{path: "/activity", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "Board observed"},
		{path: "/activity?type=board.observed&actor=system", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "board.observed"},
		{path: "/activity?type=invented", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusBadRequest, contains: "Choose an event type"},
		{path: "/activity/1", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "register_set"},
		{path: "/activity/999", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusNotFound, contains: "does not exist or has expired"},
		{path: "/diagnostics/report.json", status: http.StatusSeeOther, location: "/login"},
		{path: "/diagnostics/report.json", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: `"schema_version": 1`},
		{path: "/duty-cycle", status: http.StatusSeeOther, location: "/login"},
		{path: "/duty-cycle", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "No automatic or simulated duty-cycle transition is active"},
		{path: "/duty-cycle/edit", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "Save and review wake and shutdown"},
		{path: "/jobs", status: http.StatusSeeOther, location: "/login"},
		{path: "/jobs", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "No saved jobs yet"},
		{path: "/jobs/new", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "/bin/bash -lc"},
		{path: "/jobs/run", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: "Run once without saving"},
		{path: "/readyz", status: http.StatusSeeOther, location: "/login"},
		{path: "/readyz", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusOK, contains: `"ready":true`},
		{path: "/missing", cookie: &http.Cookie{Name: sessionCookie, Value: session.Token}, status: http.StatusNotFound, contains: "Page not found"},
	}
	for _, test := range tests {
		t.Run(test.path+test.location, func(t *testing.T) {
			var cookies []*http.Cookie
			if test.cookie != nil {
				cookies = append(cookies, test.cookie)
			}
			response := request(handler, http.MethodGet, test.path, nil, cookies...)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if response.Header().Get("Location") != test.location {
				t.Fatalf("location = %q, want %q", response.Header().Get("Location"), test.location)
			}
			if !strings.Contains(response.Body.String(), test.contains) {
				t.Fatalf("body does not contain %q", test.contains)
			}
			if response.Header().Get("Content-Security-Policy") == "" || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("security headers are missing")
			}
		})
	}
}

func TestOverviewDescribesCurrentGuardedCapabilities(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	page := request(handler, http.MethodGet, "/", nil, &http.Cookie{Name: sessionCookie, Value: session.Token})
	for _, want := range []string{"Reviewed temporary changes", "Jobs and schedules", "Verified duty cycle only", "Flash commits", "Unavailable"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("overview missing %q: %s", want, page.Body.String())
		}
	}
	if strings.Contains(page.Body.String(), "All mutations remain outside the read-only product boundary") {
		t.Fatalf("overview retains obsolete capability copy: %s", page.Body.String())
	}
}

func TestHostIdentityAppearsThroughoutSiteAndOverviewShowsUptime(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	for _, path := range []string{"/", "/registers", "/duty-cycle", "/jobs", "/activity", "/missing"} {
		page := request(handler, http.MethodGet, path, nil, cookie)
		for _, want := range []string{"— LifePower Pi Control</title>", "LifePower Pi Control", "demo-pi"} {
			if !strings.Contains(page.Body.String(), want) {
				t.Fatalf("%s missing %q: %s", path, want, page.Body.String())
			}
		}
	}
	login := request(handler, http.MethodGet, "/login", nil)
	for _, want := range []string{"— LifePower Pi Control</title>", "demo-pi"} {
		if !strings.Contains(login.Body.String(), want) {
			t.Fatalf("login missing %q: %s", want, login.Body.String())
		}
	}
	overviewPage := request(handler, http.MethodGet, "/", nil, cookie)
	for _, want := range []string{"Host system", "2 days 1 hour 7 minutes"} {
		if !strings.Contains(overviewPage.Body.String(), want) {
			t.Fatalf("overview missing %q: %s", want, overviewPage.Body.String())
		}
	}
}

func TestHostImageUploadDisplayAndRemoval(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	initial := request(handler, http.MethodGet, "/", nil, cookie)
	for _, want := range []string{`action="/host-image"`, `enctype="multipart/form-data"`, `accept="image/jpeg,image/png"`, `Upload image`} {
		if initial.Code != http.StatusOK || !strings.Contains(initial.Body.String(), want) {
			t.Fatalf("initial overview missing %q: %d: %s", want, initial.Code, initial.Body.String())
		}
	}
	if strings.Contains(initial.Body.String(), `class="host-image"`) || strings.Contains(initial.Body.String(), `action="/host-image/remove"`) {
		t.Fatalf("initial overview presents a stored image: %s", initial.Body.String())
	}

	encoded := serverTestPNG(t, 3, 2)
	upload := postMultipartImage(handler, session.CSRFToken, encoded, cookie)
	if upload.Code != http.StatusSeeOther || upload.Header().Get("Location") != "/" {
		t.Fatalf("upload response = %d, %q: %s", upload.Code, upload.Header().Get("Location"), upload.Body.String())
	}
	overviewPage := request(handler, http.MethodGet, "/", nil, cookie)
	for _, want := range []string{`class="host-image" src="/host-image" alt="Host demo-pi"`, `Replace image`, `action="/host-image/remove"`, `Remove image`} {
		if !strings.Contains(overviewPage.Body.String(), want) {
			t.Fatalf("overview missing %q after upload: %s", want, overviewPage.Body.String())
		}
	}
	imageResponse := request(handler, http.MethodGet, "/host-image", nil, cookie)
	if imageResponse.Code != http.StatusOK || imageResponse.Header().Get("Content-Type") != "image/png" || !bytes.Equal(imageResponse.Body.Bytes(), encoded) {
		t.Fatalf("image response = %d, %q, %d bytes", imageResponse.Code, imageResponse.Header().Get("Content-Type"), imageResponse.Body.Len())
	}
	removed := postForm(handler, "/host-image/remove", url.Values{"csrf": {session.CSRFToken}}, cookie)
	if removed.Code != http.StatusSeeOther || removed.Header().Get("Location") != "/" {
		t.Fatalf("remove response = %d, %q: %s", removed.Code, removed.Header().Get("Location"), removed.Body.String())
	}
	missing := request(handler, http.MethodGet, "/host-image", nil, cookie)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("removed image response = %d: %s", missing.Code, missing.Body.String())
	}
}

func TestHostImageUploadRequiresValidImageAndCSRF(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	invalid := postMultipartImage(handler, session.CSRFToken, []byte("not an image"), cookie)
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid image response = %d: %s", invalid.Code, invalid.Body.String())
	}
	badCSRF := postMultipartImage(handler, "wrong", serverTestPNG(t, 1, 1), cookie)
	if badCSRF.Code != http.StatusForbidden {
		t.Fatalf("bad-CSRF image response = %d: %s", badCSRF.Code, badCSRF.Body.String())
	}
	unauthenticated := postMultipartImage(handler, session.CSRFToken, serverTestPNG(t, 1, 1))
	if unauthenticated.Code != http.StatusSeeOther || unauthenticated.Header().Get("Location") != "/login" {
		t.Fatalf("unauthenticated image response = %d, %q", unauthenticated.Code, unauthenticated.Header().Get("Location"))
	}
}

func TestFormatUptime(t *testing.T) {
	tests := map[time.Duration]string{
		0:                                        "Less than 1 minute",
		time.Minute:                              "1 minute",
		time.Hour + 2*time.Minute:                "1 hour 2 minutes",
		48*time.Hour + time.Hour + 7*time.Minute: "2 days 1 hour 7 minutes",
		-time.Second:                             "Unavailable",
	}
	for input, want := range tests {
		if got := formatUptime(input); got != want {
			t.Errorf("formatUptime(%v) = %q, want %q", input, got, want)
		}
	}
}

func TestActionRowsUseConsistentVisualHierarchy(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	tests := []struct {
		path string
		want []string
	}{
		{path: "/registers", want: []string{`class="button-primary" href="/registers/battery-thresholds/edit"`}},
		{path: "/duty-cycle/edit", want: []string{`class="button-secondary" href="/duty-cycle">Cancel`}},
		{path: "/jobs", want: []string{`class="button-primary" href="/jobs/run"`, `class="button-secondary" href="/jobs/new"`}},
		{path: "/activity", want: []string{`class="button-secondary" href="/activity">Clear filters`}},
	}
	for _, test := range tests {
		page := request(handler, http.MethodGet, test.path, nil, cookie)
		for _, want := range test.want {
			if !strings.Contains(page.Body.String(), want) {
				t.Fatalf("%s missing %q: %s", test.path, want, page.Body.String())
			}
		}
		if strings.Contains(page.Body.String(), "button-link") {
			t.Fatalf("%s retains obsolete button-link class", test.path)
		}
	}
	busy := request(handler, http.MethodGet, "/static/busy.js", nil)
	if !strings.Contains(busy.Body.String(), "button.replaceChildren(labelNode)") {
		t.Fatalf("busy enhancement does not support plain-text buttons: %s", busy.Body.String())
	}
}

func TestDutyCyclePresentationKeepsSafeModeExplicit(t *testing.T) {
	lifecycle := schedule.Lifecycle{Phase: schedule.PhaseManualSafe, Reason: "startup uncertainty"}
	if got := lifecycleTone(lifecycle.Phase); got != "error" {
		t.Fatalf("tone = %q", got)
	}
	for _, want := range []string{"Automation is disabled", "power actions are inhibited"} {
		if got := lifecycleSummary(lifecycle, schedule.KindDisabled); !strings.Contains(got, want) {
			t.Fatalf("summary %q does not contain %q", got, want)
		}
	}
}

func TestDutyCycleFormBuildsTypedConfiguration(t *testing.T) {
	values := url.Values{
		"kind":          {"selected-weekdays"},
		"timezone":      {"Europe/London"},
		"local_time":    {"06:45"},
		"weekdays":      {"monday", "friday"},
		"quiet_enabled": {"true"},
		"quiet_start":   {"22:00"},
		"quiet_end":     {"05:30"},
		"run_list":      {"00112233445566778899aabbccddeeff:2", "ffeeddccbbaa99887766554433221100:7"},
	}
	configuration, err := configurationFromValues(4, values)
	if err != nil {
		t.Fatal(err)
	}
	if err := configuration.Validate(); err != nil {
		t.Fatal(err)
	}
	if configuration.Enabled || configuration.Revision != 4 || configuration.Schedule.LocalMinute != 6*60+45 || len(configuration.Schedule.Weekdays) != 2 || configuration.Schedule.QuietPeriod.StartMinute != 22*60 || len(configuration.RunList) != 2 || configuration.RunList[1].JobRevision != 7 {
		t.Fatalf("configuration = %+v", configuration)
	}
}

func TestDutyCycleFormRejectsInvalidTimingAndJobReference(t *testing.T) {
	for _, values := range []url.Values{
		{"kind": {"daily"}, "timezone": {"UTC"}, "local_time": {"25:00"}, "quiet_enabled": {"false"}},
		{"kind": {"hourly"}, "timezone": {"UTC"}, "minute": {"15"}, "quiet_enabled": {"false"}, "run_list": {"job-without-revision"}},
	} {
		if _, err := configurationFromValues(1, values); err == nil {
			t.Fatalf("configurationFromValues(%v) succeeded", values)
		}
	}
}

func TestDutyCycleFormExplainsInvalidTimezoneAndMissingOneTimeDate(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	values := url.Values{
		"csrf": {session.CSRFToken}, "base_revision": {"1"}, "configuration_revision": {"1"},
		"kind": {"daily"}, "timezone": {"Europe/Londn"}, "minute": {"0"}, "local_time": {"09:00"},
		"delay_minutes": {"60"}, "one_time_date": {""}, "quiet_enabled": {"false"}, "quiet_start": {"22:00"}, "quiet_end": {"06:00"},
	}
	response := postForm(handler, "/duty-cycle/review", values, cookie)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `id="cycle-timezone-error"`) || !strings.Contains(response.Body.String(), `Timezone &#34;Europe/Londn&#34; is not available`) || !strings.Contains(response.Body.String(), `value="Europe/Londn"`) {
		t.Fatalf("invalid timezone response = %d: %s", response.Code, response.Body.String())
	}
	values.Set("kind", "one-time")
	values.Set("timezone", "UTC")
	response = postForm(handler, "/duty-cycle/review", values, cookie)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `id="cycle-date-error"`) || !strings.Contains(response.Body.String(), "Choose a valid date for the one-time schedule") {
		t.Fatalf("missing date response = %d: %s", response.Code, response.Body.String())
	}
}

func TestDutyCycleEditorHasOnlyRecurrenceChoicesAndNoInactiveDelayConstraint(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	page := request(handler, http.MethodGet, "/duty-cycle/edit", nil, &http.Cookie{Name: sessionCookie, Value: session.Token})
	body := page.Body.String()
	if page.Code != http.StatusOK || strings.Contains(body, `<option value="disabled"`) || strings.Contains(body, `id="cycle-delay" name="delay_minutes" type="number" min="1"`) || !strings.Contains(body, "Edit Duty Cycle") {
		t.Fatalf("editor response = %d: %s", page.Code, body)
	}
}

func TestDutyCycleEditorMarksRelevantScheduleFieldsAndExplainsCompletion(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	page := request(handler, http.MethodGet, "/duty-cycle/edit", nil, &http.Cookie{Name: sessionCookie, Value: session.Token})
	body := page.Body.String()
	for _, want := range []string{
		`data-schedule-fields="hourly"`,
		`data-schedule-fields="daily selected-weekdays one-time"`,
		`data-schedule-fields="after-completion"`,
		`data-schedule-fields="selected-weekdays"`,
		`data-quiet-fields`,
		`/static/duty-cycle.js?v=4`,
		"Delay after successful run list (minutes)",
		"after every enabled job in the ordered run list succeeds",
		"failed or timed-out run list does not schedule another occurrence",
		"The server uses and validates only the fields required by the selected schedule type",
	} {
		if page.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("editor missing %q: %d: %s", want, page.Code, body)
		}
	}
	asset := request(handler, http.MethodGet, "/static/duty-cycle.js", nil)
	for _, want := range []string{`#cycle-kind`, `[data-schedule-fields]`, `kind.addEventListener("change"`, `quiet.addEventListener("change"`} {
		if !strings.Contains(asset.Body.String(), want) {
			t.Fatalf("schedule enhancement missing %q: %s", want, asset.Body.String())
		}
	}
	styles := request(handler, http.MethodGet, "/static/app.css", nil)
	if !strings.Contains(styles.Body.String(), `[hidden] { display: none !important; }`) {
		t.Fatalf("stylesheet does not preserve the hidden attribute: %s", styles.Body.String())
	}
	if !strings.Contains(body, `/static/app.css?v=4`) {
		t.Fatalf("editor does not use the corrected stylesheet version: %s", body)
	}
}

func TestDutyCycleLifecycleSummaryDistinguishesHardwareAndSimulation(t *testing.T) {
	hardware := schedule.Lifecycle{Phase: schedule.PhaseRunningJob, Simulated: false}
	simulation := schedule.Lifecycle{Phase: schedule.PhaseRunningJob, Simulated: true}
	if got := lifecycleSummary(hardware, schedule.KindHourly); !strings.Contains(got, "A duty cycle is in progress") || strings.Contains(got, "simulation") {
		t.Fatalf("hardware summary = %q", got)
	}
	if got := lifecycleSummary(simulation, schedule.KindHourly); !strings.Contains(got, "simulation-only") {
		t.Fatalf("simulation summary = %q", got)
	}
	if canDisableLifecycle(schedule.PhaseRunningJob) || !canDisableLifecycle(schedule.PhaseShutdownRequested) {
		t.Fatal("disable availability does not match controller lifecycle boundary")
	}
}

func TestSharedShellSupportsKeyboardNarrowAndReducedMotionUse(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	for _, page := range []*httptest.ResponseRecorder{
		request(handler, http.MethodGet, "/login", nil),
		request(handler, http.MethodGet, "/", nil, cookie),
	} {
		body := page.Body.String()
		for _, want := range []string{`class="skip-link" href="#main-content"`, `id="main-content"`} {
			if page.Code != http.StatusOK || !strings.Contains(body, want) {
				t.Fatalf("shared shell missing %q: %d: %s", want, page.Code, body)
			}
		}
	}
	login := request(handler, http.MethodGet, "/login", nil)
	if strings.Contains(login.Body.String(), "autofocus") {
		t.Fatalf("login steals initial keyboard focus: %s", login.Body.String())
	}
	styles := request(handler, http.MethodGet, "/static/app.css", nil).Body.String()
	for _, want := range []string{".skip-link:focus", ":focus-visible", "@media (max-width: 760px)", "@media (prefers-reduced-motion: reduce)"} {
		if !strings.Contains(styles, want) {
			t.Fatalf("shared stylesheet missing %q", want)
		}
	}
}

func TestDutyCycleLifecycleSummaryDoesNotPromiseAnotherOneTimeWake(t *testing.T) {
	for _, phase := range []schedule.Phase{schedule.PhaseExpectedBootObserved, schedule.PhasePreparingRun, schedule.PhaseRunningJob} {
		got := lifecycleSummary(schedule.Lifecycle{Phase: phase}, schedule.KindOneTime)
		if !strings.Contains(got, "one-time duty cycle") || !strings.Contains(got, "turn off") || strings.Contains(got, "next verified board wake") {
			t.Fatalf("phase %q summary = %q", phase, got)
		}
	}
	got := lifecycleSummary(schedule.Lifecycle{Phase: schedule.PhaseRunListSucceeded}, schedule.KindOneTime)
	if !strings.Contains(got, "completing the one-time duty cycle") || strings.Contains(got, "next board wake") {
		t.Fatalf("run-list-succeeded summary = %q", got)
	}
}

func TestDutyCycleRunningHardwarePageUsesConcreteLanguageAndHidesRejectedActions(t *testing.T) {
	configuration := schedule.Configuration{Revision: 3, Enabled: true, Schedule: schedule.Specification{Kind: schedule.KindOneTime, Timezone: "UTC", LocalMinute: 9 * 60, OneTimeDate: "2026-10-12"}, RunList: []schedule.RunListEntry{{JobID: "job", JobRevision: 1}}}
	lifecycle := schedule.Lifecycle{Phase: schedule.PhaseRunningJob, ConfigurationRevision: 3, IntendedOccurrence: 1_797_000_000, Simulated: false, RunList: []schedule.RunEntry{{JobID: "job", JobRevision: 1, RunID: "00112233445566778899aabbccddeeff", State: schedule.RunRunning}}}
	duty := serverTestDutyCycle{response: controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionStatus, StateRevision: 9, ConfigurationRevision: 3, Configuration: &configuration, Lifecycle: &lifecycle}}
	handler, _, session, _ := newTestServerWithDuty(t, true, duty)
	page := request(handler, http.MethodGet, "/duty-cycle", nil, &http.Cookie{Name: sessionCookie, Value: session.Token})
	body := page.Body.String()
	for _, want := range []string{"Power control enabled", "A one-time duty cycle is in progress", "Automatic operation will turn off", "<dt>Enabled</dt><dd>Yes</dd>", "cannot be disabled or edited"} {
		if !strings.Contains(body, want) {
			t.Fatalf("running page missing %q: %s", want, body)
		}
	}
	for _, unwanted := range []string{"Real power control enabled", "simulated duty-cycle run", "Disable duty cycle", "Edit configuration"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("running page contains %q: %s", unwanted, body)
		}
	}
}

func TestDutyCycleRunRowsDistinguishInactiveFromArmedWaiting(t *testing.T) {
	configuration := schedule.Configuration{Revision: 3, Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 0}, RunList: []schedule.RunListEntry{{JobID: "job", JobRevision: 1}}}
	for _, test := range []struct {
		name     string
		enabled  bool
		phase    schedule.Phase
		want     string
		unwanted string
	}{
		{name: "inactive", phase: schedule.PhaseInactive, want: "Not started", unwanted: ">Waiting<"},
		{name: "armed", enabled: true, phase: schedule.PhaseWakeVerified, want: "Waiting", unwanted: "Not started"},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := configuration
			current.Enabled = test.enabled
			lifecycle := schedule.Lifecycle{Phase: test.phase, ConfigurationRevision: 3, IntendedOccurrence: 1_800_000_000}
			duty := serverTestDutyCycle{response: controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionStatus, StateRevision: 9, ConfigurationRevision: 3, Configuration: &current, Lifecycle: &lifecycle}}
			handler, _, session, _ := newTestServerWithDuty(t, true, duty)
			body := request(handler, http.MethodGet, "/duty-cycle", nil, &http.Cookie{Name: sessionCookie, Value: session.Token}).Body.String()
			if !strings.Contains(body, test.want) || strings.Contains(body, test.unwanted) {
				t.Fatalf("page does not distinguish row state: %s", body)
			}
		})
	}
}

func TestDutyCycleReviewSavesDisabledConfigurationBeforePreview(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	values := url.Values{
		"csrf":                   {session.CSRFToken},
		"base_revision":          {"1"},
		"configuration_revision": {"1"},
		"kind":                   {"daily"},
		"timezone":               {"UTC"},
		"minute":                 {"0"},
		"local_time":             {"09:00"},
		"delay_minutes":          {"60"},
		"one_time_date":          {""},
		"quiet_enabled":          {"false"},
		"quiet_start":            {"22:00"},
		"quiet_end":              {"06:00"},
	}
	response := postForm(handler, "/duty-cycle/review", values, &http.Cookie{Name: sessionCookie, Value: session.Token})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	for _, want := range []string{"Review Duty Cycle", "Board-clock calculation", "2027-01-15 09:00:00", "Confirm wake and shutdown", "Maximum sequential runtime", "1m5s", "5m0s"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("review missing %q: %s", want, response.Body.String())
		}
	}
}

func TestDutyCycleEnableRequiresControllerReviewEvidence(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	review := schedule.Review{ConfigurationRevision: 1, Reference: 1_800_000_000, MinimumLeadSeconds: 60, Identity: strings.Repeat("a", 64), Occurrences: []schedule.Occurrence{{Unix: 1_800_003_600, Local: "2027-01-15 09:00:00", UTCOffset: "+00:00", Timezone: "UTC"}}}
	token, err := encodeReview(review)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReview(token)
	if err != nil || decoded.Identity != review.Identity || decoded.Occurrences[0].Unix != review.Occurrences[0].Unix {
		t.Fatalf("decoded review = %+v, %v", decoded, err)
	}
	values := url.Values{"csrf": {session.CSRFToken}, "state_revision": {"2"}, "configuration_revision": {"1"}, "review": {token}}
	response := postForm(handler, "/duty-cycle/enable", values, &http.Cookie{Name: sessionCookie, Value: session.Token})
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/duty-cycle" {
		t.Fatalf("enable response = %d, %q: %s", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	values.Set("review", token+"tampered")
	response = postForm(handler, "/duty-cycle/enable", values, &http.Cookie{Name: sessionCookie, Value: session.Token})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("tampered review status = %d: %s", response.Code, response.Body.String())
	}
}

func TestDutyCycleRealActivationRequiresSeparateBoardClockReview(t *testing.T) {
	configuration := schedule.Configuration{Revision: 1, Schedule: schedule.Specification{Kind: schedule.KindDaily, Timezone: "UTC", LocalMinute: 540}}
	lifecycle := schedule.Lifecycle{Phase: schedule.PhaseInactive}
	duty := serverTestDutyCycle{response: controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionStatus, StateRevision: 2, ConfigurationRevision: 1, Configuration: &configuration, Lifecycle: &lifecycle}}
	handler, _, session, _ := newTestServerWithDuty(t, true, duty)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	values := url.Values{"csrf": {session.CSRFToken}, "state_revision": {"2"}, "configuration_revision": {"1"}}
	reviewResponse := postForm(handler, "/duty-cycle/review-real", values, cookie)
	if reviewResponse.Code != http.StatusOK {
		t.Fatalf("real review status = %d: %s", reviewResponse.Code, reviewResponse.Body.String())
	}
	for _, want := range []string{"Power action", "Confirm wake and shutdown", "5m0s"} {
		if !strings.Contains(reviewResponse.Body.String(), want) {
			t.Fatalf("real review missing %q: %s", want, reviewResponse.Body.String())
		}
	}
	review := schedule.Review{ConfigurationRevision: 1, Reference: 1_800_000_000, MinimumLeadSeconds: uint32(schedule.VerifiedWakeMinimumLead.Seconds()), Identity: strings.Repeat("b", 64), Occurrences: []schedule.Occurrence{{Unix: 1_800_003_600, Local: "2027-01-15 09:00:00", UTCOffset: "+00:00", Timezone: "UTC"}}}
	token, err := encodeReview(review)
	if err != nil {
		t.Fatal(err)
	}
	values.Set("review", token)
	enableResponse := postForm(handler, "/duty-cycle/enable-real", values, cookie)
	if enableResponse.Code != http.StatusAccepted {
		t.Fatalf("real enable response = %d: %s", enableResponse.Code, enableResponse.Body.String())
	}
	for _, want := range []string{"Shutdown accepted", "Powering off in approximately", "2027-01-15 09:00:00 UTC", "Wake verification", "Passed"} {
		if !strings.Contains(enableResponse.Body.String(), want) {
			t.Fatalf("shutdown acceptance missing %q: %s", want, enableResponse.Body.String())
		}
	}
	badReview := review
	badReview.MinimumLeadSeconds = 60
	badToken, err := encodeReview(badReview)
	if err != nil {
		t.Fatal(err)
	}
	values.Set("review", badToken)
	if response := postForm(handler, "/duty-cycle/enable-real", values, cookie); response.Code != http.StatusBadRequest {
		t.Fatalf("unsafe real lead status = %d: %s", response.Code, response.Body.String())
	}
}

func TestDutyCycleOwnerReviewHasOneWakeAndShutdownPath(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	values := url.Values{
		"csrf":                   {session.CSRFToken},
		"base_revision":          {"1"},
		"configuration_revision": {"1"},
		"kind":                   {"daily"},
		"timezone":               {"UTC"},
		"minute":                 {"0"},
		"local_time":             {"09:00"},
		"delay_minutes":          {"60"},
		"one_time_date":          {""},
		"quiet_enabled":          {"false"},
		"quiet_start":            {"22:00"},
		"quiet_end":              {"06:00"},
	}
	response := postForm(handler, "/duty-cycle/review", values, &http.Cookie{Name: sessionCookie, Value: session.Token})
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "Confirm wake and shutdown") {
		t.Fatalf("review response = %d: %s", response.Code, body)
	}
	for _, unwanted := range []string{"Enable simulated duty cycle", "/duty-cycle/enable\"", "/duty-cycle/review-real\"", "Review wake and shutdown", "Review real wake and shutdown", "Simulation records only"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("owner review contains %q: %s", unwanted, body)
		}
	}
}

func TestDutyCycleTooSoonOneTimeReviewReturnsPopulatedEditor(t *testing.T) {
	lifecycle := schedule.Lifecycle{Phase: schedule.PhaseInactive}
	duty := noOccurrenceDutyCycle{serverTestDutyCycle{response: controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionStatus, StateRevision: 7, Lifecycle: &lifecycle}}}
	handler, _, session, _ := newTestServerWithDuty(t, true, duty)
	values := url.Values{
		"csrf": {session.CSRFToken}, "base_revision": {"7"}, "configuration_revision": {"1"},
		"kind": {"one-time"}, "timezone": {"Europe/London"}, "minute": {"0"}, "local_time": {"18:30"},
		"delay_minutes": {"60"}, "one_time_date": {"2026-10-12"}, "quiet_enabled": {"false"},
		"quiet_start": {"22:00"}, "quiet_end": {"06:00"},
	}
	response := postForm(handler, "/duty-cycle/review", values, &http.Cookie{Name: sessionCookie, Value: session.Token})
	body := response.Body.String()
	for _, want := range []string{"The configuration was saved", "no occurrence meets the 5-minute verified-wake lead", "at least 5 minutes in the future", `value="18:30"`, `value="2026-10-12"`, "Save and review wake and shutdown"} {
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(body, want) {
			t.Fatalf("too-soon review missing %q: %d: %s", want, response.Code, body)
		}
	}
	if strings.Contains(body, "Confirm wake and shutdown") {
		t.Fatalf("too-soon review offered confirmation: %s", body)
	}
}

func TestDutyCycleExpiredConfirmationReturnsCorrectionWithoutPowerAction(t *testing.T) {
	configuration := schedule.Configuration{Revision: 3, Schedule: schedule.Specification{Kind: schedule.KindOneTime, Timezone: "Europe/London", LocalMinute: 18 * 60, OneTimeDate: "2026-10-12"}}
	lifecycle := schedule.Lifecycle{Phase: schedule.PhaseInactive}
	duty := expiredConfirmationDutyCycle{serverTestDutyCycle{response: controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionStatus, StateRevision: 12, ConfigurationRevision: 3, Configuration: &configuration, Lifecycle: &lifecycle}}}
	handler, _, session, _ := newTestServerWithDuty(t, true, duty)
	review := schedule.Review{ConfigurationRevision: 3, Reference: 1_791_824_000, MinimumLeadSeconds: uint32(schedule.VerifiedWakeMinimumLead.Seconds()), Identity: strings.Repeat("a", 64), Occurrences: []schedule.Occurrence{{Unix: 1_791_825_600, Local: "2026-10-12 18:00:00", UTCOffset: "+01:00", Timezone: "Europe/London"}}}
	token, err := encodeReview(review)
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"csrf": {session.CSRFToken}, "state_revision": {"12"}, "configuration_revision": {"3"}, "review": {token}}
	response := postForm(handler, "/duty-cycle/enable-real", values, &http.Cookie{Name: sessionCookie, Value: session.Token})
	body := response.Body.String()
	for _, want := range []string{"reviewed occurrence is now too close or has passed", "No wake was programmed and no shutdown was requested", "at least 5 minutes in the future", "Save and review wake and shutdown"} {
		if response.Code != http.StatusConflict || !strings.Contains(body, want) {
			t.Fatalf("expired confirmation missing %q: %d: %s", want, response.Code, body)
		}
	}
	if strings.Contains(body, "Confirm wake and shutdown") || strings.Contains(body, "Shutdown accepted") {
		t.Fatalf("expired confirmation offered a power action: %s", body)
	}
}

func TestDutyCycleReviewShowsOrderedSavedJobNamesAndRevisions(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	known := make(map[string]bool)
	createJob := func(name string) string {
		t.Helper()
		form := url.Values{
			"csrf": {session.CSRFToken}, "revision": {"0"}, "name": {name}, "enabled": {"true"},
			"mode": {"direct"}, "executable": {"/usr/bin/true"}, "arguments": {""}, "command": {""},
			"working_directory": {"/home/pi"}, "environment": {""}, "timeout_seconds": {"30"},
			"stdout_bytes": {"1048576"}, "stderr_bytes": {"1048576"},
		}
		if response := postForm(handler, "/jobs", form, cookie); response.Code != http.StatusSeeOther {
			t.Fatalf("create %q = %d: %s", name, response.Code, response.Body.String())
		}
		list := request(handler, http.MethodGet, "/jobs", nil, cookie).Body.String()
		matches := regexp.MustCompile(`/jobs/([0-9a-f]{32})/edit`).FindAllStringSubmatch(list, -1)
		for _, match := range matches {
			if !known[match[1]] {
				known[match[1]] = true
				return match[1]
			}
		}
		t.Fatalf("created job %q not listed: %s", name, list)
		return ""
	}
	firstID := createJob("First review job")
	secondID := createJob("Second review job")
	values := url.Values{
		"csrf": {session.CSRFToken}, "base_revision": {"1"}, "configuration_revision": {"1"},
		"kind": {"daily"}, "timezone": {"UTC"}, "minute": {"0"}, "local_time": {"09:00"},
		"delay_minutes": {"60"}, "one_time_date": {""}, "quiet_enabled": {"false"},
		"quiet_start": {"22:00"}, "quiet_end": {"06:00"},
		"run_list": {firstID + ":1", secondID + ":1"},
	}
	response := postForm(handler, "/duty-cycle/review", values, cookie)
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("review response = %d: %s", response.Code, body)
	}
	first := strings.Index(body, "First review job")
	second := strings.Index(body, "Second review job")
	if first < 0 || second <= first || !strings.Contains(body, "<h2>Ordered run list</h2>") {
		t.Fatalf("ordered review jobs missing: %s", body)
	}
}

func TestReviewJobRowsMarksUnavailableReference(t *testing.T) {
	runList := []schedule.RunListEntry{{JobID: "first", JobRevision: 2}, {JobID: "missing", JobRevision: 7}}
	definitions := []jobs.Definition{{ID: "first", Revision: 4, DefinitionInput: jobs.DefinitionInput{Name: "Visible name"}}}
	rows := reviewJobRows(runList, definitions)
	if len(rows) != 2 || rows[0].Position != 1 || rows[0].Name != "Visible name" || rows[0].Revision != 2 || !rows[0].Resolved {
		t.Fatalf("resolved row = %+v", rows)
	}
	if rows[1].Position != 2 || rows[1].Name != "Unavailable saved job" || rows[1].Revision != 7 || rows[1].Resolved {
		t.Fatalf("unavailable row = %+v", rows[1])
	}
}

func TestDutyCycleDisableAndSimulatedBootAreRevisionBound(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	values := url.Values{"csrf": {session.CSRFToken}, "state_revision": {"3"}, "configuration_revision": {"2"}}
	for _, path := range []string{"/duty-cycle/disable", "/duty-cycle/simulate-boot"} {
		response := postForm(handler, path, values, cookie)
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/duty-cycle" {
			t.Fatalf("%s response = %d, %q: %s", path, response.Code, response.Header().Get("Location"), response.Body.String())
		}
	}
	values.Set("state_revision", "not-a-revision")
	if response := postForm(handler, "/duty-cycle/simulate-boot", values, cookie); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid revision status = %d: %s", response.Code, response.Body.String())
	}
	values.Set("state_revision", "3")
	values.Set("csrf", "invalid")
	if response := postForm(handler, "/duty-cycle/disable", values, cookie); response.Code != http.StatusForbidden {
		t.Fatalf("invalid CSRF status = %d: %s", response.Code, response.Body.String())
	}
}

func TestDutyCyclePageShowsLiveRunProgressAndFailureEvidence(t *testing.T) {
	configuration := schedule.Configuration{Revision: 2, Schedule: schedule.Specification{Kind: schedule.KindDaily, Timezone: "UTC", LocalMinute: 540}, RunList: []schedule.RunListEntry{{JobID: "first", JobRevision: 1}, {JobID: "second", JobRevision: 3}}}
	lifecycle := schedule.Lifecycle{Phase: schedule.PhaseManualSafe, ConfigurationRevision: 2, Simulated: true, Reason: "scheduled job failed", RunList: []schedule.RunEntry{{JobID: "first", JobRevision: 1, RunID: "00112233445566778899aabbccddeeff", State: schedule.RunFailed}, {JobID: "second", JobRevision: 3, State: schedule.RunNotRun}}}
	duty := serverTestDutyCycle{response: controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionStatus, StateRevision: 8, ConfigurationRevision: 2, Configuration: &configuration, Lifecycle: &lifecycle}}
	handler, _, session, _ := newTestServerWithDuty(t, true, duty)
	page := request(handler, http.MethodGet, "/duty-cycle", nil, &http.Cookie{Name: sessionCookie, Value: session.Token})
	if page.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{"Manual safe", "scheduled job failed", "2 of 2 steps finished", "failed", "not run", "00112233445566778899aabbccddeeff", `href="/runs/00112233445566778899aabbccddeeff"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("page missing %q: %s", want, page.Body.String())
		}
	}
	if strings.Contains(page.Body.String(), "data-duty-cycle-active=\"true\"") {
		t.Fatal("manual-safe page must not auto-refresh")
	}
}

func TestActiveDutyCyclePageEnablesBoundedRefresh(t *testing.T) {
	configuration := schedule.Configuration{Revision: 2, Enabled: true, Schedule: schedule.Specification{Kind: schedule.KindDaily, Timezone: "UTC", LocalMinute: 540}, RunList: []schedule.RunListEntry{{JobID: "first", JobRevision: 1}}}
	lifecycle := schedule.Lifecycle{Phase: schedule.PhaseRunningJob, ConfigurationRevision: 2, IntendedOccurrence: 1_800_000_000, Simulated: true, RunList: []schedule.RunEntry{{JobID: "first", JobRevision: 1, RunID: "00112233445566778899aabbccddeeff", State: schedule.RunRunning}}}
	duty := serverTestDutyCycle{response: controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionStatus, StateRevision: 8, ConfigurationRevision: 2, Configuration: &configuration, Lifecycle: &lifecycle}}
	handler, _, session, _ := newTestServerWithDuty(t, true, duty)
	page := request(handler, http.MethodGet, "/duty-cycle", nil, &http.Cookie{Name: sessionCookie, Value: session.Token})
	for _, want := range []string{"data-duty-cycle-active=\"true\"", "/static/duty-cycle.js?v=4", "Refresh status", "0 of 1 steps finished", "running"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("page missing %q: %s", want, page.Body.String())
		}
	}
	asset := request(handler, http.MethodGet, "/static/duty-cycle.js", nil).Body.String()
	for _, want := range []string{`matchMedia("(prefers-reduced-motion: reduce)")`, `addEventListener("focusin"`, `if (!automaticRefreshAllowed) return`} {
		if !strings.Contains(asset, want) {
			t.Fatalf("active refresh enhancement missing %q: %s", want, asset)
		}
	}
}

func TestRegisterDefaultsAreLabelledWithoutClaimingFlashState(t *testing.T) {
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		register board.Register
		value    string
		raw      string
		note     string
	}{
		{register: "PI_BOOT_TO", value: "300 seconds", raw: "30", note: "not a reading of the last flash-saved value"},
		{register: "VBAT_OFFSET", value: "Factory calibrated", raw: "Board-specific", note: "individual board"},
		{register: "PI_RUNNING", value: "Established at runtime", raw: "Dynamic", note: "does not have a fixed"},
		{register: "VBAT", value: "Not specified", raw: "Not specified", note: "does not state"},
	}
	for _, test := range tests {
		t.Run(string(test.register), func(t *testing.T) {
			definition, exists := catalogue.Find(7, test.register)
			if !exists {
				t.Fatal("definition not found")
			}
			value, raw, note := formatDefault(definition)
			if !strings.Contains(value, test.value) || raw != test.raw || !strings.Contains(note, test.note) {
				t.Fatalf("default = %q, %q, %q", value, raw, note)
			}
		})
	}
}

func TestTemporaryRegisterReviewApplyAndActivity(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}

	detail := request(handler, http.MethodGet, "/registers/LED_STATE", nil, cookie)
	for _, want := range []string{"Review temporary change", "Not saved to flash", "pulsing", "fast-flash"} {
		if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), want) {
			t.Fatalf("LED detail missing %q: %d %s", want, detail.Code, detail.Body.String())
		}
	}
	locked := request(handler, http.MethodGet, "/registers/VOUT_MAX", nil, cookie)
	if locked.Code != http.StatusOK || !strings.Contains(locked.Body.String(), "Not editable in the generic workflow") || !strings.Contains(locked.Body.String(), "specialised advanced workflow") {
		t.Fatalf("advanced register was not visibly locked: %d %s", locked.Code, locked.Body.String())
	}

	proposal := url.Values{"csrf": {session.CSRFToken}, "expected_raw": {"1"}, "proposed_value": {"2"}}
	review := postForm(handler, "/registers/LED_STATE/review", proposal, cookie)
	if review.Code != http.StatusOK || !strings.Contains(review.Body.String(), "Review before applying") || !strings.Contains(review.Body.String(), "Apply temporarily") {
		t.Fatalf("review response = %d: %s", review.Code, review.Body.String())
	}
	activityBeforeApply := request(handler, http.MethodGet, "/activity?type=register.change-attempted", nil, cookie)
	if strings.Contains(activityBeforeApply.Body.String(), "Temporary register change attempted</a>") {
		t.Fatalf("review alone recorded a mutation attempt: %s", activityBeforeApply.Body.String())
	}

	direct := postForm(handler, "/registers/LED_STATE/apply", proposal, cookie)
	if direct.Code != http.StatusBadRequest || !strings.Contains(direct.Body.String(), "Invalid form") {
		t.Fatalf("direct apply response = %d: %s", direct.Code, direct.Body.String())
	}
	proposal.Set("review_token", registerReviewToken(session.Token, "LED_STATE", 1, 2))
	applied := postForm(handler, "/registers/LED_STATE/apply", proposal, cookie)
	if applied.Code != http.StatusOK || !strings.Contains(applied.Body.String(), "Temporary change verified") || !strings.Contains(applied.Body.String(), "Temporary only") {
		t.Fatalf("apply response = %d: %s", applied.Code, applied.Body.String())
	}

	activityPage := request(handler, http.MethodGet, "/activity", nil, cookie)
	for _, want := range []string{"Temporary register change attempted", "Temporary register change observed", "LED_STATE"} {
		if !strings.Contains(activityPage.Body.String(), want) {
			t.Fatalf("Activity page missing %q: %s", want, activityPage.Body.String())
		}
	}

	stale := postForm(handler, "/registers/LED_STATE/apply", proposal, cookie)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "Nothing was written") || !strings.Contains(stale.Body.String(), "Board value changed") {
		t.Fatalf("stale replay response = %d: %s", stale.Code, stale.Body.String())
	}
}

func TestTemporaryRegisterReviewRejectsIneligibleAndTamperedRequests(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	advanced := postForm(handler, "/registers/VOUT_MAX/review", url.Values{
		"csrf": {session.CSRFToken}, "expected_raw": {"5449"}, "proposed_value": {"5450"},
	}, cookie)
	if advanced.Code != http.StatusUnprocessableEntity {
		t.Fatalf("advanced review status = %d: %s", advanced.Code, advanced.Body.String())
	}
	tampered := postForm(handler, "/registers/LED_STATE/apply", url.Values{
		"csrf": {session.CSRFToken}, "expected_raw": {"1"}, "proposed_value": {"3"},
		"review_token": {registerReviewToken(session.Token, "LED_STATE", 1, 2)},
	}, cookie)
	if tampered.Code != http.StatusForbidden {
		t.Fatalf("tampered apply status = %d: %s", tampered.Code, tampered.Body.String())
	}
}

func TestAutoBootUsesStandardSingleRegisterWorkflow(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	detail := request(handler, http.MethodGet, "/registers/AUTO_BOOT", nil, cookie)
	for _, want := range []string{
		"Review temporary change",
		"Choose when the board should start the host automatically.",
		"Manual start only",
		"Battery available — always start",
		"Battery available — respect manual shutdown",
		"External power present — always start",
		"External power present — respect manual shutdown",
		"External power absent — always start",
		"External power lost again — respect manual shutdown",
		"External power restored after removal",
		"unexpected unattended startup",
	} {
		if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), want) {
			t.Fatalf("AUTO_BOOT detail missing %q: %d %s", want, detail.Code, detail.Body.String())
		}
	}
	proposal := url.Values{"csrf": {session.CSRFToken}, "expected_raw": {"1"}, "proposed_value": {"2"}}
	review := postForm(handler, "/registers/AUTO_BOOT/review", proposal, cookie)
	if review.Code != http.StatusOK || !strings.Contains(review.Body.String(), "Review before applying") || !strings.Contains(review.Body.String(), "Battery available — respect manual shutdown") {
		t.Fatalf("AUTO_BOOT review = %d: %s", review.Code, review.Body.String())
	}
	proposal.Set("review_token", registerReviewToken(session.Token, "AUTO_BOOT", 1, 2))
	applied := postForm(handler, "/registers/AUTO_BOOT/apply", proposal, cookie)
	if applied.Code != http.StatusOK || !strings.Contains(applied.Body.String(), "Temporary change verified") {
		t.Fatalf("AUTO_BOOT apply = %d: %s", applied.Code, applied.Body.String())
	}
}

func TestShutdownDelayReviewApplyAndStaleReplay(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	edit := request(handler, http.MethodGet, "/registers/shutdown-delay/edit", nil, cookie)
	for _, want := range []string{"Shutdown and reboot delay", "All registers", "Exact board value 40 ticks", "Manufacturer-documented default: 5 seconds", "Review configuration", `type="number"`, `min="5"`, `max="15"`} {
		if edit.Code != http.StatusOK || !strings.Contains(edit.Body.String(), want) {
			t.Fatalf("shutdown-delay edit missing %q: %d %s", want, edit.Code, edit.Body.String())
		}
	}
	form := url.Values{"csrf": {session.CSRFToken}, "expected_ticks": {"40"}, "proposed_seconds": {"12"}}
	review := postForm(handler, "/registers/shutdown-delay/review", form, cookie)
	if review.Code != http.StatusOK || !strings.Contains(review.Body.String(), "12 seconds") || !strings.Contains(review.Body.String(), "Apply temporarily") {
		t.Fatalf("shutdown-delay review = %d: %s", review.Code, review.Body.String())
	}
	form.Set("review_token", registerReviewToken(session.Token, "SHDN_DELAY", 40, 96))
	applied := postForm(handler, "/registers/shutdown-delay/apply", form, cookie)
	if applied.Code != http.StatusOK || !strings.Contains(applied.Body.String(), "Temporary shutdown delay verified") || !strings.Contains(applied.Body.String(), "not committed to flash") {
		t.Fatalf("shutdown-delay apply = %d: %s", applied.Code, applied.Body.String())
	}
	stale := postForm(handler, "/registers/shutdown-delay/apply", form, cookie)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "Nothing was written") {
		t.Fatalf("shutdown-delay stale replay = %d: %s", stale.Code, stale.Body.String())
	}
}

func TestShutdownDelayRejectsOutOfPolicyAndTamperedRequests(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	invalid := postForm(handler, "/registers/shutdown-delay/review", url.Values{
		"csrf": {session.CSRFToken}, "expected_ticks": {"40"}, "proposed_seconds": {"16"},
	}, cookie)
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid shutdown delay status = %d: %s", invalid.Code, invalid.Body.String())
	}
	tampered := postForm(handler, "/registers/shutdown-delay/apply", url.Values{
		"csrf": {session.CSRFToken}, "expected_ticks": {"40"}, "proposed_seconds": {"13"},
		"review_token": {registerReviewToken(session.Token, "SHDN_DELAY", 40, 96)},
	}, cookie)
	if tampered.Code != http.StatusForbidden {
		t.Fatalf("tampered shutdown delay status = %d: %s", tampered.Code, tampered.Body.String())
	}
}

func TestBatteryThresholdScaledReviewApplyAndStaleReplay(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	expected := map[board.Register]int64{"VBAT_MIN": 4200, "VBAT_SHDN": 4500, "VBAT_BOOT": 4800}
	proposed := map[board.Register]int64{"VBAT_MIN": 4250, "VBAT_SHDN": 4550, "VBAT_BOOT": 4850}
	form := url.Values{"csrf": {session.CSRFToken}}
	for _, register := range batteryThresholdNames {
		key := strings.ToLower(string(register))
		form.Set("expected_"+key, strconv.FormatInt(expected[register], 10))
		form.Set("proposed_"+key, strconv.FormatInt(proposed[register], 10))
	}
	review := postForm(handler, "/registers/battery-thresholds/review", form, cookie)
	if review.Code != http.StatusOK || !strings.Contains(review.Body.String(), "Review before applying") || strings.Contains(review.Body.String(), "Raw value") {
		t.Fatalf("review response = %d: %s", review.Code, review.Body.String())
	}
	form.Set("review_token", batteryThresholdReviewToken(session.Token, expected, proposed))
	applied := postForm(handler, "/registers/battery-thresholds/apply", form, cookie)
	if applied.Code != http.StatusOK || !strings.Contains(applied.Body.String(), "Temporary thresholds verified") || !strings.Contains(applied.Body.String(), "4250 mV") {
		t.Fatalf("apply response = %d: %s", applied.Code, applied.Body.String())
	}
	stale := postForm(handler, "/registers/battery-thresholds/apply", form, cookie)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "Nothing was written") {
		t.Fatalf("stale replay response = %d: %s", stale.Code, stale.Body.String())
	}
}

func TestSavedJobCreateEditDeleteAndActivity(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	jobForm := url.Values{
		"csrf": {session.CSRFToken}, "revision": {"0"}, "name": {"Take photograph"}, "enabled": {"true"},
		"mode": {"direct"}, "executable": {"/home/pi/bin/capture"}, "arguments": {"--once\n--quiet"},
		"working_directory": {"/home/pi"}, "environment": {"CAMERA=front"}, "timeout_seconds": {"60"},
		"stdout_bytes": {"1048576"}, "stderr_bytes": {"1048576"},
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	created := postForm(handler, "/jobs", jobForm, cookie)
	if created.Code != http.StatusSeeOther || created.Header().Get("Location") != "/jobs" {
		t.Fatalf("create response = %d, %q: %s", created.Code, created.Header().Get("Location"), created.Body.String())
	}
	list := request(handler, http.MethodGet, "/jobs", nil, cookie)
	body := list.Body.String()
	match := regexp.MustCompile(`/jobs/([0-9a-f]{32})`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("created job link missing: %s", body)
	}
	id := match[1]
	for _, want := range []string{
		`class="button-secondary" href="/jobs/` + id + `/edit">Edit`,
		`class="button-danger" href="/jobs/` + id + `/delete">Delete`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("saved-job actions missing %q: %s", want, body)
		}
	}
	jobForm.Set("revision", "1")
	jobForm.Set("name", "Take evening photograph")
	updated := postForm(handler, "/jobs/"+id, jobForm, cookie)
	if updated.Code != http.StatusSeeOther {
		t.Fatalf("update response = %d: %s", updated.Code, updated.Body.String())
	}
	stale := postForm(handler, "/jobs/"+id, jobForm, cookie)
	if stale.Code != http.StatusUnprocessableEntity || !strings.Contains(stale.Body.String(), "changed after the form was loaded") {
		t.Fatalf("stale update response = %d: %s", stale.Code, stale.Body.String())
	}
	run := postForm(handler, "/jobs/"+id+"/run", url.Values{"csrf": {session.CSRFToken}, "revision": {"2"}}, cookie)
	runLocation := run.Header().Get("Location")
	if run.Code != http.StatusSeeOther || !regexp.MustCompile(`^/runs/[0-9a-f]{32}$`).MatchString(runLocation) {
		t.Fatalf("run response = %d, %q: %s", run.Code, runLocation, run.Body.String())
	}
	runPage := request(handler, http.MethodGet, runLocation, nil, cookie)
	if runPage.Code != http.StatusOK || !strings.Contains(runPage.Body.String(), "succeeded") || !strings.Contains(runPage.Body.String(), "View captured stdout") {
		t.Fatalf("run page = %d: %s", runPage.Code, runPage.Body.String())
	}
	history := request(handler, http.MethodGet, "/runs", nil, cookie)
	for _, want := range []string{"Run history", "Manual saved job", "Retained", `href="` + runLocation + `"`} {
		if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), want) {
			t.Fatalf("run history missing %q: %d: %s", want, history.Code, history.Body.String())
		}
	}
	stdout := request(handler, http.MethodGet, runLocation+"/output/stdout", nil, cookie)
	if stdout.Code != http.StatusOK || stdout.Body.String() != "test stdout\n" || stdout.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("stdout response = %d, %q: %q", stdout.Code, stdout.Header().Get("Cache-Control"), stdout.Body.String())
	}
	cancelled := postForm(handler, runLocation+"/cancel", url.Values{"csrf": {session.CSRFToken}}, cookie)
	if cancelled.Code != http.StatusSeeOther || cancelled.Header().Get("Location") != runLocation {
		t.Fatalf("cancel response = %d, %q: %s", cancelled.Code, cancelled.Header().Get("Location"), cancelled.Body.String())
	}
	deleted := postForm(handler, "/jobs/"+id+"/delete", url.Values{"csrf": {session.CSRFToken}, "revision": {"2"}}, cookie)
	if deleted.Code != http.StatusSeeOther {
		t.Fatalf("delete response = %d: %s", deleted.Code, deleted.Body.String())
	}
	activityPage := request(handler, http.MethodGet, "/activity", nil, cookie)
	for _, want := range []string{"Saved job created", "Saved job updated", "Manual job run requested", "Manual job run observed", "Manual job cancellation requested", "Saved job deleted"} {
		if !strings.Contains(activityPage.Body.String(), want) {
			t.Fatalf("Activity page missing %q: %s", want, activityPage.Body.String())
		}
	}
}

func TestRunPresentationDistinguishesRetainedTruncatedAndExpiredOutput(t *testing.T) {
	output, err := jobs.NewOutputStore(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	app := application{jobOutput: output}
	run := controllerstate.Run{
		RunID: "00112233445566778899aabbccddeeff", DisplayJobID: "job", JobRevision: 2,
		Trigger: controllerstate.RunTriggerScheduled, State: jobs.StateSucceeded,
		CreatedAt: time.Unix(1, 0), StartedAt: time.Unix(2, 0), FinishedAt: time.Unix(3, 0),
		Occurrence: 1_800_000_000, RunListPosition: 1, StdoutTotal: 20, StderrTotal: 0,
	}
	expired := app.jobRunPage(run, map[string]string{"job": "Camera"})
	if expired.HasStdout || !strings.Contains(expired.Stdout, "expired from retention") || expired.Trigger != "Scheduled duty cycle" || expired.Duration != "1s" {
		t.Fatalf("expired run page = %+v", expired)
	}
	if err := output.Save(run.RunID, []byte("partial"), nil); err != nil {
		t.Fatal(err)
	}
	run.StdoutTruncated = true
	retained := app.jobRunPage(run, map[string]string{"job": "Camera"})
	if !retained.HasStdout || !strings.Contains(retained.Stdout, "retained") || !strings.Contains(retained.Stdout, "capture truncated") {
		t.Fatalf("retained run page = %+v", retained)
	}
}

func TestActivityRunSubjectsUseCanonicalRunIdentity(t *testing.T) {
	runID := "00112233445566778899aabbccddeeff"
	event := activity.Event{Type: string(activity.JobRunObserved), Subject: runID}
	if got := activitySubjectLink(event); got != "/runs/"+runID {
		t.Fatalf("run subject link = %q", got)
	}
}

func TestJobsPageShowsFixedExecutionIdentity(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	page := request(handler, http.MethodGet, "/jobs", nil, &http.Cookie{Name: sessionCookie, Value: session.Token})
	for _, want := range []string{"Execution identity", "pi", "UID 1000", "/home/pi", jobs.DefaultPath, "/bin/bash -lc"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Jobs page missing %q: %s", want, page.Body.String())
		}
	}
}

func TestJobFormsExposeModeSpecificProgressiveEnhancement(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	for _, path := range []string{"/jobs/new", "/jobs/run"} {
		page := request(handler, http.MethodGet, path, nil, cookie)
		body := page.Body.String()
		for _, want := range []string{
			`data-job-form`,
			`data-execution-fields="direct"`,
			`data-execution-fields="shell"`,
			`data-execution-required`,
			`/static/jobs.js?v=1`,
			`JavaScript is off.`,
			`Optional; one argument per non-empty line.`,
		} {
			if page.Code != http.StatusOK || !strings.Contains(body, want) {
				t.Fatalf("%s missing %q: %d: %s", path, want, page.Code, body)
			}
		}
	}
	asset := request(handler, http.MethodGet, "/static/jobs.js", nil)
	for _, want := range []string{`[data-job-form]`, `[data-execution-fields]`, `control.disabled = !active`, `control.required = active`, `mode.addEventListener("change"`} {
		if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), want) {
			t.Fatalf("job enhancement missing %q: %d: %s", want, asset.Code, asset.Body.String())
		}
	}
}

func TestDirectJobWithoutArgumentsRemainsValid(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	form := url.Values{
		"csrf": {session.CSRFToken}, "revision": {"0"}, "name": {"No arguments"}, "enabled": {"true"},
		"mode": {"direct"}, "executable": {"/bin/true"}, "arguments": {""},
		"working_directory": {"/tmp"}, "environment": {""}, "timeout_seconds": {"30"},
		"stdout_bytes": {"1024"}, "stderr_bytes": {"1024"},
	}
	created := postForm(handler, "/jobs", form, cookie)
	if created.Code != http.StatusSeeOther {
		t.Fatalf("zero-argument direct job response = %d: %s", created.Code, created.Body.String())
	}
}

func TestShellJobBrowserFormCreatesAndUpdatesWithoutDirectFields(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	form := url.Values{
		"csrf": {session.CSRFToken}, "revision": {"0"}, "name": {"Shell browser form"}, "enabled": {"true"},
		"mode": {"shell"}, "command": {"printf first"}, "working_directory": {"/tmp"}, "environment": {""},
		"timeout_seconds": {"30"}, "stdout_bytes": {"1024"}, "stderr_bytes": {"1024"},
	}
	created := postForm(handler, "/jobs", form, cookie)
	if created.Code != http.StatusSeeOther {
		t.Fatalf("shell create response = %d: %s", created.Code, created.Body.String())
	}
	list := request(handler, http.MethodGet, "/jobs", nil, cookie)
	match := regexp.MustCompile(`/jobs/([0-9a-f]{32})/edit`).FindStringSubmatch(list.Body.String())
	if len(match) != 2 {
		t.Fatalf("created shell job link missing: %s", list.Body.String())
	}
	form.Set("revision", "1")
	form.Set("command", "printf second")
	updated := postForm(handler, "/jobs/"+match[1], form, cookie)
	if updated.Code != http.StatusSeeOther {
		t.Fatalf("shell update response = %d: %s", updated.Code, updated.Body.String())
	}
}

func TestJobBrowserFormStillRejectsUnexpectedRepeatedAndMissingFields(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	form := url.Values{
		"csrf": {session.CSRFToken}, "revision": {"0"}, "name": {"Strict browser form"}, "enabled": {"true"},
		"mode": {"shell"}, "command": {"printf strict"}, "working_directory": {"/tmp"}, "environment": {""},
		"timeout_seconds": {"30"}, "stdout_bytes": {"1024"}, "stderr_bytes": {"1024"},
	}
	form.Set("unexpected", "value")
	response := postForm(handler, "/jobs", form, cookie)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "unexpected fields") {
		t.Fatalf("unexpected-field response = %d: %s", response.Code, response.Body.String())
	}
	form.Del("unexpected")
	form["command"] = []string{"printf first", "printf second"}
	response = postForm(handler, "/jobs", form, cookie)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "missing or repeated fields") {
		t.Fatalf("repeated-field response = %d: %s", response.Code, response.Body.String())
	}
	form.Del("command")
	response = postForm(handler, "/jobs", form, cookie)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "missing or repeated fields") {
		t.Fatalf("missing-mode-field response = %d: %s", response.Code, response.Body.String())
	}
}

func TestAdHocRunOnlyAndSaveThenRun(t *testing.T) {
	handler, _, session, _ := newTestServer(t, true)
	cookie := &http.Cookie{Name: sessionCookie, Value: session.Token}
	form := url.Values{
		"csrf": {session.CSRFToken}, "name": {""}, "save": {"false"}, "mode": {"direct"},
		"executable": {"/bin/echo"}, "arguments": {"hello"}, "working_directory": {"/tmp"},
		"environment": {""}, "timeout_seconds": {"30"}, "stdout_bytes": {"1024"}, "stderr_bytes": {"1024"},
	}
	run := postForm(handler, "/jobs/run", form, cookie)
	if run.Code != http.StatusSeeOther || !regexp.MustCompile(`^/runs/[0-9a-f]{32}$`).MatchString(run.Header().Get("Location")) {
		t.Fatalf("ad-hoc response = %d, %q: %s", run.Code, run.Header().Get("Location"), run.Body.String())
	}
	page := request(handler, http.MethodGet, run.Header().Get("Location"), nil, cookie)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Ad-hoc command") {
		t.Fatalf("ad-hoc status = %d: %s", page.Code, page.Body.String())
	}

	form.Set("save", "true")
	form.Set("name", "One-off diagnostic")
	form.Set("mode", "shell")
	form.Del("executable")
	form.Del("arguments")
	form.Set("command", "printf 'out'; printf 'error' >&2\r\n")
	saved := postForm(handler, "/jobs/run", form, cookie)
	if saved.Code != http.StatusSeeOther || !regexp.MustCompile(`^/runs/[0-9a-f]{32}$`).MatchString(saved.Header().Get("Location")) {
		t.Fatalf("save-and-run response = %d, %q: %s", saved.Code, saved.Header().Get("Location"), saved.Body.String())
	}
	list := request(handler, http.MethodGet, "/jobs", nil, cookie)
	if !strings.Contains(list.Body.String(), "One-off diagnostic") {
		t.Fatalf("saved definition missing: %s", list.Body.String())
	}
	if strings.Contains(list.Body.String(), "\r") {
		t.Fatal("saved shell command retained a carriage return")
	}
}

func TestUnixTimeInterpretationPreservesExactReading(t *testing.T) {
	catalogue, err := board.RegisterSet7Catalog()
	if err != nil {
		t.Fatal(err)
	}
	rtc, _ := catalogue.Find(7, "RTC_TIME")
	utc, local := interpretReading(board.Reading{Definition: rtc, EngineeringValue: 1_791_666_092})
	if utc != "2026-10-10 21:01:32 UTC" || local == "" {
		t.Fatalf("time interpretation = %q, %q", utc, local)
	}
	wake, _ := catalogue.Find(7, "RTC_WAKE_TIME")
	utc, local = interpretReading(board.Reading{Definition: wake, EngineeringValue: 0, Sentinel: true})
	if utc != "Disabled — no absolute wake scheduled" || local != "" {
		t.Fatalf("disabled wake interpretation = %q, %q", utc, local)
	}
	ordinary, _ := catalogue.Find(7, "PI_BOOT_TO")
	utc, local = interpretReading(board.Reading{Definition: ordinary, EngineeringValue: 300})
	if utc != "" || local != "" {
		t.Fatalf("ordinary interpretation = %q, %q", utc, local)
	}
}

func TestUnsupportedRegisterSetHasNoCatalogueRows(t *testing.T) {
	data := registerListPageData(registers.Snapshot{State: registers.StateUnsupported, Identity: board.Identity{RegisterSet: 8}})
	if data.Label != "Unsupported" || data.RegisterSet != "8" || len(data.Groups) != 0 {
		t.Fatalf("unsupported page data = %+v", data)
	}
}

func TestSetupLifecycleWithoutJavaScript(t *testing.T) {
	handler, manager, _, setup := newTestServer(t, false)
	page := request(handler, http.MethodGet, "/setup?token="+url.QueryEscape(setup.Token), nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `method="post"`) {
		t.Fatalf("setup page status/body = %d, %q", page.Code, page.Body.String())
	}
	csrfCookie := cookieNamed(t, page, setupCSRFCookie)
	reloaded := request(handler, http.MethodGet, "/setup?token="+url.QueryEscape(setup.Token), nil, csrfCookie)
	if reloaded.Code != http.StatusOK || !strings.Contains(reloaded.Body.String(), `value="`+csrfCookie.Value+`"`) {
		t.Fatalf("reloaded setup page did not reuse CSRF cookie")
	}
	for _, cookie := range reloaded.Result().Cookies() {
		if cookie.Name == setupCSRFCookie {
			t.Fatal("reloaded setup page unexpectedly replaced CSRF cookie")
		}
	}
	form := url.Values{
		"csrf": {csrfCookie.Value}, "setup_token": {setup.Token},
		"password": {testPassword}, "password_confirmation": {testPassword},
	}
	req := httptest.NewRequest(http.MethodPost, testOrigin+"/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", testOrigin)
	req.AddCookie(csrfCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("setup response = %d, location %q", response.Code, response.Header().Get("Location"))
	}
	sessionCookieValue := cookieNamed(t, response, sessionCookie)
	if !sessionCookieValue.HttpOnly || sessionCookieValue.SameSite != http.SameSiteLaxMode || sessionCookieValue.Path != "/" || sessionCookieValue.Secure {
		t.Fatalf("unsafe loopback cookie = %+v", sessionCookieValue)
	}
	if !manager.IsConfigured() {
		t.Fatal("HTTP setup did not configure authentication")
	}
	replay := postForm(handler, "/setup", form, csrfCookie)
	if replay.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate setup status = %d", replay.Code)
	}
	if _, err := manager.Authenticate(sessionCookieValue.Value); err != nil {
		t.Fatalf("duplicate setup disturbed original session: %v", err)
	}
}

func TestLoginLogoutAndCSRF(t *testing.T) {
	handler, manager, _, _ := newTestServer(t, true)
	loginPage := request(handler, http.MethodGet, "/login", nil)
	csrfCookie := cookieNamed(t, loginPage, loginCSRFCookie)
	login := url.Values{"csrf": {csrfCookie.Value}, "password": {testPassword}}
	req := httptest.NewRequest(http.MethodPost, testOrigin+"/login", strings.NewReader(login.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", testOrigin+"/login")
	req.AddCookie(csrfCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d: %s", response.Code, response.Body.String())
	}
	sessionCookieValue := cookieNamed(t, response, sessionCookie)
	session, err := manager.Authenticate(sessionCookieValue.Value)
	if err != nil {
		t.Fatal(err)
	}

	badLogout := postForm(handler, "/logout", url.Values{"csrf": {"wrong"}}, sessionCookieValue)
	if badLogout.Code != http.StatusForbidden {
		t.Fatalf("bad logout status = %d", badLogout.Code)
	}
	logout := postForm(handler, "/logout", url.Values{"csrf": {session.CSRFToken}}, sessionCookieValue)
	if logout.Code != http.StatusSeeOther || logout.Header().Get("Location") != "/login" {
		t.Fatalf("logout response = %d, %q", logout.Code, logout.Header().Get("Location"))
	}
	if _, err := manager.Authenticate(sessionCookieValue.Value); !errors.Is(err, auth.ErrSession) {
		t.Fatalf("logged-out session error = %v", err)
	}
}

func postForm(handler http.Handler, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, testOrigin+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", testOrigin)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func postMultipartImage(handler http.Handler, csrf string, value []byte, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("csrf", csrf)
	part, _ := writer.CreateFormFile("image", "host.png")
	_, _ = part.Write(value)
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, testOrigin+"/host-image", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", testOrigin)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func serverTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, width, height))
	value.Set(0, 0, color.NRGBA{G: 0xff, A: 0xff})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, value); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestRejectsHostOriginDuplicateAndOversizedForms(t *testing.T) {
	handler, _, _, _ := newTestServer(t, true)

	wrongHost := httptest.NewRequest(http.MethodGet, "http://wrong.test/healthz", nil)
	wrongHostResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongHostResponse, wrongHost)
	if wrongHostResponse.Code != http.StatusBadRequest {
		t.Fatalf("wrong-host status = %d", wrongHostResponse.Code)
	}

	page := request(handler, http.MethodGet, "/login", nil)
	csrf := cookieNamed(t, page, loginCSRFCookie)
	form := url.Values{"csrf": {csrf.Value}, "password": {testPassword}}
	requestWithOrigin := func(origin string, encoded string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, testOrigin+"/login", strings.NewReader(encoded))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.AddCookie(csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := requestWithOrigin("http://attacker.test", form.Encode()); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", response.Code)
	}
	if response := requestWithOrigin("null", form.Encode()); response.Code != http.StatusSeeOther {
		t.Fatalf("opaque-origin status = %d", response.Code)
	}
	if response := requestWithOrigin("", form.Encode()); response.Code != http.StatusForbidden {
		t.Fatalf("missing-origin status = %d", response.Code)
	}
	duplicate := form.Encode() + "&password=second"
	if response := requestWithOrigin(testOrigin, duplicate); response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate-field status = %d", response.Code)
	}
	oversized := "csrf=" + url.QueryEscape(csrf.Value) + "&password=" + strings.Repeat("x", int(limits.Default().HTTP.MaxBodyBytes))
	if response := requestWithOrigin(testOrigin, oversized); response.Code != http.StatusBadRequest {
		t.Fatalf("oversized-form status = %d", response.Code)
	}
}

func TestHTTPSCookiesAreSecure(t *testing.T) {
	manager, err := auth.NewManager(t.TempDir(), limits.Default().Authentication)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := activity.Open(t.TempDir(), activity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	overviewService, registerService, registerEditor, batchRegisterEditor := serverTestServices(t, activity.NewAudit(journal))
	jobManager, err := controllerstate.Open(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	jobOutput, err := jobs.NewOutputStore(t.TempDir(), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	hostImageStore, err := hostimage.New(t.TempDir(), limits.Default().HostImage)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Options{
		Authentication: manager, Overview: overviewService, Registers: registerService, RegisterEditor: registerEditor, BatchRegisterEditor: batchRegisterEditor, Activity: journal,
		ExternalOrigin: "https://power.test", MaxBodyBytes: 1024, DiagnosticMaxBytes: limits.Default().Persistence.MaxSnapshotBytes,
		Jobs: jobManager, JobLimits: limits.Default().Jobs, JobIdentity: jobs.ExecutionIdentity{User: "pi", UID: 1000, Home: "/home/pi", Path: jobs.DefaultPath}, JobRunner: &serverTestJobRunner{}, JobOutput: jobOutput, HostImage: hostImageStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://power.test/setup", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if !cookieNamed(t, response, setupCSRFCookie).Secure {
		t.Fatal("HTTPS pre-authentication cookie is not Secure")
	}
}

func TestBackoffIsBounded(t *testing.T) {
	now := time.Date(2026, time.October, 10, 12, 0, 0, 0, time.UTC)
	value := newBackoff(func() time.Time { return now })
	for range 4 {
		value.failed("client")
	}
	if delay := value.retryAfter("client"); delay != 0 {
		t.Fatalf("early delay = %v", delay)
	}
	value.failed("client")
	if delay := value.retryAfter("client"); delay != time.Second {
		t.Fatalf("initial delay = %v", delay)
	}
	for range 20 {
		value.failed("client")
	}
	if delay := value.retryAfter("client"); delay > 32*time.Second {
		t.Fatalf("unbounded delay = %v", delay)
	}
	now = now.Add(16 * time.Minute)
	if delay := value.retryAfter("client"); delay != 0 {
		t.Fatalf("stale delay = %v", delay)
	}
}
