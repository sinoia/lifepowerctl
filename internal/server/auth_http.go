package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/auth"
)

const (
	sessionCookie     = "lifepowerctl_session"
	loginCSRFCookie   = "lifepowerctl_login_csrf"
	setupCSRFCookie   = "lifepowerctl_setup_csrf"
	preAuthTokenBytes = 32
	maxFormFields     = 16
)

type contextKey byte

const sessionContextKey contextKey = 1

type authPageData struct {
	Title      string
	Build      BuildInfo
	CSRFToken  string
	SetupToken string
	Error      string
}

func (app *application) setupPage(response http.ResponseWriter, request *http.Request) {
	if app.authentication.IsConfigured() {
		http.Redirect(response, request, "/login", http.StatusSeeOther)
		return
	}
	csrf, err := app.preAuthCookie(response, request, setupCSRFCookie)
	if err != nil {
		app.errorPage(response, http.StatusInternalServerError, "Setup unavailable", "The setup form could not be created. Try again.")
		return
	}
	app.render(response, "setup", authPageData{Title: "Set up owner", Build: app.build, CSRFToken: csrf, SetupToken: request.URL.Query().Get("token")})
}

func (app *application) completeSetup(response http.ResponseWriter, request *http.Request) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return
	}
	form, ok := app.parseForm(response, request, "csrf", "setup_token", "password", "password_confirmation")
	if !ok {
		return
	}
	if !app.validPreAuthCSRF(request, setupCSRFCookie, form.Get("csrf")) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return
	}
	source := app.sourceAddress(request)
	if retry := app.setupBackoff.retryAfter(source); retry > 0 {
		setRetryAfter(response, retry)
		app.errorPage(response, http.StatusTooManyRequests, "Setup temporarily delayed", "Wait for the indicated retry interval, then submit the form again.")
		return
	}
	if form.Get("password") != form.Get("password_confirmation") {
		app.recordAuthenticationFailure("setup", app.setupBackoff.failed(source))
		app.renderSetupError(response, form.Get("setup_token"), "Passwords do not match.")
		return
	}
	session, err := app.authentication.CompleteSetup(form.Get("setup_token"), form.Get("password"))
	if err != nil {
		app.recordAuthenticationFailure("setup", app.setupBackoff.failed(source))
		message := "Setup could not be completed. Generate a new setup link locally if it has expired."
		if errors.Is(err, auth.ErrInvalidPassword) {
			message = "Use at least 12 characters and no more than 256 encoded bytes."
		}
		app.renderSetupError(response, form.Get("setup_token"), message)
		return
	}
	app.setupBackoff.succeeded(source)
	app.setSessionCookie(response, session)
	app.clearCookie(response, setupCSRFCookie)
	http.Redirect(response, request, "/", http.StatusSeeOther)
}

func (app *application) renderSetupError(response http.ResponseWriter, setupToken, message string) {
	csrf, err := app.issuePreAuthCookie(response, setupCSRFCookie)
	if err != nil {
		app.errorPage(response, http.StatusInternalServerError, "Setup unavailable", "The setup form could not be created. Try again.")
		return
	}
	app.renderStatus(response, http.StatusUnprocessableEntity, "setup", authPageData{Title: "Set up owner", Build: app.build, CSRFToken: csrf, SetupToken: setupToken, Error: message})
}

func (app *application) loginPage(response http.ResponseWriter, request *http.Request) {
	if !app.authentication.IsConfigured() {
		http.Redirect(response, request, "/setup", http.StatusSeeOther)
		return
	}
	csrf, err := app.preAuthCookie(response, request, loginCSRFCookie)
	if err != nil {
		app.errorPage(response, http.StatusInternalServerError, "Sign-in unavailable", "The sign-in form could not be created. Try again.")
		return
	}
	app.render(response, "login", authPageData{Title: "Sign in", Build: app.build, CSRFToken: csrf})
}

func (app *application) login(response http.ResponseWriter, request *http.Request) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return
	}
	form, ok := app.parseForm(response, request, "csrf", "password")
	if !ok {
		return
	}
	if !app.validPreAuthCSRF(request, loginCSRFCookie, form.Get("csrf")) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return
	}
	source := app.sourceAddress(request)
	if retry := app.loginBackoff.retryAfter(source); retry > 0 {
		setRetryAfter(response, retry)
		app.errorPage(response, http.StatusTooManyRequests, "Sign-in temporarily delayed", "Wait for the indicated retry interval, then submit the form again.")
		return
	}
	session, err := app.authentication.Login(form.Get("password"))
	if err != nil {
		app.recordAuthenticationFailure("login", app.loginBackoff.failed(source))
		csrf, csrfErr := app.issuePreAuthCookie(response, loginCSRFCookie)
		if csrfErr != nil {
			app.errorPage(response, http.StatusInternalServerError, "Sign-in unavailable", "The sign-in form could not be created. Try again.")
			return
		}
		app.renderStatus(response, http.StatusUnauthorized, "login", authPageData{Title: "Sign in", Build: app.build, CSRFToken: csrf, Error: "Sign-in failed. Check the password and try again."})
		return
	}
	app.loginBackoff.succeeded(source)
	app.setSessionCookie(response, session)
	app.clearCookie(response, loginCSRFCookie)
	http.Redirect(response, request, "/", http.StatusSeeOther)
}

