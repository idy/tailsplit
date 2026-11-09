package tailsplit

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestExitMagicDNSMetadataRoundTripKeepsNetworkIdentity(t *testing.T) {
	config, err := loadTestConfig(t, `
state_dir: ./state
tailscale:
  hostname: laptop
  tailnet: primary.example
exits:
  - id: office
    name: gateway@example.com
    auto_name: true
    exit_node: 100.101.0.11
    tailnet: example.com
    magic_dns_suffix: TAIL123.TS.NET.
`)
	if err != nil {
		t.Fatal(err)
	}
	exit := config.Exits[0]
	if exit.MagicDNSSuffix != "tail123.ts.net" || exit.Tailnet != "example.com" || exit.Name != "gateway@example.com" || !exit.AutoName {
		t.Fatalf("DNS metadata changed the saved network identity: %+v", exit)
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "magic_dns_suffix: tail123.ts.net") {
		t.Fatalf("DNS suffix was not persisted: %s", data)
	}
	again, err := loadTestConfig(t, string(data))
	if err != nil || again.Exits[0] != exit {
		t.Fatalf("known-fields decoding lost exit identity: %+v, %v", again.Exits, err)
	}
	focused, err := again.FocusExit("office")
	if err == nil {
		focused, err = Normalize(focused, t.TempDir())
	}
	if err != nil {
		t.Fatal(err)
	}
	if focused.Tailscale.Tailnet != "example.com" || focused.Tailscale.Name != exit.Name || focused.Exits[0] != exit {
		t.Fatalf("focus changed the authentication guard or inventory: %+v", focused)
	}
	if focused.ExitStateDir("office") != filepath.Join(config.StateDir, "exits", "office", "tailscale") ||
		focused.ExitStateDir(MainExit) != filepath.Join(config.StateDir, "tailscale") {
		t.Fatal("DNS metadata changed a device state path")
	}
}

func TestExitMagicDNSSuffixValidation(t *testing.T) {
	for _, test := range []struct{ value, want string }{
		{"", ""},
		{"tail123.ts.net", "tail123.ts.net"},
		{"TAIL123.TS.NET.", "tail123.ts.net"},
		{"bücher.example", "xn--bcher-kva.example"},
	} {
		t.Run("valid/"+test.value, func(t *testing.T) {
			config, err := Normalize(Config{Exits: []ExitConfig{{ID: "office", MagicDNSSuffix: test.value}}}, t.TempDir())
			if err != nil || config.Exits[0].MagicDNSSuffix != test.want {
				t.Fatalf("suffix %q: %+v, %v", test.value, config.Exits, err)
			}
			if test.value == "" {
				data, err := yaml.Marshal(config.Exits)
				if err != nil || strings.Contains(string(data), "magic_dns_suffix") {
					t.Fatalf("legacy omitted metadata changed: %s, %v", data, err)
				}
			}
		})
	}
	invalid := []string{"example", "https://example.com", "8.8.8.8", "bad name.ts.net", "tail..ts.net", "-tail.ts.net", "tail_.ts.net", strings.Repeat("a", 64) + ".ts.net", strings.Repeat("a.", 127) + "ts.net"}
	for control := rune(0); control <= 0x1f; control++ {
		invalid = append(invalid, "tail"+string(control)+"123.ts.net")
	}
	invalid = append(invalid, "tail123.ts.net\n", "tail\x7f123.ts.net", "tail\u0085123.ts.net")
	for index, value := range invalid {
		t.Run(fmt.Sprintf("invalid/%d", index), func(t *testing.T) {
			_, err := Normalize(Config{Exits: []ExitConfig{{ID: "office", MagicDNSSuffix: value}}}, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), "exits[0].magic_dns_suffix") {
				t.Fatalf("invalid suffix %q was accepted: %v", value, err)
			}
		})
	}
}

