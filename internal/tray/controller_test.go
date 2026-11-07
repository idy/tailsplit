package tray

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("TAILSPLIT_TEST_HELPER")
	if mode == "" {
		return
	}
	for _, argument := range os.Args {
		if argument == "logout" {
			if mode == "logout-fail" {
				fmt.Fprintln(os.Stderr, "fixture logout failed")
				os.Exit(3)
			}
			fmt.Println("Signed out fixture embedded device")
			os.Exit(0)
		}
	}
	if expected := os.Getenv("TAILSPLIT_TEST_CWD"); expected != "" {
		current, err := os.Getwd()
		// macOS runners return /private/var/... from getcwd even when
		// testing.T.TempDir supplied the /var/... alias.
		expected, expectedErr := filepath.EvalSymlinks(expected)
		if err != nil || expectedErr != nil || current != expected {
			fmt.Fprintln(os.Stderr, "fixture helper working directory did not match its resolved config directory")
			os.Exit(4)
		}
	}
	if mode == "fail" {
		os.Exit(3)
	}
	if mode == "fail-once" {
		marker := os.Getenv("TAILSPLIT_TEST_FAILURE_MARKER")
		file, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = file.Close()
			fmt.Println(`{"event":"error","message":"fixture service unavailable"}`)
			os.Exit(3)
		}
		if !errors.Is(err, os.ErrExist) {
			os.Exit(4)
		}
	}
	signals := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if mode == "stubborn" {
		signal.Ignore(syscall.SIGTERM)
		signals = []os.Signal{os.Interrupt}
	}
	fmt.Fprintln(os.Stderr, "fixture core log")
	fmt.Println(`{"event":"ready"}`)
	if mode == "login" {
		fmt.Println(`{"event":"status","backend_state":"NeedsLogin","auth_url":"https://login.tailscale.com/a/test-login"}`)
	} else if mode == "discovery" {
		if !slices.Contains(os.Args, "discover") {
			fmt.Fprintln(os.Stderr, "fixture discovery selected a network or login command")
			os.Exit(4)
		}
		fmt.Println(`{"event":"status","mode":"discovery","backend_state":"Running","hostname":"saved-device","exit_nodes":[{"id":"100.64.0.2","name":"saved-exit","online":true}]}`)
		fmt.Println(`{"event":"discovered","mode":"discovery"}`)
		os.Exit(0)
	} else if mode != "manual-events" {
		fmt.Println(`{"event":"status","backend_state":"Running","exit_node":"fixture-exit","exit_online":true}`)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), signals...)
	defer cancel()
	<-ctx.Done()
	os.Exit(0)
}

func testController(t *testing.T, mode string) *Controller {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	controller, err := New(Options{
		Helper: executable, ConfigPath: filepath.Join(directory, "config with spaces.yaml"),
		LogPath:    filepath.Join(directory, "private", "core.log"),
		HelperArgs: []string{"-test.run=^TestHelperProcess$", "--"},
		ExtraEnv: []string{"TAILSPLIT_TEST_HELPER=" + mode, "TAILSPLIT_TEST_CWD=" + directory,
			"TAILSPLIT_TEST_FAILURE_MARKER=" + filepath.Join(directory, "failed-once")},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	return controller
}

func TestReconnectRetriesAnInactiveFailedSession(t *testing.T) {
	controller := testController(t, "fail-once")
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "failed")
	select {
	case <-controller.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the failed broker did not exit")
	}
	if controller.Snapshot().Active {
		t.Fatal("a failed broker still owns the session")
	}
	if err := controller.Restart(); err != nil {
		t.Fatal("Reconnect could not retry an inactive failed session:", err)
	}
	snapshot := waitState(t, controller, "running")
	if snapshot.Error != "" || !snapshot.Active {
		t.Fatal("Retry retained the failed session's error:", snapshot)
	}
	if err := controller.Restart(); err != nil {
		t.Fatal("Reconnect did not replace a running session:", err)
	}
	waitState(t, controller, "running")
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := controller.Restart(); err != nil {
		t.Fatal("Reconnect did not start a stopped session:", err)
	}
	waitState(t, controller, "running")
}

func waitState(t *testing.T, controller *Controller, state string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current := controller.Snapshot()
		if current.State == state {
			return current
		}
		time.Sleep(10 * time.Millisecond)
	}
	log, _ := os.ReadFile(controller.options.LogPath)
	t.Fatalf("state did not become %s: %+v; fixture log: %s", state, controller.Snapshot(), redactLog(string(log)))
	return Snapshot{}
}

