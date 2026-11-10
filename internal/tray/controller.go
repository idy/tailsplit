package tray

import (
	"bytes"
	"context"
	"crypto/rand"
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
	TrafficExit    string
	Phase          string
	BackendState   string
	Error          string
	ErrorCode      string
	AddedExit      string
	AddedRevision  string
	ConnectRequest string
	Hostname       string
	ExitNodes      []tailsplit.ExitNode
	Active         bool
	PingAvailable  bool
	Traffic        *tailsplit.TrafficSnapshot
	// Exits holds additional devices' latest status, keyed by exit ID. The
	// identity fields above always describe the primary device; State, Active,
	// Mode and RoutingMode describe the overall connection.
	Exits map[string]ExitSnapshot
	// Focus names the additional exit an active login session belongs to.
	Focus string
}

type ExitSnapshot struct {
	// Phase distinguishes runtime isolation from an SDK authentication state.
	Phase          string
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
	if s.Traffic != nil {
		traffic := *s.Traffic
		traffic.Routes = make(map[string]tailsplit.TrafficCounters, len(s.Traffic.Routes))
		for id, counters := range s.Traffic.Routes {
			traffic.Routes[id] = counters
		}
		s.Traffic = &traffic
	}
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
	snapshot.ConnectRequest = c.snapshot.ConnectRequest
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
	pendingAdd    *pendingExitAddition
	preparation   *servicePreparation
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
	snapshot.Active = c.command != nil || c.loggingOut || c.preparation != nil
	snapshot.TrafficExit = c.trafficExit
	snapshot.PingAvailable = c.pingAvailableLocked()
	return snapshot
}

// RequestConnectionSetup lets a native Connect click enter the same guided
// path as the window switch. It never starts a helper or changes configuration.
func (c *Controller) RequestConnectionSetup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot.ConnectRequest = rand.Text()
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

// beginReconfigure keeps independent starts and Ping requests out of the gap
// between checking a candidate, stopping a device and starting its replacement.
func (c *Controller) beginReconfigure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.loggingOut || c.reconfiguring || c.preparation != nil {
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
	return c.startReconfigured()
}

// StartLogin authenticates the embedded device, preparing its service if needed, without
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
	return c.ReconfigureChecked(nil, update)
}

// ReconfigureChecked checks the candidate against the current session before
// stopping it. Independent starts cannot change that decision until the update
// and any internal restart finish. A rejected check leaves the helper untouched.
func (c *Controller) ReconfigureChecked(check func(Snapshot) error, update func() error) error {
	if err := c.beginReconfigure(); err != nil {
		return err
	}
	defer c.endReconfigure()
	before := c.Snapshot()
	if check != nil {
		if err := check(before); err != nil {
			return err
		}
	}
	reconnect := before.Active && (before.Mode == "tun" || before.Mode == "proxy")
	if err := c.Stop(); err != nil {
		return err
	}
	err := update()
	if reconnect {
		err = errors.Join(err, c.startReconfigured())
	} else if err == nil {
		// A repaired configuration is idle; discard an earlier helper failure.
		c.mu.Lock()
		if c.command == nil && (c.snapshot.State == "failed" || c.snapshot.State == "stopped" && c.snapshot.Error != "") {
			c.reset(Snapshot{State: "stopped"})
		}
		c.mu.Unlock()
	}
	return err
}

func (c *Controller) start(mode, focus string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reconfiguring {
		return tailsplit.ErrPingBusy
	}
	return c.startLocked(mode, focus)
}

// Only the owner of the reconfiguration gate may bypass the public-start guard.
func (c *Controller) startReconfigured() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startLocked("proxy", "")
}

func (c *Controller) startLocked(mode, focus string) error {
	return c.startConfigLocked(mode, focus, c.options.ConfigPath)
}

