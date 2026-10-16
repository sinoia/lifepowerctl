package controllerapi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

func TestProtocolHealthRoundTrip(t *testing.T) {
	var frame bytes.Buffer
	build := BuildIdentity{Product: "lifepowerctl", Version: "v0.1.0-test", Commit: "0123456789abcdef", SourceState: "clean", TargetOS: "linux", TargetArch: "arm64", StateSchemaCurrent: 1, StateSchemaMinReadable: 1}
	want := Response{Version: ProtocolVersion, Action: ActionHealth, Health: HealthReady, StateRevision: 7, StateSchema: 1, Build: &build}
	if err := WriteResponse(&frame, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadResponse(&frame)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadResponse() = %+v, want %+v", got, want)
	}
}

func TestProtocolRejectsUnknownAndOversizedMessages(t *testing.T) {
	unknown := []byte(`{"version":1,"action":"health","extra":true}`)
	var framed bytes.Buffer
	_ = binary.Write(&framed, binary.BigEndian, uint32(len(unknown)))
	framed.Write(unknown)
	if _, err := ReadRequest(&framed); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("unknown-field error = %v", err)
	}

	framed.Reset()
	_ = binary.Write(&framed, binary.BigEndian, uint32(MaxFrameBytes+1))
	if _, err := ReadRequest(&framed); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestProtocolRejectsUnsupportedActionsAndHealth(t *testing.T) {
	if err := (Request{Version: ProtocolVersion, Action: "mutate"}).Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("request error = %v", err)
	}
	if err := (Response{Version: ProtocolVersion, Action: ActionHealth, Health: "unknown", Build: &BuildIdentity{Product: "lifepowerctl"}}).Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("response error = %v", err)
	}
}

func TestBuildIdentityRejectsUnsafeMachineValuesAndSchemaRanges(t *testing.T) {
	valid := BuildIdentity{Product: "lifepowerctl", Version: "v0.1.0-test", Commit: "0123456789abcdef", SourceState: "clean", TargetOS: "linux", TargetArch: "arm64", StateSchemaCurrent: 2, StateSchemaMinReadable: 1}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	unsafe := valid
	unsafe.Version = "v0.1.0\nrunning_commit=forged"
	if err := unsafe.Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("unsafe build value error = %v", err)
	}
	unsafe = valid
	unsafe.StateSchemaMinReadable = 3
	if err := unsafe.Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("invalid schema range error = %v", err)
	}
}

func TestProtocolRejectsInconsistentDurableStateSchema(t *testing.T) {
	build := BuildIdentity{Product: "lifepowerctl", Version: "v0.1.0-test", Commit: "0123456789abcdef", SourceState: "clean", TargetOS: "linux", TargetArch: "arm64", StateSchemaCurrent: 2, StateSchemaMinReadable: 1}
	if err := (Response{Version: ProtocolVersion, Action: ActionHealth, Health: HealthReady, StateRevision: 7, Build: &build}).Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("missing durable schema error = %v", err)
	}
	inactive := schedule.Lifecycle{Phase: schedule.PhaseInactive}
	if err := (Response{Version: ProtocolVersion, Action: ActionStatus, StateSchema: 1, Lifecycle: &inactive}).Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("schema without revision error = %v", err)
	}
}

func TestProtocolAcceptsTypedDisabledConfigurationProposal(t *testing.T) {
	configuration := schedule.Configuration{Revision: 1, Schedule: schedule.Specification{Kind: schedule.KindDisabled}}
	request := Request{Version: ProtocolVersion, Action: ActionSaveDisabled, ProposalID: "00112233445566778899aabbccddeeff", Configuration: &configuration}
	var frame bytes.Buffer
	if err := WriteRequest(&frame, request); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRequest(&frame)
	if err != nil {
		t.Fatal(err)
	}
	if got.Configuration == nil || got.Configuration.Revision != 1 {
		t.Fatalf("ReadRequest() = %+v", got)
	}
}

func TestProtocolAcceptsBoundedPreviewRequest(t *testing.T) {
	request := Request{Version: ProtocolVersion, Action: ActionPreview, ConfigurationRevision: 3, PreviewCount: 5}
	var frame bytes.Buffer
	if err := WriteRequest(&frame, request); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRequest(&frame)
	if err != nil || got.ConfigurationRevision != 3 || got.PreviewCount != 5 {
		t.Fatalf("ReadRequest() = %+v, %v", got, err)
	}
	request.PreviewCount = schedule.MaxPreviewOccurrences + 1
	if err := request.Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("excessive preview error = %v", err)
	}
}

