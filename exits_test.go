package tailsplit

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const multiExitYAML = `
tailscale:
  hostname: laptop
  exit_node: 100.64.0.10
exits:
  - id: office
    name: Office
    exit_node: 100.101.0.11
    tailnet: office-net.ts.net
  - id: spare
    exit_node: ""
rules:
  proxy_domains: [example.com]
  final: office
  routes:
    - exit: office
      domains: [corp.example]
      ips: [203.0.113.0/24]
`

func compiledNative(t *testing.T, config Config) map[string]any {
	t.Helper()
	compiled, err := compile(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if TailscaleEnabled {
		if err := Check(compiled); err != nil {
			t.Fatal(err)
		}
	}
	var native map[string]any
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		t.Fatal(err)
	}
	return native
}

func endpointTags(native map[string]any) map[string]map[string]any {
	tags := map[string]map[string]any{}
	endpoints, _ := native["endpoints"].([]any)
	for _, raw := range endpoints {
		endpoint := raw.(map[string]any)
		tags[endpoint["tag"].(string)] = endpoint
	}
	return tags
}

func TestUnsetDefaultRequiresChoiceButAllowsDeviceDiscovery(t *testing.T) {
	config, err := loadTestConfig(t, multiExitYAML)
	if err != nil {
		t.Fatal(err)
	}
	config.Tailscale = nil
	config.Rules.DefaultExit, config.Rules.GlobalExit = UnsetExit, UnsetExit
	for _, mode := range []string{RoutingSplit, RoutingGlobal} {
		config.Rules.Mode = mode
		normalized, err := Normalize(config, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateTrafficExit(normalized); err == nil {
			t.Fatalf("%s connected without an explicit default", mode)
		}
		compiled, err := compilePreflight(normalized, true)
		if err != nil {
			t.Fatal(err)
		}
		if TailscaleEnabled {
			if err := Check(compiled); err != nil {
				t.Fatal("device discovery needs to work before choosing a default:", err)
			}
		}
		if normalized.Rules.DefaultExit != UnsetExit || normalized.Rules.GlobalExit != UnsetExit {
			t.Fatal("discovery silently assigned a traffic default")
		}
	}
}

func TestExitsCompileToSeparateDevicesAndRoutes(t *testing.T) {
	config, err := loadTestConfig(t, multiExitYAML)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Exits[0].Hostname; got != "laptop-office" {
		t.Fatalf("default exit hostname: %q", got)
	}
	native := compiledNative(t, config)
	tags := endpointTags(native)
	if len(tags) != 2 || tags["tailnet"] == nil || tags["exit-office"] == nil {
		t.Fatalf("pending exits must not run and others must: %v", tags)
	}
	if tags["tailnet"]["state_directory"] != filepath.Join(config.StateDir, "tailscale") ||
		tags["exit-office"]["state_directory"] != filepath.Join(config.StateDir, "exits", "office", "tailscale") {
		t.Fatal("each device needs its own state directory, keeping the primary's original path")
	}
	if tags["exit-office"]["exit_node"] != "100.101.0.11" {
		t.Fatal("exit node not applied to its own device")
	}
	route := native["route"].(map[string]any)
	if route["final"] != "exit-office" {
		t.Fatalf("final exit: %v", route["final"])
	}
	var outbounds []string
	for _, raw := range route["rules"].([]any) {
		rule := raw.(map[string]any)
		if outbound, ok := rule["outbound"].(string); ok {
			outbounds = append(outbounds, outbound)
		}
		if suffix, ok := rule["domain_suffix"].([]any); ok && slices.Contains(suffix, any("pending.example")) {
			t.Fatal("a route to a pending exit must not be compiled")
		}
	}
	// Tailnet suffix, Tailnet ranges, the route group and primary proxy rules.
	officeIndex := slices.Index(outbounds, "exit-office")
	if officeIndex < 0 || !slices.Contains(outbounds, "tailnet") {
		t.Fatalf("routes lost: %v", outbounds)
	}
	dns := native["dns"].(map[string]any)
	if dns["final"] != "dns-proxy-office" {
		t.Fatalf("unmatched names must resolve through their exit: %v", dns["final"])
	}
	found := false
	for _, raw := range dns["servers"].([]any) {
		server := raw.(map[string]any)
		if server["tag"] == "dns-proxy-office" && server["detour"] == "exit-office" {
			found = true
		}
	}
	if !found {
		t.Fatal("per-exit proxy DNS missing")
	}
}

func TestPendingExitsJoinDeviceSessions(t *testing.T) {
	config, err := loadTestConfig(t, multiExitYAML)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compilePreflight(config, true)
	if err != nil {
		t.Fatal(err)
	}
	var native map[string]any
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		t.Fatal(err)
	}
	if tags := endpointTags(native); tags["exit-spare"] == nil {
		t.Fatal("discovery must include exits that still need a node")
	}
}

