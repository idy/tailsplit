package configweb

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

func TestExitsAndRuleGroupsPatchAsStructuredLists(t *testing.T) {
	server, path, _ := testServer(t)
	original, revision, err := tray.ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	// Automatic labels include both the node and network, including UTF-8 names.
	name := strings.Repeat("香港出口", 8) + "@office.ts.net"
	changes := map[string]any{
		"tailscale.exit_node": "100.64.0.10",
		"exits":               []map[string]any{{"id": "office", "name": name, "auto_name": true, "exit_node": "100.101.0.11"}},
		"rules.routes":        []map[string]any{{"exit": "office", "domains": []string{"corp.example"}, "ips": []string{"203.0.113.0/24"}}},
		"rules.final":         "office",
	}
	status, body := request(t, server, "POST", "/api/config", map[string]any{"yaml": string(original), "revision": revision, "changes": changes}, nil)
	if status != http.StatusOK {
		t.Fatalf("save: %d %s", status, body)
	}
	config, err := tailsplit.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Exits) != 1 || config.Exits[0].ExitNode != "100.101.0.11" || config.Exits[0].Name != name || !config.Exits[0].AutoName ||
		len(config.Rules.Routes) != 1 || config.Rules.Routes[0].Exit != "office" || config.Rules.Final != "office" {
		t.Fatalf("structured lists were not written: %+v %+v", config.Exits, config.Rules)
	}
	focused, err := config.FocusExit("office")
	if err != nil {
		t.Fatal(err)
	}
	focused, err = tailsplit.Normalize(focused, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if focused.Tailscale.Name != name {
		t.Fatalf("automatic label lost during focused login: %q", focused.Tailscale.Name)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "ExitNode") || !strings.Contains(string(data), "exit_node: 100.101.0.11") {
		t.Fatalf("lists must use YAML field names:\n%s", data)
	}
	// Unknown keys inside a list item are rejected rather than silently dropped.
	_, revision, _ = tray.ReadConfigDocument(path)
	status, _ = request(t, server, "POST", "/api/preview", map[string]any{"yaml": string(data), "revision": revision,
		"changes": map[string]any{"exits": []map[string]any{{"id": "office", "state_directory": "/etc"}}}}, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("unknown exit field accepted: %d", status)
	}
}

func TestExitActionsAcceptOnlyConfiguredExits(t *testing.T) {
	server, path, _ := testServer(t)
	original, revision, _ := tray.ReadConfigDocument(path)
	if status, body := request(t, server, "POST", "/api/config", map[string]any{"yaml": string(original), "revision": revision,
		"changes": map[string]any{"tailscale.exit_node": "100.64.0.10", "exits": []map[string]any{{"id": "office", "exit_node": ""}}}}, nil); status != http.StatusOK {
		t.Fatalf("save: %d %s", status, body)
	}
	var calls []string
	server.options.Action = func(_ context.Context, action string) error { calls = append(calls, action); return nil }
	for _, test := range []struct {
		body map[string]string
		want int
	}{
		{map[string]string{"action": "login-exit", "exit": "office"}, http.StatusOK},
		{map[string]string{"action": "logout-exit", "exit": "office"}, http.StatusOK},
		{map[string]string{"action": "login-exit", "exit": "../escape"}, http.StatusBadRequest},
		{map[string]string{"action": "login-exit", "exit": "main"}, http.StatusBadRequest},
		{map[string]string{"action": "login-exit"}, http.StatusBadRequest},
		{map[string]string{"action": "connect", "exit": "office"}, http.StatusBadRequest},
	} {
		if status, _ := request(t, server, "POST", "/api/action", test.body, nil); status != test.want {
			t.Fatalf("%v: status %d, want %d", test.body, status, test.want)
		}
	}
	if strings.Join(calls, ",") != "login-exit:office,logout-exit:office" {
		t.Fatalf("controller calls: %v", calls)
	}
}

