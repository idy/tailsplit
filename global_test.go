package tailsplit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	compiled, err := compilePreflight(config, true)
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

func TestGlobalSelectedExtraOwnsTrafficDNSAndPreflight(t *testing.T) {
	config := focusedExitTestConfig(t)
	config.Rules.Mode = RoutingGlobal
	config.Rules.GlobalExit = "office"
	config.Rules.GFWList = filepath.Join(t.TempDir(), "missing-list")
	config.Rules.Final = "spare"
	config.Rules.Routes = []RouteConfig{{Exit: "spare", Domains: []string{"split.example"}}}
	// Neither the primary nor the unrelated spare has a chosen node. They must
	// not participate in Global routing, DNS or connection readiness.
	config.Tailscale.ExitNode = ""
	before, _ := json.Marshal(config)
	for _, preflight := range []bool{false, true} {
		t.Run(fmt.Sprint("preflight=", preflight), func(t *testing.T) {
			var compiled Compiled
			var err error
			if preflight {
				compiled, err = compilePreflight(config, false)
			} else {
				compiled, err = Compile(config)
			}
			if err != nil {
				t.Fatal(err)
			}
			if compiled.runtimeExit != "office" {
				t.Fatal("compiled endpoint lost its selected device identity")
			}
			var native object
			if err := json.Unmarshal(compiled.JSON, &native); err != nil {
				t.Fatal(err)
			}
			endpoints := native["endpoints"].([]any)
			if len(endpoints) != 1 {
				t.Fatalf("Global runs unrelated devices: %v", endpoints)
			}
			endpoint := endpoints[0].(map[string]any)
			if endpoint["tag"] != "tailnet" || endpoint["hostname"] != "laptop-office" ||
				endpoint["exit_node"] != "100.101.0.11" || endpoint["state_directory"] != config.ExitStateDir("office") {
				t.Fatalf("Global uses another device: %v", endpoint)
			}
			data := string(compiled.JSON)
			for _, unwanted := range []string{"primary.ts.net", "split.example", "dns-proxy-spare", "exit-spare", "corp.example"} {
				if strings.Contains(data, unwanted) {
					t.Fatalf("Global retained unrelated policy %q", unwanted)
				}
			}
			dns := native["dns"].(map[string]any)
			if dns["final"] != "dns-proxy" || native["route"].(map[string]any)["final"] != "tailnet" {
				t.Fatal("Global can fall back to another route or resolver")
			}
			for _, item := range dns["servers"].([]any) {
				server := item.(map[string]any)
				if server["tag"] == "dns-proxy" && server["detour"] != "tailnet" ||
					server["tag"] == "dns-tailnet" && server["endpoint"] != "tailnet" {
					t.Fatalf("DNS uses a different saved device: %v", server)
				}
			}
			if !strings.Contains(data, "office.ts.net") {
				t.Fatal("Global lost the chosen device's MagicDNS suffix")
			}
			if TailscaleEnabled {
				if err := Check(compiled); err != nil {
					t.Fatal(err)
				}
				// Construct only; never Start or read a saved login. The primary's
				// auth-key environment must not be requested for this extra device.
				engine, err := Open(context.Background(), config, compiled)
				if err != nil {
					t.Fatal(err)
				}
				defer engine.Close()
				if len(engine.exits) != 1 || engine.exits[0].id != "office" || engine.exits[0].tailnet != "office.ts.net" ||
					engine.exits[0].exitNode != "100.101.0.11" {
					t.Fatalf("readiness/status watches another exit: %+v", engine.exits)
				}
				if _, ok := engine.endpoint(MainExit); ok {
					t.Fatal("Global can ping the unrelated primary through its chosen extra")
				}
			}
		})
	}
	after, _ := json.Marshal(config)
	if string(before) != string(after) {
		t.Fatal("Global compilation changed saved Split policy or migration inventory")
	}
}

func TestGlobalMainDefaultAndSwitchBackPreserveSplitPolicy(t *testing.T) {
	config := focusedExitTestConfig(t)
	before, _ := json.Marshal(config)
	split, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Rules.Mode = RoutingGlobal
	for _, id := range []string{"", MainExit, "office"} {
		config.Rules.GlobalExit = id
		compiled, err := Compile(config)
		if err != nil {
			t.Fatal(err)
		}
		var native object
		_ = json.Unmarshal(compiled.JSON, &native)
		endpoints := native["endpoints"].([]any)
		want := id
		if want == "" {
			want = MainExit
		}
		if len(endpoints) != 1 || endpoints[0].(map[string]any)["state_directory"] != config.ExitStateDir(want) {
			t.Fatalf("Global %q did not select exactly its saved device: %v", id, endpoints)
		}
	}
	config.Rules.Mode, config.Rules.GlobalExit = RoutingSplit, MainExit
	restored, err := Compile(config)
	if err != nil || string(split.JSON) != string(restored.JSON) {
		t.Fatalf("switching back changed Split compilation: %v", err)
	}
	after, _ := json.Marshal(config)
	if string(before) != string(after) {
		t.Fatal("switching mode discarded the saved configuration")
	}
}

func TestGlobalExitValidationAndPendingDraft(t *testing.T) {
	config := focusedExitTestConfig(t)
	config.Rules.Mode = RoutingGlobal
	config.Rules.GlobalExit = "unknown"
	if _, err := Normalize(config, t.TempDir()); err == nil || !strings.Contains(err.Error(), "rules.global_exit") {
		t.Fatalf("unknown Global device accepted: %v", err)
	}
	if _, err := Compile(config); err == nil {
		t.Fatal("unknown Global device compiled")
	}
	config.Rules.GlobalExit = "spare"
	if _, err := Normalize(config, t.TempDir()); err != nil {
		t.Fatalf("cannot save a pending Global device before login: %v", err)
	}
	if _, err := Compile(config); err == nil || !strings.Contains(err.Error(), "spare") {
		t.Fatalf("Global silently fell back from an unconfigured node: %v", err)
	}
	if err := PrepareTailnet(context.Background(), config, nil, nil); err == nil || !strings.Contains(err.Error(), "spare") {
		t.Fatalf("Global did not reject a missing node before opening saved state: %v", err)
	}
	// Unfocused discovery may still inspect every device, regardless of the
	// selected Global device and without requiring any of their exit nodes.
	compiled, err := compilePreflight(config, true)
	if err != nil {
		t.Fatal(err)
	}
	var native object
	_ = json.Unmarshal(compiled.JSON, &native)
	if len(native["endpoints"].([]any)) != len(config.ExitIDs()) || compiled.runtimeExit != "" {
		t.Fatal("Global selection narrowed ordinary all-device discovery")
	}
	for _, id := range config.ExitIDs() {
		focused, err := config.FocusExit(id)
		if err != nil {
			t.Fatal(err)
		}
		endpoint := focusedPreflightEndpoint(t, focused)
		if endpoint["state_directory"] != config.ExitStateDir(id) || !reflect.DeepEqual(focused.Exits, config.Exits) {
			t.Fatal("Global preference overrode explicit focus or removed migration inventory")
		}
		ping, err := savedExitPingConfig(config, id)
		if err != nil {
			t.Fatal(err)
		}
		endpoint = focusedPreflightEndpoint(t, ping)
		if endpoint["state_directory"] != config.ExitStateDir(id) || endpoint["exit_node"] != "" || ping.Tailscale.AuthKeyEnv != "" {
			t.Fatal("Global preference overrode or unsanitized saved ping")
		}
	}
}
