package tailsplit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/idy/tailsplit/internal/domainrules"
)

type object = map[string]any

// Compiled contains native sing-box JSON and audit information. The JSON never
// includes an authentication key; Run injects an explicitly requested env key.
type Compiled struct {
	JSON      []byte
	GFWList   *domainrules.RulesInfo
	GFWSource string
	// runtimeExit binds Global's single endpoint to its saved device metadata.
	// Empty means normal all-device compilation or an explicit focused session.
	runtimeExit string
	// privateExits lists compiled private-name owners by logical device ID.
	// Blocked owners remain present: their namespaces are reserved and rejected.
	privateExits []string
}

// Compile loads local or cached rules, downloading a remote source when its
// matching cache is missing. Use CompileContext to cancel a download.
func Compile(config Config) (Compiled, error) {
	return CompileContext(context.Background(), config)
}

func CompileContext(ctx context.Context, config Config) (Compiled, error) {
	return compileWithOptions(ctx, config, compileOptions{validateTraffic: true})
}

type compileOptions struct {
	blockedExits    map[string]bool
	validateTraffic bool
}

func compile(ctx context.Context, config Config) (Compiled, error) {
	return compileWithOptions(ctx, config, compileOptions{})
}

func compileWithOptions(ctx context.Context, config Config, options compileOptions) (Compiled, error) {
	base, err := os.Getwd()
	if err != nil {
		return Compiled{}, err
	}
	if err := config.normalize(base); err != nil {
		return Compiled{}, err
	}
	if options.validateTraffic {
		if config.Rules.Mode == RoutingGlobal && !config.TUN.Enabled {
			return Compiled{}, fmt.Errorf("global Kill Switch mode requires tun.enabled: true")
		}
		if err := ValidateTrafficExit(config); err != nil {
			return Compiled{}, err
		}
	}
	isBlocked := func(id string) bool {
		return !config.includePending && (options.blockedExits[id] || id == MainExit && options.blockedExits[""])
	}
	for _, id := range config.ExitIDs() {
		if isBlocked(id) && requiresTrafficExit(config, id) {
			return Compiled{}, fmt.Errorf("cannot block required traffic exit %q", id)
		}
	}
	// Global traffic runs only its selected saved device. Device-only sessions
	// (login, discovery, logout) keep inspecting every configured device.
	runtimeExit := ""
	if config.Rules.Mode == RoutingGlobal && config.Focus == "" && !config.includePending {
		if config, err = config.trafficConfig(); err != nil {
			return Compiled{}, err
		}
		runtimeExit = config.Focus
	}
	// A focused traffic session uses its own saved suffix, never the original
	// primary's names. Device-only sessions do not consume routing metadata.
	if config.Focus != "" && config.Focus != MainExit && !config.includePending {
		for _, exit := range config.Exits {
			if exit.ID == config.Focus && exit.magicDNSSuffix() != "" {
				config.DNS.MagicDomains = []string{exit.magicDNSSuffix()}
				break
			}
		}
	}
	global := config.Rules.Mode == RoutingGlobal
	var projection *domainrules.Projection
	var source string
	if !global {
		projection, source, err = loadProjection(ctx, config)
		if err != nil {
			return Compiled{}, err
		}
	}
	// An exit without a selected node is pending: it can log in and list
	// nodes, but it carries no traffic until a node is chosen.
	var exits []ExitConfig
	active := map[string]bool{}
	primaryActive := config.Tailscale != nil && (config.Tailscale.ExitNode != "" || config.includePending)
	primaryID := MainExit
	if config.Focus != "" {
		primaryID = config.Focus
	}
	primaryBlocked := isBlocked(primaryID)
	if !global && !config.includePending && !primaryActive {
		if len(config.DNS.MagicDomains) > 0 {
			if config.Tailscale == nil {
				return Compiled{}, fmt.Errorf("%w: exit %q for dns.magic_domains is not configured", ErrUnresolvedSplitExit, MainExit)
			}
			return Compiled{}, fmt.Errorf("select an exit node for exit %q before using dns.magic_domains", MainExit)
		}
		if config.Tailscale != nil && len(config.Tailscale.Domains) > 0 {
			return Compiled{}, fmt.Errorf("select an exit node for exit %q before using tailscale.domains", MainExit)
		}
	}
	if primaryActive {
		active[MainExit] = true
	}
	if config.Focus == "" {
		for _, exit := range config.Exits {
			if !global && !config.includePending && exit.ExitNode == "" && exit.magicDNSSuffix() != "" {
				return Compiled{}, fmt.Errorf("select an exit node for exit %q before using its MagicDNS suffix", exit.ID)
			}
			if exit.ExitNode != "" || config.includePending {
				exits = append(exits, exit)
				active[exit.ID] = true
			}
		}
	}
	if !global && config.Rules.Final != "direct" && config.Rules.Final != "proxy" && !active[config.Rules.Final] {
		return Compiled{}, fmt.Errorf("select an exit node for exit %q before using it for unmatched traffic", config.Rules.Final)
	}
	defaultID := config.TrafficExitID()
	defaultTag := ExitTag(defaultID)
	if config.Focus != "" {
		defaultID, defaultTag = MainExit, "tailnet"
	}
	final := "direct"
	if global || config.Rules.Final == "proxy" {
		final = defaultTag
	} else if config.Rules.Final != "direct" {
		final = ExitTag(config.Rules.Final)
	}
	// Each exit resolves its own proxied names through itself.
	proxyServer := func(exit string) string {
		if exit == "" || exit == MainExit {
			return "dns-proxy"
		}
		return "dns-proxy-" + exit
	}
	defaultDNS := proxyServer(defaultID)
	directDNS := resolver("dns-direct", config.DNS.Direct)
	servers := []object{directDNS}
	type tailnetSuffix struct {
		suffix, server, outbound, owner string
		blocked                         bool
	}
	var tailnetSuffixes []tailnetSuffix
	// MagicDNS and custom domains share longest-suffix precedence in both DNS
	// and traffic. Primary ownership wins exact ties, with its MagicDNS resolver
	// preferred over custom-domain resolution; then the first extra exit wins.
	// Blocked owners keep that position so another device cannot claim their names.
	ownedSuffixes := make(map[string]bool)
	if primaryActive {
		for _, suffix := range config.DNS.MagicDomains {
			suffix = strings.ToLower(suffix)
			if !ownedSuffixes[suffix] {
				ownedSuffixes[suffix] = true
				tailnetSuffixes = append(tailnetSuffixes, tailnetSuffix{suffix, "dns-tailnet", "tailnet", primaryID, primaryBlocked})
			}
		}
		// Custom names keep real answers, including Tailscale Service VIPs.
		server := "dns-direct"
		if global {
			server = "dns-proxy"
		}
		for _, suffix := range config.Tailscale.Domains {
			if !ownedSuffixes[suffix] {
				ownedSuffixes[suffix] = true
				tailnetSuffixes = append(tailnetSuffixes, tailnetSuffix{suffix, server, "tailnet", primaryID, primaryBlocked})
			}
		}
	}
	if primaryActive && !primaryBlocked {
		proxyDNS := resolver("dns-proxy", config.DNS.Proxy)
		proxyDNS["detour"] = "tailnet"
		servers = append(servers, proxyDNS)
		if len(config.DNS.MagicDomains) > 0 {
			servers = append(servers, object{
				"type": "tailscale", "tag": "dns-tailnet", "endpoint": "tailnet",
				"accept_default_resolvers": false, "accept_search_domain": false,
			})
		}
	}
	if config.Focus == "" {
		for _, exit := range exits {
			blocked := isBlocked(exit.ID)
			if !blocked {
				extra := resolver(proxyServer(exit.ID), config.DNS.Proxy)
				extra["detour"] = ExitTag(exit.ID)
				servers = append(servers, extra)
			}
			suffix := exit.magicDNSSuffix()
			if suffix != "" && !ownedSuffixes[suffix] {
				ownedSuffixes[suffix] = true
				tag := "dns-tailnet-" + exit.ID
				if !blocked {
					servers = append(servers, object{
						"type": "tailscale", "tag": tag, "endpoint": ExitTag(exit.ID),
						"accept_default_resolvers": false, "accept_search_domain": false,
					})
				}
				tailnetSuffixes = append(tailnetSuffixes, tailnetSuffix{suffix, tag, ExitTag(exit.ID), exit.ID, blocked})
			}
		}
	}
	// Capture the same canonicalized, deduplicated ownership used below, before
	// longest-suffix sorting. Keep a compact, stable list in device policy order,
	// including blocked owners rather than transferring their names to a duplicate.
	privateExits := []string{}
	privateOwners := make(map[string]bool)
	for _, suffix := range tailnetSuffixes {
		if !privateOwners[suffix.owner] {
			privateOwners[suffix.owner] = true
			privateExits = append(privateExits, suffix.owner)
		}
	}
	slices.SortStableFunc(tailnetSuffixes, func(a, b tailnetSuffix) int {
		return len(b.suffix) - len(a.suffix)
	})
	var routeGroups []RouteConfig
	if !global && config.Focus == "" {
		for _, route := range config.Rules.Routes {
			if !active[route.Exit] && len(route.Domains)+len(route.IPs) > 0 {
				return Compiled{}, fmt.Errorf("select an exit node for exit %q before using it in routing rules", route.Exit)
			}
			if active[route.Exit] {
				routeGroups = append(routeGroups, route)
			}
		}
	}
	if config.fakeIP() {
		servers = append(servers, object{
			"type": "fakeip", "tag": "dns-fake",
			"inet4_range": "198.18.0.0/15", "inet6_range": "2001:2::/48",
		})
	}

	var directMatchers, proxyMatchers, sets []object
	if !global && len(config.Rules.DirectDomains) > 0 {
		directMatchers = append(directMatchers, object{"domain_suffix": config.Rules.DirectDomains})
	}
	if !global && len(config.Rules.ProxyDomains) > 0 {
		proxyMatchers = append(proxyMatchers, object{"domain_suffix": config.Rules.ProxyDomains})
	}
	if projection != nil {
		if projected := domainRules(projection.Direct); len(projected) > 0 {
			sets = append(sets, object{"type": "inline", "tag": "gfw-direct", "rules": projected})
			directMatchers = append(directMatchers, object{"rule_set": []string{"gfw-direct"}})
		}
		sets = append(sets, object{"type": "inline", "tag": "gfw-proxy", "rules": domainRules(projection.Proxy)})
		proxyMatchers = append(proxyMatchers, object{"rule_set": []string{"gfw-proxy"}})
	}

	var dnsRules []object
	appendDirectDNS := func(match object) {
		if config.DNS.DirectIPv6 != nil && !*config.DNS.DirectIPv6 {
			noAAAA := maps.Clone(match)
			noAAAA["query_type"] = []string{"AAAA"}
			noAAAA["action"], noAAAA["rcode"] = "predefined", "NOERROR"
			dnsRules = append(dnsRules, noAAAA)
		}
		dnsRules = append(dnsRules, withAction(match, "server", "dns-direct"))
	}
	for _, suffix := range tailnetSuffixes {
		if suffix.blocked {
			dnsRules = append(dnsRules, object{"domain_suffix": []string{suffix.suffix}, "action": "predefined", "rcode": "REFUSED"})
		} else {
			dnsRules = append(dnsRules, withAction(object{"domain_suffix": []string{suffix.suffix}}, "server", suffix.server))
		}
	}
	for _, match := range directMatchers {
		appendDirectDNS(match)
	}
	appendProxyDNS := func(match object, server string) {
		if config.fakeIP() {
			fake := withAction(match, "server", "dns-fake")
			fake["query_type"] = []string{"A", "AAAA"}
			dnsRules = append(dnsRules, fake)
		}
		dnsRules = append(dnsRules, withAction(match, "server", server))
	}
	for _, route := range routeGroups {
		if len(route.Domains) > 0 {
			appendProxyDNS(object{"domain_suffix": route.Domains}, proxyServer(route.Exit))
		}
	}
	for _, match := range proxyMatchers {
		appendProxyDNS(match, defaultDNS)
	}
	dnsFinal := "dns-direct"
	if global || config.Rules.Final == "proxy" {
		dnsFinal = defaultDNS
	} else if final != "direct" {
		dnsFinal = proxyServer(config.Rules.Final)
	} else if config.DNS.DirectIPv6 != nil && !*config.DNS.DirectIPv6 {
		// Domain-specific Tailnet/proxy rules have already matched above.
		// With no direct IPv6 uplink, an AAAA answer would otherwise make
		// the user-space TUN accept TCP and then reset after egress fails.
		dnsRules = append(dnsRules, object{
			"query_type": []string{"AAAA"}, "action": "predefined", "rcode": "NOERROR",
		})
	}
	// Without TUN interception, a bare fake IP can escape through the OS's
	// ordinary route. Fake answers are therefore available only on the
	// TUN DNS peer. Proxy-only mode does not publish this resolver to the OS.
	// Internal outbound hostname lookups use sing-box's real-resolution path.
	dns := object{
		"servers": servers, "rules": dnsRules, "final": dnsFinal,
		"reverse_mapping": true,
	}

	if global && config.fakeIP() {
		// Only client DNS gets synthetic addresses. The endpoint's control
		// lookups and internal connection resolution still need real answers.
		dnsRules = append(dnsRules, object{
			"inbound":    []string{"tun-in"},
			"query_type": []string{"A", "AAAA"}, "action": "route", "server": "dns-fake",
		})
		dns["rules"] = dnsRules
	}
	routes := []object{{"inbound": []string{"tun-in"}, "port": 53, "action": "hijack-dns"}}
	if config.TUN.Enabled {
		// Existing sockets and application DNS caches can supply a real IP
		// without going through this run's fake-IP resolver. Recover HTTP/TLS/
		// QUIC hostnames before evaluating domain rules for those connections.
		routes = append(routes, object{
			"inbound": []string{"tun-in"}, "action": "sniff",
			"sniffer": []string{"http", "tls", "quic"}, "timeout": "300ms",
		})
	} else {
		// This interface serves DNS in proxy-only mode. It must not become
		// an accidental alternate route for non-DNS application traffic.
		routes = append(routes, object{"inbound": []string{"tun-in"}, "action": "reject"})
	}
	if !global && config.DNS.DirectIPv6 != nil && !*config.DNS.DirectIPv6 {
		// Explicit application proxies can also receive cached IPv6 literals.
		// Recover their TLS/QUIC hostname before selecting the outbound.
		routes = append(routes, object{
			"inbound": []string{"proxy-in"}, "action": "sniff",
			"sniffer": []string{"http", "tls", "quic"}, "timeout": "300ms",
		})
	}
	// Peer and Service VIP routing must precede generic private-IP bypass.
	// Resolve magic/custom Tailnet hostnames to their real addresses before
	// they enter the endpoint; a direct DNS resolver must never see MagicDNS.
	{
		for _, suffix := range tailnetSuffixes {
			match := object{"domain_suffix": []string{suffix.suffix}}
			if suffix.blocked {
				match["action"] = "reject"
				routes = append(routes, match)
			} else {
				routes = append(routes,
					object{"domain_suffix": match["domain_suffix"], "action": "resolve", "server": suffix.server},
					withAction(match, "outbound", suffix.outbound),
				)
			}
		}
	}
	for _, match := range directMatchers {
		routes = append(routes, withAction(match, "outbound", "direct"))
	}
	if !global && len(config.Rules.DirectIPs) > 0 {
		routes = append(routes, withAction(object{"ip_cidr": config.Rules.DirectIPs}, "outbound", "direct"))
	}
	for _, route := range routeGroups {
		if len(route.Domains) > 0 {
			routes = append(routes, withAction(object{"domain_suffix": route.Domains}, "outbound", ExitTag(route.Exit)))
		}
		if len(route.IPs) > 0 {
			routes = append(routes, withAction(object{"ip_cidr": route.IPs}, "outbound", ExitTag(route.Exit)))
		}
	}
	// Only unmatched Tailnet addresses fall back to the default device. A
	// more specific per-exit rule must be allowed to choose another Tailnet.
	if active[defaultID] {
		routes = append(routes, withAction(object{
			"ip_cidr": []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"},
		}, "outbound", defaultTag))
	}
	// Explicit IP ranges are meaningful even for destinations that have no
	// DNS lookup. They are independent of the GFWList domain projection.
	if !global && len(config.Rules.ProxyIPs) > 0 {
		routes = append(routes, withAction(object{"ip_cidr": config.Rules.ProxyIPs}, "outbound", defaultTag))
	}
	for _, match := range proxyMatchers {
		routes = append(routes, withAction(match, "outbound", defaultTag))
	}
	if !global {
		routes = append(routes, withAction(object{"ip_is_private": true}, "outbound", "direct"))
	}
	native := object{
		"log": object{"level": config.LogLevel, "timestamp": true},
		"dns": dns,
		"inbounds": []object{
			listener("mixed", "proxy-in", config.Listen),
		},
		"outbounds": []object{{
			"type": "direct", "tag": "direct", "domain_resolver": "dns-direct",
		}},
		"route": object{
			"rules": routes, "rule_set": sets, "final": final,
			"auto_detect_interface": true, "default_domain_resolver": "dns-direct",
		},
		"experimental": object{"cache_file": object{
			"enabled": true, "path": filepath.Join(config.StateDir, "cache.db"),
			"store_fakeip": true,
		}},
	}
	if !global && config.DNS.DirectIPv6 != nil && !*config.DNS.DirectIPv6 {
		// Recover cached/app-resolved IPv6 only after the original destination
		// has selected direct routing, preserving domain and CIDR precedence.
		direct := native["outbounds"].([]object)[0]
		direct["type"] = directIPv4Type
		direct["domain_resolver"] = object{"server": "dns-direct", "strategy": "ipv4_only"}
	}
	if global {
		// There is no client-selectable direct outbound or DNS fallback in
		// global mode. dns-direct exists solely for Tailnet bootstrap.
		// tailnet is an endpoint below, not a member of this outbound list;
		// sing-box's outbound lookup also resolves endpoint tags.
		native["outbounds"] = []object{}
		native["route"].(object)["default_domain_resolver"] = "dns-proxy"
	}
	if config.Interface != "" {
		native["route"].(object)["auto_detect_interface"] = false
		native["route"].(object)["default_interface"] = config.Interface
		if !global {
			native["outbounds"].([]object)[0]["bind_interface"] = config.Interface
		}
		directDNS["bind_interface"] = config.Interface
	}
	{
		device := func(id, hostname, exitNode string) object {
			taildrop := filepath.Join(config.StateDir, "taildrop")
			if id != MainExit {
				taildrop = filepath.Join(config.StateDir, "exits", id, "taildrop")
			}
			endpoint := object{
				"type": "tailscale", "tag": ExitTag(id),
				"hostname":           hostname,
				"state_directory":    config.ExitStateDir(id),
				"taildrop_directory": taildrop,
				"exit_node":          exitNode,
				"system_interface":   false, "accept_routes": false,
				"advertise_exit_node": false, "domain_resolver": "dns-direct",
			}
			if config.Interface != "" {
				endpoint["bind_interface"] = config.Interface
			}
			return endpoint
		}
		// A focused session keeps the primary tag so single-device flows apply.
		focus := MainExit
		if config.Focus != "" {
			focus = config.Focus
		}
		var endpoints []object
		if primaryActive && !primaryBlocked {
			primary := device(focus, config.Tailscale.Hostname, config.Tailscale.ExitNode)
			primary["tag"] = "tailnet"
			endpoints = append(endpoints, primary)
		}
		if config.Focus == "" {
			for _, exit := range exits {
				if !isBlocked(exit.ID) {
					endpoints = append(endpoints, device(exit.ID, exit.Hostname, exit.ExitNode))
				}
			}
		}
		if len(endpoints) > 0 {
			native["endpoints"] = endpoints
		}
	}
	tun := object{
		"type": "tun", "tag": "tun-in", "address": tunAddresses(),
		"mtu": config.TUN.MTU, "stack": "mixed",
		"auto_route": true, "strict_route": config.TUN.Enabled, "dns_mode": "hijack",
	}
	if runtime.GOOS == "windows" {
		// Handle synthetic DNS-peer TCP in userspace instead of reinjecting
		// the connection through a native Windows listener.
		tun["stack"] = "gvisor"
	}
	if !config.TUN.Enabled {
		// Keep only the DNS host routes. Do not install Internet routes or
		// publish this resolver through the platform's native DNS settings.
		tun["route_address"] = []string{
			tunIPv4.Addr().Next().String() + "/32",
			tunIPv6.Addr().Next().String() + "/128",
		}
		tun["dns_mode"] = "disabled"
	}
	if config.TUN.Name != "" {
		tun["interface_name"] = config.TUN.Name
	}
	native["inbounds"] = append(native["inbounds"].([]object), tun)
	data, err := json.MarshalIndent(native, "", "  ")
	if err != nil {
		return Compiled{}, err
	}
	result := Compiled{JSON: append(data, '\n'), GFWSource: source, runtimeExit: runtimeExit, privateExits: privateExits}
	if projection != nil {
		result.GFWList = &projection.Info
	}
	return result, nil
}

