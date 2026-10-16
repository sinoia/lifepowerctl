package server

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/auth"
	"github.com/sinoia/lifepowerctl/internal/controllerapi"
	"github.com/sinoia/lifepowerctl/internal/controllerstate"
	"github.com/sinoia/lifepowerctl/internal/diagnostic"
	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/overview"
	"github.com/sinoia/lifepowerctl/internal/registeredit"
	"github.com/sinoia/lifepowerctl/internal/registers"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

//go:embed templates/*.html static/*
var assets embed.FS

type BuildInfo struct {
	Version string
	Commit  string
}

type Options struct {
	Build               BuildInfo
	HostStatus          HostStatus
	Authentication      *auth.Manager
	Overview            *overview.Service
	Registers           *registers.Service
	RegisterEditor      *registeredit.Service
	BatchRegisterEditor *registeredit.BatchService
	ShutdownDelayEditor *registeredit.Service
	Audit               *activity.Audit
	Activity            *activity.Journal
	ExternalOrigin      string
	TrustedProxy        string
	MaxBodyBytes        int64
	DiagnosticMaxBytes  int
	Jobs                *controllerstate.Manager
	JobDefinitions      JobDefinitions
	RunState            RunStateController
	DutyCycle           DutyCycleController
	JobLimits           limits.Jobs
	JobIdentity         jobs.ExecutionIdentity
	JobRunner           JobRunner
	JobOutput           *jobs.OutputStore
}

type JobRunner interface {
	Do(context.Context, jobs.Request) (jobs.Response, error)
}

type DutyCycleController interface {
	Status(context.Context) (controllerapi.Response, error)
	SaveDisabled(context.Context, string, uint64, schedule.Configuration) (controllerapi.Response, error)
	ProposalStatus(context.Context, string) (controllerapi.Response, error)
	Preview(context.Context, uint64, int) (controllerapi.Response, error)
	EnableSimulation(context.Context, string, uint64, uint64, schedule.Review) (controllerapi.Response, error)
	PreviewReal(context.Context, uint64, int) (controllerapi.Response, error)
	EnableReal(context.Context, string, uint64, uint64, schedule.Review) (controllerapi.Response, error)
	Disable(context.Context, string, uint64, uint64) (controllerapi.Response, error)
	SimulateExpectedBoot(context.Context, uint64, uint64) (controllerapi.Response, error)
}

type JobDefinitions interface {
	ListJobs(context.Context) (controllerapi.Response, error)
	CreateJob(context.Context, jobs.DefinitionInput) (controllerapi.Response, error)
	UpdateJob(context.Context, string, uint64, jobs.DefinitionInput) (controllerapi.Response, error)
	DeleteJob(context.Context, string, uint64) (controllerapi.Response, error)
}

type RunStateController interface {
	BeginRun(context.Context, jobs.Request, string, bool) (controllerapi.Response, error)
	UpdateRun(context.Context, jobs.Response) (controllerapi.Response, error)
	MarkRunObserved(context.Context, string, jobs.RunState) (controllerapi.Response, error)
	MarkRunLost(context.Context, string) (controllerapi.Response, error)
	ListRuns(context.Context, int, string) (controllerapi.Response, error)
	GetRun(context.Context, string) (controllerapi.Response, error)
}

type localJobDefinitions struct {
	state *controllerstate.Manager
	audit *activity.Audit
}

func (source localJobDefinitions) ListJobs(context.Context) (controllerapi.Response, error) {
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionJobList, StateRevision: source.state.Revision(), Jobs: source.state.Jobs()}, nil
}
func (source localJobDefinitions) CreateJob(_ context.Context, input jobs.DefinitionInput) (controllerapi.Response, error) {
	definition, err := source.state.Create(input)
	if err == nil {
		source.auditJob(activity.JobCreated, definition)
	}
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionJobCreate, StateRevision: source.state.Revision(), Job: &definition}, err
}
func (source localJobDefinitions) UpdateJob(_ context.Context, id string, revision uint64, input jobs.DefinitionInput) (controllerapi.Response, error) {
	definition, err := source.state.Update(id, revision, input)
	if err == nil {
		source.auditJob(activity.JobUpdated, definition)
	}
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionJobUpdate, StateRevision: source.state.Revision(), Job: &definition}, err
}
func (source localJobDefinitions) DeleteJob(_ context.Context, id string, revision uint64) (controllerapi.Response, error) {
	err := source.state.Delete(id, revision)
	if err == nil {
		source.audit.Record(activity.JobDeleted, activity.ActorAdmin, id, struct {
			Revision uint64 `json:"revision"`
		}{Revision: revision})
	}
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionJobDelete, StateRevision: source.state.Revision()}, err
}

