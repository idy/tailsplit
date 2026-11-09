package configweb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

func TestExitsAndRuleGroupsPatchAsStructuredLists(t *testing.T) {
	if !tailsplit.TailscaleEnabled {
		t.Skip("live exit save includes native Tailscale validation; use -tags with_gvisor")
	}
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
		"changes": map[string]any{"tailscale.exit_node": "", "exits": []map[string]any{{"id": "office", "exit_node": ""}}}}, nil); status != http.StatusOK {
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
			if test.extraNode != "" && !tailsplit.TailscaleEnabled {
				t.Skip("selected Global exit requires native validation; use -tags with_gvisor")
			}
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

func TestRemovedSplitPolicyCanAddReplacementWhileStopped(t *testing.T) {
	for name, policy := range map[string]string{
		"final": "  final: office # Retained final\n",
		"group": "  routes:\n    - exit: office # Retained group\n      domains: [private.example]\n",
		"both":  "  final: office # Retained final\n  routes:\n    - exit: office # Retained group\n      domains: [private.example]\n",
	} {
		t.Run(name, func(t *testing.T) {
			server, path, applied := testServer(t)
			original := "state_dir: ./state\ntailscale:\n  exit_node: 100.64.0.10\nexits:\n  - id: office\n    exit_node: 100.101.0.11\nrules:\n  default_exit: main\n  global_exit: main\n" + policy
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			server.options.RemoveExit = func(_ context.Context, id, revision string) error {
				return tray.RemoveExitDocument(path, id, revision, func(string) error { return nil })
			}
			_, revision, _ := tray.ReadConfigDocument(path)
			status, body := request(t, server, "POST", "/api/exit-remove", map[string]string{"exit": "office", "revision": revision}, nil)
			var doc document
			if status != http.StatusOK || json.Unmarshal(body, &doc) != nil || !doc.Valid {
				t.Fatalf("remove policy owner: %d %s", status, body)
			}
			for _, endpoint := range []string{"/api/validate", "/api/config"} {
				status, body = request(t, server, "POST", endpoint, map[string]any{
					"yaml": doc.YAML, "revision": doc.Revision,
					"changes": map[string]any{"exits": []map[string]any{{"id": "replacement", "exit_node": ""}}},
				}, nil)
				want := http.StatusOK
				if endpoint == "/api/validate" {
					want = http.StatusUnprocessableEntity
				}
				if status != want {
					t.Fatalf("add replacement to retained draft at %s: %d %s", endpoint, status, body)
				}
			}
			config, err := tailsplit.LoadDraft(path)
			if err != nil || applied.Load() != 1 || config.TrafficExitID() != "main" || len(config.Exits) != 1 || config.Exits[0].ID != "replacement" {
				t.Fatalf("replacement changed the selected remaining gateway: %+v, %v", config, err)
			}
			if name != "group" && config.Rules.Final != "office" || name != "final" && (len(config.Rules.Routes) != 1 || config.Rules.Routes[0].Exit != "office") {
				t.Fatalf("adding a replacement discarded unresolved policy: %+v", config.Rules)
			}
			if _, err := tailsplit.CompileContext(context.Background(), config); !errors.Is(err, tailsplit.ErrUnresolvedSplitExit) {
				t.Fatalf("editable draft became eligible for live traffic: %v", err)
			}
		})
	}
}

func TestActiveHelperRejectsUnresolvedSplitSaveBeforeApply(t *testing.T) {
	for name, policy := range map[string]string{
		"final": "  final: office\n",
		"group": "  routes:\n    - exit: office\n      domains: [private.example]\n",
	} {
		for _, mode := range []string{"tun", "proxy", "login"} {
			for _, pending := range []bool{false, true} {
				t.Run(name+"/"+mode+map[bool]string{false: "/ready", true: "/pending"}[pending], func(t *testing.T) {
					server, path, applied := testServer(t)
					original := "state_dir: ./state\ntailscale:\n  exit_node: 100.64.0.10\nrules:\n  mode: global\n  default_exit: main\n  global_exit: main\n" + policy + "tun:\n  enabled: true\n"
					if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
						t.Fatal(err)
					}
					server.options.Snapshot = func() tray.Snapshot { return tray.Snapshot{Active: true, Mode: mode} }
					_, revision, _ := tray.ReadConfigDocument(path)
					changes := map[string]any{"rules.mode": "split"}
					if pending {
						changes["tailscale.exit_node"] = ""
					}
					for _, endpoint := range []string{"/api/validate", "/api/config"} {
						status, body := request(t, server, "POST", endpoint, map[string]any{"yaml": original, "revision": revision, "changes": changes}, nil)
						current, currentRevision, _ := tray.ReadConfigDocument(path)
						if status != http.StatusUnprocessableEntity || applied.Load() != 0 || currentRevision != revision || string(current) != original {
							t.Fatalf("active unresolved save reached Apply or changed saved state: %d %s", status, body)
						}
					}
				})
			}
		}
	}
}

