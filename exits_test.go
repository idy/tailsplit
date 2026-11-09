package tailsplit

import (
	"context"
	"encoding/json"
	"errors"
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

func TestAdditionalDefaultHostnameSurvivesPrimaryChanges(t *testing.T) {
	base := t.TempDir()
	var hostname, state string
	for _, primary := range []string{"original", "renamed", ""} {
		config := Config{
			StateDir: base,
			Exits: []ExitConfig{
				{ID: "office", ExitNode: "100.101.0.11"},
				{ID: "explicit", Hostname: "renamed-office"},
			},
			Rules: RulesConfig{DefaultExit: UnsetExit},
		}
		if primary != "" {
			config.Tailscale = &TailscaleConfig{Hostname: primary}
		}
		normalized, err := Normalize(config, base)
		if err != nil {
			t.Fatalf("primary %q introduced a derived hostname collision: %v", primary, err)
		}
		if hostname == "" {
			hostname, state = normalized.Exits[0].Hostname, normalized.ExitStateDir("office")
		}
		if normalized.Exits[0].Hostname != hostname || normalized.Exits[0].ID != "office" || normalized.ExitStateDir("office") != state || normalized.Exits[1].Hostname != "renamed-office" {
			t.Fatalf("primary %q changed an additional identity: %+v", primary, normalized.Exits)
		}
		native := compiledNative(t, normalized)
		if endpoint := endpointTags(native)["exit-office"]; endpoint["hostname"] != hostname || endpoint["state_directory"] != state {
			t.Fatalf("primary %q changed the compiled additional identity: %v", primary, endpoint)
		}
	}
}

func TestDirectAndNamedPoliciesDoNotRequireUnusedDefault(t *testing.T) {
	for _, test := range []struct {
		name, defaultExit, final string
		routes                   []RouteConfig
		removePrimary            bool
	}{
		{name: "unset default direct", defaultExit: UnsetExit, final: "direct"},
		{name: "pending primary default", defaultExit: MainExit, final: "direct"},
		{name: "pending extra default", defaultExit: "spare", final: "direct"},
		{name: "missing legacy main default", defaultExit: MainExit, final: "direct", removePrimary: true},
		{name: "named final", defaultExit: UnsetExit, final: "office"},
		{name: "named domain group", defaultExit: UnsetExit, final: "direct", routes: []RouteConfig{{Exit: "office", Domains: []string{"corp.example"}}}},
		{name: "named IP group", defaultExit: UnsetExit, final: "direct", routes: []RouteConfig{{Exit: "office", IPs: []string{"203.0.113.0/24"}}}},
		{name: "empty missing group", defaultExit: UnsetExit, final: "direct", routes: []RouteConfig{{Exit: "missing"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := Config{
				StateDir: t.TempDir(), Tailscale: &TailscaleConfig{Hostname: "primary"},
				Exits: []ExitConfig{{ID: "office", ExitNode: "100.101.0.11"}, {ID: "spare"}},
				Rules: RulesConfig{DefaultExit: test.defaultExit, Final: test.final, Routes: test.routes},
			}
			if test.removePrimary {
				config.Tailscale = nil
			}
			config, err := Normalize(config, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if config.RequiresDefaultExit() || requiresTrafficExit(config, MainExit) || requiresTrafficExit(config, "spare") {
				t.Fatal("unused missing or pending default became a required gateway")
			}
			if err := ValidateTrafficExit(config); err != nil {
				t.Fatal(err)
			}
			compiled, err := CompileContext(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			if TailscaleEnabled {
				if err := Check(compiled); err != nil {
					t.Fatal("compiled policy references an unavailable DNS/outbound:", err)
				}
			}
			var native map[string]any
			if err := json.Unmarshal(compiled.JSON, &native); err != nil {
				t.Fatal(err)
			}
			if tags := endpointTags(native); tags["tailnet"] != nil || tags["exit-spare"] != nil {
				t.Fatal("pending device entered the traffic runtime", tags)
			}
			for _, raw := range native["route"].(map[string]any)["rules"].([]any) {
				rule := raw.(map[string]any)
				prefixes, _ := rule["ip_cidr"].([]any)
				if slices.Contains(prefixes, any("100.64.0.0/10")) {
					t.Fatal("bare Tailnet fallback referenced an unusable default", rule)
				}
			}
			if test.final == "office" || len(test.routes) > 0 && len(test.routes[0].Domains)+len(test.routes[0].IPs) > 0 {
				if !requiresTrafficExit(config, "office") {
					t.Fatal("explicit owner lost its required status")
				}
				config.Exits[0].ExitNode = ""
				if _, err := CompileContext(t.Context(), config); err == nil || !strings.Contains(err.Error(), `"office"`) {
					t.Fatal("unused default exemption weakened required-owner readiness", err)
				}
			}
		})
	}
}

func TestNamedPrimaryPolicyWorksWithUnsetDefault(t *testing.T) {
	for _, final := range []string{MainExit, "direct"} {
		config := Config{
			StateDir: t.TempDir(), Tailscale: &TailscaleConfig{Hostname: "primary", ExitNode: "100.64.0.10"},
			Rules: RulesConfig{DefaultExit: UnsetExit, Final: final},
		}
		if final == "direct" {
			config.Rules.Routes = []RouteConfig{{Exit: MainExit, Domains: []string{"corp.example"}}}
		}
		if config.RequiresDefaultExit() || !requiresTrafficExit(config, MainExit) {
			t.Fatal("named primary policy became dependent on an unrelated default")
		}
		compiled, err := CompileContext(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		if TailscaleEnabled {
			if err := Check(compiled); err != nil {
				t.Fatal(err)
			}
		}
		config.Tailscale.ExitNode = ""
		if _, err := CompileContext(t.Context(), config); err == nil || !strings.Contains(err.Error(), `"main"`) {
			t.Fatal("unset default exemption weakened explicit primary ownership", err)
		}
	}
}

func TestOnlyPendingDevicesCanPrepareDirectConnection(t *testing.T) {
	config, err := Normalize(Config{
		StateDir: filepath.Join(t.TempDir(), "state"), Tailscale: &TailscaleConfig{Hostname: "primary"},
		Exits: []ExitConfig{{ID: "office"}}, Rules: RulesConfig{DefaultExit: UnsetExit},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileContext(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareConnection(t.Context(), config, compiled, nil, nil)
	if err != nil || string(prepared.JSON) != string(compiled.JSON) {
		t.Fatal("no active Tailnet endpoints should need authentication before direct traffic", err)
	}
}

func TestUsableDefaultStillRequiresBareTailnetFallback(t *testing.T) {
	config, err := Normalize(Config{
		StateDir: t.TempDir(), Exits: []ExitConfig{{ID: "office", ExitNode: "100.101.0.11"}},
		Rules: RulesConfig{DefaultExit: "office", Final: "direct"},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !config.RequiresDefaultExit() || !requiresTrafficExit(config, "office") {
		t.Fatal("configured default's bare Tailnet policy became optional")
	}
	native := compiledNative(t, config)
	found := false
	for _, raw := range native["route"].(map[string]any)["rules"].([]any) {
		rule := raw.(map[string]any)
		prefixes, _ := rule["ip_cidr"].([]any)
		if slices.Contains(prefixes, any("100.64.0.0/10")) && slices.Contains(prefixes, any("fd7a:115c:a1e0::/48")) {
			found = rule["outbound"] == "exit-office"
		}
	}
	if !found {
		t.Fatal("configured default lost bare Tailnet-address ownership")
	}
	required, optional := preflightExits(config, []engineExit{{id: "office"}}, true)
	if done, waiting, _, err := connectionReadiness("office", required, optional, map[string]Status{"office": {BackendState: "Running"}}, true, false); err != nil || done || !waiting {
		t.Fatal("offline implicit fallback owner was approved", done, waiting, err)
	}
	if _, err := compileWithOptions(t.Context(), config, compileOptions{blockedExits: map[string]bool{"office": true}}); err == nil {
		t.Fatal("unavailable bare Tailnet fallback could silently become direct")
	}
}

func TestPendingNamedOwnerRetainsSelectionDraftBoundary(t *testing.T) {
	config := Config{
		StateDir: t.TempDir(), Tailscale: &TailscaleConfig{Hostname: "primary", ExitNode: "100.64.0.10"},
		Exits: []ExitConfig{{ID: "office"}}, Rules: RulesConfig{DefaultExit: "office", Final: "office"},
	}
	if config.RequiresDefaultExit() || !requiresTrafficExit(config, "office") {
		t.Fatal("pending nominal selection lost its explicit named ownership")
	}
	if err := ValidateTrafficExit(config); err == nil || !strings.Contains(err.Error(), `"office"`) {
		t.Fatal("pending selected named owner lost its selection-draft error", err)
	}
	for _, defaultExit := range []string{MainExit, UnsetExit} {
		config.Rules.DefaultExit = defaultExit
		if err := ValidateTrafficExit(config); err != nil {
			t.Fatal("unrelated nominal selection changed pending named-owner classification", err)
		}
		if _, err := CompileContext(t.Context(), config); err == nil || !strings.Contains(err.Error(), `"office"`) {
			t.Fatal("pending named owner could start traffic", err)
		}
	}
}

func TestDefaultProxyPolicyStillRequiresSelection(t *testing.T) {
	for _, rules := range []RulesConfig{
		{Final: "proxy"}, {GFWList: "builtin"}, {Source: new("builtin")},
		{ProxyDomains: []string{"proxy.example"}}, {ProxyIPs: []string{"203.0.113.0/24"}},
		{Mode: RoutingGlobal, GlobalExit: UnsetExit},
	} {
		rules.DefaultExit = UnsetExit
		config := Config{Exits: []ExitConfig{{ID: "office", ExitNode: "100.101.0.11"}}, Rules: rules}
		if !config.RequiresDefaultExit() {
			t.Fatalf("actual proxy policy lost its default requirement: %+v", rules)
		}
		if err := ValidateTrafficExit(config); err == nil || !strings.Contains(err.Error(), "select a default exit") {
			t.Fatalf("proxy policy used an unset default: %+v: %v", rules, err)
		}
	}
}

func TestExitsCompileToSeparateDevicesAndRoutes(t *testing.T) {
	config, err := loadTestConfig(t, multiExitYAML)
	if err != nil {
		t.Fatal(err)
	}
	withoutPrimary, err := loadTestConfig(t, "exits:\n  - id: office\n")
	if err != nil || config.Exits[0].Hostname != withoutPrimary.Exits[0].Hostname {
		t.Fatalf("additional default hostname depends on primary: %q, %v", config.Exits[0].Hostname, err)
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
			tags["tailnet"]["hostname"] != config.Exits[0].Hostname {
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
		"unsafe route exit":  "tailscale: {}\nrules:\n  routes:\n    - exit: ../nowhere\n      domains: [a.example]\n",
		"unsafe final exit":  "tailscale: {}\nrules:\n  final: ../nowhere\n",
		"bad route CIDR":     "tailscale: {}\nexits:\n  - id: a\nrules:\n  routes:\n    - exit: a\n      ips: [10.0.0.1/8]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadTestConfig(t, text); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}

func TestExplicitMainFinalIsIndependentOfAdditionalDefault(t *testing.T) {
	config := focusedExitTestConfig(t)
	config.Rules.DefaultExit, config.Rules.Final = "office", MainExit
	config.Rules.ProxyDomains = []string{"proxy.example"}
	compiled, err := CompileContext(t.Context(), config)
	if err != nil {
		t.Fatal("explicit main final was rejected:", err)
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
	if native["route"].(map[string]any)["final"] != "tailnet" || native["dns"].(map[string]any)["final"] != "dns-proxy" {
		t.Fatal("named main final silently used the additional default")
	}
	if firstSuffixRuleValue(native["route"].(map[string]any)["rules"].([]any), "proxy.example", "outbound") != "exit-office" {
		t.Fatal("explicit main final replaced the default used by proxy matches")
	}
	for tag, id := range map[string]string{"tailnet": MainExit, "exit-office": "office"} {
		if endpointTags(native)[tag]["state_directory"] != config.ExitStateDir(id) {
			t.Fatal("explicit main final changed the saved device's state path", tag)
		}
	}
}

func TestMainFinalDraftsRemainEditableAndDeviceSessionsWork(t *testing.T) {
	for _, primary := range []string{"missing", "pending"} {
		t.Run(primary, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.Rules.DefaultExit, config.Rules.GlobalExit, config.Rules.Final = "office", "office", MainExit
			config.Rules.Routes, config.DNS.MagicDomains = nil, nil
			if primary == "missing" {
				config.Tailscale = nil
			} else {
				config.Tailscale.ExitNode = ""
			}
			config, err := Normalize(config, t.TempDir())
			if err != nil || config.Rules.Final != MainExit {
				t.Fatal("main final draft was rejected or replaced", err)
			}
			_, err = CompileContext(t.Context(), config)
			if err == nil || !strings.Contains(err.Error(), `exit "main"`) || errors.Is(err, ErrUnresolvedSplitExit) != (primary == "missing") {
				t.Fatal("missing/pending main final used a different default exit", err)
			}
			if primary == "missing" {
				if _, err := PrepareConnection(t.Context(), config, Compiled{}, nil, nil); !errors.Is(err, ErrUnresolvedSplitExit) {
					t.Fatal("connection preflight did not reject missing main before opening state", err)
				}
			}
			if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
				t.Fatal("unready main final opened saved device state")
			}
			discovery, err := compilePreflight(config, true)
			if err != nil {
				t.Fatal("dormant main final prevented login/discovery", err)
			}
			if TailscaleEnabled {
				if err := Check(discovery); err != nil {
					t.Fatal(err)
				}
			}
			config.Rules.Mode = RoutingGlobal
			global, err := CompileContext(t.Context(), config)
			if err != nil || global.runtimeExit != "office" {
				t.Fatal("Global activated dormant main final", err)
			}
			if TailscaleEnabled {
				if err := Check(global); err != nil {
					t.Fatal(err)
				}
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

func TestDormantSplitReferencesSurviveInventoryChanges(t *testing.T) {
	for _, inventory := range []string{"empty", "primary", "replacement extra"} {
		for _, policy := range []string{"final", "domain group", "IP group", "empty group"} {
			t.Run(inventory+"/"+policy, func(t *testing.T) {
				config := Config{StateDir: filepath.Join(t.TempDir(), "state")}
				switch inventory {
				case "primary":
					config.Tailscale = &TailscaleConfig{Hostname: "primary", ExitNode: "100.64.0.10"}
				case "replacement extra":
					config.Exits = []ExitConfig{{ID: "replacement", Hostname: "replacement", ExitNode: "100.64.0.10"}}
					config.Rules.DefaultExit, config.Rules.GlobalExit = "replacement", "replacement"
				}
				switch policy {
				case "final":
					config.Rules.Final = "office"
				case "domain group":
					config.Rules.Routes = []RouteConfig{{Exit: "office", Domains: []string{"corp.example"}}}
				case "IP group":
					config.Rules.Routes = []RouteConfig{{Exit: "office", IPs: []string{"203.0.113.0/24"}}}
				case "empty group":
					config.Rules.Routes = []RouteConfig{{Exit: "office"}}
				}
				config, err := Normalize(config, t.TempDir())
				if err != nil {
					t.Fatalf("inventory made retained Split policy unsaveable: %v", err)
				}
				data, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				var received Config
				if err := json.Unmarshal(data, &received); err != nil {
					t.Fatal(err)
				}
				received, err = Normalize(received, t.TempDir())
				if err != nil || received.Rules.Final != config.Rules.Final || !slices.EqualFunc(received.Rules.Routes, config.Rules.Routes, func(a, b RouteConfig) bool {
					return a.Exit == b.Exit && slices.Equal(a.Domains, b.Domains) && slices.Equal(a.IPs, b.IPs)
				}) {
					t.Fatalf("wire normalization discarded retained policy: %+v, %v", received.Rules, err)
				}
				live := policy != "empty group"
				for _, check := range []struct {
					name string
					run  func(Config) error
				}{
					{"validation", ValidateTrafficExit},
					{"compile", func(c Config) error { _, err := Compile(c); return err }},
					{"connection preflight compile", func(c Config) error { _, err := compilePreflight(c, false); return err }},
					{"connection preflight", func(c Config) error {
						if !live {
							return nil
						}
						return PrepareTailnet(t.Context(), c, nil, nil)
					}},
				} {
					err := check.run(received)
					if live && (err == nil || !strings.Contains(err.Error(), `"office"`)) {
						t.Fatalf("%s allowed unresolved live policy to fall through: %v", check.name, err)
					}
					if !live && err != nil {
						t.Fatalf("%s activated an empty dormant group: %v", check.name, err)
					}
				}
				if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
					t.Fatal("unresolved policy opened saved state before rejection")
				}
				deviceSession, err := compilePreflight(received, true)
				if err != nil {
					t.Fatalf("retained policy blocked login/discovery: %v", err)
				}
				if TailscaleEnabled {
					if err := Check(deviceSession); err != nil {
						t.Fatal(err)
					}
				}
				// Explicitly restoring the referenced ID resolves the policy. Adding
				// another ready device alone must never reassign its traffic.
				received.Exits = append(received.Exits, ExitConfig{ID: "office", Hostname: "office", ExitNode: "100.101.0.11"})
				if inventory == "empty" {
					received.Rules.DefaultExit, received.Rules.GlobalExit = "office", "office"
				}
				received, err = Normalize(received, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				compiled, err := Compile(received)
				if err != nil {
					t.Fatalf("explicitly resolved policy did not compile: %v", err)
				}
				if TailscaleEnabled {
					if err := Check(compiled); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestUnresolvedSplitExitClassification(t *testing.T) {
	for _, test := range []struct {
		name       string
		configure  func(*Config)
		unresolved bool
		wantExit   string
		compileOK  bool
	}{
		{name: "missing final owner", unresolved: true, wantExit: "office", configure: func(c *Config) { c.Rules.Final = "office" }},
		{name: "missing domain group owner", unresolved: true, wantExit: "office", configure: func(c *Config) {
			c.Rules.Routes = []RouteConfig{{Exit: "office", Domains: []string{"corp.example"}}}
		}},
		{name: "missing IP group owner", unresolved: true, wantExit: "office", configure: func(c *Config) {
			c.Rules.Routes = []RouteConfig{{Exit: "office", IPs: []string{"203.0.113.0/24"}}}
		}},
		{name: "missing primary group owner", unresolved: true, wantExit: MainExit, configure: func(c *Config) {
			c.Tailscale = nil
			c.Exits = []ExitConfig{{ID: "replacement", Hostname: "replacement", ExitNode: "100.64.0.10"}}
			c.Rules.DefaultExit, c.Rules.GlobalExit = "replacement", "replacement"
			c.Rules.Routes = []RouteConfig{{Exit: MainExit, Domains: []string{"corp.example"}}}
		}},
		{name: "missing primary final owner", unresolved: true, wantExit: MainExit, configure: func(c *Config) {
			c.Tailscale = nil
			c.Exits = []ExitConfig{{ID: "replacement", Hostname: "replacement", ExitNode: "100.64.0.10"}}
			c.Rules.DefaultExit, c.Rules.GlobalExit, c.Rules.Final = "replacement", "replacement", MainExit
		}},
		{name: "missing private-name owner", unresolved: true, wantExit: MainExit, configure: func(c *Config) {
			c.Tailscale = nil
			c.Exits = []ExitConfig{{ID: "replacement", Hostname: "replacement", ExitNode: "100.64.0.10"}}
			c.Rules.DefaultExit, c.Rules.GlobalExit = "replacement", "replacement"
			c.DNS.MagicDomains = []string{"primary.ts.net"}
		}},
		{name: "last private-name owner removed", unresolved: true, wantExit: MainExit, configure: func(c *Config) {
			c.Tailscale = nil
			c.DNS.MagicDomains = []string{"primary.ts.net"}
		}},
		{name: "empty missing group", compileOK: true, configure: func(c *Config) {
			c.Rules.Routes = []RouteConfig{{Exit: "office"}}
		}},
		{name: "pending default node", configure: func(c *Config) { c.Tailscale.ExitNode = ""; c.Rules.Final = "proxy" }},
		{name: "missing default device", configure: func(c *Config) { c.Tailscale = nil; c.Rules.Final = "proxy" }},
		{name: "pending final node", configure: func(c *Config) {
			c.Exits = []ExitConfig{{ID: "office", Hostname: "office"}}
			c.Rules.Final = "office"
		}},
		{name: "pending group node", configure: func(c *Config) {
			c.Exits = []ExitConfig{{ID: "office", Hostname: "office"}}
			c.Rules.Routes = []RouteConfig{{Exit: "office", Domains: []string{"corp.example"}}}
		}},
		{name: "pending private-name owner", configure: func(c *Config) {
			c.Exits = []ExitConfig{{ID: "office", Hostname: "office", ExitNode: "100.101.0.11"}}
			c.Rules.DefaultExit = "office"
			c.Tailscale.ExitNode = ""
			c.DNS.MagicDomains = []string{"primary.ts.net"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := Config{
				StateDir:  filepath.Join(t.TempDir(), "state"),
				Tailscale: &TailscaleConfig{Hostname: "primary", ExitNode: "100.64.0.10"},
			}
			test.configure(&config)
			config, err := Normalize(config, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, api := range []string{"ValidateTrafficExit", "CompileContext"} {
				err := ValidateTrafficExit(config)
				if api == "CompileContext" {
					_, err = CompileContext(t.Context(), config)
					if (err == nil) != test.compileOK {
						t.Fatalf("connection compilation changed readiness behavior: %v", err)
					}
				}
				if errors.Is(err, ErrUnresolvedSplitExit) != test.unresolved {
					t.Fatalf("%s classified the wrong draft condition: %v", api, err)
				}
				if test.unresolved && !strings.Contains(err.Error(), `"`+test.wantExit+`"`) {
					t.Fatalf("%s lost the actionable missing exit ID: %v", api, err)
				}
			}
		})
	}
}

func TestRetainedSplitPolicyDoesNotAffectGlobalOrFocusedSessions(t *testing.T) {
	for _, selected := range []string{MainExit, "replacement"} {
		for _, session := range []string{"global", "focused"} {
			t.Run(selected+"/"+session, func(t *testing.T) {
				config := Config{
					StateDir: filepath.Join(t.TempDir(), "state"),
					Rules: RulesConfig{DefaultExit: selected, GlobalExit: selected, Final: "office", Routes: []RouteConfig{
						{Exit: "office", Domains: []string{"corp.example"}},
					}},
					TUN: TUNConfig{Enabled: true},
				}
				if selected == MainExit {
					config.Tailscale = &TailscaleConfig{Hostname: "primary", ExitNode: "100.64.0.10"}
				} else {
					config.Exits = []ExitConfig{{ID: selected, Hostname: "replacement", ExitNode: "100.64.0.10"}}
				}
				config, err := Normalize(config, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				if session == "global" {
					config.Rules.Mode = RoutingGlobal
				} else {
					config, err = config.FocusExit(selected)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := ValidateTrafficExit(config); err != nil {
					t.Fatal("device session activated retained Split policy:", err)
				}
				compiled, err := Compile(config)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(compiled.JSON), "corp.example") || strings.Contains(string(compiled.JSON), "exit-office") {
					t.Fatal("device session compiled retained Split policy")
				}
				if TailscaleEnabled {
					if err := Check(compiled); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestMissingPrimaryRouteOwnerRemainsSaveableButCannotConnect(t *testing.T) {
	config, err := Normalize(Config{
		StateDir: t.TempDir(),
		Exits:    []ExitConfig{{ID: "replacement", ExitNode: "100.101.0.11"}},
		Rules: RulesConfig{DefaultExit: "replacement", GlobalExit: "replacement", Routes: []RouteConfig{
			{Exit: MainExit, Domains: []string{"primary.example"}},
		}},
	}, t.TempDir())
	if err != nil {
		t.Fatal("removed primary's explicit policy became unsaveable:", err)
	}
	if config.Tailscale != nil {
		t.Fatal("retained route invented a replacement primary")
	}
	if err := ValidateTrafficExit(config); err == nil || !strings.Contains(err.Error(), `"main"`) {
		t.Fatal("a ready replacement hid the removed primary route owner:", err)
	}
	if _, err := Compile(config); err == nil {
		t.Fatal("removed primary's explicit traffic fell through to the replacement")
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
	if _, err := Compile(pending); err != nil {
		t.Fatal("unused pending first extra blocked direct-only traffic:", err)
	}
	pending.Rules.Final = "proxy"
	if _, err := Compile(pending); err == nil {
		t.Fatal("pending default can carry required proxy traffic before choosing a node")
	}
}
