package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/Assan233/assan-server-store/internal/auth"
	"github.com/Assan233/assan-server-store/internal/video"
)

func (a *authAPI) registerVideo(mux *http.ServeMux, service *video.Service) {
	withUser := func(fn func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			u, err := a.currentUser(r)
			if errors.Is(err, auth.ErrUnauth) {
				writeError(w, 401, "unauthenticated", "请先登录。")
			} else if err != nil {
				writeError(w, 500, "internal_error", "无法验证登录状态。")
			} else {
				fn(w, r, u.ID)
			}
		}
	}
	mux.HandleFunc("POST /api/v1/video/resolve", a.csrfProtected(withUser(func(w http.ResponseWriter, r *http.Request, user int64) {
		var body struct {
			URL string `json:"url"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		episodes, err := service.Resolve(r.Context(), user, body.URL)
		if err != nil {
			writeVideoError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": episodes})
	})))
	mux.HandleFunc("POST /api/v1/video/prepare", a.csrfProtected(withUser(func(w http.ResponseWriter, r *http.Request, user int64) {
		var body struct {
			EpisodeID string `json:"episodeId"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(video.MaxDuration + 10*time.Second))
		id, err := service.Prepare(r.Context(), user, body.EpisodeID)
		if err != nil {
			writeVideoError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": map[string]string{"id": id}})
	})))
	mux.HandleFunc("GET /api/v1/video/file/{id}", withUser(func(w http.ResponseWriter, r *http.Request, user int64) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		file, name, release := service.Take(user, r.PathValue("id"))
		if file == nil {
			writeVideoError(w, video.ErrMissing)
			return
		}
		defer release()
		info, err := file.Stat()
		if err != nil {
			writeError(w, 500, "internal_error", "无法读取生成的视频。")
			return
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Minute))
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		_, _ = io.Copy(w, file)
	}))
}

func writeVideoError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, video.ErrUnavailable):
		writeError(w, 503, "video_unavailable", "服务端尚未配置视频解析服务。")
	case errors.Is(err, video.ErrInput):
		writeError(w, 400, "invalid_video_url", "请输入有效的优酷视频链接。")
	case errors.Is(err, video.ErrMissing):
		writeError(w, 404, "video_expired", "剧集或文件已失效，请重新解析或生成。")
	case errors.Is(err, video.ErrBusy):
		writeError(w, 429, "video_busy", "视频服务正忙，请稍后重试。")
	case errors.Is(err, video.ErrRestricted):
		writeError(w, 502, "video_playback_restricted", "优酷限制了此视频的播放，请换一个可公开播放的视频链接。")
	default:
		writeError(w, 502, "video_upstream_error", "无法解析或生成视频。链接可能已失效、视频不受支持，或下载超过了时间或大小限制。")
	}
}
