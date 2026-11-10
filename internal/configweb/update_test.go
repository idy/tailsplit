package configweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/idy/tailsplit/internal/appupdate"
)

func TestUpdateActionsRequireCapabilityAndExactVocabulary(t *testing.T) {
	server, path, applied := testServer(t)
	status, _ := request(t, server, "POST", "/api/update-action", map[string]string{"action": "check"}, nil)
	if status != http.StatusNotFound {
		t.Fatal("server without update callback exposed update actions")
	}
	var calls []string
	server.options.UpdateAction = func(_ context.Context, action string) error {
		calls = append(calls, action)
		if action == "install" {
			return fmt.Errorf("cannot install: %w", ErrBusy)
		}
		return nil
	}
	for _, test := range []struct {
		name, action string
		headers      map[string]string
		want         int
	}{
		{"missing capability", "check", map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		{"wrong capability", "check", map[string]string{"Authorization": "Bearer invalid"}, http.StatusUnauthorized},
		{"foreign origin", "check", map[string]string{"Origin": "https://foreign.example"}, http.StatusForbidden},
		{"rebound host", "check", map[string]string{"Host": "foreign.example"}, http.StatusForbidden},
		{"wrong content type", "check", map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
		{"unknown action", "connect", nil, http.StatusBadRequest},
		{"command", "cmd.exe", nil, http.StatusBadRequest},
		{"path", "../installer", nil, http.StatusBadRequest},
		{"check", "check", nil, http.StatusOK},
		{"automatic on", "automatic-on", nil, http.StatusOK},
		{"automatic off", "automatic-off", nil, http.StatusOK},
		{"busy install", "install", nil, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, _ := request(t, server, "POST", "/api/update-action", map[string]string{"action": test.action}, test.headers)
			if status != test.want {
				t.Fatalf("status %d, want %d", status, test.want)
			}
		})
	}
	if !reflect.DeepEqual(calls, []string{"check", "automatic-on", "automatic-off", "install"}) {
		t.Fatalf("unauthorized or unknown action reached update callback: %v", calls)
	}
	status, _ = request(t, server, "POST", "/api/action", map[string]string{"action": "connect"}, nil)
	if status != http.StatusNotFound {
		t.Fatal("update callback exposed desktop connection controls")
	}
	server.options.UpdateAction = func(context.Context, string) error { return fmt.Errorf("cannot check: %w", appupdate.ErrBusy) }
	status, _ = request(t, server, "POST", "/api/update-action", map[string]string{"action": "check"}, nil)
	if status != http.StatusConflict {
		t.Fatal("updater busy error was not reported as a conflict")
	}
	server.options.UpdateAction = func(context.Context, string) error { return nil }
	status, body := request(t, server, "POST", "/api/update-action", map[string]string{"action": "install"}, nil)
	if status != http.StatusOK || !bytes.Contains(body, []byte(`"ok":true`)) {
		t.Fatal("install action failed:", string(body))
	}
	server.options.UpdateAction = func(context.Context, string) error { return errors.New("update unavailable") }
	status, body = request(t, server, "POST", "/api/update-action", map[string]string{"action": "check"}, nil)
	if status != http.StatusBadRequest || !bytes.Contains(body, []byte("update unavailable")) {
		t.Fatal("update failure was not reported:", string(body))
	}
	data, _ := os.ReadFile(path)
	if applied.Load() != 0 || string(data) != fixtureConfig {
		t.Fatal("update action applied or reconfigured the connection")
	}
}

func TestUpdateActionsRejectExtraOrMalformedJSON(t *testing.T) {
	server, _, _ := testServer(t)
	calls := 0
	server.options.UpdateAction = func(context.Context, string) error { calls++; return nil }
	for _, body := range []string{
		`{"action":"check","path":"C:\\Windows"}`, `{"action":"check","automatic":true}`,
		`{"action":"check"} {}`, `{"action":"check"} null`, `{"action":`,
		`null`, `{}`, `[]`, `{"action":null}`, `{"action":false}`, `{"action":"CHECK"}`,
		`{"action":"` + strings.Repeat("x", 2048) + `"}`,
	} {
		req := httptest.NewRequest("POST", "http://"+server.address+"/api/update-action", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+server.token)
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		server.handler().ServeHTTP(result, req)
		if result.Code != http.StatusBadRequest {
			t.Fatalf("accepted malformed update request %q: %d", body, result.Code)
		}
	}
	if calls != 0 {
		t.Fatalf("malformed request reached update callback: %d calls", calls)
	}
}

func TestUpdateStateIsOptionalAndContainsOnlyPublicUpdateFields(t *testing.T) {
	server, path, _ := testServer(t)
	status, body := request(t, server, "GET", "/api/state", nil, nil)
	var state map[string]any
	if err := json.Unmarshal(body, &state); err != nil || status != http.StatusOK {
		t.Fatal("could not load state:", string(body), err)
	}
	if state["updates"] != nil {
		t.Fatal("server without update state exposed updates")
	}
	checked := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	server.options.UpdateState = func() appupdate.State {
		return appupdate.State{
			CurrentVersion: "0.6.1", LatestVersion: "0.6.2", Automatic: true,
			Busy: true, Confirming: true, Checking: true, Downloading: true, Installing: true, Available: true,
			DownloadedBytes: 64, TotalBytes: 128, LastChecked: checked,
			Error: "update unavailable", ReleaseURL: "https://github.com/idy/tailsplit/releases/tag/v0.6.2",
			ReleaseNotes: "Release notes for 0.6.2",
		}
	}
	status, body = request(t, server, "GET", "/api/state", nil, nil)
	if err := json.Unmarshal(body, &state); err != nil || status != http.StatusOK {
		t.Fatal("could not load update state:", string(body), err)
	}
	want := map[string]any{
		"current_version": "0.6.1", "latest_version": "0.6.2", "automatic": true,
		"busy": true, "confirming": true, "checking": true, "downloading": true, "installing": true, "available": true,
		"downloaded_bytes": float64(64), "total_bytes": float64(128), "last_checked": checked.Format(time.RFC3339),
		"error": "update unavailable", "release_url": "https://github.com/idy/tailsplit/releases/tag/v0.6.2",
		"release_notes": "Release notes for 0.6.2",
	}
	if !reflect.DeepEqual(state["updates"], want) || state["desktop"] != false {
		t.Fatal("incorrect public update state or desktop capability:", string(body))
	}
	for _, private := range []string{path, server.token, "secret.example", "LoginURL", "installer_path", "download_path"} {
		if bytes.Contains(body, []byte(private)) {
			t.Fatalf("update state leaked %q", private)
		}
	}
}
