// Package controllerapi defines the bounded local protocol between the
// independently managed web process and the always-on controller.
package controllerapi

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/sinoia/lifepowerctl/internal/controllerstate"
	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

const (
	ProtocolVersion = 4
	MaxFrameBytes   = 64 << 10
)

var ErrInvalidMessage = errors.New("invalid controller message")

type Action string

const (
	ActionHealth           Action = "health"
	ActionStatus           Action = "status"
	ActionSaveDisabled     Action = "save-disabled"
	ActionProposal         Action = "proposal-status"
	ActionPreview          Action = "preview"
	ActionEnableSimulation Action = "enable-simulation"
	ActionPreviewReal      Action = "preview-real"
	ActionEnableReal       Action = "enable-real"
	ActionDisable          Action = "disable"
	ActionSimulateBoot     Action = "simulate-expected-boot"
	ActionJobList          Action = "job-list"
	ActionJobCreate        Action = "job-create"
	ActionJobUpdate        Action = "job-update"
	ActionJobDelete        Action = "job-delete"
	ActionRunBegin         Action = "run-begin"
	ActionRunUpdate        Action = "run-update"
	ActionRunObserve       Action = "run-observe"
	ActionRunLost          Action = "run-lost"
	ActionRunList          Action = "run-list"
	ActionRunGet           Action = "run-get"
)

type Request struct {
	Version               int                     `json:"version"`
	Action                Action                  `json:"action"`
	ProposalID            string                  `json:"proposal_id,omitempty"`
	BaseRevision          uint64                  `json:"base_revision,omitempty"`
	Configuration         *schedule.Configuration `json:"configuration,omitempty"`
	ConfigurationRevision uint64                  `json:"configuration_revision,omitempty"`
	PreviewCount          int                     `json:"preview_count,omitempty"`
	Review                *schedule.Review        `json:"review,omitempty"`
	JobID                 string                  `json:"job_id,omitempty"`
	JobRevision           uint64                  `json:"job_revision,omitempty"`
	JobInput              *jobs.DefinitionInput   `json:"job_input,omitempty"`
	RunRequest            *jobs.Request           `json:"run_request,omitempty"`
	RunResponse           *jobs.Response          `json:"run_response,omitempty"`
	DisplayJobID          string                  `json:"display_job_id,omitempty"`
	AdHoc                 bool                    `json:"ad_hoc,omitempty"`
	RunID                 string                  `json:"run_id,omitempty"`
	RunState              jobs.RunState           `json:"run_state,omitempty"`
	RunLimit              int                     `json:"run_limit,omitempty"`
	RunFilterJobID        string                  `json:"run_filter_job_id,omitempty"`
}

type Health string

const (
	HealthReady     Health = "ready"
	HealthRecovered Health = "recovered"
)

// BuildIdentity is the bounded, machine-readable identity of the running
// controller. Packaging uses it to distinguish an installed payload from the
// process that is actually serving the local socket.
type BuildIdentity struct {
	Product                string `json:"product"`
	Version                string `json:"version"`
	Commit                 string `json:"commit"`
	SourceState            string `json:"source_state"`
	TargetOS               string `json:"target_os"`
	TargetArch             string `json:"target_arch"`
	StateSchemaCurrent     int    `json:"state_schema_current"`
	StateSchemaMinReadable int    `json:"state_schema_min_readable"`
}

func (identity BuildIdentity) Validate() error {
	values := []string{identity.Product, identity.Version, identity.Commit, identity.SourceState, identity.TargetOS, identity.TargetArch}
	for _, value := range values {
		if value == "" || len(value) > 128 || !utf8.ValidString(value) || !machineValue(value) {
			return fmt.Errorf("%w: invalid controller build identity", ErrInvalidMessage)
		}
	}
	if identity.StateSchemaMinReadable < 1 || identity.StateSchemaCurrent < identity.StateSchemaMinReadable {
		return fmt.Errorf("%w: invalid controller state schema range", ErrInvalidMessage)
	}
	return nil
}

func machineValue(value string) bool {
	for _, character := range value {
		if character < 0x21 || character > 0x7e || character == '=' {
			return false
		}
	}
	return true
}

