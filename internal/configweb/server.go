package configweb

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appservice"
	"github.com/idy/tailsplit/internal/appupdate"
	"github.com/idy/tailsplit/internal/locale"
	"github.com/idy/tailsplit/internal/tray"

	"gopkg.in/yaml.v3"
)

//go:embed assets/*
var assets embed.FS

var ErrBusy = errors.New("another app operation is in progress")

type Options struct {
	Language     *locale.Store
	ConfigPath   string
	Listen       string
	Preview      bool
	Snapshot     func() tray.Snapshot
	Apply        func(context.Context, func() error) error
	Action       func(context.Context, string) error
	Logs         func() (string, error)
	Startup      func() bool
	UpdateState  func() appupdate.State
	UpdateAction func(context.Context, string) error
}

type Server struct {
	options Options
	server  *http.Server
	address string
	token   string
}

type document struct {
	YAML     string         `json:"yaml"`
	Revision string         `json:"revision,omitempty"`
	Path     string         `json:"path,omitempty"`
	Values   map[string]any `json:"values"`
	Valid    bool           `json:"valid"`
	Parsed   bool           `json:"parsed"`
	Error    string         `json:"error,omitempty"`
}

type editRequest struct {
	YAML     string                     `json:"yaml"`
	Revision string                     `json:"revision,omitempty"`
	Changes  map[string]json.RawMessage `json:"changes,omitempty"`
}

func Start(options Options) (*Server, error) {
	if _, _, err := tray.ReadConfigDocument(options.ConfigPath); err != nil {
		return nil, err
	}
	if options.Listen == "" {
		options.Listen = "127.0.0.1:0"
	}
	address, err := netip.ParseAddrPort(options.Listen)
	if err != nil || address.Addr() != netip.MustParseAddr("127.0.0.1") {
		return nil, errors.New("configuration page must listen on 127.0.0.1")
	}
	if options.Apply == nil {
		return nil, errors.New("configuration page needs an apply handler")
	}
	if options.Language == nil {
		options.Language = locale.NewStore(options.ConfigPath)
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", options.Listen)
	if err != nil {
		return nil, err
	}
	s := &Server{options: options, address: listener.Addr().String(), token: hex.EncodeToString(secret[:])}
	s.server = &http.Server{
		Handler: s.handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 60 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192,
	}
	go func() { _ = s.server.Serve(listener) }()
	return s, nil
}

// The capability stays in the fragment, so it is not sent in HTTP request URLs.
func (s *Server) URL() string { return "http://" + s.address + "/#token=" + s.token }

func (s *Server) Close() error { return s.server.Close() }

func (s *Server) handler() http.Handler {
	files, _ := fs.Sub(assets, "assets")
	static := http.FileServer(http.FS(files))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/language", s.readLanguage)
	mux.HandleFunc("POST /api/language", s.setLanguage)
	mux.HandleFunc("GET /api/config", s.readConfig)
	mux.HandleFunc("GET /api/state", s.readState)
	mux.HandleFunc("POST /api/preview", s.preview)
	mux.HandleFunc("POST /api/validate", s.validate)
	mux.HandleFunc("POST /api/config", s.save)
	mux.HandleFunc("POST /api/action", s.desktopAction)
	mux.HandleFunc("POST /api/update-action", s.updateAction)
	mux.HandleFunc("GET /api/logs", s.desktopLogs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = languageResponseWriter{ResponseWriter: w, english: requestsEnglish(r)}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		_, port, _ := net.SplitHostPort(s.address)
		hostOK := r.Host == s.address || s.options.Preview && r.Host == "localhost:"+port
		if !hostOK || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+r.Host) {
			fail(w, http.StatusForbidden, "origin", "请从 TailSplit 菜单打开本机配置页面。")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) != 1 {
				fail(w, http.StatusUnauthorized, "authorization", "页面授权已失效，请从 TailSplit 菜单重新打开。")
				return
			}
			if r.Method == http.MethodPost && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				fail(w, http.StatusUnsupportedMediaType, "content_type", "请求必须使用 JSON。")
				return
			}
			mux.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet || (r.URL.Path != "/" && r.URL.Path != "/app.js" && r.URL.Path != "/i18n.js" && r.URL.Path != "/style.css") {
			http.NotFound(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})
}

func (s *Server) describe(data []byte) document {
	doc := document{YAML: string(data), Values: map[string]any{}}
	if err := yaml.Unmarshal(data, &doc.Values); err != nil {
		doc.Values = map[string]any{}
		doc.Error = err.Error()
		return doc
	}
	if _, err := json.Marshal(doc.Values); err != nil {
		// YAML permits non-finite numbers and keys the form cannot represent.
		// Keep the original source readable instead of emitting an empty JSON body.
		doc.Values = map[string]any{}
		doc.Error = "请在 YAML 原文中修正无法用于表单的值：" + err.Error()
		return doc
	}
	doc.Parsed = true
	config, err := tray.ValidateConfigDocument(s.options.ConfigPath, data)
	if err == nil {
		err = appservice.ValidateAppConfig(config)
	}
	if err != nil {
		doc.Error = err.Error()
		return doc
	}
	doc.Valid = true
	return doc
}

