package tray

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/idy/tailsplit"

	"gopkg.in/yaml.v3"
)

type Options struct {
	Helper     string
	ConfigPath string
	LogPath    string
	HelperArgs []string // Used by isolated subprocess tests.
	ExtraEnv   []string
}

type Snapshot struct {
	State          string
	LoginURL       string
	ExitNode       string
	NetworkName    string
	MagicDNSSuffix string
	Mode           string
	RoutingMode    string
	// TrafficExit is the logical device selected by the current helper, which
	// can differ from configuration saved since that helper started.
	TrafficExit   string
	BackendState  string
	Error         string
	Hostname      string
	ExitNodes     []tailsplit.ExitNode
	Active        bool
	PingAvailable bool
	// Exits holds additional devices' latest status, keyed by exit ID. The
	// identity fields above always describe the primary device; State, Active,
	// Mode and RoutingMode describe the overall connection.
	Exits map[string]ExitSnapshot
	// Focus names the additional exit an active login session belongs to.
	Focus string
}

type ExitSnapshot struct {
	BackendState   string
	NetworkName    string
	MagicDNSSuffix string
	Hostname       string
	ExitNode       string
	ExitOnline     bool
	ExitVisible    bool
	ExitNodes      []tailsplit.ExitNode
	PingAvailable  bool
}

// LoginExitID identifies the device supplying the current helper's authorization.
func (s Snapshot) LoginExitID() string {
	if s.Focus != "" {
		return s.Focus
	}
	if (s.Mode == "proxy" || s.Mode == "tun") && s.TrafficExit != "" {
		return s.TrafficExit
	}
	return tailsplit.MainExit
}

func (s Snapshot) clone() Snapshot {
	s.ExitNodes = append([]tailsplit.ExitNode(nil), s.ExitNodes...)
	if s.Exits != nil {
		exits := make(map[string]ExitSnapshot, len(s.Exits))
		for id, exit := range s.Exits {
			exit.ExitNodes = append([]tailsplit.ExitNode(nil), exit.ExitNodes...)
			exits[id] = exit
		}
		s.Exits = exits
	}
	return s
}

// reset replaces the primary state while keeping other devices' last status.
func (c *Controller) reset(snapshot Snapshot) {
	snapshot.Exits = c.snapshot.Exits
	c.snapshot = snapshot
}

// Controller owns only its bundled helper process, never a system sing-box or
// Tailscale daemon. The UI starts it explicitly or through an opt-in preference.
type Controller struct {
	mu            sync.Mutex
	options       Options
	snapshot      Snapshot
	command       *exec.Cmd
	input         io.WriteCloser
	done          chan struct{}
	stopping      bool
	loggingOut    bool
	reconfiguring bool
	closed        bool
	log           *rotatingLog
	close         sync.Once
	closeErr      error
	stopWait      time.Duration
	pingCancel    context.CancelFunc
	pingDone      chan struct{}
	trafficExit   string // Selected traffic device for this running session.
}

func New(options Options) (*Controller, error) {
	if options.Helper == "" || options.ConfigPath == "" || options.LogPath == "" {
		return nil, errors.New("helper, config and log paths are required")
	}
	log, err := openLog(options.LogPath)
	if err != nil {
		return nil, err
	}
	return &Controller{options: options, snapshot: Snapshot{State: "stopped"}, log: log, stopWait: 10 * time.Second}, nil
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot := c.snapshot.clone()
	snapshot.Active = c.command != nil || c.loggingOut
	snapshot.TrafficExit = c.trafficExit
	snapshot.PingAvailable = c.pingAvailableLocked()
	return snapshot
}

func (c *Controller) RecordLoginBrowserResult(err error) {
	message := "Requested a new default-browser window for Tailscale login.\n"
	if err != nil {
		message = "Could not open the Tailscale login browser; use Copy login link.\n"
	}
	_, _ = io.WriteString(c.log, message)
}

