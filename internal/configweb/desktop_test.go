package configweb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestDesktopActionsRequireCapabilityAndExactVocabulary(t *testing.T) {
	server, _, _ := testServer(t)
	calls := 0
	server.options.Action = func(_ context.Context, action string) error {
		calls++
		if action == "logout" {
			return ErrBusy
		}
		return nil
	}
	for _, test := range []struct {
		action  string
		headers map[string]string
		want    int
	}{
		{"connect", map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		{"connect", map[string]string{"Origin": "https://foreign.example"}, http.StatusForbidden},
		{"cmd.exe", nil, http.StatusBadRequest},
		{"../config.yaml", nil, http.StatusBadRequest},
		{"connect", nil, http.StatusOK},
		{"switch-network", map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		{"switch-network", map[string]string{"Origin": "https://foreign.example"}, http.StatusForbidden},
		{"switch-network", nil, http.StatusOK},
		{"logout", nil, http.StatusConflict},
	} {
		status, _ := request(t, server, "POST", "/api/action", map[string]string{"action": test.action}, test.headers)
		if status != test.want {
			t.Fatalf("%q: status %d, want %d", test.action, status, test.want)
		}
	}
	if calls != 3 {
		t.Fatalf("unauthorized/unknown action reached controller: %d calls", calls)
	}
	status, _ := request(t, server, "POST", "/api/action", map[string]string{"action": "connect", "path": "C:\\Windows"}, nil)
	if status != 400 || calls != 3 {
		t.Fatal("accepted an unknown authority field")
	}
}

func TestConnectRequestClaimsRequireCapabilityOriginAndStrictJSON(t *testing.T) {
	server, path, _ := testServer(t)
	status, _ := request(t, server, "POST", "/api/connect-request", map[string]string{"request": "id"}, nil)
	if status != http.StatusNotFound {
		t.Fatal("standalone server exposed native Connect claims")
	}
	controller, err := tray.New(tray.Options{Helper: "unused", ConfigPath: path, LogPath: filepath.Join(t.TempDir(), "core.log")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	server.options.ClaimConnectRequest = controller.ClaimConnectRequest
	controller.RequestConnectionSetup()
	id := controller.Snapshot().ConnectRequest
	for _, test := range []struct {
		name, body string
		headers    map[string]string
		want       int
	}{
		{"missing capability", `{"request":"` + id + `"}`, map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		{"wrong capability", `{"request":"` + id + `"}`, map[string]string{"Authorization": "Bearer wrong"}, http.StatusUnauthorized},
		{"foreign origin", `{"request":"` + id + `"}`, map[string]string{"Origin": "https://foreign.example"}, http.StatusForbidden},
		{"rebound host", `{"request":"` + id + `"}`, map[string]string{"Host": "foreign.example"}, http.StatusForbidden},
		{"wrong content type", `{"request":"` + id + `"}`, map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
		{"unknown field", `{"request":"` + id + `","action":"connect"}`, nil, http.StatusBadRequest},
		{"wrong type", `{"request":12}`, nil, http.StatusBadRequest},
		{"malformed", `{"request":`, nil, http.StatusBadRequest},
		{"trailing JSON", `{"request":"` + id + `"}{}`, nil, http.StatusBadRequest},
		{"oversized", `{"request":"` + strings.Repeat("x", 2048) + `"}`, nil, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://"+server.address+"/api/connect-request", strings.NewReader(test.body))
			req.Header.Set("Authorization", "Bearer "+server.token)
			req.Header.Set("Content-Type", "application/json")
			for key, value := range test.headers {
				if key == "Host" {
					req.Host = value
				} else {
					req.Header.Set(key, value)
				}
			}
			response := httptest.NewRecorder()
			server.handler().ServeHTTP(response, req)
			if response.Code != test.want || controller.Snapshot().ConnectRequest != id {
				t.Fatal("rejected request consumed native intent", response.Code, response.Body.String())
			}
		})
	}
	for _, claim := range []struct {
		id   string
		want bool
	}{{"", false}, {"stale", false}, {id, true}, {id, false}, {"", false}} {
		status, body := request(t, server, "POST", "/api/connect-request", map[string]string{"request": claim.id}, nil)
		var result map[string]bool
		if err := json.Unmarshal(body, &result); err != nil || status != http.StatusOK || len(result) != 1 || result["claimed"] != claim.want {
			t.Fatalf("claim %q returned %d %s, want claimed=%t", claim.id, status, body, claim.want)
		}
	}
	controller.RequestConnectionSetup()
	newer := controller.Snapshot().ConnectRequest
	status, body := request(t, server, "POST", "/api/connect-request", map[string]string{"request": id}, nil)
	if status != http.StatusOK || strings.TrimSpace(string(body)) != `{"claimed":false}` || controller.Snapshot().ConnectRequest != newer {
		t.Fatal("old WebView acknowledgement consumed a newer request", status, string(body))
	}
}

func TestConcurrentConnectRequestClaimsHaveOnlyOneWinner(t *testing.T) {
	server, path, _ := testServer(t)
	controller, err := tray.New(tray.Options{Helper: "unused", ConfigPath: path, LogPath: filepath.Join(t.TempDir(), "core.log")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	server.options.ClaimConnectRequest = controller.ClaimConnectRequest
	controller.RequestConnectionSetup()
	id := controller.Snapshot().ConnectRequest
	handler := server.handler()
	var winners, failures atomic.Int32
	var consumers sync.WaitGroup
	ready := make(chan struct{})
	for range 32 {
		consumers.Go(func() {
			req := httptest.NewRequest("POST", "http://"+server.address+"/api/connect-request", strings.NewReader(`{"request":"`+id+`"}`))
			req.Header.Set("Authorization", "Bearer "+server.token)
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			<-ready
			handler.ServeHTTP(response, req)
			var result struct {
				Claimed bool `json:"claimed"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
				failures.Add(1)
			} else if result.Claimed {
				winners.Add(1)
			}
		})
	}
	close(ready)
	consumers.Wait()
	if winners.Load() != 1 || failures.Load() != 0 || controller.Snapshot().ConnectRequest != "" {
		t.Fatal("concurrent WebViews did not have exactly one claimant", winners.Load(), failures.Load())
	}
}

func TestDesktopLogsAreBoundedAndAuthenticated(t *testing.T) {
	server, _, _ := testServer(t)
	status, _ := request(t, server, "GET", "/api/logs", nil, nil)
	if status != 404 {
		t.Fatal("non-desktop server exposed logs")
	}
	server.options.Logs = func() (string, error) { return strings.Repeat("x", 256<<10), nil }
	status, _ = request(t, server, "GET", "/api/logs", nil, map[string]string{"Authorization": ""})
	if status != 401 {
		t.Fatal("unauthenticated logs were accessible")
	}
	status, body := request(t, server, "GET", "/api/logs", nil, nil)
	if status != 200 || len(body) > (128<<10)+32 {
		t.Fatal("unbounded log response")
	}
	server.options.Logs = func() (string, error) { return "", errors.New("private details") }
	status, body = request(t, server, "GET", "/api/logs", nil, nil)
	if status != 500 || strings.Contains(string(body), "private details") {
		t.Fatal("log failure exposed private details")
	}
}
