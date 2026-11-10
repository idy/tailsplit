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

// Claiming consumes the intent before guided setup begins, even when that flow
// later cancels. The controller callback compares and clears the ID atomically.
func (s *Server) claimConnectRequest(w http.ResponseWriter, r *http.Request) {
	if s.options.ClaimConnectRequest == nil {
		http.NotFound(w, r)
		return
	}
	var request struct {
		Request string `json:"request"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF {
		fail(w, http.StatusBadRequest, "request", "无法读取操作请求。")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"claimed": s.options.ClaimConnectRequest(request.Request)})
}

// Desktop controls use the same capability and origin policy as configuration
// saves. A fixed action vocabulary cannot become a command/path execution API.
func (s *Server) desktopAction(w http.ResponseWriter, r *http.Request) {
	if s.options.Action == nil && s.options.Connect == nil && s.options.AddExit == nil && s.options.OpenPanel == nil && s.options.Quit == nil {
		http.NotFound(w, r)
		return
	}
	var request struct {
		Action   string `json:"action"`
		Exit     string `json:"exit,omitempty"`
		Revision string `json:"revision,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF {
		fail(w, http.StatusBadRequest, "request", "无法读取操作请求。")
		return
	}
	action := request.Action
	if request.Action != "add-exit" && request.Action != "connect" && request.Revision != "" {
		fail(w, http.StatusBadRequest, "action", "未知操作。")
		return
	}
	var operation func() error
	switch request.Action {
	case "open-panel", "quit":
		if request.Exit != "" {
			fail(w, http.StatusBadRequest, "action", "未知操作。")
			return
		}
		callback := s.options.OpenPanel
		if request.Action == "quit" {
			callback = s.options.Quit
		}
		if callback == nil {
			http.NotFound(w, r)
			return
		}
		// Acknowledge before native activation dismisses the popup or quitting
		// shuts the server down. The callback uses the normal desktop lifecycle.
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		_ = http.NewResponseController(w).Flush()
		go callback()
		return
	case "connect":
		if request.Exit != "" {
			fail(w, http.StatusBadRequest, "action", "未知操作。")
			return
		}
		if request.Revision != "" {
			if s.options.Connect == nil {
				http.NotFound(w, r)
				return
			}
			if err := tray.CheckConfigRevision(s.options.ConfigPath, request.Revision); err != nil {
				fail(w, http.StatusConflict, "conflict", "配置文件已更新，请重新读取后重试。")
				return
			}
			operation = func() error { return s.options.Connect(r.Context(), request.Revision) }
		}
	case "disconnect", "login", "logout", "switch-network", "refresh-exits", "startup-on", "startup-off":
		if request.Exit != "" {
			fail(w, http.StatusBadRequest, "action", "未知操作。")
			return
		}
	case "add-exit":
		if s.options.AddExit == nil {
			http.NotFound(w, r)
			return
		}
		original, revision, err := tray.ReadConfigDocument(s.options.ConfigPath)
		if err != nil {
			fail(w, http.StatusBadRequest, "config", err.Error())
			return
		}
		if request.Revision == "" || request.Revision != revision {
			fail(w, http.StatusConflict, "conflict", "配置文件已更新，请重新读取后重试。")
			return
		}
		// Validate the same document whose revision the page confirmed. Stale
		// inventory must produce a reload conflict before capacity/ID checks.
		config, err := tray.ValidateConfigDocument(s.options.ConfigPath, original)
		if err != nil || !tailsplit.ValidExitID(request.Exit) || len(config.Exits) >= 8 {
			fail(w, http.StatusBadRequest, "action", "无法添加出口，请先检查配置。")
			return
		}
		for _, exit := range config.Exits {
			if exit.ID == request.Exit {
				fail(w, http.StatusBadRequest, "action", "这个出口已存在。")
				return
			}
		}
		operation = func() error { return s.options.AddExit(r.Context(), request.Exit, request.Revision) }
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
	if operation == nil {
		if s.options.Action == nil {
			http.NotFound(w, r)
			return
		}
		operation = func() error { return s.options.Action(r.Context(), action) }
	}
	if err := operation(); err != nil {
		if errors.Is(err, tray.ErrConfigChanged) {
			fail(w, http.StatusConflict, "conflict", "配置文件已更新，请重新读取后重试。")
			return
		}
		status := http.StatusBadRequest
		if errors.Is(err, ErrBusy) || errors.Is(err, tailsplit.ErrPingBusy) {
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
