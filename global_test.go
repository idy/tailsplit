package tailsplit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	box "github.com/sagernet/sing-box"
)

func TestGlobalModeResolvesTheRealTailnetEndpointAsItsOutbound(t *testing.T) {
	if !TailscaleEnabled {
		t.Skip("real Tailscale endpoint registration is disabled")
	}
	config, err := loadTestConfig(t, `
state_dir: ./state
tailscale:
  exit_node: 100.64.0.10
rules:
  mode: global
tun:
  enabled: true
`)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx := engineContext(context.Background())
	options, err := parseNative(ctx, compiled.JSON)
	if err != nil {
		t.Fatal(err)
	}
	// Use the unmodified generated configuration and the real registered
	// Tailscale endpoint. Do not start a TUN, authenticate, or inject a
	// replacement SOCKS transport to validate this outbound lookup.
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	endpoint, found := instance.Endpoint().Get("tailnet")
	if !found || endpoint.Type() != "tailscale" {
		t.Fatal("global mode lost the real Tailnet endpoint")
	}
	routed, found := instance.Outbound().Outbound(options.Route.Final)
	if !found || routed != endpoint {
		t.Fatal("the global route cannot resolve its endpoint as an outbound")
	}
	var native object
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		t.Fatal(err)
	}
	for _, item := range native["dns"].(map[string]any)["servers"].([]any) {
		server := item.(map[string]any)
		if server["tag"] == "dns-proxy" {
			detour, found := instance.Outbound().Outbound(server["detour"].(string))
			if !found || detour != endpoint {
				t.Fatal("proxy DNS cannot resolve its Tailnet detour")
			}
		}
	}
}

func TestGlobalModeHasNoDirectClientRoutesOrDNS(t *testing.T) {
	config, err := loadTestConfig(t, `
state_dir: ./state
interface: en0
tailscale:
  exit_node: 100.64.0.10
  domains: [gateway.example.com]
dns:
  direct: system
  direct_ipv6: false
  magic_domains: [tail12345.ts.net]
rules:
  mode: global
  final: direct
  gfwlist: ./missing-list.txt
  direct_domains: [example.com]
  direct_ips: [192.168.0.0/16]
tun:
  enabled: true
`)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(config)
	if err != nil {
		t.Fatal("global mode still depends on the split rule file:", err)
	}
	if compiled.GFWList != nil {
		t.Fatal("global mode loaded GFWList")
	}
	var native object
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		t.Fatal(err)
	}
	route := native["route"].(map[string]any)
	if route["final"] != "tailnet" || route["default_domain_resolver"] != "dns-proxy" {
		t.Fatal("client traffic can fall back to the direct resolver or outbound:", route)
	}
	if len(native["outbounds"].([]any)) != 0 {
		t.Fatal("global mode exposes a direct outbound")
	}
	for _, item := range route["rules"].([]any) {
		rule := item.(map[string]any)
		if rule["outbound"] == "direct" || rule["server"] == "dns-direct" {
			t.Fatal("a split exception survives global mode:", rule)
		}
	}
	dns := native["dns"].(map[string]any)
	if dns["final"] != "dns-proxy" {
		t.Fatal("global DNS can fall back to direct")
	}
	for _, item := range dns["rules"].([]any) {
		rule := item.(map[string]any)
		if rule["server"] == "dns-direct" || rule["action"] == "predefined" {
			t.Fatal("global DNS retained a direct exception:", rule)
		}
	}
	endpoint := native["endpoints"].([]any)[0].(map[string]any)
	if endpoint["domain_resolver"] != "dns-direct" || endpoint["bind_interface"] != "en0" {
		t.Fatal("Tailscale control traffic cannot bootstrap outside its own TUN")
	}
	if TailscaleEnabled {
		if err := Check(compiled); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
		t.Fatal("global render/check created state or joined the Tailnet")
	}
}

func TestGlobalModeRequiresTUNButPreservesListenerFreeLogin(t *testing.T) {
	config, err := loadTestConfig(t, "tailscale: {}\nrules:\n  mode: global\n")
	if err != nil {
		t.Fatal(err)
	}
	config.Tailscale.ExitNode = "100.64.0.10"
	if _, err := Compile(config); err == nil || !strings.Contains(err.Error(), "tun.enabled") {
		t.Fatal("global mode can be mistaken for system protection with only a local proxy:", err)
	}
	compiled, err := compilePreflight(config)
	if err != nil {
		t.Fatal("global mode prevents safe device login/discovery:", err)
	}
	var native object
	if json.Unmarshal(compiled.JSON, &native) != nil || native["inbounds"] != nil {
		t.Fatal("global-mode login creates listeners or a TUN")
	}
	config.TUN.Enabled = true
	config.StateDir = filepath.Join(t.TempDir(), "state")
	if _, err := Compile(config); err != nil {
		t.Fatal("valid global TUN configuration was rejected:", err)
	}
}