func TestControllerExplicitStartStopAndRestart(t *testing.T) {
	controller := testController(t, "normal")
	if controller.Snapshot().State != "stopped" {
		t.Fatal("controller autostarted")
	}
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	status := waitState(t, controller, "running")
	if status.ExitNode != "fixture-exit" {
		t.Fatal("exit state was inferred instead of using the event")
	}
	if err := controller.Start(); err == nil {
		t.Fatal("allowed two helper processes")
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "stopped")
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "running")
	if err := controller.Close(); err != nil {
		t.Fatal(err)
	}
	if err := controller.Close(); err != nil {
		t.Fatal("close was not idempotent:", err)
	}
	if err := controller.Start(); err == nil {
		t.Fatal("restarted a closed app")
	}
}

func TestControllerLoginIsMemoryOnlyAndFailureIsVisible(t *testing.T) {
	controller := testController(t, "login")
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	if waitState(t, controller, "login").LoginURL != "https://login.tailscale.com/a/test-login" {
		t.Fatal("login URL was lost")
	}
	log, _ := os.ReadFile(controller.options.LogPath)
	if strings.Contains(string(log), "test-login") {
		t.Fatal("private status event was copied to the log")
	}
	_ = controller.Stop()
	failing := testController(t, "fail")
	if err := failing.Start(); err != nil {
		t.Fatal(err)
	}
	if waitState(t, failing, "failed").Error == "" {
		t.Fatal("helper failure was reported as stopped/connected")
	}
}

func TestLoginButtonDoesNotRunTheTUNInstallationPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS app command selection")
	}
	directory := t.TempDir()
	helper := filepath.Join(directory, "fixture-helper")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$1\" > selected-command\n" +
		"printf '%s\\n' '{\"event\":\"authenticated\",\"mode\":\"login\",\"backend_state\":\"Running\"}'\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(config, []byte("tun:\n  enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controller, err := New(Options{Helper: helper, ConfigPath: config, LogPath: filepath.Join(directory, "private.log")})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	if err := controller.StartLogin(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "authenticated")
	select {
	case <-controller.done:
	case <-time.After(time.Second):
		t.Fatal("authentication did not finish")
	}
	selected, _ := os.ReadFile(filepath.Join(directory, "selected-command"))
	if strings.TrimSpace(string(selected)) != "app-login" || controller.Snapshot().State != "authenticated" {
		t.Fatal("Login selected the network startup/install command or lost its completed status")
	}
}

func TestControllerReportsPrivilegeAndMissingTailnetExit(t *testing.T) {
	controller := testController(t, "normal")
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "running")
	controller.onEvent([]byte(`{"event":"authorizing","mode":"tun"}`))
	if got := controller.Snapshot(); got.State != "authorizing" || got.Mode != "tun" {
		t.Fatal("lost administrator authorization state:", got)
	}
	controller.onEvent([]byte(`{"event":"preflight","mode":"login"}`))
	if got := controller.Snapshot(); got.State != "authenticating" || got.Mode != "login" {
		t.Fatal("login-only startup was reported as an exit readiness check:", got)
	}
	controller.onEvent([]byte(`{"event":"status","backend_state":"Running","network_name":"original-tailnet","exit_visible":false,"phase":"preflight"}`))
	if controller.Snapshot().State != "authenticating" {
		t.Fatal("login-only status incorrectly required a visible exit")
	}
	controller.onEvent([]byte(`{"event":"preflight","mode":"tun"}`))
	if controller.Snapshot().State != "checking-exit" {
		t.Fatal("explicit TUN startup lost its exit readiness phase")
	}
	controller.onEvent([]byte(`{"event":"status","backend_state":"Running","network_name":"personal-tailnet","exit_visible":false}`))
	if got := controller.Snapshot(); got.State != "missing-exit" || got.NetworkName != "personal-tailnet" {
		t.Fatal("missing exit was hidden behind an offline message:", got)
	}
	controller.onEvent([]byte(`{"event":"status","backend_state":"Running","network_name":"original-tailnet","exit_visible":true}`))
	if controller.Snapshot().State != "waiting-exit" {
		t.Fatal("a visible but offline exit was reported as missing")
	}
	controller.onEvent([]byte(`{"event":"log","message":"Waiting for authentication: https://login.tailscale.com/a/secret tskey-auth-secret"}`))
	log, err := os.ReadFile(controller.options.LogPath)
	if err != nil || strings.Contains(string(log), "/a/secret") || strings.Contains(string(log), "tskey-") {
		t.Fatal("the privileged channel leaked login material into its log")
	}
}

