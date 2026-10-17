package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/controllerapi"
	"github.com/sinoia/lifepowerctl/internal/controllerstate"
	"github.com/sinoia/lifepowerctl/internal/jobs"
)

type jobsShellData struct {
	Title     string
	Current   string
	Build     BuildInfo
	CSRFToken string
}

type jobListData struct {
	jobsShellData
	Jobs    []jobRowData
	JobUser string
	JobUID  uint32
	JobHome string
	JobPath string
}

type jobRowData struct {
	ID        string
	Revision  uint64
	Name      string
	Enabled   bool
	Mode      string
	Summary   string
	Directory string
	Timeout   string
}

type jobFormData struct {
	jobsShellData
	Heading          string
	Lead             string
	Action           string
	SubmitLabel      string
	Error            string
	ID               string
	Revision         string
	Name             string
	Enabled          string
	Mode             string
	Executable       string
	Arguments        string
	Command          string
	WorkingDirectory string
	Environment      string
	TimeoutSeconds   string
	StdoutBytes      string
	StderrBytes      string
	Save             string
}

type jobDeleteData struct {
	jobsShellData
	ID       string
	Revision uint64
	Name     string
}

type jobRunData struct {
	jobsShellData
	JobID                                                       string
	JobName                                                     string
	RunID                                                       string
	State                                                       string
	Tone                                                        string
	Queued                                                      string
	Started                                                     string
	Finished                                                    string
	Result                                                      string
	Stdout                                                      string
	Stderr                                                      string
	HasStdout                                                   bool
	HasStderr                                                   bool
	CanCancel                                                   bool
	Trigger, Duration, JobRevision, Occurrence, RunListPosition string
}

type jobRunHistoryData struct {
	jobsShellData
	Runs                       []jobRunHistoryRow
	FilterJobID, FilterJobName string
}

type jobRunHistoryRow struct {
	RunID, JobID, JobName, Revision, Trigger, State, Tone string
	Started, Duration, Result, Output                     string
}

const maximumRunHistoryRows = 50

func (app *application) jobList(response http.ResponseWriter, request *http.Request) {
	session := sessionFromContext(request.Context())
	data := jobListData{
		jobsShellData: jobsShellData{Title: "Jobs", Current: "jobs", Build: app.build, CSRFToken: session.CSRFToken},
		JobUser:       app.jobIdentity.User, JobUID: app.jobIdentity.UID, JobHome: app.jobIdentity.Home, JobPath: app.jobIdentity.Path,
	}
	listed, err := app.jobDefinitions.ListJobs(request.Context())
	if err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Jobs unavailable", "The controller could not provide the saved-job library.")
		return
	}
	for _, definition := range listed.Jobs {
		summary := definition.Executable
		mode := "Direct program"
		if definition.Mode == jobs.ModeShell {
			mode = "Shell command"
			summary = definition.Command
		}
		data.Jobs = append(data.Jobs, jobRowData{
			ID: definition.ID, Revision: definition.Revision, Name: definition.Name, Enabled: definition.Enabled,
			Mode: mode, Summary: summary, Directory: definition.WorkingDirectory,
			Timeout: fmt.Sprintf("%d seconds", definition.TimeoutSeconds),
		})
	}
	app.render(response, "job-list", data)
}

func (app *application) jobRunHistory(response http.ResponseWriter, request *http.Request) {
	jobID := request.URL.Query().Get("job")
	if len(jobID) > app.jobLimits.MaxIdentifierBytes {
		app.errorPage(response, http.StatusBadRequest, "Invalid run-history filter", "Choose run history from the Jobs page.")
		return
	}
	listed, err := app.runState.ListRuns(request.Context(), maximumRunHistoryRows, jobID)
	if err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Run history unavailable", "The controller could not provide retained run history.")
		return
	}
	names := app.jobNames(request.Context())
	data := jobRunHistoryData{FilterJobID: jobID, FilterJobName: names[jobID]}
	for _, run := range listed.Runs {
		data.Runs = append(data.Runs, app.jobRunHistoryRow(run, names))
	}
	session := sessionFromContext(request.Context())
	data.jobsShellData = jobsShellData{Title: "Run history", Current: "jobs", Build: app.build, CSRFToken: session.CSRFToken}
	app.render(response, "job-run-history", data)
}

