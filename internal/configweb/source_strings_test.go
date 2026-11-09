package configweb

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"

	"github.com/idy/tailsplit"
)

func TestSourceStringValuesKeepLexicalDomainsAndOmittedFields(t *testing.T) {
	server, path, applied := testServer(t)
	original := `# Lexical strings must survive unrelated form edits.
state_dir: ./state
tailscale: null
exits:
  - id: ' 001 '
    name: 1.10
rules:
  default_exit: 001
  source: 1.10
  direct_domains: [1.10, 01.10, 1.00]
  routes:
    - exit: 001
      domains: [1.10, 01.10, 1.00]
    - exit: 001
      domains: []
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	wantDomains := []any{"1.10", "01.10", "1.00"}
	assertValues := func(body []byte) document {
		t.Helper()
		var doc document
		if err := json.Unmarshal(body, &doc); err != nil || !doc.Valid || !doc.Parsed {
			t.Fatalf("source strings were unreadable: %s, %v", body, err)
		}
		exit := doc.Values["exits"].([]any)[0].(map[string]any)
		if exit["id"] != "001" || exit["name"] != "1.10" {
			t.Fatalf("typed lexical exit strings changed: %v", exit)
		}
		for _, field := range []string{"exit_node", "hostname", "auto_name"} {
			if _, exists := exit[field]; exists {
				t.Fatalf("describing source inserted omitted exit field %s", field)
			}
		}
		rules := doc.Values["rules"].(map[string]any)
		if rules["source"] != "1.10" || rules["default_exit"] != "001" || !reflect.DeepEqual(rules["direct_domains"], wantDomains) {
			t.Fatalf("typed source fields lost lexical strings: %v", rules)
		}
		for _, field := range []string{"final", "global_exit", "gfwlist", "mode"} {
			if _, exists := rules[field]; exists {
				t.Fatalf("describing source inserted omitted rules field %s", field)
			}
		}
		routes := rules["routes"].([]any)
		if !reflect.DeepEqual(routes[0].(map[string]any)["domains"], wantDomains) ||
			!reflect.DeepEqual(routes[1].(map[string]any)["domains"], []any{}) {
			t.Fatalf("route domains lost their source string/empty representation: %v", routes)
		}
		if doc.Values["tailscale"] != nil {
			t.Fatal("source null primary was replaced")
		}
		return doc
	}
	status, body := request(t, server, "GET", "/api/config", nil, nil)
	if status != http.StatusOK {
		t.Fatal(status, string(body))
	}
	doc := assertValues(body)
	if doc.YAML != original {
		t.Fatal("describing strings rewrote the YAML source")
	}
	status, body = request(t, server, "POST", "/api/preview", map[string]any{
		"yaml": doc.YAML, "changes": map[string]any{"log_level": "debug"},
	}, nil)
	if status != http.StatusOK {
		t.Fatal(status, string(body))
	}
	assertValues(body)
	before, err := tailsplit.LoadDraft(path)
	if err != nil {
		t.Fatal(err)
	}
	// The route editor submits the entire returned list when one group changes.
	routes := doc.Values["rules"].(map[string]any)["routes"].([]any)
	routes[1].(map[string]any)["ips"] = []string{"203.0.113.0/24"}
	status, body = request(t, server, "POST", "/api/config", map[string]any{
		"yaml": doc.YAML, "revision": doc.Revision, "changes": map[string]any{"rules.routes": routes},
	}, nil)
	if status != http.StatusOK {
		t.Fatal(status, string(body))
	}
	assertValues(body)
	after, err := tailsplit.LoadDraft(path)
	if err != nil || !reflect.DeepEqual(after.Rules.Routes[0], before.Rules.Routes[0]) ||
		!reflect.DeepEqual(after.Rules.Routes[0].Domains, []string{"1.10", "01.10", "1.00"}) || applied.Load() != 1 {
		t.Fatalf("editing another group changed lexical domain matches: %+v, %v", after.Rules.Routes, err)
	}
}
