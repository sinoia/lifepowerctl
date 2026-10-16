package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sinoia/lifepowerctl/internal/controllerapi"
	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

type dutyCyclePageData struct {
	Title, Current, CSRFToken string
	Build                     BuildInfo
	Available                 bool
	Tone, State, Summary      string
	Enabled, Schedule         string
	ConfigurationRevision     uint64
	StateRevision             uint64
	Occurrence, Reason        string
	ActionUnavailable         string
	RunList                   []dutyCycleRunRow
	CanDisable, CanSimulate   bool
	CanEdit                   bool
	RealEnabled               bool
	Active                    bool
	Progress                  string
}

type dutyCycleRunRow struct {
	Position, Job, Name, Revision, State, Tone, RunID string
}

type dutyCycleFormData struct {
	Title, Current, CSRFToken, Error                               string
	KindError, TimezoneError, MinuteError, LocalTimeError          string
	DelayError, OneTimeDateError, WeekdaysError, QuietError        string
	RunListError                                                   string
	Build                                                          BuildInfo
	BaseRevision, ConfigurationRevision                            uint64
	Kind, Timezone, Minute, LocalTime, DelayMinutes, OneTimeDate   string
	QuietEnabled, QuietStart, QuietEnd                             string
	Monday, Tuesday, Wednesday, Thursday, Friday, Saturday, Sunday bool
	Jobs                                                           []dutyCycleJobChoice
}

type dutyCycleJobChoice struct {
	ID, Name          string
	Revision          uint64
	Selected, Enabled bool
}

type dutyCycleReviewData struct {
	Title, Current, CSRFToken            string
	ReviewToken, Message                 string
	Build                                BuildInfo
	StateRevision, ConfigurationRevision uint64
	Schedule                             string
	Occurrences                          []schedule.Occurrence
	AfterCompletion                      bool
	RunListJobs                          int
	Jobs                                 []dutyCycleReviewJob
	MaximumRun, Reference, MinimumLead   string
	Real                                 bool
}

type dutyCycleReviewJob struct {
	Position int
	Name     string
	Revision uint64
	Resolved bool
}

type dutyCycleShutdownData struct {
	Title, Current, CSRFToken, WakeTime string
	Build                               BuildInfo
	CountdownSeconds                    int
}

func (app *application) dutyCyclePage(response http.ResponseWriter, request *http.Request) {
	session := sessionFromContext(request.Context())
	data := dutyCyclePageData{Title: "Duty Cycle", Current: "duty-cycle", Build: app.build, CSRFToken: session.CSRFToken, Tone: "warning", State: "Unavailable", Summary: "The controller status endpoint is not available."}
	if app.dutyCycle == nil {
		app.renderStatus(response, http.StatusServiceUnavailable, "duty-cycle", data)
		return
	}
	status, err := app.dutyCycle.Status(request.Context())
	if err != nil || status.Lifecycle == nil {
		data.Summary = "The web process could not read authoritative duty-cycle state from the controller. No state was inferred locally."
		app.renderStatus(response, http.StatusServiceUnavailable, "duty-cycle", data)
		return
	}
	data.Available = true
	data.StateRevision = status.StateRevision
	data.State = phaseLabel(status.Lifecycle.Phase)
	data.Tone = lifecycleTone(status.Lifecycle.Phase)
	kind := schedule.KindDisabled
	if status.Configuration != nil {
		kind = status.Configuration.Schedule.Kind
	}
	data.Summary = lifecycleSummary(*status.Lifecycle, kind)
	data.Reason = status.Lifecycle.Reason
	data.Active = activeLifecycle(status.Lifecycle.Phase)
	if status.Lifecycle.IntendedOccurrence != 0 {
		data.Occurrence = formatOccurrence(status.Lifecycle.IntendedOccurrence, status.Configuration)
	}
	if status.Configuration == nil {
		data.Enabled, data.Schedule = "No", "Not configured"
		data.CanEdit = true
	} else {
		data.ConfigurationRevision = status.Configuration.Revision
		if status.Configuration.Enabled {
			if status.Lifecycle.Simulated {
				data.Enabled = "Yes — simulation only"
			} else {
				data.Enabled = "Yes"
				data.RealEnabled = true
			}
			data.CanDisable = canDisableLifecycle(status.Lifecycle.Phase)
			if !data.CanDisable {
				data.ActionUnavailable = "Automatic operation cannot be disabled or edited while the current duty-cycle transition is active."
			}
		} else {
			data.Enabled = "No"
			data.CanEdit = true
		}
		data.Schedule = scheduleSummary(status.Configuration.Schedule)
		data.CanSimulate = status.Configuration.Enabled && status.Lifecycle.Phase == schedule.PhaseSimulatedShutdownIntended
		jobNames := make(map[string]string)
		listed, _ := app.jobDefinitions.ListJobs(request.Context())
		for _, definition := range listed.Jobs {
			jobNames[definition.ID] = definition.Name
		}
		completed := 0
		for index, entry := range status.Configuration.RunList {
			defaultState, defaultTone := "Not started", ""
			if status.Configuration.Enabled {
				defaultState, defaultTone = "Waiting", "warning"
			}
			row := dutyCycleRunRow{Position: fmt.Sprintf("%d", index+1), Job: entry.JobID, Name: jobNames[entry.JobID], Revision: fmt.Sprintf("%d", entry.JobRevision), State: defaultState, Tone: defaultTone}
			if row.Name == "" {
				row.Name = entry.JobID
			}
			if index < len(status.Lifecycle.RunList) {
				run := status.Lifecycle.RunList[index]
				row.State = strings.ReplaceAll(string(run.State), "-", " ")
				row.RunID = run.RunID
				switch run.State {
				case schedule.RunSucceeded:
					row.Tone = "ok"
					completed++
				case schedule.RunFailed, schedule.RunNotRun:
					row.Tone = "error"
					completed++
				}
			}
			data.RunList = append(data.RunList, row)
		}
		if len(data.RunList) > 0 {
			data.Progress = fmt.Sprintf("%d of %d steps finished", completed, len(data.RunList))
		}
	}
	app.render(response, "duty-cycle", data)
}

