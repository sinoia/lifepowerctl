package jobs

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

const ProtocolVersion = 1

var ErrInvalidRequest = errors.New("invalid job runner request")

func NewRunID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate run ID: %w", err)
	}
	return hex.EncodeToString(value), nil
}

type Action string

const (
	ActionStart  Action = "start"
	ActionStatus Action = "status"
	ActionCancel Action = "cancel"
)

type ExecutionMode string

const (
	ModeDirect ExecutionMode = "direct"
	ModeShell  ExecutionMode = "shell"
)

type Request struct {
	Version int           `json:"version"`
	Action  Action        `json:"action"`
	RunID   string        `json:"run_id"`
	Start   *StartRequest `json:"start,omitempty"`
}

type StartRequest struct {
	JobID            string            `json:"job_id"`
	JobRevision      uint64            `json:"job_revision"`
	Mode             ExecutionMode     `json:"mode"`
	Executable       string            `json:"executable,omitempty"`
	Arguments        []string          `json:"arguments,omitempty"`
	Command          string            `json:"command,omitempty"`
	WorkingDirectory string            `json:"working_directory"`
	Environment      map[string]string `json:"environment,omitempty"`
	TimeoutSeconds   int64             `json:"timeout_seconds"`
	StdoutBytes      int64             `json:"stdout_bytes"`
	StderrBytes      int64             `json:"stderr_bytes"`
}

type RunState string

const (
	StateQueued    RunState = "queued"
	StateRunning   RunState = "running"
	StateSucceeded RunState = "succeeded"
	StateFailed    RunState = "failed"
	StateTimedOut  RunState = "timed-out"
	StateCancelled RunState = "cancelled"
	StateLost      RunState = "lost"
)

type Response struct {
	Version           int      `json:"version"`
	RunID             string   `json:"run_id"`
	State             RunState `json:"state"`
	Existing          bool     `json:"existing,omitempty"`
	ErrorCode         string   `json:"error_code,omitempty"`
	Message           string   `json:"message,omitempty"`
	QueuedUnixMilli   int64    `json:"queued_unix_milli,omitempty"`
	StartedUnixMilli  int64    `json:"started_unix_milli,omitempty"`
	FinishedUnixMilli int64    `json:"finished_unix_milli,omitempty"`
	ExitCode          *int     `json:"exit_code,omitempty"`
	Signal            string   `json:"signal,omitempty"`
	StdoutTruncated   bool     `json:"stdout_truncated,omitempty"`
	StderrTruncated   bool     `json:"stderr_truncated,omitempty"`
	StdoutTotal       int64    `json:"stdout_total,omitempty"`
	StderrTotal       int64    `json:"stderr_total,omitempty"`
}

// WriteResponse writes one bounded response frame. Output content is never
// placed in this frame; only bounded result metadata is allowed.
func WriteResponse(writer io.Writer, response Response, jobLimits limits.Jobs) error {
	if err := response.Validate(); err != nil {
		return err
	}
	return writeFrame(writer, response, jobLimits)
}

// WriteRequest writes one already validated controller request frame.
func WriteRequest(writer io.Writer, request Request, jobLimits limits.Jobs) error {
	if err := request.Validate(jobLimits); err != nil {
		return err
	}
	return writeFrame(writer, request, jobLimits)
}

func writeFrame(writer io.Writer, value any, jobLimits limits.Jobs) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	if len(payload) == 0 || int64(len(payload)) > jobLimits.MaxRunnerFrameBytes {
		return fmt.Errorf("encode frame: frame length %d exceeds limit", len(payload))
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeBytes(writer, header[:]); err != nil {
		return fmt.Errorf("write frame length: %w", err)
	}
	if err := writeBytes(writer, payload); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	return nil
}

// ReadResponse reads one strict bounded runner response. Result frames never
// contain captured output bytes.
func ReadResponse(reader io.Reader, jobLimits limits.Jobs) (Response, error) {
	payload, err := readFrame(reader, jobLimits)
	if err != nil {
		return Response{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var response Response
	if err := decoder.Decode(&response); err != nil {
		return Response{}, fmt.Errorf("%w: decode response: %v", ErrInvalidRequest, err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return Response{}, err
	}
	if err := response.Validate(); err != nil {
		return Response{}, err
	}
	return response, nil
}

func (response Response) Validate() error {
	if response.Version != ProtocolVersion || !validRunID(response.RunID) || !validRunState(response.State) {
		return fmt.Errorf("%w: invalid response identity", ErrInvalidRequest)
	}
	if response.QueuedUnixMilli < 0 || response.StartedUnixMilli < 0 || response.FinishedUnixMilli < 0 || response.StdoutTotal < 0 || response.StderrTotal < 0 {
		return fmt.Errorf("%w: invalid response measurements", ErrInvalidRequest)
	}
	if len(response.ErrorCode) > 64 || len(response.Message) > 512 || len(response.Signal) > 32 {
		return fmt.Errorf("%w: response text exceeds limit", ErrInvalidRequest)
	}
	return nil
}

func writeBytes(writer io.Writer, value []byte) error {
	written, err := writer.Write(value)
	if err != nil {
		return err
	}
	if written != len(value) {
		return io.ErrShortWrite
	}
	return nil
}

func validRunState(state RunState) bool {
	switch state {
	case StateQueued, StateRunning, StateSucceeded, StateFailed, StateTimedOut, StateCancelled, StateLost:
		return true
	default:
		return false
	}
}

// ReadRequest reads exactly one length-prefixed versioned JSON request. It
// validates all fields before a runner may enqueue or execute anything.
func ReadRequest(reader io.Reader, jobLimits limits.Jobs) (Request, error) {
	payload, err := readFrame(reader, jobLimits)
	if err != nil {
		return Request{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, fmt.Errorf("%w: decode frame: %v", ErrInvalidRequest, err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return Request{}, err
	}
	if err := request.Validate(jobLimits); err != nil {
		return Request{}, err
	}
	return request, nil
}

func readFrame(reader io.Reader, jobLimits limits.Jobs) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, fmt.Errorf("%w: read frame length: %v", ErrInvalidRequest, err)
	}
	length := int64(binary.BigEndian.Uint32(header[:]))
	if length <= 0 || length > jobLimits.MaxRunnerFrameBytes {
		return nil, fmt.Errorf("%w: frame length %d exceeds limit", ErrInvalidRequest, length)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, fmt.Errorf("%w: read frame: %v", ErrInvalidRequest, err)
	}
	if !utf8.Valid(payload) {
		return nil, fmt.Errorf("%w: frame is not valid UTF-8", ErrInvalidRequest)
	}
	return payload, nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: multiple JSON values", ErrInvalidRequest)
		}
		return fmt.Errorf("%w: trailing JSON: %v", ErrInvalidRequest, err)
	}
	return nil
}

func (request Request) Validate(jobLimits limits.Jobs) error {
	if request.Version != ProtocolVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidRequest, request.Version)
	}
	if !validRunID(request.RunID) {
		return fmt.Errorf("%w: run_id must be 32 lowercase hexadecimal characters", ErrInvalidRequest)
	}
	switch request.Action {
	case ActionStart:
		if request.Start == nil {
			return fmt.Errorf("%w: start payload is required", ErrInvalidRequest)
		}
		return request.Start.validate(jobLimits)
	case ActionStatus, ActionCancel:
		if request.Start != nil {
			return fmt.Errorf("%w: %s cannot include start payload", ErrInvalidRequest, request.Action)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported action %q", ErrInvalidRequest, request.Action)
	}
}

