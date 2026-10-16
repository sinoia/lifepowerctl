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
	"time"

	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/registeredit"
	"github.com/sinoia/lifepowerctl/internal/registers"
)

type registerShellData struct {
	Title     string
	Current   string
	Build     BuildInfo
	CSRFToken string
}

type registerListData struct {
	registerShellData
	Tone        string
	Label       string
	Summary     string
	RegisterSet string
	CheckedAt   string
	Groups      []registerGroupData
}

type registerGroupData struct {
	Name  string
	Items []registerRowData
}

type registerRowData struct {
	Name        string
	Title       string
	Value       string
	Access      string
	Persistence string
	Available   bool
}

type registerDetailData struct {
	registerShellData
	Name             string
	RegisterTitle    string
	Purpose          string
	Behavior         string
	SafetyGuidance   string
	Value            string
	Choice           string
	RawValue         string
	Address          string
	Width            string
	Access           string
	Persistence      string
	SafetyClass      string
	Owner            string
	DefaultValue     string
	DefaultRaw       string
	DefaultNote      string
	Source           string
	Available        bool
	Failure          string
	InterpretedUTC   string
	InterpretedLocal string
	Editable         bool
	EditReason       string
	ExpectedRaw      string
	EditChoices      []registerEditChoice
}

type registerEditChoice struct {
	Value string
	Label string
}

type registerReviewData struct {
	registerShellData
	Name                string
	RegisterTitle       string
	CurrentValue        string
	CurrentRaw          string
	ProposedValue       string
	ProposedRaw         string
	ProposedEngineering string
	ReviewToken         string
	Behavior            string
	SafetyGuidance      string
}

type registerResultData struct {
	registerShellData
	Name        string
	Heading     string
	Tone        string
	Summary     string
	Outcome     string
	ExpectedRaw string
	ProposedRaw string
	FreshRaw    string
	ActualRaw   string
}

func (app *application) registerList(response http.ResponseWriter, request *http.Request) {
	session := sessionFromContext(request.Context())
	snapshot := app.registerService.Refresh(request.Context())
	data := registerListPageData(snapshot)
	data.registerShellData = registerShellData{Title: "Registers", Current: "registers", Build: app.build, CSRFToken: session.CSRFToken}
	app.render(response, "register-list", data)
}

func (app *application) registerDetail(response http.ResponseWriter, request *http.Request) {
	register := board.Register(request.PathValue("register"))
	definition, exists := app.registerService.Definition(register)
	if !exists {
		app.errorPage(response, http.StatusNotFound, "Register not found", "The requested register is not in the reviewed catalogue.")
		return
	}
	snapshot := app.registerService.Refresh(request.Context())
	if snapshot.Identity.RegisterSet != definition.RegisterSet || (snapshot.State != registers.StateSupported && snapshot.State != registers.StatePartial) {
		app.registerList(response, request)
		return
	}
	var item registers.Item
	for _, candidate := range snapshot.Items {
		if candidate.Definition.Register == register {
			item = candidate
			break
		}
	}
	session := sessionFromContext(request.Context())
	data := registerDetailPageData(definition, item)
	data.registerShellData = registerShellData{Title: string(register), Current: "registers", Build: app.build, CSRFToken: session.CSRFToken}
	app.render(response, "register-detail", data)
}

func (app *application) registerReview(response http.ResponseWriter, request *http.Request) {
	definition, proposal, ok := app.parseRegisterProposal(response, request, false)
	if !ok {
		return
	}
	validated, err := app.registerEditor.Validate(definition.RegisterSet, proposal)
	if err != nil {
		app.errorPage(response, http.StatusUnprocessableEntity, "Temporary change is not valid", "Return to the register page and choose one of the available values.")
		return
	}
	session := sessionFromContext(request.Context())
	data := registerReviewData{
		registerShellData: registerShellData{Title: "Review " + string(definition.Register), Current: "registers", Build: app.build, CSRFToken: session.CSRFToken},
		Name:              string(definition.Register), RegisterTitle: definition.Title,
		CurrentValue: formatRawDefinition(definition, validated.ExpectedRaw), CurrentRaw: strconv.FormatInt(validated.ExpectedRaw, 10),
		ProposedValue: formatRawDefinition(definition, validated.ProposedRaw), ProposedRaw: strconv.FormatInt(validated.ProposedRaw, 10),
		ProposedEngineering: strconv.FormatInt(validated.ProposedEngineering, 10), Behavior: definition.Behavior, SafetyGuidance: definition.SafetyGuidance,
	}
	data.ReviewToken = registerReviewToken(session.Token, definition.Register, validated.ExpectedRaw, validated.ProposedEngineering)
	app.render(response, "register-review", data)
}