func activeLifecycle(phase schedule.Phase) bool {
	switch phase {
	case schedule.PhaseExpectedBootObserved, schedule.PhasePreparingRun, schedule.PhaseRunningJob, schedule.PhaseRunListSucceeded:
		return true
	default:
		return false
	}
}

func (app *application) dutyCycleEdit(response http.ResponseWriter, request *http.Request) {
	if app.dutyCycle == nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Controller unavailable", "Duty-cycle configuration cannot be edited while controller status is unavailable.")
		return
	}
	status, err := app.dutyCycle.Status(request.Context())
	if err != nil || status.Lifecycle == nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Controller unavailable", "Duty-cycle configuration cannot be edited while controller status is unavailable.")
		return
	}
	if status.Configuration != nil && status.Configuration.Enabled {
		app.errorPage(response, http.StatusConflict, "Disable before editing", "An enabled duty cycle is immutable. Disable it before changing its schedule or run list.")
		return
	}
	data := app.dutyCycleForm(request.Context(), status.StateRevision, status.Configuration)
	data.CSRFToken = sessionFromContext(request.Context()).CSRFToken
	app.render(response, "duty-cycle-form", data)
}

func (app *application) dutyCycleReview(response http.ResponseWriter, request *http.Request) {
	if app.dutyCycle == nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Controller unavailable", "Duty-cycle configuration cannot be saved while the controller is unavailable.")
		return
	}
	form, configuration, ok := app.parseDutyCycleForm(response, request)
	if !ok {
		return
	}
	proposalID, err := newProposalID()
	if err == nil {
		_, err = app.dutyCycle.SaveDisabled(request.Context(), proposalID, form.BaseRevision, configuration)
		if err != nil {
			if resolved, lookupErr := app.dutyCycle.ProposalStatus(request.Context(), proposalID); lookupErr == nil && resolved.ConfigurationRevision == configuration.Revision {
				err = nil
			}
		}
	}
	if err != nil {
		form.Error = "The controller rejected this revision or became unavailable. Reload the current duty-cycle state before trying again."
		app.renderStatus(response, http.StatusConflict, "duty-cycle-form", form)
		return
	}
	if configuration.Schedule.Kind == schedule.KindDisabled {
		http.Redirect(response, request, "/duty-cycle", http.StatusSeeOther)
		return
	}
	preview, err := app.dutyCycle.PreviewReal(request.Context(), configuration.Revision, 5)
	if err != nil || preview.Review == nil {
		var controllerErr *controllerapi.ControllerError
		if errors.As(err, &controllerErr) && controllerErr.Code == "no_occurrence" {
			data := app.dutyCycleForm(request.Context(), preview.StateRevision, &configuration)
			data.CSRFToken = sessionFromContext(request.Context()).CSRFToken
			data.Error = "The configuration was saved, but no occurrence meets the 5-minute verified-wake lead. Choose a later occurrence and review again."
			if configuration.Schedule.Kind == schedule.KindOneTime {
				data.LocalTimeError = "Choose a one-time date and time at least 5 minutes in the future."
			}
			app.renderStatus(response, http.StatusUnprocessableEntity, "duty-cycle-form", data)
			return
		}
		app.errorPage(response, http.StatusUnprocessableEntity, "Board-clock review unavailable", "The configuration was saved, but the controller could not calculate and verify its wake-and-shutdown review from the board clock.")
		return
	}
	data, err := app.reviewPageData(request.Context(), preview.StateRevision, configuration, *preview.Review, "")
	if err != nil {
		app.errorPage(response, http.StatusInternalServerError, "Review unavailable", "The bounded occurrence review could not be prepared.")
		return
	}
	data.Real = true
	data.CSRFToken = sessionFromContext(request.Context()).CSRFToken
	app.render(response, "duty-cycle-review", data)
}

