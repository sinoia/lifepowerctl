package server

import (
	"crypto/hmac"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/sinoia/lifepowerctl/internal/registeredit"
	"github.com/sinoia/lifepowerctl/internal/registers"
)

type shutdownDelayData struct {
	registerShellData
	CurrentTicks         string
	CurrentSeconds       string
	ProposedInputSeconds string
	ReviewToken          string
	ProposedTicks        string
	ProposedSeconds      string
}

func (app *application) shutdownDelayEdit(response http.ResponseWriter, request *http.Request) {
	snapshot := app.registerService.Refresh(request.Context())
	if snapshot.Identity.RegisterSet != 7 || (snapshot.State != registers.StateSupported && snapshot.State != registers.StatePartial) {
		app.errorPage(response, http.StatusServiceUnavailable, "Shutdown delay unavailable", "A supported board and fresh SHDN_DELAY value are required before staging a change.")
		return
	}
	current, ok := shutdownDelayRaw(snapshot)
	if !ok {
		app.errorPage(response, http.StatusServiceUnavailable, "Shutdown delay unavailable", "The exact SHDN_DELAY tick value could not be read.")
		return
	}
	session := sessionFromContext(request.Context())
	data := shutdownDelayData{
		registerShellData: registerShellData{Title: "Shutdown delay", Current: "registers", Build: app.build, CSRFToken: session.CSRFToken},
		CurrentTicks:      strconv.FormatInt(current, 10), CurrentSeconds: formatShutdownDelaySeconds(current), ProposedInputSeconds: formatShutdownDelaySeconds(current),
	}
	app.render(response, "shutdown-delay-edit", data)
}

func (app *application) shutdownDelayReview(response http.ResponseWriter, request *http.Request) {
	proposal, ok := app.parseShutdownDelayProposal(response, request, false)
	if !ok {
		return
	}
	validated, err := app.shutdownDelayEditor.ValidateShutdownDelay(7, proposal)
	if err != nil {
		app.errorPage(response, http.StatusUnprocessableEntity, "Shutdown delay is not valid", "Choose a whole-second delay from 5 through 15 seconds that differs from the current value.")
		return
	}
	session := sessionFromContext(request.Context())
	data := shutdownDelayData{
		registerShellData: registerShellData{Title: "Review shutdown delay", Current: "registers", Build: app.build, CSRFToken: session.CSRFToken},
		CurrentTicks:      strconv.FormatInt(validated.ExpectedRaw, 10), CurrentSeconds: formatShutdownDelaySeconds(validated.ExpectedRaw),
		ProposedTicks: strconv.FormatInt(validated.ProposedRaw, 10), ProposedSeconds: formatShutdownDelaySeconds(validated.ProposedRaw),
	}
	data.ReviewToken = registerReviewToken(session.Token, "SHDN_DELAY", validated.ExpectedRaw, validated.ProposedRaw)
	app.render(response, "shutdown-delay-review", data)
}

