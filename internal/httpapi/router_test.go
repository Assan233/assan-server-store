package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Assan233/assan-server-store/internal/auth"
)

func TestClientIPTrustsOnlyValidProxyAddressWhenEnabled(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.RemoteAddr = "10.0.0.4:12345"
	request.Header.Set("X-Real-IP", "203.0.113.7")
	if got := clientIP(request, false); got != "10.0.0.4" {
		t.Fatalf("clientIP(untrusted proxy) = %q; want remote IP", got)
	}
	if got := clientIP(request, true); got != "203.0.113.7" {
		t.Fatalf("clientIP(trusted proxy) = %q; want forwarded IP", got)
	}
	request.Header.Set("X-Real-IP", "spoofed")
	if got := clientIP(request, true); got != "10.0.0.4" {
		t.Fatalf("clientIP(invalid forwarded IP) = %q; want remote IP", got)
	}
}

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := auth.Open(context.Background(), filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return NewHandler(store, false, false)
}

func TestSignupRequiresCSRFAndCreatesSession(t *testing.T) {
	handler := testHandler(t)
	request := func(method, path, body string, cookies []*http.Cookie, token string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		if token != "" {
			req.Header.Set("X-CSRF-Token", token)
		}
		handler.ServeHTTP(w, req)
		return w
	}
	if got := request("POST", "/api/v1/auth/signup", `{"username":"testuser","password":"long-enough-password"}`, nil, "").Code; got != http.StatusForbidden {
		t.Fatalf("signup without CSRF = %d; want 403", got)
	}
	csrfResponse := request("GET", "/api/v1/auth/csrf", "", nil, "")
	csrfCookie := csrfResponse.Result().Cookies()[0]
	var csrfBody struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(csrfResponse.Body.Bytes(), &csrfBody); err != nil {
		t.Fatal(err)
	}
	created := request("POST", "/api/v1/auth/signup", `{"username":"testuser","password":"long-enough-password"}`, []*http.Cookie{csrfCookie}, csrfBody.Data.Token)
	if created.Code != http.StatusOK {
		t.Fatalf("signup = %d: %s", created.Code, created.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range created.Result().Cookies() {
		if cookie.Name == "assan_session" {
			session = cookie
		}
	}
	if session == nil || !session.HttpOnly {
		t.Fatal("signup must set an HttpOnly session cookie")
	}
	me := request("GET", "/api/v1/auth/me", "", []*http.Cookie{session}, "")
	if me.Code != http.StatusOK {
		t.Fatalf("authenticated me = %d: %s", me.Code, me.Body.String())
	}
	if got := request("POST", "/api/v1/auth/logout", "{}", []*http.Cookie{session}, "").Code; got != http.StatusForbidden {
		t.Fatalf("logout without CSRF = %d; want 403", got)
	}
}

func TestRoutes(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/healthz", 200}, {"GET", "/api/v1/ping", 200},
		{"HEAD", "/healthz", 200}, {"POST", "/api/v1/ping", 405},
		{"GET", "/unknown", 404}, {"GET", "/healthz/extra", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			testHandler(t).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("status = %d; want %d", w.Code, tc.status)
			}
			if tc.status == http.StatusOK {
				if w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
					t.Fatal("expected JSON content type")
				}
				var body map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if tc.path == "/healthz" && body["status"] != "ok" {
					t.Fatalf("unexpected health: %v", body)
				}
				if tc.path == "/api/v1/ping" {
					data, ok := body["data"].(map[string]any)
					if !ok || data["message"] != "pong" {
						t.Fatalf("unexpected ping: %v", body)
					}
				}
			}
		})
	}
}
