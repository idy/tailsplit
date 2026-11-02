package tailsplit

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/idy/tailsplit/internal/privatefs"
)

func TestRuleRefreshKeepsLastGoodCache(t *testing.T) {
	var answer atomic.Value
	answer.Store("[AutoProxy 0.2.9]\n||example.com\n@@||safe.example.com\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(answer.Load().(string)))
	}))
	defer server.Close()
	config, err := loadTestConfig(t, "tailscale:\n  exit_node: 100.64.0.10\nrules:\n  gfwlist: "+server.URL+"\nstate_dir: ./state\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(config); err == nil || !strings.Contains(err.Error(), "refresh") {
		t.Fatal("uncached URL must fail without doing a startup download")
	}
	info, err := RefreshRules(context.Background(), config, server.Client())
	if err != nil || info.Proxy != 1 || info.Direct != 1 {
		t.Fatalf("valid refresh failed: %+v, %v", info, err)
	}
	cache := filepath.Join(config.StateDir, "gfwlist.txt")
	before, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := privatefs.CheckFile(cache); err != nil {
		t.Fatal("rule cache is not owner-only:", err)
	}
	if _, err := Compile(config); err != nil {
		t.Fatal(err)
	}
	answer.Store("not an AutoProxy list")
	if _, err := RefreshRules(context.Background(), config, server.Client()); err == nil {
		t.Fatal("accepted invalid upstream rules")
	}
	after, _ := os.ReadFile(cache)
	if string(after) != string(before) {
		t.Fatal("failed refresh replaced the working rule cache")
	}
	answer.Store(base64.StdEncoding.EncodeToString([]byte("[AutoProxy 0.2.9]\n||next.example\n")))
	if _, err := RefreshRules(context.Background(), config, server.Client()); err != nil {
		t.Fatal(err)
	}
	answer.Store(strings.Repeat("x", (2<<20)+1))
	good, _ := os.ReadFile(cache)
	if _, err := RefreshRules(context.Background(), config, server.Client()); err == nil {
		t.Fatal("accepted oversized upstream")
	}
	after, _ = os.ReadFile(cache)
	if string(after) != string(good) {
		t.Fatal("oversized refresh replaced the working rule cache")
	}
}

func TestStateLockPreventsTwoRunners(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if err := privateState(state); err != nil {
		t.Fatal(err)
	}
	release, err := lockState(state)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := lockState(state); err == nil {
		_ = other()
		t.Fatal("two runners acquired the same state")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := lockState(state)
	if err != nil {
		t.Fatal(err)
	}
	_ = again()
}
