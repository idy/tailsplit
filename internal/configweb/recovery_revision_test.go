package configweb

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestRecoveryDoesNotAcknowledgeExternalReplacementAfterCommit(t *testing.T) {
	for _, action := range []string{"repair", "rebuild"} {
		for _, replacement := range []struct{ name, text string }{
			{"valid", fixtureConfig + "# external routing edit after recovery\n"},
			{"invalid", "dns: [\n"},
		} {
			t.Run(action+"/"+replacement.name, func(t *testing.T) {
				server, path, applied := testServer(t)
				original := []byte("tailscale:\n  exit_node: 100.64.0.10\ndns:\n  listen: 127.0.0.1:53\nrules:\n  direct_domains: [kept.example]\n")
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Fatal(err)
				}
				_, revision, err := tray.ReadConfigDocument(path)
				if err != nil {
					t.Fatal(err)
				}
				apply := server.options.Apply
				server.options.Apply = func(ctx context.Context, check func(tray.Snapshot) error, update func() error) error {
					if err := apply(ctx, check, update); err != nil {
						return err
					}
					return os.WriteFile(path, []byte(replacement.text), 0600)
				}
				status, body := request(t, server, "POST", "/api/config-"+action, map[string]string{"revision": revision}, nil)
				var response struct {
					Code string `json:"code"`
				}
				if status != 409 || json.Unmarshal(body, &response) != nil || response.Code != "conflict" {
					t.Fatalf("external edit acknowledged as recovery: %d %s", status, body)
				}
				current, _, err := tray.ReadConfigDocument(path)
				if err != nil || string(current) != replacement.text {
					t.Fatal("recovery overwrote external replacement", err)
				}
				backups, err := filepath.Glob(path + ".backup-*")
				if err != nil || len(backups) != 1 || applied.Load() != 1 {
					t.Fatal("recovery did not commit exactly one backed-up update", backups, applied.Load(), err)
				}
				backup, err := os.ReadFile(backups[0])
				if err != nil || !bytes.Equal(backup, original) {
					t.Fatal("original backup was not retained", err)
				}
			})
		}
	}
}
