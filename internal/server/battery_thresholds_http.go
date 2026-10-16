package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/registeredit"
)

var batteryThresholdNames = []board.Register{"VBAT_MIN", "VBAT_SHDN", "VBAT_BOOT"}

type batteryThresholdRow struct {
	Name                 string
	Key                  string
	Title                string
	ExpectedValue        string
	CurrentValue         string
	ProposedValueInteger string
	ProposedValue        string
	ProposedInput        string
	Minimum              string
	Maximum              string
	ActualValue          string
	Changed              bool
}

type batteryThresholdEditData struct {
	registerShellData
	Rows []batteryThresholdRow
}

type batteryThresholdReviewData struct {
	registerShellData
	Rows        []batteryThresholdRow
	ReviewToken string
}

type batteryThresholdResultData struct {
	registerShellData
	Heading string
	Tone    string
	Summary string
	Outcome string
	Rows    []batteryThresholdRow
}

func (app *application) batteryThresholdEdit(response http.ResponseWriter, request *http.Request) {
	before, err := app.batchRegisterEditor.ReadBatteryThresholds(request.Context())
	if err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Battery thresholds unavailable", "All three scaled threshold values must be readable before staging a change.")
		return
	}
	if !(before["VBAT_MIN"] < before["VBAT_SHDN"] && before["VBAT_SHDN"] < before["VBAT_BOOT"]) {
		app.errorPage(response, http.StatusConflict, "Battery thresholds need inspection", "The observed threshold configuration does not satisfy VBAT_MIN < VBAT_SHDN < VBAT_BOOT, so temporary editing is locked.")
		return
	}
	session := sessionFromContext(request.Context())
	app.render(response, "battery-threshold-edit", batteryThresholdEditData{
		registerShellData: registerShellData{Title: "Battery thresholds", Current: "registers", Build: app.build, CSRFToken: session.CSRFToken},
		Rows:              batteryThresholdRows(app.registerService, before, before, nil),
	})
}

func (app *application) batteryThresholdReview(response http.ResponseWriter, request *http.Request) {
	proposal, proposedEngineering, ok := app.parseBatteryThresholdProposal(response, request, false)
	if !ok {
		return
	}
	plan, err := app.batchRegisterEditor.ReviewBatteryThresholds(proposal)
	if err != nil {
		app.errorPage(response, http.StatusUnprocessableEntity, "Battery thresholds are not valid", "The complete proposed configuration must satisfy VBAT_MIN < VBAT_SHDN < VBAT_BOOT and use values representable by the vendor interface.")
		return
	}
	session := sessionFromContext(request.Context())
	data := batteryThresholdReviewData{
		registerShellData: registerShellData{Title: "Review battery thresholds", Current: "registers", Build: app.build, CSRFToken: session.CSRFToken},
		Rows:              batteryThresholdRows(app.registerService, plan.Before, plan.After, proposedEngineering),
	}
	data.ReviewToken = batteryThresholdReviewToken(session.Token, proposal.ExpectedValues, proposedEngineering)
	app.render(response, "battery-threshold-review", data)
}

func (app *application) batteryThresholdApply(response http.ResponseWriter, request *http.Request) {
	proposal, proposedEngineering, ok := app.parseBatteryThresholdProposal(response, request, true)
	if !ok {
		return
	}
	result, err := app.batchRegisterEditor.ApplyBatteryThresholds(request.Context(), proposal)
	app.registerService.Invalidate()
	session := sessionFromContext(request.Context())
	data := batteryThresholdResultData{
		registerShellData: registerShellData{Title: "Battery-threshold result", Current: "registers", Build: app.build, CSRFToken: session.CSRFToken},
		Outcome:           string(result.Outcome),
		Rows:              batteryThresholdResultRows(app.registerService, proposal.ExpectedValues, proposal.ProposedValues, proposedEngineering, result.Fresh, result.Actual),
	}
	status := http.StatusOK
	switch {
	case err == nil && result.Outcome == registeredit.BatchOutcomeApplied:
		data.Heading, data.Tone, data.Summary = "Temporary thresholds verified", "ok", "Every changed threshold was written in a safe order and read back successfully. Nothing was committed to flash."
	case errors.Is(err, registeredit.ErrBatchStale):
		status = http.StatusConflict
		data.Heading, data.Tone, data.Summary = "Battery thresholds changed", "warning", "Nothing was written because the fresh complete threshold configuration no longer matched the configuration you reviewed."
	case errors.Is(err, registeredit.ErrBatchUncertain):
		status = http.StatusServiceUnavailable
		data.Heading, data.Tone, data.Summary = "Outcome uncertain", "error", "Communication was lost and the complete actual threshold configuration could not be recovered. Do not commit to flash; inspect the board before further changes."
	case errors.Is(err, registeredit.ErrBatchFailed):
		status = http.StatusBadGateway
		data.Heading, data.Tone = "Threshold change did not complete cleanly", "warning"
		switch result.Outcome {
		case registeredit.BatchOutcomeRolledBack:
			data.Summary = "A write failed, and the reviewed starting configuration was restored and re-read successfully."
		case registeredit.BatchOutcomeReconciledApplied:
			data.Summary = "The device reported an error, but a complete reconciliation read observed the proposed configuration. Inspect the board before further changes."
		default:
			data.Summary = "A write failed. The complete observed configuration is shown where it could be recovered; inspect the board before further changes."
		}
	default:
		status = http.StatusUnprocessableEntity
		data.Heading, data.Tone, data.Summary = "Threshold change rejected", "error", "The request was rejected before a verified temporary configuration completed."
	}
	app.renderStatus(response, status, "battery-threshold-result", data)
}

