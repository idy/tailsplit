package configweb

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestRepairConfigurationPreservesExits(t *testing.T) {
	server, path, applied := testServer(t)
	original := "tailscale:\n  exit_node: 100.64.0.10\ndns:\n  listen: 127.0.0.1:53\nrules:\n  direct_domains: [keep.example]\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	status, body := request(t, server, "GET", "/api/config", nil, nil)
	var before document
	if err := json.Unmarshal(body, &before); err != nil || status != 200 || before.Valid || !before.Repairable {
		t.Fatalf("repair not advertised: %s %v", body, err)
	}
	status, body = request(t, server, "POST", "/api/config-repair", map[string]string{"revision": before.Revision}, nil)
	var after struct {
		document
		BackupPath string `json:"backup_path"`
	}
	if err := json.Unmarshal(body, &after); err != nil || status != 200 || !after.Valid || after.Repairable || applied.Load() != 1 {
		t.Fatalf("repair failed: %d %s %v", status, body, err)
	}
	backup, err := os.ReadFile(after.BackupPath)
	if err != nil || string(backup) != original {
		t.Fatalf("backup differs: %v", err)
	}
	if after.Values["tailscale"].(map[string]any)["exit_node"] != "100.64.0.10" {
		t.Fatal("lost saved exit")
	}
}

func TestRepairRejectsUnrepairableOrStaleConfig(t *testing.T) {
	for _, original := range []string{"dns: [\n", "dns:\n  listen: x\n  typo: true\n", fixtureConfig} {
		server, path, applied := testServer(t)
		if err := os.WriteFile(path, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		_, revision, _ := tray.ReadConfigDocument(path)
		status, _ := request(t, server, "POST", "/api/config-repair", map[string]string{"revision": revision}, nil)
		if status != 422 && status != 409 {
			t.Fatalf("unexpected status %d", status)
		}
		status, _ = request(t, server, "POST", "/api/config-repair", map[string]string{"revision": "stale"}, nil)
		if status != 409 || applied.Load() != 0 {
			t.Fatalf("stale repair applied: %d", status)
		}
		current, _, _ := tray.ReadConfigDocument(path)
		if string(current) != original {
			t.Fatal("failed repair changed source")
		}
	}
}