func (app *application) dutyCycleEnable(response http.ResponseWriter, request *http.Request) {
	if app.dutyCycle == nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Controller unavailable", "The duty cycle cannot be enabled while the controller is unavailable.")
		return
	}
	form, ok := app.verifiedDutyCycleAction(response, request, "state_revision", "configuration_revision", "review")
	if !ok {
		return
	}
	stateRevision, stateErr := strconv.ParseUint(form.Get("state_revision"), 10, 64)
	configurationRevision, configurationErr := strconv.ParseUint(form.Get("configuration_revision"), 10, 64)
	review, reviewErr := decodeReview(form.Get("review"))
	if stateErr != nil || configurationErr != nil || configurationRevision == 0 || reviewErr != nil || review.ConfigurationRevision != configurationRevision {
		app.errorPage(response, http.StatusBadRequest, "Invalid review", "The occurrence review was invalid. Return to Duty Cycle and calculate it again.")
		return
	}
	proposalID, err := newProposalID()
	if err != nil {
		app.errorPage(response, http.StatusInternalServerError, "Enable unavailable", "A proposal identity could not be generated.")
		return
	}
	result, err := app.dutyCycle.EnableSimulation(request.Context(), proposalID, stateRevision, configurationRevision, review)
	if err == nil {
		http.Redirect(response, request, "/duty-cycle", http.StatusSeeOther)
		return
	}
	var controllerErr *controllerapi.ControllerError
	if errors.As(err, &controllerErr) && controllerErr.Code == "confirmation_required" && result.Review != nil {
		status, statusErr := app.dutyCycle.Status(request.Context())
		if statusErr != nil || status.Configuration == nil || status.Configuration.Revision != configurationRevision {
			app.errorPage(response, http.StatusConflict, "Duty cycle changed", "The saved configuration changed while the occurrence was being confirmed. Review it again.")
			return
		}
		data, encodeErr := app.reviewPageData(request.Context(), result.StateRevision, *status.Configuration, *result.Review, "The first occurrence changed while you were reviewing it. Review the fresh controller calculation and confirm again.")
		if encodeErr != nil {
			app.errorPage(response, http.StatusInternalServerError, "Review unavailable", "The updated review could not be prepared.")
			return
		}
		data.CSRFToken = sessionFromContext(request.Context()).CSRFToken
		app.renderStatus(response, http.StatusConflict, "duty-cycle-review", data)
		return
	}
	if resolved, lookupErr := app.dutyCycle.ProposalStatus(request.Context(), proposalID); lookupErr == nil && resolved.ConfigurationRevision == configurationRevision {
		http.Redirect(response, request, "/duty-cycle", http.StatusSeeOther)
		return
	}
	app.errorPage(response, http.StatusConflict, "Enable rejected", "The controller rejected enablement. The duty cycle remains disabled; reload its current state before trying again.")
}

func (app *application) dutyCycleReviewReal(response http.ResponseWriter, request *http.Request) {
	if app.dutyCycle == nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Controller unavailable", "A scheduled wake-and-shutdown review cannot be calculated while the controller is unavailable.")
		return
	}
	form, ok := app.verifiedDutyCycleAction(response, request, "state_revision", "configuration_revision")
	if !ok {
		return
	}
	configurationRevision, err := strconv.ParseUint(form.Get("configuration_revision"), 10, 64)
	if err != nil || configurationRevision == 0 {
		app.errorPage(response, http.StatusBadRequest, "Invalid wake-and-shutdown review", "The duty-cycle revision was invalid.")
		return
	}
	status, err := app.dutyCycle.Status(request.Context())
	if err != nil || status.Configuration == nil || status.Configuration.Enabled || status.Configuration.Revision != configurationRevision {
		app.errorPage(response, http.StatusConflict, "Duty cycle changed", "The saved disabled configuration changed before the scheduled wake-and-shutdown review.")
		return
	}
	preview, err := app.dutyCycle.PreviewReal(request.Context(), configurationRevision, 5)
	if err != nil || preview.Review == nil {
		app.errorPage(response, http.StatusUnprocessableEntity, "Board-clock preview unavailable", "The controller could not calculate and verify a scheduled wake from the board clock.")
		return
	}
	data, err := app.reviewPageData(request.Context(), preview.StateRevision, *status.Configuration, *preview.Review, "")
	if err != nil {
		app.errorPage(response, http.StatusInternalServerError, "Review unavailable", "The bounded wake-and-shutdown review could not be prepared.")
		return
	}
	data.Real = true
	data.CSRFToken = sessionFromContext(request.Context()).CSRFToken
	app.render(response, "duty-cycle-review", data)
}

