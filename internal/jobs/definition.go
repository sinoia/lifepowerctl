package jobs

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

type DefinitionInput struct {
	Name             string            `json:"name"`
	Enabled          bool              `json:"enabled"`
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

type Definition struct {
	ID        string    `json:"id"`
	Revision  uint64    `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	DefinitionInput
}

func (input DefinitionInput) Validate(jobLimits limits.Jobs) error {
	if input.Name == "" || strings.TrimSpace(input.Name) != input.Name || len(input.Name) > jobLimits.MaxIdentifierBytes || !utf8.ValidString(input.Name) {
		return fmt.Errorf("job name must be non-empty, trimmed, valid UTF-8, and at most %d bytes", jobLimits.MaxIdentifierBytes)
	}
	for _, character := range input.Name {
		if unicode.IsControl(character) {
			return fmt.Errorf("job name cannot contain control characters")
		}
	}
	request := StartRequest{
		JobID: "definition-validation", JobRevision: 1, Mode: input.Mode,
		Executable: input.Executable, Arguments: input.Arguments, Command: input.Command,
		WorkingDirectory: input.WorkingDirectory, Environment: input.Environment,
		TimeoutSeconds: input.TimeoutSeconds, StdoutBytes: input.StdoutBytes, StderrBytes: input.StderrBytes,
	}
	if err := request.validate(jobLimits); err != nil {
		return fmt.Errorf("invalid job definition: %w", err)
	}
	return nil
}

func (definition Definition) Validate(jobLimits limits.Jobs) error {
	if !validRunID(definition.ID) || definition.Revision == 0 || definition.CreatedAt.IsZero() || definition.UpdatedAt.IsZero() || definition.UpdatedAt.Before(definition.CreatedAt) {
		return fmt.Errorf("invalid saved-job identity or revision")
	}
	return definition.DefinitionInput.Validate(jobLimits)
}

func (definition Definition) Start(runID string) Request {
	return Request{Version: ProtocolVersion, Action: ActionStart, RunID: runID, Start: &StartRequest{
		JobID: definition.ID, JobRevision: definition.Revision, Mode: definition.Mode,
		Executable: definition.Executable, Arguments: append([]string(nil), definition.Arguments...), Command: definition.Command,
		WorkingDirectory: definition.WorkingDirectory, Environment: cloneEnvironment(definition.Environment),
		TimeoutSeconds: definition.TimeoutSeconds, StdoutBytes: definition.StdoutBytes, StderrBytes: definition.StderrBytes,
	}}
}

func CloneDefinition(definition Definition) Definition {
	definition.Arguments = append([]string(nil), definition.Arguments...)
	definition.Environment = cloneEnvironment(definition.Environment)
	return definition
}

func cloneEnvironment(environment map[string]string) map[string]string {
	if environment == nil {
		return nil
	}
	clone := make(map[string]string, len(environment))
	for name, value := range environment {
		clone[name] = value
	}
	return clone
}
