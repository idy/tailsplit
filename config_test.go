package tailsplit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadTestConfig(t *testing.T, text string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestConfigRejectsSilentlyUnsafeInput(t *testing.T) {
	for name, text := range map[string]string{
		"unknown root":            "versoin: 1\n",
		"unknown nested":          "dns:\n  fack_ip: true\n",
		"multiple docs":           "version: 1\n---\nversion: 1\n",
		"duplicate":               "version: 1\nversion: 2\n",
		"public proxy":            "listen: 0.0.0.0:17890\n",
		"public dns":              "dns:\n  listen: 0.0.0.0:15353\n",
		"dns loop":                "dns:\n  direct: 172.31.255.2\n",
		"dns IPv6 loop":           "dns:\n  direct: fd74:6169:6c73::2\n",
		"fake resolver":           "dns:\n  direct: 198.18.0.1\n",
		"magic resolver":          "dns:\n  direct: 100.100.100.100\n",
		"no endpoint":             "rules:\n  proxy_domains: [example.com]\n",
		"global without endpoint": "rules:\n  mode: global\n",
		"invalid routing mode":    "rules:\n  mode: everything\n",
		"bad hostname":            "tailscale:\n  hostname: 'bad name'\n",
		"raw auth":                "tailscale:\n  auth_key: secret\n",
		"default range":           "rules:\n  direct_ips: [0.0.0.0/0]\n",
		"host CIDR":               "rules:\n  direct_ips: [192.168.1.5/24]\n",
		"fake overlap":            "rules:\n  direct_ips: [198.18.0.0/16]\n",
		"URL domain":              "rules:\n  direct_domains: [https://example.com/path]\n",
		"IP domain":               "rules:\n  direct_domains: [8.8.8.8]\n",
		"single label":            "rules:\n  direct_domains: [com]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadTestConfig(t, text); err == nil {
				t.Fatalf("accepted unsafe configuration: %s", name)
			}
		})
	}
}

func TestDNSListenIsNotConfigurable(t *testing.T) {
	for _, capture := range []bool{false, true} {
		for _, address := range []string{
			"127.0.0.1:15353", "127.0.0.1:53", "'[::1]:15353'", "0.0.0.0:53",
			"172.31.255.2:53", "'[fd74:6169:6c73::2]:53'", "''", "", "null",
		} {
			t.Run(fmt.Sprintf("capture=%t/listen=%s", capture, address), func(t *testing.T) {
				_, err := loadTestConfig(t, fmt.Sprintf("dns:\n  listen: %s\ntun:\n  enabled: %t\n", address, capture))
				if err == nil || !strings.Contains(err.Error(), "field listen not found") {
					t.Fatalf("dns.listen must be rejected as an unsupported option: %v", err)
				}
			})
		}
	}
}

func TestDeviceNamesDefaultToHostAndPreserveCustomNames(t *testing.T) {
	for host, want := range map[string]string{
		"Idy-MacBook.local": "tailsplit-idy-macbook",
		"Mac Book.local":    "tailsplit-mac-book",
		"":                  "tailsplit-device",
	} {
		if got := defaultDeviceHostname(host); got != want {
			t.Fatalf("hostname %q: got %q, want %q", host, got, want)
		}
	}
	if got := defaultDeviceHostname(strings.Repeat("a", 100)); len(got) > 63 {
		t.Fatal("derived device name exceeds a DNS label")
	}
	if got, err := DeviceHostname(" My-Mac "); err != nil || got != "my-mac" {
		t.Fatal("custom device name was not retained:", got, err)
	}
	for _, invalid := range []string{"-mac", "mac-", "a.b", "my mac", strings.Repeat("a", 64), "mac\nother"} {
		if _, err := DeviceHostname(invalid); err == nil {
			t.Fatalf("invalid device name accepted: %q", invalid)
		}
	}
	config, err := loadTestConfig(t, "tailscale: {}\n")
	if err != nil {
		t.Fatal("login/picker configuration requires an exit before discovery:", err)
	}
	want, _ := DeviceHostname("")
	if config.Tailscale.Hostname != want || config.App.ConnectOnStart {
		t.Fatal("automatic hostname or explicit-start default was lost")
	}
	if _, err := Compile(config); err == nil {
		t.Fatal("a proxy can start without an explicitly selected exit")
	}
	if _, err := compilePreflight(config); err != nil {
		t.Fatal("listener-free discovery cannot run before choosing an exit:", err)
	}
}

func TestConfigNormalizesDomainsAndRelativePaths(t *testing.T) {
	config, err := loadTestConfig(t, `
state_dir: ./state
rules:
  direct_domains: ["*.EXAMPLE.com.", "bücher.example"]
`)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(config.StateDir) || filepath.Base(config.StateDir) != "state" {
		t.Fatalf("state path was not resolved relative to config: %s", config.StateDir)
	}
	if config.Rules.DirectDomains[0] != "example.com" || config.Rules.DirectDomains[1] != "xn--bcher-kva.example" {
		t.Fatalf("domain normalization: %v", config.Rules.DirectDomains)
	}
}

func TestExampleCompilesWithLocalRulesWithoutCredentialsOrStateCreation(t *testing.T) {
	state := filepath.Join(t.TempDir(), "must-not-exist")
	text := strings.Replace(string(ExampleYAML), "~/.local/share/letsolo/tailsplit", state, 1)
	text = strings.Replace(text, "  # auth_key_env: TAILSPLIT_AUTH_KEY", "  auth_key_env: TAILSPLIT_AUTH_KEY", 1)
	t.Setenv("TAILSPLIT_AUTH_KEY", "credential-must-not-appear-in-render")
	config, err := loadTestConfig(t, text)
	if err != nil {
		t.Fatal(err)
	}
	// Compile local rules offline without creating device state. Remote sources
	// have separate first-download and persisted-cache coverage in rules_test.go.
	config.Rules.GFWList = filepath.Join(t.TempDir(), "local-rules.txt")
	if err := os.WriteFile(config.Rules.GFWList, []byte("[AutoProxy 0.2.9]\n||fixture.example\n@@||safe.fixture.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(compiled.JSON, []byte("credential-must-not-appear")) || bytes.Contains(compiled.JSON, []byte(`"auth_key"`)) {
		t.Fatal("render exposed an authentication key")
	}
	if compiled.GFWList == nil || compiled.GFWList.Proxy != 1 || compiled.GFWList.Direct != 1 {
		t.Fatalf("local projection not loaded: %+v", compiled.GFWList)
	}
	var native map[string]any
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		t.Fatal(err)
	}
	endpoint := native["endpoints"].([]any)[0].(map[string]any)
	if endpoint["taildrop_directory"] != filepath.Join(config.StateDir, "taildrop") {
		t.Fatal("Tailscale file state can escape into the app's launch working directory")
	}
	again, err := Compile(config)
	if err != nil || !bytes.Equal(compiled.JSON, again.JSON) {
		t.Fatal("render is not deterministic")
	}
	if TailscaleEnabled {
		if err := Check(compiled); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("check/render created a persistent state directory")
	}
}

func TestMagicDNSAndTUNNativeOptionsValidate(t *testing.T) {
	if !TailscaleEnabled {
		t.Skip("Tailscale validation requires make test / -tags with_gvisor")
	}
	config, err := loadTestConfig(t, `
state_dir: ./state
tailscale:
  exit_node: 100.64.0.10
  domains: [gateway.example.com]
dns:
  magic_domains: [tail12345.ts.net]
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
	if err := Check(compiled); err != nil {
		t.Fatal(err)
	}
}
