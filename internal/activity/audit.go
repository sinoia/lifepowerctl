package activity

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type Kind string

const (
	AuthenticationSetupCompleted  Kind = "authentication.setup-completed"
	AuthenticationLoginSucceeded  Kind = "authentication.login-succeeded"
	AuthenticationLogout          Kind = "authentication.logout"
	AuthenticationReset           Kind = "authentication.reset"
	AuthenticationFailures        Kind = "authentication.failure-summary"
	ServiceStarted                Kind = "service.started"
	BoardObserved                 Kind = "board.observed"
	BoardStateChanged             Kind = "board.state-changed"
	JobCreated                    Kind = "job.created"
	JobUpdated                    Kind = "job.updated"
	JobDeleted                    Kind = "job.deleted"
	JobRunRequested               Kind = "job.run-requested"
	JobRunObserved                Kind = "job.run-observed"
	JobRunCancelRequested         Kind = "job.run-cancel-requested"
	RegisterChangeAttempted       Kind = "register.change-attempted"
	RegisterChangeObserved        Kind = "register.change-observed"
	RegisterBatchAttempted        Kind = "register.batch-attempted"
	RegisterBatchWriteObserved    Kind = "register.batch-write-observed"
	RegisterBatchRollbackObserved Kind = "register.batch-rollback-observed"
	RegisterBatchObserved         Kind = "register.batch-observed"
	DutyCycleConfiguration        Kind = "duty-cycle.configuration"
	DutyCycleTransition           Kind = "duty-cycle.transition"
)

const (
	ActorAdmin  = "admin"
	ActorLocal  = "local-root"
	ActorRemote = "remote-client"
	ActorSystem = "system"
)

type Audit struct {
	mu      sync.Mutex
	journal *Journal
	now     func() time.Time
	lastErr error
}

func NewAudit(journal *Journal) *Audit {
	return &Audit{journal: journal, now: time.Now}
}

// Record is deliberately best-effort after an owning state transaction has
// committed. A failed append is retained for readiness rather than turning a
// successful security change into a misleading failure response.
func (audit *Audit) Record(kind Kind, actor, subject string, details any) {
	if audit == nil {
		return
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if !validKind(kind) || !validActor(actor) {
		audit.lastErr = fmt.Errorf("invalid typed activity event")
		return
	}
	var encoded json.RawMessage
	if details != nil {
		value, err := json.Marshal(details)
		if err != nil {
			audit.lastErr = fmt.Errorf("encode activity details: %w", err)
			return
		}
		encoded = value
	}
	_, err := audit.journal.Append(Event{
		Time: audit.now().UTC(), Type: string(kind), Actor: actor, Subject: subject, Details: encoded,
	})
	audit.lastErr = err
}

func (audit *Audit) Healthy() bool {
	if audit == nil {
		return true
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	return audit.lastErr == nil
}

func validKind(kind Kind) bool {
	switch kind {
	case AuthenticationSetupCompleted, AuthenticationLoginSucceeded, AuthenticationLogout,
		AuthenticationReset, AuthenticationFailures, ServiceStarted, BoardObserved, BoardStateChanged,
		JobCreated, JobUpdated, JobDeleted, JobRunRequested, JobRunObserved, JobRunCancelRequested,
		RegisterChangeAttempted, RegisterChangeObserved, RegisterBatchAttempted,
		RegisterBatchWriteObserved, RegisterBatchRollbackObserved, RegisterBatchObserved,
		DutyCycleConfiguration, DutyCycleTransition:
		return true
	default:
		return false
	}
}

func validActor(actor string) bool {
	switch actor {
	case ActorAdmin, ActorLocal, ActorRemote, ActorSystem:
		return true
	default:
		return false
	}
}

func KnownKinds() []Kind {
	return []Kind{
		AuthenticationSetupCompleted, AuthenticationLoginSucceeded, AuthenticationLogout,
		AuthenticationReset, AuthenticationFailures, ServiceStarted, BoardObserved, BoardStateChanged,
		JobCreated, JobUpdated, JobDeleted, JobRunRequested, JobRunObserved, JobRunCancelRequested,
		RegisterChangeAttempted, RegisterChangeObserved, RegisterBatchAttempted,
		RegisterBatchWriteObserved, RegisterBatchRollbackObserved, RegisterBatchObserved,
		DutyCycleConfiguration, DutyCycleTransition,
	}
}

func KnownActors() []string {
	return []string{ActorAdmin, ActorLocal, ActorRemote, ActorSystem}
}
