package configweb

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestAddExitStartsAuthenticatedTransactionWithoutBrowserSaving(t *testing.T) {
	s, _, _ := testServer(t)
	original, revision, err := tray.ReadConfigDocument(s.options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var receivedID, receivedRevision string
	s.options.AddExit = func(_ context.Context, id, expected string) error {
		receivedID, receivedRevision = id, expected
		return nil
	}
	status, _ := request(t, s, "POST", "/api/action", map[string]string{"action": "add-exit", "exit": "new-exit", "revision": revision}, nil)
	if status != http.StatusOK || receivedID != "new-exit" || receivedRevision != revision {
		t.Fatal(status, receivedID, receivedRevision)
	}
	after, err := os.ReadFile(s.options.ConfigPath)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("starting authorization published a provisional profile", err)
	}
}

func TestAddExitRequiresAuthenticationAndFreshUnusedID(t *testing.T) {
	s, _, _ := testServer(t)
	original, revision, err := tray.ReadConfigDocument(s.options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := tray.PatchConfigDocument(original, tray.ConfigChange{Key: "exits", Value: []map[string]any{{"id": "known", "exit_node": ""}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := tray.SaveConfigDocument(s.options.ConfigPath, revision, candidate); err != nil {
		t.Fatal(err)
	}
	_, revision, err = tray.ReadConfigDocument(s.options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.options.AddExit = func(context.Context, string, string) error { calls++; return nil }
	for _, test := range []struct {
		id      string
		headers map[string]string
		want    int
	}{
		{"new-exit", map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		{"new-exit", map[string]string{"Origin": "https://foreign.example"}, http.StatusForbidden},
		{"", nil, http.StatusBadRequest}, {"../escape", nil, http.StatusBadRequest}, {"main", nil, http.StatusBadRequest}, {"known", nil, http.StatusBadRequest},
	} {
		status, _ := request(t, s, "POST", "/api/action", map[string]string{"action": "add-exit", "exit": test.id, "revision": revision}, test.headers)
		if status != test.want {
			t.Fatalf("id=%q status=%d want=%d", test.id, status, test.want)
		}
	}
	if calls != 0 {
		t.Fatal("invalid Add reached the controller")
	}
}

func TestAddExitRejectsStaleOrMissingDisplayedRevision(t *testing.T) {
	s, _, _ := testServer(t)
	data, revision, err := tray.ReadConfigDocument(s.options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.options.ConfigPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.options.AddExit = func(context.Context, string, string) error { calls++; return nil }
	for _, expected := range []string{"", revision} {
		status, result := request(t, s, "POST", "/api/action", map[string]string{"action": "add-exit", "exit": "new-exit", "revision": expected}, nil)
		if status != http.StatusConflict || !bytes.Contains(result, []byte(`"code":"conflict"`)) {
			t.Fatal(status, result)
		}
	}
	if calls != 0 {
		t.Fatal("stale Add started authentication")
	}
}

func TestAddExitMapsControllerRevisionConflict(t *testing.T) {
	s, _, _ := testServer(t)
	_, revision, err := tray.ReadConfigDocument(s.options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	s.options.AddExit = func(_ context.Context, id, expected string) error {
		if id != "new-exit" || expected != revision {
			t.Fatal("revision was not forwarded")
		}
		return tray.ErrConfigChanged
	}
	status, result := request(t, s, "POST", "/api/action", map[string]string{"action": "add-exit", "exit": "new-exit", "revision": revision}, nil)
	if status != http.StatusConflict || !bytes.Contains(result, []byte(`"code":"conflict"`)) {
		t.Fatal(status, result)
	}
}
