package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/sinoia/lifepowerctl/internal/hostimage"
	"github.com/sinoia/lifepowerctl/internal/overview"
)

const multipartOverheadBytes = 64 << 10

func overviewPageData(snapshot overview.Snapshot) pageData {
	data := pageData{
		BoardState: string(snapshot.State), BoardTone: "warning", BoardLabel: "Unknown",
		BoardSummary: "Board compatibility has not been checked.", RegisterSet: "not detected",
		CheckedAt: formatStatusTime(snapshot.CheckedAt), LastSuccess: formatStatusTime(snapshot.LastSuccess),
	}
	if snapshot.Identity.RegisterSet > 0 {
		data.RegisterSet = fmt.Sprintf("%d", snapshot.Identity.RegisterSet)
	}
	switch snapshot.State {
	case overview.StateSupported:
		data.BoardTone = "ok"
		data.BoardLabel = "Ready"
		data.BoardSummary = "Register set 7 is supported by this LifePower Control build."
	case overview.StateUnsupported:
		data.BoardTone = "error"
		data.BoardLabel = "Unsupported"
		data.BoardSummary = "The detected register set is not supported. No hardware action is available."
	case overview.StateUnavailable:
		data.BoardTone = "error"
		data.BoardLabel = "Unavailable"
		data.BoardSummary = "The board identity could not be read. Hardware actions remain inhibited."
		data.FailureGuidance = failureGuidance(snapshot.Failure)
	case overview.StateStale:
		data.BoardTone = "warning"
		data.BoardLabel = "Stale"
		data.BoardSummary = "The last supported identity is retained, but the latest refresh failed or is too old."
		data.FailureGuidance = failureGuidance(snapshot.Failure)
	}
	return data
}

func (app *application) hostImageFile(response http.ResponseWriter, request *http.Request) {
	file, info, err := app.hostImage.Open()
	if errors.Is(err, hostimage.ErrNotFound) {
		app.errorPage(response, http.StatusNotFound, "Host image not found", "No host image has been uploaded.")
		return
	}
	if err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Host image unavailable", "The stored host image could not be read safely.")
		return
	}
	defer file.Close()
	response.Header().Set("Content-Type", info.ContentType)
	response.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size))
	response.Header().Set("Content-Disposition", "inline")
	http.ServeContent(response, request, "", time.Unix(0, info.Modified), file)
}

func (app *application) hostImageUpload(response http.ResponseWriter, request *http.Request) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The image upload could not be verified.")
		return
	}
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
		app.errorPage(response, http.StatusUnsupportedMediaType, "Unsupported upload", "Use the image upload form on the Overview page.")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, app.hostImage.Limits().MaxBytes+multipartOverheadBytes)
	if err := request.ParseMultipartForm(app.maxBodyBytes); err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			app.errorPage(response, http.StatusRequestEntityTooLarge, "Image too large", "Choose an image no larger than 8 MiB.")
			return
		}
		app.errorPage(response, http.StatusBadRequest, "Invalid upload", "The submitted image form was invalid or incomplete.")
		return
	}
	defer request.MultipartForm.RemoveAll()
	values := request.MultipartForm.Value
	files := request.MultipartForm.File
	if len(values) != 1 || len(values["csrf"]) != 1 || len(files) != 1 || len(files["image"]) != 1 {
		app.errorPage(response, http.StatusBadRequest, "Invalid upload", "The submitted image form contained missing, repeated, or unexpected fields.")
		return
	}
	if err := app.authentication.CheckCSRF(sessionFromContext(request.Context()).Token, values["csrf"][0]); err != nil {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The image upload could not be verified.")
		return
	}
	upload, err := files["image"][0].Open()
	if err != nil {
		app.errorPage(response, http.StatusBadRequest, "Invalid upload", "The uploaded image could not be opened.")
		return
	}
	defer upload.Close()
	if _, err := app.hostImage.Replace(upload); errors.Is(err, hostimage.ErrInvalid) {
		app.errorPage(response, http.StatusUnprocessableEntity, "Unsupported image", "Choose a complete JPEG or PNG image no larger than 8 MiB and 16 megapixels.")
		return
	} else if err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Image not saved", "The host image could not be stored safely.")
		return
	}
	http.Redirect(response, request, "/", http.StatusSeeOther)
}

func (app *application) hostImageRemove(response http.ResponseWriter, request *http.Request) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The image removal could not be verified.")
		return
	}
	form, ok := app.parseForm(response, request, "csrf")
	if !ok {
		return
	}
	if err := app.authentication.CheckCSRF(sessionFromContext(request.Context()).Token, form.Get("csrf")); err != nil {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The image removal could not be verified.")
		return
	}
	if err := app.hostImage.Remove(); err != nil {
		app.errorPage(response, http.StatusServiceUnavailable, "Image not removed", "The host image could not be removed safely.")
		return
	}
	http.Redirect(response, request, "/", http.StatusSeeOther)
}

func formatUptime(value time.Duration) string {
	if value < 0 {
		return "Unavailable"
	}
	minutes := int64(value / time.Minute)
	if minutes == 0 {
		return "Less than 1 minute"
	}
	days := minutes / (24 * 60)
	hours := minutes % (24 * 60) / 60
	minutes %= 60
	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", days, countUnit(days, "day", "days")))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", hours, countUnit(hours, "hour", "hours")))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", minutes, countUnit(minutes, "minute", "minutes")))
	}
	return strings.Join(parts, " ")
}

func countUnit(value int64, singular, pluralValue string) string {
	if value == 1 {
		return singular
	}
	return pluralValue
}

func failureGuidance(failure overview.Failure) string {
	switch failure {
	case overview.FailureTimeout:
		return "The fixed vendor read exceeded its three-second deadline."
	case overview.FailureOutputLimit:
		return "The vendor command exceeded its bounded output allowance."
	case overview.FailureInvalidOutput:
		return "The vendor command returned an unexpected value."
	case overview.FailureCommand:
		return "The vendor command could not read the board. Check the daemon, I²C access, and hardware connection."
	case overview.FailureCommunication:
		return "The board read could not be completed. Check the vendor installation and hardware connection."
	default:
		return "Refresh the page to try the bounded read again."
	}
}

func formatStatusTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.Local().Format("2006-01-02 15:04:05 MST")
}

func (app *application) readiness(response http.ResponseWriter, _ *http.Request) {
	snapshot := app.overviewService.Current()
	ready := app.audit == nil || app.audit.Healthy()
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(struct {
		Ready      bool   `json:"ready"`
		BoardState string `json:"board_state"`
	}{Ready: ready, BoardState: string(snapshot.State)})
}
