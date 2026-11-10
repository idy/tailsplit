package configweb

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestAddExitChecksRevisionBeforeCurrentConfigurationValidation(t *testing.T) {
	for _, change := range []string{"capacity", "duplicate", "invalid-document"} {
		t.Run(change, func(t *testing.T) {
			s, _, _ := testServer(t)
			original, revision, err := tray.ReadConfigDocument(s.options.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if change == "capacity" {
				exits := make([]map[string]any, 7)
				for i := range exits {
					exits[i] = map[string]any{"id": fmt.Sprintf("saved-%d", i), "exit_node": ""}
				}
				data, err := tray.PatchConfigDocument(original, tray.ConfigChange{Key: "exits", Value: exits})
				if err != nil {
					t.Fatal(err)
				}
				if err := tray.SaveConfigDocument(s.options.ConfigPath, revision, data); err != nil {
					t.Fatal(err)
				}
				original, revision, err = tray.ReadConfigDocument(s.options.ConfigPath)
				if err != nil {
					t.Fatal(err)
				}
			}
			var updated []byte
			if change == "invalid-document" {
				updated = []byte("exits: [\n")
			} else {
				exits := []map[string]any{{"id": "new-exit", "exit_node": ""}}
				if change == "capacity" {
					exits = make([]map[string]any, 8)
					for i := range exits {
						exits[i] = map[string]any{"id": fmt.Sprintf("saved-%d", i), "exit_node": ""}
					}
				}
				updated, err = tray.PatchConfigDocument(original, tray.ConfigChange{Key: "exits", Value: exits})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(s.options.ConfigPath, updated, 0o600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			s.options.AddExit = func(context.Context, string, string) error { calls++; return nil }
			status, body := request(t, s, "POST", "/api/action", map[string]string{"action": "add-exit", "exit": "new-exit", "revision": revision}, nil)
			if status != http.StatusConflict || !bytes.Contains(body, []byte(`"code":"conflict"`)) {
				t.Fatalf("current validation masked stale revision: %d %s", status, body)
			}
			if calls != 0 {
				t.Fatal("stale request started authorization")
			}
			_, current, err := tray.ReadConfigDocument(s.options.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			status, _ = request(t, s, "POST", "/api/action", map[string]string{"action": "add-exit", "exit": "new-exit", "revision": current}, nil)
			if status != http.StatusBadRequest || calls != 0 {
				t.Fatal("fresh invalid request bypassed validation", status, calls)
			}
		})
	}
}
