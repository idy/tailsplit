package configweb

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
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
		{"logout", nil, http.StatusConflict},
	} {
		status, _ := request(t, server, "POST", "/api/action", map[string]string{"action": test.action}, test.headers)
		if status != test.want {
			t.Fatalf("%q: status %d, want %d", test.action, status, test.want)
		}
	}
	if calls != 2 {
		t.Fatalf("unauthorized/unknown action reached controller: %d calls", calls)
	}
	status, _ := request(t, server, "POST", "/api/action", map[string]string{"action": "connect", "path": "C:\\Windows"}, nil)
	if status != 400 || calls != 2 {
		t.Fatal("accepted an unknown authority field")
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