func (source localJobDefinitions) auditJob(kind activity.Kind, definition jobs.Definition) {
	source.audit.Record(kind, activity.ActorAdmin, definition.ID, struct {
		Revision uint64             `json:"revision"`
		Enabled  bool               `json:"enabled"`
		Mode     jobs.ExecutionMode `json:"mode"`
	}{Revision: definition.Revision, Enabled: definition.Enabled, Mode: definition.Mode})
}

type localRunState struct{ state *controllerstate.Manager }

func (source localRunState) BeginRun(_ context.Context, request jobs.Request, display string, adHoc bool) (controllerapi.Response, error) {
	run, err := source.state.BeginRun(request, display, adHoc)
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionRunBegin, StateRevision: source.state.Revision(), Run: &run}, err
}
func (source localRunState) UpdateRun(_ context.Context, result jobs.Response) (controllerapi.Response, error) {
	run, changed, err := source.state.UpdateRun(result)
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionRunUpdate, StateRevision: source.state.Revision(), Run: &run, Changed: changed}, err
}
func (source localRunState) MarkRunObserved(_ context.Context, runID string, state jobs.RunState) (controllerapi.Response, error) {
	err := source.state.MarkRunObserved(runID, state)
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionRunObserve, StateRevision: source.state.Revision()}, err
}
func (source localRunState) MarkRunLost(_ context.Context, runID string) (controllerapi.Response, error) {
	run, err := source.state.MarkRunLost(runID)
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionRunLost, StateRevision: source.state.Revision(), Run: &run, Changed: true}, err
}
func (source localRunState) ListRuns(_ context.Context, limit int, jobID string) (controllerapi.Response, error) {
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionRunList, StateRevision: source.state.Revision(), Runs: source.state.Runs(limit, jobID)}, nil
}
func (source localRunState) GetRun(_ context.Context, runID string) (controllerapi.Response, error) {
	run, err := source.state.Run(runID)
	return controllerapi.Response{Version: controllerapi.ProtocolVersion, Action: controllerapi.ActionRunGet, StateRevision: source.state.Revision(), Run: &run}, err
}

type application struct {
	build               BuildInfo
	hostStatus          HostStatus
	authentication      *auth.Manager
	overviewService     *overview.Service
	registerService     *registers.Service
	registerEditor      *registeredit.Service
	batchRegisterEditor *registeredit.BatchService
	shutdownDelayEditor *registeredit.Service
	audit               *activity.Audit
	activityJournal     *activity.Journal
	templates           *template.Template
	origin              *url.URL
	trustedProxy        netip.Addr
	maxBodyBytes        int64
	diagnosticMaxBytes  int
	jobDefinitions      JobDefinitions
	runState            RunStateController
	dutyCycle           DutyCycleController
	jobLimits           limits.Jobs
	jobIdentity         jobs.ExecutionIdentity
	jobRunner           JobRunner
	jobOutput           *jobs.OutputStore
	secureCookies       bool
	loginBackoff        *backoff
	setupBackoff        *backoff
}

type pageData struct {
	Title           string
	Current         string
	Build           BuildInfo
	CSRFToken       string
	BoardState      string
	BoardTone       string
	BoardLabel      string
	BoardSummary    string
	RegisterSet     string
	CheckedAt       string
	LastSuccess     string
	FailureGuidance string
	Uptime          string
}

type errorPageData struct {
	Title   string
	Build   BuildInfo
	Status  int
	Message string
}

