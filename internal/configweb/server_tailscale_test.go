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
	if _, err := os.Stat(tailsplit.RulesCachePath(config)); !os.IsNotExist(err) {
		t.Fatal("draft validation populated the saved subscription's cache:", err)
	}
	compiled, err := tailsplit.Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	if requestsA.Load() != 1 || requestsB.Load() != 1 ||
		!strings.Contains(string(compiled.JSON), `"saved.fixture.example"`) ||
		strings.Contains(string(compiled.JSON), `"draft.fixture.example"`) {
		t.Fatal("saved subscription reused an unsaved draft's routing policy")
	}
}

func TestConfiguredTailnetCanBeValidatedWithoutStartingIt(t *testing.T) {
	server, path, applied := testServer(t)
	state := filepath.Join(filepath.Dir(path), "preview-state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "gfwlist.txt"), []byte("[AutoProxy 0.2.9]\n||fixture.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := "version: 1\nstate_dir: ./preview-state\ntailscale:\n  exit_node: 100.64.0.10\ndns:\n  direct_ipv6: false\nrules:\n  gfwlist: builtin\ntun:\n  enabled: true\n"
	status, body := request(t, server, "POST", "/api/validate", map[string]string{"yaml": config}, nil)
	if status != 200 || applied.Load() != 0 {
		t.Fatal("configured Tailnet could not be checked without applying it:", string(body))
	}
}
