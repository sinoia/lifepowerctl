package jobs

import (
	"strings"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

func validDefinitionInput() DefinitionInput {
	return DefinitionInput{
		Name: "Take photograph", Enabled: true, Mode: ModeDirect,
		Executable: "/home/pi/camera/take-photo", Arguments: []string{"--quiet"},
		WorkingDirectory: "/home/pi/camera", Environment: map[string]string{"CAMERA": "front"},
		TimeoutSeconds: 60, StdoutBytes: 1 << 20, StderrBytes: 1 << 20,
	}
}

func TestDefinitionInputKeepsDirectAndShellSemanticsDistinct(t *testing.T) {
	jobLimits := limits.Default().Jobs
	direct := validDefinitionInput()
	if err := direct.Validate(jobLimits); err != nil {
		t.Fatal(err)
	}
	shell := direct
	shell.Mode, shell.Executable, shell.Arguments = ModeShell, "", nil
	shell.Command = "source .venv/bin/activate && python frame.py"
	if err := shell.Validate(jobLimits); err != nil {
		t.Fatal(err)
	}
	shell.Executable = "/bin/bash"
	if err := shell.Validate(jobLimits); err == nil {
		t.Fatal("shell definition accepted a separately configurable executable")
	}
}

func TestDefinitionInputRejectsUnsafeOrUnboundedFields(t *testing.T) {
	jobLimits := limits.Default().Jobs
	tests := []struct {
		name   string
		change func(*DefinitionInput)
	}{
		{name: "blank name", change: func(value *DefinitionInput) { value.Name = " " }},
		{name: "control in name", change: func(value *DefinitionInput) { value.Name = "bad\nname" }},
		{name: "relative directory", change: func(value *DefinitionInput) { value.WorkingDirectory = "camera" }},
		{name: "reserved environment", change: func(value *DefinitionInput) { value.Environment = map[string]string{"HOME": "/tmp"} }},
		{name: "too long", change: func(value *DefinitionInput) { value.Name = strings.Repeat("x", jobLimits.MaxIdentifierBytes+1) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validDefinitionInput()
			test.change(&value)
			if err := value.Validate(jobLimits); err == nil {
				t.Fatal("invalid definition accepted")
			}
		})
	}
}

func TestDefinitionProducesImmutableRunnerSnapshot(t *testing.T) {
	now := time.Date(2026, time.October, 11, 9, 0, 0, 0, time.UTC)
	definition := Definition{ID: testRunID, Revision: 2, CreatedAt: now, UpdatedAt: now, DefinitionInput: validDefinitionInput()}
	request := definition.Start("ffeeddccbbaa99887766554433221100")
	definition.Arguments[0] = "changed"
	definition.Environment["CAMERA"] = "changed"
	if request.Start.JobID != testRunID || request.Start.JobRevision != 2 || request.Start.Arguments[0] != "--quiet" || request.Start.Environment["CAMERA"] != "front" {
		t.Fatalf("runner snapshot changed with definition: %+v", request.Start)
	}
}