func (app *application) registerApply(response http.ResponseWriter, request *http.Request) {
	definition, proposal, ok := app.parseRegisterProposal(response, request, true)
	if !ok {
		return
	}
	result, err := app.registerEditor.ApplyTemporary(request.Context(), definition.RegisterSet, proposal)
	app.registerService.Invalidate()
	session := sessionFromContext(request.Context())
	data := registerResultData{
		registerShellData: registerShellData{Title: "Temporary change result", Current: "registers", Build: app.build, CSRFToken: session.CSRFToken},
		Name:              string(definition.Register), Outcome: string(result.Outcome), ExpectedRaw: strconv.FormatInt(result.ExpectedRaw, 10), ProposedRaw: strconv.FormatInt(result.ProposedRaw, 10),
		FreshRaw: optionalRaw(result.FreshRaw), ActualRaw: optionalRaw(result.ActualRaw),
	}
	status := http.StatusOK
	switch {
	case err == nil && result.Outcome == registeredit.OutcomeApplied:
		data.Heading, data.Tone, data.Summary = "Temporary change verified", "ok", "The proposed value was written and read back successfully. It has not been committed to flash."
	case errors.Is(err, registeredit.ErrStaleState):
		status = http.StatusConflict
		data.Heading, data.Tone, data.Summary = "Board value changed", "warning", "Nothing was written because the fresh board value no longer matched the value you reviewed."
	case errors.Is(err, registeredit.ErrUncertain):
		status = http.StatusServiceUnavailable
		data.Heading, data.Tone, data.Summary = "Outcome uncertain", "error", "Communication was lost and the actual board value could not be recovered. Do not commit to flash; refresh and inspect the board before further changes."
	case errors.Is(err, registeredit.ErrMutationFailed):
		status = http.StatusBadGateway
		data.Heading, data.Tone = "Change did not complete cleanly", "warning"
		if result.Outcome == registeredit.OutcomeReconciledProposed {
			data.Summary = "The write reported an error, but a reconciliation read observed the proposed value. Treat the operation as exceptional and inspect the board before further changes."
		} else {
			data.Summary = "The write reported an error. A reconciliation read recovered the actual value shown below."
		}
	default:
		status = http.StatusUnprocessableEntity
		data.Heading, data.Tone, data.Summary = "Temporary change rejected", "error", "The request was rejected before a verified temporary change completed."
	}
	app.renderStatus(response, status, "register-result", data)
}

func (app *application) parseRegisterProposal(response http.ResponseWriter, request *http.Request, requireReview bool) (board.Definition, registeredit.Proposal, bool) {
	register := board.Register(request.PathValue("register"))
	definition, exists := app.registerService.Definition(register)
	if !exists {
		app.errorPage(response, http.StatusNotFound, "Register not found", "The requested register is not in the reviewed catalogue.")
		return board.Definition{}, registeredit.Proposal{}, false
	}
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return board.Definition{}, registeredit.Proposal{}, false
	}
	fields := []string{"csrf", "expected_raw", "proposed_value"}
	if requireReview {
		fields = append(fields, "review_token")
	}
	form, ok := app.parseForm(response, request, fields...)
	if !ok {
		return board.Definition{}, registeredit.Proposal{}, false
	}
	session := sessionFromContext(request.Context())
	if err := app.authentication.CheckCSRF(session.Token, form.Get("csrf")); err != nil {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return board.Definition{}, registeredit.Proposal{}, false
	}
	expected, expectedErr := strconv.ParseInt(form.Get("expected_raw"), 10, 64)
	proposed, proposedErr := strconv.ParseInt(form.Get("proposed_value"), 10, 64)
	if expectedErr != nil || proposedErr != nil {
		app.errorPage(response, http.StatusBadRequest, "Invalid temporary change", "The submitted values were not valid integers.")
		return board.Definition{}, registeredit.Proposal{}, false
	}
	if requireReview {
		want := registerReviewToken(session.Token, register, expected, proposed)
		if !hmac.Equal([]byte(form.Get("review_token")), []byte(want)) {
			app.errorPage(response, http.StatusForbidden, "Review expired or changed", "Return to the register page and review the temporary change again.")
			return board.Definition{}, registeredit.Proposal{}, false
		}
	}
	return definition, registeredit.Proposal{Register: register, ExpectedRaw: expected, ProposedEngineering: proposed}, true
}

func registerReviewToken(sessionToken string, register board.Register, expectedRaw, proposedEngineering int64) string {
	digest := hmac.New(sha256.New, []byte(sessionToken))
	_, _ = fmt.Fprintf(digest, "%s\x00%d\x00%d", register, expectedRaw, proposedEngineering)
	return base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
}

