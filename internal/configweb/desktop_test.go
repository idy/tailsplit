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

func TestConditionalDisconnectNeverFallsBackToUnconditionalStop(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		t.Run(map[bool]string{false: "without-guarded-callback", true: "with-guarded-callback"}[guarded], func(t *testing.T) {
			server, path, _ := testServer(t)
			controller, err := tray.New(tray.Options{Helper: "unused", ConfigPath: path, LogPath: filepath.Join(t.TempDir(), "core.log")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = controller.Close() })
			if guarded {
				server.options.Cancel = func(_ context.Context, request, operation string) error { return controller.Cancel(request, operation) }
			}
			calls := 0
			server.options.Action = func(_ context.Context, action string) error { calls++; return nil }
			controller.RequestConnectionSetup()
			old := controller.Snapshot().ConnectRequest
			controller.RequestConnectionSetup()
			newer := controller.Snapshot().ConnectRequest
			want := http.StatusNotFound
			if guarded {
				want = http.StatusOK
			}
			for _, id := range []string{old, newer} {
				status, body := request(t, server, "POST", "/api/action", map[string]string{"action": "disconnect", "connect_request": id}, nil)
				if status != want || calls != 0 {
					t.Fatal("conditional cancellation called legacy Stop", status, string(body), calls)
				}
				pending := newer
				if guarded && id == newer {
					pending = ""
				}
				if controller.Snapshot().ConnectRequest != pending {
					t.Fatal("consumed the wrong native intent", controller.Snapshot())
				}
			}
			status, _ := request(t, server, "POST", "/api/action", map[string]string{"action": "disconnect"}, nil)
			if status != http.StatusOK || calls != 1 {
				t.Fatal("explicit legacy disconnect failed", status, calls)
			}
		})
	}
}