// ErrUnresolvedSplitExit identifies a missing device referenced by live Split
// final, route-group or private-name policy. Such policy can be saved as a draft,
// but cannot connect. Missing defaults and pending nodes do not use it.
var ErrUnresolvedSplitExit = errors.New("unresolved Split traffic policy")

// ValidateTrafficExit requires a selected traffic device and configured owners
// for live Split policy before a connection starts. It does not open saved state
// or check network availability. Dormant drafts remain usable for device sessions.
func ValidateTrafficExit(config Config) error {
	if config.Focus == "" && config.Rules.Mode != RoutingGlobal && !config.includePending {
		if config.Tailscale == nil && len(config.DNS.MagicDomains) > 0 {
			return fmt.Errorf("%w: exit %q for dns.magic_domains is not configured", ErrUnresolvedSplitExit, MainExit)
		}
		if config.Rules.Final != "" && config.Rules.Final != "direct" && config.Rules.Final != "proxy" && !config.hasExit(config.Rules.Final) {
			return fmt.Errorf("%w: exit %q for unmatched traffic is not configured", ErrUnresolvedSplitExit, config.Rules.Final)
		}
		for _, route := range config.Rules.Routes {
			if len(route.Domains)+len(route.IPs) > 0 && !config.hasExit(route.Exit) {
				return fmt.Errorf("%w: exit %q for routing rules is not configured", ErrUnresolvedSplitExit, route.Exit)
			}
		}
	}
	if !requiresTrafficExit(config, config.TrafficExitID()) && config.Focus == "" {
		return nil
	}
	if len(config.ExitIDs()) == 0 && config.Focus == "" {
		return errors.New("configure an exit device before starting proxy routing")
	}
	id := config.TrafficExitID()
	if id == UnsetExit {
		return errors.New("select a default exit before connecting")
	}
	if !config.hasExit(id) {
		return fmt.Errorf("traffic exit %q is not configured", id)
	}
	node := ""
	if id == MainExit {
		node = config.Tailscale.ExitNode
	} else {
		for _, exit := range config.Exits {
			if exit.ID == id {
				node = exit.ExitNode
			}
		}
	}
	if strings.TrimSpace(node) == "" {
		return fmt.Errorf("select an exit node for exit %q before starting the proxy", id)
	}
	return nil
}

