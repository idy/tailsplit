package configweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

func enablePingFixture(server *Server) {
	server.options.Snapshot = func() tray.Snapshot {
		return tray.Snapshot{State: "running", BackendState: "Running", PingAvailable: true, ExitNodes: []tailsplit.ExitNode{
			{ID: "100.64.0.10", Name: "online", Online: true}, {ID: "100.64.0.20", Name: "offline"},
		}}
	}
}

func TestExitPingAPIRequiresCapabilityOriginAndAuthorizedOnlineTarget(t *testing.T) {
	server, _, _ := testServer(t)
	enablePingFixture(server)
	calls := 0
	server.options.PingExit = func(ctx context.Context, id string) (tailsplit.ExitPingResult, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > tailsplit.ExitPingTimeout {
			t.Fatal("browser ping has no bounded deadline")
		}
		return tailsplit.ExitPingResult{ID: id, LatencyMS: 18.75, Direct: false, Relay: "sin"}, nil
	}
	for _, test := range []struct {
		name, id string
		headers  map[string]string
		status   int
	}{
		{"missing capability", "100.64.0.10", map[string]string{"Authorization": ""}, 401},
		{"foreign origin", "100.64.0.10", map[string]string{"Origin": "https://other.example"}, 403},
		{"rebound host", "100.64.0.10", map[string]string{"Host": "other.example"}, 403},
		{"wrong content type", "100.64.0.10", map[string]string{"Content-Type": "text/plain"}, 415},
		{"arbitrary IP", "203.0.113.1", nil, 400},
		{"hostname injection", "127.0.0.1; ping example.com", nil, 400},
		{"offline", "100.64.0.20", nil, 409},
		{"empty", "", nil, 400},
		{"oversized", strings.Repeat("x", 2048), nil, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, body := request(t, server, "POST", "/api/exit-ping", map[string]string{"id": test.id}, test.headers)
			if status != test.status {
				t.Fatalf("status=%d body=%s", status, body)
			}
		})
	}
	if calls != 0 {
		t.Fatal("rejected request reached ping callback")
	}
	status, body := request(t, server, "POST", "/api/exit-ping", map[string]string{"id": "100.64.0.10"}, nil)
	var result tailsplit.ExitPingResult
	if json.Unmarshal(body, &result) != nil || status != 200 || calls != 1 || result.ID != "100.64.0.10" || result.LatencyMS != 18.75 || result.Direct || result.Relay != "sin" {
		t.Fatalf("ping contract mismatch: %d %s", status, body)
	}
	for _, raw := range []string{`{"id":"100.64.0.10","command":"ping"}`, `{"id":"100.64.0.10"}{}`, `null`, `[]`} {
		req := httptest.NewRequest("POST", "http://"+server.address+"/api/exit-ping", strings.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+server.token)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.handler().ServeHTTP(response, req)
		if response.Code != 400 {
			t.Fatalf("accepted invalid body %q: %d", raw, response.Code)
		}
	}
	if calls != 1 {
		t.Fatal("malformed body reached the ping callback")
	}
}

func TestExitPingAPIReportsCapabilityAndLocalizedBoundedErrors(t *testing.T) {
	server, _, _ := testServer(t)
	enablePingFixture(server)
	_, body := request(t, server, "GET", "/api/state", nil, nil)
	if !bytes.Contains(body, []byte(`"ping_available":false`)) {
		t.Fatal("advertised missing callback")
	}
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{tailsplit.ErrPingTarget, 400, "ping_target"},
		{tailsplit.ErrPingOffline, 409, "ping_offline"},
		{tailsplit.ErrPingBusy, 409, "ping_busy"},
		{tailsplit.ErrPingUnavailable, 503, "ping_unavailable"},
		{context.DeadlineExceeded, 504, "ping_timeout"},
		{context.Canceled, 408, "ping_canceled"},
		{errors.New("secret upstream https://login.tailscale.com/a/private"), 502, "ping_failed"},
	} {
		server.options.PingExit = func(context.Context, string) (tailsplit.ExitPingResult, error) {
			return tailsplit.ExitPingResult{}, test.err
		}
		for _, language := range []string{"en", "zh"} {
			status, body := request(t, server, "POST", "/api/exit-ping", map[string]string{"id": "100.64.0.10"}, map[string]string{"Accept-Language": language})
			var reply struct {
				Code, Error string
				I18n        map[string]string `json:"error_i18n"`
			}
			if json.Unmarshal(body, &reply) != nil || status != test.status || reply.Code != test.code || reply.Error != reply.I18n[language] || reply.I18n["en"] == reply.I18n["zh"] || reply.Error == "" {
				t.Fatalf("missing localized failure: %d %s", status, body)
			}
			if bytes.Contains(body, []byte("secret")) || bytes.Contains(body, []byte("login.tailscale.com")) {
				t.Fatal("upstream diagnostic exposed")
			}
		}
	}
	_, body = request(t, server, "GET", "/api/state", nil, nil)
	if !bytes.Contains(body, []byte(`"ping_available":true`)) {
		t.Fatal("usable callback not advertised")
	}
	server.options.Snapshot = func() tray.Snapshot { return tray.Snapshot{} }
	status, _ := request(t, server, "POST", "/api/exit-ping", map[string]string{"id": "100.64.0.10"}, nil)
	if status != 503 {
		t.Fatal("unavailable device accepted", status)
	}
}

func TestExitPingAPISerializesAndPropagatesRequestCancellation(t *testing.T) {
	server, _, _ := testServer(t)
	enablePingFixture(server)
	started := make(chan struct{})
	var calls atomic.Int32
	server.options.PingExit = func(ctx context.Context, _ string) (tailsplit.ExitPingResult, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return tailsplit.ExitPingResult{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("POST", "http://"+server.address+"/api/exit-ping", strings.NewReader(`{"id":"100.64.0.10"}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+server.token)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { server.handler().ServeHTTP(response, req); close(done) }()
	<-started
	status, body := request(t, server, "POST", "/api/exit-ping", map[string]string{"id": "100.64.0.10"}, nil)
	if status != 409 || !bytes.Contains(body, []byte("ping_busy")) || calls.Load() != 1 {
		t.Fatalf("concurrent ping allowed: %d %s", status, body)
	}
	cancel()
	select {
	case <-done:
		if response.Code != 408 {
			t.Fatal("cancel not propagated", response.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled request leaked a ping")
	}
}
