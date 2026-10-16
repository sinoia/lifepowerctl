package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/limits"
	"github.com/sinoia/lifepowerctl/internal/storage"
	"golang.org/x/crypto/argon2"
)

const tokenBytes = 32
const sessionTouchInterval = 5 * time.Minute

var ErrSetupUnavailable = errors.New("setup token is invalid or expired")
var ErrAuthentication = errors.New("authentication failed")
var ErrSession = errors.New("session is invalid or expired")
var ErrAlreadyConfigured = errors.New("authentication is already configured")
var ErrSetupPending = errors.New("an unexpired setup token already exists")

type Setup struct {
	Token     string
	ExpiresAt time.Time
}

type Session struct {
	Token     string
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type persistedSetup struct {
	Digest           string `json:"digest"`
	ExpiresUnixMilli int64  `json:"expires_unix_milli"`
}

type persistedSession struct {
	Digest             string `json:"digest"`
	CSRFToken          string `json:"csrf_token"`
	CreatedUnixMilli   int64  `json:"created_unix_milli"`
	LastSeenUnixMilli  int64  `json:"last_seen_unix_milli"`
	PersistedUnixMilli int64  `json:"persisted_unix_milli"`
}

type authState struct {
	Password *PasswordEnvelope  `json:"password,omitempty"`
	Setup    *persistedSetup    `json:"setup,omitempty"`
	Sessions []persistedSession `json:"sessions,omitempty"`
}

type Manager struct {
	mu       sync.Mutex
	limits   limits.Authentication
	store    *storage.Store
	revision uint64
	state    authState
	now      func() time.Time
	random   io.Reader
	audit    *activity.Audit
}

func (manager *Manager) SetAudit(audit *activity.Audit) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.audit = audit
}

func NewManager(dir string, authLimits limits.Authentication) (*Manager, error) {
	if authLimits.MaxSessions < 1 || authLimits.IdleLifetime <= 0 || authLimits.AbsoluteLifetime < authLimits.IdleLifetime || authLimits.SetupLifetime <= 0 {
		return nil, fmt.Errorf("invalid authentication limits")
	}
	store, err := storage.New(dir, "auth")
	if err != nil {
		return nil, err
	}
	manager := &Manager{limits: authLimits, store: store, now: time.Now, random: rand.Reader}
	snapshot, err := store.Load()
	if errors.Is(err, storage.ErrNotFound) {
		return manager, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load authentication state: %w", err)
	}
	state, err := decodeAuthState(snapshot.Payload, authLimits)
	if err != nil {
		return nil, err
	}
	if snapshot.Recovered {
		// A previous snapshot can predate logout or setup-token replacement.
		// Preserve the credential but never resurrect bearer authority.
		state.Setup = nil
		state.Sessions = nil
		saved, err := store.Save(snapshot.Revision, state)
		if err != nil {
			return nil, fmt.Errorf("secure recovered authentication state: %w", err)
		}
		manager.state = state
		manager.revision = saved.Revision
		return manager, nil
	}
	manager.state = state
	manager.revision = snapshot.Revision
	return manager, nil
}

func (manager *Manager) IsConfigured() bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.state.Password != nil
}

// EnsureSetup creates the first setup token only when authentication has not
// already been configured and no usable setup token is pending. It is safe for
// installers because it never replaces credentials, sessions, or a token that
// may already have been shown to the owner.
func (manager *Manager) EnsureSetup() (Setup, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	now := manager.now().UTC()
	if manager.state.Password != nil {
		return Setup{}, ErrAlreadyConfigured
	}
	if manager.state.Setup != nil && now.UnixMilli() < manager.state.Setup.ExpiresUnixMilli {
		return Setup{}, ErrSetupPending
	}
	token, err := randomToken(manager.random)
	if err != nil {
		return Setup{}, err
	}
	expires := now.Add(manager.limits.SetupLifetime)
	next := authState{Setup: &persistedSetup{Digest: tokenDigest(token), ExpiresUnixMilli: expires.UnixMilli()}}
	if err := manager.saveLocked(next); err != nil {
		return Setup{}, err
	}
	return Setup{Token: token, ExpiresAt: expires}, nil
}

