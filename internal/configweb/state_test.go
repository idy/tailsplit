package configweb

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

func TestStateExposesOnlySafeConnectionMetadata(t *testing.T) {
	for _, logsAvailable := range []bool{false, true} {
		name := "without logs"
		if logsAvailable {
			name = "with logs"
		}
		t.Run(name, func(t *testing.T) {
			server, path, _ := testServer(t)
			const authKey = "tskey-auth-state-api-secret"
			const authEnv = "STATE_API_PRIVATE_AUTH_KEY"
			t.Setenv(authEnv, authKey)
			if err := os.WriteFile(path, []byte(fixtureConfig+"tailscale:\n  auth_key_env: "+authEnv+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, revision, err := tray.ReadConfigDocument(path)
			if err != nil {
				t.Fatal(err)
			}
			server.options.Snapshot = func() tray.Snapshot {
				return tray.Snapshot{
					State: "running", Active: true, Mode: "tun", RoutingMode: "global",
					BackendState: "Running", Hostname: "preview-laptop",
					ExitNode: "preview-exit", NetworkName: "preview-tailnet.example",
					LoginURL:  "https://login.tailscale.com/a/" + authKey,
					ExitNodes: []tailsplit.ExitNode{{ID: "100.64.0.10", Name: "preview-exit", Online: true, Selected: true}},
				}
			}
			if logsAvailable {
				server.options.Logs = func() (string, error) {
					t.Error("reading state must not read logs")
					return "private log contents", nil
				}
			}
			status, body := request(t, server, http.MethodGet, "/api/state", nil, nil)
			var state map[string]any
			if err := json.Unmarshal(body, &state); err != nil || status != http.StatusOK {
				t.Fatalf("state: status=%d body=%s err=%v", status, body, err)
			}
			want := map[string]any{
				"state": "running", "active": true, "mode": "tun", "routing_mode": "global",
				"backend_state": "Running", "hostname": "preview-laptop", "phase": "",
				"exit": "preview-exit", "network": "preview-tailnet.example", "magic_dns_suffix": "",
				"error": "", "error_code": "", "added_exit": "", "added_revision": "", "connect_request": "", "revision": revision, "preview": false,
				"desktop": false, "startup": false, "logs_available": logsAvailable, "ping_available": false,
				"exit_nodes": []any{map[string]any{
					"id": "100.64.0.10", "name": "preview-exit", "online": true, "selected": true,
				}},
				"exits": map[string]any{}, "focus": "", "traffic_exit": "", "traffic": nil,
				"open_panel_available": false, "quit_available": false,
			}
			if !reflect.DeepEqual(state, want) {
				t.Fatalf("unexpected public state: got=%v want=%v", state, want)
			}
			for _, private := range []string{path, server.token, authEnv, authKey, "LoginURL", "login_url", "auth_url", "private log contents"} {
				if bytes.Contains(body, []byte(private)) {
					t.Fatalf("state API leaked %q", private)
				}
			}
		})
	}
}

func TestStateReportsIsolationAndOmissionSeparatelyFromSDKAuthentication(t *testing.T) {
	server, _, _ := testServer(t)
	server.options.Snapshot = func() tray.Snapshot {
		return tray.Snapshot{State: "running", Phase: "isolated", BackendState: "Running", NetworkName: "actual.example",
			Exits: map[string]tray.ExitSnapshot{"office": {Phase: "omitted"}, "lab": {Phase: "isolated", BackendState: "NeedsLogin"}}}
	}
	status, body := request(t, server, http.MethodGet, "/api/state", nil, nil)
	var state struct {
		Phase         string `json:"phase"`
		BackendState  string `json:"backend_state"`
		Network       string `json:"network"`
		PingAvailable bool   `json:"ping_available"`
		Exits         map[string]struct {
			Phase         string `json:"phase"`
			BackendState  string `json:"backend_state"`
			PingAvailable bool   `json:"ping_available"`
		} `json:"exits"`
	}
	if err := json.Unmarshal(body, &state); err != nil || status != http.StatusOK {
		t.Fatalf("isolation response: %d %s, %v", status, body, err)
	}
	if state.Phase != "isolated" || state.BackendState != "Running" || state.Network != "actual.example" || state.PingAvailable ||
		state.Exits["office"].Phase != "omitted" || state.Exits["office"].BackendState != "" || state.Exits["office"].PingAvailable ||
		state.Exits["lab"].Phase != "isolated" || state.Exits["lab"].BackendState != "NeedsLogin" || state.Exits["lab"].PingAvailable {
		t.Fatalf("API conflated session isolation and SDK identity: %s", body)
	}
}

func TestStateWithoutSnapshotOrLogs(t *testing.T) {
	server, _, _ := testServer(t)
	server.options.Snapshot = nil
	status, body := request(t, server, http.MethodGet, "/api/state", nil, nil)
	var state map[string]any
	if err := json.Unmarshal(body, &state); err != nil || status != http.StatusOK {
		t.Fatalf("state: status=%d body=%s err=%v", status, body, err)
	}
	for _, field := range []string{"hostname", "backend_state", "routing_mode"} {
		if value, exists := state[field]; !exists || value != "" {
			t.Fatalf("%s: got=%v exists=%t", field, value, exists)
		}
	}
	if state["logs_available"] != false {
		t.Fatalf("logs available without a callback: %s", body)
	}
}

func TestStateRequiresCapabilityAndSameOrigin(t *testing.T) {
	server, _, _ := testServer(t)
	for _, test := range []struct {
		name    string
		headers map[string]string
		status  int
	}{
		{"missing capability", map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		{"invalid capability", map[string]string{"Authorization": "Bearer invalid"}, http.StatusUnauthorized},
		{"foreign origin", map[string]string{"Origin": "https://other.example"}, http.StatusForbidden},
		{"rebound host", map[string]string{"Host": "other.example"}, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, _ := request(t, server, http.MethodGet, "/api/state", nil, test.headers)
			if status != test.status {
				t.Fatalf("status=%d want=%d", status, test.status)
			}
		})
	}
}