type Response struct {
	Version               int                     `json:"version"`
	Action                Action                  `json:"action"`
	Health                Health                  `json:"health,omitempty"`
	StateRevision         uint64                  `json:"state_revision"`
	StateSchema           int                     `json:"state_schema"`
	ConfigurationRevision uint64                  `json:"configuration_revision,omitempty"`
	IntendedOccurrence    uint32                  `json:"intended_occurrence,omitempty"`
	Existing              bool                    `json:"existing,omitempty"`
	ErrorCode             string                  `json:"error_code,omitempty"`
	Message               string                  `json:"message,omitempty"`
	Review                *schedule.Review        `json:"review,omitempty"`
	Configuration         *schedule.Configuration `json:"configuration,omitempty"`
	Lifecycle             *schedule.Lifecycle     `json:"lifecycle,omitempty"`
	Jobs                  []jobs.Definition       `json:"jobs,omitempty"`
	Job                   *jobs.Definition        `json:"job,omitempty"`
	Run                   *controllerstate.Run    `json:"run,omitempty"`
	Runs                  []controllerstate.Run   `json:"runs,omitempty"`
	Changed               bool                    `json:"changed,omitempty"`
	Build                 *BuildIdentity          `json:"build,omitempty"`
	ActiveRuns            int                     `json:"active_runs,omitempty"`
}