func TestGlobalExitOutageKeepsTheSessionActiveUntilExplicitDisconnect(t *testing.T) {
	controller := testController(t, "normal")
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "running")
	controller.onEvent([]byte(`{"event":"ready","mode":"tun","routing_mode":"global"}`))
	controller.onEvent([]byte(`{"event":"status","backend_state":"Running","exit_online":false,"exit_visible":true,"phase":"preflight"}`))
	if controller.Snapshot().State == "blocked" {
		t.Fatal("preflight was reported as protected before a TUN exists")
	}
	controller.onEvent([]byte(`{"event":"status","backend_state":"Running","exit_online":false,"exit_visible":true}`))
	blocked := controller.Snapshot()
	if blocked.State != "blocked" || !blocked.Active || blocked.RoutingMode != "global" {
		t.Fatal("an offline exit stopped its TUN session or hid the blocked state:", blocked)
	}
	controller.onEvent([]byte(`{"event":"status","backend_state":"Stopped","exit_online":true}`))
	if controller.Snapshot().State != "blocked" {
		t.Fatal("a cached online peer hid a stopped Tailnet transport")
	}
	controller.onEvent([]byte(`{"event":"status","backend_state":"Running","exit_online":true,"exit_visible":true}`))
	if snapshot := controller.Snapshot(); snapshot.State != "running" || !snapshot.Active {
		t.Fatal("exit recovery required a new session:", snapshot)
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if controller.Snapshot().Active {
		t.Fatal("explicit disconnect did not release the global session")
	}
}

func TestForcedBrokerStopKeepsRestartAndLogoutAvailable(t *testing.T) {
	controller := testController(t, "stubborn")
	controller.stopWait = 80 * time.Millisecond
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "running")
	if err := controller.Stop(); err != nil {
		t.Fatal("a terminated broker prevented a follow-up action:", err)
	}
	if controller.Snapshot().State != "stopped" {
		t.Fatal("forced stop did not finish the owning broker")
	}
	if err := controller.Start(); err != nil {
		t.Fatal("a terminated broker blocked restart:", err)
	}
	waitState(t, controller, "running")
	if err := controller.Logout(); err != nil {
		t.Fatal("a terminated broker blocked device logout:", err)
	}
	if err := controller.Start(); err != nil {
		t.Fatal("logout left the controller unable to start again:", err)
	}
	waitState(t, controller, "running")
	log, err := os.ReadFile(controller.options.LogPath)
	if err != nil || !strings.Contains(string(log), "forced stop") {
		t.Fatal("the recoverable forced stop was hidden from diagnostics")
	}
}

func TestLogoutStopsTheCoreBeforeReauthentication(t *testing.T) {
	controller := testController(t, "normal")
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "running")
	if err := controller.Logout(); err != nil {
		t.Fatal(err)
	}
	if controller.Snapshot().State != "stopped" {
		t.Fatal("logout did not stop the owning core")
	}
	if err := controller.Start(); err != nil {
		t.Fatal("logout left the app unable to start a new login:", err)
	}
	waitState(t, controller, "running")
}

func TestSwitchNetworkClearsOldSelectionAndStartsOnlyLogin(t *testing.T) {
	controller := testController(t, "login")
	config := "# Keep my policy\ntailscale:\n  exit_node: 100.64.0.1\n  tailnet: old.ts.net\n  auth_key_env: OLD_NETWORK_KEY\nrules:\n  direct_domains: [example.com]\n"
	if err := os.WriteFile(controller.options.ConfigPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "login")
	controller.onEvent([]byte(`{"event":"status","backend_state":"Running","network_name":"old.ts.net","exit_node":"old-exit","exit_nodes":[{"id":"old-exit","name":"Old exit"}]}`))
	if err := controller.SwitchNetwork(); err != nil {
		t.Fatal(err)
	}
	snapshot := waitState(t, controller, "login")
	if snapshot.Mode != "login" || snapshot.NetworkName != "" || snapshot.ExitNode != "" || len(snapshot.ExitNodes) != 0 || snapshot.LoginURL == "" {
		t.Fatal("switch retained old identity or started the proxy:", snapshot)
	}
	data, _, err := ReadConfigDocument(controller.options.ConfigPath)
	if err != nil || !bytes.Contains(data, []byte("# Keep my policy")) || !bytes.Contains(data, []byte("[example.com]")) ||
		bytes.Contains(data, []byte("old.ts.net")) || bytes.Contains(data, []byte("100.64.0.1")) || bytes.Contains(data, []byte("OLD_NETWORK_KEY")) {
		t.Fatal("switch did not clear only the previous network selection:", string(data), err)
	}
}

