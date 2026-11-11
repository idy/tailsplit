package configweb

import (
	"bytes"
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
	"reflect"
	"sort"
	"strings"
	"sync"
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
	// Language shares persisted UI preferences with the desktop app, including
	// companion visibility. A nil store is loaded from ConfigPath by Start.
	Language   *locale.Store
	ConfigPath string
	Listen     string
	Preview    bool
	Snapshot   func() tray.Snapshot
	// Apply must run check against its current snapshot under the same operation
	// gate as update, before stopping a helper or changing the saved document.
	Apply  func(context.Context, func(tray.Snapshot) error, func() error) error
	Action func(context.Context, string) error
	// Connect rechecks the displayed revision under the desktop operation gate
	// before starting either authentication or traffic.
	Connect      func(context.Context, string) error
	AddExit      func(context.Context, string, string) error
	RemoveExit   func(context.Context, string, string) error
	PingExit     func(context.Context, string) (tailsplit.ExitPingResult, error)
	Logs         func() (string, error)
	Startup      func() bool
	UpdateState  func() appupdate.State
	UpdateAction func(context.Context, string) error
	// Popup actions only reveal the retained window or request normal app cleanup.
	OpenPanel func()
	Quit      func()
}

type Server struct {
	options Options
	server  *http.Server
	address string
	token   string
	ping    sync.Mutex
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

// PopupURL shares the panel's capability without putting it in request URLs.
func (s *Server) PopupURL() string { return "http://" + s.address + "/tray.html#token=" + s.token }

func (s *Server) Close() error { return s.server.Close() }

func (s *Server) handler() http.Handler {
	files, _ := fs.Sub(assets, "assets")
	static := http.FileServer(http.FS(files))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/language", s.readLanguage)
	mux.HandleFunc("POST /api/language", s.setLanguage)
	mux.HandleFunc("GET /api/companion", s.readCompanion)
	mux.HandleFunc("POST /api/companion", s.setCompanion)
	mux.HandleFunc("GET /api/config", s.readConfig)
	mux.HandleFunc("GET /api/state", s.readState)
	mux.HandleFunc("POST /api/preview", s.preview)
	mux.HandleFunc("POST /api/validate", s.validate)
	mux.HandleFunc("POST /api/config", s.save)
	mux.HandleFunc("POST /api/action", s.desktopAction)
	mux.HandleFunc("POST /api/exit-ping", s.exitPing)
	mux.HandleFunc("POST /api/exit-remove", s.removeExit)
	mux.HandleFunc("POST /api/update-action", s.updateAction)
	mux.HandleFunc("GET /api/logs", s.desktopLogs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = languageResponseWriter{ResponseWriter: w, english: requestsEnglish(r)}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
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
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/", "/ui.js", "/app.js", "/i18n.js", "/select.js", "/style.css", "/companion.js",
			"/tray.html", "/tray.js", "/tray.css",
			"/vendor/magpie/css/magpie.css", "/vendor/magpie/css/tokens.css",
			"/vendor/magpie/css/fonts.css", "/vendor/magpie/css/base.css", "/vendor/magpie/css/Magpie-MIT.txt",
			"/vendor/magpie/css/components.css", "/vendor/magpie/css/layout.css",
			"/vendor/magpie/js/select.js",
			"/vendor/magpie/icons/icons.svg", "/vendor/magpie/icons/caret-down.svg",
			"/vendor/magpie/icons/check.svg", "/vendor/magpie/icons/spinner.svg",
			"/vendor/magpie/fonts/montserrat-latin.woff2", "/vendor/magpie/fonts/monda-latin.woff2",
			"/vendor/magpie/fonts/lato-regular-latin.woff2", "/vendor/magpie/fonts/lato-bold-latin.woff2",
			"/vendor/magpie/fonts/Montserrat-OFL.txt", "/vendor/magpie/fonts/Monda-OFL.txt",
			"/vendor/magpie/fonts/Lato-OFL.txt", "/vendor/magpie/icons/LICENSE", "/vendor/magpie/LICENSE",
			"/vendor/peetty/peetty.global.js", "/vendor/peetty/peetty.css", "/vendor/peetty/LICENSE.txt",
			"/fonts/montserrat-latin.woff2", "/fonts/monda-latin.woff2",
			"/fonts/lato-regular-latin.woff2", "/fonts/lato-bold-latin.woff2",
			"/fonts/Montserrat-OFL.txt", "/fonts/Monda-OFL.txt", "/fonts/Lato-OFL.txt":
			if strings.HasSuffix(r.URL.Path, ".woff2") {
				w.Header().Set("Content-Type", "font/woff2")
			}
			static.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
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
		// Decode without normalization so the form retains lexical strings and
		// source omissions rather than exposing defaults as explicit settings.
		var source tailsplit.Config
		if err = yaml.Unmarshal(data, &source); err == nil {
			// Logical identities keep their validated spelling, including trimming.
			// Presence is still determined by doc.Values, so no defaults are added.
			for index := range source.Exits {
				source.Exits[index].ID = config.Exits[index].ID
			}
			source.Rules.DefaultExit, source.Rules.GlobalExit, source.Rules.Final = config.Rules.DefaultExit, config.Rules.GlobalExit, config.Rules.Final
			for index := range source.Rules.Routes {
				source.Rules.Routes[index].Exit = config.Rules.Routes[index].Exit
			}
			restoreStringValues(doc.Values, reflect.ValueOf(source))
			err = appservice.ValidateAppConfig(config)
		}
	}
	if err != nil {
		doc.Error = err.Error()
		return doc
	}
	doc.Valid = true
	return doc
}

// Restore schema-known strings from the typed source decode. Generic YAML
// scalars otherwise turn domains such as 1.10 into numbers and lose their text.
// Only visit fields already present in the source; never inject typed defaults.
func restoreStringValues(raw any, typed reflect.Value) any {
	if raw == nil {
		return nil
	}
	if typed.Kind() == reflect.Pointer {
		if typed.IsNil() {
			return raw
		}
		return restoreStringValues(raw, typed.Elem())
	}
	switch typed.Kind() {
	case reflect.String:
		return typed.String()
	case reflect.Struct:
		values, ok := raw.(map[string]any)
		if !ok {
			return raw
		}
		for index := 0; index < typed.NumField(); index++ {
			key, _, _ := strings.Cut(typed.Type().Field(index).Tag.Get("yaml"), ",")
			if value, present := values[key]; present && key != "" && key != "-" {
				values[key] = restoreStringValues(value, typed.Field(index))
			}
		}
	case reflect.Slice:
		values, ok := raw.([]any)
		if !ok || len(values) != typed.Len() {
			return raw
		}
		for index, value := range values {
			values[index] = restoreStringValues(value, typed.Index(index))
		}
	}
	return raw
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
		"exit": snapshot.ExitNode, "network": snapshot.NetworkName, "magic_dns_suffix": snapshot.MagicDNSSuffix,
		"hostname": snapshot.Hostname, "backend_state": snapshot.BackendState, "phase": snapshot.Phase,
		"routing_mode": snapshot.RoutingMode, "logs_available": s.options.Logs != nil,
		"error": snapshot.Error, "revision": revision, "preview": s.options.Preview,
		"exit_nodes": snapshot.ExitNodes, "desktop": s.options.Action != nil,
		"ping_available": s.options.PingExit != nil && snapshot.PingAvailable,
		"startup":        s.options.Startup != nil && s.options.Startup(),
		"exits":          exitStates(snapshot), "focus": snapshot.Focus,
		"traffic_exit":         snapshot.TrafficExit,
		"open_panel_available": s.options.OpenPanel != nil,
		"quit_available":       s.options.Quit != nil,
	}
	if s.options.UpdateState != nil {
		state["updates"] = s.options.UpdateState()
	}
	writeJSON(w, http.StatusOK, state)
}

