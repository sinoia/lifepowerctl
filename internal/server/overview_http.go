package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sinoia/lifepowerctl/internal/overview"
)

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
