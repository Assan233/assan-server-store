package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
			NewHandler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
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