func (app *application) jobNew(response http.ResponseWriter, request *http.Request) {
	session := sessionFromContext(request.Context())
	data := app.newJobForm(session.CSRFToken)
	app.render(response, "job-form", data)
}

func (app *application) jobAdHoc(response http.ResponseWriter, request *http.Request) {
	session := sessionFromContext(request.Context())
	data := app.newJobForm(session.CSRFToken)
	data.jobsShellData.Title = "Run ad-hoc command"
	data.Heading, data.Lead, data.Action, data.SubmitLabel = "Run ad-hoc command", "Run once now as the fixed execution user. Optionally save the same definition first.", "/jobs/run", "Run now"
	data.Name, data.Save = "", "false"
	app.render(response, "job-adhoc", data)
}

func (app *application) jobAdHocRun(response http.ResponseWriter, request *http.Request) {
	form, ok := app.parseAdHocForm(response, request)
	if !ok {
		return
	}
	input, err := form.input()
	if form.Save != "true" {
		input.Name = "Ad-hoc command"
	}
	if err == nil {
		err = input.Validate(app.jobLimits)
	}
	if err != nil {
		form.Error = jobFormError(err)
		form.jobsShellData = jobsShellData{Title: "Run ad-hoc command", Current: "jobs", Build: app.build, CSRFToken: sessionFromContext(request.Context()).CSRFToken}
		form.Heading, form.Lead, form.Action, form.SubmitLabel = "Run ad-hoc command", "Run once now as the fixed execution user. Optionally save the same definition first.", "/jobs/run", "Run now"
		app.renderStatus(response, http.StatusUnprocessableEntity, "job-adhoc", form)
		return
	}
	runID, err := jobs.NewRunID()
	if err != nil {
		app.errorPage(response, http.StatusInternalServerError, "Run unavailable", "A new run identity could not be created.")
		return
	}
	definition := jobs.Definition{ID: "adhoc-" + runID, Revision: 1, DefinitionInput: input}
	pathID := "adhoc"
	if form.Save == "true" {
		var created controllerapi.Response
		created, err = app.jobDefinitions.CreateJob(request.Context(), input)
		if err == nil && created.Job != nil {
			definition = *created.Job
		}
		if err != nil {
			form.Error = jobFormError(err)
			form.jobsShellData = jobsShellData{Title: "Run ad-hoc command", Current: "jobs", Build: app.build, CSRFToken: sessionFromContext(request.Context()).CSRFToken}
			form.Heading, form.Lead, form.Action, form.SubmitLabel = "Run ad-hoc command", "Run once now as the fixed execution user. Optionally save the same definition first.", "/jobs/run", "Run now"
			app.renderStatus(response, http.StatusUnprocessableEntity, "job-adhoc", form)
			return
		}
		pathID = definition.ID
	}
	requestValue := definition.Start(runID)
	if _, err := app.runState.BeginRun(request.Context(), requestValue, pathID, pathID == "adhoc"); err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Run unavailable", "The controller could not durably record this run, so it was not sent to the runner.")
		return
	}
	app.audit.Record(activity.JobRunRequested, activity.ActorAdmin, runID, struct {
		JobID       string        `json:"job_id"`
		JobRevision uint64        `json:"job_revision"`
		State       jobs.RunState `json:"state"`
		AdHoc       bool          `json:"ad_hoc"`
	}{JobID: definition.ID, JobRevision: definition.Revision, State: jobs.StateQueued, AdHoc: pathID == "adhoc"})
	_, _ = app.runState.MarkRunObserved(request.Context(), runID, jobs.StateQueued)
	result, err := app.jobRunner.Do(request.Context(), requestValue)
	if err != nil {
		http.Redirect(response, request, "/runs/"+runID, http.StatusSeeOther)
		return
	}
	_, _ = app.runState.UpdateRun(request.Context(), result)
	http.Redirect(response, request, "/runs/"+runID, http.StatusSeeOther)
}

func (app *application) jobEdit(response http.ResponseWriter, request *http.Request) {
	definition, ok := app.findJob(request.Context(), request.PathValue("id"))
	if !ok {
		app.jobNotFound(response)
		return
	}
	session := sessionFromContext(request.Context())
	data := jobFormFromDefinition(definition)
	data.jobsShellData = jobsShellData{Title: "Edit job", Current: "jobs", Build: app.build, CSRFToken: session.CSRFToken}
	data.Heading, data.Lead, data.Action, data.SubmitLabel = "Edit saved job", "Changes create a new immutable job revision.", "/jobs/"+definition.ID, "Save changes"
	app.render(response, "job-form", data)
}