func (app *application) dutyCycleEnableReal(response http.ResponseWriter, request *http.Request) {
	if app.dutyCycle == nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Controller unavailable", "Automatic duty-cycle power operation cannot be enabled while the controller is unavailable.")
		return
	}
	form, ok := app.verifiedDutyCycleAction(response, request, "state_revision", "configuration_revision", "review")
	if !ok {
		return
	}
	stateRevision, stateErr := strconv.ParseUint(form.Get("state_revision"), 10, 64)
	configurationRevision, configurationErr := strconv.ParseUint(form.Get("configuration_revision"), 10, 64)
	review, reviewErr := decodeReview(form.Get("review"))
	if stateErr != nil || configurationErr != nil || configurationRevision == 0 || reviewErr != nil || review.ConfigurationRevision != configurationRevision || review.MinimumLeadSeconds != uint32(schedule.VerifiedWakeMinimumLead.Seconds()) {
		app.errorPage(response, http.StatusBadRequest, "Invalid wake-and-shutdown review", "The board-clock occurrence review was invalid. Calculate it again before enabling automatic wake and shutdown.")
		return
	}
	proposalID, err := newProposalID()
	if err != nil {
		app.errorPage(response, http.StatusInternalServerError, "Enable unavailable", "A proposal identity could not be generated.")
		return
	}
	result, err := app.dutyCycle.EnableReal(request.Context(), proposalID, stateRevision, configurationRevision, review)
	if err == nil {
		wakeTime := review.Occurrences[0].Local + " " + review.Occurrences[0].Timezone
		if result.IntendedOccurrence != 0 {
			wakeTime = formatOccurrence(result.IntendedOccurrence, &schedule.Configuration{Schedule: schedule.Specification{Timezone: review.Occurrences[0].Timezone}})
		}
		app.renderStatus(response, http.StatusAccepted, "duty-cycle-shutdown", dutyCycleShutdownData{
			Title: "Shutdown accepted", Current: "duty-cycle", Build: app.build,
			CSRFToken: sessionFromContext(request.Context()).CSRFToken,
			WakeTime:  wakeTime, CountdownSeconds: 8,
		})
		return
	}
	var controllerErr *controllerapi.ControllerError
	if errors.As(err, &controllerErr) && controllerErr.Code == "confirmation_required" && result.Review != nil {
		status, statusErr := app.dutyCycle.Status(request.Context())
		if statusErr != nil || status.Configuration == nil || status.Configuration.Revision != configurationRevision {
			app.errorPage(response, http.StatusConflict, "Duty cycle changed", "The saved configuration changed while the board-clock occurrence was being confirmed.")
			return
		}
		data, encodeErr := app.reviewPageData(request.Context(), result.StateRevision, *status.Configuration, *result.Review, "The first board-clock occurrence changed. Review the fresh calculation before confirming wake and shutdown.")
		if encodeErr != nil {
			app.errorPage(response, http.StatusInternalServerError, "Review unavailable", "The updated wake-and-shutdown review could not be prepared.")
			return
		}
		data.Real = true
		data.CSRFToken = sessionFromContext(request.Context()).CSRFToken
		app.renderStatus(response, http.StatusConflict, "duty-cycle-review", data)
		return
	}
	if errors.As(err, &controllerErr) && controllerErr.Code == "no_occurrence" {
		status, statusErr := app.dutyCycle.Status(request.Context())
		if statusErr == nil && status.Configuration != nil && !status.Configuration.Enabled && status.Configuration.Revision == configurationRevision {
			data := app.dutyCycleForm(request.Context(), status.StateRevision, status.Configuration)
			data.CSRFToken = sessionFromContext(request.Context()).CSRFToken
			data.Error = "The reviewed occurrence is now too close or has passed. No wake was programmed and no shutdown was requested. Choose a later occurrence and review again."
			if status.Configuration.Schedule.Kind == schedule.KindOneTime {
				data.LocalTimeError = "Choose a one-time date and time at least 5 minutes in the future."
			}
			app.renderStatus(response, http.StatusConflict, "duty-cycle-form", data)
			return
		}
	}
	if controllerErr != nil {
		app.errorPage(response, http.StatusConflict, "Duty-cycle activation stopped", "The controller did not complete verified wake programming and Linux shutdown. Automatic operation was inhibited; inspect the current duty-cycle evidence before trying again.")
		return
	}
	if resolved, lookupErr := app.dutyCycle.ProposalStatus(request.Context(), proposalID); lookupErr == nil && resolved.ConfigurationRevision == configurationRevision {
		status, statusErr := app.dutyCycle.Status(request.Context())
		if statusErr == nil && status.Configuration != nil && status.Configuration.Enabled && status.Configuration.Revision == configurationRevision && status.Lifecycle != nil && status.Lifecycle.Phase == schedule.PhaseShutdownRequested && !status.Lifecycle.Simulated {
			http.Redirect(response, request, "/duty-cycle", http.StatusSeeOther)
			return
		}
	}
	app.errorPage(response, http.StatusAccepted, "Power transition pending", "The controller connection ended while programming the board wake and requesting Linux shutdown. The system may be shutting down; do not repeat the request until power state and controller status have been checked.")
}

func (app *application) dutyCycleDisable(response http.ResponseWriter, request *http.Request) {
	if app.dutyCycle == nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Controller unavailable", "The duty cycle cannot be disabled while the controller is unavailable.")
		return
	}
	form, ok := app.verifiedDutyCycleAction(response, request, "state_revision", "configuration_revision")
	if !ok {
		return
	}
	stateRevision, stateErr := strconv.ParseUint(form.Get("state_revision"), 10, 64)
	configurationRevision, configurationErr := strconv.ParseUint(form.Get("configuration_revision"), 10, 64)
	if stateErr != nil || configurationErr != nil || configurationRevision == 0 {
		app.errorPage(response, http.StatusBadRequest, "Invalid disable request", "The duty-cycle revision was invalid.")
		return
	}
	proposalID, err := newProposalID()
	if err == nil {
		_, err = app.dutyCycle.Disable(request.Context(), proposalID, stateRevision, configurationRevision)
	}
	if err != nil {
		if resolved, lookupErr := app.dutyCycle.ProposalStatus(request.Context(), proposalID); lookupErr != nil || resolved.ConfigurationRevision != configurationRevision {
			app.errorPage(response, http.StatusConflict, "Disable rejected", "The controller rejected disablement or a run transition is active. Reload the current state before trying again.")
			return
		}
	}
	http.Redirect(response, request, "/duty-cycle", http.StatusSeeOther)
}

