package tailsplit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPendingPrimaryPolicyFailsClosed(t *testing.T) {
	for _, policy := range []string{"dns.magic_domains", "tailscale.domains"} {
		t.Run(policy, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.Tailscale.ExitNode = ""
			config.Rules.DefaultExit = "office"
			config.DNS.MagicDomains = nil
			if policy == "dns.magic_domains" {
				config.DNS.MagicDomains = []string{"primary.ts.net"}
			} else {
				config.Tailscale.Domains = []string{"service.example"}
			}
			if err := ValidateTrafficExit(config); err != nil {
				t.Fatalf("ready additional default should pass traffic-exit validation: %v", err)
			}
			for _, preflight := range []bool{false, true} {
				var err error
				if preflight {
					_, err = compilePreflight(config, false)
				} else {
					_, err = CompileContext(t.Context(), config)
				}
				if err == nil || !strings.Contains(err.Error(), policy) || !strings.Contains(err.Error(), `exit "main"`) {
					t.Fatalf("preflight=%v: pending primary policy could fall through to another device/resolver: %v", preflight, err)
				}
			}
		})
	}
}

func TestPendingPrimaryWithoutOwnedPolicyAllowsAdditionalDefault(t *testing.T) {
	config := focusedExitTestConfig(t)
	config.Tailscale.ExitNode = ""
	config.DNS.MagicDomains = nil
	config.Rules.DefaultExit = "office"
	config.Rules.Final = "proxy"
	compiled, err := CompileContext(t.Context(), config)
	if err != nil {
		t.Fatalf("an unused pending primary must not block a ready additional default: %v", err)
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
	tags := endpointTags(native)
	if len(tags) != 1 || tags["exit-office"] == nil || native["route"].(map[string]any)["final"] != "exit-office" ||
		native["dns"].(map[string]any)["final"] != "dns-proxy-office" {
		t.Fatal("pending primary changed the additional default's runtime identity or policy")
	}
}

func TestPendingPrimaryPolicyAllowsLoginAndDiscovery(t *testing.T) {
	for _, focus := range []string{"", MainExit, "office"} {
		t.Run("focus="+focus, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.Tailscale.ExitNode = ""
			config.Tailscale.Domains = []string{"service.example"}
			config.Rules.DefaultExit = "office"
			if focus != "" {
				var err error
				config, err = config.FocusExit(focus)
				if err != nil {
					t.Fatal(err)
				}
			}
			compiled, err := compilePreflight(config, true)
			if err != nil {
				t.Fatalf("pending-primary policy blocked device-only login/discovery: %v", err)
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
			if native["inbounds"] != nil {
				t.Fatal("device-only session opened traffic listeners")
			}
			tags := endpointTags(native)
			wantID, wantCount := MainExit, 3
			if focus != "" {
				wantID, wantCount = focus, 1
			}
			if len(tags) != wantCount || tags["tailnet"] == nil || tags["tailnet"]["state_directory"] != config.ExitStateDir(wantID) {
				t.Fatalf("device-only session changed saved device identity: %v", tags)
			}
			if wantID == MainExit && tags["tailnet"]["exit_node"] != "" {
				t.Fatal("login/discovery silently selected a node for the pending primary")
			}
		})
	}
}

func TestMagicDNSLongestSuffixOwnsResolutionAndTraffic(t *testing.T) {
	type lookup struct {
		hostname, server, outbound string
	}
	for _, test := range []struct {
		name                     string
		primary                  []string
		extraSuffix, spareSuffix string
		lookups                  []lookup
	}{
		{
			name: "broad primary and narrow extra", primary: []string{"ts.net"}, extraSuffix: "office.ts.net",
			lookups: []lookup{
				{"host.office.ts.net", "dns-tailnet-office", "exit-office"},
				{"office.ts.net", "dns-tailnet-office", "exit-office"},
				{"host.other.ts.net", "dns-tailnet", "tailnet"},
				{"host.notoffice.ts.net", "dns-tailnet", "tailnet"},
			},
		},
		{
			name: "narrow primary and broad extra", primary: []string{"office.ts.net"}, extraSuffix: "ts.net",
			lookups: []lookup{
				{"host.office.ts.net", "dns-tailnet", "tailnet"},
				{"office.ts.net", "dns-tailnet", "tailnet"},
				{"host.other.ts.net", "dns-tailnet-office", "exit-office"},
			},
		},
		{
			name: "extra between primary suffixes", primary: []string{"ts.net", "secure.office.ts.net"}, extraSuffix: "office.ts.net",
			lookups: []lookup{
				{"host.secure.office.ts.net", "dns-tailnet", "tailnet"},
				{"host.office.ts.net", "dns-tailnet-office", "exit-office"},
				{"host.other.ts.net", "dns-tailnet", "tailnet"},
			},
		},
		{
			name: "primary owns exact duplicate", primary: []string{"shared.ts.net"}, extraSuffix: "shared.ts.net", spareSuffix: "shared.ts.net",
			lookups: []lookup{{"host.shared.ts.net", "dns-tailnet", "tailnet"}},
		},
		{
			name: "first extra owns exact duplicate", extraSuffix: "shared.ts.net", spareSuffix: "shared.ts.net",
			lookups: []lookup{{"host.shared.ts.net", "dns-tailnet-office", "exit-office"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.DNS.MagicDomains = test.primary
			config.Exits[0].Tailnet = "Office network"
			config.Exits[0].MagicDNSSuffix = test.extraSuffix
			if test.spareSuffix != "" {
				config.Exits[1].ExitNode = "100.101.0.12"
				config.Exits[1].MagicDNSSuffix = test.spareSuffix
			}
			native := compiledNative(t, config)
			dnsRules := native["dns"].(map[string]any)["rules"].([]any)
			trafficRules := native["route"].(map[string]any)["rules"].([]any)
			for _, lookup := range test.lookups {
				if got := firstSuffixRuleValue(dnsRules, lookup.hostname, "server"); got != lookup.server {
					t.Errorf("%s DNS server = %q; want %q", lookup.hostname, got, lookup.server)
				}
				if got := firstSuffixRuleValue(trafficRules, lookup.hostname, "server"); got != lookup.server {
					t.Errorf("%s route resolver = %q; want %q", lookup.hostname, got, lookup.server)
				}
				if got := firstSuffixRuleValue(trafficRules, lookup.hostname, "outbound"); got != lookup.outbound {
					t.Errorf("%s outbound = %q; want %q", lookup.hostname, got, lookup.outbound)
				}
			}
		})
	}
}

func TestExtraMagicDNSSuffixPrefersIdentityMetadata(t *testing.T) {
	for _, test := range []struct {
		name, suffix, tailnet, wantSuffix string
	}{
		{"metadata with display name", "office.ts.net", "Office network", "office.ts.net"},
		{"metadata overrides legacy suffix", "office.ts.net", "old.ts.net", "office.ts.net"},
		{"legacy suffix fallback", "", "legacy.ts.net", "legacy.ts.net"},
		{"display name has no DNS ownership", "", "Office network", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.DNS.MagicDomains = nil
			config.Exits[0].MagicDNSSuffix = test.suffix
			config.Exits[0].Tailnet = test.tailnet
			native := compiledNative(t, config)
			dnsRules := native["dns"].(map[string]any)["rules"].([]any)
			trafficRules := native["route"].(map[string]any)["rules"].([]any)
			for _, suffix := range []string{"office.ts.net", "old.ts.net", "legacy.ts.net"} {
				wantServer, wantOutbound := "", ""
				if suffix == test.wantSuffix {
					wantServer, wantOutbound = "dns-tailnet-office", "exit-office"
				}
				if got := firstSuffixRuleValue(dnsRules, "host."+suffix, "server"); got != wantServer {
					t.Errorf("%s DNS server = %q; want %q", suffix, got, wantServer)
				}
				if got := firstSuffixRuleValue(trafficRules, "host."+suffix, "outbound"); got != wantOutbound {
					t.Errorf("%s outbound = %q; want %q", suffix, got, wantOutbound)
				}
			}
		})
	}
}

func TestOrphanedPrivateSuffixPolicyFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name, suffix, tailnet, exit string
		missingPrimary              bool
	}{
		{name: "missing primary", missingPrimary: true, exit: MainExit},
		{name: "pending extra metadata", suffix: "private.ts.net", exit: "spare"},
		{name: "pending extra legacy suffix", tailnet: "private.ts.net", exit: "spare"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.Rules.DefaultExit = "office"
			if test.missingPrimary {
				config.Tailscale = nil
			} else {
				config.Exits[1].MagicDNSSuffix = test.suffix
				config.Exits[1].Tailnet = test.tailnet
			}
			for _, preflight := range []bool{false, true} {
				var err error
				if preflight {
					_, err = compilePreflight(config, false)
				} else {
					_, err = CompileContext(t.Context(), config)
				}
				if err == nil || !strings.Contains(err.Error(), `exit "`+test.exit+`"`) {
					t.Fatalf("preflight=%v: orphaned private DNS policy was silently dropped: %v", preflight, err)
				}
			}

			// Login/discovery remains available while the policy is dormant.
			discovery, err := compilePreflight(config, true)
			if err != nil {
				t.Fatalf("orphaned policy blocked device-only discovery: %v", err)
			}
			if TailscaleEnabled {
				if err := Check(discovery); err != nil {
					t.Fatal(err)
				}
			}
			var native map[string]any
			if err := json.Unmarshal(discovery.JSON, &native); err != nil {
				t.Fatal(err)
			}
			if native["inbounds"] != nil || endpointTags(native)["exit-spare"] == nil {
				t.Fatal("discovery opened listeners or omitted the pending saved device")
			}

			// Global ignores the saved Split policy and runs only its selected device.
			config.Rules.Mode, config.Rules.GlobalExit = RoutingGlobal, "office"
			compiled, err := CompileContext(t.Context(), config)
			if err != nil {
				t.Fatalf("orphaned Split policy blocked Global: %v", err)
			}
			if TailscaleEnabled {
				if err := Check(compiled); err != nil {
					t.Fatal(err)
				}
			}
			if compiled.runtimeExit != "office" || strings.Contains(string(compiled.JSON), "private.ts.net") ||
				strings.Contains(string(compiled.JSON), "primary.ts.net") {
				t.Fatal("Global retained the ignored private suffix policy")
			}
		})
	}
}

func firstSuffixRuleValue(rules []any, hostname, key string) string {
	for _, raw := range rules {
		rule := raw.(map[string]any)
		suffixes, _ := rule["domain_suffix"].([]any)
		for _, suffix := range suffixes {
			if domain := suffix.(string); hostname == domain || strings.HasSuffix(hostname, "."+domain) {
				if value, ok := rule[key].(string); ok {
					return value
				}
			}
		}
	}
	return ""
}
