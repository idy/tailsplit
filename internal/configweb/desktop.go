package configweb

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF {
		fail(w, http.StatusBadRequest, "request", "无法读取操作请求。")
		return
	}
	switch request.Action {
	case "connect", "disconnect", "login", "logout", "refresh-exits", "startup-on", "startup-off":
	default:
		fail(w, http.StatusBadRequest, "action", "未知操作。")
		return
	}
	if err := s.options.Action(r.Context(), request.Action); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrBusy) {
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