func (app *application) parseBatteryThresholdProposal(response http.ResponseWriter, request *http.Request, requireReview bool) (registeredit.BatchProposal, map[board.Register]int64, bool) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return registeredit.BatchProposal{}, nil, false
	}
	fields := []string{"csrf"}
	for _, register := range batteryThresholdNames {
		key := strings.ToLower(string(register))
		fields = append(fields, "expected_"+key, "proposed_"+key)
	}
	if requireReview {
		fields = append(fields, "review_token")
	}
	form, ok := app.parseForm(response, request, fields...)
	if !ok {
		return registeredit.BatchProposal{}, nil, false
	}
	session := sessionFromContext(request.Context())
	if err := app.authentication.CheckCSRF(session.Token, form.Get("csrf")); err != nil {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return registeredit.BatchProposal{}, nil, false
	}
	expected := make(map[board.Register]int64, len(batteryThresholdNames))
	proposed := make(map[board.Register]int64, len(batteryThresholdNames))
	proposedEngineering := make(map[board.Register]int64, len(batteryThresholdNames))
	for _, register := range batteryThresholdNames {
		key := strings.ToLower(string(register))
		expectedValue, expectedErr := strconv.ParseInt(form.Get("expected_"+key), 10, 64)
		engineering, proposedErr := strconv.ParseInt(form.Get("proposed_"+key), 10, 64)
		definition, exists := app.registerService.Definition(register)
		if expectedErr != nil || proposedErr != nil || !exists {
			app.errorPage(response, http.StatusBadRequest, "Invalid battery thresholds", "Every reviewed and proposed threshold must be a valid integer.")
			return registeredit.BatchProposal{}, nil, false
		}
		minimum, minimumErr := definition.EngineeringValue(*definition.RawMinimum)
		maximum, maximumErr := definition.EngineeringValue(*definition.RawMaximum)
		if minimumErr != nil || maximumErr != nil || engineering < minimum || engineering > maximum || expectedValue < minimum || expectedValue > maximum {
			app.errorPage(response, http.StatusUnprocessableEntity, "Battery threshold cannot be represented", "Use whole millivolt values within the ranges shown on the form.")
			return registeredit.BatchProposal{}, nil, false
		}
		expected[register], proposed[register], proposedEngineering[register] = expectedValue, engineering, engineering
	}
	if requireReview {
		want := batteryThresholdReviewToken(session.Token, expected, proposedEngineering)
		if !hmac.Equal([]byte(form.Get("review_token")), []byte(want)) {
			app.errorPage(response, http.StatusForbidden, "Review expired or changed", "Return to the battery-threshold page and review the complete temporary configuration again.")
			return registeredit.BatchProposal{}, nil, false
		}
	}
	return registeredit.BatchProposal{RegisterSet: 7, ExpectedValues: expected, ProposedValues: proposed}, proposedEngineering, true
}

type registerDefinitionService interface {
	Definition(board.Register) (board.Definition, bool)
}

func batteryThresholdRows(service registerDefinitionService, before, after, proposedEngineering map[board.Register]int64) []batteryThresholdRow {
	rows := make([]batteryThresholdRow, 0, len(batteryThresholdNames))
	for _, register := range batteryThresholdNames {
		definition, _ := service.Definition(register)
		current := before[register]
		proposed := proposedEngineering[register]
		if proposedEngineering == nil {
			proposed = after[register]
		}
		minimum, _ := definition.EngineeringValue(*definition.RawMinimum)
		maximum, _ := definition.EngineeringValue(*definition.RawMaximum)
		rows = append(rows, batteryThresholdRow{Name: string(register), Key: strings.ToLower(string(register)), Title: definition.Title, ExpectedValue: strconv.FormatInt(before[register], 10), CurrentValue: fmt.Sprintf("%d %s", current, definition.Unit), ProposedValueInteger: strconv.FormatInt(after[register], 10), ProposedValue: fmt.Sprintf("%d %s", proposed, definition.Unit), ProposedInput: strconv.FormatInt(proposed, 10), Minimum: strconv.FormatInt(minimum, 10), Maximum: strconv.FormatInt(maximum, 10), Changed: before[register] != after[register]})
	}
	return rows
}

func batteryThresholdResultRows(service registerDefinitionService, before, after, proposedEngineering, fresh, actual map[board.Register]int64) []batteryThresholdRow {
	rows := batteryThresholdRows(service, before, after, proposedEngineering)
	for index, register := range batteryThresholdNames {
		rows[index].CurrentValue = optionalMapValue(fresh, register)
		rows[index].ActualValue = optionalMapValue(actual, register)
	}
	return rows
}

func optionalMapValue(values map[board.Register]int64, register board.Register) string {
	value, exists := values[register]
	if !exists {
		return "Unknown"
	}
	return strconv.FormatInt(value, 10)
}

func batteryThresholdReviewToken(sessionToken string, expected, proposedEngineering map[board.Register]int64) string {
	digest := hmac.New(sha256.New, []byte(sessionToken))
	for _, register := range batteryThresholdNames {
		_, _ = fmt.Fprintf(digest, "%s\x00%d\x00%d\x00", register, expected[register], proposedEngineering[register])
	}
	return base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
}
