//go:build darwin

package main

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/idy/tailsplit/internal/locale"
	"github.com/idy/tailsplit/internal/tray"
)

func TestAppConfigurationPreservesUserEdits(t *testing.T) {
	directory := t.TempDir()
	template := filepath.Join(directory, "template.yaml")
	path := filepath.Join(directory, "settings", "config.yaml")
	if err := os.WriteFile(template, []byte("listen: 127.0.0.1:17890\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := initializeConfig(path, template); err != nil {
		t.Fatal(err)
	}
	if proxyAddress(path) != "127.0.0.1:17890" {
		t.Fatal("proxy address could not be copied")
	}
	if err := os.WriteFile(path, []byte("listen: 127.0.0.1:19999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := initializeConfig(path, template); err != nil {
		t.Fatal(err)
	}
	if proxyAddress(path) != "127.0.0.1:19999" {
		t.Fatal("app overwrote user configuration")
	}
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0o600 {
		t.Fatal("app config is not private")
	}
}

func TestLegacySharedDeviceNameMigratesWithoutChangingChosenNames(t *testing.T) {
	for name, want := range map[string]string{"letsolo-tailsplit": "", "my-work-mac": "my-work-mac"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		text := "tailscale:\n  hostname: " + name + "\n  exit_node: 100.64.0.8\ntun:\n  enabled: true\n"
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := initializeConfig(path, "unused-existing-template"); err != nil {
			t.Fatal(err)
		}
		preferences, err := tray.ReadPreferences(path)
		if err != nil || preferences.DeviceName != want || preferences.ExitNode != "100.64.0.8" {
			t.Fatal("legacy-name migration lost a user preference:", preferences, err)
		}
		data, _ := os.ReadFile(path)
		if !bytes.Contains(data, []byte("enabled: true")) {
			t.Fatal("device-name migration changed the selected TUN mode")
		}
	}
}

func TestTemplateIconIsValid(t *testing.T) {
	for _, connected := range []bool{false, true} {
		icon, err := png.Decode(bytes.NewReader(trayIcon(connected)))
		if err != nil || icon.Bounds().Dx() != 32 {
			t.Fatal("invalid tray icon")
		}
	}
}

type desktopActionFixture struct {
	snapshot tray.Snapshot
	calls    []string
	err      error
}

func (c *desktopActionFixture) Snapshot() tray.Snapshot { return c.snapshot }
func (c *desktopActionFixture) Start() error            { return c.record("connect") }
func (c *desktopActionFixture) StartLogin() error       { return c.record("start-login") }
func (c *desktopActionFixture) Stop() error             { return c.record("disconnect") }
func (c *desktopActionFixture) Logout() error           { return c.record("logout") }
func (c *desktopActionFixture) RefreshExitNodes() error { return c.record("refresh-exits") }
func (c *desktopActionFixture) record(action string) error {
	c.calls = append(c.calls, action)
	return c.err
}

func TestDesktopLoginUsesNativeLoginFlow(t *testing.T) {
	const link = "https://login.tailscale.com/a/window-test"
	for _, action := range []string{"login", "connect"} {
		t.Run(action, func(t *testing.T) {
			controller := &desktopActionFixture{snapshot: tray.Snapshot{State: "stopped", BackendState: "NeedsLogin"}}
			var flow tray.LoginFlow
			operation, err := desktopActionOperation(action, controller, &flow, nil)
			if err != nil || operation != nil || !slices.Equal(controller.calls, []string{"start-login"}) {
				t.Fatalf("login was not prepared on the event loop: calls %v, error %v", controller.calls, err)
			}
			if next := flow.Next(controller.snapshot); next != "" {
				t.Fatal("login opened before a fresh device link was ready")
			}
			controller.snapshot.LoginURL = link
			if next := flow.Next(controller.snapshot); next != link || !flow.Opening() {
				t.Fatal("window login did not use the existing default-browser flow")
			}
		})
	}
	controller := &desktopActionFixture{snapshot: tray.Snapshot{Active: true, State: "login", LoginURL: link}}
	var flow tray.LoginFlow
	operation, err := desktopActionOperation("login", controller, &flow, nil)
	if err != nil || operation != nil || len(controller.calls) != 0 || flow.Next(controller.snapshot) != link {
		t.Fatal("login did not reuse the active device session")
	}
}

func TestDesktopLoginFailureKeepsPrivateDetailsOutOfActionError(t *testing.T) {
	controller := &desktopActionFixture{err: errors.New("https://login.tailscale.com/a/private tskey-auth-private")}
	var flow tray.LoginFlow
	operation, err := desktopActionOperation("login", controller, &flow, nil)
	if operation != nil || err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "tskey-") {
		t.Fatalf("login error exposed helper details: %v", err)
	}
	if !flow.ShowMessage() || flow.Next(tray.Snapshot{LoginURL: "https://login.tailscale.com/a/stale"}) != "" {
		t.Fatal("failed login left a browser request pending")
	}
}

func TestDesktopActionsDeferWorkAndCancelLoginBeforeWorker(t *testing.T) {
	for _, action := range []string{"connect", "disconnect", "logout", "refresh-exits", "startup-on", "startup-off"} {
		t.Run(action, func(t *testing.T) {
			controller := &desktopActionFixture{}
			var flow tray.LoginFlow
			flow.Request()
			var startupCalls []bool
			operation, err := desktopActionOperation(action, controller, &flow, func(enabled bool) error {
				startupCalls = append(startupCalls, enabled)
				return nil
			})
			if err != nil || operation == nil {
				t.Fatalf("no serialized operation: %v", err)
			}
			if len(controller.calls) != 0 || len(startupCalls) != 0 {
				t.Fatal("a blocking operation ran on the event loop")
			}
			if action == "disconnect" || action == "logout" {
				if next := flow.Next(tray.Snapshot{LoginURL: "https://login.tailscale.com/a/stale"}); next != "" {
					t.Fatal("login intent was not canceled before dispatching the worker")
				}
			}
			if err := operation(); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(action, "startup-") {
				if !slices.Equal(startupCalls, []bool{action == "startup-on"}) || len(controller.calls) != 0 {
					t.Fatal("startup changed the connection")
				}
			} else if !slices.Equal(controller.calls, []string{action}) {
				t.Fatalf("unexpected controller operations: %v", controller.calls)
			}
			if action == "logout" && flow.Next(tray.Snapshot{LoginURL: "https://login.tailscale.com/a/new"}) != "" {
				t.Fatal("window logout automatically requested another browser login")
			}
		})
	}
}

func TestDesktopDisconnectCancelsPendingAuthorization(t *testing.T) {
	controller := &desktopActionFixture{snapshot: tray.Snapshot{State: "authorizing", Active: true, Mode: "tun"}}
	var flow tray.LoginFlow
	flow.Request()
	if desktopConfigurationBusy("disconnect", controller.Snapshot(), false, false, flow.Opening()) {
		t.Fatal("disconnect cannot cancel the pending service installation")
	}
	operation, err := desktopActionOperation("disconnect", controller, &flow, nil)
	if err != nil || operation == nil || len(controller.calls) != 0 {
		t.Fatal("disconnect was not deferred to the serialized settings worker:", err)
	}
	if flow.Next(tray.Snapshot{LoginURL: "https://login.tailscale.com/a/cancelled"}) != "" {
		t.Fatal("cancelling authorization retained a pending browser request")
	}
	if err := operation(); err != nil || !slices.Equal(controller.calls, []string{"disconnect"}) {
		t.Fatal("cancelling authorization did not stop the helper:", controller.calls, err)
	}
}

func TestDesktopConfigurationKeepsSerializationGuardsDuringAuthorization(t *testing.T) {
	snapshot := tray.Snapshot{State: "authorizing", Active: true}
	for _, action := range []string{"", "connect", "login", "logout", "refresh-exits", "startup-on", "startup-off"} {
		if !desktopConfigurationBusy(action, snapshot, false, false, false) {
			t.Fatalf("%q bypassed pending service authorization", action)
		}
	}
	for _, test := range []struct {
		name       string
		editing    bool
		loggingOut bool
		opening    bool
	}{
		{"editing", true, false, false},
		{"logging-out", false, true, false},
		{"opening-browser", false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !desktopConfigurationBusy("disconnect", snapshot, test.editing, test.loggingOut, test.opening) {
				t.Fatal("disconnect bypassed an active serialized operation")
			}
		})
	}
	if desktopConfigurationBusy("connect", tray.Snapshot{State: "stopped"}, false, false, false) {
		t.Fatal("an idle controller rejected a normal request")
	}
}

