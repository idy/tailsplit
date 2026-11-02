package configweb

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

const fixtureConfig = "# My local policy\nversion: 1\nlisten: 127.0.0.1:17890\nlog_level: info # Keep this comment\ndns:\n  direct_ipv6: false\nrules:\n  direct_domains: [example.com]\n"

func testServer(t *testing.T) (*Server, string, *atomic.Int32) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(fixtureConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	var applied atomic.Int32
	var mutex sync.Mutex
	server, err := Start(Options{
		ConfigPath: path,
		Snapshot:   func() tray.Snapshot { return tray.Snapshot{LoginURL: "https://secret.example/auth", State: "stopped"} },
		Apply: func(ctx context.Context, update func() error) error {
			mutex.Lock()
			defer mutex.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := update(); err != nil {
				return err
			}
			applied.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, path, &applied
}

func request(t *testing.T, server *Server, method, path string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, "http://"+server.address+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+server.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		if key == "Host" {
			req.Host = value
		} else {
			req.Header.Set(key, value)
		}
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err = io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

func TestLocalPageRequiresCapabilityAndRejectsForeignOrigins(t *testing.T) {
	server, path, _ := testServer(t)
	for _, test := range []struct {
		name, path string
		headers    map[string]string
		status     int
	}{
		{"missing capability", "/api/config", map[string]string{"Authorization": ""}, 401},
		{"wrong capability", "/api/config", map[string]string{"Authorization": "Bearer invalid"}, 401},
		{"foreign origin", "/api/config", map[string]string{"Origin": "https://other.example"}, 403},
		{"rebound host", "/api/config", map[string]string{"Host": "other.example"}, 403},
		{"static traversal", "/config.yaml", nil, 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, _ := request(t, server, "GET", test.path, nil, test.headers)
			if status != test.status {
				t.Fatalf("status %d, want %d", status, test.status)
			}
		})
	}
	status, page := request(t, server, "GET", "/", nil, map[string]string{"Authorization": ""})
	if status != 200 || !bytes.Contains(page, []byte("YAML 原文")) || bytes.Contains(page, []byte(server.token)) {
		t.Fatal("static page is missing or exposes the capability")
	}
	status, body := request(t, server, "GET", "/api/state", nil, nil)
	if status != 200 || bytes.Contains(body, []byte("secret.example")) || bytes.Contains(body, []byte("LoginURL")) {
		t.Fatal("state API leaked login material:", string(body))
	}
	for _, address := range []string{"0.0.0.0:0", "192.168.1.2:0", "localhost:0"} {
		if _, err := Start(Options{ConfigPath: path, Listen: address}); err == nil {
			t.Fatalf("accepted non-loopback binding %s", address)
		}
	}
}

func TestFormPreviewSaveAndExternalEditConflict(t *testing.T) {
	server, path, applied := testServer(t)
	status, body := request(t, server, "GET", "/api/config", nil, nil)
	var original document
	if err := json.Unmarshal(body, &original); err != nil || status != 200 || !original.Valid {
		t.Fatal("could not load document:", string(body), err)
	}
	status, body = request(t, server, "POST", "/api/preview", map[string]any{
		"yaml": original.YAML, "changes": map[string]any{"log_level": "debug", "rules.direct_domains": []string{"example.com", "doubao.com"}},
	}, nil)
	var preview document
	if err := json.Unmarshal(body, &preview); err != nil || status != 200 || !preview.Valid {
		t.Fatal("could not preview fields:", string(body), err)
	}
	if !strings.Contains(preview.YAML, "# Keep this comment") || !strings.Contains(preview.YAML, "direct_ipv6: false") {
		t.Fatal("preview lost comments or unrelated DNS policy")
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != fixtureConfig || applied.Load() != 0 {
		t.Fatal("preview changed the actual configuration")
	}
	status, body = request(t, server, "POST", "/api/config", map[string]string{"yaml": preview.YAML, "revision": original.Revision}, nil)
	if status != 200 || applied.Load() != 1 {
		t.Fatal("save did not apply once:", string(body))
	}
	config, err := tailsplit.Load(path)
	if err != nil || config.LogLevel != "debug" || len(config.Rules.DirectDomains) != 2 {
		t.Fatal("saved configuration differs from preview:", config, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("web save exposed private configuration")
	}
	status, _ = request(t, server, "POST", "/api/config", map[string]string{"yaml": original.YAML, "revision": original.Revision}, nil)
	if status != 409 || applied.Load() != 1 {
		t.Fatal("stale browser draft overwrote a later edit")
	}
}

func TestInvalidEditsNeverApplyAndBrokenYAMLRemainsReadable(t *testing.T) {
	server, path, applied := testServer(t)
	_, revision, err := tray.ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"listen: 0.0.0.0:1234\n", "unknown_setting: true\n", "dns:\n  direct: 172.31.255.2\n", "rules:\n  direct_ips: [1.2.3.4/24]\n", "listen: [invalid\n",
		"listen: 127.0.0.1:80\n", "dns:\n  listen: 127.0.0.1:80\n",
		"tailscale:\n  exit_node: 100.64.0.10\n  auth_key_env: TEST_AUTH_KEY\n", "tun:\n  name: utun99\n",
	} {
		status, _ := request(t, server, "POST", "/api/config", map[string]string{"yaml": text, "revision": revision}, nil)
		if status != 422 {
			t.Fatalf("invalid config status %d for %q", status, text)
		}
	}
	for _, changes := range []map[string]any{
		{"unlisted.field": "x"}, {"dns.fake_ip": "true"}, {"tun.mtu": 1400.5}, {"log_level": nil},
	} {
		status, _ := request(t, server, "POST", "/api/preview", map[string]any{"yaml": fixtureConfig, "changes": changes}, nil)
		if status != 400 {
			t.Fatal("accepted malformed form edits:", changes)
		}
	}
	data, _ := os.ReadFile(path)
	if string(data) != fixtureConfig || applied.Load() != 0 {
		t.Fatal("invalid edit reached the running session")
	}
	if err := os.WriteFile(path, []byte("dns: [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, body := request(t, server, "GET", "/api/config", nil, nil)
	var doc document
	if err := json.Unmarshal(body, &doc); err != nil || status != 200 || doc.Valid || doc.YAML != "dns: [broken" {
		t.Fatal("broken configuration cannot be repaired in the source editor:", string(body), err)
	}
}

func TestUnsupportedYAMLValuesRemainReadableInSourceEditor(t *testing.T) {
	server, path, _ := testServer(t)
	for _, text := range []string{
		"tun:\n  mtu: .nan\n", "tun:\n  mtu: .inf\n", "dns:\n  direct: -.inf\n",
		"rules:\n  invalid: {true: value}\n",
	} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, method := range []string{"GET", "POST"} {
			endpoint := "/api/config"
			var edit any
			if method == "POST" {
				endpoint, edit = "/api/preview", map[string]string{"yaml": text}
			}
			status, body := request(t, server, method, endpoint, edit, nil)
			var doc document
			if err := json.Unmarshal(body, &doc); err != nil || status != 200 || doc.Valid || doc.Parsed || doc.YAML != text {
				t.Fatalf("unsupported YAML source became unreadable: status=%d body=%s err=%v", status, body, err)
			}
		}
	}
}

func TestBusyApplyLeavesSavedConfigurationUntouched(t *testing.T) {
	server, path, _ := testServer(t)
	server.options.Apply = func(context.Context, func() error) error { return ErrBusy }
	_, revision, _ := tray.ReadConfigDocument(path)
	status, body := request(t, server, "POST", "/api/config", map[string]string{"yaml": strings.Replace(fixtureConfig, "log_level: info", "log_level: debug", 1), "revision": revision}, nil)
	if status != 409 || !bytes.Contains(body, []byte(`"code":"busy"`)) {
		t.Fatal("busy app was not reported:", string(body))
	}
	data, _ := os.ReadFile(path)
	if string(data) != fixtureConfig {
		t.Fatal("busy apply changed the file")
	}
}
