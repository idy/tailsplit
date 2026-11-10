package configweb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

func TestRebuildInvalidConfiguration(t *testing.T) {
	for _, broken := range []string{"dns: [\n", "dns:\n  listen: 127.0.0.1:53\n"} {
		t.Run(broken, func(t *testing.T) {
			server, path, applied := testServer(t)
			if err := os.WriteFile(path, []byte(broken), 0600); err != nil {
				t.Fatal(err)
			}
			_, revision, _ := tray.ReadConfigDocument(path)
			status, body := request(t, server, "POST", "/api/config-rebuild", map[string]string{"revision": revision}, nil)
			var result struct {
				document
				BackupPath string `json:"backup_path"`
			}
			if err := json.Unmarshal(body, &result); err != nil || status != 200 || !result.Valid || applied.Load() != 1 {
				t.Fatalf("rebuild failed: %d %s %v", status, body, err)
			}
			backup, err := os.ReadFile(result.BackupPath)
			if err != nil || string(backup) != broken || filepath.Dir(result.BackupPath) != filepath.Dir(path) {
				t.Fatalf("backup: %q %v", backup, err)
			}
			if result.YAML != string(tailsplit.DefaultYAML) || result.Revision == revision {
				t.Fatal("did not return fresh desktop defaults")
			}
			config, err := tailsplit.Load(path)
			if err != nil || len(config.ExitIDs()) != 0 {
				t.Fatalf("defaults retained old exits: %+v %v", config, err)
			}
		})
	}
}

func TestRebuildRejectsUnsafeRequests(t *testing.T) {
	for _, scenario := range []string{"valid", "missing revision", "stale", "active", "active under gate", "changed under gate", "busy", "unauthorized", "foreign origin"} {
		t.Run(scenario, func(t *testing.T) {
			server, path, applied := testServer(t)
			original := "dns:\n  listen: 127.0.0.1:53\n"
			if scenario == "valid" {
				original = fixtureConfig
			}
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			_, revision, _ := tray.ReadConfigDocument(path)
			headers := map[string]string{}
			want := 409
			switch scenario {
			case "missing revision":
				revision, want = "", 400
			case "stale":
				revision = "old"
			case "active":
				server.options.Snapshot = func() tray.Snapshot { return tray.Snapshot{Active: true, Mode: "tun"} }
			case "active under gate":
				server.options.Apply = func(_ context.Context, check func(tray.Snapshot) error, update func() error) error {
					if err := check(tray.Snapshot{Active: true, Mode: "tun"}); err != nil {
						return err
					}
					return update()
				}
			case "changed under gate":
				server.options.Apply = func(_ context.Context, check func(tray.Snapshot) error, update func() error) error {
					original = fixtureConfig
					if err := os.WriteFile(path, []byte(original), 0600); err != nil {
						return err
					}
					if err := check(tray.Snapshot{}); err != nil {
						return err
					}
					return update()
				}
			case "busy":
				server.options.Apply = func(context.Context, func(tray.Snapshot) error, func() error) error { return ErrBusy }
			case "unauthorized":
				headers["Authorization"], want = "", 401
			case "foreign origin":
				headers["Origin"], want = "https://other.example", 403
			}
			status, body := request(t, server, "POST", "/api/config-rebuild", map[string]string{"revision": revision}, headers)
			if status != want {
				t.Fatalf("status %d, want %d: %s", status, want, body)
			}
			current, err := os.ReadFile(path)
			if err != nil || string(current) != original || applied.Load() != 0 {
				t.Fatalf("rejected request changed config: %s %v", current, err)
			}
			backups, _ := filepath.Glob(path + ".backup-*")
			if len(backups) != 0 {
				t.Fatalf("rejected request created backups: %v", backups)
			}
		})
	}
}
