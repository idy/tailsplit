//go:build with_gvisor

package configweb

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/privatefs"
	"github.com/idy/tailsplit/internal/tray"
)

func TestDraftSubscriptionValidationDoesNotReplaceSavedRules(t *testing.T) {
	var requestsA, requestsB atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var domain string
		switch r.URL.Path {
		case "/a":
			requestsA.Add(1)
			domain = "saved.fixture.example"
		case "/b":
			requestsB.Add(1)
			domain = "draft.fixture.example"
		default:
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "[AutoProxy 0.2.9]\n||"+domain+"\n")
	}))
	defer upstream.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = upstream.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = transport })

	server, path, applied := testServer(t)
	saved := "version: 1\nstate_dir: ./state\ntailscale:\n  exit_node: 100.64.0.10\nrules:\n  gfwlist: " + upstream.URL + "/a\n"
	if err := os.WriteFile(path, []byte(saved), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(filepath.Dir(path), "state")
	if err := privatefs.EnsureDir(state); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(state, "gfwlist.txt")
	legacyData := "[AutoProxy 0.2.9]\n||saved.fixture.example\n"
	if err := os.WriteFile(legacy, []byte(legacyData), 0o600); err != nil {
		t.Fatal(err)
	}
	draft := strings.Replace(saved, upstream.URL+"/a", upstream.URL+"/b", 1)
	status, body := request(t, server, "POST", "/api/validate", map[string]string{"yaml": draft}, nil)
	if status != http.StatusOK || applied.Load() != 0 || requestsB.Load() != 1 || requestsA.Load() != 0 {
		t.Fatalf("draft validation did not stay separate from saved policy: status=%d, body=%s", status, body)
	}
	status, body = request(t, server, "POST", "/api/config", map[string]string{"yaml": draft, "revision": "stale-revision"}, nil)
	if status != http.StatusConflict || applied.Load() != 0 {
		t.Fatalf("failed save did not preserve the saved policy: status=%d, body=%s", status, body)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != saved {
		t.Fatal("draft validation changed the saved configuration:", err)
	}
	config, err := tailsplit.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := os.ReadFile(tailsplit.RulesCachePath(config))
	if err != nil || string(migrated) != legacyData {
		t.Fatal("saved legacy subscription did not retain its own cache bytes:", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy shared cache remained available for another source:", err)
	}
	status, body = request(t, server, "POST", "/api/validate", map[string]string{"yaml": saved}, nil)
	if status != http.StatusOK || applied.Load() != 0 {
		t.Fatalf("migrated saved source could not validate offline: status=%d, body=%s", status, body)
	}
	compiled, err := tailsplit.Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	if requestsA.Load() != 0 || requestsB.Load() != 1 ||
		!strings.Contains(string(compiled.JSON), `"saved.fixture.example"`) ||
		strings.Contains(string(compiled.JSON), `"draft.fixture.example"`) {
		t.Fatal("saved subscription reused an unsaved draft's routing policy")
	}
}

func TestGlobalGatewayChangeRequiresExplicitDisconnect(t *testing.T) {
	server, path, applied := testServer(t)
	// A saved policy can drift while the old helper still uses main. Decisions
	// must use that helper's actual TrafficExit rather than the saved selection.
	original := "state_dir: ./state\ntailscale:\n  exit_node: 100.64.0.10\nexits:\n  - id: office\n    exit_node: 100.101.0.11\nrules:\n  mode: global\n  global_exit: office\ntun:\n  enabled: true\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	var active atomic.Bool
	active.Store(true)
	server.options.Snapshot = func() tray.Snapshot {
		return tray.Snapshot{Active: active.Load(), Mode: "tun", RoutingMode: tailsplit.RoutingGlobal, TrafficExit: tailsplit.MainExit}
	}
	_, revision, _ := tray.ReadConfigDocument(path)
	edit := map[string]any{"yaml": original, "revision": revision, "changes": map[string]any{"log_level": "debug"}}
	status, body := request(t, server, "POST", "/api/config", edit, nil)
	current, currentRevision, err := tray.ReadConfigDocument(path)
	if status != http.StatusUnprocessableEntity || applied.Load() != 0 || err != nil || string(current) != original || currentRevision != revision {
		t.Fatalf("save replaced the actual protected gateway without disconnect: %d %s, %v", status, body, err)
	}
	active.Store(false)
	status, body = request(t, server, "POST", "/api/config", edit, nil)
	if status != http.StatusOK || applied.Load() != 1 {
		t.Fatalf("explicitly disconnected gateway could not be saved: %d %s", status, body)
	}
}

func TestGlobalToSplitCanUseAnotherDefaultDevice(t *testing.T) {
	server, path, applied := testServer(t)
	original := "state_dir: ./state\ntailscale:\n  exit_node: 100.64.0.10\nexits:\n  - id: office\n    exit_node: 100.101.0.11\nrules:\n  mode: global\n  global_exit: main\n  default_exit: office\ntun:\n  enabled: true\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	server.options.Snapshot = func() tray.Snapshot {
		return tray.Snapshot{Active: true, Mode: "tun", RoutingMode: tailsplit.RoutingGlobal, TrafficExit: tailsplit.MainExit}
	}
	_, revision, _ := tray.ReadConfigDocument(path)
	status, body := request(t, server, "POST", "/api/config", map[string]any{
		"yaml": original, "revision": revision, "changes": map[string]any{"rules.mode": tailsplit.RoutingSplit},
	}, nil)
	if status != http.StatusOK || applied.Load() != 1 {
		t.Fatalf("explicit Rule-based mode switch could not use its own default: %d %s", status, body)
	}
	config, err := tailsplit.LoadDraft(path)
	if err != nil || config.Rules.Mode != tailsplit.RoutingSplit || config.TrafficExitID() != "office" {
		t.Fatalf("Split shortcut did not retain its independent default device: %+v, %v", config.Rules, err)
	}
}

func TestConfiguredTailnetCanBeValidatedWithoutStartingIt(t *testing.T) {
	server, path, applied := testServer(t)
	state := filepath.Join(filepath.Dir(path), "preview-state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tailsplit.RulesCachePath(tailsplit.Config{StateDir: state, Rules: tailsplit.RulesConfig{GFWList: "builtin"}}), []byte("[AutoProxy 0.2.9]\n||fixture.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := "version: 1\nstate_dir: ./preview-state\ntailscale:\n  exit_node: 100.64.0.10\ndns:\n  direct_ipv6: false\nrules:\n  gfwlist: builtin\ntun:\n  enabled: true\n"
	status, body := request(t, server, "POST", "/api/validate", map[string]string{"yaml": config}, nil)
	if status != 200 || applied.Load() != 0 {
		t.Fatal("configured Tailnet could not be checked without applying it:", string(body))
	}
}