func (app *application) jobDeleteConfirm(response http.ResponseWriter, request *http.Request) {
	definition, ok := app.findJob(request.Context(), request.PathValue("id"))
	if !ok {
		app.jobNotFound(response)
		return
	}
	session := sessionFromContext(request.Context())
	app.render(response, "job-delete", jobDeleteData{
		jobsShellData: jobsShellData{Title: "Delete job", Current: "jobs", Build: app.build, CSRFToken: session.CSRFToken},
		ID:            definition.ID, Revision: definition.Revision, Name: definition.Name,
	})
}

func (app *application) jobCreate(response http.ResponseWriter, request *http.Request) {
	form, ok := app.parseJobForm(response, request)
	if !ok {
		return
	}
	if form.Revision != "0" {
		app.errorPage(response, http.StatusBadRequest, "Invalid job form", "A new job cannot carry an existing revision.")
		return
	}
	input, err := form.input()
	if err == nil {
		_, err = app.jobDefinitions.CreateJob(request.Context(), input)
	}
	if err != nil {
		form.Error = jobFormError(err)
		form.jobsShellData = jobsShellData{Title: "New job", Current: "jobs", Build: app.build, CSRFToken: sessionFromContext(request.Context()).CSRFToken}
		form.Heading, form.Lead, form.Action, form.SubmitLabel = "Create saved job", "Saving does not run the job or add it to a duty cycle.", "/jobs", "Create job"
		app.renderStatus(response, http.StatusUnprocessableEntity, "job-form", form)
		return
	}
	http.Redirect(response, request, "/jobs", http.StatusSeeOther)
}

func (app *application) jobUpdate(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	form, ok := app.parseJobForm(response, request)
	if !ok {
		return
	}
	revision, err := strconv.ParseUint(form.Revision, 10, 64)
	if err == nil && revision > 0 {
		var input jobs.DefinitionInput
		input, err = form.input()
		if err == nil {
			_, err = app.jobDefinitions.UpdateJob(request.Context(), id, revision, input)
		}
	} else {
		err = controllerstate.ErrJobRevisionConflict
	}
	if errors.Is(err, controllerstate.ErrJobNotFound) || controllerErrorCode(err) == "job_not_found" {
		app.jobNotFound(response)
		return
	}
	if err != nil {
		form.Error = jobFormError(err)
		form.ID = id
		form.jobsShellData = jobsShellData{Title: "Edit job", Current: "jobs", Build: app.build, CSRFToken: sessionFromContext(request.Context()).CSRFToken}
		form.Heading, form.Lead, form.Action, form.SubmitLabel = "Edit saved job", "Changes create a new immutable job revision.", "/jobs/"+id, "Save changes"
		app.renderStatus(response, http.StatusUnprocessableEntity, "job-form", form)
		return
	}
	http.Redirect(response, request, "/jobs", http.StatusSeeOther)
}

func (app *application) jobDelete(response http.ResponseWriter, request *http.Request) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return
	}
	form, ok := app.parseForm(response, request, "csrf", "revision")
	if !ok {
		return
	}
	session := sessionFromContext(request.Context())
	if err := app.authentication.CheckCSRF(session.Token, form.Get("csrf")); err != nil {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return
	}
	id := request.PathValue("id")
	_, exists := app.findJob(request.Context(), id)
	if !exists {
		app.jobNotFound(response)
		return
	}
	revision, err := strconv.ParseUint(form.Get("revision"), 10, 64)
	if err != nil || revision == 0 {
		app.errorPage(response, http.StatusBadRequest, "Invalid job form", "The saved-job revision is invalid.")
		return
	}
	if _, err := app.jobDefinitions.DeleteJob(request.Context(), id, revision); err != nil {
		if errors.Is(err, controllerstate.ErrJobRevisionConflict) || controllerErrorCode(err) == "revision_conflict" {
			app.errorPage(response, http.StatusConflict, "Job changed", "The job changed after this page was loaded. Review it before deleting.")
			return
		}
		app.errorPage(response, http.StatusInternalServerError, "Delete failed", "The saved job could not be deleted.")
		return
	}
	http.Redirect(response, request, "/jobs", http.StatusSeeOther)
}