func (app *application) dutyCycleSimulateBoot(response http.ResponseWriter, request *http.Request) {
	if app.dutyCycle == nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Controller unavailable", "A simulated boot cannot be requested while the controller is unavailable.")
		return
	}
	form, ok := app.verifiedDutyCycleAction(response, request, "state_revision", "configuration_revision")
	if !ok {
		return
	}
	stateRevision, stateErr := strconv.ParseUint(form.Get("state_revision"), 10, 64)
	configurationRevision, configurationErr := strconv.ParseUint(form.Get("configuration_revision"), 10, 64)
	if stateErr != nil || configurationErr != nil || configurationRevision == 0 {
		app.errorPage(response, http.StatusBadRequest, "Invalid simulation request", "The duty-cycle revision was invalid.")
		return
	}
	if _, err := app.dutyCycle.SimulateExpectedBoot(request.Context(), stateRevision, configurationRevision); err != nil {
		app.errorPage(response, http.StatusConflict, "Simulation rejected", "No matching simulated occurrence was ready. Reload the current controller state before trying again.")
		return
	}
	http.Redirect(response, request, "/duty-cycle", http.StatusSeeOther)
}

func (app *application) reviewPageData(ctx context.Context, stateRevision uint64, configuration schedule.Configuration, review schedule.Review, message string) (dutyCycleReviewData, error) {
	token, err := encodeReview(review)
	if err != nil {
		return dutyCycleReviewData{}, err
	}
	maximum := time.Duration(review.MaximumRunSeconds) * time.Second
	minimumLead := time.Duration(review.MinimumLeadSeconds) * time.Second
	var definitions []jobs.Definition
	if app.jobDefinitions != nil {
		if listed, listErr := app.jobDefinitions.ListJobs(ctx); listErr == nil {
			definitions = listed.Jobs
		}
	}
	return dutyCycleReviewData{Title: "Review duty cycle", Current: "duty-cycle", Build: app.build, ReviewToken: token, Message: message, StateRevision: stateRevision, ConfigurationRevision: configuration.Revision, Schedule: scheduleSummary(configuration.Schedule), Occurrences: review.Occurrences, AfterCompletion: configuration.Schedule.Kind == schedule.KindAfterCompletion, RunListJobs: review.RunListJobs, Jobs: reviewJobRows(configuration.RunList, definitions), MaximumRun: maximum.String(), Reference: strconv.FormatUint(uint64(review.Reference), 10), MinimumLead: minimumLead.String()}, nil
}

func reviewJobRows(runList []schedule.RunListEntry, definitions []jobs.Definition) []dutyCycleReviewJob {
	names := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		names[definition.ID] = definition.Name
	}
	rows := make([]dutyCycleReviewJob, 0, len(runList))
	for index, entry := range runList {
		name, resolved := names[entry.JobID]
		if !resolved || strings.TrimSpace(name) == "" {
			name = "Unavailable saved job"
			resolved = false
		}
		rows = append(rows, dutyCycleReviewJob{Position: index + 1, Name: name, Revision: entry.JobRevision, Resolved: resolved})
	}
	return rows
}

func (app *application) verifiedDutyCycleAction(response http.ResponseWriter, request *http.Request, fields ...string) (url.Values, bool) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified.")
		return nil, false
	}
	form, ok := app.parseForm(response, request, append([]string{"csrf"}, fields...)...)
	if !ok {
		return nil, false
	}
	if err := app.authentication.CheckCSRF(sessionFromContext(request.Context()).Token, form.Get("csrf")); err != nil {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified.")
		return nil, false
	}
	return form, true
}