func (request Request) Validate() error {
	if request.Version != ProtocolVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidMessage, request.Version)
	}
	jobAction := request.Action == ActionJobList || request.Action == ActionJobCreate || request.Action == ActionJobUpdate || request.Action == ActionJobDelete
	runAction := request.Action == ActionRunBegin || request.Action == ActionRunUpdate || request.Action == ActionRunObserve || request.Action == ActionRunLost
	runReadAction := request.Action == ActionRunList || request.Action == ActionRunGet
	if !jobAction && (request.JobID != "" || request.JobRevision != 0 || request.JobInput != nil) {
		return fmt.Errorf("%w: request contains unexpected saved-job fields", ErrInvalidMessage)
	}
	if jobAction && (request.ProposalID != "" || request.BaseRevision != 0 || request.Configuration != nil || request.ConfigurationRevision != 0 || request.PreviewCount != 0 || request.Review != nil) {
		return fmt.Errorf("%w: saved-job request contains duty-cycle fields", ErrInvalidMessage)
	}
	if !runAction && !runReadAction && (request.RunRequest != nil || request.RunResponse != nil || request.DisplayJobID != "" || request.AdHoc || request.RunID != "" || request.RunState != "") {
		return fmt.Errorf("%w: request contains unexpected run fields", ErrInvalidMessage)
	}
	if !runReadAction && (request.RunLimit != 0 || request.RunFilterJobID != "") {
		return fmt.Errorf("%w: request contains unexpected run-history fields", ErrInvalidMessage)
	}
	if runAction && (jobAction || request.ProposalID != "" || request.BaseRevision != 0 || request.Configuration != nil || request.ConfigurationRevision != 0 || request.PreviewCount != 0 || request.Review != nil || request.JobID != "" || request.JobRevision != 0 || request.JobInput != nil) {
		return fmt.Errorf("%w: run request contains unrelated fields", ErrInvalidMessage)
	}
	switch request.Action {
	case ActionHealth, ActionStatus:
		if request.ProposalID != "" || request.BaseRevision != 0 || request.Configuration != nil || request.ConfigurationRevision != 0 || request.PreviewCount != 0 || request.Review != nil {
			return fmt.Errorf("%w: health request contains proposal fields", ErrInvalidMessage)
		}
	case ActionSaveDisabled:
		if !validProposalID(request.ProposalID) || request.Configuration == nil || request.ConfigurationRevision != 0 || request.PreviewCount != 0 || request.Review != nil {
			return fmt.Errorf("%w: save-disabled requires proposal identity and configuration", ErrInvalidMessage)
		}
		if request.Configuration.Enabled {
			return fmt.Errorf("%w: save-disabled cannot enable automation", ErrInvalidMessage)
		}
		if err := request.Configuration.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidMessage, err)
		}
	case ActionProposal:
		if !validProposalID(request.ProposalID) || request.BaseRevision != 0 || request.Configuration != nil || request.ConfigurationRevision != 0 || request.PreviewCount != 0 || request.Review != nil {
			return fmt.Errorf("%w: proposal-status contains invalid fields", ErrInvalidMessage)
		}
	case ActionPreview, ActionPreviewReal:
		if request.ProposalID != "" || request.BaseRevision != 0 || request.Configuration != nil || request.ConfigurationRevision == 0 || request.PreviewCount <= 0 || request.PreviewCount > schedule.MaxPreviewOccurrences || request.Review != nil {
			return fmt.Errorf("%w: preview contains invalid fields", ErrInvalidMessage)
		}
	case ActionEnableSimulation, ActionEnableReal:
		if !validProposalID(request.ProposalID) || request.BaseRevision == 0 || request.Configuration != nil || request.ConfigurationRevision == 0 || request.PreviewCount != 0 || request.Review == nil || request.Review.ConfigurationRevision != request.ConfigurationRevision || len(request.Review.Identity) != 64 || len(request.Review.Occurrences) == 0 || len(request.Review.Occurrences) > schedule.MaxPreviewOccurrences {
			return fmt.Errorf("%w: enable-simulation contains invalid review evidence", ErrInvalidMessage)
		}
	case ActionDisable:
		if !validProposalID(request.ProposalID) || request.BaseRevision == 0 || request.Configuration != nil || request.ConfigurationRevision == 0 || request.PreviewCount != 0 || request.Review != nil {
			return fmt.Errorf("%w: disable contains invalid fields", ErrInvalidMessage)
		}
	case ActionSimulateBoot:
		if request.ProposalID != "" || request.BaseRevision == 0 || request.Configuration != nil || request.ConfigurationRevision == 0 || request.PreviewCount != 0 || request.Review != nil {
			return fmt.Errorf("%w: simulate-expected-boot contains invalid fields", ErrInvalidMessage)
		}
	case ActionJobList:
		if request.JobID != "" || request.JobRevision != 0 || request.JobInput != nil {
			return fmt.Errorf("%w: job-list contains mutation fields", ErrInvalidMessage)
		}
	case ActionJobCreate:
		if request.JobID != "" || request.JobRevision != 0 || request.JobInput == nil {
			return fmt.Errorf("%w: job-create contains invalid fields", ErrInvalidMessage)
		}
	case ActionJobUpdate:
		if request.JobID == "" || len(request.JobID) > 128 || request.JobRevision == 0 || request.JobInput == nil {
			return fmt.Errorf("%w: job-update contains invalid fields", ErrInvalidMessage)
		}
	case ActionJobDelete:
		if request.JobID == "" || len(request.JobID) > 128 || request.JobRevision == 0 || request.JobInput != nil {
			return fmt.Errorf("%w: job-delete contains invalid fields", ErrInvalidMessage)
		}
	case ActionRunBegin:
		if request.RunRequest == nil || request.RunResponse != nil || request.DisplayJobID == "" || request.RunID != "" || request.RunState != "" {
			return fmt.Errorf("%w: run-begin contains invalid fields", ErrInvalidMessage)
		}
	case ActionRunUpdate:
		if request.RunResponse == nil || request.RunRequest != nil || request.DisplayJobID != "" || request.AdHoc || request.RunID != "" || request.RunState != "" {
			return fmt.Errorf("%w: run-update contains invalid fields", ErrInvalidMessage)
		}
	case ActionRunObserve:
		if request.RunID == "" || request.RunState == "" || request.RunRequest != nil || request.RunResponse != nil || request.DisplayJobID != "" || request.AdHoc {
			return fmt.Errorf("%w: run-observe contains invalid fields", ErrInvalidMessage)
		}
	case ActionRunLost:
		if request.RunID == "" || request.RunState != "" || request.RunRequest != nil || request.RunResponse != nil || request.DisplayJobID != "" || request.AdHoc {
			return fmt.Errorf("%w: run-lost contains invalid fields", ErrInvalidMessage)
		}
	case ActionRunList:
		if request.RunRequest != nil || request.RunResponse != nil || request.DisplayJobID != "" || request.AdHoc || request.RunID != "" || request.RunState != "" || request.RunLimit < 1 || request.RunLimit > 50 || len(request.RunFilterJobID) > 128 {
			return fmt.Errorf("%w: run-list contains invalid fields", ErrInvalidMessage)
		}
	case ActionRunGet:
		if request.RunRequest != nil || request.RunResponse != nil || request.DisplayJobID != "" || request.AdHoc || !validProposalID(request.RunID) || request.RunState != "" || request.RunLimit != 0 || request.RunFilterJobID != "" {
			return fmt.Errorf("%w: run-get contains invalid fields", ErrInvalidMessage)
		}
	default:
		return fmt.Errorf("%w: unsupported action %q", ErrInvalidMessage, request.Action)
	}
	return nil
}