func TestExitPingUsesThatDevicesNodes(t *testing.T) {
	server, _, _ := testServer(t)
	server.options.Snapshot = func() tray.Snapshot {
		return tray.Snapshot{PingAvailable: true, BackendState: "Running", ExitNodes: []tailsplit.ExitNode{{ID: "100.64.0.10", Online: true}},
			Exits: map[string]tray.ExitSnapshot{"office": {BackendState: "Running", PingAvailable: true,
				ExitNodes: []tailsplit.ExitNode{{ID: "100.101.0.11", Online: true}}}}}
	}
	var targets []string
	server.options.PingExit = func(_ context.Context, target string) (tailsplit.ExitPingResult, error) {
		targets = append(targets, target)
		_, id := tailsplit.SplitPingTarget(target)
		return tailsplit.ExitPingResult{ID: id, LatencyMS: 5, Direct: true}, nil
	}
	if status, body := request(t, server, "POST", "/api/exit-ping", map[string]string{"id": "100.101.0.11", "exit": "office"}, nil); status != http.StatusOK {
		t.Fatalf("extra ping: %d %s", status, body)
	}
	// A node from another Tailnet cannot be pinged through this device.
	if status, _ := request(t, server, "POST", "/api/exit-ping", map[string]string{"id": "100.64.0.10", "exit": "office"}, nil); status != http.StatusBadRequest {
		t.Fatalf("cross-device ping accepted: %d", status)
	}
	if status, _ := request(t, server, "POST", "/api/exit-ping", map[string]string{"id": "100.64.0.10"}, nil); status != http.StatusOK {
		t.Fatal("primary ping failed")
	}
	if strings.Join(targets, ",") != "office|100.101.0.11,100.64.0.10" {
		t.Fatalf("ping targets: %v", targets)
	}
	status, body := request(t, server, "GET", "/api/state", nil, nil)
	var state map[string]any
	_ = json.Unmarshal(body, &state)
	if status != http.StatusOK || state["exits"].(map[string]any)["office"] == nil || strings.Contains(string(body), "auth") && strings.Contains(string(body), "login.tailscale") {
		t.Fatalf("state: %d %s", status, body)
	}
}

func TestGlobalExitPatchValidatesSelectedDeviceAndRetainsSplitDraft(t *testing.T) {
	for _, test := range []struct {
		name, mainNode, extraNode string
	}{
		{"pending extra can save before login", "100.64.0.10", ""},
		{"chosen extra does not require primary", "", "100.101.0.11"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, path, _ := testServer(t)
			original, revision, err := tray.ReadConfigDocument(path)
			if err != nil {
				t.Fatal(err)
			}
			changes := map[string]any{
				"tailscale.exit_node": test.mainNode,
				"exits":               []map[string]any{{"id": "office", "exit_node": test.extraNode}, {"id": "spare", "exit_node": ""}},
				"rules.mode":          "global", "rules.global_exit": "office", "tun.enabled": true,
				"rules.routes": []map[string]any{{"exit": "spare", "domains": []string{"corp.example"}}},
				"rules.final":  "spare",
			}
			status, body := request(t, server, "POST", "/api/config", map[string]any{
				"yaml": string(original) + "state_dir: ./state\n", "revision": revision, "changes": changes,
			}, nil)
			if status != http.StatusOK {
				t.Fatalf("save selected Global device: %d %s", status, body)
			}
			config, err := tailsplit.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if config.Rules.GlobalExit != "office" || config.Rules.Mode != "global" || !config.TUN.Enabled ||
				config.Rules.Final != "spare" || len(config.Rules.Routes) != 1 || len(config.Rules.DirectDomains) != 1 || len(config.Exits) != 2 {
				t.Fatalf("Global save discarded Split configuration: %+v", config)
			}
			// The existing revision check still protects this new field.
			status, _ = request(t, server, "POST", "/api/config", map[string]any{
				"yaml": string(original), "revision": revision, "changes": map[string]any{"rules.global_exit": "main"},
			}, nil)
			if status != http.StatusConflict {
				t.Fatalf("stale Global selection overwrote saved configuration: %d", status)
			}
			current, revision, _ := tray.ReadConfigDocument(path)
			status, _ = request(t, server, "POST", "/api/config", map[string]any{
				"yaml": string(current), "revision": revision, "changes": map[string]any{"rules.global_exit": "unknown"},
			}, nil)
			if status != http.StatusUnprocessableEntity {
				t.Fatalf("unknown Global device accepted: %d", status)
			}
		})
	}
}