func (app *application) jobRun(response http.ResponseWriter, request *http.Request) {
	form, ok := app.verifiedJobAction(response, request, "revision")
	if !ok {
		return
	}
	definition, exists := app.findJob(request.Context(), request.PathValue("id"))
	if !exists {
		app.jobNotFound(response)
		return
	}
	revision, err := strconv.ParseUint(form.Get("revision"), 10, 64)
	if err != nil || revision == 0 || revision != definition.Revision {
		app.errorPage(response, http.StatusConflict, "Job changed", "The job changed after this page was loaded. Review it before running.")
		return
	}
	if !definition.Enabled {
		app.errorPage(response, http.StatusConflict, "Job disabled", "Enable this saved job before running it.")
		return
	}
	runID, err := jobs.NewRunID()
	if err != nil {
		app.errorPage(response, http.StatusInternalServerError, "Run unavailable", "A new run identity could not be created.")
		return
	}
	requestValue := definition.Start(runID)
	if _, err := app.runState.BeginRun(request.Context(), requestValue, definition.ID, false); err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Run unavailable", "The controller could not durably record this run, so it was not sent to the runner.")
		return
	}
	app.audit.Record(activity.JobRunRequested, activity.ActorAdmin, runID, struct {
		JobID       string        `json:"job_id"`
		JobRevision uint64        `json:"job_revision"`
		State       jobs.RunState `json:"state"`
	}{JobID: definition.ID, JobRevision: definition.Revision, State: jobs.StateQueued})
	_, _ = app.runState.MarkRunObserved(request.Context(), runID, jobs.StateQueued)
	result, err := app.jobRunner.Do(request.Context(), requestValue)
	if err != nil {
		var runnerErr *jobs.RunnerError
		if errors.As(err, &runnerErr) && runnerErr.Code == "job_active" {
			_, _ = app.runState.MarkRunLost(request.Context(), runID)
			app.errorPage(response, http.StatusConflict, "Job already active", "This saved job already has a queued or running instance. Review that run before starting another.")
			return
		}
		http.Redirect(response, request, "/runs/"+runID, http.StatusSeeOther)
		return
	}
	_, _ = app.runState.UpdateRun(request.Context(), result)
	http.Redirect(response, request, "/runs/"+runID, http.StatusSeeOther)
}

func (app *application) jobRunStatus(response http.ResponseWriter, request *http.Request) {
	runID := request.PathValue("runID")
	stored, err := app.runState.GetRun(request.Context(), runID)
	if err != nil || stored.Run == nil {
		app.errorPage(response, http.StatusNotFound, "Run not found", "The requested run is not retained in run history.")
		return
	}
	run := *stored.Run
	if run.State == jobs.StateQueued || run.State == jobs.StateRunning {
		result, runnerErr := app.jobRunner.Do(request.Context(), jobs.Request{Version: jobs.ProtocolVersion, Action: jobs.ActionStatus, RunID: runID})
		if runnerErr == nil {
			app.observeRunState(request.Context(), run.DisplayJobID, result)
			if updated, updateErr := app.runState.GetRun(request.Context(), runID); updateErr == nil && updated.Run != nil {
				run = *updated.Run
			}
		}
	}
	session := sessionFromContext(request.Context())
	data := app.jobRunPage(run, app.jobNames(request.Context()))
	data.jobsShellData = jobsShellData{Title: "Job run", Current: "jobs", Build: app.build, CSRFToken: session.CSRFToken}
	app.render(response, "job-run", data)
}

