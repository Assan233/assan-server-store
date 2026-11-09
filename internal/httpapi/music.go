package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
)

func (a *authAPI) musicAccess(w http.ResponseWriter, r *http.Request) {
	if _, err := a.currentUser(r); err != nil {
		writeError(w, 401, "unauthenticated", "请先登录")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *authAPI) musicUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if len(a.musicKey) != 32 {
		writeError(w, 503, "music_unavailable", "请配置音乐凭据加密密钥")
		return 0, false
	}
	u, err := a.currentUser(r)
	if err != nil {
		writeError(w, 401, "unauthenticated", "请先登录")
		return 0, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return u.ID, true
}

func (a *authAPI) musicCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := a.musicUser(w, r)
	if !ok {
		return
	}
	// Only the iframe's login document requests the password. Same-origin
	// player scripts have the same privileges as this application by design.
	if r.URL.Query().Has("login") {
		username, password, err := a.store.MusicCredential(r.Context(), id, a.musicKey)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "not_configured", "请先保存音乐账号")
			return
		}
		if err != nil {
			writeError(w, 500, "internal_error", "无法读取音乐账号")
			return
		}
		writeJSON(w, map[string]any{"data": map[string]string{"username": username, "password": password}})
		return
	}
	has, err := a.store.HasMusicCredential(r.Context(), id)
	if err != nil {
		writeError(w, 500, "internal_error", "无法读取音乐账号状态")
		return
	}
	writeJSON(w, map[string]any{"data": map[string]bool{"configured": has}})
}

func (a *authAPI) saveMusicCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := a.musicUser(w, r)
	if !ok {
		return
	}
	var body credentials
	if !decodeBody(w, r, &body) {
		return
	}
	if err := a.store.SaveMusicCredential(r.Context(), id, body.Username, body.Password, a.musicKey); err != nil {
		writeError(w, 400, "invalid_input", "音乐账号或密码无效")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *authAPI) deleteMusicCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := a.musicUser(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteMusicCredential(r.Context(), id); err != nil {
		writeError(w, 500, "internal_error", "删除音乐账号失败")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