// exitStates exposes additional devices' credential-free status by exit ID.
func exitStates(snapshot tray.Snapshot) map[string]any {
	exits := map[string]any{}
	for id, exit := range snapshot.Exits {
		nodes := exit.ExitNodes
		if nodes == nil {
			nodes = []tailsplit.ExitNode{}
		}
		exits[id] = map[string]any{
			"backend_state": exit.BackendState, "phase": exit.Phase, "network": exit.NetworkName, "magic_dns_suffix": exit.MagicDNSSuffix, "hostname": exit.Hostname,
			"exit": exit.ExitNode, "exit_online": exit.ExitOnline, "exit_visible": exit.ExitVisible,
			"exit_nodes": nodes, "ping_available": exit.PingAvailable && exit.BackendState == "Running",
		}
	}
	return exits
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
	"tailscale.name": "string", "exits": "exits", "rules.routes": "routes",
	"dns.direct": "string", "dns.proxy": "string", "dns.direct_ipv6": "bool",
	"dns.fake_ip": "bool", "dns.magic_domains": "list",
	"rules.mode": "string", "rules.global_exit": "string", "rules.default_exit": "string", "rules.source": "string", "rules.gfwlist": "string", "rules.final": "string",
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
		case "exits":
			target = new([]tailsplit.ExitConfig)
		case "routes":
			target = new([]tailsplit.RouteConfig)
		}
		if kind == "exits" || kind == "routes" {
			// Structured lists use the YAML field names, not Go's JSON names.
			decoder := yaml.NewDecoder(bytes.NewReader(raw))
			decoder.KnownFields(true)
			if err := decoder.Decode(target); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			// Keep the submitted field presence after schema checking. Encoding
			// the Go structs would insert defaults into otherwise untouched entries.
			var sequence yaml.Node
			if err := yaml.Unmarshal(raw, &sequence); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if err := formatStructuredJSON(sequence.Content[0]); err != nil {
				return nil, err
			}
			target = sequence.Content[0]
		} else if err := json.Unmarshal(raw, target); err != nil {
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

// Render new JSON entries as ordinary YAML, with only ambiguous strings quoted.
// Existing entries retain their source styles when the document patch reuses them.
func formatStructuredJSON(node *yaml.Node) error {
	node.Style = 0
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		return node.Encode(node.Value)
	}
	for _, child := range node.Content {
		if err := formatStructuredJSON(child); err != nil {
			return err
		}
	}
	return nil
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

func (s *Server) candidateConfig(data []byte) (tailsplit.Config, error) {
	config, err := tray.ValidateConfigDocument(s.options.ConfigPath, data)
	if err == nil {
		err = appservice.ValidateAppConfig(config)
	}
	return config, err
}

func compileCandidate(ctx context.Context, config tailsplit.Config) error {
	compiled, err := tailsplit.CompileContext(ctx, config)
	if err != nil {
		return err
	}
	return tailsplit.Check(compiled)
}

func (s *Server) validateRuntime(ctx context.Context, data []byte) error {
	config, err := s.candidateConfig(data)
	if err == nil {
		err = tailsplit.ValidateTrafficExit(config)
	}
	if err != nil {
		return err
	}
	return compileCandidate(ctx, config)
}

// Drafts may be saved while stopped, but cannot replace an active helper.
// Replacing any active Global configuration tears down its TUN, even when only
// metadata changes. Consent is followed by a separate disconnect, so a failed
// save cannot silently restore the old protected session.
func checkCandidateSession(snapshot tray.Snapshot, config tailsplit.Config, draftErr error) error {
	if !snapshot.Active {
		return nil
	}
	if draftErr != nil {
		return draftErr
	}
	if config.Rules.Mode == tailsplit.RoutingGlobal && snapshot.RoutingMode == tailsplit.RoutingGlobal &&
		(snapshot.Mode == "tun" || snapshot.Mode == "proxy") {
		return errors.New("disconnect before applying changes to an active Global connection")
	}
	return nil
}

type applyValidationError struct{ error }

func (err applyValidationError) Unwrap() error { return err.error }

func (s *Server) validate(w http.ResponseWriter, r *http.Request) {
	edit, ok := decodeEdit(w, r)
	if !ok {
		return
	}
	data, err := applyChanges(edit)
	if err == nil {
		err = s.validateRuntime(r.Context(), data)
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
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	// A byte-identical save is a no-op: it must not stop even a protected helper.
	// Revisions still have to match so a stale request cannot claim success.
	current, currentRevision, err := tray.ReadConfigDocument(s.options.ConfigPath)
	if err != nil {
		fail(w, http.StatusInternalServerError, "read", err.Error())
		return
	}
	if currentRevision != edit.Revision {
		fail(w, http.StatusConflict, "conflict", "配置文件已由其他操作修改，请重新读取后再保存。")
		return
	}
	if bytes.Equal(current, data) {
		s.readConfig(w, r)
		return
	}
	var config tailsplit.Config
	var draftErr error
	if err == nil {
		config, err = s.candidateConfig(data)
	}
	if err == nil {
		// Selection and retained-owner drafts need no runnable configuration to
		// be saved while idle. Explicit /api/validate never uses this exception.
		draftErr = tailsplit.ValidateTrafficExit(config)
		if draftErr == nil {
			err = compileCandidate(r.Context(), config)
			if errors.Is(err, tailsplit.ErrUnresolvedSplitExit) {
				draftErr, err = err, nil
			}
		}
	}
	check := func(snapshot tray.Snapshot) error {
		return checkCandidateSession(snapshot, config, draftErr)
	}
	if err == nil && s.options.Snapshot != nil {
		err = check(s.options.Snapshot())
	}
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	// Recheck the session under the operation gate before stopping, and the
	// revision at the atomic replacement boundary before writing.
	err = s.options.Apply(r.Context(), func(snapshot tray.Snapshot) error {
		if err := check(snapshot); err != nil {
			return applyValidationError{err}
		}
		return nil
	}, func() error {
		return tray.SaveConfigDocument(s.options.ConfigPath, edit.Revision, data)
	})
	if err != nil {
		status, code := http.StatusInternalServerError, "apply"
		var validationErr applyValidationError
		if errors.As(err, &validationErr) {
			status, code = http.StatusUnprocessableEntity, "validation"
		} else if errors.Is(err, tray.ErrConfigChanged) {
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
