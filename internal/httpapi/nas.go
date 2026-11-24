package httpapi

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"syscall"
	"time"

	"github.com/Assan233/assan-server-store/internal/nas"
)

func (a *authAPI) registerNAS(mux *http.ServeMux, media *nas.Service) {
	protected := func(next http.HandlerFunc) http.HandlerFunc {
		return a.adminOnly(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if media == nil {
				writeError(w, 503, "nas_unavailable", "尚未配置 NAS 媒体目录。")
				return
			}
			next(w, r)
		})
	}
	mux.HandleFunc("GET /api/v1/nas/entries", protected(func(w http.ResponseWriter, r *http.Request) {
		entries, err := media.List(r.URL.Query().Get("path"), r.URL.Query().Get("kind"))
		if err != nil {
			writeNASError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": entries})
	}))
	mux.HandleFunc("POST /api/v1/nas/convert", a.csrfProtected(protected(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Source    string `json:"source"`
			Directory string `json:"directory"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(nas.MaxDuration + 10*time.Second))
		name, err := media.Convert(r.Context(), body.Source, body.Directory)
		writeNASResult(w, name, err)
	})))
	mux.HandleFunc("POST /api/v1/nas/rename", a.csrfProtected(protected(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Source string `json:"source"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		name, err := media.Rename(r.Context(), body.Source)
		writeNASResult(w, name, err)
	})))
}

func writeNASResult(w http.ResponseWriter, name string, err error) {
	if err != nil {
		writeNASError(w, err)
		return
	}
	writeJSON(w, map[string]any{"data": map[string]string{"path": name}})
}

func writeNASError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, nas.ErrInput):
		writeError(w, 400, "invalid_nas_input", "请选择有效的 NAS 路径和支持的文件格式。")
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, 404, "nas_not_found", "文件或文件夹已不存在，请刷新后重新选择。")
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.EROFS):
		writeError(w, 403, "nas_permission_denied", "无法读取源文件或写入所选目录，请检查权限或选择可写的 NAS 目录。")
	case errors.Is(err, fs.ErrExist):
		writeError(w, 409, "nas_conflict", "目标文件已存在，未覆盖原文件。")
	case errors.Is(err, nas.ErrChanged):
		writeError(w, 409, "nas_source_changed", "源文件已变化，请刷新后重试。")
	case errors.Is(err, nas.ErrBusy):
		writeError(w, 429, "nas_busy", "NAS 正在处理其他文件，请稍后再试。")
	case errors.Is(err, nas.ErrNoTitle):
		writeError(w, 422, "nas_no_title", "未找到可用的歌曲标题，原文件名未更改。")
	case errors.Is(err, nas.ErrMedia):
		writeError(w, 422, "nas_media_error", "无法处理该文件，请确认格式、音轨和输出大小。")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, 504, "nas_timeout", "NAS 处理超时。")
	default:
		writeError(w, 500, "nas_io_error", "无法访问 NAS 文件，请检查目录权限和存储状态。")
	}
}
