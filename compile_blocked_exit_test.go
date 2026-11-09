package tailsplit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func blockedExitNative(t *testing.T, config Config, blocked map[string]bool) (Compiled, map[string]any) {
	t.Helper()
	compiled, err := compileWithOptions(t.Context(), config, compileOptions{blockedExits: blocked})
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
	return compiled, native
}

func firstDomainSuffixRule(rules []any, hostname string) map[string]any {
	for _, raw := range rules {
		rule := raw.(map[string]any)
		suffixes, _ := rule["domain_suffix"].([]any)
		for _, rawSuffix := range suffixes {
			suffix := rawSuffix.(string)
			if hostname == suffix || strings.HasSuffix(hostname, "."+suffix) {
				return rule
			}
		}
	}
	return nil
}

func assertBlockedExitDomainPolicy(t *testing.T, native map[string]any, hostname, server, outbound string) {
	t.Helper()
	dnsRules := native["dns"].(map[string]any)["rules"].([]any)
	trafficRules := native["route"].(map[string]any)["rules"].([]any)
	dnsRule := firstDomainSuffixRule(dnsRules, hostname)
	trafficRule := firstDomainSuffixRule(trafficRules, hostname)
	if server == "" {
		if dnsRule["action"] != "predefined" || dnsRule["rcode"] != "REFUSED" || dnsRule["server"] != nil {
			t.Errorf("%s DNS must refuse without a resolver: %v", hostname, dnsRule)
		}
		if trafficRule["action"] != "reject" || trafficRule["server"] != nil || trafficRule["outbound"] != nil {
			t.Errorf("%s traffic must reject before resolution or egress: %v", hostname, trafficRule)
		}
		return
	}
	if dnsRule["action"] != "route" || dnsRule["server"] != server {
		t.Errorf("%s DNS must use %q: %v", hostname, server, dnsRule)
	}
	if trafficRule["action"] != "resolve" || trafficRule["server"] != server {
		t.Errorf("%s traffic must resolve through %q: %v", hostname, server, trafficRule)
	}
	if got := firstSuffixRuleValue(trafficRules, hostname, "outbound"); got != outbound {
		t.Errorf("%s outbound = %q; want %q", hostname, got, outbound)
	}
}

func TestBlockedOptionalPrimaryRetainsSuffixOwnership(t *testing.T) {
	for _, test := range []struct {
		name, primarySuffix, extraSuffix, blockedHost, readyHost, blockedID string
	}{
		{"broad primary narrow extra", "ts.net", "office.ts.net", "host.other.ts.net", "host.office.ts.net", MainExit},
		{"narrow primary broad extra", "office.ts.net", "ts.net", "host.office.ts.net", "host.other.ts.net", MainExit},
		{"primary exact duplicate", "office.ts.net", "office.ts.net", "host.office.ts.net", "", MainExit},
		{"empty primary status ID", "ts.net", "office.ts.net", "host.other.ts.net", "host.office.ts.net", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.Rules.DefaultExit = "office"
			config.DNS.MagicDomains = []string{test.primarySuffix}
			config.Tailscale.Domains = []string{"service.example"}
			config.Exits[0].MagicDNSSuffix = test.extraSuffix
			config.Rules.DirectDomains = []string{"service.example", "direct.example"}
			beforeConfig, _ := json.Marshal(config)
			original, err := compile(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			originalJSON := bytes.Clone(original.JSON)
			_, native := blockedExitNative(t, config, map[string]bool{test.blockedID: true})
			tags := endpointTags(native)
			if len(tags) != 1 || tags["tailnet"] != nil || tags["exit-office"] == nil ||
				tags["exit-office"]["state_directory"] != config.ExitStateDir("office") {
				t.Fatalf("blocked primary changed the ready device identity: %v", tags)
			}
			for _, raw := range native["dns"].(map[string]any)["servers"].([]any) {
				server := raw.(map[string]any)
				if server["tag"] == "dns-proxy" || server["tag"] == "dns-tailnet" || server["endpoint"] == "tailnet" {
					t.Fatalf("blocked primary retained a live DNS server: %v", server)
				}
			}
			assertBlockedExitDomainPolicy(t, native, test.blockedHost, "", "")
			assertBlockedExitDomainPolicy(t, native, "service.example", "", "")
			if test.readyHost != "" {
				assertBlockedExitDomainPolicy(t, native, test.readyHost, "dns-tailnet-office", "exit-office")
			}
			trafficRules := native["route"].(map[string]any)["rules"].([]any)
			if firstDomainSuffixRule(trafficRules, "direct.example")["outbound"] != "direct" ||
				native["route"].(map[string]any)["final"] != "exit-office" {
				t.Fatal("optional blocking changed direct precedence or the required final exit")
			}
			afterConfig, _ := json.Marshal(config)
			again, err := compile(t.Context(), config)
			if err != nil || !bytes.Equal(beforeConfig, afterConfig) || !bytes.Equal(original.JSON, originalJSON) || !bytes.Equal(again.JSON, originalJSON) {
				t.Fatal("blocked compilation changed saved inventory, original output, or ordinary compilation:", err)
			}
		})
	}
}