func TestStoppedDraftExceptionDoesNotBypassOtherRuntimeErrors(t *testing.T) {
	for name, extra := range map[string]string{
		"pending explicit owner": "exits:\n  - id: office\nrules:\n  final: office\n",
		"pending private owner":  "exits:\n  - id: office\n    tailnet: office.ts.net\n",
		"missing rule source":    "rules:\n  source: ./missing-rules.txt\n",
	} {
		t.Run(name, func(t *testing.T) {
			server, path, applied := testServer(t)
			original, revision, _ := tray.ReadConfigDocument(path)
			candidate := "state_dir: ./state\ntailscale:\n  exit_node: 100.64.0.10\n" + extra
			config, err := tray.ValidateConfigDocument(path, []byte(candidate))
			if err != nil {
				t.Fatal("fixture failed general validation:", err)
			}
			if _, err := tailsplit.CompileContext(context.Background(), config); err == nil || errors.Is(err, tailsplit.ErrUnresolvedSplitExit) {
				t.Fatalf("fixture must exercise a different runtime error: %v", err)
			}
			status, body := request(t, server, "POST", "/api/config", map[string]string{"yaml": candidate, "revision": revision}, nil)
			current, currentRevision, _ := tray.ReadConfigDocument(path)
			if status != http.StatusUnprocessableEntity || applied.Load() != 0 || currentRevision != revision || string(current) != string(original) {
				t.Fatalf("unrelated runtime error used the draft exception: %d %s", status, body)
			}
		})
	}
}

