package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/sinoia/lifepowerctl/internal/activity"
)

const maximumActivityRows = 200

type activityShellData struct {
	Title     string
	Current   string
	Build     BuildInfo
	CSRFToken string
}

type activityListData struct {
	activityShellData
	Events        []activityRowData
	Kinds         []activityFilterOption
	Actors        []activityFilterOption
	SelectedKind  string
	SelectedActor string
	Truncated     bool
}

type activityFilterOption struct {
	Value    string
	Label    string
	Selected bool
}

type activityRowData struct {
	ID          uint64
	Time        string
	Type        string
	Label       string
	Actor       string
	Subject     string
	SubjectLink string
}

type activityDetailData struct {
	activityShellData
	ID          uint64
	Time        string
	Type        string
	Label       string
	Actor       string
	Subject     string
	Details     string
	SubjectLink string
	RunLink     string
}

func (app *application) activityList(response http.ResponseWriter, request *http.Request) {
	kind := request.URL.Query().Get("type")
	actor := request.URL.Query().Get("actor")
	if !knownActivityKind(kind) || !knownActivityActor(actor) {
		app.errorPage(response, http.StatusBadRequest, "Invalid Activity filter", "Choose an event type and actor from the available filters.")
		return
	}
	events, err := app.activityJournal.Events()
	if err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Activity unavailable", "The Activity journal could not be read.")
		return
	}
	data := activityListData{SelectedKind: kind, SelectedActor: actor}
	for _, value := range activity.KnownKinds() {
		data.Kinds = append(data.Kinds, activityFilterOption{Value: string(value), Label: activityKindLabel(string(value)), Selected: kind == string(value)})
	}
	for _, value := range activity.KnownActors() {
		data.Actors = append(data.Actors, activityFilterOption{Value: value, Label: activityActorLabel(value), Selected: actor == value})
	}
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if kind != "" && event.Type != kind || actor != "" && event.Actor != actor {
			continue
		}
		if len(data.Events) == maximumActivityRows {
			data.Truncated = true
			break
		}
		data.Events = append(data.Events, activityRow(event))
	}
	session := sessionFromContext(request.Context())
	data.activityShellData = activityShellData{Title: "Activity", Current: "activity", Build: app.build, CSRFToken: session.CSRFToken}
	app.render(response, "activity-list", data)
}

func (app *application) activityDetail(response http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseUint(request.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		app.errorPage(response, http.StatusNotFound, "Activity event not found", "The requested Activity event does not exist or has expired from retention.")
		return
	}
	events, err := app.activityJournal.Events()
	if err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Activity unavailable", "The Activity journal could not be read.")
		return
	}
	var found *activity.Event
	for index := range events {
		if events[index].ID == id {
			found = &events[index]
			break
		}
	}
	if found == nil {
		app.errorPage(response, http.StatusNotFound, "Activity event not found", "The requested Activity event does not exist or has expired from retention.")
		return
	}
	var detail bytes.Buffer
	if len(found.Details) > 0 {
		if err := json.Indent(&detail, found.Details, "", "  "); err != nil {
			detail.WriteString("Details could not be decoded.")
		}
	} else {
		detail.WriteString("No additional details.")
	}
	session := sessionFromContext(request.Context())
	data := activityDetailData{
		activityShellData: activityShellData{Title: fmt.Sprintf("Activity %d", found.ID), Current: "activity", Build: app.build, CSRFToken: session.CSRFToken},
		ID:                found.ID, Time: formatStatusTime(found.Time), Type: found.Type, Label: activityKindLabel(found.Type),
		Actor: activityActorLabel(found.Actor), Subject: found.Subject, Details: detail.String(), SubjectLink: activitySubjectLink(*found),
	}
	if found.Type == string(activity.DutyCycleTransition) && len(found.Details) > 0 {
		var fields struct {
			RunID string `json:"run_id"`
		}
		if json.Unmarshal(found.Details, &fields) == nil && validRunIdentity(fields.RunID) {
			data.RunLink = "/runs/" + fields.RunID
		}
	}
	app.render(response, "activity-detail", data)
}