func resolver(tag, value string) object {
	if value == "system" {
		return object{"type": "local", "tag": tag, "prefer_go": runtime.GOOS == "darwin"}
	}
	address, _ := parseResolver(value) // Config has already validated this.
	return object{"type": "udp", "tag": tag, "server": address.Addr().String(), "server_port": address.Port()}
}

func listener(kind, tag, value string) object {
	address := netip.MustParseAddrPort(value)
	return object{"type": kind, "tag": tag, "listen": address.Addr().String(), "listen_port": address.Port()}
}

func withAction(match object, key, value string) object {
	result := maps.Clone(match)
	result["action"] = "route"
	result[key] = value
	return result
}

func domainRules(projection domainrules.DomainProjection) []object {
	var result []object
	for _, item := range []struct {
		key    string
		values []string
	}{
		{"domain", projection.Exact},
		{"domain_suffix", projection.Suffix},
		{"domain_regex", projection.Regex},
	} {
		if len(item.values) > 0 {
			result = append(result, object{item.key: item.values})
		}
	}
	return result
}

func loadProjection(ctx context.Context, config Config) (*domainrules.Projection, string, error) {
	source := config.Rules.GFWList
	if source == "" {
		return nil, "", nil
	}
	if source == "builtin" || strings.HasPrefix(source, "https://") {
		if err := EnsureRules(ctx, config, nil); err != nil {
			return nil, "", err
		}
		source = RulesCachePath(config)
	}
	projection, err := readProjection(source)
	return projection, source, err
}

func readProjection(source string) (*domainrules.Projection, error) {
	stream, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	stat, err := stream.Stat()
	if err != nil {
		return nil, err
	}
	if stat.Size() > 2<<20 {
		return nil, fmt.Errorf("rule list exceeds 2 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(stream, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	projection, err := domainrules.ProjectActiveRules(data)
	return &projection, err
}
