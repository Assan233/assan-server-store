// Package httpapi defines the application's HTTP boundary.
package httpapi

import (
	"encoding/json"
	"net/http"
)

// NewHandler returns the HTTP API router. Dependencies belong in constructors.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"data": map[string]string{"message": "pong"}})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Responses here contain only JSON-safe static values. The HTTP server owns
	// connection errors; a failed write cannot be replaced by another response.
	_ = json.NewEncoder(w).Encode(value)
}