func (response Response) Validate() error {
	if response.Version != ProtocolVersion {
		return fmt.Errorf("%w: invalid response identity", ErrInvalidMessage)
	}
	if response.Action != ActionStatus && (response.Configuration != nil || response.Lifecycle != nil) {
		return fmt.Errorf("%w: response contains unexpected status fields", ErrInvalidMessage)
	}
	if response.ActiveRuns < 0 || response.ActiveRuns > 10000 || response.Action != ActionStatus && response.ActiveRuns != 0 {
		return fmt.Errorf("%w: invalid active run count", ErrInvalidMessage)
	}
	if response.Action != ActionHealth && response.Build != nil {
		return fmt.Errorf("%w: response contains unexpected build identity", ErrInvalidMessage)
	}
	if response.Action != ActionEnableReal && response.IntendedOccurrence != 0 {
		return fmt.Errorf("%w: response contains an unexpected intended occurrence", ErrInvalidMessage)
	}
	jobAction := response.Action == ActionJobList || response.Action == ActionJobCreate || response.Action == ActionJobUpdate || response.Action == ActionJobDelete
	if !jobAction && (len(response.Jobs) != 0 || response.Job != nil) {
		return fmt.Errorf("%w: response contains unexpected saved-job fields", ErrInvalidMessage)
	}
	runAction := response.Action == ActionRunBegin || response.Action == ActionRunUpdate || response.Action == ActionRunObserve || response.Action == ActionRunLost || response.Action == ActionRunGet
	if !runAction && response.Action != ActionRunList && (response.Run != nil || response.Changed || len(response.Runs) != 0) {
		return fmt.Errorf("%w: response contains unexpected run fields", ErrInvalidMessage)
	}
	if response.Action != ActionRunList && len(response.Runs) != 0 {
		return fmt.Errorf("%w: response contains unexpected run history", ErrInvalidMessage)
	}
	switch response.Action {
	case ActionHealth:
		if response.Health != HealthReady && response.Health != HealthRecovered {
			return fmt.Errorf("%w: invalid controller health %q", ErrInvalidMessage, response.Health)
		}
		if response.Configuration != nil || response.Lifecycle != nil || response.Build == nil {
			return fmt.Errorf("%w: health response contains status fields", ErrInvalidMessage)
		}
		if err := response.Build.Validate(); err != nil {
			return err
		}
		if response.StateRevision == 0 && response.StateSchema != 0 || response.StateRevision != 0 && (response.StateSchema < response.Build.StateSchemaMinReadable || response.StateSchema > response.Build.StateSchemaCurrent) {
			return fmt.Errorf("%w: health response has invalid durable state schema", ErrInvalidMessage)
		}
	case ActionStatus:
		if response.Health != "" || response.ErrorCode != "" || response.Review != nil || response.Lifecycle == nil {
			return fmt.Errorf("%w: invalid status response", ErrInvalidMessage)
		}
		if response.Configuration == nil {
			if response.ConfigurationRevision != 0 || response.Lifecycle.Phase != schedule.PhaseInactive {
				return fmt.Errorf("%w: invalid empty status response", ErrInvalidMessage)
			}
		} else {
			if response.ConfigurationRevision != response.Configuration.Revision {
				return fmt.Errorf("%w: status configuration revision mismatch", ErrInvalidMessage)
			}
			if err := response.Configuration.Validate(); err != nil {
				return fmt.Errorf("%w: invalid status configuration: %v", ErrInvalidMessage, err)
			}
		}
		if err := response.Lifecycle.Validate(response.Configuration); err != nil {
			return fmt.Errorf("%w: invalid status lifecycle: %v", ErrInvalidMessage, err)
		}
		if response.StateRevision == 0 && response.StateSchema != 0 || response.StateRevision != 0 && response.StateSchema < 1 {
			return fmt.Errorf("%w: status response has invalid durable state schema", ErrInvalidMessage)
		}
	case ActionSaveDisabled, ActionProposal, ActionDisable, ActionSimulateBoot:
		if response.Health != "" || response.Review != nil || response.ErrorCode == "" && (response.StateRevision == 0 || response.ConfigurationRevision == 0) {
			return fmt.Errorf("%w: invalid configuration response", ErrInvalidMessage)
		}
	case ActionPreview, ActionPreviewReal:
		if response.Health != "" || response.ErrorCode == "" && (response.StateRevision == 0 || response.ConfigurationRevision == 0 || response.Review == nil) {
			return fmt.Errorf("%w: invalid preview response", ErrInvalidMessage)
		}
	case ActionEnableSimulation, ActionEnableReal:
		if response.Health != "" || response.ErrorCode == "" && (response.StateRevision == 0 || response.ConfigurationRevision == 0 || response.Review != nil) {
			return fmt.Errorf("%w: invalid simulated enable response", ErrInvalidMessage)
		}
		if response.Action == ActionEnableReal && response.ErrorCode == "" && response.IntendedOccurrence == 0 {
			return fmt.Errorf("%w: real enable response lacks intended occurrence", ErrInvalidMessage)
		}
	case ActionJobList:
		if response.Health != "" || response.Review != nil || response.Job != nil || response.ErrorCode != "" {
			return fmt.Errorf("%w: invalid job-list response", ErrInvalidMessage)
		}
	case ActionJobCreate, ActionJobUpdate:
		if response.Health != "" || response.Review != nil || len(response.Jobs) != 0 || response.ErrorCode == "" && response.Job == nil {
			return fmt.Errorf("%w: invalid saved-job response", ErrInvalidMessage)
		}
	case ActionJobDelete:
		if response.Health != "" || response.Review != nil || len(response.Jobs) != 0 || response.Job != nil {
			return fmt.Errorf("%w: invalid job-delete response", ErrInvalidMessage)
		}
	case ActionRunBegin, ActionRunUpdate, ActionRunLost, ActionRunGet:
		if response.Health != "" || response.Review != nil || response.ErrorCode == "" && response.Run == nil {
			return fmt.Errorf("%w: invalid run response", ErrInvalidMessage)
		}
	case ActionRunObserve:
		if response.Health != "" || response.Review != nil || response.Run != nil || response.Changed {
			return fmt.Errorf("%w: invalid run-observe response", ErrInvalidMessage)
		}
	case ActionRunList:
		if response.Health != "" || response.Review != nil || response.Run != nil || response.Changed || response.ErrorCode != "" || len(response.Runs) > 50 {
			return fmt.Errorf("%w: invalid run-list response", ErrInvalidMessage)
		}
	default:
		return fmt.Errorf("%w: unsupported response action %q", ErrInvalidMessage, response.Action)
	}
	if len(response.ErrorCode) > 64 || len(response.Message) > 512 {
		return fmt.Errorf("%w: response text exceeds limit", ErrInvalidMessage)
	}
	return nil
}