func (app *application) jobRunCancel(response http.ResponseWriter, request *http.Request) {
	if _, ok := app.verifiedJobAction(response, request); !ok {
		return
	}
	stored, err := app.runState.GetRun(request.Context(), request.PathValue("runID"))
	if err != nil || stored.Run == nil {
		app.errorPage(response, http.StatusNotFound, "Run not found", "The requested run is not retained in run history.")
		return
	}
	runID := request.PathValue("runID")
	result, err := app.jobRunner.Do(request.Context(), jobs.Request{Version: jobs.ProtocolVersion, Action: jobs.ActionCancel, RunID: runID})
	if err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Cancellation unavailable", "The job runner could not confirm the cancellation request.")
		return
	}
	app.audit.Record(activity.JobRunCancelRequested, activity.ActorAdmin, runID, struct {
		JobID string        `json:"job_id"`
		State jobs.RunState `json:"state"`
	}{JobID: stored.Run.DisplayJobID, State: result.State})
	_, _ = app.runState.UpdateRun(request.Context(), result)
	_, _ = app.runState.MarkRunObserved(request.Context(), runID, result.State)
	http.Redirect(response, request, "/runs/"+runID, http.StatusSeeOther)
}

func (app *application) jobRunOutput(response http.ResponseWriter, request *http.Request) {
	stored, err := app.runState.GetRun(request.Context(), request.PathValue("runID"))
	if err != nil || stored.Run == nil {
		app.errorPage(response, http.StatusNotFound, "Run not found", "The requested run is not retained in run history.")
		return
	}
	data, err := app.jobOutput.Read(request.PathValue("runID"), jobs.OutputStream(request.PathValue("stream")))
	if errors.Is(err, jobs.ErrOutputNotFound) {
		if !terminalRun(stored.Run.State) {
			app.errorPage(response, http.StatusConflict, "Output not available yet", "The run has not finished publishing its captured output.")
			return
		}
		app.errorPage(response, http.StatusGone, "Output expired", "This run is retained, but the captured stream has expired under bounded output retention.")
		return
	}
	if err != nil {
		app.errorPage(response, http.StatusBadRequest, "Invalid output request", "The requested output stream is invalid or could not be safely read.")
		return
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(data)
}

func (app *application) jobForRunPath(ctx context.Context, id string) (jobs.Definition, bool) {
	if id == "adhoc" {
		return jobs.Definition{ID: "adhoc", Revision: 1, DefinitionInput: jobs.DefinitionInput{Name: "Ad-hoc command"}}, true
	}
	return app.findJob(ctx, id)
}

func (app *application) verifiedJobAction(response http.ResponseWriter, request *http.Request, fields ...string) (url.Values, bool) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return nil, false
	}
	allFields := append([]string{"csrf"}, fields...)
	form, ok := app.parseForm(response, request, allFields...)
	if !ok {
		return nil, false
	}
	session := sessionFromContext(request.Context())
	if err := app.authentication.CheckCSRF(session.Token, form.Get("csrf")); err != nil {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return nil, false
	}
	return form, true
}

func (app *application) jobRunPage(run controllerstate.Run, names map[string]string) jobRunData {
	name := runName(run, names)
	data := jobRunData{
		JobID: run.DisplayJobID, JobName: name, RunID: run.RunID, JobRevision: strconv.FormatUint(run.JobRevision, 10),
		State: string(run.State), Tone: "warning", CanCancel: run.State == jobs.StateQueued || run.State == jobs.StateRunning,
		Queued: runTimestamp(run.QueuedAt, run.CreatedAt), Started: runTimestamp(run.StartedAt, time.Time{}), Finished: runTimestamp(run.FinishedAt, time.Time{}),
		Trigger: runTriggerLabel(run.Trigger), Duration: runDuration(run),
	}
	if run.Trigger == controllerstate.RunTriggerScheduled {
		data.Occurrence = runTimestamp(time.Unix(int64(run.Occurrence), 0).UTC(), time.Time{})
		data.RunListPosition = strconv.Itoa(run.RunListPosition)
	}
	if run.State == jobs.StateSucceeded {
		data.Tone = "ok"
	} else if run.State == jobs.StateFailed || run.State == jobs.StateTimedOut || run.State == jobs.StateLost {
		data.Tone = "error"
	}
	if run.ExitCode != nil {
		data.Result = fmt.Sprintf("Exit code %d", *run.ExitCode)
	} else if run.Signal != "" {
		data.Result = "Signal " + run.Signal
	} else {
		data.Result = "—"
	}
	data.Stdout, data.HasStdout = app.outputState(run, jobs.OutputStdout)
	data.Stderr, data.HasStderr = app.outputState(run, jobs.OutputStderr)
	if run.StdoutTruncated {
		data.Stdout += " (capture truncated)"
	}
	if run.StderrTruncated {
		data.Stderr += " (capture truncated)"
	}
	return data
}

