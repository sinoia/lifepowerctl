// Package controllerstate owns the one atomic controller configuration
// snapshot. Job output and Activity remain in their separate bounded stores.
package controllerstate

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sinoia/lifepowerctl/internal/jobs"
	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/schedule"
	"github.com/sinoia/lifepowerctl/internal/storage"
)

var (
	ErrJobNotFound           = errors.New("saved job not found")
	ErrJobRevisionConflict   = errors.New("saved job revision conflict")
	ErrJobNameConflict       = errors.New("saved job name already exists")
	ErrJobLimit              = errors.New("saved job limit reached")
	ErrConfigurationRevision = errors.New("controller configuration revision conflict")
	ErrProposalIDConflict    = errors.New("configuration proposal ID conflict")
	ErrJobReference          = errors.New("run list job reference is stale or missing")
	ErrJobInRunList          = errors.New("saved job is referenced by the duty-cycle run list")
	ErrProposalNotFound      = errors.New("configuration proposal not found")
	ErrActivation            = errors.New("duty-cycle activation is not valid")
	ErrDutyCycleEnabled      = errors.New("duty cycle must be explicitly disabled before editing")
	ErrTransitionActive      = errors.New("duty-cycle transition is active")
)

const retainedConfigurationProposals = 16

type Manager struct {
	mu        sync.Mutex
	store     *storage.Store
	limits    limits.Jobs
	now       func() time.Time
	random    io.Reader
	revision  uint64
	schema    int
	recovered bool
	state     snapshot
}

type snapshot struct {
	Jobs                   []jobs.Definition       `json:"jobs"`
	Runs                   []Run                   `json:"runs,omitempty"`
	DutyCycle              *schedule.Configuration `json:"duty_cycle,omitempty"`
	ConfigurationProposals []configurationProposal `json:"configuration_proposals,omitempty"`
	DutyCycleLifecycle     schedule.Lifecycle      `json:"duty_cycle_lifecycle"`
}

type configurationProposal struct {
	ID                    string `json:"id"`
	Action                string `json:"action,omitempty"`
	Digest                string `json:"digest"`
	StateRevision         uint64 `json:"state_revision"`
	ConfigurationRevision uint64 `json:"configuration_revision"`
}

type ConfigurationResult struct {
	StateRevision         uint64
	ConfigurationRevision uint64
	Existing              bool
}

func Open(directory string, jobLimits limits.Jobs) (*Manager, error) {
	if jobLimits.MaxSavedJobs < 1 {
		return nil, fmt.Errorf("maximum saved jobs must be positive")
	}
	store, err := storage.New(directory, "controller")
	if err != nil {
		return nil, err
	}
	manager := &Manager{store: store, limits: jobLimits, now: time.Now, random: rand.Reader}
	stored, err := store.Load()
	if errors.Is(err, storage.ErrNotFound) {
		return manager, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load controller state: %w", err)
	}
	decoded, err := decodeSnapshot(stored.Payload)
	if err != nil {
		return nil, err
	}
	if err := validateSnapshot(decoded, jobLimits); err != nil {
		return nil, err
	}
	manager.state = decoded
	manager.revision = stored.Revision
	manager.schema = stored.Schema
	manager.recovered = stored.Recovered
	return manager, nil
}

func (manager *Manager) Jobs() []jobs.Definition {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return cloneJobs(manager.state.Jobs)
}

func (manager *Manager) Recovered() bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.recovered
}

// Revision is the current durable aggregate-state revision. It is exposed to
// the controller/web handoff so proposals can be bound to an exact base state.
func (manager *Manager) Revision() uint64 {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.revision
}

// Schema is the durable envelope schema currently stored on disk. Zero means
// no controller snapshot has been created yet.
func (manager *Manager) Schema() int {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.schema
}

func (manager *Manager) DutyCycleConfiguration() (schedule.Configuration, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.state.DutyCycle == nil {
		return schedule.Configuration{}, false
	}
	return schedule.CloneConfiguration(*manager.state.DutyCycle), true
}