func (s *Server) readConfig(w http.ResponseWriter, _ *http.Request) {
	data, revision, err := tray.ReadConfigDocument(s.options.ConfigPath)
	if err != nil {
		fail(w, http.StatusInternalServerError, "read", "无法读取配置文件："+err.Error())
		return
	}
	doc := s.describe(data)
	doc.Revision, doc.Path = revision, s.options.ConfigPath
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) readState(w http.ResponseWriter, _ *http.Request) {
	var snapshot tray.Snapshot
	if s.options.Snapshot != nil {
		snapshot = s.options.Snapshot()
	}
	_, revision, _ := tray.ReadConfigDocument(s.options.ConfigPath)
	// LoginURL and other authentication state never enter the browser API.
	state := map[string]any{
		"state": snapshot.State, "active": snapshot.Active, "mode": snapshot.Mode,
		"exit": snapshot.ExitNode, "network": snapshot.NetworkName,
		"error": snapshot.Error, "revision": revision, "preview": s.options.Preview,
		"exit_nodes": snapshot.ExitNodes, "desktop": s.options.Action != nil,
		"startup": s.options.Startup != nil && s.options.Startup(),
	}
	if s.options.UpdateState != nil {
		state["updates"] = s.options.UpdateState()
	}
	writeJSON(w, http.StatusOK, state)
}

func decodeEdit(w http.ResponseWriter, r *http.Request) (editRequest, bool) {
	var edit editRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*tray.MaxConfigBytes+4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&edit); err != nil {
		fail(w, http.StatusBadRequest, "request", "无法读取编辑内容："+err.Error())
		return edit, false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || len(edit.YAML) > tray.MaxConfigBytes {
		fail(w, http.StatusBadRequest, "request", "配置不得超过 1 MiB，且只能提交一份内容。")
		return edit, false
	}
	return edit, true
}

var fieldTypes = map[string]string{
	"listen": "string", "interface": "string", "log_level": "string",
	"app.connect_on_start": "bool", "tailscale.hostname": "string",
	"tailscale.exit_node": "string", "tailscale.tailnet": "string", "tailscale.domains": "list",
	"dns.direct": "string", "dns.proxy": "string", "dns.direct_ipv6": "bool",
	"dns.fake_ip": "bool", "dns.magic_domains": "list",
	"rules.mode": "string", "rules.gfwlist": "string", "rules.final": "string",
	"rules.proxy_domains": "list", "rules.direct_domains": "list",
	"rules.proxy_ips": "list", "rules.direct_ips": "list",
	"tun.enabled": "bool", "tun.mtu": "int",
}

func applyChanges(edit editRequest) ([]byte, error) {
	if len(edit.Changes) == 0 {
		return []byte(edit.YAML), nil
	}
	keys := make([]string, 0, len(edit.Changes))
	for key := range edit.Changes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	changes := make([]tray.ConfigChange, 0, len(keys))
	for _, path := range keys {
		kind, ok := fieldTypes[path]
		if !ok {
			return nil, fmt.Errorf("unknown form field %q", path)
		}
		raw := edit.Changes[path]
		if string(raw) == "null" {
			return nil, fmt.Errorf("%s must not be null", path)
		}
		var target any
		switch kind {
		case "string":
			target = new(string)
		case "bool":
			target = new(bool)
		case "int":
			target = new(int)
		case "list":
			target = new([]string)
		}
		if err := json.Unmarshal(raw, target); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		section, key, nested := strings.Cut(path, ".")
		if !nested {
			key, section = section, ""
		}
		changes = append(changes, tray.ConfigChange{Section: section, Key: key, Value: target})
	}
	return tray.PatchConfigDocument([]byte(edit.YAML), changes...)
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	edit, ok := decodeEdit(w, r)
	if !ok {
		return
	}
	data, err := applyChanges(edit)
	if err != nil {
		fail(w, http.StatusBadRequest, "edit", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.describe(data))
}

func (s *Server) validateRuntime(data []byte) error {
	config, err := tray.ValidateConfigDocument(s.options.ConfigPath, data)
	if err != nil {
		return err
	}
	if err := appservice.ValidateAppConfig(config); err != nil {
		return err
	}
	// An unregistered device can still save its setup before selecting an exit.
	if config.Tailscale != nil && config.Tailscale.ExitNode == "" {
		return nil
	}
	compiled, err := tailsplit.Compile(config)
	if err != nil {
		return err
	}
	return tailsplit.Check(compiled)
}

func (s *Server) validate(w http.ResponseWriter, r *http.Request) {
	edit, ok := decodeEdit(w, r)
	if !ok {
		return
	}
	data, err := applyChanges(edit)
	if err == nil {
		err = s.validateRuntime(data)
	}
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"valid": true})
}

func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	edit, ok := decodeEdit(w, r)
	if !ok {
		return
	}
	data, err := applyChanges(edit)
	if err == nil {
		err = s.validateRuntime(data)
	}
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	// Check before stopping a running session, then check again at the atomic
	// replacement boundary so a native-menu edit cannot silently be overwritten.
	_, currentRevision, err := tray.ReadConfigDocument(s.options.ConfigPath)
	if err != nil {
		fail(w, http.StatusInternalServerError, "read", err.Error())
		return
	}
	if currentRevision != edit.Revision {
		fail(w, http.StatusConflict, "conflict", "配置文件已由其他操作修改，请重新读取后再保存。")
		return
	}
	err = s.options.Apply(r.Context(), func() error {
		return tray.SaveConfigDocument(s.options.ConfigPath, edit.Revision, data)
	})
	if err != nil {
		status, code := http.StatusInternalServerError, "apply"
		if errors.Is(err, tray.ErrConfigChanged) {
			status, code = http.StatusConflict, "conflict"
		} else if errors.Is(err, ErrBusy) {
			status, code = http.StatusConflict, "busy"
		}
		fail(w, status, code, "无法应用配置："+err.Error())
		return
	}
	s.readConfig(w, r)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		data = []byte(`{"code":"encoding","error":"无法生成配置响应。"}`)
	}
	if localized, ok := w.(languageResponseWriter); ok {
		data = localizeErrors(data, localized.english)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "error": message})
}