func validRunID(value string) bool {
	if len(value) != 32 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (request StartRequest) validate(jobLimits limits.Jobs) error {
	if request.JobID == "" || len(request.JobID) > jobLimits.MaxIdentifierBytes || strings.ContainsRune(request.JobID, 0) {
		return fmt.Errorf("%w: invalid job_id", ErrInvalidRequest)
	}
	if request.JobRevision == 0 {
		return fmt.Errorf("%w: job_revision must be positive", ErrInvalidRequest)
	}
	if request.WorkingDirectory == "" || len(request.WorkingDirectory) > jobLimits.MaxPathBytes || !filepath.IsAbs(request.WorkingDirectory) || strings.ContainsRune(request.WorkingDirectory, 0) {
		return fmt.Errorf("%w: working directory must be a bounded absolute path", ErrInvalidRequest)
	}
	if request.TimeoutSeconds <= 0 || request.TimeoutSeconds > int64(jobLimits.MaximumTimeout.Seconds()) {
		return fmt.Errorf("%w: invalid timeout", ErrInvalidRequest)
	}
	if request.StdoutBytes <= 0 || request.StdoutBytes > jobLimits.MaximumStreamBytes || request.StderrBytes <= 0 || request.StderrBytes > jobLimits.MaximumStreamBytes {
		return fmt.Errorf("%w: invalid output limit", ErrInvalidRequest)
	}
	if err := validateEnvironment(request.Environment, jobLimits); err != nil {
		return err
	}
	switch request.Mode {
	case ModeDirect:
		if request.Executable == "" || len(request.Executable) > jobLimits.MaxPathBytes || strings.ContainsRune(request.Executable, 0) || request.Command != "" {
			return fmt.Errorf("%w: invalid direct execution fields", ErrInvalidRequest)
		}
		if len(request.Arguments) > jobLimits.MaxArguments {
			return fmt.Errorf("%w: too many arguments", ErrInvalidRequest)
		}
		total := 0
		for _, argument := range request.Arguments {
			if strings.ContainsRune(argument, 0) {
				return fmt.Errorf("%w: argument contains NUL", ErrInvalidRequest)
			}
			total += len(argument) + 1
		}
		if total > jobLimits.MaxArgumentBytes {
			return fmt.Errorf("%w: arguments exceed byte limit", ErrInvalidRequest)
		}
	case ModeShell:
		if request.Command == "" || len(request.Command) > jobLimits.MaxCommandBytes || strings.ContainsRune(request.Command, 0) || request.Executable != "" || len(request.Arguments) != 0 {
			return fmt.Errorf("%w: invalid shell execution fields", ErrInvalidRequest)
		}
	default:
		return fmt.Errorf("%w: unsupported execution mode %q", ErrInvalidRequest, request.Mode)
	}
	return nil
}

func validateEnvironment(environment map[string]string, jobLimits limits.Jobs) error {
	if len(environment) > jobLimits.MaxEnvironment {
		return fmt.Errorf("%w: too many environment entries", ErrInvalidRequest)
	}
	total := 0
	for name, value := range environment {
		if !validEnvironmentName(name) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("%w: invalid environment entry", ErrInvalidRequest)
		}
		switch name {
		case "HOME", "USER", "LOGNAME", "PATH":
			return fmt.Errorf("%w: reserved environment entry %s", ErrInvalidRequest, name)
		}
		total += len(name) + len(value) + 2
	}
	if total > jobLimits.MaxEnvironmentBytes {
		return fmt.Errorf("%w: environment exceeds byte limit", ErrInvalidRequest)
	}
	return nil
}

func validEnvironmentName(name string) bool {
	if name == "" || !isEnvironmentStart(name[0]) {
		return false
	}
	for index := 1; index < len(name); index++ {
		character := name[index]
		if !isEnvironmentStart(character) && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func isEnvironmentStart(character byte) bool {
	return character == '_' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
}
