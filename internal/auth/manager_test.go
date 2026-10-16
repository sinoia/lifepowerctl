package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/limits"
)

type sequenceReader byte

func (reader *sequenceReader) Read(value []byte) (int, error) {
	for index := range value {
		*reader = *reader + 1
		value[index] = byte(*reader)
	}
	return len(value), nil
}

func testManager(t *testing.T) (*Manager, *time.Time, string) {
	t.Helper()
	now := time.Date(2026, time.October, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	manager, err := NewManager(dir, limits.Default().Authentication)
	if err != nil {
		t.Fatal(err)
	}
	random := sequenceReader(0)
	manager.random = &random
	manager.now = func() time.Time { return now }
	return manager, &now, dir
}

func TestSetupSessionPersistenceAndLogout(t *testing.T) {
	manager, _, dir := testManager(t)
	setup, err := manager.ResetSetup()
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), setup.Token) {
		t.Fatal("persisted setup state contains the bearer token")
	}

	session, err := manager.CompleteSetup(setup.Token, "a correct long passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.IsConfigured() {
		t.Fatal("manager is not configured after setup")
	}
	stored, err = os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), session.Token) {
		t.Fatal("persisted session state contains the bearer token")
	}
	if _, err := manager.CompleteSetup(setup.Token, "a correct long passphrase"); !errors.Is(err, ErrSetupUnavailable) {
		t.Fatalf("reused setup token error = %v", err)
	}
	if _, err := manager.Authenticate(session.Token); err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckCSRF(session.Token, session.CSRFToken); err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckCSRF(session.Token, "wrong"); !errors.Is(err, ErrSession) {
		t.Fatalf("wrong CSRF error = %v", err)
	}
	if _, err := manager.Login("an incorrect passphrase"); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("incorrect login error = %v", err)
	}
	loginSession, err := manager.Login("a correct long passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(loginSession.Token); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewManager(dir, limits.Default().Authentication)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.now = manager.now
	if _, err := reloaded.Authenticate(session.Token); err != nil {
		t.Fatalf("authenticate after restart: %v", err)
	}
	if err := reloaded.Logout(session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Authenticate(session.Token); !errors.Is(err, ErrSession) {
		t.Fatalf("logged-out session error = %v", err)
	}
}

func TestRecoveredStateDoesNotResurrectSession(t *testing.T) {
	manager, _, dir := testManager(t)
	setup, err := manager.ResetSetup()
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.CompleteSetup(setup.Token, "a correct long passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Logout(session.Token); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("damaged\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	recovered, err := NewManager(dir, limits.Default().Authentication)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.IsConfigured() {
		t.Fatal("recovery discarded the password credential")
	}
	if _, err := recovered.Authenticate(session.Token); !errors.Is(err, ErrSession) {
		t.Fatalf("session recovered from previous snapshot: %v", err)
	}
}

func TestSetupAndSessionExpiry(t *testing.T) {
	manager, now, _ := testManager(t)
	setup, err := manager.ResetSetup()
	if err != nil {
		t.Fatal(err)
	}
	*now = setup.ExpiresAt
	if _, err := manager.CompleteSetup(setup.Token, "a correct long passphrase"); !errors.Is(err, ErrSetupUnavailable) {
		t.Fatalf("expired setup error = %v", err)
	}

	*now = now.Add(-manager.limits.SetupLifetime)
	setup, err = manager.ResetSetup()
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.CompleteSetup(setup.Token, "a correct long passphrase")
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(manager.limits.IdleLifetime)
	if _, err := manager.Authenticate(session.Token); !errors.Is(err, ErrSession) {
		t.Fatalf("expired session error = %v", err)
	}
}

func TestResetSetupInvalidatesCredentialAndSessions(t *testing.T) {
	manager, _, _ := testManager(t)
	setup, err := manager.ResetSetup()
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.CompleteSetup(setup.Token, "a correct long passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ResetSetup(); err != nil {
		t.Fatal(err)
	}
	if manager.IsConfigured() {
		t.Fatal("credential survived authentication reset")
	}
	if _, err := manager.Authenticate(session.Token); !errors.Is(err, ErrSession) {
		t.Fatalf("old session error = %v", err)
	}
}

func TestEnsureSetupNeverReplacesPendingOrConfiguredAuthentication(t *testing.T) {
	manager, _, _ := testManager(t)
	setup, err := manager.EnsureSetup()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnsureSetup(); !errors.Is(err, ErrSetupPending) {
		t.Fatalf("pending setup error = %v", err)
	}
	if _, err := manager.CompleteSetup(setup.Token, "a correct long passphrase"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnsureSetup(); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("configured setup error = %v", err)
	}

	second, expiry, _ := testManager(t)
	old, err := second.EnsureSetup()
	if err != nil {
		t.Fatal(err)
	}
	*expiry = old.ExpiresAt
	replacement, err := second.EnsureSetup()
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Token == old.Token {
		t.Fatal("expired setup token was reused")
	}
}

func TestAuthenticationEmitsTypedActivity(t *testing.T) {
	manager, _, _ := testManager(t)
	journal, err := activity.Open(t.TempDir(), activity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	manager.SetAudit(activity.NewAudit(journal))
	setup, err := manager.ResetSetup()
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.CompleteSetup(setup.Token, "a correct long passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Logout(session.Token); err != nil {
		t.Fatal(err)
	}
	events, err := journal.Events()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != string(activity.AuthenticationSetupCompleted) || events[1].Type != string(activity.AuthenticationLogout) {
		t.Fatalf("authentication events = %+v", events)
	}
}
