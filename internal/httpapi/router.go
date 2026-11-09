// Package httpapi defines the application's HTTP boundary.
package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Assan233/assan-server-store/internal/auth"
)

// NewHandler returns the HTTP API router. Dependencies belong in constructors.
func NewHandler(store *auth.Store, secureCookies, trustProxy bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"data": map[string]string{"message": "pong"}})
	})
	api := &authAPI{store: store, secureCookies: secureCookies, trustProxy: trustProxy, limiter: newRateLimiter()}
	mux.HandleFunc("GET /api/v1/auth/csrf", api.csrf)
	mux.HandleFunc("POST /api/v1/auth/signup", api.csrfProtected(api.limited(api.signup)))
	mux.HandleFunc("POST /api/v1/auth/login", api.csrfProtected(api.limited(api.login)))
	mux.HandleFunc("GET /api/v1/auth/me", api.me)
	mux.HandleFunc("POST /api/v1/auth/logout", api.csrfProtected(api.logout))
	mux.HandleFunc("POST /api/v1/auth/password", api.csrfProtected(api.changePassword))
	return securityHeaders(mux)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Responses here contain only JSON-safe static values. The HTTP server owns
	// connection errors; a failed write cannot be replaced by another response.
	_ = json.NewEncoder(w).Encode(value)
}

type authAPI struct {
	store         *auth.Store
	secureCookies bool
	trustProxy    bool
	limiter       *rateLimiter
}
type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type passwordChange struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (a *authAPI) csrf(w http.ResponseWriter, r *http.Request) {
	if token, err := r.Cookie("assan_csrf"); err == nil && len(token.Value) >= 32 {
		writeJSON(w, map[string]any{"data": map[string]string{"token": token.Value}})
		return
	}
	token, err := randomToken()
	if err != nil {
		writeError(w, 500, "internal_error", "internal server error")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "assan_csrf", Value: token, Path: "/", Secure: a.secureCookies, HttpOnly: false, SameSite: http.SameSiteStrictMode, MaxAge: 86400})
	writeJSON(w, map[string]any{"data": map[string]string{"token": token}})
}
func (a *authAPI) signup(w http.ResponseWriter, r *http.Request) {
	var body credentials
	if !decodeBody(w, r, &body) {
		return
	}
	u, token, err := a.store.Register(r.Context(), body.Username, body.Password)
	if err != nil {
		if errors.Is(err, auth.ErrExists) {
			writeError(w, 409, "username_exists", "username already exists")
		} else if errors.Is(err, auth.ErrInvalid) {
			writeError(w, 400, "invalid_input", "username or password is invalid")
		} else {
			writeError(w, 500, "internal_error", "internal server error")
		}
		return
	}
	a.setSession(w, token)
	writeJSON(w, map[string]any{"data": u})
}
func (a *authAPI) login(w http.ResponseWriter, r *http.Request) {
	var body credentials
	if !decodeBody(w, r, &body) {
		return
	}
	u, token, err := a.store.Login(r.Context(), body.Username, body.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalid) {
			writeError(w, 401, "invalid_credentials", "username or password is incorrect")
		} else {
			writeError(w, 500, "internal_error", "internal server error")
		}
		return
	}
	a.setSession(w, token)
	writeJSON(w, map[string]any{"data": u})
}
func (a *authAPI) me(w http.ResponseWriter, r *http.Request) {
	u, err := a.currentUser(r)
	if err != nil {
		writeError(w, 401, "unauthenticated", "authentication required")
		return
	}
	writeJSON(w, map[string]any{"data": u})
}
func (a *authAPI) logout(w http.ResponseWriter, r *http.Request) {
	if token := sessionToken(r); token != "" {
		if err := a.store.Logout(r.Context(), token); err != nil {
			writeError(w, 500, "internal_error", "internal server error")
			return
		}
	}
	a.clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}
func (a *authAPI) changePassword(w http.ResponseWriter, r *http.Request) {
	u, err := a.currentUser(r)
	if err != nil {
		writeError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var body passwordChange
	if !decodeBody(w, r, &body) {
		return
	}
	if err := a.store.ChangePassword(r.Context(), u.ID, body.CurrentPassword, body.NewPassword); err != nil {
		if errors.Is(err, auth.ErrInvalid) {
			writeError(w, 400, "invalid_password", "current password is incorrect or new password is invalid")
		} else {
			writeError(w, 500, "internal_error", "internal server error")
		}
		return
	}
	a.clearSession(w)
	w.WriteHeader(http.StatusNoContent)
}
func (a *authAPI) currentUser(r *http.Request) (auth.User, error) {
	token := sessionToken(r)
	if token == "" {
		return auth.User{}, auth.ErrUnauth
	}
	return a.store.UserForToken(r.Context(), token)
}
func (a *authAPI) setSession(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: "assan_session", Value: token, Path: "/", Secure: a.secureCookies, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int((7 * 24 * time.Hour).Seconds())})
}
func (a *authAPI) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "assan_session", Value: "", Path: "/", Secure: a.secureCookies, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: "assan_csrf", Value: "", Path: "/", Secure: a.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
func sessionToken(r *http.Request) string {
	c, err := r.Cookie("assan_session")
	if err != nil {
		return ""
	}
	return c.Value
}
func (a *authAPI) csrfProtected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("assan_csrf")
		header := r.Header.Get("X-CSRF-Token")
		if err != nil || len(header) == 0 || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
			writeError(w, 403, "csrf_failed", "request verification failed")
			return
		}
		next(w, r)
	}
}
func (a *authAPI) limited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.limiter.allow(clientIP(r, a.trustProxy)) {
			writeError(w, 429, "rate_limited", "too many attempts; try again later")
			return
		}
		next(w, r)
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		writeError(w, 400, "invalid_json", "request body is invalid")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, 400, "invalid_json", "request body must contain one JSON value")
		return false
	}
	return true
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.WriteHeader(status)
	writeJSON(w, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

type rateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newRateLimiter() *rateLimiter { return &rateLimiter{attempts: map[string][]time.Time{}} }
func (l *rateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-15 * time.Minute)
	valid := l.attempts[ip][:0]
	for _, t := range l.attempts[ip] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) >= 10 {
		l.attempts[ip] = valid
		return false
	}
	l.attempts[ip] = append(valid, now)
	return true
}
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if ip := net.ParseIP(r.Header.Get("X-Real-IP")); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