// RecordDesktopEvent retains native window startup failures in the same
// private, redacted log as engine events; a dialog alone can be inaccessible.
func (c *Controller) RecordDesktopEvent(message string) {
	_, _ = io.WriteString(c.log, RedactLog(message)+"\n")
}

// TrafficExitID reads the current saved preference, rather than the last
// session's RoutingMode/trafficExit. Settings can change while disconnected.
func (c *Controller) TrafficExitID() (string, error) {
	config, err := tailsplit.Load(c.options.ConfigPath)
	if err != nil {
		return "", err
	}
	return config.TrafficExitID(), nil
}

func (c *Controller) Start() error {
	return c.start("proxy", "")
}

// beginReconfigure keeps independent Ping requests out of the gap between
// stopping a device and starting its replacement.
func (c *Controller) beginReconfigure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.loggingOut || c.reconfiguring {
		return tailsplit.ErrPingBusy
	}
	c.reconfiguring = true
	return nil
}

func (c *Controller) endReconfigure() {
	c.mu.Lock()
	c.reconfiguring = false
	c.mu.Unlock()
}

// Restart is also available after a failed or stopped session. Reconfigure
// intentionally preserves a disconnected state and cannot implement Retry.
func (c *Controller) Restart() error {
	if err := c.beginReconfigure(); err != nil {
		return err
	}
	defer c.endReconfigure()
	if err := c.Stop(); err != nil {
		return err
	}
	return c.Start()
}

// StartLogin authenticates the embedded device without installing a service or
// creating a TUN. Connecting the network is a separate, explicit Start action.
func (c *Controller) StartLogin() error {
	return c.start("login", tailsplit.MainExit)
}

// StartExitLogin authorizes one additional exit's device by itself.
func (c *Controller) StartExitLogin(id string) error {
	if id == "" || id == tailsplit.MainExit {
		return c.StartLogin()
	}
	return c.start("login", id)
}

// RefreshExitNodes reuses a running core's live status. When disconnected it
// starts only a short, listener-free discovery session, never a TUN or browser.
func (c *Controller) RefreshExitNodes() error {
	if c.Snapshot().Active {
		return nil
	}
	return c.start("discovery", "")
}

// Reconfigure releases the saved device's state lock before applying a change,
// then reconnects only if the proxy was running. A failed edit restores the old
// running configuration instead of leaving a previously connected user offline.
func (c *Controller) Reconfigure(update func() error) error {
	if err := c.beginReconfigure(); err != nil {
		return err
	}
	defer c.endReconfigure()
	before := c.Snapshot()
	reconnect := before.Active && (before.Mode == "tun" || before.Mode == "proxy")
	if err := c.Stop(); err != nil {
		return err
	}
	err := update()
	if reconnect {
		err = errors.Join(err, c.Start())
	}
	return err
}