func validProposalID(value string) bool {
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

func WriteRequest(writer io.Writer, request Request) error {
	if err := request.Validate(); err != nil {
		return err
	}
	return writeFrame(writer, request)
}

func ReadRequest(reader io.Reader) (Request, error) {
	var request Request
	if err := readFrame(reader, &request); err != nil {
		return Request{}, err
	}
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	return request, nil
}

func WriteResponse(writer io.Writer, response Response) error {
	if err := response.Validate(); err != nil {
		return err
	}
	return writeFrame(writer, response)
}

func ReadResponse(reader io.Reader) (Response, error) {
	var response Response
	if err := readFrame(reader, &response); err != nil {
		return Response{}, err
	}
	if err := response.Validate(); err != nil {
		return Response{}, err
	}
	return response, nil
}

func writeFrame(writer io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode controller message: %w", err)
	}
	if len(payload) == 0 || len(payload) > MaxFrameBytes {
		return fmt.Errorf("encode controller message: frame length %d exceeds limit", len(payload))
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func readFrame(reader io.Reader, destination any) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return fmt.Errorf("%w: read frame length: %v", ErrInvalidMessage, err)
	}
	length := int(binary.BigEndian.Uint32(header[:]))
	if length <= 0 || length > MaxFrameBytes {
		return fmt.Errorf("%w: frame length %d exceeds limit", ErrInvalidMessage, length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return fmt.Errorf("%w: read frame: %v", ErrInvalidMessage, err)
	}
	if !utf8.Valid(payload) {
		return fmt.Errorf("%w: frame is not valid UTF-8", ErrInvalidMessage)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: decode frame: %v", ErrInvalidMessage, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing JSON", ErrInvalidMessage)
	}
	return nil
}

func writeAll(writer io.Writer, value []byte) error {
	written, err := writer.Write(value)
	if err != nil {
		return fmt.Errorf("write controller message: %w", err)
	}
	if written != len(value) {
		return fmt.Errorf("write controller message: %w", io.ErrShortWrite)
	}
	return nil
}
