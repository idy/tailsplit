//go:build with_gvisor

package configweb

import "testing"

func TestConfiguredTailnetCanBeValidatedWithoutStartingIt(t *testing.T) {
	server, _, applied := testServer(t)
	config := "version: 1\nstate_dir: ./preview-state\ntailscale:\n  exit_node: 100.64.0.10\ndns:\n  direct_ipv6: false\nrules:\n  gfwlist: builtin\ntun:\n  enabled: true\n"
	status, body := request(t, server, "POST", "/api/validate", map[string]string{"yaml": config}, nil)
	if status != 200 || applied.Load() != 0 {
		t.Fatal("configured Tailnet could not be checked without applying it:", string(body))
	}
}
