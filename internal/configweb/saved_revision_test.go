package configweb

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestStateSeparatesSavedRevisionFromRunningHelperSource(t *testing.T) {
	server, path, _ := testServer(t)
	_, launchedRevision, err := tray.ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	var current atomic.Value
	current.Store(tray.Snapshot{State: "running", Active: true, Mode: "proxy", ActiveRevision: launchedRevision})
	server.options.Snapshot = func() tray.Snapshot { return current.Load().(tray.Snapshot) }
	check := func(saved, active string) {
		t.Helper()
		status, body := request(t, server, "GET", "/api/state", nil, nil)
		var state map[string]any
		if json.Unmarshal(body, &state) != nil || status != http.StatusOK || state["revision"] != saved || state["active_revision"] != active {
			t.Fatalf("saved and active revisions were confused: status=%d body=%s", status, body)
		}
	}
	check(launchedRevision, launchedRevision)
	if err := os.WriteFile(path, []byte(fixtureConfig+"# external edit while helper is active\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, savedRevision, err := tray.ReadConfigDocument(path)
	if err != nil || savedRevision == launchedRevision {
		t.Fatal("fixture did not change the saved revision", err)
	}
	check(savedRevision, launchedRevision)
	current.Store(tray.Snapshot{State: "stopped"})
	check(savedRevision, "")
	current.Store(tray.Snapshot{State: "login", Active: true, Mode: "login"})
	check(savedRevision, "")
	current.Store(tray.Snapshot{State: "running", Active: true, Mode: "tun", ActiveRevision: savedRevision})
	check(savedRevision, savedRevision)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check("", savedRevision)
}

func TestSaveCannotAcknowledgeAnExternalEditAsItsOwnRevision(t *testing.T) {
	server, path, _ := testServer(t)
	_, revision, _ := tray.ReadConfigDocument(path)
	apply := server.options.Apply
	external := []byte(fixtureConfig + "# external policy after the save\n")
	server.options.Apply = func(ctx context.Context, check func(tray.Snapshot) error, update func() error) error {
		if err := apply(ctx, check, update); err != nil {
			return err
		}
		return os.WriteFile(path, external, 0600)
	}
	status, body := request(t, server, "POST", "/api/config", map[string]any{"yaml": fixtureConfig, "revision": revision, "changes": map[string]any{"log_level": "debug"}}, nil)
	if status != 409 {
		t.Fatalf("external policy was acknowledged as a successful save: %d %s", status, body)
	}
	data, _, _ := tray.ReadConfigDocument(path)
	if string(data) != string(external) {
		t.Fatal("external edit was overwritten")
	}
}
