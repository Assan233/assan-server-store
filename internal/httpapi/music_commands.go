package httpapi

import (
	"errors"
	"net/http"

	"github.com/Assan233/assan-server-store/internal/auth"
)

func (a *authAPI) enqueueMusicCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := a.store.EnqueueMusicCommand(r.Context(), body.Action); err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalid):
			writeError(w, 400, "invalid_input", "音乐操作无效")
		case errors.Is(err, auth.ErrMusicQueueFull):
			writeError(w, 429, "queue_full", "音乐操作队列已满")
		default:
			writeError(w, 500, "internal_error", "音乐操作入队失败")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *authAPI) nextMusicCommand(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	action, err := a.store.ConsumeMusicCommand(r.Context())
	if err != nil {
		writeError(w, 500, "internal_error", "读取音乐操作失败")
		return
	}
	var value any
	if action != "" {
		value = action
	}
	writeJSON(w, map[string]any{"data": map[string]any{"action": value}})
}