func encodeReview(review schedule.Review) (string, error) {
	encoded, err := json.Marshal(review)
	if err != nil {
		return "", err
	}
	if len(encoded) > controllerapi.MaxFrameBytes/2 {
		return "", fmt.Errorf("review exceeds bound")
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeReview(value string) (schedule.Review, error) {
	if len(value) == 0 || len(value) > controllerapi.MaxFrameBytes {
		return schedule.Review{}, fmt.Errorf("review token exceeds bound")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return schedule.Review{}, err
	}
	var review schedule.Review
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&review); err != nil {
		return schedule.Review{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return schedule.Review{}, fmt.Errorf("review contains trailing data")
	}
	if len(review.Identity) != 64 || len(review.Occurrences) == 0 || len(review.Occurrences) > schedule.MaxPreviewOccurrences {
		return schedule.Review{}, fmt.Errorf("invalid review evidence")
	}
	return review, nil
}

func (app *application) dutyCycleForm(ctx context.Context, base uint64, configuration *schedule.Configuration) dutyCycleFormData {
	data := dutyCycleFormData{Title: "Edit Duty Cycle", Current: "duty-cycle", Build: app.build, BaseRevision: base, ConfigurationRevision: 1, Kind: string(schedule.KindHourly), Timezone: "UTC", Minute: "0", LocalTime: "00:00", DelayMinutes: "60", QuietEnabled: "false", QuietStart: "22:00", QuietEnd: "06:00"}
	selected := map[string]uint64{}
	if configuration != nil {
		data.ConfigurationRevision = configuration.Revision + 1
		if configuration.Schedule.Kind != schedule.KindDisabled {
			data.Kind, data.Timezone, data.Minute = string(configuration.Schedule.Kind), configuration.Schedule.Timezone, strconv.Itoa(configuration.Schedule.Minute)
			data.LocalTime = minuteString(configuration.Schedule.LocalMinute)
			if configuration.Schedule.DelaySeconds > 0 {
				data.DelayMinutes = strconv.FormatInt(configuration.Schedule.DelaySeconds/60, 10)
			}
			data.OneTimeDate = configuration.Schedule.OneTimeDate
		}
		for _, day := range configuration.Schedule.Weekdays {
			setWeekday(&data, day)
		}
		if configuration.Schedule.QuietPeriod != nil {
			data.QuietEnabled = "true"
			data.QuietStart = minuteString(configuration.Schedule.QuietPeriod.StartMinute)
			data.QuietEnd = minuteString(configuration.Schedule.QuietPeriod.EndMinute)
		}
		for _, entry := range configuration.RunList {
			selected[entry.JobID] = entry.JobRevision
		}
	}
	listed, _ := app.jobDefinitions.ListJobs(ctx)
	for _, job := range listed.Jobs {
		data.Jobs = append(data.Jobs, dutyCycleJobChoice{ID: job.ID, Name: job.Name, Revision: job.Revision, Selected: selected[job.ID] == job.Revision, Enabled: job.Enabled})
	}
	return data
}

func (app *application) parseDutyCycleForm(response http.ResponseWriter, request *http.Request) (dutyCycleFormData, schedule.Configuration, bool) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified.")
		return dutyCycleFormData{}, schedule.Configuration{}, false
	}
	request.Body = http.MaxBytesReader(response, request.Body, app.maxBodyBytes)
	if err := request.ParseForm(); err != nil {
		app.errorPage(response, http.StatusBadRequest, "Invalid form", "The submitted form was invalid or too large.")
		return dutyCycleFormData{}, schedule.Configuration{}, false
	}
	allowed := map[string]bool{"csrf": true, "base_revision": true, "configuration_revision": true, "kind": true, "timezone": true, "minute": true, "local_time": true, "weekdays": true, "delay_minutes": true, "one_time_date": true, "quiet_enabled": true, "quiet_start": true, "quiet_end": true, "run_list": true}
	for key := range request.PostForm {
		if !allowed[key] {
			app.errorPage(response, http.StatusBadRequest, "Invalid form", "The submitted form contained unexpected fields.")
			return dutyCycleFormData{}, schedule.Configuration{}, false
		}
	}
	for _, key := range []string{"csrf", "base_revision", "configuration_revision", "kind", "timezone", "minute", "local_time", "delay_minutes", "one_time_date", "quiet_enabled", "quiet_start", "quiet_end"} {
		if len(request.PostForm[key]) != 1 {
			app.errorPage(response, http.StatusBadRequest, "Invalid form", "The submitted form contained missing or repeated fields.")
			return dutyCycleFormData{}, schedule.Configuration{}, false
		}
	}
	if err := app.authentication.CheckCSRF(sessionFromContext(request.Context()).Token, request.PostForm.Get("csrf")); err != nil {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified.")
		return dutyCycleFormData{}, schedule.Configuration{}, false
	}
	base, err1 := strconv.ParseUint(request.PostForm.Get("base_revision"), 10, 64)
	revision, err2 := strconv.ParseUint(request.PostForm.Get("configuration_revision"), 10, 64)
	form := app.dutyCycleForm(request.Context(), base, nil)
	form.CSRFToken = sessionFromContext(request.Context()).CSRFToken
	form.ConfigurationRevision = revision
	form.Kind = request.PostForm.Get("kind")
	form.Timezone = request.PostForm.Get("timezone")
	form.Minute = request.PostForm.Get("minute")
	form.LocalTime = request.PostForm.Get("local_time")
	form.DelayMinutes = request.PostForm.Get("delay_minutes")
	form.OneTimeDate = request.PostForm.Get("one_time_date")
	form.QuietEnabled = request.PostForm.Get("quiet_enabled")
	form.QuietStart = request.PostForm.Get("quiet_start")
	form.QuietEnd = request.PostForm.Get("quiet_end")
	configuration, parseErr := configurationFromValues(revision, request.PostForm)
	validationErr := configuration.Validate()
	if err1 != nil || err2 != nil || revision == 0 || parseErr != nil || validationErr != nil {
		form.Error = "Correct the highlighted schedule information and submit again."
		if fieldErr, ok := parseErr.(*dutyCycleValidationError); ok {
			form.setValidationError(fieldErr)
		} else if parseErr == nil && validationErr != nil {
			form.Error = "The schedule contains conflicting information. Review the selected schedule type and its fields."
		}
		app.renderStatus(response, http.StatusUnprocessableEntity, "duty-cycle-form", form)
		return form, schedule.Configuration{}, false
	}
	return form, configuration, true
}