func (app *application) jobRunHistoryRow(run controllerstate.Run, names map[string]string) jobRunHistoryRow {
	page := app.jobRunPage(run, names)
	started := page.Started
	if started == "—" {
		started = page.Queued
	}
	output := "Pending"
	if terminalRun(run.State) {
		if page.HasStdout || page.HasStderr {
			output = "Retained"
		} else {
			output = "Expired"
		}
		if run.StdoutTruncated || run.StderrTruncated {
			output += "; truncated"
		}
	}
	return jobRunHistoryRow{RunID: run.RunID, JobID: run.DisplayJobID, JobName: page.JobName, Revision: page.JobRevision, Trigger: page.Trigger, State: page.State, Tone: page.Tone, Started: started, Duration: page.Duration, Result: page.Result, Output: output}
}

func (app *application) outputState(run controllerstate.Run, stream jobs.OutputStream) (string, bool) {
	total := run.StdoutTotal
	if stream == jobs.OutputStderr {
		total = run.StderrTotal
	}
	if !terminalRun(run.State) {
		return fmt.Sprintf("%d bytes observed; capture pending", total), false
	}
	exists, err := app.jobOutput.Exists(run.RunID, stream)
	if err != nil || !exists {
		return fmt.Sprintf("%d bytes observed; expired from retention", total), false
	}
	return fmt.Sprintf("%d bytes observed; retained", total), true
}

func (app *application) jobNames(ctx context.Context) map[string]string {
	result := map[string]string{"adhoc": "Ad-hoc command"}
	if listed, err := app.jobDefinitions.ListJobs(ctx); err == nil {
		for _, definition := range listed.Jobs {
			result[definition.ID] = definition.Name
		}
	}
	return result
}

func runName(run controllerstate.Run, names map[string]string) string {
	if name := names[run.DisplayJobID]; name != "" {
		return name
	}
	if run.Trigger == controllerstate.RunTriggerAdHoc {
		return "Ad-hoc command"
	}
	return "Saved job " + run.DisplayJobID
}

func runTriggerLabel(trigger controllerstate.RunTrigger) string {
	switch trigger {
	case controllerstate.RunTriggerScheduled:
		return "Scheduled duty cycle"
	case controllerstate.RunTriggerAdHoc:
		return "Manual ad-hoc"
	default:
		return "Manual saved job"
	}
}

func runTimestamp(value, fallback time.Time) string {
	if value.IsZero() {
		value = fallback
	}
	if value.IsZero() {
		return "—"
	}
	return formatStatusTime(value.UTC())
}

func runDuration(run controllerstate.Run) string {
	if run.StartedAt.IsZero() {
		return "—"
	}
	end := run.FinishedAt
	if end.IsZero() {
		return "Running"
	}
	return end.Sub(run.StartedAt).Round(time.Millisecond).String()
}

func terminalRun(state jobs.RunState) bool {
	switch state {
	case jobs.StateSucceeded, jobs.StateFailed, jobs.StateTimedOut, jobs.StateCancelled, jobs.StateLost:
		return true
	default:
		return false
	}
}

func (app *application) parseJobForm(response http.ResponseWriter, request *http.Request) (jobFormData, bool) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return jobFormData{}, false
	}
	fields := []string{"csrf", "revision", "name", "enabled", "mode", "working_directory", "environment", "timeout_seconds", "stdout_bytes", "stderr_bytes"}
	form, ok := app.parseFormWithOptional(response, request, fields, []string{"executable", "arguments", "command"})
	if !ok {
		return jobFormData{}, false
	}
	if !app.requireJobExecutionFields(response, form) {
		return jobFormData{}, false
	}
	session := sessionFromContext(request.Context())
	if err := app.authentication.CheckCSRF(session.Token, form.Get("csrf")); err != nil {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return jobFormData{}, false
	}
	return jobFormData{
		Revision: form.Get("revision"), Name: form.Get("name"), Enabled: form.Get("enabled"), Mode: form.Get("mode"),
		Executable: form.Get("executable"), Arguments: form.Get("arguments"), Command: form.Get("command"),
		WorkingDirectory: form.Get("working_directory"), Environment: form.Get("environment"), TimeoutSeconds: form.Get("timeout_seconds"),
		StdoutBytes: form.Get("stdout_bytes"), StderrBytes: form.Get("stderr_bytes"),
	}, true
}