// ResetSetup invalidates the current credential, sessions, and previous setup
// token before returning one new short-lived token.
func (manager *Manager) ResetSetup() (Setup, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	token, err := randomToken(manager.random)
	if err != nil {
		return Setup{}, err
	}
	now := manager.now().UTC()
	expires := now.Add(manager.limits.SetupLifetime)
	next := authState{Setup: &persistedSetup{Digest: tokenDigest(token), ExpiresUnixMilli: expires.UnixMilli()}}
	if err := manager.saveLocked(next); err != nil {
		return Setup{}, err
	}
	return Setup{Token: token, ExpiresAt: expires}, nil
}

func (manager *Manager) CompleteSetup(token, password string) (Session, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	now := manager.now().UTC()
	if manager.state.Setup == nil || now.UnixMilli() >= manager.state.Setup.ExpiresUnixMilli || !equalDigest(manager.state.Setup.Digest, tokenDigest(token)) {
		return Session{}, ErrSetupUnavailable
	}
	envelope, err := hashPassword(password, manager.random)
	if err != nil {
		return Session{}, err
	}
	session, stored, err := manager.newSession(now)
	if err != nil {
		return Session{}, err
	}
	next := authState{Password: &envelope, Sessions: []persistedSession{stored}}
	if err := manager.saveLocked(next); err != nil {
		return Session{}, err
	}
	manager.audit.Record(activity.AuthenticationSetupCompleted, activity.ActorAdmin, "web", nil)
	return session, nil
}

func (manager *Manager) Login(password string) (Session, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.state.Password == nil {
		burnPasswordVerification(password)
		return Session{}, ErrAuthentication
	}
	matched, err := VerifyPassword(*manager.state.Password, password)
	if err != nil {
		return Session{}, err
	}
	if !matched {
		return Session{}, ErrAuthentication
	}
	now := manager.now().UTC()
	next := cloneAuthState(manager.state)
	next.Sessions = activeSessions(next.Sessions, now, manager.limits)
	if len(next.Sessions) >= manager.limits.MaxSessions {
		next.Sessions = next.Sessions[1:]
	}
	session, stored, err := manager.newSession(now)
	if err != nil {
		return Session{}, err
	}
	next.Sessions = append(next.Sessions, stored)
	if err := manager.saveLocked(next); err != nil {
		return Session{}, err
	}
	manager.audit.Record(activity.AuthenticationLoginSucceeded, activity.ActorAdmin, "web", nil)
	return session, nil
}

func (manager *Manager) Authenticate(token string) (Session, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	now := manager.now().UTC()
	digest := tokenDigest(token)
	next := cloneAuthState(manager.state)
	next.Sessions = activeSessions(next.Sessions, now, manager.limits)
	index := -1
	for candidate := range next.Sessions {
		if equalDigest(next.Sessions[candidate].Digest, digest) {
			index = candidate
			break
		}
	}
	if index < 0 {
		if len(next.Sessions) != len(manager.state.Sessions) {
			_ = manager.saveLocked(next)
		}
		return Session{}, ErrSession
	}
	stored := &next.Sessions[index]
	stored.LastSeenUnixMilli = now.UnixMilli()
	if now.UnixMilli()-stored.PersistedUnixMilli >= sessionTouchInterval.Milliseconds() {
		stored.PersistedUnixMilli = now.UnixMilli()
		if err := manager.saveLocked(next); err != nil {
			return Session{}, err
		}
	} else {
		manager.state = next
	}
	return sessionView(token, *stored, manager.limits), nil
}

func (manager *Manager) CheckCSRF(sessionToken, csrfToken string) error {
	session, err := manager.Authenticate(sessionToken)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(session.CSRFToken), []byte(csrfToken)) != 1 {
		return ErrSession
	}
	return nil
}

func (manager *Manager) Logout(token string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	digest := tokenDigest(token)
	next := cloneAuthState(manager.state)
	kept := next.Sessions[:0]
	found := false
	for _, session := range next.Sessions {
		if equalDigest(session.Digest, digest) {
			found = true
			continue
		}
		kept = append(kept, session)
	}
	if !found {
		return ErrSession
	}
	next.Sessions = kept
	if err := manager.saveLocked(next); err != nil {
		return err
	}
	manager.audit.Record(activity.AuthenticationLogout, activity.ActorAdmin, "web", nil)
	return nil
}