func TestCancelRequiresExactTargetsCapabilityOriginAndStrictJSON(t *testing.T) {
	server, path, _ := testServer(t)
	controller, err := tray.New(tray.Options{Helper: "unused", ConfigPath: path, LogPath: filepath.Join(t.TempDir(), "core.log")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	controller.RequestConnectionSetup()
	id := controller.Snapshot().ConnectRequest
	calls := 0
	server.options.Cancel = func(_ context.Context, request, operation string) error {
		calls++
		return controller.Cancel(request, operation)
	}
	for _, test := range []struct {
		name, body string
		headers    map[string]string
		want       int
	}{
		{"missing capability", `{"request":"` + id + `"}`, map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		{"foreign origin", `{"request":"` + id + `"}`, map[string]string{"Origin": "https://foreign.example"}, http.StatusForbidden},
		{"wrong host", `{"request":"` + id + `"}`, map[string]string{"Host": "foreign.example"}, http.StatusForbidden},
		{"wrong content type", `{"request":"` + id + `"}`, map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
		{"empty targets", `{"request":"","operation_id":""}`, nil, http.StatusBadRequest},
		{"unknown field", `{"request":"` + id + `","action":"disconnect"}`, nil, http.StatusBadRequest},
		{"wrong type", `{"operation_id":12}`, nil, http.StatusBadRequest},
		{"trailing JSON", `{"request":"` + id + `"}{}`, nil, http.StatusBadRequest},
		{"malformed", `{"request":`, nil, http.StatusBadRequest},
		{"oversized", `{"operation_id":"` + strings.Repeat("x", 2048) + `"}`, nil, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://"+server.address+"/api/cancel", strings.NewReader(test.body))
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
			if response.Code != test.want || calls != 0 || controller.Snapshot().ConnectRequest != id {
				t.Fatal(response.Code, response.Body.String(), calls, controller.Snapshot())
			}
		})
	}
	for _, target := range []map[string]string{{"request": "unknown"}, {"operation_id": "not-admitted"}, {"operation_id": "not-admitted"}, {"request": id, "operation_id": id}} {
		status, body := request(t, server, "POST", "/api/cancel", target, nil)
		if status != http.StatusOK {
			t.Fatal(status, string(body))
		}
	}
	if controller.ClaimConnectOperation(id, id) || controller.ClaimConnectOperation(id, "new-id") || controller.Snapshot().ConnectRequest != "" {
		t.Fatal("cancelled request was claimable")
	}
	if err := controller.ForOperation("not-admitted").Start(); !errors.Is(err, tray.ErrOperationCanceled) {
		t.Fatal("cancel before admission was lost", err)
	}
}

func TestActionOperationIdentitySurvivesDispatchAndRejectsCanceledAdmission(t *testing.T) {
	server, path, _ := testServer(t)
	controller, err := tray.New(tray.Options{Helper: "unused", ConfigPath: path, LogPath: filepath.Join(t.TempDir(), "core.log")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	server.options.Cancel = func(_ context.Context, request, operation string) error { return controller.Cancel(request, operation) }
	server.options.Action = func(ctx context.Context, action string) error {
		if id := tray.OperationID(ctx); id != "caller-known" {
			t.Fatal("lost caller identity", id)
		}
		return controller.ForOperation(tray.OperationID(ctx)).Check()
	}
	status, _ := request(t, server, "POST", "/api/action", map[string]string{"action": "login", "operation_id": "caller-known"}, nil)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	status, _ = request(t, server, "POST", "/api/cancel", map[string]string{"operation_id": "caller-known"}, nil)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	status, body := request(t, server, "POST", "/api/action", map[string]string{"action": "login", "operation_id": "caller-known"}, nil)
	if status != http.StatusConflict || !strings.Contains(string(body), `"code":"canceled"`) {
		t.Fatal(status, string(body))
	}
	server.options.Cancel = nil
	status, _ = request(t, server, "POST", "/api/action", map[string]string{"action": "login", "operation_id": "caller-known"}, nil)
	if status != http.StatusNotFound {
		t.Fatal("guarded action reached unavailable callback", status)
	}
}

func TestConfigSaveCarriesOperationIdentityAndRejectsCanceledSave(t *testing.T) {
	server, path, _ := testServer(t)
	controller, err := tray.New(tray.Options{Helper: "unused", ConfigPath: path, LogPath: filepath.Join(t.TempDir(), "core.log")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	server.options.Cancel = func(_ context.Context, request, operation string) error { return controller.Cancel(request, operation) }
	server.options.Snapshot = controller.Snapshot
	server.options.Apply = func(ctx context.Context, check func(tray.Snapshot) error, update func() error) error {
		if tray.OperationID(ctx) != "guided-operation" {
			t.Fatal("save lost operation identity")
		}
		return controller.ReconfigureOperation(tray.OperationID(ctx), check, update)
	}
	_, revision, _ := tray.ReadConfigDocument(path)
	status, body := request(t, server, "POST", "/api/config", map[string]any{
		"yaml": fixtureConfig, "revision": revision, "operation_id": "guided-operation", "changes": map[string]any{"log_level": "debug"},
	}, nil)
	if status != http.StatusOK || controller.Snapshot().OperationID != "guided-operation" {
		t.Fatal(status, string(body), controller.Snapshot())
	}
	if err := controller.CancelOperation("guided-operation"); err != nil {
		t.Fatal(err)
	}
	original, revision, _ := tray.ReadConfigDocument(path)
	status, body = request(t, server, "POST", "/api/config", map[string]any{
		"yaml": string(original), "revision": revision, "operation_id": "guided-operation", "changes": map[string]any{"log_level": "warn"},
	}, nil)
	_, currentRevision, _ := tray.ReadConfigDocument(path)
	if status != http.StatusConflict || currentRevision != revision || !strings.Contains(string(body), `"code":"canceled"`) {
		t.Fatal("canceled save was applied", status, string(body))
	}
	server.options.Cancel = nil
	status, _ = request(t, server, "POST", "/api/config", map[string]any{
		"yaml": string(original), "revision": revision, "operation_id": "guided-operation", "changes": map[string]any{"log_level": "warn"},
	}, nil)
	if status != http.StatusNotFound {
		t.Fatal("guarded save fell back to legacy apply", status)
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