func activityRow(event activity.Event) activityRowData {
	return activityRowData{ID: event.ID, Time: formatStatusTime(event.Time), Type: event.Type, Label: activityKindLabel(event.Type), Actor: activityActorLabel(event.Actor), Subject: event.Subject, SubjectLink: activitySubjectLink(event)}
}

func knownActivityKind(value string) bool {
	if value == "" {
		return true
	}
	for _, candidate := range activity.KnownKinds() {
		if value == string(candidate) {
			return true
		}
	}
	return false
}

func knownActivityActor(value string) bool {
	if value == "" {
		return true
	}
	for _, candidate := range activity.KnownActors() {
		if value == candidate {
			return true
		}
	}
	return false
}

func activityKindLabel(value string) string {
	switch activity.Kind(value) {
	case activity.AuthenticationSetupCompleted:
		return "Owner setup completed"
	case activity.AuthenticationLoginSucceeded:
		return "Sign-in succeeded"
	case activity.AuthenticationLogout:
		return "Signed out"
	case activity.AuthenticationReset:
		return "Authentication reset"
	case activity.AuthenticationFailures:
		return "Authentication failures"
	case activity.ServiceStarted:
		return "Controller started"
	case activity.BoardObserved:
		return "Board observed"
	case activity.BoardStateChanged:
		return "Board state changed"
	case activity.JobCreated:
		return "Saved job created"
	case activity.JobUpdated:
		return "Saved job updated"
	case activity.JobDeleted:
		return "Saved job deleted"
	case activity.JobRunRequested:
		return "Manual job run requested"
	case activity.JobRunObserved:
		return "Manual job run observed"
	case activity.JobRunCancelRequested:
		return "Manual job cancellation requested"
	case activity.RegisterChangeAttempted:
		return "Temporary register change attempted"
	case activity.RegisterChangeObserved:
		return "Temporary register change observed"
	case activity.RegisterBatchAttempted:
		return "Temporary register batch attempted"
	case activity.RegisterBatchWriteObserved:
		return "Register batch write observed"
	case activity.RegisterBatchRollbackObserved:
		return "Register batch rollback observed"
	case activity.RegisterBatchObserved:
		return "Temporary register batch observed"
	case activity.DutyCycleConfiguration:
		return "Duty cycle configuration changed"
	case activity.DutyCycleTransition:
		return "Duty cycle state changed"
	default:
		return value
	}
}

func activityActorLabel(value string) string {
	switch value {
	case activity.ActorAdmin:
		return "Owner"
	case activity.ActorLocal:
		return "Local administrator"
	case activity.ActorRemote:
		return "Remote client"
	case activity.ActorSystem:
		return "System"
	default:
		return value
	}
}

func activitySubjectLink(event activity.Event) string {
	if event.Subject == "board" {
		return "/"
	}
	if event.Type == string(activity.RegisterChangeAttempted) || event.Type == string(activity.RegisterChangeObserved) {
		return "/registers/" + event.Subject
	}
	if event.Type == string(activity.RegisterBatchAttempted) || event.Type == string(activity.RegisterBatchWriteObserved) || event.Type == string(activity.RegisterBatchRollbackObserved) || event.Type == string(activity.RegisterBatchObserved) {
		return "/registers"
	}
	if event.Type == string(activity.DutyCycleConfiguration) || event.Type == string(activity.DutyCycleTransition) {
		return "/duty-cycle"
	}
	if (event.Type == string(activity.JobRunRequested) || event.Type == string(activity.JobRunObserved) || event.Type == string(activity.JobRunCancelRequested)) && validRunIdentity(event.Subject) {
		return "/runs/" + event.Subject
	}
	return ""
}

func validRunIdentity(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}