func configurationFromValues(revision uint64, values url.Values) (schedule.Configuration, error) {
	spec := schedule.Specification{Kind: schedule.Kind(values.Get("kind"))}
	if spec.Kind != schedule.KindHourly && spec.Kind != schedule.KindDaily && spec.Kind != schedule.KindSelectedWeekday && spec.Kind != schedule.KindAfterCompletion && spec.Kind != schedule.KindOneTime && spec.Kind != schedule.KindDisabled {
		return schedule.Configuration{}, validationError("kind", "Choose a supported schedule type.")
	}
	if spec.Kind != schedule.KindDisabled {
		spec.Timezone = values.Get("timezone")
		if spec.Timezone == "" {
			return schedule.Configuration{}, validationError("timezone", "Enter a named timezone such as Europe/London or UTC.")
		}
		if len(spec.Timezone) > 128 || strings.TrimSpace(spec.Timezone) != spec.Timezone {
			return schedule.Configuration{}, validationError("timezone", "Enter a timezone name without leading or trailing spaces.")
		}
		if _, loadErr := time.LoadLocation(spec.Timezone); loadErr != nil {
			return schedule.Configuration{}, validationError("timezone", fmt.Sprintf("Timezone %q is not available. Use an IANA name such as Europe/London or UTC.", spec.Timezone))
		}
	}
	var err error
	switch spec.Kind {
	case schedule.KindHourly:
		spec.Minute, err = strconv.Atoi(values.Get("minute"))
		if err != nil || spec.Minute < 0 || spec.Minute > 59 {
			return schedule.Configuration{}, validationError("minute", "Enter an hourly minute from 0 to 59.")
		}
	case schedule.KindDaily, schedule.KindSelectedWeekday, schedule.KindOneTime:
		spec.LocalMinute, err = parseMinute(values.Get("local_time"))
		if err != nil {
			return schedule.Configuration{}, validationError("local_time", "Choose a local clock time for this schedule.")
		}
	case schedule.KindAfterCompletion:
		var minutes int64
		minutes, err = strconv.ParseInt(values.Get("delay_minutes"), 10, 64)
		if err != nil || minutes <= 0 || minutes > 365*24*60 {
			return schedule.Configuration{}, validationError("delay_minutes", "Enter a delay from 1 minute to 525600 minutes.")
		}
		spec.DelaySeconds = minutes * 60
	}
	if spec.Kind == schedule.KindSelectedWeekday {
		if len(values["weekdays"]) == 0 {
			return schedule.Configuration{}, validationError("weekdays", "Select at least one weekday.")
		}
		for _, value := range values["weekdays"] {
			spec.Weekdays = append(spec.Weekdays, schedule.Weekday(value))
		}
	}
	if spec.Kind == schedule.KindOneTime {
		spec.OneTimeDate = values.Get("one_time_date")
		parsed, dateErr := time.Parse("2006-01-02", spec.OneTimeDate)
		if dateErr != nil || parsed.Format("2006-01-02") != spec.OneTimeDate {
			return schedule.Configuration{}, validationError("one_time_date", "Choose a valid date for the one-time schedule.")
		}
	}
	if values.Get("quiet_enabled") == "true" && spec.Kind != schedule.KindDisabled {
		start, e1 := parseMinute(values.Get("quiet_start"))
		end, e2 := parseMinute(values.Get("quiet_end"))
		if e1 != nil || e2 != nil {
			return schedule.Configuration{}, validationError("quiet", "Choose valid start and end times for the quiet period.")
		}
		if start == end {
			return schedule.Configuration{}, validationError("quiet", "Quiet start and end must be different.")
		}
		spec.QuietPeriod = &schedule.QuietPeriod{StartMinute: start, EndMinute: end}
	}
	configuration := schedule.Configuration{Revision: revision, Schedule: spec}
	for _, value := range values["run_list"] {
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			return schedule.Configuration{}, validationError("run_list", "The selected job reference is invalid. Reload the current configuration.")
		}
		jobRevision, e := strconv.ParseUint(parts[1], 10, 64)
		if e != nil {
			return schedule.Configuration{}, validationError("run_list", "The selected job revision is invalid. Reload the current configuration.")
		}
		configuration.RunList = append(configuration.RunList, schedule.RunListEntry{JobID: parts[0], JobRevision: jobRevision})
	}
	return configuration, nil
}

type dutyCycleValidationError struct {
	Field   string
	Message string
}

func (err *dutyCycleValidationError) Error() string { return err.Message }

func validationError(field, message string) error {
	return &dutyCycleValidationError{Field: field, Message: message}
}