func TestSwitchNetworkKeepsSelectionWhenLogoutFails(t *testing.T) {
	controller := testController(t, "logout-fail")
	original := []byte("tailscale:\n  exit_node: 100.64.0.1\n  tailnet: old.ts.net\n")
	if err := os.WriteFile(controller.options.ConfigPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "running")
	if err := controller.SwitchNetwork(); err == nil {
		t.Fatal("logout failure was hidden")
	}
	data, _, err := ReadConfigDocument(controller.options.ConfigPath)
	if err != nil || !bytes.Equal(original, data) {
		t.Fatal("failed logout changed the network selection:", err)
	}
	if snapshot := controller.Snapshot(); snapshot.Active || snapshot.LoginURL != "" || snapshot.State != "failed" {
		t.Fatal("failed logout started a new login:", snapshot)
	}
}

func TestReconfigurationStopsBeforeEditingAndRestartsAfterFailure(t *testing.T) {
	controller := testController(t, "normal")
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "running")
	updateErr := errors.New("fixture settings save failed")
	if err := controller.Reconfigure(func() error {
		if controller.Snapshot().Active {
			t.Fatal("configuration was edited while the old core still owned its state")
		}
		return updateErr
	}); !errors.Is(err, updateErr) {
		t.Fatal("reconfiguration failure was hidden:", err)
	}
	waitState(t, controller, "running")
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := controller.Reconfigure(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if controller.Snapshot().Active {
		t.Fatal("editing a disconnected device implicitly started a proxy")
	}
}

func TestDiscoveryNeverUsesTUNStartupAndKeepsPickerAfterCompletion(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS app command selection")
	}
	directory := t.TempDir()
	helper := filepath.Join(directory, "fixture-helper")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$1\" > selected-command\n" +
		"printf '%s\\n' '{\"event\":\"status\",\"mode\":\"discovery\",\"backend_state\":\"NeedsLogin\",\"exit_nodes\":[],\"auth_url\":\"https://login.tailscale.com/a/not-a-browser-request\"}'\n" +
		"printf '%s\\n' '{\"event\":\"discovered\",\"mode\":\"discovery\"}'\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(config, []byte("tun:\n  enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controller, err := New(Options{Helper: helper, ConfigPath: config, LogPath: filepath.Join(directory, "private.log")})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	if err := controller.RefreshExitNodes(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-controller.done:
	case <-time.After(3 * time.Second):
		t.Fatal("discovery waited for an interactive login")
	}
	selected, _ := os.ReadFile(filepath.Join(directory, "selected-command"))
	snapshot := controller.Snapshot()
	if strings.TrimSpace(string(selected)) != "app-discover" || snapshot.Active ||
		snapshot.State != "stopped" || snapshot.LoginURL != "" || snapshot.Mode != "" {
		t.Fatalf("discovery selected installation/startup or blocked later login: %+v", snapshot)
	}
	if err := controller.StartLogin(); err != nil {
		t.Fatal("discovery prevented a subsequent explicit login:", err)
	}
	_ = controller.Stop()
}

func TestControllerDiscoveryRestoresSavedLoginWithoutConnecting(t *testing.T) {
	controller := testController(t, "discovery")
	if err := controller.RefreshExitNodes(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-controller.done:
	case <-time.After(5 * time.Second):
		t.Fatal("discovery did not release its helper session")
	}
	snapshot := controller.Snapshot()
	if snapshot.State != "stopped" || snapshot.Active || snapshot.Mode != "" || snapshot.LoginURL != "" {
		t.Fatal("idle discovery started a connection or retained a login request:", snapshot)
	}
	if snapshot.BackendState != "Running" || snapshot.Hostname != "saved-device" ||
		len(snapshot.ExitNodes) != 1 || snapshot.ExitNodes[0].Name != "saved-exit" {
		t.Fatal("idle discovery did not restore the saved device's login and exit status:", snapshot)
	}
}

func TestPickerSnapshotsDoNotShareMutableSlices(t *testing.T) {
	controller := testController(t, "normal")
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "running")
	controller.onEvent([]byte(`{"event":"status","backend_state":"Running","exit_online":true,"exit_nodes":[{"id":"100.64.0.2","name":"second","online":true}]}`))
	snapshot := controller.Snapshot()
	snapshot.ExitNodes[0].Name = "mutated"
	if controller.Snapshot().ExitNodes[0].Name != "second" {
		t.Fatal("picker UI can race with and mutate the controller's state")
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if len(controller.Snapshot().ExitNodes) != 1 {
		t.Fatal("stopping the proxy discarded its discovered exits")
	}
}

