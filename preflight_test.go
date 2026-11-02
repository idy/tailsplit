package tailsplit

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPreflightHasNoListenersOrTUNEvenWhenConfigured(t *testing.T) {
	config, err := loadTestConfig(t, `
state_dir: ./state
tailscale:
  exit_node: 100.64.0.10
  tailnet: original@example.com
tun:
  enabled: true
`)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compilePreflight(config)
	if err != nil {
		t.Fatal(err)
	}
	var native map[string]any
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		t.Fatal(err)
	}
	if _, exists := native["inbounds"]; exists {
		t.Fatal("preflight can start listeners or capture system traffic")
	}
	if native["endpoints"].([]any)[0].(map[string]any)["system_interface"] != false {
		t.Fatal("preflight can create a Tailscale system interface")
	}
	if !config.TUN.Enabled {
		t.Fatal("preflight changed the user's selected TUN mode")
	}
	config.Tailscale.ExitNode = ""
	discovery, err := compilePreflight(config)
	if err != nil {
		t.Fatal("device discovery needs a preconfigured exit:", err)
	}
	if TailscaleEnabled {
		if err := Check(discovery); err != nil {
			t.Fatal("exit discovery is not a valid listener-free core:", err)
		}
		if err := Check(compiled); err != nil {
			t.Fatal("preflight is not a valid embedded core config:", err)
		}
		engine, err := Open(context.Background(), config, compiled)
		if err != nil {
			t.Fatal(err)
		}
		defer engine.Close()
		if engine.hasTUN || engine.closeDNS != nil {
			t.Fatal("the preflight engine can install a system DNS override")
		}
	}
}

func TestExitCheckCannotEnableTUNInTheWrongTailnet(t *testing.T) {
	tests := []struct {
		name         string
		expected     string
		status       Status
		ready        bool
		wrongNetwork bool
	}{
		{"wrong personal network", "original@example.com", Status{BackendState: "Running", NetworkName: "personal@example.com", ExitVisible: true, ExitOnline: true}, false, true},
		{"missing exit", "original@example.com", Status{BackendState: "Running", NetworkName: "original@example.com"}, false, false},
		{"offline exit", "original@example.com", Status{BackendState: "Running", NetworkName: "original@example.com", ExitVisible: true}, false, false},
		{"network not received", "original@example.com", Status{BackendState: "Running", ExitVisible: true, ExitOnline: true}, false, false},
		{"correct network", "original@example.com", Status{BackendState: "Running", NetworkName: "original@example.com", ExitVisible: true, ExitOnline: true}, true, false},
		{"correct suffix", "tail-example.ts.net", Status{BackendState: "Running", NetworkName: "original@example.com", MagicDNSSuffix: "tail-example.ts.net", ExitVisible: true, ExitOnline: true}, true, false},
		{"no guard still needs exit", "", Status{BackendState: "Running"}, false, false},
		{"no guard ready", "", Status{BackendState: "Running", ExitVisible: true, ExitOnline: true}, true, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ready, err := preflightReady(test.expected, test.status)
			if ready != test.ready || (err != nil) != test.wrongNetwork {
				t.Fatalf("ready=%v error=%v", ready, err)
			}
		})
	}
}

func TestLoginCompletesIndependentlyOfExitAvailability(t *testing.T) {
	for _, status := range []Status{
		{BackendState: "Running", NetworkName: "original@example.com"},
		{BackendState: "Running", NetworkName: "original@example.com", ExitVisible: true},
	} {
		ready, err := authenticationReady("original@example.com", status)
		if err != nil || !ready {
			t.Fatal("device login incorrectly required a visible, online exit:", ready, err)
		}
		if tunReady, err := preflightReady("original@example.com", status); err != nil || tunReady {
			t.Fatal("TUN preflight stopped enforcing an online approved exit")
		}
	}
	if ready, err := authenticationReady("original@example.com",
		Status{BackendState: "Running", NetworkName: "personal@example.com"}); ready || err == nil {
		t.Fatal("login-only startup accepted the wrong Tailnet")
	}
}