func (app *application) logout(response http.ResponseWriter, request *http.Request) {
	if !app.validMutation(request) {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return
	}
	cookie, err := request.Cookie(sessionCookie)
	if err != nil {
		app.errorPage(response, http.StatusUnauthorized, "Authentication required", "Sign in before performing this action.")
		return
	}
	form, ok := app.parseForm(response, request, "csrf")
	if !ok {
		return
	}
	if err := app.authentication.CheckCSRF(cookie.Value, form.Get("csrf")); err != nil {
		app.errorPage(response, http.StatusForbidden, "Request rejected", "The request could not be verified. Return to the form and try again.")
		return
	}
	if err := app.authentication.Logout(cookie.Value); err != nil && !errors.Is(err, auth.ErrSession) {
		app.errorPage(response, http.StatusInternalServerError, "Sign-out unavailable", "The session could not be closed. Try again.")
		return
	}
	app.clearCookie(response, sessionCookie)
	http.Redirect(response, request, "/login", http.StatusSeeOther)
}

func (app *application) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(response, request, "/login", http.StatusSeeOther)
			return
		}
		session, err := app.authentication.Authenticate(cookie.Value)
		if err != nil {
			app.clearCookie(response, sessionCookie)
			http.Redirect(response, request, "/login", http.StatusSeeOther)
			return
		}
		app.setSessionCookie(response, session)
		ctx := context.WithValue(request.Context(), sessionContextKey, session)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func sessionFromContext(ctx context.Context) auth.Session {
	session, _ := ctx.Value(sessionContextKey).(auth.Session)
	return session
}

func (app *application) validMutation(request *http.Request) bool {
	if supplied := request.Header.Get("Origin"); supplied != "" {
		// Some privacy-focused browsers serialize an opaque origin as "null".
		// The exact Host check and the per-form CSRF cookie/token comparison still
		// bind the mutation to a form served by this application.
		if supplied == "null" {
			return strings.EqualFold(request.Host, app.origin.Host)
		}
		return sameOriginURL(supplied, app.origin)
	}
	referer, err := url.Parse(request.Header.Get("Referer"))
	if err != nil || referer.User != nil || referer.Scheme == "" || referer.Host == "" {
		return false
	}
	return strings.EqualFold(referer.Scheme, app.origin.Scheme) && strings.EqualFold(referer.Host, app.origin.Host)
}

func sameOriginURL(supplied string, expected *url.URL) bool {
	parsed, err := url.Parse(supplied)
	return err == nil && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && strings.EqualFold(parsed.Scheme, expected.Scheme) && strings.EqualFold(parsed.Host, expected.Host)
}

func (app *application) parseForm(response http.ResponseWriter, request *http.Request, fields ...string) (url.Values, bool) {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(request.Header.Get("Content-Type"), ";", 2)[0]))
	if mediaType != "application/x-www-form-urlencoded" {
		app.errorPage(response, http.StatusUnsupportedMediaType, "Unsupported form", "Submit the application-generated form without changing its encoding.")
		return nil, false
	}
	request.Body = http.MaxBytesReader(response, request.Body, app.maxBodyBytes)
	if err := request.ParseForm(); err != nil {
		app.errorPage(response, http.StatusBadRequest, "Invalid form", "The submitted form was invalid or too large.")
		return nil, false
	}
	if len(request.PostForm) > maxFormFields || len(request.PostForm) != len(fields) {
		app.errorPage(response, http.StatusBadRequest, "Invalid form", "The submitted form contained unexpected fields.")
		return nil, false
	}
	for _, field := range fields {
		values, exists := request.PostForm[field]
		if !exists || len(values) != 1 {
			app.errorPage(response, http.StatusBadRequest, "Invalid form", "The submitted form contained missing or repeated fields.")
			return nil, false
		}
	}
	return request.PostForm, true
}

func (app *application) issuePreAuthCookie(response http.ResponseWriter, name string) (string, error) {
	value := make([]byte, preAuthTokenBytes)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate form token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(value)
	app.setCookie(response, name, token, time.Now().Add(15*time.Minute))
	return token, nil
}

