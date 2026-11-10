package configweb

import (
	"bytes"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/idy/tailsplit/internal/tray"
)

func TestPopupAssetsAndCapability(t *testing.T) {
	server, _, _ := testServer(t)
	parsed, err := url.Parse(server.PopupURL())
	if err != nil || parsed.Path != "/tray.html" || parsed.RawQuery != "" || parsed.Fragment != "token="+server.token {
		t.Fatal("popup must use the same fragment-only capability")
	}
	for _, path := range []string{"/tray.html", "/tray.js", "/tray.css"} {
		status, body := request(t, server, "GET", path, nil, map[string]string{"Authorization": ""})
		if status != 200 || len(body) == 0 || bytes.Contains(body, []byte(server.token)) {
			t.Fatalf("bad popup asset %s: %d", path, status)
		}
	}
}

func TestPopupNativeActionsRequireCapabilityAndMatchingOrigin(t *testing.T) {
	server, _, _ := testServer(t)
	called := make(chan string, 8)
	server.options.OpenPanel = func() { called <- "open-panel" }
	server.options.Quit = func() { called <- "quit" }
	for _, action := range []string{"open-panel", "quit"} {
		body := map[string]string{"action": action}
		for _, headers := range []map[string]string{
			{"Authorization": ""}, {"Origin": "https://foreign.example"}, {"Host": "foreign.example"},
		} {
			status, _ := request(t, server, "POST", "/api/action", body, headers)
			if status != 401 && status != 403 {
				t.Fatalf("accepted unauthorized %s: %d", action, status)
			}
		}
		status, _ := request(t, server, "POST", "/api/action", map[string]string{"action": action, "exit": "office"}, nil)
		if status != 400 {
			t.Fatalf("accepted unexpected exit for %s: %d", action, status)
		}
		select {
		case unexpected := <-called:
			t.Fatalf("rejected request invoked %s", unexpected)
		default:
		}
		status, _ = request(t, server, "POST", "/api/action", body, nil)
		if status != 200 {
			t.Fatalf("native action failed: %d", status)
		}
		select {
		case got := <-called:
			if got != action {
				t.Fatalf("called %s, want %s", got, action)
			}
		case <-time.After(time.Second):
			t.Fatalf("native action %s never ran", action)
		}
	}
}

func TestPopupStateIdentifiesActualTrafficDevice(t *testing.T) {
	server, _, _ := testServer(t)
	server.options.Snapshot = func() tray.Snapshot {
		return tray.Snapshot{Active: true, State: "running", Mode: "tun", TrafficExit: "office",
			BackendState: "NeedsLogin", LoginURL: "https://private.example/auth",
			Exits: map[string]tray.ExitSnapshot{"office": {BackendState: "Running", ExitNode: "100.64.0.2"}}}
	}
	status, data := request(t, server, "GET", "/api/state", nil, nil)
	var state map[string]any
	if status != 200 || json.Unmarshal(data, &state) != nil {
		t.Fatal("invalid state")
	}
	if state["traffic_exit"] != "office" || state["state"] != "running" {
		t.Fatalf("lost actual gateway: %s", data)
	}
	if bytes.Contains(data, []byte("private.example")) {
		t.Fatal("leaked authorization link")
	}
	if state["quit_available"] != false || state["open_panel_available"] != false {
		t.Fatal("invented native capabilities")
	}
	for _, action := range []string{"open-panel", "quit"} {
		status, _ := request(t, server, "POST", "/api/action", map[string]string{"action": action}, nil)
		if status != 404 {
			t.Fatalf("action %s without native host: %d", action, status)
		}
	}
}