func TestDesktopConnectIsIdempotentWhileActiveAndUnknownActionsAreRejected(t *testing.T) {
	controller := &desktopActionFixture{snapshot: tray.Snapshot{Active: true}}
	var flow tray.LoginFlow
	if operation, err := desktopActionOperation("connect", controller, &flow, nil); operation != nil || err != nil {
		t.Fatal("connect tried to replace an active session")
	}
	if operation, err := desktopActionOperation("open-shell", controller, &flow, nil); operation != nil || err == nil {
		t.Fatal("unknown desktop action was accepted")
	}
	if len(controller.calls) != 0 {
		t.Fatal("an invalid action reached the controller")
	}
}

func TestDesktopStartupHandlesApprovalAndRegistrationErrors(t *testing.T) {
	old := uiLanguage
	t.Cleanup(func() { uiLanguage = old })
	uiLanguage = locale.NewStore(filepath.Join(t.TempDir(), "config.yaml"))
	for _, language := range []string{"en", "zh"} {
		if err := uiLanguage.Set(language); err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			name    string
			enabled bool
			status  tray.LoginItemStatus
			setErr  error
			approve bool
		}{
			{"enabled", true, tray.LoginItemEnabled, nil, false},
			{"approval", true, tray.LoginItemNeedsApproval, nil, true},
			{"cancel-approval", false, tray.LoginItemNeedsApproval, nil, false},
			{"disabled", false, tray.LoginItemDisabled, nil, false},
			{"registration-error", true, tray.LoginItemUnavailable, errors.New("fixture unavailable"), false},
		} {
			t.Run(language+"/"+test.name, func(t *testing.T) {
				sets, opens, reads := 0, 0, 0
				err := setDesktopStartup(test.enabled, func(enabled bool) error {
					sets++
					if enabled != test.enabled {
						t.Fatal("wrong registration request")
					}
					return test.setErr
				}, func() tray.LoginItemStatus {
					reads++
					return test.status
				}, func() { opens++ })
				if sets != 1 {
					t.Fatal("login item was not updated once")
				}
				if test.approve {
					want := "System Settings"
					if language == "zh" {
						want = "系统设置"
					}
					if err == nil || !strings.Contains(err.Error(), want) || opens != 1 || reads != 1 {
						t.Fatalf("missing localized approval instruction: %v, opened %d, read %d", err, opens, reads)
					}
				} else if !errors.Is(err, test.setErr) || opens != 0 || test.setErr != nil && reads != 0 {
					t.Fatalf("unexpected startup result: %v, opened %d, read %d", err, opens, reads)
				}
			})
		}
	}
}