func TestFocusSelectsOneDeviceIdempotently(t *testing.T) {
	config, err := loadTestConfig(t, multiExitYAML)
	if err != nil {
		t.Fatal(err)
	}
	focused, err := config.FocusExit("office")
	if err != nil {
		t.Fatal(err)
	}
	// The service receives the focused config as JSON and normalizes again.
	data, _ := json.Marshal(focused)
	var received Config
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatal(err)
	}
	if received, err = Normalize(received, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []Config{focused, received} {
		if candidate.Tailscale.ExitNode != "100.101.0.11" || candidate.Tailscale.Tailnet != "office-net.ts.net" {
			t.Fatalf("focus did not select the office device: %+v", candidate.Tailscale)
		}
		native := compiledNative(t, candidate)
		tags := endpointTags(native)
		if len(tags) != 1 || tags["tailnet"]["state_directory"] != candidate.ExitStateDir("office") ||
			tags["tailnet"]["hostname"] != "laptop-office" {
			t.Fatalf("focused session must run only the office device: %v", tags)
		}
	}
	main, err := config.FocusExit(MainExit)
	if err != nil || len(main.Exits) != len(config.Exits) || main.Tailscale.ExitNode != "100.64.0.10" {
		t.Fatalf("main focus: %+v %v", main.Tailscale, err)
	}
	if _, err := config.FocusExit("../escape"); err == nil {
		t.Fatal("unknown exit accepted")
	}
}