func (manager *Manager) newSession(now time.Time) (Session, persistedSession, error) {
	token, err := randomToken(manager.random)
	if err != nil {
		return Session{}, persistedSession{}, err
	}
	csrf, err := randomToken(manager.random)
	if err != nil {
		return Session{}, persistedSession{}, err
	}
	stored := persistedSession{
		Digest: tokenDigest(token), CSRFToken: csrf,
		CreatedUnixMilli: now.UnixMilli(), LastSeenUnixMilli: now.UnixMilli(), PersistedUnixMilli: now.UnixMilli(),
	}
	return sessionView(token, stored, manager.limits), stored, nil
}

func (manager *Manager) saveLocked(next authState) error {
	saved, err := manager.store.Save(manager.revision, next)
	if err != nil {
		return fmt.Errorf("save authentication state: %w", err)
	}
	manager.state = next
	manager.revision = saved.Revision
	return nil
}

func activeSessions(sessions []persistedSession, now time.Time, authLimits limits.Authentication) []persistedSession {
	active := make([]persistedSession, 0, len(sessions))
	for _, session := range sessions {
		if now.UnixMilli()-session.LastSeenUnixMilli >= authLimits.IdleLifetime.Milliseconds() || now.UnixMilli()-session.CreatedUnixMilli >= authLimits.AbsoluteLifetime.Milliseconds() {
			continue
		}
		active = append(active, session)
	}
	return active
}

func sessionView(token string, stored persistedSession, authLimits limits.Authentication) Session {
	absolute := time.UnixMilli(stored.CreatedUnixMilli).Add(authLimits.AbsoluteLifetime)
	idle := time.UnixMilli(stored.LastSeenUnixMilli).Add(authLimits.IdleLifetime)
	if idle.Before(absolute) {
		absolute = idle
	}
	return Session{Token: token, CSRFToken: stored.CSRFToken, CreatedAt: time.UnixMilli(stored.CreatedUnixMilli), ExpiresAt: absolute}
}

func randomToken(random io.Reader) (string, error) {
	value := make([]byte, tokenBytes)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", fmt.Errorf("generate authentication token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func tokenDigest(token string) string {
	// Session tokens contain 256 bits from a cryptographic random source. This
	// digest is a one-way lookup value for those high-entropy bearer tokens, not
	// a password hash.
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func equalDigest(first, second string) bool {
	return subtle.ConstantTimeCompare([]byte(first), []byte(second)) == 1
}

func cloneAuthState(value authState) authState {
	clone := value
	if value.Password != nil {
		password := *value.Password
		clone.Password = &password
	}
	if value.Setup != nil {
		setup := *value.Setup
		clone.Setup = &setup
	}
	clone.Sessions = append([]persistedSession(nil), value.Sessions...)
	return clone
}

func decodeAuthState(payload []byte, authLimits limits.Authentication) (authState, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var state authState
	if err := decoder.Decode(&state); err != nil {
		return authState{}, fmt.Errorf("decode authentication state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return authState{}, fmt.Errorf("decode authentication state: trailing data")
	}
	if state.Password != nil {
		if err := validateEnvelope(*state.Password); err != nil {
			return authState{}, err
		}
	}
	if state.Setup != nil && (state.Setup.Digest == "" || state.Setup.ExpiresUnixMilli <= 0) {
		return authState{}, fmt.Errorf("invalid persisted setup token")
	}
	if state.Password == nil && len(state.Sessions) != 0 {
		return authState{}, fmt.Errorf("sessions exist without a configured credential")
	}
	if len(state.Sessions) > authLimits.MaxSessions {
		return authState{}, fmt.Errorf("too many persisted sessions")
	}
	for _, session := range state.Sessions {
		if session.Digest == "" || session.CSRFToken == "" || session.CreatedUnixMilli <= 0 || session.LastSeenUnixMilli < session.CreatedUnixMilli || session.PersistedUnixMilli < session.CreatedUnixMilli {
			return authState{}, fmt.Errorf("invalid persisted session")
		}
	}
	return state, nil
}

func burnPasswordVerification(password string) {
	candidate := password
	if ValidatePassword(candidate) != nil {
		candidate = "invalid password placeholder"
	}
	salt := make([]byte, Argon2SaltBytes)
	want := make([]byte, Argon2KeyBytes)
	got := argon2.IDKey([]byte(candidate), salt, Argon2Passes, Argon2MemoryKiB, Argon2Lanes, Argon2KeyBytes)
	_ = subtle.ConstantTimeCompare(got, want)
}