func (app *application) shutdownDelayApply(response http.ResponseWriter, request *http.Request) {
	proposal, ok := app.parseShutdownDelayProposal(response, request, true)
	if !ok {
		return
	}
	result, err := app.shutdownDelayEditor.ApplyShutdownDelayTemporary(request.Context(), 7, proposal)
	app.registerService.Invalidate()
	session := sessionFromContext(request.Context())
	data := registerResultData{
		registerShellData: registerShellData{Title: "Shutdown-delay result", Current: "registers", Build: app.build, CSRFToken: session.CSRFToken},
		Name:              "SHDN_DELAY", Outcome: string(result.Outcome), ExpectedRaw: strconv.FormatInt(result.ExpectedRaw, 10), ProposedRaw: strconv.FormatInt(result.ProposedRaw, 10), FreshRaw: optionalRaw(result.FreshRaw), ActualRaw: optionalRaw(result.ActualRaw),
	}
	status := http.StatusOK
	switch {
	case err == nil && result.Outcome == registeredit.OutcomeApplied:
		data.Heading, data.Tone, data.Summary = "Temporary shutdown delay verified", "ok", "The exact tick value was written and read back successfully. It has not been committed to flash."
	case errors.Is(err, registeredit.ErrStaleState):
		status = http.StatusConflict
		data.Heading, data.Tone, data.Summary = "Shutdown delay changed", "warning", "Nothing was written because the fresh tick value no longer matched the value you reviewed."
	case errors.Is(err, registeredit.ErrUncertain):
		status = http.StatusServiceUnavailable
		data.Heading, data.Tone, data.Summary = "Outcome uncertain", "error", "Communication was lost and the actual tick value could not be recovered. Do not commit to flash; refresh and inspect the board."
	case errors.Is(err, registeredit.ErrMutationFailed):
		status = http.StatusBadGateway
		data.Heading, data.Tone = "Shutdown-delay change did not complete cleanly", "warning"
		if result.Outcome == registeredit.OutcomeRolledBack {
			data.Summary = "The write did not complete cleanly, and the reviewed starting value was restored and read back successfully."
		} else {
			data.Summary = "The write reported an error. The observed actual value is shown where reconciliation succeeded; inspect the board before another change."
		}
	default:
		status = http.StatusUnprocessableEntity
		data.Heading, data.Tone, data.Summary = "Shutdown-delay change rejected", "error", "The request was rejected before a verified temporary change completed."
	}
	app.renderStatus(response, status, "shutdown-delay-result", data)
}

func (app *application) parseShutdownDelayProposal(response http.ResponseWriter, request *http.Request, requireReview bool) (registeredit.Proposal, bool) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return registeredit.Proposal{}, false
	}
	fields := []string{"csrf", "expected_ticks", "proposed_seconds"}
	if requireReview {
		fields = append(fields, "review_token")
	}
	form, ok := app.parseForm(response, request, fields...)
	if !ok {
		return registeredit.Proposal{}, false
	}
	session := sessionFromContext(request.Context())
	if err := app.authentication.CheckCSRF(session.Token, form.Get("csrf")); err != nil {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return registeredit.Proposal{}, false
	}
	expected, expectedErr := strconv.ParseInt(form.Get("expected_ticks"), 10, 64)
	proposedSeconds, proposedErr := strconv.ParseInt(form.Get("proposed_seconds"), 10, 64)
	if expectedErr != nil || proposedErr != nil {
		app.errorPage(response, http.StatusBadRequest, "Invalid shutdown delay", "The reviewed and proposed tick values must be valid integers.")
		return registeredit.Proposal{}, false
	}
	if proposedSeconds < 5 || proposedSeconds > 15 {
		app.errorPage(response, http.StatusUnprocessableEntity, "Shutdown delay is not valid", "Choose a whole-second delay from 5 through 15 seconds.")
		return registeredit.Proposal{}, false
	}
	proposed := proposedSeconds * registeredit.ShutdownDelayTicksPerSecond
	if requireReview {
		want := registerReviewToken(session.Token, "SHDN_DELAY", expected, proposed)
		if !hmac.Equal([]byte(form.Get("review_token")), []byte(want)) {
			app.errorPage(response, http.StatusForbidden, "Review expired or changed", "Return to the shutdown-delay page and review the temporary change again.")
			return registeredit.Proposal{}, false
		}
	}
	return registeredit.Proposal{Register: "SHDN_DELAY", ExpectedRaw: expected, ProposedEngineering: proposed}, true
}

func shutdownDelayRaw(snapshot registers.Snapshot) (int64, bool) {
	for _, item := range snapshot.Items {
		if item.Definition.Register == "SHDN_DELAY" && item.Reading != nil && item.Reading.RawValue != nil {
			return *item.Reading.RawValue, true
		}
	}
	return 0, false
}

func formatShutdownDelaySeconds(ticks int64) string {
	whole, remainder := ticks/registeredit.ShutdownDelayTicksPerSecond, ticks%registeredit.ShutdownDelayTicksPerSecond
	if remainder == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return fmt.Sprintf("%d.%03d", whole, remainder*125)
}