func (data *dutyCycleFormData) setValidationError(err *dutyCycleValidationError) {
	switch err.Field {
	case "kind":
		data.KindError = err.Message
	case "timezone":
		data.TimezoneError = err.Message
	case "minute":
		data.MinuteError = err.Message
	case "local_time":
		data.LocalTimeError = err.Message
	case "delay_minutes":
		data.DelayError = err.Message
	case "one_time_date":
		data.OneTimeDateError = err.Message
	case "weekdays":
		data.WeekdaysError = err.Message
	case "quiet":
		data.QuietError = err.Message
	case "run_list":
		data.RunListError = err.Message
	}
}

func newProposalID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
func parseMinute(value string) (int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, err
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}
func minuteString(value int) string { return fmt.Sprintf("%02d:%02d", value/60, value%60) }
func setWeekday(data *dutyCycleFormData, day schedule.Weekday) {
	switch day {
	case schedule.Monday:
		data.Monday = true
	case schedule.Tuesday:
		data.Tuesday = true
	case schedule.Wednesday:
		data.Wednesday = true
	case schedule.Thursday:
		data.Thursday = true
	case schedule.Friday:
		data.Friday = true
	case schedule.Saturday:
		data.Saturday = true
	case schedule.Sunday:
		data.Sunday = true
	}
}

func phaseLabel(phase schedule.Phase) string {
	if phase == "" {
		return "Inactive"
	}
	return strings.ToUpper(string(phase[:1])) + strings.ReplaceAll(string(phase[1:]), "-", " ")
}

func lifecycleTone(phase schedule.Phase) string {
	switch phase {
	case schedule.PhaseManualSafe:
		return "error"
	case schedule.PhaseSimulatedShutdownIntended, schedule.PhaseExpectedBootObserved, schedule.PhasePreparingRun, schedule.PhaseRunningJob, schedule.PhaseRunListSucceeded:
		return "warning"
	default:
		return "ok"
	}
}

func lifecycleSummary(lifecycle schedule.Lifecycle, kind schedule.Kind) string {
	switch lifecycle.Phase {
	case schedule.PhaseManualSafe:
		return "Automation is disabled and power actions are inhibited until the owner reviews this state."
	case schedule.PhaseSimulatedShutdownIntended:
		return "A simulated occurrence is armed. No wake register will be written and Linux will remain running."
	case schedule.PhaseExpectedBootObserved, schedule.PhasePreparingRun, schedule.PhaseRunningJob:
		if lifecycle.Simulated {
			return "A simulation-only duty cycle is in progress. It cannot program a board wake or shut down Linux."
		}
		if kind == schedule.KindOneTime {
			return "A one-time duty cycle is in progress. Automatic operation will turn off after every scheduled job succeeds."
		}
		return "A duty cycle is in progress. The controller will program the next verified board wake only after every scheduled job succeeds."
	case schedule.PhaseRunListSucceeded:
		if lifecycle.Simulated {
			return "The run list succeeded and the controller is calculating its next simulation-only occurrence."
		}
		if kind == schedule.KindOneTime {
			return "The run list succeeded and the controller is completing the one-time duty cycle."
		}
		return "The run list succeeded and the controller is calculating and verifying the next board wake."
	case schedule.PhaseCompleted:
		if lifecycle.Simulated {
			return "The one-time simulation completed and automatic operation is off."
		}
		return "The one-time duty cycle completed and automatic operation is off."
	default:
		return "No automatic or simulated duty-cycle transition is active."
	}
}

func canDisableLifecycle(phase schedule.Phase) bool {
	switch phase {
	case schedule.PhaseExpectedBootObserved, schedule.PhasePreparingRun, schedule.PhaseRunningJob, schedule.PhaseRunListSucceeded:
		return false
	default:
		return true
	}
}

func scheduleSummary(specification schedule.Specification) string {
	switch specification.Kind {
	case schedule.KindHourly:
		return fmt.Sprintf("Hourly at minute %02d in %s", specification.Minute, specification.Timezone)
	case schedule.KindDaily:
		return fmt.Sprintf("Daily at %02d:%02d in %s", specification.LocalMinute/60, specification.LocalMinute%60, specification.Timezone)
	case schedule.KindSelectedWeekday:
		return fmt.Sprintf("Selected weekdays at %02d:%02d in %s", specification.LocalMinute/60, specification.LocalMinute%60, specification.Timezone)
	case schedule.KindAfterCompletion:
		return fmt.Sprintf("%s after the complete ordered run list succeeds", (time.Duration(specification.DelaySeconds) * time.Second).String())
	case schedule.KindOneTime:
		return fmt.Sprintf("Once on %s at %02d:%02d in %s", specification.OneTimeDate, specification.LocalMinute/60, specification.LocalMinute%60, specification.Timezone)
	default:
		return "No schedule configured"
	}
}

func formatOccurrence(unix uint32, configuration *schedule.Configuration) string {
	location := time.UTC
	if configuration != nil && configuration.Schedule.Timezone != "" {
		if loaded, err := time.LoadLocation(configuration.Schedule.Timezone); err == nil {
			location = loaded
		}
	}
	return time.Unix(int64(unix), 0).In(location).Format("2006-01-02 15:04:05 MST")
}