func (app *application) preAuthCookie(response http.ResponseWriter, request *http.Request, name string) (string, error) {
	if cookie, err := request.Cookie(name); err == nil {
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(cookie.Value)
		if decodeErr == nil && len(decoded) == preAuthTokenBytes {
			return cookie.Value, nil
		}
	}
	return app.issuePreAuthCookie(response, name)
}

func (app *application) validPreAuthCSRF(request *http.Request, name, submitted string) bool {
	cookie, err := request.Cookie(name)
	return err == nil && len(submitted) > 0 && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(submitted)) == 1
}

func (app *application) setSessionCookie(response http.ResponseWriter, session auth.Session) {
	app.setCookie(response, sessionCookie, session.Token, session.ExpiresAt)
}

func (app *application) setCookie(response http.ResponseWriter, name, value string, expires time.Time) {
	http.SetCookie(response, &http.Cookie{Name: name, Value: value, Path: "/", Expires: expires, HttpOnly: true, Secure: app.secureCookies, SameSite: http.SameSiteLaxMode})
}

func (app *application) clearCookie(response http.ResponseWriter, name string) {
	http.SetCookie(response, &http.Cookie{Name: name, Path: "/", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: app.secureCookies, SameSite: http.SameSiteLaxMode})
}

func (app *application) sourceAddress(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	peer, _ := netip.ParseAddr(host)
	if app.trustedProxy.IsValid() && peer == app.trustedProxy {
		if address, err := netip.ParseAddr(strings.TrimSpace(request.Header.Get("X-Forwarded-For"))); err == nil {
			return address.String()
		}
	}
	if peer.IsValid() {
		return peer.String()
	}
	return "unknown"
}

type backoffEntry struct {
	failures int
	next     time.Time
	seen     time.Time
}

type backoff struct {
	mu      sync.Mutex
	now     func() time.Time
	sources map[string]backoffEntry
	global  backoffEntry
}

func newBackoff(now func() time.Time) *backoff {
	return &backoff{now: now, sources: make(map[string]backoffEntry)}
}

func (value *backoff) retryAfter(source string) time.Duration {
	value.mu.Lock()
	defer value.mu.Unlock()
	now := value.now()
	value.prune(now)
	until := value.sources[source].next
	if value.global.next.After(until) {
		until = value.global.next
	}
	if until.After(now) {
		return until.Sub(now)
	}
	return 0
}

type failureCounts struct {
	Source int `json:"source_count"`
	Global int `json:"global_count"`
}

func (value *backoff) failed(source string) failureCounts {
	value.mu.Lock()
	defer value.mu.Unlock()
	now := value.now()
	entry := value.sources[source]
	if now.Sub(entry.seen) > time.Minute {
		entry = backoffEntry{}
	}
	entry.failures++
	entry.seen = now
	entry.next = now.Add(backoffDelay(entry.failures, 5))
	value.sources[source] = entry
	if now.Sub(value.global.seen) > time.Minute {
		value.global = backoffEntry{}
	}
	value.global.failures++
	value.global.seen = now
	value.global.next = now.Add(backoffDelay(value.global.failures, 20))
	value.prune(now)
	return failureCounts{Source: entry.failures, Global: value.global.failures}
}

func setRetryAfter(response http.ResponseWriter, retry time.Duration) {
	seconds := int((retry + time.Second - 1) / time.Second)
	response.Header().Set("Retry-After", strconv.Itoa(seconds))
}

func (app *application) recordAuthenticationFailure(subject string, counts failureCounts) {
	if backoffMilestone(counts.Source, 5) || backoffMilestone(counts.Global, 20) {
		app.audit.Record(activity.AuthenticationFailures, activity.ActorRemote, subject, counts)
	}
}

func backoffMilestone(count, threshold int) bool {
	return count >= threshold && count <= threshold+5
}

func (value *backoff) succeeded(source string) {
	value.mu.Lock()
	defer value.mu.Unlock()
	delete(value.sources, source)
}

func (value *backoff) prune(now time.Time) {
	for source, entry := range value.sources {
		if now.Sub(entry.seen) > 15*time.Minute {
			delete(value.sources, source)
		}
	}
	if len(value.sources) > 1024 {
		for source := range value.sources {
			delete(value.sources, source)
			if len(value.sources) <= 768 {
				break
			}
		}
	}
	if now.Sub(value.global.seen) > time.Minute {
		value.global = backoffEntry{}
	}
}

func backoffDelay(failures, threshold int) time.Duration {
	if failures < threshold {
		return 0
	}
	shift := failures - threshold
	if shift > 5 {
		shift = 5
	}
	return time.Second * time.Duration(1<<shift)
}