func (manager *Manager) DutyCycleLifecycle() schedule.Lifecycle {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state := manager.state.DutyCycleLifecycle
	if state.Phase == "" {
		state.Phase = schedule.PhaseInactive
	}
	return cloneLifecycle(state)
}

// DutyCycleRunListMaximum returns the conservative sequential runtime bound
// for the current immutable job revisions, including timeout termination grace.
func (manager *Manager) DutyCycleRunListMaximum() (int, uint64, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.state.DutyCycle == nil {
		return 0, 0, ErrActivation
	}
	if err := manager.validateRunListLocked(manager.state.DutyCycle.RunList); err != nil {
		return 0, 0, err
	}
	var seconds uint64
	grace := uint64(manager.limits.TerminationGrace / time.Second)
	for _, entry := range manager.state.DutyCycle.RunList {
		definition := manager.state.Jobs[manager.indexLocked(entry.JobID)]
		timeout := uint64(definition.TimeoutSeconds)
		if ^uint64(0)-seconds < timeout+grace {
			return 0, 0, fmt.Errorf("run-list maximum duration overflows")
		}
		seconds += timeout + grace
	}
	return len(manager.state.DutyCycle.RunList), seconds, nil
}

func (manager *Manager) ConfigurationProposalResult(proposalID string) (ConfigurationResult, error) {
	if !validProposalID(proposalID) {
		return ConfigurationResult{}, fmt.Errorf("invalid configuration proposal ID")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, receipt := range manager.state.ConfigurationProposals {
		if receipt.ID == proposalID {
			return ConfigurationResult{StateRevision: receipt.StateRevision, ConfigurationRevision: receipt.ConfigurationRevision, Existing: true}, nil
		}
	}
	return ConfigurationResult{}, ErrProposalNotFound
}

func (manager *Manager) SimulationResult(proposalID string, baseRevision, configurationRevision uint64, reviewIdentity string, intendedOccurrence uint32) (ConfigurationResult, error) {
	return manager.activationResult("enable-simulation", proposalID, baseRevision, configurationRevision, reviewIdentity, intendedOccurrence)
}

func (manager *Manager) RealActivationResult(proposalID string, baseRevision, configurationRevision uint64, reviewIdentity string, intendedOccurrence uint32) (ConfigurationResult, error) {
	return manager.activationResult("enable-real", proposalID, baseRevision, configurationRevision, reviewIdentity, intendedOccurrence)
}

func (manager *Manager) activationResult(action, proposalID string, baseRevision, configurationRevision uint64, reviewIdentity string, intendedOccurrence uint32) (ConfigurationResult, error) {
	digest, err := activationDigest(baseRevision, configurationRevision, reviewIdentity, intendedOccurrence)
	if err != nil {
		return ConfigurationResult{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, receipt := range manager.state.ConfigurationProposals {
		if receipt.ID != proposalID {
			continue
		}
		if receipt.Action != action || receipt.Digest != digest {
			return ConfigurationResult{}, ErrProposalIDConflict
		}
		return ConfigurationResult{StateRevision: receipt.StateRevision, ConfigurationRevision: receipt.ConfigurationRevision, Existing: true}, nil
	}
	return ConfigurationResult{}, ErrProposalNotFound
}

// SaveDisabledConfiguration atomically accepts one revision-bound proposal.
// Enablement uses a separate future action because it requires occurrence
// preview and fresh board-time validation.
func (manager *Manager) SaveDisabledConfiguration(proposalID string, baseRevision uint64, proposed schedule.Configuration) (ConfigurationResult, error) {
	if !validProposalID(proposalID) {
		return ConfigurationResult{}, fmt.Errorf("invalid configuration proposal ID")
	}
	if proposed.Enabled {
		return ConfigurationResult{}, fmt.Errorf("save-disabled cannot enable automation")
	}
	if err := proposed.Validate(); err != nil {
		return ConfigurationResult{}, err
	}
	digest, err := configurationDigest(baseRevision, proposed)
	if err != nil {
		return ConfigurationResult{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, receipt := range manager.state.ConfigurationProposals {
		if receipt.ID != proposalID {
			continue
		}
		if receipt.Digest != digest {
			return ConfigurationResult{}, ErrProposalIDConflict
		}
		return ConfigurationResult{StateRevision: receipt.StateRevision, ConfigurationRevision: receipt.ConfigurationRevision, Existing: true}, nil
	}
	if manager.revision != baseRevision {
		return ConfigurationResult{}, fmt.Errorf("%w: have %d, expected %d", ErrConfigurationRevision, manager.revision, baseRevision)
	}
	if manager.state.DutyCycle != nil && manager.state.DutyCycle.Enabled {
		return ConfigurationResult{}, ErrDutyCycleEnabled
	}
	wantConfigurationRevision := uint64(1)
	if manager.state.DutyCycle != nil {
		wantConfigurationRevision = manager.state.DutyCycle.Revision + 1
	}
	if proposed.Revision != wantConfigurationRevision {
		return ConfigurationResult{}, fmt.Errorf("%w: next configuration revision is %d", ErrConfigurationRevision, wantConfigurationRevision)
	}
	if err := manager.validateRunListLocked(proposed.RunList); err != nil {
		return ConfigurationResult{}, err
	}
	previous := cloneSnapshotConfiguration(manager.state)
	cloned := schedule.CloneConfiguration(proposed)
	manager.state.DutyCycle = &cloned
	manager.state.DutyCycleLifecycle = schedule.Lifecycle{Phase: schedule.PhaseInactive}
	receipt := configurationProposal{ID: proposalID, Action: "save-disabled", Digest: digest, StateRevision: manager.revision + 1, ConfigurationRevision: proposed.Revision}
	manager.state.ConfigurationProposals = append(manager.state.ConfigurationProposals, receipt)
	if len(manager.state.ConfigurationProposals) > retainedConfigurationProposals {
		manager.state.ConfigurationProposals = append([]configurationProposal(nil), manager.state.ConfigurationProposals[len(manager.state.ConfigurationProposals)-retainedConfigurationProposals:]...)
	}
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycle = previous.DutyCycle
		manager.state.ConfigurationProposals = previous.ConfigurationProposals
		manager.state.DutyCycleLifecycle = previous.DutyCycleLifecycle
		return ConfigurationResult{}, fmt.Errorf("persist duty-cycle configuration: %w", err)
	}
	return ConfigurationResult{StateRevision: manager.revision, ConfigurationRevision: proposed.Revision}, nil
}

func (manager *Manager) EnableSimulation(proposalID string, baseRevision, configurationRevision uint64, reviewIdentity string, intendedOccurrence uint32) (ConfigurationResult, error) {
	return manager.enableActivation("enable-simulation", proposalID, baseRevision, configurationRevision, reviewIdentity, intendedOccurrence, intendedOccurrence, true)
}

func (manager *Manager) EnableReal(proposalID string, baseRevision, configurationRevision uint64, reviewIdentity string, reviewedOccurrence, intendedOccurrence uint32) (ConfigurationResult, error) {
	return manager.enableActivation("enable-real", proposalID, baseRevision, configurationRevision, reviewIdentity, reviewedOccurrence, intendedOccurrence, false)
}

func (manager *Manager) enableActivation(action, proposalID string, baseRevision, configurationRevision uint64, reviewIdentity string, reviewedOccurrence, intendedOccurrence uint32, simulated bool) (ConfigurationResult, error) {
	if !validProposalID(proposalID) || len(reviewIdentity) != 64 || reviewedOccurrence == 0 || intendedOccurrence == 0 {
		return ConfigurationResult{}, fmt.Errorf("%w: invalid activation evidence", ErrActivation)
	}
	digest, err := activationDigest(baseRevision, configurationRevision, reviewIdentity, reviewedOccurrence)
	if err != nil {
		return ConfigurationResult{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, receipt := range manager.state.ConfigurationProposals {
		if receipt.ID != proposalID {
			continue
		}
		if receipt.Action != action || receipt.Digest != digest {
			return ConfigurationResult{}, ErrProposalIDConflict
		}
		return ConfigurationResult{StateRevision: receipt.StateRevision, ConfigurationRevision: receipt.ConfigurationRevision, Existing: true}, nil
	}
	if manager.revision != baseRevision {
		return ConfigurationResult{}, fmt.Errorf("%w: have %d, expected %d", ErrConfigurationRevision, manager.revision, baseRevision)
	}
	if manager.state.DutyCycle == nil || manager.state.DutyCycle.Revision != configurationRevision || manager.state.DutyCycle.Enabled || manager.state.DutyCycle.Schedule.Kind == schedule.KindDisabled || len(manager.state.DutyCycle.RunList) == 0 {
		return ConfigurationResult{}, fmt.Errorf("%w: configuration cannot be enabled", ErrActivation)
	}
	if err := manager.validateRunListLocked(manager.state.DutyCycle.RunList); err != nil {
		return ConfigurationResult{}, err
	}
	for _, entry := range manager.state.DutyCycle.RunList {
		if !manager.state.Jobs[manager.indexLocked(entry.JobID)].Enabled {
			return ConfigurationResult{}, fmt.Errorf("%w: run-list job %s is disabled", ErrActivation, entry.JobID)
		}
	}
	previous := cloneSnapshotConfiguration(manager.state)
	manager.state.DutyCycle.Enabled = true
	phase := schedule.PhaseNextWakeCalculated
	if simulated {
		phase = schedule.PhaseSimulatedShutdownIntended
	}
	manager.state.DutyCycleLifecycle = schedule.Lifecycle{Phase: phase, ConfigurationRevision: configurationRevision, IntendedOccurrence: intendedOccurrence, Simulated: simulated}
	receipt := configurationProposal{ID: proposalID, Action: action, Digest: digest, StateRevision: manager.revision + 1, ConfigurationRevision: configurationRevision}
	manager.state.ConfigurationProposals = append(manager.state.ConfigurationProposals, receipt)
	if len(manager.state.ConfigurationProposals) > retainedConfigurationProposals {
		manager.state.ConfigurationProposals = append([]configurationProposal(nil), manager.state.ConfigurationProposals[len(manager.state.ConfigurationProposals)-retainedConfigurationProposals:]...)
	}
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycle = previous.DutyCycle
		manager.state.ConfigurationProposals = previous.ConfigurationProposals
		manager.state.DutyCycleLifecycle = previous.DutyCycleLifecycle
		return ConfigurationResult{}, fmt.Errorf("persist duty-cycle activation: %w", err)
	}
	return ConfigurationResult{StateRevision: manager.revision, ConfigurationRevision: configurationRevision}, nil
}

func (manager *Manager) Disable(proposalID string, baseRevision, configurationRevision uint64) (ConfigurationResult, error) {
	digest, err := simpleActionDigest("disable", baseRevision, configurationRevision)
	if err != nil {
		return ConfigurationResult{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, receipt := range manager.state.ConfigurationProposals {
		if receipt.ID != proposalID {
			continue
		}
		if receipt.Action != "disable" || receipt.Digest != digest {
			return ConfigurationResult{}, ErrProposalIDConflict
		}
		return ConfigurationResult{StateRevision: receipt.StateRevision, ConfigurationRevision: receipt.ConfigurationRevision, Existing: true}, nil
	}
	if !validProposalID(proposalID) || manager.revision != baseRevision {
		return ConfigurationResult{}, fmt.Errorf("%w: disable base revision does not match", ErrConfigurationRevision)
	}
	if manager.state.DutyCycle == nil || manager.state.DutyCycle.Revision != configurationRevision {
		return ConfigurationResult{}, fmt.Errorf("%w: duty-cycle configuration changed", ErrConfigurationRevision)
	}
	switch manager.state.DutyCycleLifecycle.Phase {
	case schedule.PhaseExpectedBootObserved, schedule.PhasePreparingRun, schedule.PhaseRunningJob, schedule.PhaseRunListSucceeded:
		return ConfigurationResult{}, ErrTransitionActive
	}
	previous := cloneSnapshotConfiguration(manager.state)
	manager.state.DutyCycle.Enabled = false
	manager.state.DutyCycleLifecycle = schedule.Lifecycle{Phase: schedule.PhaseInactive}
	receipt := configurationProposal{ID: proposalID, Action: "disable", Digest: digest, StateRevision: manager.revision + 1, ConfigurationRevision: configurationRevision}
	manager.appendProposalLocked(receipt)
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycle = previous.DutyCycle
		manager.state.DutyCycleLifecycle = previous.DutyCycleLifecycle
		manager.state.ConfigurationProposals = previous.ConfigurationProposals
		return ConfigurationResult{}, fmt.Errorf("persist duty-cycle disable: %w", err)
	}
	return ConfigurationResult{StateRevision: manager.revision, ConfigurationRevision: configurationRevision}, nil
}

// ReconcileStartup makes every active or inconsistent simulated lifecycle
// conservative before the controller becomes ready. Recovery has no real wake
// evidence, so a process restart cannot classify itself as an expected boot.
func (manager *Manager) ReconcileStartup() (bool, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	phase := manager.state.DutyCycleLifecycle.Phase
	if manager.state.DutyCycle == nil || !manager.state.DutyCycle.Enabled && (phase == "" || phase == schedule.PhaseInactive || phase == schedule.PhaseManualSafe || phase == schedule.PhaseCompleted) {
		return false, nil
	}
	previous := cloneSnapshotConfiguration(manager.state)
	manager.enterRunFailureLocked("controller restarted during an active duty-cycle transition")
	manager.state.DutyCycleLifecycle.ConfigurationRevision = manager.state.DutyCycle.Revision
	if err := manager.saveLocked(); err != nil {
		manager.state.DutyCycle = previous.DutyCycle
		manager.state.DutyCycleLifecycle = previous.DutyCycleLifecycle
		return false, fmt.Errorf("persist startup manual-safe state: %w", err)
	}
	return true, nil
}

func (manager *Manager) validateRunListLocked(entries []schedule.RunListEntry) error {
	for _, entry := range entries {
		index := manager.indexLocked(entry.JobID)
		if index < 0 || manager.state.Jobs[index].Revision != entry.JobRevision {
			return fmt.Errorf("%w: %s revision %d", ErrJobReference, entry.JobID, entry.JobRevision)
		}
	}
	return nil
}

func configurationDigest(baseRevision uint64, proposed schedule.Configuration) (string, error) {
	encoded, err := json.Marshal(struct {
		BaseRevision  uint64                 `json:"base_revision"`
		Configuration schedule.Configuration `json:"configuration"`
	}{BaseRevision: baseRevision, Configuration: proposed})
	if err != nil {
		return "", fmt.Errorf("encode configuration proposal: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func activationDigest(baseRevision, configurationRevision uint64, reviewIdentity string, intendedOccurrence uint32) (string, error) {
	encoded, err := json.Marshal(struct {
		Action                string `json:"action"`
		BaseRevision          uint64 `json:"base_revision"`
		ConfigurationRevision uint64 `json:"configuration_revision"`
		ReviewIdentity        string `json:"review_identity"`
		IntendedOccurrence    uint32 `json:"intended_occurrence"`
	}{Action: "enable-simulation", BaseRevision: baseRevision, ConfigurationRevision: configurationRevision, ReviewIdentity: reviewIdentity, IntendedOccurrence: intendedOccurrence})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func simpleActionDigest(action string, baseRevision, configurationRevision uint64) (string, error) {
	encoded, err := json.Marshal(struct {
		Action                string `json:"action"`
		BaseRevision          uint64 `json:"base_revision"`
		ConfigurationRevision uint64 `json:"configuration_revision"`
	}{Action: action, BaseRevision: baseRevision, ConfigurationRevision: configurationRevision})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (manager *Manager) appendProposalLocked(receipt configurationProposal) {
	manager.state.ConfigurationProposals = append(manager.state.ConfigurationProposals, receipt)
	if len(manager.state.ConfigurationProposals) > retainedConfigurationProposals {
		manager.state.ConfigurationProposals = append([]configurationProposal(nil), manager.state.ConfigurationProposals[len(manager.state.ConfigurationProposals)-retainedConfigurationProposals:]...)
	}
}

func validProposalID(value string) bool {
	if len(value) != 32 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func cloneSnapshotConfiguration(value snapshot) snapshot {
	clone := snapshot{ConfigurationProposals: append([]configurationProposal(nil), value.ConfigurationProposals...), DutyCycleLifecycle: cloneLifecycle(value.DutyCycleLifecycle)}
	if value.DutyCycle != nil {
		configuration := schedule.CloneConfiguration(*value.DutyCycle)
		clone.DutyCycle = &configuration
	}
	return clone
}

func (manager *Manager) Create(input jobs.DefinitionInput) (jobs.Definition, error) {
	if err := input.Validate(manager.limits); err != nil {
		return jobs.Definition{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.state.Jobs) >= manager.limits.MaxSavedJobs {
		return jobs.Definition{}, ErrJobLimit
	}
	if manager.nameExistsLocked(input.Name, "") {
		return jobs.Definition{}, ErrJobNameConflict
	}
	id, err := manager.uniqueIDLocked()
	if err != nil {
		return jobs.Definition{}, fmt.Errorf("generate saved-job ID: %w", err)
	}
	now := manager.now().UTC()
	definition := jobs.Definition{ID: id, Revision: 1, CreatedAt: now, UpdatedAt: now, DefinitionInput: input}
	definition = jobs.CloneDefinition(definition)
	previous := cloneJobs(manager.state.Jobs)
	manager.state.Jobs = append(manager.state.Jobs, definition)
	if err := manager.saveLocked(); err != nil {
		manager.state.Jobs = previous
		return jobs.Definition{}, fmt.Errorf("persist saved job: %w", err)
	}
	return jobs.CloneDefinition(definition), nil
}

func (manager *Manager) uniqueIDLocked() (string, error) {
	for range 4 {
		id, err := randomID(manager.random)
		if err != nil {
			return "", err
		}
		if manager.indexLocked(id) < 0 {
			return id, nil
		}
	}
	return "", fmt.Errorf("could not generate a unique saved-job ID")
}

func (manager *Manager) Update(id string, expectedRevision uint64, input jobs.DefinitionInput) (jobs.Definition, error) {
	if err := input.Validate(manager.limits); err != nil {
		return jobs.Definition{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	index := manager.indexLocked(id)
	if index < 0 {
		return jobs.Definition{}, ErrJobNotFound
	}
	current := manager.state.Jobs[index]
	if current.Revision != expectedRevision {
		return jobs.Definition{}, ErrJobRevisionConflict
	}
	if manager.jobReferencedLocked(id) {
		return jobs.Definition{}, ErrJobInRunList
	}
	if manager.nameExistsLocked(input.Name, id) {
		return jobs.Definition{}, ErrJobNameConflict
	}
	updated := jobs.Definition{ID: current.ID, Revision: current.Revision + 1, CreatedAt: current.CreatedAt, UpdatedAt: manager.now().UTC(), DefinitionInput: input}
	updated = jobs.CloneDefinition(updated)
	manager.state.Jobs[index] = updated
	if err := manager.saveLocked(); err != nil {
		manager.state.Jobs[index] = current
		return jobs.Definition{}, fmt.Errorf("persist saved job: %w", err)
	}
	return jobs.CloneDefinition(updated), nil
}

func (manager *Manager) Delete(id string, expectedRevision uint64) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	index := manager.indexLocked(id)
	if index < 0 {
		return ErrJobNotFound
	}
	if manager.state.Jobs[index].Revision != expectedRevision {
		return ErrJobRevisionConflict
	}
	if manager.jobReferencedLocked(id) {
		return ErrJobInRunList
	}
	previous := cloneJobs(manager.state.Jobs)
	manager.state.Jobs = slices.Delete(manager.state.Jobs, index, index+1)
	if err := manager.saveLocked(); err != nil {
		manager.state.Jobs = previous
		return fmt.Errorf("persist saved jobs: %w", err)
	}
	return nil
}

func (manager *Manager) indexLocked(id string) int {
	for index := range manager.state.Jobs {
		if manager.state.Jobs[index].ID == id {
			return index
		}
	}
	return -1
}

func (manager *Manager) nameExistsLocked(name, exceptID string) bool {
	for _, definition := range manager.state.Jobs {
		if definition.ID != exceptID && strings.EqualFold(definition.Name, name) {
			return true
		}
	}
	return false
}

func (manager *Manager) jobReferencedLocked(id string) bool {
	if manager.state.DutyCycle == nil {
		return false
	}
	for _, entry := range manager.state.DutyCycle.RunList {
		if entry.JobID == id {
			return true
		}
	}
	return false
}

func (manager *Manager) saveLocked() error {
	saved, err := manager.store.Save(manager.revision, manager.state)
	if err != nil {
		return err
	}
	manager.revision = saved.Revision
	manager.schema = saved.Schema
	manager.recovered = false
	return nil
}

func validateSnapshot(value snapshot, jobLimits limits.Jobs) error {
	if len(value.Jobs) > jobLimits.MaxSavedJobs {
		return fmt.Errorf("controller state has %d saved jobs; maximum is %d", len(value.Jobs), jobLimits.MaxSavedJobs)
	}
	ids := make(map[string]struct{}, len(value.Jobs))
	names := make(map[string]struct{}, len(value.Jobs))
	for _, definition := range value.Jobs {
		if err := definition.Validate(jobLimits); err != nil {
			return fmt.Errorf("invalid saved job %q: %w", definition.ID, err)
		}
		if _, exists := ids[definition.ID]; exists {
			return fmt.Errorf("duplicate saved-job ID %q", definition.ID)
		}
		ids[definition.ID] = struct{}{}
		name := strings.ToLower(definition.Name)
		if _, exists := names[name]; exists {
			return fmt.Errorf("duplicate saved-job name %q", definition.Name)
		}
		names[name] = struct{}{}
	}
	if err := validateRuns(value.Runs, jobLimits); err != nil {
		return err
	}
	if value.DutyCycle != nil {
		if err := value.DutyCycle.Validate(); err != nil {
			return fmt.Errorf("invalid duty-cycle configuration: %w", err)
		}
		for _, entry := range value.DutyCycle.RunList {
			found := false
			for _, definition := range value.Jobs {
				if definition.ID == entry.JobID && definition.Revision == entry.JobRevision {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("duty-cycle run list has stale job reference %q", entry.JobID)
			}
		}
	}
	if err := value.DutyCycleLifecycle.Validate(value.DutyCycle); err != nil {
		return fmt.Errorf("invalid duty-cycle lifecycle: %w", err)
	}
	if len(value.ConfigurationProposals) > retainedConfigurationProposals {
		return fmt.Errorf("too many retained configuration proposals")
	}
	proposalIDs := make(map[string]struct{}, len(value.ConfigurationProposals))
	for _, proposal := range value.ConfigurationProposals {
		if !validProposalID(proposal.ID) || len(proposal.Digest) != 64 || proposal.StateRevision == 0 || proposal.ConfigurationRevision == 0 {
			return fmt.Errorf("invalid retained configuration proposal")
		}
		if proposal.Action != "" && proposal.Action != "save-disabled" && proposal.Action != "enable-simulation" && proposal.Action != "enable-real" && proposal.Action != "disable" {
			return fmt.Errorf("invalid retained configuration proposal action")
		}
		if _, exists := proposalIDs[proposal.ID]; exists {
			return fmt.Errorf("duplicate retained configuration proposal")
		}
		proposalIDs[proposal.ID] = struct{}{}
	}
	return nil
}

func decodeSnapshot(payload []byte) (snapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var value snapshot
	if err := decoder.Decode(&value); err != nil {
		return snapshot{}, fmt.Errorf("decode controller state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return snapshot{}, fmt.Errorf("decode controller state: trailing data")
	}
	return value, nil
}

func randomID(source io.Reader) (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(source, value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func cloneJobs(definitions []jobs.Definition) []jobs.Definition {
	clone := make([]jobs.Definition, len(definitions))
	for index, definition := range definitions {
		clone[index] = jobs.CloneDefinition(definition)
	}
	return clone
}
