package tailsplit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
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
}

// Compile loads local or cached rules, downloading a remote source when its
// cache is missing. Use CompileContext to cancel that first download.
func Compile(config Config) (Compiled, error) {
	return CompileContext(context.Background(), config)
}

func CompileContext(ctx context.Context, config Config) (Compiled, error) {
	if config.Rules.Mode == RoutingGlobal && !config.TUN.Enabled {
		return Compiled{}, fmt.Errorf("global Kill Switch mode requires tun.enabled: true")
	}
	if config.Tailscale != nil && strings.TrimSpace(config.Tailscale.ExitNode) == "" {
		return Compiled{}, fmt.Errorf("select a tailscale.exit_node before starting the proxy")
	}
	return compile(ctx, config)
}

func compile(ctx context.Context, config Config) (Compiled, error) {
	base, err := os.Getwd()
	if err != nil {
		return Compiled{}, err
	}
	if err := config.normalize(base); err != nil {
		return Compiled{}, err
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
	final := "direct"
	if global || config.Rules.Final == "proxy" {
		final = "tailnet"
	}
	directDNS := resolver("dns-direct", config.DNS.Direct)
	servers := []object{directDNS}
	if config.Tailscale != nil {
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
	if len(config.DNS.MagicDomains) > 0 {
		dnsRules = append(dnsRules, withAction(object{"domain_suffix": config.DNS.MagicDomains}, "server", "dns-tailnet"))
	}
	if config.Tailscale != nil && len(config.Tailscale.Domains) > 0 {
		// Public custom names keep real answers, including Tailscale Service VIPs.
		server := "dns-direct"
		if global {
			server = "dns-proxy"
		}
		dnsRules = append(dnsRules, withAction(object{"domain_suffix": config.Tailscale.Domains}, "server", server))
	}
	for _, match := range directMatchers {
		appendDirectDNS(match)
	}
	for _, match := range proxyMatchers {
		if config.fakeIP() {
			fake := withAction(match, "server", "dns-fake")
			fake["query_type"] = []string{"A", "AAAA"}
			dnsRules = append(dnsRules, fake)
		}
		dnsRules = append(dnsRules, withAction(match, "server", "dns-proxy"))
	}
	dnsFinal := "dns-direct"
	if final == "tailnet" {
		dnsFinal = "dns-proxy"
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
	if config.Tailscale != nil {
		if len(config.DNS.MagicDomains) > 0 {
			routes = append(routes,
				object{"domain_suffix": config.DNS.MagicDomains, "action": "resolve", "server": "dns-tailnet"},
				withAction(object{"domain_suffix": config.DNS.MagicDomains}, "outbound", "tailnet"),
			)
		}
		if len(config.Tailscale.Domains) > 0 {
			server := "dns-direct"
			if global {
				server = "dns-proxy"
			}
			routes = append(routes,
				object{"domain_suffix": config.Tailscale.Domains, "action": "resolve", "server": server},
				withAction(object{"domain_suffix": config.Tailscale.Domains}, "outbound", "tailnet"),
			)
		}
		routes = append(routes, withAction(object{
			"ip_cidr": []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"},
		}, "outbound", "tailnet"))
	}
	for _, match := range directMatchers {
		routes = append(routes, withAction(match, "outbound", "direct"))
	}
	if !global && len(config.Rules.DirectIPs) > 0 {
		routes = append(routes, withAction(object{"ip_cidr": config.Rules.DirectIPs}, "outbound", "direct"))
	}
	// Explicit IP ranges are meaningful even for destinations that have no
	// DNS lookup. They are independent of the GFWList domain projection.
	if !global && len(config.Rules.ProxyIPs) > 0 {
		routes = append(routes, withAction(object{"ip_cidr": config.Rules.ProxyIPs}, "outbound", "tailnet"))
	}
	for _, match := range proxyMatchers {
		routes = append(routes, withAction(match, "outbound", "tailnet"))
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
	if config.Tailscale != nil {
		endpoint := object{
			"type": "tailscale", "tag": "tailnet",
			"hostname":           config.Tailscale.Hostname,
			"state_directory":    filepath.Join(config.StateDir, "tailscale"),
			"taildrop_directory": filepath.Join(config.StateDir, "taildrop"),
			"exit_node":          config.Tailscale.ExitNode,
			"system_interface":   false, "accept_routes": false,
			"advertise_exit_node": false, "domain_resolver": "dns-direct",
		}
		if config.Interface != "" {
			endpoint["bind_interface"] = config.Interface
		}
		native["endpoints"] = []object{endpoint}
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
	result := Compiled{JSON: append(data, '\n'), GFWSource: source}
	if projection != nil {
		result.GFWList = &projection.Info
	}
	return result, nil
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
		source = filepath.Join(config.StateDir, "gfwlist.txt")
	}
	stream, err := os.Open(source)
	if err != nil {
		return nil, "", err
	}
	defer stream.Close()
	stat, err := stream.Stat()
	if err != nil {
		return nil, "", err
	}
	if stat.Size() > 2<<20 {
		return nil, "", fmt.Errorf("GFWList exceeds 2 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(stream, (2<<20)+1))
	if err != nil {
		return nil, "", err
	}
	projection, err := domainrules.ProjectActiveRules(data)
	return &projection, source, err
}