func (app *application) parseAdHocForm(response http.ResponseWriter, request *http.Request) (jobFormData, bool) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return jobFormData{}, false
	}
	fields := []string{"csrf", "name", "save", "mode", "working_directory", "environment", "timeout_seconds", "stdout_bytes", "stderr_bytes"}
	values, ok := app.parseFormWithOptional(response, request, fields, []string{"executable", "arguments", "command"})
	if !ok {
		return jobFormData{}, false
	}
	if !app.requireJobExecutionFields(response, values) {
		return jobFormData{}, false
	}
	session := sessionFromContext(request.Context())
	if err := app.authentication.CheckCSRF(session.Token, values.Get("csrf")); err != nil {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return jobFormData{}, false
	}
	if values.Get("save") != "true" && values.Get("save") != "false" {
		app.errorPage(response, http.StatusBadRequest, "Invalid ad-hoc form", "Choose whether this command should also be saved.")
		return jobFormData{}, false
	}
	return jobFormData{
		Name: values.Get("name"), Save: values.Get("save"), Enabled: "true", Mode: values.Get("mode"),
		Executable: values.Get("executable"), Arguments: values.Get("arguments"), Command: values.Get("command"),
		WorkingDirectory: values.Get("working_directory"), Environment: values.Get("environment"), TimeoutSeconds: values.Get("timeout_seconds"),
		StdoutBytes: values.Get("stdout_bytes"), StderrBytes: values.Get("stderr_bytes"),
	}, true
}

func (app *application) requireJobExecutionFields(response http.ResponseWriter, values url.Values) bool {
	var required []string
	switch jobs.ExecutionMode(values.Get("mode")) {
	case jobs.ModeDirect:
		required = []string{"executable", "arguments"}
	case jobs.ModeShell:
		required = []string{"command"}
	default:
		return true
	}
	for _, field := range required {
		if _, exists := values[field]; !exists {
			app.errorPage(response, http.StatusBadRequest, "Invalid form", "The submitted form contained missing or repeated fields.")
			return false
		}
	}
	return true
}

func (form jobFormData) input() (jobs.DefinitionInput, error) {
	timeout, err := strconv.ParseInt(form.TimeoutSeconds, 10, 64)
	if err != nil {
		return jobs.DefinitionInput{}, fmt.Errorf("timeout must be a whole number of seconds")
	}
	stdout, err := strconv.ParseInt(form.StdoutBytes, 10, 64)
	if err != nil {
		return jobs.DefinitionInput{}, fmt.Errorf("stdout limit must be a whole number of bytes")
	}
	stderr, err := strconv.ParseInt(form.StderrBytes, 10, 64)
	if err != nil {
		return jobs.DefinitionInput{}, fmt.Errorf("stderr limit must be a whole number of bytes")
	}
	environment := make(map[string]string)
	for _, line := range splitNonemptyLines(form.Environment) {
		name, value, found := strings.Cut(line, "=")
		if !found || name == "" {
			return jobs.DefinitionInput{}, fmt.Errorf("environment entries must use NAME=value, one per line")
		}
		if _, duplicate := environment[name]; duplicate {
			return jobs.DefinitionInput{}, fmt.Errorf("environment variable %s is repeated", name)
		}
		environment[name] = value
	}
	if len(environment) == 0 {
		environment = nil
	}
	input := jobs.DefinitionInput{
		Name: form.Name, Enabled: form.Enabled == "true", Mode: jobs.ExecutionMode(form.Mode), Executable: form.Executable,
		Arguments: splitNonemptyLines(form.Arguments), Command: normalizeLineEndings(form.Command), WorkingDirectory: form.WorkingDirectory,
		Environment: environment, TimeoutSeconds: timeout, StdoutBytes: stdout, StderrBytes: stderr,
	}
	if form.Enabled != "true" && form.Enabled != "false" {
		return jobs.DefinitionInput{}, fmt.Errorf("enabled state is invalid")
	}
	return input, nil
}