func registerListPageData(snapshot registers.Snapshot) registerListData {
	data := registerListData{Tone: "warning", Label: "Checking required", Summary: "No compatible register catalogue is available.", RegisterSet: "not detected", CheckedAt: formatStatusTime(snapshot.CheckedAt)}
	if snapshot.Identity.RegisterSet > 0 {
		data.RegisterSet = strconv.Itoa(snapshot.Identity.RegisterSet)
	}
	switch snapshot.State {
	case registers.StateSupported:
		data.Tone, data.Label, data.Summary = "ok", "Complete", "All reviewed register values were read successfully."
	case registers.StatePartial:
		data.Tone, data.Label = "warning", "Partial"
		data.Summary = fmt.Sprintf("%d values were read; %d could not be read. Available values are retained.", snapshot.Successful, snapshot.Failed)
	case registers.StateUnsupported:
		data.Tone, data.Label, data.Summary = "error", "Unsupported", "This register set has no reviewed catalogue. No register reads were attempted."
	case registers.StateUnavailable:
		data.Tone, data.Label, data.Summary = "error", "Unavailable", "The board or vendor service could not provide a compatible register set."
	}
	if snapshot.State != registers.StateSupported && snapshot.State != registers.StatePartial {
		return data
	}
	groupIndexes := make(map[string]int)
	for _, item := range snapshot.Items {
		group := registerGroup(item.Definition.Register)
		index, exists := groupIndexes[group]
		if !exists {
			index = len(data.Groups)
			groupIndexes[group] = index
			data.Groups = append(data.Groups, registerGroupData{Name: group})
		}
		row := registerRowData{Name: string(item.Definition.Register), Title: item.Definition.Title, Access: string(item.Definition.Access), Persistence: string(item.Definition.Persistence)}
		if item.Reading != nil {
			row.Available = true
			row.Value = formatReading(*item.Reading)
		} else {
			row.Value = "Unavailable (" + string(item.Failure) + ")"
		}
		data.Groups[index].Items = append(data.Groups[index].Items, row)
	}
	return data
}

func registerDetailPageData(definition board.Definition, item registers.Item) registerDetailData {
	pages := make([]string, len(definition.Source.Pages))
	for index, page := range definition.Source.Pages {
		pages[index] = strconv.Itoa(page)
	}
	data := registerDetailData{
		Name: string(definition.Register), RegisterTitle: definition.Title, Purpose: definition.Purpose,
		Behavior: definition.Behavior, SafetyGuidance: definition.SafetyGuidance,
		Address: fmt.Sprintf("0x%02X", definition.Address), Width: fmt.Sprintf("%d byte", definition.WidthBytes),
		Access: string(definition.Access), Persistence: string(definition.Persistence), SafetyClass: string(definition.SafetyClass), Owner: string(definition.Owner),
		Source: fmt.Sprintf("%s, revision %s, page%s %s", definition.Source.Document, definition.Source.Revision, plural(len(pages)), strings.Join(pages, ", ")),
	}
	data.DefaultValue, data.DefaultRaw, data.DefaultNote = formatDefault(definition)
	if item.Reading == nil {
		data.Value, data.RawValue, data.Failure, data.EditReason = "Unavailable", "Unavailable", string(item.Failure), "A fresh readable value is required before editing."
		return data
	}
	data.Available = true
	data.Value = formatReading(*item.Reading)
	data.InterpretedUTC, data.InterpretedLocal = interpretReading(*item.Reading)
	data.Choice = item.Reading.Choice
	if item.Reading.RawValue == nil {
		data.RawValue = "Not recoverable exactly from the scaled vendor CLI value"
		data.EditReason = "An exact raw value is required before editing."
	} else {
		data.RawValue = strconv.FormatInt(*item.Reading.RawValue, 10)
		eligibility := registeredit.EditEligibility(definition)
		data.Editable, data.EditReason = eligibility.Editable, eligibility.Reason
		if data.Editable {
			data.ExpectedRaw = data.RawValue
			for _, choice := range definition.Choices {
				engineering, err := definition.EngineeringValue(choice.Value)
				if err == nil && choice.Value != *item.Reading.RawValue {
					data.EditChoices = append(data.EditChoices, registerEditChoice{Value: strconv.FormatInt(engineering, 10), Label: registerChoiceLabel(definition.Register, choice.Value, choice.Name)})
				}
			}
			if len(data.EditChoices) == 0 {
				data.Editable, data.EditReason = false, "No alternative reviewed value is available."
			}
		}
	}
	return data
}

func formatRawDefinition(definition board.Definition, raw int64) string {
	engineering, err := definition.EngineeringValue(raw)
	if err != nil {
		return "Unavailable"
	}
	reading := board.Reading{Definition: definition, EngineeringValue: engineering, RawValue: &raw}
	for _, choice := range definition.Choices {
		if choice.Value == raw {
			reading.Choice = registerChoiceLabel(definition.Register, choice.Value, choice.Name)
			break
		}
	}
	return formatReading(reading)
}

