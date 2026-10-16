package jobs

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

const testRunID = "00112233445566778899aabbccddeeff"

func validStartRequest() Request {
	return Request{
		Version: ProtocolVersion,
		Action:  ActionStart,
		RunID:   testRunID,
		Start: &StartRequest{
			JobID:            "camera-job",
			JobRevision:      3,
			Mode:             ModeDirect,
			Executable:       "/home/pi/camera/take-photo",
			Arguments:        []string{"--quiet"},
			WorkingDirectory: "/home/pi/camera",
			Environment:      map[string]string{"CAMERA": "front"},
			TimeoutSeconds:   60,
			StdoutBytes:      1 << 20,
			StderrBytes:      1 << 20,
		},
	}
}

func frameJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	return frame
}

func TestReadRequest(t *testing.T) {
	request, err := ReadRequest(bytes.NewReader(frameJSON(t, validStartRequest())), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	if request.RunID != testRunID || request.Start == nil || request.Start.Executable != "/home/pi/camera/take-photo" {
		t.Fatalf("ReadRequest() = %+v", request)
	}
}

func TestWriteRequestRoundTrip(t *testing.T) {
	var frame bytes.Buffer
	if err := WriteRequest(&frame, validStartRequest(), limits.Default().Jobs); err != nil {
		t.Fatal(err)
	}
	request, err := ReadRequest(&frame, limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	if request.RunID != testRunID || request.Start == nil || request.Start.JobRevision != 3 {
		t.Fatalf("round-trip request = %+v", request)
	}
}

func TestReadRequestRejectsInvalidFrames(t *testing.T) {
	jobLimits := limits.Default().Jobs
	tests := []struct {
		name  string
		frame []byte
	}{
		{name: "empty", frame: []byte{0, 0, 0, 0}},
		{name: "oversized", frame: []byte{0, 1, 0, 1}},
		{name: "short", frame: []byte{0, 0, 0, 5, '{'}},
		{name: "invalid UTF-8", frame: []byte{0, 0, 0, 1, 0xff}},
		{name: "unknown field", frame: framedPayload(`{"version":1,"action":"status","run_id":"` + testRunID + `","extra":true}`)},
		{name: "multiple values", frame: framedPayload(`{} {}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ReadRequest(bytes.NewReader(test.frame), jobLimits); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("ReadRequest() error = %v", err)
			}
		})
	}
}

func framedPayload(payload string) []byte {
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	return frame
}

func TestRequestValidation(t *testing.T) {
	jobLimits := limits.Default().Jobs
	tests := []struct {
		name   string
		change func(*Request)
	}{
		{name: "version", change: func(request *Request) { request.Version = 2 }},
		{name: "run ID", change: func(request *Request) { request.RunID = strings.ToUpper(testRunID) }},
		{name: "action", change: func(request *Request) { request.Action = "remove" }},
		{name: "job revision", change: func(request *Request) { request.Start.JobRevision = 0 }},
		{name: "relative directory", change: func(request *Request) { request.Start.WorkingDirectory = "camera" }},
		{name: "timeout", change: func(request *Request) { request.Start.TimeoutSeconds = int64(jobLimits.MaximumTimeout.Seconds()) + 1 }},
		{name: "output", change: func(request *Request) { request.Start.StdoutBytes = jobLimits.MaximumStreamBytes + 1 }},
		{name: "mixed mode", change: func(request *Request) { request.Start.Command = "echo unsafe" }},
		{name: "argument NUL", change: func(request *Request) { request.Start.Arguments = []string{"bad\x00argument"} }},
		{name: "reserved environment", change: func(request *Request) { request.Start.Environment = map[string]string{"HOME": "/tmp"} }},
		{name: "environment name", change: func(request *Request) { request.Start.Environment = map[string]string{"BAD-NAME": "x"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validStartRequest()
			test.change(&request)
			if err := request.Validate(jobLimits); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestShellAndLookupRequestsValidate(t *testing.T) {
	request := validStartRequest()
	request.Start.Mode = ModeShell
	request.Start.Executable = ""
	request.Start.Arguments = nil
	request.Start.Command = "source .venv/bin/activate && python frame.py"
	if err := request.Validate(limits.Default().Jobs); err != nil {
		t.Fatal(err)
	}

	for _, action := range []Action{ActionStatus, ActionCancel} {
		lookup := Request{Version: ProtocolVersion, Action: action, RunID: testRunID}
		if err := lookup.Validate(limits.Default().Jobs); err != nil {
			t.Fatalf("%s Validate() error = %v", action, err)
		}
	}
}

func TestWriteResponse(t *testing.T) {
	var output bytes.Buffer
	response := Response{Version: ProtocolVersion, RunID: testRunID, State: StateQueued, QueuedUnixMilli: 1234}
	if err := WriteResponse(&output, response, limits.Default().Jobs); err != nil {
		t.Fatal(err)
	}
	frame := output.Bytes()
	if len(frame) < 5 || int(binary.BigEndian.Uint32(frame[:4])) != len(frame)-4 {
		t.Fatalf("invalid response frame length: %x", frame)
	}
	var decoded Response
	if err := json.Unmarshal(frame[4:], &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RunID != testRunID || decoded.State != StateQueued {
		t.Fatalf("decoded response = %+v", decoded)
	}
}

func TestReadResponse(t *testing.T) {
	response := Response{Version: ProtocolVersion, RunID: testRunID, State: StateSucceeded, QueuedUnixMilli: 1, StartedUnixMilli: 2, FinishedUnixMilli: 3}
	decoded, err := ReadResponse(bytes.NewReader(frameJSON(t, response)), limits.Default().Jobs)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.RunID != testRunID || decoded.State != StateSucceeded || decoded.FinishedUnixMilli != 3 {
		t.Fatalf("ReadResponse() = %+v", decoded)
	}
}

func TestReadResponseRejectsUnboundedOrUnknownMetadata(t *testing.T) {
	jobLimits := limits.Default().Jobs
	tests := []Response{
		{Version: ProtocolVersion, RunID: testRunID, State: StateFailed, StdoutTotal: -1},
		{Version: ProtocolVersion, RunID: testRunID, State: StateFailed, Message: strings.Repeat("x", 513)},
	}
	for _, response := range tests {
		if _, err := ReadResponse(bytes.NewReader(frameJSON(t, response)), jobLimits); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("ReadResponse() error = %v", err)
		}
	}
	unknown := framedPayload(`{"version":1,"run_id":"` + testRunID + `","state":"failed","output":"secret"}`)
	if _, err := ReadResponse(bytes.NewReader(unknown), jobLimits); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown-field error = %v", err)
	}
}

func TestWriteResponseRejectsInvalidMetadata(t *testing.T) {
	response := Response{Version: ProtocolVersion, RunID: testRunID, State: "invented"}
	if err := WriteResponse(&bytes.Buffer{}, response, limits.Default().Jobs); err == nil {
		t.Fatal("WriteResponse accepted unknown state")
	}
}