func (c *Controller) start(mode, focus string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("controller is closed")
	}
	if c.command != nil {
		return errors.New("proxy is already running")
	}
	if c.pingCancel != nil {
		return tailsplit.ErrPingBusy
	}
	if c.loggingOut {
		return errors.New("embedded Tailscale logout is in progress")
	}
	runCommand := "run"
	routingMode := tailsplit.RoutingSplit
	trafficExit := tailsplit.MainExit
	if mode == "discovery" {
		runCommand = "discover"
	}
	if len(c.options.HelperArgs) == 0 {
		if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
			runCommand = "app-run"
			if mode == "login" {
				runCommand = "app-login"
			} else if mode == "discovery" {
				runCommand = "app-discover"
			}
		}
		data, err := os.ReadFile(c.options.ConfigPath)
		if err != nil {
			return err
		}
		var config struct {
			TUN struct {
				Enabled bool `yaml:"enabled"`
			} `yaml:"tun"`
			Rules struct {
				Mode        string `yaml:"mode"`
				GlobalExit  string `yaml:"global_exit"`
				DefaultExit string `yaml:"default_exit"`
			} `yaml:"rules"`
		}
		if err := yaml.Unmarshal(data, &config); err != nil {
			return err
		}
		if config.TUN.Enabled && os.Geteuid() != 0 && mode == "proxy" {
			mode = "tun"
		}
		if config.Rules.Mode == tailsplit.RoutingGlobal {
			routingMode = tailsplit.RoutingGlobal
		}
		trafficExit = (tailsplit.Config{Rules: tailsplit.RulesConfig{
			Mode: config.Rules.Mode, DefaultExit: config.Rules.DefaultExit, GlobalExit: config.Rules.GlobalExit,
		}}).TrafficExitID()
	}
	args := append(append([]string{}, c.options.HelperArgs...), runCommand, "--events",
		"--parent-pid", fmt.Sprint(os.Getpid()), "-c", c.options.ConfigPath)
	if focus != "" {
		args = append(args, "--exit", focus)
	}
	command := exec.Command(c.options.Helper, args...)
	command.Dir = filepath.Dir(c.options.ConfigPath)
	command.Env = append(os.Environ(), c.options.ExtraEnv...)
	command.Stderr = c.log
	command.Stdout = &eventWriter{callback: c.onEvent}
	input, err := prepareProcess(command, true)
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		if input != nil {
			_ = input.Close()
		}
		c.reset(Snapshot{State: "failed", Error: "启动失败，请查看日志"})
		_, _ = io.WriteString(c.log, "Could not launch the bundled helper.\n")
		return err
	}
	c.snapshot.State, c.snapshot.Mode = "starting", mode
	c.snapshot.Focus = focus
	c.snapshot.RoutingMode = routingMode
	c.trafficExit = trafficExit
	c.snapshot.Error, c.snapshot.LoginURL = "", ""
	// Only refresh primary authentication when the helper reads that device.
	// A focused login emits no primary status and must retain its saved state.
	if (focus == "" || focus == tailsplit.MainExit) &&
		(trafficExit == tailsplit.MainExit || mode == "login" || mode == "discovery") {
		c.snapshot.BackendState = ""
	}
	authExit := focus
	if mode == "proxy" || mode == "tun" {
		authExit = trafficExit
	}
	if authExit != "" && authExit != tailsplit.MainExit {
		// LoginFlow must wait for this helper's status, not a previous session.
		exit := c.snapshot.Exits[authExit]
		exit.BackendState = ""
		c.setExit(authExit, exit)
	}
	c.command, c.input = command, input
	c.stopping = false
	c.done = make(chan struct{})
	done := c.done
	go func() {
		err := command.Wait()
		if input != nil {
			_ = input.Close()
		}
		c.mu.Lock()
		if c.command == command {
			c.command, c.input = nil, nil
			if !c.stopping && c.snapshot.State == "failed" {
				// Preserve an actionable error delivered by the elevated core.
			} else if !c.stopping && err == nil && (c.snapshot.State == "authenticated" ||
				c.snapshot.State == "stopped" && c.snapshot.Mode == "discovery") {
				// Authentication can finish without ever starting the proxy.
				if c.snapshot.Mode == "discovery" {
					c.snapshot.Mode = ""
				}
			} else if err != nil && !c.stopping {
				c.reset(Snapshot{State: "failed", Error: "代理已退出，请查看日志"})
			} else {
				c.snapshot.State, c.snapshot.Mode, c.snapshot.LoginURL = "stopped", "", ""
			}
			c.snapshot.Focus = ""
			c.stopping = false
		}
		close(done)
		c.mu.Unlock()
	}()
	return nil
}

