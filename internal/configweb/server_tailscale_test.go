//go:build with_gvisor

package configweb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfiguredTailnetCanBeValidatedWithoutStartingIt(t *testing.T) {
	server, path, applied := testServer(t)
	state := filepath.Join(filepath.Dir(path), "preview-state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "gfwlist.txt"), []byte("[AutoProxy 0.2.9]\n||fixture.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := "version: 1\nstate_dir: ./preview-state\ntailscale:\n  exit_node: 100.64.0.10\ndns:\n  direct_ipv6: false\nrules:\n  gfwlist: builtin\ntun:\n  enabled: true\n"
	status, body := request(t, server, "POST", "/api/validate", map[string]string{"yaml": config}, nil)
	if status != 200 || applied.Load() != 0 {
		t.Fatal("configured Tailnet could not be checked without applying it:", string(body))
	}
}
