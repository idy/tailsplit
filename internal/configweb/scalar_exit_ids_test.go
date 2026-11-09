package configweb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"testing"

	"github.com/idy/tailsplit"
)

func TestScalarExitInventorySurvivesServicePolicyRejection(t *testing.T) {
	server, path, applied := testServer(t)
	original := "tailscale: null\nexits:\n  - id: 001\n"
	for index := 1; index <= 8; index++ {
		original += fmt.Sprintf("  - id: exit-%d\n", index)
	}
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	status, body := request(t, server, "GET", "/api/config", nil, nil)
	var doc document
	if err := json.Unmarshal(body, &doc); err != nil || status != http.StatusOK || !doc.Parsed || doc.Valid || doc.Error == "" {
		t.Fatalf("oversized service inventory was not readable: %s, %v", body, err)
	}
	exits := doc.Values["exits"].([]any)
	if len(exits) != 9 || exits[0].(map[string]any)["id"] != "001" || doc.YAML != original || applied.Load() != 0 {
		t.Fatal("service-policy rejection hid an otherwise valid saved identity", doc)
	}
}

func TestScalarExitIDsRemainStringsAcrossConfigResponsesAndAdd(t *testing.T) {
	for _, id := range []string{"0", "1", "001", "1e3", "0x10", "true", "false", "12345678901234567890123456789012"} {
		t.Run(id, func(t *testing.T) {
			server, path, applied := testServer(t)
			original := fmt.Sprintf("# Scalar exit identity\nstate_dir: ./state\nlog_level: info\ntailscale: null\nexits:\n  - id: %s\n    name: Scalar\n    exit_node: ''\n  - id: office\n    name: Office\n    exit_node: ''\nrules:\n  default_exit: %s\n  global_exit: %s\n  final: %s\n  routes:\n    - exit: %s\n      domains: [private.example]\n", id, id, id, id, id)
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := tailsplit.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			check := func(body []byte) document {
				t.Helper()
				var doc document
				if err := json.Unmarshal(body, &doc); err != nil || !doc.Valid || !doc.Parsed {
					t.Fatalf("valid scalar identity was not readable: %s, %v", body, err)
				}
				exits := doc.Values["exits"].([]any)
				if exits[0].(map[string]any)["id"] != id || exits[1].(map[string]any)["id"] != "office" {
					t.Fatalf("configuration inventory changed the validated ID %q: %v", id, exits)
				}
				rules := doc.Values["rules"].(map[string]any)
				for _, field := range []string{"default_exit", "global_exit", "final"} {
					if rules[field] != id {
						t.Fatalf("%s no longer identifies %q: %v", field, id, rules[field])
					}
				}
				if route := rules["routes"].([]any)[0].(map[string]any); route["exit"] != id {
					t.Fatalf("route ownership changed: %v", route)
				}
				return doc
			}
			status, body := request(t, server, "GET", "/api/config", nil, nil)
			if status != http.StatusOK {
				t.Fatalf("read failed: %d %s", status, body)
			}
			doc := check(body)
			if doc.YAML != original {
				t.Fatal("describing an exit identity rewrote the source YAML")
			}
			status, body = request(t, server, "POST", "/api/preview", map[string]any{
				"yaml": doc.YAML, "changes": map[string]any{"log_level": "debug"},
			}, nil)
			if status != http.StatusOK {
				t.Fatalf("preview failed: %d %s", status, body)
			}
			check(body)
			if data, err := os.ReadFile(path); err != nil || string(data) != original || applied.Load() != 0 {
				t.Fatal("read/preview changed saved state", err)
			}
			// Add uses the inventory returned to the browser, retaining every saved profile.
			exits := append(doc.Values["exits"].([]any), map[string]any{"id": "exit-1", "auto_name": true, "exit_node": ""})
			status, body = request(t, server, "POST", "/api/config", map[string]any{
				"yaml": doc.YAML, "revision": doc.Revision, "changes": map[string]any{"exits": exits},
			}, nil)
			if status != http.StatusOK {
				t.Fatalf("add failed: %d %s", status, body)
			}
			check(body)
			after, err := tailsplit.Load(path)
			if err != nil || len(after.Exits) != 3 || !reflect.DeepEqual(after.Exits[:2], before.Exits) ||
				after.Exits[2].ID != "exit-1" || after.ExitStateDir(id) != before.ExitStateDir(id) ||
				!reflect.DeepEqual(after.Rules, before.Rules) || applied.Load() != 1 {
				t.Fatalf("adding an exit changed a saved identity or route: %+v, %v", after, err)
			}
		})
	}
}
