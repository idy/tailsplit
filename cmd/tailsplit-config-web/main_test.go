package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/configweb"
	"github.com/idy/tailsplit/internal/tray"
)

const previewFixtureConfig = `version: 1
tailscale:
  hostname: preview-laptop
  exit_node: 100.64.0.10
rules:
  mode: split
tun:
  enabled: false
`

func testDesktopPreview(t *testing.T) configweb.Options {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(previewFixtureConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return previewOptions(path, "127.0.0.1:0", true)
}

func previewAction(t *testing.T, options configweb.Options, action string) tray.Snapshot {
	t.Helper()
	if err := options.Action(context.Background(), action); err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return options.Snapshot()
}

func TestDesktopPreviewNetworkSwitchKeepsUnrelatedSettingsAndStaysDisconnected(t *testing.T) {
	options := testDesktopPreview(t)
	previewAction(t, options, "login")
	previewAction(t, options, "connect")
	before, err := os.ReadFile(options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := tray.PatchConfigDocument(append([]byte("# keep-preview-settings\n"), before...),
		tray.ConfigChange{Section: "tailscale", Key: "tailnet", Value: "old.tail.ts.net"},
		tray.ConfigChange{Section: "tailscale", Key: "auth_key_env", Value: "LEGACY_PREVIEW_AUTH"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(options.ConfigPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	switched := previewAction(t, options, "switch-network")
	if switched.Active || switched.Mode != "" || switched.State != "authenticated" || switched.NetworkName != "preview-work.ts.net" || switched.ExitNode != "" || switched.Hostname != "preview-laptop" {
		t.Fatalf("network switch retained an active session or lost the device settings: %+v", switched)
	}
	for _, exit := range switched.ExitNodes {
		if exit.Selected || exit.SelectedFor != "" {
			t.Fatal("network switch retained the previous exit selection:", exit)
		}
	}
	after, err := os.ReadFile(options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"old.tail.ts.net", "LEGACY_PREVIEW_AUTH", "exit_node: 100.64.0.10"} {
		if strings.Contains(string(after), old) {
			t.Fatalf("network switch retained old authorization configuration: %s", after)
		}
	}
	for _, kept := range []string{"# keep-preview-settings", "hostname: preview-laptop", "mode: split"} {
		if !strings.Contains(string(after), kept) {
			t.Fatalf("network switch discarded unrelated configuration: %s", after)
		}
	}
	if refreshed := previewAction(t, options, "refresh-exits"); !reflect.DeepEqual(refreshed, switched) {
		t.Fatalf("refresh restored the prior preview network: %+v", refreshed)
	}
}

func TestDesktopPreviewIdentityAndExitLifecycle(t *testing.T) {
	options := testDesktopPreview(t)
	initial := options.Snapshot()
	if initial.State != "stopped" || initial.Active || initial.BackendState != "NeedsLogin" || initial.Hostname != "" || initial.NetworkName != "" || len(initial.ExitNodes) != 0 {
		t.Fatalf("preview did not start signed out: %+v", initial)
	}
	for _, action := range []string{"connect", "refresh-exits"} {
		if err := options.Action(context.Background(), action); err == nil || !reflect.DeepEqual(initial, options.Snapshot()) {
			t.Fatalf("%s bypassed simulated sign-in", action)
		}
	}
	signedIn := previewAction(t, options, "login")
	if signedIn.State != "authenticated" || signedIn.Active || signedIn.BackendState != "Running" || signedIn.Hostname != "preview-laptop" || signedIn.NetworkName != "preview-tailnet.example" || signedIn.LoginURL != "" {
		t.Fatalf("unexpected simulated identity: %+v", signedIn)
	}
	if len(signedIn.ExitNodes) != 4 {
		t.Fatalf("unexpected discovery size: %+v", signedIn.ExitNodes)
	}
	online, offline, selected := 0, 0, 0
	for _, exit := range signedIn.ExitNodes {
		if !strings.HasPrefix(exit.Name, "preview-") {
			t.Fatalf("exit is not labeled as preview data: %+v", exit)
		}
		if exit.Online {
			online++
		} else {
			offline++
		}
		if exit.Selected {
			selected++
			if exit.ID != signedIn.ExitNode {
				t.Fatalf("wrong selected exit: %+v", exit)
			}
		}
	}
	if online != 3 || offline != 1 || selected != 1 {
		t.Fatalf("unexpected online/offline/selected counts: %d/%d/%d", online, offline, selected)
	}
	if refreshed := previewAction(t, options, "refresh-exits"); !reflect.DeepEqual(refreshed, signedIn) {
		t.Fatalf("idle discovery changed simulated identity or intent: %+v", refreshed)
	}
	copy := options.Snapshot()
	copy.ExitNodes[0].Name = "mutated"
	if !reflect.DeepEqual(options.Snapshot(), signedIn) {
		t.Fatal("snapshot exposes mutable preview exit data")
	}
	connected := previewAction(t, options, "connect")
	if connected.State != "running" || !connected.Active || connected.Mode != "proxy" || connected.ExitNode != "100.64.0.10" || connected.RoutingMode != "split" {
		t.Fatalf("connect did not use saved policy: %+v", connected)
	}
	if refreshed := previewAction(t, options, "refresh-exits"); !reflect.DeepEqual(refreshed, connected) {
		t.Fatalf("discovery interrupted simulated connection: %+v", refreshed)
	}
	disconnected := previewAction(t, options, "disconnect")
	if disconnected.State != "stopped" || disconnected.Active || disconnected.Mode != "" || disconnected.BackendState != "Running" || disconnected.Hostname != signedIn.Hostname || disconnected.NetworkName != signedIn.NetworkName || !reflect.DeepEqual(disconnected.ExitNodes, signedIn.ExitNodes) {
		t.Fatalf("disconnect discarded signed-in identity or exits: %+v", disconnected)
	}
	previewAction(t, options, "connect")
	loggedOut := previewAction(t, options, "logout")
	if loggedOut.State != "stopped" || loggedOut.Active || loggedOut.BackendState != "NeedsLogin" || loggedOut.Hostname != "" || loggedOut.NetworkName != "" || loggedOut.ExitNode != "" || len(loggedOut.ExitNodes) != 0 || loggedOut.LoginURL != "" {
		t.Fatalf("logout retained simulated identity or exits: %+v", loggedOut)
	}
	if err := options.Action(context.Background(), "connect"); err == nil {
		t.Fatal("connect retained a logged-out identity")
	}
	logs, err := options.Logs()
	if err != nil || !strings.Contains(logs, "simulation") || !strings.Contains(logs, "No proxy, TUN, network service or Tailnet connection is started.") {
		t.Fatalf("logs must make simulation explicit: %q, %v", logs, err)
	}
}

func TestDesktopPreviewApplyTracksSavedConfigurationAndIntent(t *testing.T) {
	for _, running := range []bool{false, true} {
		name := "idle"
		if running {
			name = "running"
		}
		t.Run(name, func(t *testing.T) {
			options := testDesktopPreview(t)
			before := previewAction(t, options, "login")
			if running {
				before = previewAction(t, options, "connect")
			}
			data, revision, err := tray.ReadConfigDocument(options.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			data, err = tray.PatchConfigDocument(data,
				tray.ConfigChange{Section: "tailscale", Key: "hostname", Value: "preview-renamed"},
				tray.ConfigChange{Section: "tailscale", Key: "exit_node", Value: "100.64.0.20"},
				tray.ConfigChange{Section: "rules", Key: "mode", Value: "global"},
				tray.ConfigChange{Section: "tun", Key: "enabled", Value: true},
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- options.Apply(ctx, func() error {
					return tray.SaveConfigDocument(options.ConfigPath, revision, data)
				})
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("Apply did not finish; preview mutex may have been locked twice")
			}
			after := options.Snapshot()
			wantMode := ""
			if running {
				wantMode = "tun"
			}
			if after.State != before.State || after.Active != running || after.Mode != wantMode || after.Hostname != "preview-renamed" || after.ExitNode != "100.64.0.20" || after.RoutingMode != "global" || after.NetworkName != before.NetworkName {
				t.Fatalf("Apply lost policy or connection intent: before=%+v after=%+v", before, after)
			}
			selected := 0
			for _, exit := range after.ExitNodes {
				if exit.Selected {
					selected++
					if exit.ID != after.ExitNode {
						t.Fatalf("Apply retained stale selection: %+v", exit)
					}
				}
			}
			if selected != 1 {
				t.Fatalf("selected exits after Apply: %d", selected)
			}
			_, savedRevision, err := tray.ReadConfigDocument(options.ConfigPath)
			if err != nil || savedRevision == revision {
				t.Fatalf("configuration did not change revision: %q, %v", savedRevision, err)
			}
		})
	}
}

func TestDesktopPreviewConnectReadsLatestSavedConfiguration(t *testing.T) {
	options := testDesktopPreview(t)
	previewAction(t, options, "login")
	data := strings.Replace(previewFixtureConfig, "100.64.0.10", "preview-tokyo-exit", 1)
	data = strings.Replace(data, "mode: split", "mode: global", 1)
	data = strings.Replace(data, "enabled: false", "enabled: true", 1)
	if err := os.WriteFile(options.ConfigPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	connected := previewAction(t, options, "connect")
	if connected.Mode != "tun" || connected.RoutingMode != "global" || connected.ExitNode != "preview-tokyo-exit" || !connected.ExitNodes[1].Selected || connected.ExitNodes[0].Selected {
		t.Fatalf("connect ignored saved policy or exit name: %+v", connected)
	}
}

func TestDesktopPreviewFailedOrCanceledOperationsKeepSnapshot(t *testing.T) {
	options := testDesktopPreview(t)
	before := previewAction(t, options, "login")
	failed := errors.New("save failed")
	if err := options.Apply(context.Background(), func() error { return failed }); !errors.Is(err, failed) || !reflect.DeepEqual(before, options.Snapshot()) {
		t.Fatalf("failed save changed preview state: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := options.Apply(ctx, func() error { t.Error("canceled save ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Apply: %v", err)
	}
	if err := options.Action(ctx, "logout"); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(before, options.Snapshot()) {
		t.Fatalf("canceled action changed preview state: %v", err)
	}
}

func TestDesktopPreviewDefaultExtraAndLocalRemovalStayDisconnected(t *testing.T) {
	options := testDesktopPreview(t)
	original, revision, _ := tray.ReadConfigDocument(options.ConfigPath)
	updated, err := tray.PatchConfigDocument(original,
		tray.ConfigChange{Key: "state_dir", Value: "./state"},
		tray.ConfigChange{Key: "exits", Value: []tailsplit.ExitConfig{{ID: "office", ExitNode: "100.101.0.11"}}},
		tray.ConfigChange{Section: "rules", Key: "default_exit", Value: "office"},
		tray.ConfigChange{Section: "rules", Key: "global_exit", Value: "office"},
		tray.ConfigChange{Section: "rules", Key: "proxy_domains", Value: []string{"proxy.example"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := tray.SaveConfigDocument(options.ConfigPath, revision, updated); err != nil {
		t.Fatal(err)
	}
	previewAction(t, options, "login-exit:office")
	connected := previewAction(t, options, "connect")
	if !connected.Active || connected.State != "running" || connected.BackendState != "NeedsLogin" {
		t.Fatalf("selected extra did not connect independently of primary: %+v", connected)
	}
	if err := options.RemoveExit(context.Background(), "main", "stale"); !errors.Is(err, tray.ErrConfigChanged) || !options.Snapshot().Active {
		t.Fatal("stale preview removal stopped a connection or lost conflict identity")
	}
	_, revision, _ = tray.ReadConfigDocument(options.ConfigPath)
	if err := options.RemoveExit(context.Background(), "main", revision); err != nil {
		t.Fatal(err)
	}
	removed := options.Snapshot()
	if removed.Active || removed.State != "stopped" || removed.Mode != "" || removed.BackendState != "" || removed.Exits["office"].BackendState != "Running" {
		t.Fatalf("local removal reconnected, promoted extra or lost remaining metadata: %+v", removed)
	}
	if connected := previewAction(t, options, "connect"); !connected.Active || connected.BackendState != "" {
		t.Fatalf("extras-only preview could not connect without inventing primary: %+v", connected)
	}
	_, revision, _ = tray.ReadConfigDocument(options.ConfigPath)
	if err := options.RemoveExit(context.Background(), "office", revision); err != nil {
		t.Fatal(err)
	}
	if options.Snapshot().Active {
		t.Fatal("last preview removal reconnected")
	}
	if err := options.Action(context.Background(), "connect"); err == nil {
		t.Fatal("empty-device preview silently routed dormant proxy policy directly")
	}
}