func New(options Options) (http.Handler, error) {
	if options.Authentication == nil {
		return nil, fmt.Errorf("authentication manager is required")
	}
	if options.Overview == nil {
		return nil, fmt.Errorf("overview service is required")
	}
	if options.Registers == nil {
		return nil, fmt.Errorf("register service is required")
	}
	if options.RegisterEditor == nil {
		return nil, fmt.Errorf("register editor service is required")
	}
	if options.BatchRegisterEditor == nil {
		return nil, fmt.Errorf("batch register editor service is required")
	}
	if options.ShutdownDelayEditor == nil {
		options.ShutdownDelayEditor = options.RegisterEditor
	}
	if options.Activity == nil {
		return nil, fmt.Errorf("activity journal is required")
	}
	origin, err := url.Parse(options.ExternalOrigin)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return nil, fmt.Errorf("invalid external origin")
	}
	if options.MaxBodyBytes <= 0 {
		return nil, fmt.Errorf("maximum body size must be positive")
	}
	if options.DiagnosticMaxBytes <= 0 {
		return nil, fmt.Errorf("diagnostic maximum size must be positive")
	}
	if options.JobLimits.MaxSavedJobs < 1 || options.JobDefinitions == nil && options.Jobs == nil || options.RunState == nil && options.Jobs == nil {
		return nil, fmt.Errorf("saved-job controller and limits are required")
	}
	if err := options.JobIdentity.Validate(); err != nil {
		return nil, fmt.Errorf("job execution identity: %w", err)
	}
	if options.JobRunner == nil || options.JobOutput == nil {
		return nil, fmt.Errorf("job runner client and output reader are required")
	}
	var trustedProxy netip.Addr
	if options.TrustedProxy != "" {
		trustedProxy, err = netip.ParseAddr(options.TrustedProxy)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy")
		}
	}
	hostStatus := options.HostStatus
	if hostStatus == nil {
		hostStatus = systemHostStatus{}
	}
	hostname, err := hostStatus.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "unavailable"
	}
	templates, err := template.New("").Funcs(template.FuncMap{
		"hostName": func() string { return hostname },
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	staticFiles, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	app := &application{
		build: options.Build, hostStatus: hostStatus, authentication: options.Authentication, overviewService: options.Overview, registerService: options.Registers, registerEditor: options.RegisterEditor, batchRegisterEditor: options.BatchRegisterEditor, shutdownDelayEditor: options.ShutdownDelayEditor, audit: options.Audit, activityJournal: options.Activity, templates: templates,
		origin: origin, trustedProxy: trustedProxy, maxBodyBytes: options.MaxBodyBytes, diagnosticMaxBytes: options.DiagnosticMaxBytes,
		dutyCycle: options.DutyCycle, jobLimits: options.JobLimits, jobIdentity: options.JobIdentity, jobRunner: options.JobRunner, jobOutput: options.JobOutput,
		secureCookies: origin.Scheme == "https", loginBackoff: newBackoff(time.Now), setupBackoff: newBackoff(time.Now),
	}
	if options.JobDefinitions != nil {
		app.jobDefinitions = options.JobDefinitions
	} else {
		app.jobDefinitions = localJobDefinitions{state: options.Jobs, audit: options.Audit}
	}
	if options.RunState != nil {
		app.runState = options.RunState
	} else {
		app.runState = localRunState{state: options.Jobs}
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFiles)))
	mux.HandleFunc("GET /healthz", app.health)
	mux.HandleFunc("GET /setup", app.setupPage)
	mux.HandleFunc("POST /setup", app.completeSetup)
	mux.HandleFunc("GET /login", app.loginPage)
	mux.HandleFunc("POST /login", app.login)
	mux.HandleFunc("POST /logout", app.logout)
	mux.Handle("GET /readyz", app.requireSession(http.HandlerFunc(app.readiness)))
	mux.Handle("GET /registers", app.requireSession(http.HandlerFunc(app.registerList)))
	mux.Handle("GET /registers/{register}", app.requireSession(http.HandlerFunc(app.registerDetail)))
	mux.Handle("POST /registers/{register}/review", app.requireSession(http.HandlerFunc(app.registerReview)))
	mux.Handle("POST /registers/{register}/apply", app.requireSession(http.HandlerFunc(app.registerApply)))
	mux.Handle("GET /registers/battery-thresholds/edit", app.requireSession(http.HandlerFunc(app.batteryThresholdEdit)))
	mux.Handle("POST /registers/battery-thresholds/review", app.requireSession(http.HandlerFunc(app.batteryThresholdReview)))
	mux.Handle("POST /registers/battery-thresholds/apply", app.requireSession(http.HandlerFunc(app.batteryThresholdApply)))
	mux.Handle("GET /registers/shutdown-delay/edit", app.requireSession(http.HandlerFunc(app.shutdownDelayEdit)))
	mux.Handle("POST /registers/shutdown-delay/review", app.requireSession(http.HandlerFunc(app.shutdownDelayReview)))
	mux.Handle("POST /registers/shutdown-delay/apply", app.requireSession(http.HandlerFunc(app.shutdownDelayApply)))
	mux.Handle("GET /activity", app.requireSession(http.HandlerFunc(app.activityList)))
	mux.Handle("GET /activity/{id}", app.requireSession(http.HandlerFunc(app.activityDetail)))
	mux.Handle("GET /diagnostics/report.json", app.requireSession(http.HandlerFunc(app.diagnosticReport)))
	mux.Handle("GET /duty-cycle", app.requireSession(http.HandlerFunc(app.dutyCyclePage)))
	mux.Handle("GET /duty-cycle/edit", app.requireSession(http.HandlerFunc(app.dutyCycleEdit)))
	mux.Handle("POST /duty-cycle/review", app.requireSession(http.HandlerFunc(app.dutyCycleReview)))
	mux.Handle("POST /duty-cycle/enable", app.requireSession(http.HandlerFunc(app.dutyCycleEnable)))
	mux.Handle("POST /duty-cycle/review-real", app.requireSession(http.HandlerFunc(app.dutyCycleReviewReal)))
	mux.Handle("POST /duty-cycle/enable-real", app.requireSession(http.HandlerFunc(app.dutyCycleEnableReal)))
	mux.Handle("POST /duty-cycle/disable", app.requireSession(http.HandlerFunc(app.dutyCycleDisable)))
	mux.Handle("POST /duty-cycle/simulate-boot", app.requireSession(http.HandlerFunc(app.dutyCycleSimulateBoot)))
	mux.Handle("GET /jobs", app.requireSession(http.HandlerFunc(app.jobList)))
	mux.Handle("GET /jobs/new", app.requireSession(http.HandlerFunc(app.jobNew)))
	mux.Handle("GET /jobs/run", app.requireSession(http.HandlerFunc(app.jobAdHoc)))
	mux.Handle("POST /jobs/run", app.requireSession(http.HandlerFunc(app.jobAdHocRun)))
	mux.Handle("POST /jobs", app.requireSession(http.HandlerFunc(app.jobCreate)))
	mux.Handle("GET /jobs/{id}/edit", app.requireSession(http.HandlerFunc(app.jobEdit)))
	mux.Handle("GET /jobs/{id}/delete", app.requireSession(http.HandlerFunc(app.jobDeleteConfirm)))
	mux.Handle("POST /jobs/{id}", app.requireSession(http.HandlerFunc(app.jobUpdate)))
	mux.Handle("POST /jobs/{id}/delete", app.requireSession(http.HandlerFunc(app.jobDelete)))
	mux.Handle("POST /jobs/{id}/run", app.requireSession(http.HandlerFunc(app.jobRun)))
	mux.Handle("GET /runs", app.requireSession(http.HandlerFunc(app.jobRunHistory)))
	mux.Handle("GET /runs/{runID}", app.requireSession(http.HandlerFunc(app.jobRunStatus)))
	mux.Handle("POST /runs/{runID}/cancel", app.requireSession(http.HandlerFunc(app.jobRunCancel)))
	mux.Handle("GET /runs/{runID}/output/{stream}", app.requireSession(http.HandlerFunc(app.jobRunOutput)))
	mux.Handle("GET /jobs/{id}/runs/{runID}", app.requireSession(http.HandlerFunc(app.jobRunStatus)))
	mux.Handle("POST /jobs/{id}/runs/{runID}/cancel", app.requireSession(http.HandlerFunc(app.jobRunCancel)))
	mux.Handle("GET /jobs/{id}/runs/{runID}/output/{stream}", app.requireSession(http.HandlerFunc(app.jobRunOutput)))
	mux.Handle("GET /", app.requireSession(http.HandlerFunc(app.overview)))
	return securityHeaders(app.validateHost(mux)), nil
}