func optionalRaw(value *int64) string {
	if value == nil {
		return "Unknown"
	}
	return strconv.FormatInt(*value, 10)
}

func interpretReading(reading board.Reading) (utc, local string) {
	if reading.Definition.Interpretation != board.InterpretationUnixTime {
		return "", ""
	}
	if reading.Sentinel {
		return "Disabled — no absolute wake scheduled", ""
	}
	instant := time.Unix(reading.EngineeringValue, 0)
	return instant.UTC().Format("2006-01-02 15:04:05 UTC"), instant.Local().Format("2006-01-02 15:04:05 MST")
}

func formatDefault(definition board.Definition) (value, raw, note string) {
	switch definition.DefaultKind {
	case board.DefaultNumeric:
		if definition.RawDefault == nil {
			return "Unavailable", "Unavailable", "The catalogue has no numeric default for this entry."
		}
		engineering, err := definition.EngineeringValue(*definition.RawDefault)
		if err != nil {
			return "Unavailable", "Unavailable", "The documented default could not be converted using the reviewed scale."
		}
		reading := board.Reading{Definition: definition, EngineeringValue: engineering, RawValue: definition.RawDefault}
		for _, choice := range definition.Choices {
			if choice.Value == *definition.RawDefault {
				reading.Choice = registerChoiceLabel(definition.Register, choice.Value, choice.Name)
				break
			}
		}
		for _, sentinel := range definition.SentinelValues {
			if sentinel == *definition.RawDefault {
				reading.Sentinel = true
				break
			}
		}
		return formatReading(reading), strconv.FormatInt(*definition.RawDefault, 10), "Manufacturer-documented default; this is not a reading of the last flash-saved value."
	case board.DefaultFactory:
		return "Factory calibrated", "Board-specific", "There is no universal numeric default; the value was established for this individual board."
	case board.DefaultDynamic:
		return "Established at runtime", "Dynamic", "This operational value does not have a fixed configuration default."
	default:
		return "Not specified", "Not specified", "The reviewed manufacturer source does not state a default value."
	}
}

func formatReading(reading board.Reading) string {
	value := strconv.FormatInt(reading.EngineeringValue, 10)
	if reading.Definition.Unit != "" {
		value += " " + reading.Definition.Unit
	}
	choice := reading.Choice
	if reading.RawValue != nil {
		for _, candidate := range reading.Definition.Choices {
			if candidate.Value == *reading.RawValue {
				choice = registerChoiceLabel(reading.Definition.Register, candidate.Value, candidate.Name)
				break
			}
		}
	}
	if choice != "" {
		value += " (" + choice + ")"
	}
	if reading.Sentinel {
		value += " — special disabled/default value"
	}
	return value
}

func registerChoiceLabel(register board.Register, raw int64, fallback string) string {
	if register != "AUTO_BOOT" {
		return fallback
	}
	labels := [...]string{
		"Manual start only",
		"Battery available — always start",
		"Battery available — respect manual shutdown",
		"External power present — always start",
		"External power present — respect manual shutdown",
		"External power absent — always start",
		"External power lost again — respect manual shutdown",
		"External power restored after removal",
	}
	if raw < 0 || raw >= int64(len(labels)) {
		return fallback
	}
	return labels[raw]
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func registerGroup(register board.Register) string {
	switch register {
	case "I2C_REG_VER", "I2C_ADDRESS":
		return "Identity and interface"
	case "LED_STATE", "TOUCH_CAP_CYCLES", "TOUCH_THRESHOLD", "TOUCH_HYSTERESIS", "TOUCH_STATE":
		return "Button and indicator"
	case "DCO_RSEL", "DCO_DCOMOD", "VBAT_OFFSET", "VOUT_OFFSET", "VIN_OFFSET", "IOUT_OFFSET":
		return "Calibration"
	case "VBAT_MIN", "VBAT_SHDN", "VBAT_BOOT", "VOUT_MAX", "VIN_THRESHOLD", "IOUT_SHDN_THRESHOLD", "VBAT", "VOUT", "VIN", "IOUT":
		return "Power and thresholds"
	case "SHDN_DELAY", "AUTO_SHDN_TIME", "AUTO_BOOT", "PI_BOOT_TO", "PI_SHDN_TO", "PI_RUNNING":
		return "Host lifecycle"
	case "WATCHDOG_CFG", "WATCHDOG_GRACE", "WATCHDOG_TIMER":
		return "Watchdog"
	case "WAKE_TIME", "RTC_TIME", "RTC_WAKE_TIME":
		return "Clock and wake"
	default:
		return "Configuration actions"
	}
}
