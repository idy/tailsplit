package tailsplit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	"github.com/idy/tailsplit/internal/privatefs"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport"
	"github.com/sagernet/sing-box/dns/transport/fakeip"
	singlog "github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/direct"
	"github.com/sagernet/sing-box/protocol/mixed"
	"github.com/sagernet/sing-box/protocol/tun"
	singJSON "github.com/sagernet/sing/common/json"
)

const CoreVersion = "1.14.4"

// Engine embeds sing-box in this process. A desktop app can use Open, Start
// and Close without launching a sing-box subprocess. A connected core always
// owns a DNS TUN; only traffic-capture mode changes the system DNS resolver.
type Engine struct {
	ctx         context.Context
	ping        sync.Mutex
	box         *box.Box
	cancel      context.CancelFunc
	unlock      func() error
	once        sync.Once
	closeErr    error
	exits       []engineExit
	routingMode string
	hasTUN      bool
	dnsListen   string
	closeDNS    func() error
}

func engineContext(ctx context.Context) context.Context {
	in := inbound.NewRegistry()
	mixed.RegisterInbound(in)
	direct.RegisterInbound(in)
	tun.RegisterInbound(in)
	out := outbound.NewRegistry()
	direct.RegisterOutbound(out)
	registerIPv4DirectOutbound(out)
	endpoints := endpoint.NewRegistry()
	transports := dns.NewTransportRegistry()
	transport.RegisterUDP(transports)
	transport.RegisterTCP(transports)
	registerLocalDNS(transports)
	fakeip.RegisterTransport(transports)
	registerTailscale(endpoints, transports)
	return box.Context(ctx, in, out, endpoints, transports, service.NewRegistry(), certificate.NewRegistry())
}

func parseNative(ctx context.Context, data []byte) (option.Options, error) {
	var options option.Options
	if err := singJSON.UnmarshalContext(ctx, data, &options); err != nil {
		return options, fmt.Errorf("validate generated sing-box config: %w", err)
	}
	return options, nil
}

// Check constructs the core without starting listeners, a TUN, or a Tailnet
// login. No authentication environment variables are read by this operation.
func Check(compiled Compiled) error {
	ctx := engineContext(context.Background())
	options, err := parseNative(ctx, compiled.JSON)
	if err != nil {
		return err
	}
	instance, err := box.New(box.Options{Options: options, Context: ctx})
	if err != nil {
		return fmt.Errorf("initialize sing-box config: %w", err)
	}
	return instance.Close()
}

// Open prepares an owner-only state directory and locks it for this process.
// Only Start joins the Tailnet or begins handling traffic.
func Open(ctx context.Context, config Config, compiled Compiled) (*Engine, error) {
	return OpenWithLogWriter(ctx, config, compiled, nil)
}

// OpenWithLogWriter also delivers logs to a private local UI channel.
func OpenWithLogWriter(ctx context.Context, config Config, compiled Compiled, writer singlog.PlatformWriter) (*Engine, error) {
	return openWithLogWriter(ctx, config, compiled, writer, false)
}