func (app *application) newJobForm(csrf string) jobFormData {
	return jobFormData{
		jobsShellData: jobsShellData{Title: "New job", Current: "jobs", Build: app.build, CSRFToken: csrf},
		Heading:       "Create saved job", Lead: "Saving does not run the job or add it to a duty cycle.", Action: "/jobs", SubmitLabel: "Create job",
		Revision: "0", Enabled: "true", Mode: string(jobs.ModeDirect), WorkingDirectory: "/home/pi",
		TimeoutSeconds: strconv.FormatInt(int64(app.jobLimits.DefaultTimeout.Seconds()), 10),
		StdoutBytes:    strconv.FormatInt(app.jobLimits.MaxStdoutBytes, 10), StderrBytes: strconv.FormatInt(app.jobLimits.MaxStderrBytes, 10),
	}
}

func jobFormFromDefinition(definition jobs.Definition) jobFormData {
	environment := make([]string, 0, len(definition.Environment))
	for name, value := range definition.Environment {
		environment = append(environment, name+"="+value)
	}
	slices.Sort(environment)
	return jobFormData{
		ID: definition.ID, Revision: strconv.FormatUint(definition.Revision, 10), Name: definition.Name,
		Enabled: strconv.FormatBool(definition.Enabled), Mode: string(definition.Mode), Executable: definition.Executable,
		Arguments: strings.Join(definition.Arguments, "\n"), Command: definition.Command, WorkingDirectory: definition.WorkingDirectory,
		Environment: strings.Join(environment, "\n"), TimeoutSeconds: strconv.FormatInt(definition.TimeoutSeconds, 10),
		StdoutBytes: strconv.FormatInt(definition.StdoutBytes, 10), StderrBytes: strconv.FormatInt(definition.StderrBytes, 10),
	}
}

func splitNonemptyLines(value string) []string {
	value = normalizeLineEndings(value)
	lines := strings.Split(value, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func normalizeLineEndings(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func (app *application) observeRunState(ctx context.Context, jobID string, result jobs.Response) {
	updated, err := app.runState.UpdateRun(ctx, result)
	if err != nil || updated.Run == nil || updated.Run.ObservedState == updated.Run.State {
		return
	}
	app.audit.Record(activity.JobRunObserved, activity.ActorSystem, result.RunID, struct {
		JobID       string        `json:"job_id"`
		State       jobs.RunState `json:"state"`
		ExitCode    *int          `json:"exit_code,omitempty"`
		Signal      string        `json:"signal,omitempty"`
		StdoutTotal int64         `json:"stdout_total"`
		StderrTotal int64         `json:"stderr_total"`
	}{
		JobID: jobID, State: result.State, ExitCode: result.ExitCode, Signal: result.Signal,
		StdoutTotal: result.StdoutTotal, StderrTotal: result.StderrTotal,
	})
	_, _ = app.runState.MarkRunObserved(ctx, result.RunID, result.State)
}

func (app *application) findJob(ctx context.Context, id string) (jobs.Definition, bool) {
	listed, err := app.jobDefinitions.ListJobs(ctx)
	if err != nil {
		return jobs.Definition{}, false
	}
	for _, definition := range listed.Jobs {
		if definition.ID == id {
			return definition, true
		}
	}
	return jobs.Definition{}, false
}

func jobFormError(err error) string {
	switch {
	case errors.Is(err, controllerstate.ErrJobRevisionConflict), controllerErrorCode(err) == "revision_conflict":
		return "This job changed after the form was loaded. Return to the Jobs page and review the current revision."
	case errors.Is(err, controllerstate.ErrJobNameConflict), controllerErrorCode(err) == "name_conflict":
		return "Another saved job already uses that name."
	case errors.Is(err, controllerstate.ErrJobLimit), controllerErrorCode(err) == "job_limit":
		return "The saved-job limit has been reached."
	case errors.Is(err, controllerstate.ErrJobInRunList), controllerErrorCode(err) == "job_in_run_list":
		return "This saved job is referenced by the duty-cycle run list. Remove it there before changing or deleting it."
	default:
		return err.Error()
	}
}

func controllerErrorCode(err error) string {
	var controllerErr *controllerapi.ControllerError
	if errors.As(err, &controllerErr) {
		return controllerErr.Code
	}
	return ""
}

func (app *application) jobNotFound(response http.ResponseWriter) {
	app.errorPage(response, http.StatusNotFound, "Job not found", "The requested saved job does not exist.")
}