func TestBlockedOptionalExtraRetainsSuffixOwnership(t *testing.T) {
	for _, mode := range []string{"metadata", "legacy suffix", "first extra exact duplicate"} {
		t.Run(mode, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.Rules.Final = "direct"
			config.Rules.Routes = nil
			config.DNS.MagicDomains = []string{"ts.net"}
			config.Tailscale.Domains = []string{"service.example"}
			config.Exits[0].Tailnet = "Office network"
			config.Exits[0].MagicDNSSuffix = "office.ts.net"
			if mode == "legacy suffix" {
				config.Exits[0].Tailnet, config.Exits[0].MagicDNSSuffix = "office.ts.net", ""
			}
			if mode == "first extra exact duplicate" {
				config.Exits[1].ExitNode = "100.101.0.12"
				config.Exits[1].MagicDNSSuffix = "office.ts.net"
			}
			_, native := blockedExitNative(t, config, map[string]bool{"office": true})
			tags := endpointTags(native)
			if tags["exit-office"] != nil || tags["tailnet"] == nil || tags["tailnet"]["state_directory"] != config.ExitStateDir(MainExit) {
				t.Fatalf("blocked extra changed the primary runtime identity: %v", tags)
			}
			if mode == "first extra exact duplicate" && tags["exit-spare"] == nil {
				t.Fatal("blocking the suffix owner removed another ready saved device")
			}
			for _, raw := range native["dns"].(map[string]any)["servers"].([]any) {
				server := raw.(map[string]any)
				if server["tag"] == "dns-proxy-office" || server["tag"] == "dns-tailnet-office" || server["endpoint"] == "exit-office" {
					t.Fatalf("blocked extra retained a live DNS server: %v", server)
				}
			}
			assertBlockedExitDomainPolicy(t, native, "host.office.ts.net", "", "")
			assertBlockedExitDomainPolicy(t, native, "host.other.ts.net", "dns-tailnet", "tailnet")
			assertBlockedExitDomainPolicy(t, native, "service.example", "dns-direct", "tailnet")
		})
	}
}

func TestBlockedRequiredTrafficExitIsRejected(t *testing.T) {
	for _, test := range []struct {
		name, blockedID, defaultExit, final, mode, globalExit string
		routes                                                []RouteConfig
	}{
		{name: "primary default", blockedID: MainExit},
		{name: "primary default status alias", blockedID: ""},
		{name: "extra default", blockedID: "office", defaultExit: "office"},
		{name: "named final", blockedID: "office", final: "office"},
		{name: "named primary final", blockedID: MainExit, defaultExit: "office", final: MainExit},
		{name: "named primary final status alias", blockedID: "", defaultExit: "office", final: MainExit},
		{name: "domain group", blockedID: "office", routes: []RouteConfig{{Exit: "office", Domains: []string{"corp.example"}}}},
		{name: "IP group", blockedID: "office", routes: []RouteConfig{{Exit: "office", IPs: []string{"203.0.113.0/24"}}}},
		{name: "Global selected extra", blockedID: "office", mode: RoutingGlobal, globalExit: "office"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.Rules.Final, config.Rules.Routes = "direct", test.routes
			if test.defaultExit != "" {
				config.Rules.DefaultExit = test.defaultExit
			}
			if test.final != "" {
				config.Rules.Final = test.final
			}
			if test.mode != "" {
				config.Rules.Mode, config.Rules.GlobalExit = test.mode, test.globalExit
			}
			_, err := compileWithOptions(t.Context(), config, compileOptions{blockedExits: map[string]bool{test.blockedID: true}})
			if err == nil || !strings.Contains(err.Error(), "cannot block required traffic exit") {
				t.Fatalf("required gateway could be silently omitted: %v", err)
			}
		})
	}
}

func TestBlockedExitOptionsPreserveDiscoveryAndGlobalScope(t *testing.T) {
	t.Run("discovery keeps every saved device", func(t *testing.T) {
		config := focusedExitTestConfig(t)
		config.includePending = true
		config.Tailscale.ExitNode = ""
		original, err := compile(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		compiled, native := blockedExitNative(t, config, map[string]bool{MainExit: true, "office": true, "spare": true})
		if !bytes.Equal(original.JSON, compiled.JSON) || len(endpointTags(native)) != 3 {
			t.Fatal("traffic-only blocked options altered login/discovery compilation")
		}
	})
	t.Run("Global ignores optional Split devices", func(t *testing.T) {
		config := focusedExitTestConfig(t)
		config.Rules.Mode, config.Rules.GlobalExit = RoutingGlobal, "office"
		config.Rules.Final = "spare"
		config.Rules.Routes = []RouteConfig{{Exit: MainExit, Domains: []string{"split.example"}}}
		original, err := compile(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		compiled, _ := blockedExitNative(t, config, map[string]bool{MainExit: true, "spare": true})
		if !bytes.Equal(original.JSON, compiled.JSON) || compiled.runtimeExit != "office" {
			t.Fatal("blocking an optional Split device changed Global compilation")
		}
	})
	t.Run("empty route group remains optional", func(t *testing.T) {
		config := focusedExitTestConfig(t)
		config.Rules.Final = "direct"
		config.Rules.Routes = []RouteConfig{{Exit: "office"}}
		_, native := blockedExitNative(t, config, map[string]bool{"office": true})
		if endpointTags(native)["exit-office"] != nil {
			t.Fatal("an empty group incorrectly made the optional endpoint required")
		}
	})
}
