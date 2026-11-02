package tailsplit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
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
	box         *box.Box
	cancel      context.CancelFunc
	unlock      func() error
	once        sync.Once
	closeErr    error
	exitNode    string
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
	data := compiled.JSON
	if config.Tailscale != nil && config.Tailscale.AuthKeyEnv != "" {
		key := os.Getenv(config.Tailscale.AuthKeyEnv)
		if key == "" {
			return nil, fmt.Errorf("required authentication variable %s is empty", config.Tailscale.AuthKeyEnv)
		}
		var native object
		if err := json.Unmarshal(data, &native); err != nil {
			return nil, err
		}
		endpoints, ok := native["endpoints"].([]any)
		if !ok || len(endpoints) != 1 {
			return nil, errors.New("compiled config does not contain the expected Tailscale endpoint")
		}
		endpoint, ok := endpoints[0].(map[string]any)
		if !ok || endpoint["type"] != "tailscale" {
			return nil, errors.New("compiled config does not contain the expected Tailscale endpoint")
		}
		endpoint["auth_key"] = key
		data, err = json.Marshal(native)
		if err != nil {
			return nil, errors.New("could not inject the authentication key")
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
	engine := &Engine{box: instance, cancel: cancel, unlock: unlock, routingMode: config.Rules.Mode}
	for _, in := range options.Inbounds {
		if in.Type == "tun" {
			engine.hasTUN = true
			if options, ok := in.Options.(*option.TunInboundOptions); ok && options.DNSMode == "hijack" {
				engine.dnsListen = systemDNSListen()
			}
		}
	}
	if config.Tailscale != nil {
		engine.exitNode = config.Tailscale.ExitNode
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
	Event          string     `json:"event"`
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
}

// ExitNode is the bounded, credential-free metadata used by the local picker.
// ID is an address accepted by sing-box's exit_node configuration.
type ExitNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Online   bool   `json:"online"`
	Selected bool   `json:"selected"`
}

func (e *Engine) WatchStatus(ctx context.Context, callback func(Status)) error {
	endpoint, exists := e.box.Endpoint().Get("tailnet")
	if !exists {
		callback(Status{Event: "status", BackendState: "Direct"})
		return nil
	}
	tailnet, ok := endpoint.(adapter.TailscaleEndpoint)
	if !ok {
		return errors.New("configured endpoint does not expose Tailscale status")
	}
	return tailnet.SubscribeTailscaleStatus(ctx, func(current *adapter.TailscaleEndpointStatus) {
		status := connectionStatus(current, e.exitNode)
		status.RoutingMode = e.routingMode
		callback(status)
	})
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
					status.ExitNodes = append(status.ExitNodes, ExitNode{
						ID: id, Name: name, Online: peer.Online && !peer.Expired,
						Selected: configuredExit != "" && matchesExit(peer, configuredExit),
					})
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
	if config.Tailscale != nil {
		if err := PrepareTailnet(ctx, config, nil, nil); err != nil {
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

func privateState(path string) error {
	return privatefs.EnsureDir(path)
}