func (c *Controller) Stop() error {
	c.mu.Lock()
	c.stopping = true
	if c.pingCancel != nil {
		c.pingCancel()
		done := c.pingDone
		c.mu.Unlock()
		select {
		case <-done:
		case <-time.After(c.stopWait):
			// Keep the helper alive and its lease held while the service finishes
			// cleanup; a later operation can retry once pingDone closes.
			c.mu.Lock()
			c.stopping = false
			c.mu.Unlock()
			return tailsplit.ErrPingBusy
		}
		c.mu.Lock()
	}
	command, done, input := c.command, c.done, c.input
	if command == nil {
		c.stopping = false
		c.mu.Unlock()
		return nil
	}
	c.snapshot.State = "stopping"
	c.mu.Unlock()
	if err := requestProcessStop(command, input); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-done:
		return nil
	case <-time.After(c.stopWait):
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		<-done
		// The broker is gone, so the service's authenticated socket sees EOF
		// and cancels its core. Keep follow-up repair/logout/restart available.
		// The service still rejects a new core until its cleanup has finished.
		_, _ = io.WriteString(c.log, "Bundled helper required a forced stop; its service session is closing.\n")
		return nil
	}
}

func (c *Controller) Logout() error {
	return c.LogoutExit("")
}

// LogoutExit signs out one device. Other devices keep their logins.
func (c *Controller) LogoutExit(id string) error {
	focus := id
	if focus == "" {
		focus = tailsplit.MainExit
	}
	if id == tailsplit.MainExit {
		id = ""
	}
	c.mu.Lock()
	if c.closed || c.loggingOut {
		c.mu.Unlock()
		return errors.New("the app is closing or logout is already in progress")
	}
	c.loggingOut = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.loggingOut = false
		c.mu.Unlock()
	}()
	if err := c.Stop(); err != nil {
		return err
	}
	c.mu.Lock()
	previous := c.snapshot
	if id == "" {
		c.reset(Snapshot{State: "logging-out"})
	} else {
		c.snapshot.State = "logging-out"
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	logoutCommand := "logout"
	if len(c.options.HelperArgs) == 0 && (runtime.GOOS == "darwin" || runtime.GOOS == "windows") {
		logoutCommand = "app-logout"
	}
	args := append(append([]string{}, c.options.HelperArgs...), logoutCommand, "-c", c.options.ConfigPath)
	args = append(args, "--exit", focus)
	command := exec.CommandContext(ctx, c.options.Helper, args...)
	command.Dir = filepath.Dir(c.options.ConfigPath)
	command.Env = append(os.Environ(), c.options.ExtraEnv...)
	if _, err := prepareProcess(command, false); err != nil {
		return err
	}
	data, err := command.CombinedOutput()
	_, _ = io.WriteString(c.log, RedactLog(string(data)))
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.reset(Snapshot{State: "failed", Error: "退出设备登录失败，请查看日志"})
		return err
	}
	if id != "" {
		previous.State, previous.Mode = "stopped", ""
		previous.Exits = c.snapshot.Exits
		c.snapshot = previous
		c.setExit(id, ExitSnapshot{BackendState: "NeedsLogin"})
		return nil
	}
	c.reset(Snapshot{State: "stopped", BackendState: "NeedsLogin"})
	return nil
}

// RemoveExit performs a local device logout and revision-checked config
// removal. Unlike Reconfigure, it deliberately leaves the connection stopped.
func (c *Controller) RemoveExit(id, revision string) error {
	return c.removeExit(id, revision, c.LogoutExit)
}

func (c *Controller) removeExit(id, revision string, logout func(string) error) error {
	if err := c.beginReconfigure(); err != nil {
		return err
	}
	defer c.endReconfigure()
	if err := RemoveExitDocument(c.options.ConfigPath, id, revision, logout); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == tailsplit.MainExit {
		c.reset(Snapshot{State: "stopped"})
	} else {
		delete(c.snapshot.Exits, id)
		c.snapshot.State, c.snapshot.Mode, c.snapshot.LoginURL = "stopped", "", ""
	}
	return nil
}

// SwitchNetwork leaves the proxy disconnected and requests a fresh interactive
// device authorization. Desktop callers serialize it with other user actions.
func (c *Controller) SwitchNetwork() error {
	save, err := prepareNetworkSwitch(c.options.ConfigPath)
	if err != nil {
		return err
	}
	if err := c.Logout(); err != nil {
		return err
	}
	if err := save(); err != nil {
		return err
	}
	return c.StartLogin()
}