func TestGlobalExitDNSMetadataAndLegacyFallback(t *testing.T) {
	for _, test := range []struct{ name, tailnet, suffix, want string }{
		{"distinct DNS metadata", "example.com", "tail123.ts.net", "tail123.ts.net"},
		{"metadata takes precedence", "old.ts.net", "tail123.ts.net", "tail123.ts.net"},
		{"legacy DNS suffix", "LEGACY.ts.net", "", "legacy.ts.net"},
		{"display domain is not DNS metadata", "example.com", "", ""},
		{"omitted metadata", "", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := Normalize(Config{
				Tailscale: &TailscaleConfig{Hostname: "primary"},
				Exits:     []ExitConfig{{ID: "office", Name: "gateway@" + test.tailnet, Tailnet: test.tailnet, MagicDNSSuffix: test.suffix}},
				DNS:       DNSConfig{MagicDomains: []string{"primary.ts.net"}},
				Rules:     RulesConfig{Mode: RoutingGlobal, GlobalExit: "office"},
			}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			focused, err := config.trafficConfig()
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" && len(focused.DNS.MagicDomains) != 0 || test.want != "" &&
				(len(focused.DNS.MagicDomains) != 1 || focused.DNS.MagicDomains[0] != test.want) {
				t.Fatalf("focused DNS: got %v, want %q", focused.DNS.MagicDomains, test.want)
			}
			if focused.Tailscale.Tailnet != test.tailnet || focused.Tailscale.Name != config.Exits[0].Name || focused.Focus != "office" {
				t.Fatalf("DNS selection changed network authentication or focus: %+v", focused)
			}
		})
	}
}

func TestAdditionalExitIDsRejectWindowsDeviceBasenames(t *testing.T) {
	reserved := []string{"con", "prn", "aux", "nul"}
	for index := 1; index <= 9; index++ {
		reserved = append(reserved, fmt.Sprintf("com%d", index), fmt.Sprintf("lpt%d", index))
	}
	for _, id := range reserved {
		t.Run("reserved/"+id, func(t *testing.T) {
			_, err := loadTestConfig(t, fmt.Sprintf("exits:\n  - id: %q\n", id))
			if err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Fatalf("Windows device basename %q was accepted: %v", id, err)
			}
		})
	}
	for _, id := range []string{"console", "con-office", "prn-1", "aux-gateway", "nul-2", "com0", "com10", "com1-office", "lpt0", "lpt10", "lpt9-office", "office", "exit-1"} {
		t.Run("valid/"+id, func(t *testing.T) {
			config, err := loadTestConfig(t, fmt.Sprintf("state_dir: ./state\nexits:\n  - id: %q\n", id))
			if err != nil || config.Exits[0].ID != id || config.ExitStateDir(id) != filepath.Join(config.StateDir, "exits", id, "tailscale") {
				t.Fatalf("ordinary ID %q lost its stable state path: %+v, %v", id, config.Exits, err)
			}
		})
	}
}

func TestAdditionalExitIDsReserveFinalRouteKeywords(t *testing.T) {
	for _, id := range []string{MainExit, "direct", "proxy", " direct ", " proxy "} {
		t.Run("reserved/"+id, func(t *testing.T) {
			_, err := loadTestConfig(t, fmt.Sprintf("exits:\n  - id: %q\n", id))
			if err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Fatalf("reserved exit ID %q was accepted: %v", id, err)
			}
		})
	}
	for _, id := range []string{"office", "direct-gateway", "proxy-2", "a", strings.Repeat("x", 32)} {
		t.Run("valid/"+id, func(t *testing.T) {
			config, err := loadTestConfig(t, fmt.Sprintf("exits:\n  - id: %q\nrules:\n  final: %s\n", id, id))
			if err != nil || config.Exits[0].ID != id || config.Rules.Final != id {
				t.Fatalf("valid exit ID %q was rejected: %+v, %v", id, config.Exits, err)
			}
		})
	}
}
