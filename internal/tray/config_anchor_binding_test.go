package tray

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/idy/tailsplit"
)

func TestRouteReorderingPreservesReusedAnchorNameBindings(t *testing.T) {
	const original = `tailscale: {exit_node: 100.64.0.10}
exits:
  - id: office
    exit_node: 100.101.0.11
  - id: lab
    exit_node: 100.101.0.12
rules:
  proxy_domains: &domains [office-secret.example]
  routes:
    - exit: office
      domains: *domains # Office binds the original definition
    - exit: lab
      domains: &domains [lab-secret.example]
    - exit: main
      domains: *domains # Main binds the redefinition
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	before, err := ValidateConfigDocument(path, []byte(original))
	if err != nil {
		t.Fatal(err)
	}
	changes := []tailsplit.RouteConfig{before.Rules.Routes[1], before.Rules.Routes[0], before.Rules.Routes[2]}
	updated, err := PatchConfigDocument([]byte(original), ConfigChange{Section: "rules", Key: "routes", Value: changes})
	if err != nil {
		t.Fatal(err)
	}
	after, err := ValidateConfigDocument(path, updated)
	if err != nil {
		t.Fatalf("reordered anchors did not produce readable YAML: %v\n%s", err, updated)
	}
	if !reflect.DeepEqual(after.Rules.Routes, changes) || !reflect.DeepEqual(after.Rules.ProxyDomains, before.Rules.ProxyDomains) {
		t.Fatalf("reused anchor name rebound a route to another definition: got %+v want %+v\n%s", after.Rules.Routes, changes, updated)
	}
	// A later scalar patch must preserve the same binding relationships too.
	updated, err = PatchConfigDocument(updated, ConfigChange{Key: "log_level", Value: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	after, err = ValidateConfigDocument(path, updated)
	if err != nil || !reflect.DeepEqual(after.Rules.Routes, changes) {
		t.Fatal("subsequent patch changed alias ownership", after.Rules.Routes, err)
	}
}
