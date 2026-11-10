package configweb

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/idy/tailsplit/internal/tray"
)

// Recovery uses the same operation gate and revision checks as ordinary saves.
// It never replaces a valid document or reconnects an active network session.
func (s *Server) rebuildConfig(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Revision string `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF || request.Revision == "" {
		fail(w, http.StatusBadRequest, "request", "重建配置需要当前文件版本。")
		return
	}
	active := errors.New("请先断开连接，再重建配置。")
	valid := errors.New("当前配置有效，无需重建。")
	check := func(snapshot tray.Snapshot) error {
		data, revision, err := tray.ReadConfigDocument(s.options.ConfigPath)
		if err != nil {
			return err
		}
		if revision != request.Revision {
			return tray.ErrConfigChanged
		}
		if s.describe(data).Valid {
			return valid
		}
		if snapshot.Active {
			return active
		}
		return nil
	}
	var backup string
	err := s.options.Apply(r.Context(), check, func() error {
		var err error
		backup, err = tray.RebuildConfigDocument(s.options.ConfigPath, request.Revision)
		return err
	})
	if err != nil {
		status, code := http.StatusInternalServerError, "rebuild"
		switch {
		case errors.Is(err, tray.ErrConfigChanged):
			status, code = http.StatusConflict, "conflict"
		case errors.Is(err, ErrBusy):
			status, code = http.StatusConflict, "busy"
		case errors.Is(err, active), errors.Is(err, valid):
			status, code = http.StatusConflict, "config"
		}
		fail(w, status, code, "无法重建配置："+err.Error())
		return
	}
	data, revision, err := tray.ReadConfigDocument(s.options.ConfigPath)
	if err != nil {
		fail(w, http.StatusInternalServerError, "read", "无法读取配置文件："+err.Error())
		return
	}
	doc := s.describe(data)
	doc.Revision, doc.Path = revision, s.options.ConfigPath
	writeJSON(w, http.StatusOK, struct {
		document
		BackupPath string `json:"backup_path"`
	}{doc, backup})
}