func (c *Controller) startConfigLocked(mode, focus, configPath string) error {
	freshLogin := mode == "login-new"
	if freshLogin {
		mode = "login"
	}
	if c.preparation != nil {
		return tailsplit.ErrPingBusy
	}
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
		data, err := os.ReadFile(configPath)
		if err != nil {
			return err
		}
		var config struct {
			TUN struct {
				Enabled bool `yaml:"enabled"`
			} `yaml:"tun"`
			Exits []struct {
				ID string `yaml:"id"`
			} `yaml:"exits"`
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
		if mode == "proxy" || mode == "tun" || mode == "discovery" {
			for _, exit := range config.Exits {
				if _, ok := c.snapshot.Exits[exit.ID]; !ok {
					c.setExit(exit.ID, ExitSnapshot{})
				}
			}
		}
	}
	if freshLogin {
		runCommand = "app-login-new"
	}
	args := append(append([]string{}, c.options.HelperArgs...), runCommand, "--events",
		"--parent-pid", fmt.Sprint(os.Getpid()), "-c", configPath)
	if focus != "" {
		args = append(args, "--exit", focus)
	}
	command := exec.Command(c.options.Helper, args...)
	command.Dir = filepath.Dir(configPath)
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
	c.snapshot.Traffic = nil
	c.snapshot.RoutingMode = routingMode
	c.trafficExit = trafficExit
	c.snapshot.Error, c.snapshot.ErrorCode, c.snapshot.LoginURL = "", "", ""
	if mode == "proxy" || mode == "tun" || mode == "discovery" {
		// A new traffic/discovery helper must observe every device afresh. An
		// optional endpoint can be omitted without ever producing SDK status.
		c.invalidateDeviceStatuses(routingMode, trafficExit, mode != "discovery")
	} else if focus == "" || focus == tailsplit.MainExit {
		c.invalidatePrimaryStatus("")
	} else {
		// Focused login preserves other devices' cached authentication.
		exit := c.snapshot.Exits[focus]
		c.setExit(focus, ExitSnapshot{NetworkName: exit.NetworkName, MagicDNSSuffix: exit.MagicDNSSuffix, Hostname: exit.Hostname})
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
			c.finishPendingAddLocked(err)
			c.command, c.input = nil, nil
			c.snapshot.Traffic = nil
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

func (c *Controller) invalidatePrimaryStatus(phase string) {
	c.snapshot.Phase = phase
	c.snapshot.BackendState, c.snapshot.ExitNode = "", ""
	c.snapshot.ExitNodes, c.snapshot.PingAvailable = nil, false
}

func (c *Controller) invalidateDeviceStatuses(routingMode, trafficExit string, traffic bool) {
	phase := func(id string) string {
		if traffic && routingMode == tailsplit.RoutingGlobal && id != trafficExit {
			return "omitted"
		}
		return ""
	}
	c.invalidatePrimaryStatus(phase(tailsplit.MainExit))
	for id, exit := range c.snapshot.Exits {
		c.setExit(id, ExitSnapshot{Phase: phase(id), NetworkName: exit.NetworkName,
			MagicDNSSuffix: exit.MagicDNSSuffix, Hostname: exit.Hostname})
	}
}

func (c *Controller) Stop() error {
	c.mu.Lock()
	c.stopping = true
	if c.pingCancel != nil {
		c.pingCancel()
	}
	pingDone := c.pingDone
	var preparationDone <-chan struct{}
	if c.preparation != nil {
		c.preparation.cancel()
		preparationDone = c.preparation.done
		c.snapshot.State = "stopping"
	}
	command, done, input := c.command, c.done, c.input
	if command != nil {
		c.snapshot.State = "stopping"
	}
	c.mu.Unlock()
	// Stop the network session even when a failed Ping is still recovering its
	// channel. Waiting for Ping first can prevent the active engine from closing.
	if command != nil {
		if err := requestProcessStop(command, input); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
	}
	timer := time.NewTimer(c.stopWait)
	defer timer.Stop()
	for done != nil || pingDone != nil || preparationDone != nil {
		select {
		case <-done:
			done = nil
		case <-pingDone:
			pingDone = nil
		case <-preparationDone:
			preparationDone = nil
		case <-timer.C:
			// The helper owns the saved-state lease until service cleanup is
			// confirmed. Killing it here would release that lease too early.
			c.mu.Lock()
			if c.command == nil {
				c.stopping = false
			}
			c.mu.Unlock()
			if pingDone != nil {
				return tailsplit.ErrPingBusy
			}
			return errors.New("network service cleanup is still in progress")
		}
	}
	c.mu.Lock()
	c.stopping = false
	c.mu.Unlock()
	return nil
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
	logoutCommand := "logout"
	if len(c.options.HelperArgs) == 0 && (runtime.GOOS == "darwin" || runtime.GOOS == "windows") {
		logoutCommand = "app-logout"
	}
	args := append(append([]string{}, c.options.HelperArgs...), logoutCommand, "-c", c.options.ConfigPath)
	args = append(args, "--exit", focus)
	// The logout helper has its own operation deadline and must outlive that
	// deadline while it confirms service cleanup and retains the device lease.
	command := exec.Command(c.options.Helper, args...)
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
		Event          string                     `json:"event"`
		BackendState   string                     `json:"backend_state"`
		AuthURL        string                     `json:"auth_url"`
		ExitNode       string                     `json:"exit_node"`
		ExitOnline     bool                       `json:"exit_online"`
		ExitVisible    bool                       `json:"exit_visible"`
		NetworkName    string                     `json:"network_name"`
		MagicDNSSuffix string                     `json:"magic_dns_suffix"`
		Mode           string                     `json:"mode"`
		RoutingMode    string                     `json:"routing_mode"`
		Message        string                     `json:"message"`
		Code           string                     `json:"code"`
		Phase          string                     `json:"phase"`
		Hostname       string                     `json:"hostname"`
		ExitNodes      []tailsplit.ExitNode       `json:"exit_nodes"`
		PingAvailable  bool                       `json:"ping_available"`
		Exit           string                     `json:"exit"`
		Traffic        *tailsplit.TrafficSnapshot `json:"traffic"`
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
	if event.Event == "traffic" {
		if (c.snapshot.Mode == "proxy" || c.snapshot.Mode == "tun") && event.Traffic != nil &&
			event.Traffic.SessionID != "" && !event.Traffic.SampledAt.IsZero() && event.Traffic.Routes != nil {
			c.snapshot.Traffic = event.Traffic
		}
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
		if c.pendingAdd != nil && c.snapshot.Mode == "login" && c.snapshot.Focus == c.pendingAdd.id {
			c.pendingAdd.authenticated = true
		}
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
		c.snapshot.ErrorCode = event.Code
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
	if event.Phase == "isolated" {
		// Isolation is an application runtime decision, not an invented SDK
		// identity. Never retain availability from an earlier status/session.
		event.AuthURL, event.ExitNode = "", ""
		event.ExitOnline, event.ExitVisible, event.PingAvailable = false, false, false
		event.ExitNodes = nil
	}
	selected := c.trafficExit
	if selected == tailsplit.MainExit {
		selected = ""
	}
	if event.Exit != "" {
		c.setExit(event.Exit, ExitSnapshot{
			Phase: event.Phase, BackendState: event.BackendState, NetworkName: event.NetworkName, MagicDNSSuffix: event.MagicDNSSuffix, Hostname: event.Hostname,
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
		c.snapshot.Phase = event.Phase
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