func TestProtocolAcceptsSimulatedEnableReview(t *testing.T) {
	configuration := schedule.Configuration{Revision: 3, Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15}}
	review, err := schedule.BuildReview(configuration, 1_800_000_000, 60*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Version: ProtocolVersion, Action: ActionEnableSimulation, ProposalID: "00112233445566778899aabbccddeeff", BaseRevision: 8, ConfigurationRevision: 3, Review: &review}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolAcceptsRealPreviewAndEnableReview(t *testing.T) {
	configuration := schedule.Configuration{Revision: 3, Schedule: schedule.Specification{Kind: schedule.KindHourly, Timezone: "UTC", Minute: 15}}
	review, err := schedule.BuildReview(configuration, 1_800_000_000, schedule.VerifiedWakeMinimumLead, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []Request{
		{Version: ProtocolVersion, Action: ActionPreviewReal, ConfigurationRevision: 3, PreviewCount: 5},
		{Version: ProtocolVersion, Action: ActionEnableReal, ProposalID: "00112233445566778899aabbccddeeff", BaseRevision: 8, ConfigurationRevision: 3, Review: &review},
	} {
		if err := request.Validate(); err != nil {
			t.Fatalf("%s: %v", request.Action, err)
		}
	}
	response := Response{Version: ProtocolVersion, Action: ActionEnableReal, StateRevision: 9, ConfigurationRevision: 3, IntendedOccurrence: review.Occurrences[0].Unix}
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	response.IntendedOccurrence = 0
	if err := response.Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("missing intended occurrence error = %v", err)
	}
}

func TestProtocolAcceptsExplicitDisable(t *testing.T) {
	request := Request{Version: ProtocolVersion, Action: ActionDisable, ProposalID: "0123456789abcdef0123456789abcdef", BaseRevision: 9, ConfigurationRevision: 3}
	var frame bytes.Buffer
	if err := WriteRequest(&frame, request); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRequest(&frame)
	if err != nil || got.Action != ActionDisable || got.BaseRevision != 9 {
		t.Fatalf("ReadRequest() = %+v, %v", got, err)
	}
}

func TestProtocolAcceptsExpectedBootSimulationRequest(t *testing.T) {
	request := Request{Version: ProtocolVersion, Action: ActionSimulateBoot, BaseRevision: 9, ConfigurationRevision: 3}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	request.BaseRevision = 0
	if err := request.Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("missing revision error = %v", err)
	}
}

func TestProtocolAcceptsTypedSavedJobMutations(t *testing.T) {
	input := jobs.DefinitionInput{Name: "Camera", Enabled: true, Mode: jobs.ModeDirect, Executable: "/bin/true", WorkingDirectory: "/tmp", TimeoutSeconds: 30, StdoutBytes: 1024, StderrBytes: 1024}
	for _, request := range []Request{
		{Version: ProtocolVersion, Action: ActionJobList},
		{Version: ProtocolVersion, Action: ActionJobCreate, JobInput: &input},
		{Version: ProtocolVersion, Action: ActionJobUpdate, JobID: "00112233445566778899aabbccddeeff", JobRevision: 2, JobInput: &input},
		{Version: ProtocolVersion, Action: ActionJobDelete, JobID: "00112233445566778899aabbccddeeff", JobRevision: 2},
	} {
		var frame bytes.Buffer
		if err := WriteRequest(&frame, request); err != nil {
			t.Fatalf("%s: %v", request.Action, err)
		}
		if _, err := ReadRequest(&frame); err != nil {
			t.Fatalf("%s round trip: %v", request.Action, err)
		}
	}
	invalid := Request{Version: ProtocolVersion, Action: ActionJobDelete, JobID: "job", JobRevision: 1, ConfigurationRevision: 2}
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("mixed fields error = %v", err)
	}
}

func TestProtocolAcceptsBoundedRunHistoryRequests(t *testing.T) {
	for _, request := range []Request{
		{Version: ProtocolVersion, Action: ActionRunList, RunLimit: 50},
		{Version: ProtocolVersion, Action: ActionRunList, RunLimit: 20, RunFilterJobID: "00112233445566778899aabbccddeeff"},
		{Version: ProtocolVersion, Action: ActionRunGet, RunID: "ffeeddccbbaa99887766554433221100"},
	} {
		var frame bytes.Buffer
		if err := WriteRequest(&frame, request); err != nil {
			t.Fatalf("%s: %v", request.Action, err)
		}
		if _, err := ReadRequest(&frame); err != nil {
			t.Fatalf("%s round trip: %v", request.Action, err)
		}
	}
	if err := (Request{Version: ProtocolVersion, Action: ActionRunList, RunLimit: 51}).Validate(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("unbounded run-list error = %v", err)
	}
}