func TestStructuredListEditsPreserveCommentsAnchorsAndOmittedFields(t *testing.T) {
	const original = `state_dir: ./state
tailscale: null
exits: # Saved devices
  - &office
    id: office # Office identity
    name: &label 'Office' # Office label
  - id: lab # Lab identity
    name: *label # Shared label
rules:
  default_exit: office
  global_exit: office
  routes: # Saved groups
    - &first
      exit: office # First owner
      domains: &domains [first.example] # First matches
    - exit: office # Second owner
      domains: [second.example] # Second matches
    - <<: *first # Inherited group
      domains: [third.example] # Third matches
  proxy_domains: *domains # Retain old domains
`
	for _, operation := range []string{"add exit", "edit route", "remove route", "edit aliased field", "reorder aliases"} {
		t.Run(operation, func(t *testing.T) {
			server, path, applied := testServer(t)
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			status, body := request(t, server, "GET", "/api/config", nil, nil)
			var doc document
			if status != http.StatusOK || json.Unmarshal(body, &doc) != nil || !doc.Valid {
				t.Fatalf("fixture must be readable: %d %s", status, body)
			}
			exits := doc.Values["exits"].([]any)
			routes := doc.Values["rules"].(map[string]any)["routes"].([]any)
			changes := map[string]any{}
			wantDomains := []string{"first.example", "second.example", "third.example"}
			switch operation {
			case "add exit":
				changes["exits"] = append(exits, map[string]any{"id": "new", "auto_name": true, "exit_node": ""})
			case "edit route":
				routes[0].(map[string]any)["domains"] = []string{"changed.example"}
				changes["rules.routes"] = routes
				wantDomains[0] = "changed.example"
			case "remove route":
				changes["rules.routes"] = routes[1:]
				wantDomains = wantDomains[1:]
			case "edit aliased field":
				exits[1].(map[string]any)["name"] = "Lab"
				changes["exits"] = exits
			case "reorder aliases":
				changes["exits"] = []any{exits[1], exits[0]}
			}
			status, body = request(t, server, "POST", "/api/config", map[string]any{
				"yaml": original, "revision": doc.Revision, "changes": changes,
			}, nil)
			if status != http.StatusOK || json.Unmarshal(body, &doc) != nil || !doc.Valid || applied.Load() != 1 {
				t.Fatalf("structured save: %d %s", status, body)
			}
			for _, comment := range []string{"# Saved devices", "# Office identity", "# Office label", "# Lab identity", "# Shared label", "# Saved groups", "# Second owner", "# Second matches", "# Inherited group", "# Third matches", "# Retain old domains"} {
				if !strings.Contains(doc.YAML, comment) {
					t.Fatalf("save lost %s:\n%s", comment, doc.YAML)
				}
			}
			for _, anchor := range []string{"&office", "&label"} {
				if !strings.Contains(doc.YAML, anchor) {
					t.Fatalf("save lost an unchanged anchor %s:\n%s", anchor, doc.YAML)
				}
			}
			if strings.Contains(doc.YAML, "hostname:") || strings.Contains(doc.YAML, "auto_name: false") || strings.Count(doc.YAML, "exit_node:") != map[bool]int{true: 1, false: 0}[operation == "add exit"] {
				t.Fatalf("save serialized defaults into unrelated entries:\n%s", doc.YAML)
			}
			config, err := tailsplit.LoadDraft(path)
			if err != nil || !reflect.DeepEqual(config.Rules.ProxyDomains, []string{"first.example"}) || len(config.Rules.Routes) != len(wantDomains) {
				t.Fatalf("aliases or saved groups changed: %+v, %v", config.Rules, err)
			}
			for index, domain := range wantDomains {
				if !reflect.DeepEqual(config.Rules.Routes[index].Domains, []string{domain}) {
					t.Fatalf("group %d changed: %+v", index, config.Rules.Routes)
				}
			}
			// Structured edits retain the same atomic revision boundary.
			_, savedRevision, _ := tray.ReadConfigDocument(path)
			status, _ = request(t, server, "POST", "/api/config", map[string]any{
				"yaml": original, "revision": "stale", "changes": changes,
			}, nil)
			_, currentRevision, _ := tray.ReadConfigDocument(path)
			if status != http.StatusConflict || applied.Load() != 1 || currentRevision != savedRevision {
				t.Fatal("stale structured save crossed the apply/revision boundary")
			}
		})
	}
}

func TestStructuredListReuseDoesNotTreatFalseIdentityAsAnOmittedDefault(t *testing.T) {
	server, path, applied := testServer(t)
	const original = "tailscale: null\nexits:\n  - id: false\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	_, revision, _ := tray.ReadConfigDocument(path)
	status, body := request(t, server, "POST", "/api/config", map[string]any{
		"yaml": original, "revision": revision, "changes": map[string]any{"exits": []map[string]any{{}}},
	}, nil)
	current, currentRevision, _ := tray.ReadConfigDocument(path)
	if status != http.StatusUnprocessableEntity || applied.Load() != 0 || string(current) != original || currentRevision != revision {
		t.Fatalf("missing identity reused the old false identity: %d %s", status, body)
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