func (app *application) diagnosticReport(response http.ResponseWriter, request *http.Request) {
	snapshot := app.registerService.Refresh(request.Context())
	events, err := app.activityJournal.Events()
	if err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Diagnostic report unavailable", "The Activity journal could not be read.")
		return
	}
	encoded, err := diagnostic.Generate(time.Now(), diagnostic.Build{Version: app.build.Version, Commit: app.build.Commit}, snapshot, events, maximumActivityRows, app.diagnosticMaxBytes)
	if err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Diagnostic report unavailable", "The bounded diagnostic report could not be created.")
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Content-Disposition", `attachment; filename="lifepowerctl-diagnostic.json"`)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(encoded)
}

func (app *application) health(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("ok\n"))
}

func (app *application) overview(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		app.errorPage(response, http.StatusNotFound, "Page not found", "The requested page does not exist in this version of LifePower Control.")
		return
	}
	session := sessionFromContext(request.Context())
	snapshot := app.overviewService.Refresh(request.Context())
	data := overviewPageData(snapshot)
	data.Title = "Overview"
	data.Current = "overview"
	data.Build = app.build
	data.CSRFToken = session.CSRFToken
	if uptime, err := app.hostStatus.Uptime(); err == nil {
		data.Uptime = formatUptime(uptime)
	} else {
		data.Uptime = "Unavailable"
	}
	app.render(response, "page", data)
}

func (app *application) errorPage(response http.ResponseWriter, status int, title, message string) {
	app.renderStatus(response, status, "error-page", errorPageData{Title: title, Build: app.build, Status: status, Message: message})
}

func (app *application) render(response http.ResponseWriter, name string, data any) {
	app.renderStatus(response, http.StatusOK, name, data)
}

func (app *application) renderStatus(response http.ResponseWriter, status int, name string, data any) {
	var output bytes.Buffer
	if err := app.templates.ExecuteTemplate(&output, name, data); err != nil {
		http.Error(response, "render page", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_, _ = response.Write(output.Bytes())
}

func (app *application) validateHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.EqualFold(request.Host, app.origin.Host) {
			http.Error(response, "invalid request host", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(response, request)
	})
}