func TestLoginFlowOpensAgainAfterTheControllerLogsOut(t *testing.T) {
	controller := testController(t, "login")
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	var flow LoginFlow
	flow.Request()
	first := waitState(t, controller, "login")
	if flow.Next(first) == "" {
		t.Fatal("initial login could not open its browser")
	}
	flow.Complete(nil)
	flow.Cancel()
	if err := controller.Logout(); err != nil {
		t.Fatal(err)
	}
	if controller.Snapshot().LoginURL != "" {
		t.Fatal("logout retained the previous client's login URL")
	}
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	// The UI requests the next browser open only after logout and restart
	// finish. The new URL may still arrive asynchronously after that request.
	flow.Request()
	second := waitState(t, controller, "login")
	if flow.Next(second) == "" {
		t.Fatal("logout made the next login click ineffective")
	}
}

func TestLoginFlowWaitsForFreshHelperAuthenticationAfterStop(t *testing.T) {
	const link = "https://login.tailscale.com/a/fresh-helper"
	for _, test := range []struct {
		name    string
		event   string
		wantURL string
	}{
		{"login-link", `{"event":"status","backend_state":"NeedsLogin","auth_url":"` + link + `"}`, link},
		{"running-status", `{"event":"status","backend_state":"Running"}`, ""},
		{"authenticated", `{"event":"authenticated","mode":"login"}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// This helper emits only ready; the test controls all backend events.
			controller := testController(t, "manual-events")
			if err := controller.Start(); err != nil {
				t.Fatal(err)
			}
			waitState(t, controller, "connecting")
			controller.onEvent([]byte(`{"event":"status","backend_state":"Running","exit_online":true}`))
			if snapshot := controller.Snapshot(); snapshot.State != "running" {
				t.Fatal("previous helper did not reach Running:", snapshot)
			}
			if err := controller.Stop(); err != nil {
				t.Fatal(err)
			}
			if snapshot := controller.Snapshot(); snapshot.Active || snapshot.BackendState != "Running" {
				t.Fatal("fixture did not retain the stopped helper's backend state:", snapshot)
			}
			if err := controller.StartLogin(); err != nil {
				t.Fatal(err)
			}
			var flow LoginFlow
			flow.Request()
			assertWaiting := func() {
				t.Helper()
				if value := flow.Next(controller.Snapshot()); value != "" || !flow.pending || flow.Opening() {
					t.Fatalf("login request did not wait for fresh authentication: pending=%t, message=%q, snapshot=%+v",
						flow.pending, flow.Message(), controller.Snapshot())
				}
			}
			// The desktop event loop checks immediately after starting login,
			// then checks again while the helper is preparing authentication.
			assertWaiting()
			waitState(t, controller, "connecting")
			assertWaiting()
			controller.onEvent([]byte(`{"event":"preflight","mode":"login"}`))
			assertWaiting()
			controller.onEvent([]byte(test.event))
			if value := flow.Next(controller.Snapshot()); value != test.wantURL || flow.pending {
				t.Fatal("fresh helper authentication did not resolve the pending request:", flow.Message())
			}
			if test.wantURL != "" {
				if !flow.Opening() || flow.Next(controller.Snapshot()) != "" {
					t.Fatal("the fresh login link did not open exactly once")
				}
			} else if flow.Opening() || flow.ShowMessage() || flow.Message() != "设备已登录" {
				t.Fatal("fresh authentication was not recognized as an already logged in device:", flow.Message())
			}
		})
	}
}

func TestLoginURLDoesNotOpenArbitrarySites(t *testing.T) {
	for _, bad := range []string{
		"http://login.tailscale.com/a/test", "https://evil.example/a/test",
		"https://login.tailscale.com.evil.example/a/test", "https://user@login.tailscale.com/a/test",
		"file:///tmp/test", "https://login.tailscale.com/not-login",
	} {
		if validLoginURL(bad) {
			t.Fatalf("accepted arbitrary login destination %q", bad)
		}
	}
}

func TestLogRotationAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.log")
	log, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	for i := 0; i < 2; i++ {
		if _, err := log.Write(bytes.Repeat([]byte("x"), 3<<20)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{path, path + ".1"} {
		stat, err := os.Stat(name)
		if err != nil || stat.Mode().Perm() != 0o600 || stat.Size() > 5<<20 {
			t.Fatalf("unsafe log state: %s %v", name, err)
		}
	}
}