func openWithLogWriter(ctx context.Context, config Config, compiled Compiled, writer singlog.PlatformWriter, fresh bool) (*Engine, error) {
	if compiled.runtimeExit != "" {
		// Watch, authenticate and ping exactly the device Global compiled.
		var err error
		if config, err = config.FocusExit(compiled.runtimeExit); err != nil {
			return nil, err
		}
	}
	if err := privateState(config.StateDir); err != nil {
		return nil, err
	}
	unlock, err := lockState(config.StateDir)
	if err != nil {
		return nil, err
	}
	release := true
	defer func() {
		if release {
			_ = unlock()
		}
	}()
	if fresh {
		// Reserve under the same state lease as every saved-device operation,
		// before parsing or constructing any endpoint that could reuse state.
		if err := ReserveNewExitState(config); err != nil {
			return nil, err
		}
	}
	data := compiled.JSON
	if config.Tailscale != nil && config.Tailscale.AuthKeyEnv != "" {
		var native object
		if err := json.Unmarshal(data, &native); err != nil {
			return nil, err
		}
		endpoints, _ := native["endpoints"].([]any)
		var endpoint map[string]any
		for _, candidate := range endpoints {
			// The environment key authenticates only the primary device.
			if value, ok := candidate.(map[string]any); ok && value["type"] == "tailscale" && value["tag"] == "tailnet" {
				endpoint = value
			}
		}
		if endpoint != nil {
			// A pending primary may be omitted while another device carries traffic.
			// Its authentication variable is irrelevant until its endpoint runs.
			key := os.Getenv(config.Tailscale.AuthKeyEnv)
			if key == "" {
				return nil, fmt.Errorf("required authentication variable %s is empty", config.Tailscale.AuthKeyEnv)
			}
			endpoint["auth_key"] = key
			data, err = json.Marshal(native)
			if err != nil {
				return nil, errors.New("could not inject the authentication key")
			}
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	ctx = engineContext(ctx)
	options, err := parseNative(ctx, data)
	if err != nil {
		cancel()
		return nil, err
	}
	if writer != nil {
		level, err := singlog.ParseLevel(config.LogLevel)
		if err != nil {
			level = singlog.LevelInfo
		}
		writer = filteredLogWriter{writer: writer, level: level}
		// The private UI channel is the sole log destination in app mode.
		// PlatformWriter otherwise receives every trace message in addition
		// to the console logger, regardless of the configured log level.
		options.Log.Output = os.DevNull
	}
	instance, err := box.New(box.Options{Options: options, Context: ctx, PlatformLogWriter: writer})
	if err != nil {
		cancel()
		// Do not echo native options or credentials in an initialization error.
		return nil, errors.New("could not initialize the embedded sing-box engine")
	}
	release = false
	engine := &Engine{ctx: ctx, box: instance, cancel: cancel, unlock: unlock, routingMode: config.Rules.Mode}
	for _, in := range options.Inbounds {
		if in.Type == "tun" {
			engine.hasTUN = true
			if options, ok := in.Options.(*option.TunInboundOptions); ok && options.DNSMode == "hijack" {
				engine.dnsListen = systemDNSListen()
			}
		}
	}
	primary := engineExit{tag: "tailnet"}
	if config.Tailscale != nil {
		primary.exitNode, primary.tailnet = config.Tailscale.ExitNode, config.Tailscale.Tailnet
		if config.Focus != MainExit {
			primary.id = config.Focus
		}
	}
	// Watch exactly the compiled devices; pending exits may be omitted.
	for _, endpoint := range options.Endpoints {
		if endpoint.Type != "tailscale" {
			continue
		}
		if endpoint.Tag == "tailnet" {
			engine.exits = append(engine.exits, primary)
			continue
		}
		for _, exit := range config.Exits {
			if config.Focus == "" && endpoint.Tag == ExitTag(exit.ID) {
				engine.exits = append(engine.exits, engineExit{tag: endpoint.Tag, id: exit.ID, exitNode: exit.ExitNode, tailnet: exit.Tailnet})
			}
		}
	}
	return engine, nil
}

type filteredLogWriter struct {
	writer singlog.PlatformWriter
	level  singlog.Level
}

func (w filteredLogWriter) WriteMessage(level singlog.Level, message string) {
	if level <= w.level {
		w.writer.WriteMessage(level, message)
	}
}

func (e *Engine) Start() error {
	if err := e.box.Start(); err != nil {
		_ = e.Close()
		return fmt.Errorf("start embedded sing-box: %w", err)
	}
	if e.dnsListen != "" {
		closeDNS, err := installSystemDNS(e.dnsListen)
		if err != nil {
			_ = e.Close()
			return err
		}
		e.closeDNS = closeDNS
	}
	return nil
}

// Status exposes only the connection information needed by a local UI.
// AuthURL is transient login material; callers must not publish it.
type Status struct {
	Event string `json:"event"`
	// Exit is empty for the primary device and otherwise an additional exit ID.
	Exit           string     `json:"exit,omitempty"`
	RoutingMode    string     `json:"routing_mode,omitempty"`
	BackendState   string     `json:"backend_state,omitempty"`
	AuthURL        string     `json:"auth_url,omitempty"`
	ExitNode       string     `json:"exit_node,omitempty"`
	ExitOnline     bool       `json:"exit_online"`
	NetworkName    string     `json:"network_name,omitempty"`
	MagicDNSSuffix string     `json:"magic_dns_suffix,omitempty"`
	Phase          string     `json:"phase,omitempty"`
	ExitVisible    bool       `json:"exit_visible"`
	Hostname       string     `json:"hostname,omitempty"`
	ExitNodes      []ExitNode `json:"exit_nodes"`
	PingAvailable  bool       `json:"ping_available"`
}

// ExitNode is the bounded, credential-free metadata used by the local picker.
// ID is an address accepted by sing-box's exit_node configuration.
type ExitNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Online   bool   `json:"online"`
	Selected bool   `json:"selected"`
	// Bind cached selection metadata to the configuration that produced it.
	SelectedFor string `json:"selected_for,omitempty"`
}

type engineExit struct {
	tag, id, exitNode, tailnet string
	// magicDNSSuffix binds traffic preflight to the saved routing metadata.
	// Device-only sessions authenticate the Tailnet guard independently.
	magicDNSSuffix string
}

// WatchStatus reports every device; Status.Exit identifies additional exits.
// The callback may be called concurrently for different exits.
func (e *Engine) WatchStatus(ctx context.Context, callback func(Status)) error {
	if len(e.exits) == 0 {
		callback(Status{Event: "status", BackendState: "Direct"})
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var callbackMu sync.Mutex
	errs := make(chan error, len(e.exits))
	for _, exit := range e.exits {
		go func() {
			endpoint, exists := e.box.Endpoint().Get(exit.tag)
			if !exists {
				errs <- errors.New("configured Tailscale endpoint is missing")
				return
			}
			tailnet, ok := endpoint.(adapter.TailscaleEndpoint)
			if !ok {
				errs <- errors.New("configured endpoint does not expose Tailscale status")
				return
			}
			errs <- tailnet.SubscribeTailscaleStatus(ctx, func(current *adapter.TailscaleEndpointStatus) {
				status := connectionStatus(current, exit.exitNode)
				status.Exit = exit.id
				status.RoutingMode = e.routingMode
				_, supportsPing := endpoint.(exitPingEndpoint)
				status.PingAvailable = supportsPing && status.BackendState == "Running"
				callbackMu.Lock()
				defer callbackMu.Unlock()
				callback(status)
			})
		}()
	}
	var result error
	for range e.exits {
		if err := <-errs; err != nil && result == nil {
			result = err
			cancel()
		}
	}
	return result
}

// endpointTag maps a ping/status exit ID to this engine's endpoint tag.
func (e *Engine) endpoint(exit string) (engineExit, bool) {
	if exit == MainExit {
		exit = ""
	}
	for _, candidate := range e.exits {
		if candidate.id == exit {
			return candidate, true
		}
	}
	return engineExit{}, false
}

func connectionStatus(current *adapter.TailscaleEndpointStatus, configuredExit string) Status {
	status := Status{
		Event: "status", BackendState: current.BackendState, AuthURL: current.AuthURL,
		NetworkName:    current.NetworkName,
		MagicDNSSuffix: current.MagicDNSSuffix,
	}
	if current.Self != nil {
		status.Hostname = current.Self.HostName
	}
	if current.ExitNode != nil {
		status.ExitNode = current.ExitNode.HostName
		if configuredExit == "" || matchesExit(current.ExitNode, configuredExit) {
			status.ExitVisible = true
			status.ExitOnline = current.ExitNode.Online && !current.ExitNode.Expired
		}
	}
	for _, group := range current.UserGroups {
		for _, peer := range group.Peers {
			if peer.ExitNodeOption {
				id := peer.DNSName
				for _, ip := range peer.TailscaleIPs {
					if address, err := netip.ParseAddr(ip); err == nil && address.Is4() {
						id = ip
						break
					}
				}
				if id == "" {
					id = peer.HostName
				}
				if id != "" {
					name := peer.HostName
					if name == "" {
						name = id
					}
					node := ExitNode{
						ID: id, Name: name, Online: peer.Online && !peer.Expired,
						Selected: configuredExit != "" && matchesExit(peer, configuredExit),
					}
					if node.Selected {
						node.SelectedFor = configuredExit
					}
					status.ExitNodes = append(status.ExitNodes, node)
				}
			}
			if configuredExit != "" && matchesExit(peer, configuredExit) {
				status.ExitVisible = true
				if !status.ExitOnline {
					status.ExitNode = peer.HostName
				}
			}
		}
	}
	sort.Slice(status.ExitNodes, func(i, j int) bool {
		if status.ExitNodes[i].Name != status.ExitNodes[j].Name {
			return status.ExitNodes[i].Name < status.ExitNodes[j].Name
		}
		return status.ExitNodes[i].ID < status.ExitNodes[j].ID
	})
	return status
}

func matchesExit(peer *adapter.TailscalePeer, configured string) bool {
	return slices.Contains(peer.TailscaleIPs, configured) || peer.HostName == configured ||
		peer.DNSName == configured || peer.StableID == configured
}

func (e *Engine) Close() error {
	e.once.Do(func() {
		e.cancel()
		if e.closeDNS != nil {
			e.closeErr = e.closeDNS()
		}
		e.closeErr = errors.Join(e.closeErr, e.box.Close(), e.unlock())
	})
	return e.closeErr
}

func Run(ctx context.Context, config Config, compiled Compiled) error {
	if len(config.ExitIDs()) > 0 {
		var err error
		compiled, err = PrepareConnection(ctx, config, compiled, nil, nil)
		if err != nil {
			return err
		}
	}
	engine, err := Open(ctx, config, compiled)
	if err != nil {
		return err
	}
	if err := engine.Start(); err != nil {
		return err
	}
	<-ctx.Done()
	return engine.Close()
}

func validateNewExitFocus(config Config) error {
	if !ValidExitID(config.Focus) || !config.hasExit(config.Focus) {
		return errors.New("new device authentication requires a configured additional exit")
	}
	return nil
}

// ReserveNewExitState exclusively claims a new additional device's namespace.
// The caller must hold this storage owner's saved-state lease. Every existing
// entry, including an empty directory, logout marker or symlink, rejects Add.
// Never remove this claim on failure: it prevents an unpublished identity from
// being reused by a retry or resurrected by ordinary saved-state migration.
func ReserveNewExitState(config Config) error {
	if err := validateNewExitFocus(config); err != nil {
		return err
	}
	exits := filepath.Join(config.StateDir, "exits")
	if err := privateState(exits); err != nil {
		return err
	}
	directory := filepath.Join(exits, config.Focus)
	if err := privatefs.CreateDir(directory); err != nil {
		if os.IsExist(err) {
			return errors.New("the new exit state namespace already exists; choose a new exit ID")
		}
		return err
	}
	// CreateDir is exclusive and establishes a protected Windows root ACL.
	// Keep the claim outside tailscale/, whose files the endpoint may remove.
	marker, err := privatefs.CreateFile(filepath.Join(directory, ".initialized"))
	if err != nil {
		return err
	}
	_, writeErr := marker.Write([]byte("1\n"))
	return errors.Join(writeErr, marker.Sync(), marker.Close())
}

func privateState(path string) error {
	return privatefs.EnsureDir(path)
}