func TestExitConfigRejectsUnsafeInput(t *testing.T) {
	for name, text := range map[string]string{
		"path id":            "tailscale: {}\nexits:\n  - id: ../x\n    exit_node: a\n",
		"reserved id":        "tailscale: {}\nexits:\n  - id: main\n    exit_node: a\n",
		"duplicate id":       "tailscale: {}\nexits:\n  - id: a\n  - id: a\n",
		"duplicate hostname": "tailscale:\n  hostname: laptop\nexits:\n  - id: a\n    hostname: laptop\n",
		"unknown route exit": "tailscale: {}\nrules:\n  routes:\n    - exit: nowhere\n      domains: [a.example]\n",
		"unknown final exit": "tailscale: {}\nrules:\n  final: nowhere\n",
		"main as final":      "tailscale: {}\nrules:\n  final: main\n",
		"bad route CIDR":     "tailscale: {}\nexits:\n  - id: a\nrules:\n  routes:\n    - exit: a\n      ips: [10.0.0.1/8]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadTestConfig(t, text); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}

func TestFinalToPendingExitFailsBeforeConnecting(t *testing.T) {
	config, err := loadTestConfig(t, "tailscale:\n  exit_node: 100.64.0.10\nexits:\n  - id: spare\nrules:\n  final: spare\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompileContext(t.Context(), config); err == nil {
		t.Fatal("unmatched traffic sent to an exit that has no node")
	}
}

func TestExplicitExitRangesPrecedePrimaryTailnetFallback(t *testing.T) {
	config, err := loadTestConfig(t, multiExitYAML)
	if err != nil {
		t.Fatal(err)
	}
	config.Rules.DirectIPs = []string{"100.101.0.99/32"}
	config.Rules.Routes = []RouteConfig{{Exit: "office", IPs: []string{"100.101.0.0/24", "fd7a:115c:a1e0:1234::/64"}}}
	native := compiledNative(t, config)
	rules := native["route"].(map[string]any)["rules"].([]any)
	for ip, want := range map[string]string{
		"100.101.0.12":            "exit-office",
		"fd7a:115c:a1e0:1234::12": "exit-office",
		"100.101.0.99":            "direct",
		"100.64.12.34":            "tailnet",
		"fd7a:115c:a1e0:5678::12": "tailnet",
	} {
		address := netip.MustParseAddr(ip)
		got := ""
		for _, raw := range rules {
			rule := raw.(map[string]any)
			prefixes, _ := rule["ip_cidr"].([]any)
			for _, rawPrefix := range prefixes {
				if netip.MustParsePrefix(rawPrefix.(string)).Contains(address) {
					got, _ = rule["outbound"].(string)
					break
				}
			}
			if got != "" {
				break
			}
		}
		if got != want {
			t.Errorf("%s routes through %q; want %q", ip, got, want)
		}
	}
}

func TestSharedMagicDNSSuffixUsesTheSameDeviceForResolutionAndTraffic(t *testing.T) {
	for _, primary := range []bool{true, false} {
		t.Run(map[bool]string{true: "primary owns suffix", false: "first extra owns suffix"}[primary], func(t *testing.T) {
			config, err := loadTestConfig(t, multiExitYAML)
			if err != nil {
				t.Fatal(err)
			}
			suffix := "shared.ts.net"
			config.Exits[0].Tailnet = suffix
			config.Exits[1].Tailnet = suffix
			config.Exits[1].ExitNode = "100.101.0.12"
			wantServer, wantOutbound := "dns-tailnet-office", "exit-office"
			if primary {
				config.DNS.MagicDomains = []string{suffix}
				config.Tailscale.Tailnet = suffix
				wantServer, wantOutbound = "dns-tailnet", "tailnet"
			}
			native := compiledNative(t, config)
			firstMatch := func(rules []any, key string) string {
				for _, raw := range rules {
					rule := raw.(map[string]any)
					domains, _ := rule["domain_suffix"].([]any)
					for _, domain := range domains {
						if strings.HasSuffix("host."+suffix, "."+domain.(string)) {
							if value, ok := rule[key].(string); ok {
								return value
							}
						}
					}
				}
				return ""
			}
			dnsRules := native["dns"].(map[string]any)["rules"].([]any)
			trafficRules := native["route"].(map[string]any)["rules"].([]any)
			if got := firstMatch(dnsRules, "server"); got != wantServer {
				t.Errorf("DNS server %q; want %q", got, wantServer)
			}
			if got := firstMatch(trafficRules, "server"); got != wantServer {
				t.Errorf("route resolver %q; want %q", got, wantServer)
			}
			if got := firstMatch(trafficRules, "outbound"); got != wantOutbound {
				t.Errorf("outbound %q; want %q", got, wantOutbound)
			}
		})
	}
}

func TestRouteToPendingExitFailsInsteadOfUsingDefaultRoute(t *testing.T) {
	config, err := loadTestConfig(t, multiExitYAML)
	if err != nil {
		t.Fatal(err)
	}
	config.Rules.Final = "direct"
	for _, route := range []RouteConfig{
		{Exit: "spare", Domains: []string{"pending.example"}},
		{Exit: "spare", IPs: []string{"203.0.113.0/24"}},
	} {
		config.Rules.Routes = []RouteConfig{route}
		if _, err := CompileContext(t.Context(), config); err == nil {
			t.Fatalf("pending exit rule silently fell through to direct: %+v", route)
		}
	}
}

func TestSplitDefaultExitOwnsImplicitProxyAndDNS(t *testing.T) {
	config := focusedExitTestConfig(t)
	config.Rules.DefaultExit = "office"
	config.Rules.Final = "proxy"
	config.Rules.ProxyDomains = []string{"proxy.example"}
	config.Rules.ProxyIPs = []string{"203.0.113.0/24"}
	config.Rules.GFWList = filepath.Join(t.TempDir(), "rules.txt")
	if err := os.WriteFile(config.Rules.GFWList, []byte("[AutoProxy 0.2.9]\n||listed.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config.Rules.Routes = []RouteConfig{{Exit: MainExit, Domains: []string{"explicit.example"}}}
	compiled, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	var native object
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		t.Fatal(err)
	}
	route := native["route"].(map[string]any)
	if route["final"] != "exit-office" {
		t.Fatalf("final=proxy did not use Split default: %v", route)
	}
	found := map[string]bool{}
	for _, raw := range route["rules"].([]any) {
		rule := raw.(map[string]any)
		for key, marker := range map[string]string{"domain_suffix": "proxy.example", "ip_cidr": "203.0.113.0/24", "rule_set": "gfw-proxy"} {
			values, _ := rule[key].([]any)
			for _, value := range values {
				if value == marker {
					found[marker] = rule["outbound"] == "exit-office"
				}
			}
		}
		if values, _ := rule["ip_cidr"].([]any); len(values) > 0 && values[0] == "100.64.0.0/10" {
			found["tailnet fallback"] = rule["outbound"] == "exit-office"
		}
		if values, _ := rule["domain_suffix"].([]any); len(values) > 0 && values[0] == "explicit.example" {
			found["explicit primary"] = rule["outbound"] == "tailnet"
		}
	}
	for _, marker := range []string{"proxy.example", "203.0.113.0/24", "gfw-proxy", "tailnet fallback", "explicit primary"} {
		if !found[marker] {
			t.Errorf("wrong route for %s", marker)
		}
	}
	dns := native["dns"].(map[string]any)
	if dns["final"] != "dns-proxy-office" {
		t.Fatal("default proxy DNS uses another saved device")
	}
	for _, raw := range dns["rules"].([]any) {
		rule := raw.(map[string]any)
		if rule["server"] == "dns-fake" {
			continue
		}
		for _, key := range []string{"domain_suffix", "rule_set"} {
			values, _ := rule[key].([]any)
			if len(values) > 0 && (values[0] == "proxy.example" || values[0] == "gfw-proxy") && rule["server"] != "dns-proxy-office" {
				t.Fatalf("implicit DNS uses another exit: %v", rule)
			}
		}
	}
	if TailscaleEnabled {
		if err := Check(compiled); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExtrasOnlyCompileDiscoveryAndStatusKeepStableIdentity(t *testing.T) {
	for _, mode := range []string{RoutingSplit, RoutingGlobal} {
		t.Run(mode, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.Tailscale = nil
			config.DNS.MagicDomains = nil
			config.Rules.Mode, config.Rules.DefaultExit, config.Rules.GlobalExit = mode, "office", "office"
			config.Rules.Final = "proxy"
			compiled, err := Compile(config)
			if err != nil {
				t.Fatal(err)
			}
			var native object
			_ = json.Unmarshal(compiled.JSON, &native)
			endpoints := native["endpoints"].([]any)
			if len(endpoints) != 1 || endpoints[0].(map[string]any)["state_directory"] != config.ExitStateDir("office") {
				t.Fatalf("extras-only runtime promoted or renamed a saved device: %v", endpoints)
			}
			if TailscaleEnabled {
				engine, err := Open(context.Background(), config, compiled)
				if err != nil {
					t.Fatal(err)
				}
				if len(engine.exits) != 1 || engine.exits[0].id != "office" || engine.exits[0].tailnet != "office.ts.net" {
					t.Fatalf("extras-only status attributed identity to primary: %+v", engine.exits)
				}
				if err := engine.Close(); err != nil {
					t.Fatal(err)
				}
			}
			discovery, err := compilePreflight(config, true)
			if err != nil {
				t.Fatal(err)
			}
			native = nil
			_ = json.Unmarshal(discovery.JSON, &native)
			endpoints = native["endpoints"].([]any)
			if len(endpoints) != 2 || native["inbounds"] != nil {
				t.Fatal("extras-only discovery omitted pending saved devices or opened listeners")
			}
			for _, raw := range endpoints {
				if raw.(map[string]any)["tag"] == "tailnet" {
					t.Fatal("extras-only discovery invented a primary device")
				}
			}
			ping, err := savedExitPingConfig(config, "office")
			if err != nil {
				t.Fatal(err)
			}
			endpoint := focusedPreflightEndpoint(t, ping)
			if endpoint["state_directory"] != config.ExitStateDir("office") || endpoint["exit_node"] != "" {
				t.Fatal("extras-only focused ping changed state identity or restored node")
			}
		})
	}
}

func TestEmptyDeviceDraftsRetainDormantPolicyButCannotProxy(t *testing.T) {
	for _, policy := range []string{
		"proxy_domains: [proxy.example]", "proxy_ips: [203.0.113.0/24]", "source: builtin", "final: proxy", "mode: global",
	} {
		config, err := loadTestConfig(t, "state_dir: ./state\nrules:\n  "+policy+"\n")
		if err != nil {
			t.Fatalf("dormant draft was rejected: %v", err)
		}
		config.TUN.Enabled = true
		if _, err := Compile(config); err == nil {
			t.Fatalf("dormant proxy configuration silently fell back direct: %s", policy)
		}
	}
	config, err := loadTestConfig(t, "state_dir: ./state\nrules:\n  direct_domains: [local.example]\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(config); err != nil {
		t.Fatalf("direct-only CLI configuration no longer works: %v", err)
	}
	pending, err := loadTestConfig(t, "state_dir: ./state\nexits:\n  - id: office\nrules:\n  default_exit: office\n  global_exit: office\n")
	if err != nil {
		t.Fatalf("first extra without primary cannot save before login: %v", err)
	}
	if _, err := compilePreflight(pending, true); err != nil {
		t.Fatalf("first extra cannot log in or discover before choosing a node: %v", err)
	}
	if _, err := Compile(pending); err == nil {
		t.Fatal("pending first extra can start traffic before choosing a node")
	}
}
