package tray

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
)

func TestPreferencesPreserveRulesCommentsAndUnrelatedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `# My routing policy
tailscale:
  hostname: ""
  exit_node: 100.64.0.1 # Chosen exit
  tailnet: example.ts.net
rules:
  proxy_domains: [example.com]
  direct_ips: [192.168.0.0/16]
dns:
  direct: 223.5.5.5
tun:
  enabled: true
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetDeviceName(path, "work-mac"); err != nil {
		t.Fatal(err)
	}
	if err := SetExitNode(path, "100.64.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := SetConnectOnStart(path, true); err != nil {
		t.Fatal(err)
	}
	config, err := tailsplit.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Tailscale.Hostname != "work-mac" || config.Tailscale.ExitNode != "100.64.0.2" ||
		config.Tailscale.Tailnet != "example.ts.net" || !config.TUN.Enabled ||
		!config.App.ConnectOnStart || config.Rules.ProxyDomains[0] != "example.com" ||
		config.Rules.DirectIPs[0] != "192.168.0.0/16" {
		t.Fatalf("editing desktop preferences changed routing policy: %+v", config)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "# My routing policy") || !strings.Contains(string(data), "# Chosen exit") {
		t.Fatal("preference edit discarded the user's comments")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("preference edit exposed private configuration")
	}
	if err := SetDeviceName(path, ""); err != nil {
		t.Fatal(err)
	}
	if err := SetSystemDNS(path); err != nil {
		t.Fatal(err)
	}
	preferences, err := ReadPreferences(path)
	if err != nil || preferences.DeviceName != "" || !preferences.ConnectOnStart ||
		preferences.ExitNode != "100.64.0.2" || preferences.DirectDNS != "system" {
		t.Fatal("automatic name or saved startup/exit preference was lost:", preferences, err)
	}
	before, _ := os.ReadFile(path)
	if err := SetDeviceName(path, "bad\nname"); err == nil {
		t.Fatal("accepted invalid device name")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed preference edit changed the file")
	}
}

func TestPreferencesDoNotReplaceMalformedPolicyOrFollowSymlinks(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	original := []byte("tailscale:\n  exit_node: 100.64.0.1\nunknown_policy: true\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetConnectOnStart(path, true); err == nil {
		t.Fatal("saved preferences into an invalid policy")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("invalid policy was modified")
	}
	link := filepath.Join(directory, "link.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := SetConnectOnStart(link, true); err == nil {
		t.Fatal("preference editor replaced a symlink")
	}
}

func TestGlobalPreferenceEnablesTUNAtomicallyAndPreservesSplitRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `# Keep my split policy
tailscale:
  exit_node: 100.64.0.1
rules:
  direct_domains: [example.com]
  final: direct
tun:
  enabled: false # Proxy-only until global mode is selected
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetRoutingMode(path, tailsplit.RoutingGlobal); err != nil {
		t.Fatal(err)
	}
	config, err := tailsplit.Load(path)
	if err != nil || config.Rules.Mode != tailsplit.RoutingGlobal || !config.TUN.Enabled {
		t.Fatal("global selection did not atomically enable TUN:", config, err)
	}
	if config.Rules.Final != "direct" || config.Rules.DirectDomains[0] != "example.com" {
		t.Fatal("global selection destroyed the saved split policy")
	}
	if err := SetRoutingMode(path, tailsplit.RoutingSplit); err != nil {
		t.Fatal(err)
	}
	preferences, err := ReadPreferences(path)
	if err != nil || preferences.RoutingMode != tailsplit.RoutingSplit {
		t.Fatal("cannot restore split mode:", preferences, err)
	}
	before, _ := os.ReadFile(path)
	if !strings.Contains(string(before), "# Keep my split policy") ||
		!strings.Contains(string(before), "# Proxy-only") {
		t.Fatal("changing mode lost comments")
	}
	if err := SetRoutingMode(path, "invalid"); err == nil {
		t.Fatal("invalid routing mode was accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("invalid mode damaged the saved configuration")
	}
}

func TestDebugLoggingPreferencePreservesPolicyAndRootComments(t *testing.T) {
	for _, level := range []string{"", "info", "warn", "debug", "trace"} {
		t.Run("level="+level, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			text := "# My configuration\nrules:\n  direct_domains: [example.com]\ndns:\n  direct_ipv6: false\n"
			if level != "" {
				text += "log_level: " + level + " # Logging preference\n"
			}
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			preferences, err := ReadPreferences(path)
			if err != nil || preferences.DebugLogging != (level == "debug" || level == "trace") {
				t.Fatal("log level did not match the menu preference:", preferences, err)
			}
			for _, enabled := range []bool{true, false} {
				if err := SetDebugLogging(path, enabled); err != nil {
					t.Fatal(err)
				}
				config, err := tailsplit.Load(path)
				wantLevel := "info"
				if enabled {
					wantLevel = "debug"
				}
				if err != nil || config.LogLevel != wantLevel {
					t.Fatal("log level was not saved at the configuration root:", config.LogLevel, err)
				}
				if len(config.Rules.DirectDomains) != 1 || config.Rules.DirectDomains[0] != "example.com" ||
					config.DNS.DirectIPv6 == nil || *config.DNS.DirectIPv6 {
					t.Fatal("changing logging modified the routing or DNS policy")
				}
				preferences, err = ReadPreferences(path)
				if err != nil || preferences.DebugLogging != enabled {
					t.Fatal("saved logging preference could not be read:", preferences, err)
				}
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Contains(data, []byte("# My configuration")) ||
					(level != "" && !bytes.Contains(data, []byte("# Logging preference"))) {
					t.Fatal("changing logging discarded configuration comments:", err)
				}
			}
		})
	}
}
