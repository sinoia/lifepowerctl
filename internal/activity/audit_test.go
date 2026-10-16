package activity

import (
	"testing"
	"time"
)

func TestAuditRecordsTypedEvent(t *testing.T) {
	journal, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	audit := NewAudit(journal)
	audit.now = func() time.Time { return time.Date(2026, time.October, 10, 12, 0, 0, 0, time.UTC) }
	audit.Record(AuthenticationLoginSucceeded, ActorAdmin, "web", struct {
		Method string `json:"method"`
	}{Method: "password"})
	if !audit.Healthy() {
		t.Fatal("audit is unhealthy after valid event")
	}
	events, err := journal.Events()
	if err != nil || len(events) != 1 {
		t.Fatalf("Events() = %+v, %v", events, err)
	}
	if events[0].Type != string(AuthenticationLoginSucceeded) || events[0].Actor != ActorAdmin || string(events[0].Details) != `{"method":"password"}` {
		t.Fatalf("event = %+v", events[0])
	}
}

func TestAuditRejectsUntypedValues(t *testing.T) {
	journal, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	audit := NewAudit(journal)
	audit.Record(Kind("invented"), "injected actor", "", nil)
	if audit.Healthy() {
		t.Fatal("audit accepted untyped values")
	}
	events, err := journal.Events()
	if err != nil || len(events) != 0 {
		t.Fatalf("Events() = %+v, %v", events, err)
	}
}