func (c *Controller) Close() error {
	c.close.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		err := c.Stop()
		c.closeErr = errors.Join(err, c.log.Close())
	})
	return c.closeErr
}

func (c *Controller) onEvent(data []byte) {
	var event struct {
		Event          string               `json:"event"`
		BackendState   string               `json:"backend_state"`
		AuthURL        string               `json:"auth_url"`
		ExitNode       string               `json:"exit_node"`
		ExitOnline     bool                 `json:"exit_online"`
		ExitVisible    bool                 `json:"exit_visible"`
		NetworkName    string               `json:"network_name"`
		MagicDNSSuffix string               `json:"magic_dns_suffix"`
		Mode           string               `json:"mode"`
		RoutingMode    string               `json:"routing_mode"`
		Message        string               `json:"message"`
		Phase          string               `json:"phase"`
		Hostname       string               `json:"hostname"`
		ExitNodes      []tailsplit.ExitNode `json:"exit_nodes"`
		PingAvailable  bool                 `json:"ping_available"`
		Exit           string               `json:"exit"`
	}
	if json.Unmarshal(data, &event) != nil {
		return
	}
	if event.Event == "log" {
		_, _ = io.WriteString(c.log, RedactLog(event.Message)+"\n")
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.command == nil || c.stopping || c.snapshot.State == "failed" {
		// A preflight error ends this session. Late status or ready events during
		// cleanup must not replace its actionable error before the helper exits.
		return
	}
	if event.Mode != "" {
		c.snapshot.Mode = event.Mode
	}
	if event.RoutingMode != "" {
		c.snapshot.RoutingMode = event.RoutingMode
	}
	if event.Event == "authorizing" {
		c.snapshot.State = "authorizing"
		return
	}
	if event.Event == "preflight" {
		c.snapshot.State = "checking-exit"
		if c.snapshot.Mode == "login" {
			c.snapshot.State = "authenticating"
		} else if c.snapshot.Mode == "discovery" {
			c.snapshot.State = "discovering"
		}
		return
	}
	if event.Event == "authenticated" {
		c.snapshot.State = "authenticated"
		c.snapshot.LoginURL = ""
		if c.snapshot.Focus != "" && c.snapshot.Focus != tailsplit.MainExit {
			exit := c.snapshot.Exits[c.snapshot.Focus]
			exit.BackendState = "Running"
			c.setExit(c.snapshot.Focus, exit)
		} else {
			c.snapshot.BackendState = "Running"
		}
		return
	}
	if event.Event == "discovered" {
		c.snapshot.State = "stopped"
		c.snapshot.LoginURL = ""
		return
	}
	if event.Event == "error" {
		c.snapshot.State = "failed"
		c.snapshot.LoginURL = ""
		c.snapshot.Error = RedactLog(event.Message)
		_, _ = io.WriteString(c.log, c.snapshot.Error+"\n")
		return
	}
	if event.Event == "ready" {
		c.snapshot.State = "connecting"
		return
	}
	if event.Event != "status" {
		return
	}
	trafficConnection := c.snapshot.Mode == "proxy" || c.snapshot.Mode == "tun"
	selected := c.trafficExit
	if selected == tailsplit.MainExit {
		selected = ""
	}
	if event.Exit != "" {
		c.setExit(event.Exit, ExitSnapshot{
			BackendState: event.BackendState, NetworkName: event.NetworkName, MagicDNSSuffix: event.MagicDNSSuffix, Hostname: event.Hostname,
			ExitNode: event.ExitNode, ExitOnline: event.ExitOnline, ExitVisible: event.ExitVisible,
			ExitNodes: event.ExitNodes, PingAvailable: event.PingAvailable,
		})
		if c.snapshot.Mode == "login" && event.Exit == c.snapshot.Focus {
			// A focused login drives the session state and its browser link.
			c.snapshot.LoginURL = ""
			if validLoginURL(event.AuthURL) {
				c.snapshot.LoginURL = event.AuthURL
			}
			if event.BackendState == "NeedsLogin" || event.AuthURL != "" {
				c.snapshot.State = "login"
			} else {
				c.snapshot.State = "authenticating"
			}
			return
		}
		if !trafficConnection || event.Exit != selected {
			return
		}
	} else {
		c.snapshot.ExitNode = event.ExitNode
		c.snapshot.Hostname = event.Hostname
		c.snapshot.ExitNodes = event.ExitNodes
		c.snapshot.BackendState = event.BackendState
		c.snapshot.PingAvailable = event.PingAvailable
		c.snapshot.NetworkName = event.NetworkName
		c.snapshot.MagicDNSSuffix = event.MagicDNSSuffix
		if trafficConnection && selected != "" {
			return
		}
	}
	c.snapshot.LoginURL = ""
	if validLoginURL(event.AuthURL) {
		c.snapshot.LoginURL = event.AuthURL
	}
	switch {
	case c.snapshot.Mode == "discovery":
		c.snapshot.State = "discovering"
	case c.snapshot.Mode == "tun" && c.snapshot.RoutingMode == tailsplit.RoutingGlobal &&
		event.Phase != "preflight" && (event.BackendState != "Running" || !event.ExitOnline):
		// Keep the core and TUN alive. Only an explicit disconnect closes
		// them; an offline exit never switches clients to direct routing.
		c.snapshot.State = "blocked"
	case event.BackendState == "NeedsLogin" || event.AuthURL != "":
		c.snapshot.State = "login"
	case c.snapshot.Mode == "login":
		c.snapshot.State = "authenticating"
	case event.BackendState == "Direct":
		c.snapshot.State = "running"
	case event.BackendState == "Running" && event.Phase == "preflight":
		// The peer list and selected-exit metadata arrive incrementally. Keep
		// checking until preflight confirms readiness or reports its timeout.
		c.snapshot.State = "checking-exit"
	case event.BackendState == "Running" && event.ExitOnline:
		c.snapshot.State = "running"
	case event.BackendState == "Running" && !event.ExitVisible:
		c.snapshot.State = "missing-exit"
	case event.BackendState == "Running":
		c.snapshot.State = "waiting-exit"
	default:
		c.snapshot.State = "connecting"
	}
}

func (c *Controller) setExit(id string, exit ExitSnapshot) {
	if c.snapshot.Exits == nil {
		c.snapshot.Exits = map[string]ExitSnapshot{}
	}
	c.snapshot.Exits[id] = exit
}

var privateLogMaterial = regexp.MustCompile(`https://login\.tailscale\.com/a/[^\s]+|tskey-[A-Za-z0-9_-]+`)

// RedactLog removes device authorization links and keys from UI-facing logs.
func RedactLog(message string) string {
	return privateLogMaterial.ReplaceAllString(message, "[redacted]")
}

func validLoginURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host == "login.tailscale.com" &&
		u.User == nil && len(u.Path) > 3 && u.Path[:3] == "/a/"
}

type eventWriter struct {
	mu       sync.Mutex
	buffer   []byte
	callback func([]byte)
}

func (w *eventWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(data)
	if len(w.buffer)+len(data) > 64<<10 {
		w.buffer = nil
		return n, nil
	}
	w.buffer = append(w.buffer, data...)
	for {
		index := bytes.IndexByte(w.buffer, '\n')
		if index < 0 {
			break
		}
		w.callback(w.buffer[:index])
		w.buffer = w.buffer[index+1:]
	}
	return n, nil
}

type rotatingLog struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func openLog(path string) (*rotatingLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	return &rotatingLog{path: path, file: file, size: stat.Size()}, nil
}

func (l *rotatingLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size+int64(len(data)) > 5<<20 {
		if err := l.file.Close(); err != nil {
			return 0, err
		}
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			return 0, err
		}
		file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return 0, err
		}
		l.file, l.size = file, 0
	}
	n, err := l.file.Write(data)
	l.size += int64(n)
	return n, err
}

func (l *rotatingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}
