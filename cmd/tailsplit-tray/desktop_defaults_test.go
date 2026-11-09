package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/idy/tailsplit"
)

func TestDesktopStartupWaitsForConfiguredGateway(t *testing.T) {
	ready := "tailscale:\n  exit_node: 100.64.0.10\n"
	for _, test := range []struct {
		name, config    string
		auto, noConnect bool
		want            string
	}{
		{"fresh profile", string(tailsplit.DefaultYAML), true, false, ""},
		{"direct-only auto-connect", "rules:\n  final: direct\n", true, false, "connect"},
		{"direct-only empty group", "rules:\n  final: direct\n  routes:\n    - exit: removed\n", true, false, "connect"},
		{"direct-only auto-connect off", "rules:\n  final: direct\n", false, false, ""},
		{"direct-only update override", "rules:\n  final: direct\n", true, true, ""},
		{"auto-connect off", ready, false, false, "refresh-exits"},
		{"ready auto-connect", ready, true, false, "connect"},
		{"update override", ready, true, true, "refresh-exits"},
		{"no selected node", "tailscale: {}\n", true, false, "refresh-exits"},
		{"no default", ready + "rules:\n  default_exit: \"-\"\n", true, false, "refresh-exits"},
		{"global no default", ready + "rules:\n  mode: global\n  global_exit: \"-\"\ntun:\n  enabled: true\n", true, false, "refresh-exits"},
		{"ready extra without primary", "tailscale: null\nexits:\n  - id: office\n    exit_node: 100.64.0.10\nrules:\n  default_exit: office\n", true, false, "connect"},
		{"invalid config", "rules: [broken", true, false, "refresh-exits"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(test.config), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := desktopStartupAction(path, test.auto, test.noConnect); got != test.want {
				t.Fatalf("startup action = %q, want %q", got, test.want)
			}
		})
	}
}