func TestRemoveExitEndpointChecksRevisionAndReturnsSavedDocument(t *testing.T) {
	server, path, _ := testServer(t)
	fixture := "state_dir: ./state\ntailscale:\n  hostname: primary\n  exit_node: 100.64.0.10\nexits:\n  - id: office\n    exit_node: 100.101.0.11\nrules:\n  default_exit: main\n  global_exit: main\n  proxy_domains: [proxy.example]\n"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	server.options.RemoveExit = func(ctx context.Context, id, revision string) error {
		calls = append(calls, id)
		return tray.RemoveExitDocument(path, id, revision, func(string) error { return nil })
	}
	_, revision, _ := tray.ReadConfigDocument(path)
	for _, test := range []struct {
		body    map[string]string
		headers map[string]string
		want    int
	}{
		{map[string]string{"exit": "main", "revision": revision}, map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		{map[string]string{"exit": "main", "revision": "stale"}, nil, http.StatusConflict},
		{map[string]string{"exit": "../bad", "revision": revision}, nil, http.StatusBadRequest},
		{map[string]string{"exit": "main", "revision": revision, "extra": "unknown"}, nil, http.StatusBadRequest},
	} {
		if status, body := request(t, server, "POST", "/api/exit-remove", test.body, test.headers); status != test.want {
			t.Fatalf("remove validation status %d, want %d: %s", status, test.want, body)
		}
	}
	if len(calls) != 0 {
		t.Fatal("an unauthorized, stale or invalid request reached logout")
	}
	status, body := request(t, server, "POST", "/api/exit-remove", map[string]string{"exit": "main", "revision": revision}, nil)
	var doc document
	if status != http.StatusOK || json.Unmarshal(body, &doc) != nil || !doc.Valid || doc.Revision == "" || doc.Revision == revision {
		t.Fatalf("missing updated document: %d %s", status, body)
	}
	config, err := tailsplit.Load(path)
	if err != nil || config.Tailscale != nil || len(config.Exits) != 1 || config.Rules.DefaultExit != tailsplit.UnsetExit || config.Rules.GlobalExit != tailsplit.UnsetExit {
		t.Fatalf("primary removal lost or promoted remaining device: %+v %v", config, err)
	}
	status, body = request(t, server, "POST", "/api/exit-remove", map[string]string{"exit": "office", "revision": doc.Revision}, nil)
	if status != http.StatusOK || json.Unmarshal(body, &doc) != nil || !doc.Valid {
		t.Fatalf("last-device removal did not return a valid empty draft: %d %s", status, body)
	}
	config, err = tailsplit.Load(path)
	if err != nil || len(config.ExitIDs()) != 0 || config.Rules.DefaultExit != tailsplit.UnsetExit || len(config.Rules.ProxyDomains) != 1 {
		t.Fatalf("empty-device draft lost dormant policy: %+v %v", config, err)
	}
	if strings.Join(calls, ",") != "main,office" {
		t.Fatalf("wrong local device logout calls: %v", calls)
	}
}

func TestDefaultExitCanBePatchedBeforeNodeSelection(t *testing.T) {
	server, path, _ := testServer(t)
	original, revision, _ := tray.ReadConfigDocument(path)
	changes := map[string]any{
		"exits":              []map[string]any{{"id": "office", "exit_node": ""}},
		"rules.default_exit": "office", "rules.global_exit": "office",
		"rules.proxy_domains": []string{"proxy.example"},
	}
	status, body := request(t, server, "POST", "/api/config", map[string]any{
		"yaml": string(original) + "state_dir: ./state\n", "revision": revision, "changes": changes,
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("first extra could not save before login: %d %s", status, body)
	}
	config, err := tailsplit.Load(path)
	if err != nil || config.Tailscale != nil || config.Rules.DefaultExit != "office" {
		t.Fatalf("first extra draft invalid: %+v %v", config, err)
	}
}
