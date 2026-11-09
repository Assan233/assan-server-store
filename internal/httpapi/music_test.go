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

func TestMusicCredentialsAreUserScopedAndRequireCSRF(t *testing.T) {
	store, err := auth.Open(context.Background(), filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := []byte("0123456789abcdef0123456789abcdef")
	handler := NewHandler(store, false, false, key)
	request := func(method, path, body string, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		handler.ServeHTTP(w, r)
		return w
	}
	csrfResponse := request("GET", "/api/v1/auth/csrf", "", nil, "")
	csrfCookie := csrfResponse.Result().Cookies()[0]
	var token struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(csrfResponse.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	admin := request("POST", "/api/v1/auth/setup", `{"username":"admin","password":"Password1!"}`, []*http.Cookie{csrfCookie}, token.Data.Token)
	if admin.Code != 200 {
		t.Fatalf("setup: %d %s", admin.Code, admin.Body.String())
	}
	adminCookies := append(admin.Result().Cookies(), csrfCookie)
	if got := request("GET", "/api/v1/music/access", "", nil, "").Code; got != 401 {
		t.Fatalf("anonymous music access: %d", got)
	}
	if got := request("GET", "/api/v1/music/access", "", adminCookies, "").Code; got != 204 {
		t.Fatalf("authenticated music access: %d", got)
	}
	path := "/api/v1/music/credential"
	if got := request("GET", path+"?login", "", adminCookies, "").Code; got != 404 {
		t.Fatalf("unconfigured credential: %d", got)
	}
	body := `{"username":"music-admin","password":"secret-password"}`
	if got := request("POST", path, body, adminCookies, "").Code; got != 403 {
		t.Fatalf("save without CSRF: %d", got)
	}
	if got := request("POST", path, body, adminCookies, token.Data.Token).Code; got != 204 {
		t.Fatalf("save: %d", got)
	}
	status := request("GET", path, "", adminCookies, "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"configured":true`) || strings.Contains(status.Body.String(), "secret-password") {
		t.Fatalf("credential status: %d %s", status.Code, status.Body.String())
	}
	if cache := status.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("credential cache policy: %q", cache)
	}
	if got := request("POST", "/api/v1/admin/users", `{"username":"member","password":"Password1!"}`, adminCookies, token.Data.Token).Code; got != 204 {
		t.Fatalf("create member: %d", got)
	}
	member := request("POST", "/api/v1/auth/login", `{"username":"member","password":"Password1!"}`, []*http.Cookie{csrfCookie}, token.Data.Token)
	if member.Code != 200 {
		t.Fatalf("member login: %d", member.Code)
	}
	memberCookies := append(member.Result().Cookies(), csrfCookie)
	if got := request("GET", path+"?login", "", memberCookies, "").Code; got != 404 {
		t.Fatalf("member sees admin credential: %d", got)
	}
	if got := request("POST", path, `{"username":"music-member","password":"member-secret"}`, memberCookies, token.Data.Token).Code; got != 204 {
		t.Fatalf("member save: %d", got)
	}
	login := request("GET", path+"?login", "", adminCookies, "")
	if login.Code != 200 || !strings.Contains(login.Body.String(), `"username":"music-admin"`) || !strings.Contains(login.Body.String(), `"password":"secret-password"`) || strings.Contains(login.Body.String(), "member-secret") {
		t.Fatalf("admin credential: %d %s", login.Code, login.Body.String())
	}
	if got := request("DELETE", path, "", memberCookies, "").Code; got != 403 {
		t.Fatalf("delete without CSRF: %d", got)
	}
	if got := request("DELETE", path, "", memberCookies, token.Data.Token).Code; got != 204 {
		t.Fatalf("delete member credential: %d", got)
	}
	if got := request("GET", path+"?login", "", memberCookies, "").Code; got != 404 {
		t.Fatalf("deleted member credential: %d", got)
	}
}
