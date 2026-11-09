package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appservice"
	"github.com/idy/tailsplit/internal/privatefs"
)

func TestInitDoesNotOverwriteAndUsesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	var out, diagnostic bytes.Buffer
	if err := execute([]string{"init", "-c", path}, &out, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if err := privatefs.CheckFile(path); err != nil {
		t.Fatal("init created a public config:", err)
	}
	if err := execute([]string{"init", "-c", path}, &out, &diagnostic); err == nil {
		t.Fatal("init overwrote the existing config")
	}
}

func TestRenderAndStrictCommandArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nstate_dir: ./state\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	if err := execute([]string{"render", "-c", path}, &out, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"mixed"`) {
		t.Fatal("render did not emit native sing-box config")
	}
	for _, args := range [][]string{
		{"unknown"}, {"rules"}, {"version", "extra"},
		{"render", "-c", path, "ignored"}, {"render", "--events", "-c", path},
	} {
		if err := execute(args, &out, &diagnostic); err == nil {
			t.Fatalf("ignored invalid arguments: %v", args)
		}
	}
}

func TestDeviceLogoutPassesServicePolicyWithoutChangingSavedAuthorization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("version: 1\nstate_dir: ./state\ntailscale:\n  auth_key_env: FIXTURE_OLD_NETWORK_KEY\n  tailnet: old.ts.net\n  exit_node: 100.64.0.1\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := tailsplit.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := appservice.ValidateAppConfig(config); err == nil {
		t.Fatal("fixture did not exercise the service's auth-key rejection")
	}
	logout := deviceLogoutConfig(config)
	if err := appservice.ValidateAppConfig(logout); err != nil {
		t.Fatal("device logout is still rejected by the service policy:", err)
	}
	if config.Tailscale.AuthKeyEnv != "FIXTURE_OLD_NETWORK_KEY" || logout.Tailscale.ExitNode != config.Tailscale.ExitNode || logout.Tailscale.Tailnet != config.Tailscale.Tailnet {
		t.Fatal("preparing logout changed the original configuration or device selection")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("preparing logout changed the saved configuration:", err)
	}
}

func TestParentExitCancelsBundledHelper(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var parent atomic.Int32
	parent.Store(100)
	done := make(chan struct{})
	go func() {
		watchParent(ctx, 100, func() int { return int(parent.Load()) }, cancel)
		close(done)
	}()
	parent.Store(1)
	select {
	case <-done:
		if ctx.Err() == nil {
			t.Fatal("helper survived its owning app")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parent-exit monitor did not stop")
	}
}

func TestAppRunValidatesSelectedTrafficExitBeforeLaunching(t *testing.T) {
	for _, test := range []struct {
		name, mode, selected, primaryNode, extraNode string
		wantLaunch                                   bool
	}{
		{"global extra with pending primary", "global", "office", "", "100.101.0.11", true},
		{"global extra cannot fall back", "global", "office", "100.64.0.10", "", false},
		{"global unknown cannot fall back", "global", "unknown", "100.64.0.10", "100.101.0.11", false},
		{"legacy default still requires primary", "", "", "", "100.101.0.11", false},
		{"global default still requires primary", "global", "", "", "100.101.0.11", false},
		{"split uses primary despite remembered extra", "split", "office", "100.64.0.10", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := tailsplit.Config{Tailscale: &tailsplit.TailscaleConfig{ExitNode: test.primaryNode},
				Exits: []tailsplit.ExitConfig{{ID: "office", ExitNode: test.extraNode}},
				Rules: tailsplit.RulesConfig{Mode: test.mode, GlobalExit: test.selected, ProxyDomains: []string{"proxy.example"}}}
			var out bytes.Buffer
			launched := false
			err := runAppCommand(context.Background(), config, &out, func(_ context.Context, executable string, received tailsplit.Config, output io.Writer) error {
				launched = true
				if executable == "" || output != &out || received.TrafficExitID() != config.TrafficExitID() || len(received.Exits) != 1 {
					t.Fatal("launch lost its saved selection or migration inventory")
				}
				return nil
			})
			if launched != test.wantLaunch || (err == nil) != test.wantLaunch {
				t.Fatalf("launched=%t error=%v, want launch=%t", launched, err, test.wantLaunch)
			}
			if !test.wantLaunch && !strings.Contains(out.String(), `"event":"error"`) {
				t.Fatal("rejected app launch did not emit its error event")
			}
		})
	}
}
