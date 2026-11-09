package configweb

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appupdate"
	"github.com/idy/tailsplit/internal/tray"
)

// Desktop controls use the same capability and origin policy as configuration
// saves. A fixed action vocabulary cannot become a command/path execution API.
func (s *Server) desktopAction(w http.ResponseWriter, r *http.Request) {
	if s.options.Action == nil {
		http.NotFound(w, r)
		return
	}
	var request struct {
		Action string `json:"action"`
		Exit   string `json:"exit,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF {
		fail(w, http.StatusBadRequest, "request", "无法读取操作请求。")
		return
	}
	action := request.Action
	switch request.Action {
	case "connect", "disconnect", "login", "logout", "switch-network", "refresh-exits", "startup-on", "startup-off":
		if request.Exit != "" {
			fail(w, http.StatusBadRequest, "action", "未知操作。")
			return
		}
	case "login-exit", "logout-exit":
		// Only configured additional exits are accepted; the helper rechecks.
		config, err := tailsplit.Load(s.options.ConfigPath)
		known := false
		for _, exit := range config.Exits {
			known = known || exit.ID == request.Exit
		}
		if err != nil || !known {
			fail(w, http.StatusBadRequest, "action", "未找到这个出口，请先保存设置。")
			return
		}
		action += ":" + request.Exit
	default:
		fail(w, http.StatusBadRequest, "action", "未知操作。")
		return
	}
	if err := s.options.Action(r.Context(), action); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrBusy) {
			status = http.StatusConflict
		}
		fail(w, status, "action", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// App updates are available independently of desktop connection controls.
func (s *Server) updateAction(w http.ResponseWriter, r *http.Request) {
	if s.options.UpdateAction == nil {
		http.NotFound(w, r)
		return
	}
	var request struct {
		Action string `json:"action"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF {
		fail(w, http.StatusBadRequest, "request", "无法读取操作请求。")
		return
	}
	switch request.Action {
	case "check", "install", "automatic-on", "automatic-off":
	default:
		fail(w, http.StatusBadRequest, "action", "未知操作。")
		return
	}
	if err := s.options.UpdateAction(r.Context(), request.Action); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrBusy) || errors.Is(err, appupdate.ErrBusy) {
			status = http.StatusConflict
		}
		fail(w, status, "action", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) desktopLogs(w http.ResponseWriter, r *http.Request) {
	if s.options.Logs == nil {
		http.NotFound(w, r)
		return
	}
	text, err := s.options.Logs()
	if err != nil {
		fail(w, http.StatusInternalServerError, "logs", "无法读取日志。")
		return
	}
	// The callback returns the tail, never arbitrary files or unbounded history.
	if len(text) > 128<<10 {
		text = text[len(text)-(128<<10):]
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// Removal shares capability/origin authentication with saves. The desktop
// callback owns the native operation gate and repeats the revision checks.
func (s *Server) removeExit(w http.ResponseWriter, r *http.Request) {
	if s.options.RemoveExit == nil {
		http.NotFound(w, r)
		return
	}
	var request struct {
		Exit     string `json:"exit"`
		Revision string `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF || request.Exit == "" {
		fail(w, http.StatusBadRequest, "request", "invalid exit removal request")
		return
	}
	err := tray.CheckExitRemoval(s.options.ConfigPath, request.Exit, request.Revision)
	if err == nil {
		err = s.options.RemoveExit(r.Context(), request.Exit, request.Revision)
	}
	if err != nil {
		status, code := http.StatusBadRequest, "remove_exit"
		if errors.Is(err, tray.ErrConfigChanged) {
			status, code = http.StatusConflict, "conflict"
		} else if errors.Is(err, ErrBusy) || errors.Is(err, tailsplit.ErrPingBusy) {
			status, code = http.StatusConflict, "busy"
		}
		fail(w, status, code, err.Error())
		return
	}
	s.readConfig(w, r)
}
