package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/idy/tailsplit/internal/tray"
)

type desktopLoginFixture struct {
	snapshot tray.Snapshot
	starts   int
	stops    int
	calls    []string
	err      error
	stopErr  error
}

func (c *desktopLoginFixture) Snapshot() tray.Snapshot { return c.snapshot }

func (c *desktopLoginFixture) StartLogin() error {
	c.starts++
	c.calls = append(c.calls, "start-login")
	if c.err != nil {
		return c.err
	}
	if c.snapshot.Active {
		return errors.New("fixture helper is already active")
	}
	c.snapshot = tray.Snapshot{State: "starting", Mode: "login", Active: true}
	return nil
}

func (c *desktopLoginFixture) StartExitLogin(id string) error {
	c.starts++
	c.calls = append(c.calls, "start-exit-login:"+id)
	if c.err != nil {
		return c.err
	}
	if c.snapshot.Active {
		return errors.New("fixture helper is already active")
	}
	c.snapshot = tray.Snapshot{State: "starting", Mode: "login", Focus: id, Active: true}
	return nil
}

func (c *desktopLoginFixture) Stop() error {
	c.stops++
	c.calls = append(c.calls, "stop")
	if c.stopErr != nil {
		return c.stopErr
	}
	c.snapshot.Active = false
	c.snapshot.Mode = ""
	c.snapshot.State = "stopped"
	return nil
}

func TestDesktopCanceledLoginPreservesOperationRejection(t *testing.T) {
	var flow tray.LoginFlow
	flow.Request()
	err := finishDesktopExitLogin(&flow, tray.ErrOperationCanceled)
	if !errors.Is(err, tray.ErrOperationCanceled) || flow.ShowMessage() || flow.Next(tray.Snapshot{LoginURL: "https://login.tailscale.com/a/canceled"}) != "" {
		t.Fatal("canceled native login lost its guarded error or browser revocation", err, flow.Message())
	}
}

func TestRequiredExitLoginHelperProcess(t *testing.T) {
	if os.Getenv("TAILSPLIT_REQUIRED_LOGIN_HELPER") != "1" {
		return
	}
	if !slices.Contains(os.Args, "--exit") {
		fmt.Println(`{"event":"status","backend_state":"Running","network_name":"home.ts.net","exit_visible":true,"exit_online":true,"phase":"preflight"}`)
		fmt.Println(`{"event":"status","exit":"office","backend_state":"NeedsLogin","auth_url":"https://login.tailscale.com/a/discarded-preflight","phase":"preflight"}`)
		fmt.Println(`{"event":"error","message":"exit \"office\" needs sign-in; open this saved exit in Exit Nodes and sign in, then connect again; TUN was not enabled"}`)
		// A queued peer update must not hide the sign-in error during cleanup.
		fmt.Println(`{"event":"status","backend_state":"Running","exit_visible":true,"exit_online":true,"phase":"preflight"}`)
		os.Exit(3)
	}
	index := slices.Index(os.Args, "--exit")
	if index+1 >= len(os.Args) || os.Args[index+1] != "office" {
		os.Exit(4)
	}
	fmt.Println(`{"event":"preflight","mode":"login"}`)
	fmt.Println(`{"event":"status","exit":"office","backend_state":"NeedsLogin","auth_url":"https://login.tailscale.com/a/fresh-required-office"}`)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(os.Getenv("TAILSPLIT_REQUIRED_LOGIN_AUTHORIZED")); err == nil {
			// Explicit device login succeeds before its node is online.
			fmt.Println(`{"event":"status","exit":"office","backend_state":"Running","network_name":"office.ts.net","exit_online":false}`)
			fmt.Println(`{"event":"authenticated","mode":"login"}`)
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(5)
}

func TestDesktopRequiredSecondaryLoginRecoversThroughFocusedSignIn(t *testing.T) {
	directory := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "config.yaml")
	config := "tailscale:\n  exit_node: 100.64.0.10\nexits:\n  - id: office\n    exit_node: 100.64.0.20\nrules:\n  mode: split\n  default_exit: main\n  routes:\n    - exit: office\n      domains: [corp.example]\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	authorized := filepath.Join(directory, "authorized")
	controller, err := tray.New(tray.Options{
		Helper: executable, ConfigPath: configPath, LogPath: filepath.Join(directory, "core.log"),
		HelperArgs: []string{"-test.run=^TestRequiredExitLoginHelperProcess$", "--"},
		ExtraEnv:   []string{"TAILSPLIT_REQUIRED_LOGIN_HELPER=1", "TAILSPLIT_REQUIRED_LOGIN_AUTHORIZED=" + authorized},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	wait := func(state string) tray.Snapshot {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			snapshot := controller.Snapshot()
			if snapshot.State == state && !snapshot.Active {
				return snapshot
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("helper did not finish as %s: %+v", state, controller.Snapshot())
		return tray.Snapshot{}
	}
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	failed := wait("failed")
	if !strings.Contains(failed.Error, `exit "office"`) || !strings.Contains(failed.Error, "sign in") ||
		failed.BackendState != "Running" || failed.Exits["office"].BackendState != "NeedsLogin" || failed.LoginURL != "" {
		t.Fatal("failed preflight did not retain a recoverable saved-exit sign-in state", failed)
	}
	// Both desktop implementations use this operation for the web panel's
	// login-exit:office action. It must replace the failed helper, not connect.
	var flow tray.LoginFlow
	if err := requestDesktopExitLogin(controller, &flow, "office"); err != nil {
		t.Fatal("the saved exit's sign-in action could not recover", err)
	}
	const freshLink = "https://login.tailscale.com/a/fresh-required-office"
	link := ""
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if link = flow.Next(controller.Snapshot()); link != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if link != freshLink || !flow.Opening() || flow.Next(controller.Snapshot()) != "" {
		t.Fatal("explicit sign-in did not open the fresh office link exactly once", link, flow.Message(), controller.Snapshot())
	}
	flow.Complete(nil)
	if err := os.WriteFile(authorized, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	authenticated := wait("authenticated")
	if authenticated.BackendState != "Running" || authenticated.NetworkName != "home.ts.net" ||
		authenticated.Exits["office"].BackendState != "Running" || authenticated.Exits["office"].ExitOnline || authenticated.LoginURL != "" {
		t.Fatal("focused recovery did not authenticate office independently of main and node availability", authenticated)
	}
}

func TestDesktopLoginPreservesActiveAuthenticationRecovery(t *testing.T) {
	const link = "https://login.tailscale.com/a/desktop-recovery-fixture"
	for _, mode := range []string{"proxy", "tun", "login"} {
		for _, state := range []string{"login", "blocked"} {
			t.Run(mode+"/"+state, func(t *testing.T) {
				before := tray.Snapshot{
					State: state, Mode: mode, Active: true,
					BackendState: "NeedsLogin", LoginURL: link, RoutingMode: "global",
				}
				controller := &desktopLoginFixture{snapshot: before}
				var flow tray.LoginFlow
				if err := requestDesktopLogin(controller, &flow); err != nil {
					t.Fatal(err)
				}
				if controller.starts != 0 || controller.stops != 0 || !reflect.DeepEqual(controller.snapshot, before) {
					t.Fatalf("authentication recovery replaced the active session: %+v", controller)
				}
				if got := flow.Next(controller.snapshot); got != link {
					t.Fatalf("browser intent = %q, want the active helper's authorization URL", got)
				}
				if got := flow.Next(controller.snapshot); got != "" {
					t.Fatal("one login request opened the browser more than once")
				}
			})
		}
	}
}

func TestDesktopLoginReplacesActiveDiscoveryWithInteractiveLogin(t *testing.T) {
	const link = "https://login.tailscale.com/a/discovery-to-login-fixture"
	for _, backend := range []string{"", "NeedsLogin", "Running"} {
		t.Run(backend, func(t *testing.T) {
			controller := &desktopLoginFixture{snapshot: tray.Snapshot{
				State: "discovering", Mode: "discovery", Active: true, BackendState: backend,
			}}
			var flow tray.LoginFlow
			if err := requestDesktopLogin(controller, &flow); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(controller.calls, []string{"stop", "start-login"}) {
				t.Fatalf("discovery was not replaced before starting interactive login: %v", controller.calls)
			}
			if !controller.snapshot.Active || controller.snapshot.Mode != "login" {
				t.Fatalf("the interactive helper is not active: %+v", controller.snapshot)
			}
			if got := flow.Next(controller.snapshot); got != "" {
				t.Fatal("browser opened before the new login helper supplied a URL")
			}
			controller.snapshot = tray.Snapshot{State: "login", Mode: "login", Active: true, BackendState: "NeedsLogin", LoginURL: link}
			if got := flow.Next(controller.snapshot); got != link {
				t.Fatalf("new authorization URL did not fulfill the pending login: %q", got)
			}
			if got := flow.Next(controller.snapshot); got != "" {
				t.Fatal("discovery transition opened the browser more than once")
			}
		})
	}
}

func TestDesktopLoginDiscoveryTransitionFailuresClearBrowserIntent(t *testing.T) {
	for _, phase := range []string{"stop", "start"} {
		t.Run(phase, func(t *testing.T) {
			controller := &desktopLoginFixture{snapshot: tray.Snapshot{State: "discovering", Mode: "discovery", Active: true}}
			want := []string{"stop"}
			if phase == "stop" {
				controller.stopErr = errors.New("fixture stop failure")
			} else {
				controller.err = errors.New("fixture start failure")
				want = append(want, "start-login")
			}
			var flow tray.LoginFlow
			flow.Request()
			if err := requestDesktopLogin(controller, &flow); err == nil {
				t.Fatal("discovery-to-login failure was ignored")
			}
			if !reflect.DeepEqual(controller.calls, want) {
				t.Fatalf("unexpected helper operations after failure: %v", controller.calls)
			}
			if got := flow.Next(tray.Snapshot{BackendState: "NeedsLogin", LoginURL: "https://login.tailscale.com/a/stale-discovery-fixture"}); got != "" {
				t.Fatal("failed transition left a pending browser request")
			}
		})
	}
}

func TestDesktopLoginStartsOnlyAnIdleHelper(t *testing.T) {
	controller := &desktopLoginFixture{}
	var flow tray.LoginFlow
	if err := requestDesktopLogin(controller, &flow); err != nil {
		t.Fatal(err)
	}
	if controller.starts != 1 || !controller.snapshot.Active || controller.snapshot.Mode != "login" {
		t.Fatalf("idle login did not start a login helper: %+v", controller)
	}
	if link := flow.Next(controller.snapshot); link != "" {
		t.Fatal("login opened a browser before a fresh authorization URL arrived")
	}
}

func TestDesktopGlobalLoginRequiresSeparateDisconnect(t *testing.T) {
	for _, primary := range []bool{false, true} {
		snapshot := tray.Snapshot{State: "running", Mode: "tun", RoutingMode: "global", Active: true,
			TrafficExit: "office", Exits: map[string]tray.ExitSnapshot{"office": {BackendState: "Running"}}}
		controller := &desktopLoginFixture{snapshot: snapshot}
		var flow tray.LoginFlow
		flow.Request()
		var err error
		if primary {
			err = requestDesktopLogin(controller, &flow)
		} else {
			err = requestDesktopExitLogin(controller, &flow, "spare")
		}
		if !errors.Is(err, errDesktopGlobalLogin) || !strings.Contains(err.Error(), "Disconnect Global mode") || len(controller.calls) != 0 || !reflect.DeepEqual(controller.snapshot, snapshot) {
			t.Fatal("native login stopped Global without separate consent", primary, controller.calls, err)
		}
		if flow.Next(tray.Snapshot{BackendState: "NeedsLogin", LoginURL: "https://login.tailscale.com/a/stale"}) != "" || flow.Message() != errDesktopGlobalLogin.Error() {
			t.Fatal("rejected login left browser intent pending or hid the recovery action")
		}
	}
}

func TestDesktopGlobalPrimaryLoginRejectsUnknownOtherDevice(t *testing.T) {
	for _, backend := range []string{"", "Starting", "Stopped"} {
		snapshot := tray.Snapshot{State: "blocked", Mode: "tun", RoutingMode: "global", Active: true, TrafficExit: "office",
			Exits: map[string]tray.ExitSnapshot{"office": {BackendState: backend}}}
		controller := &desktopLoginFixture{snapshot: snapshot}
		var flow tray.LoginFlow
		if err := requestDesktopLogin(controller, &flow); !errors.Is(err, errDesktopGlobalLogin) || len(controller.calls) != 0 || !reflect.DeepEqual(controller.snapshot, snapshot) {
			t.Fatal("unknown other-device state did not require explicit Global disconnect", backend, controller.calls, err)
		}
	}
}

func TestDesktopCurrentExitAuthorizationDoesNotReplaceGlobalHelper(t *testing.T) {
	for _, state := range []string{"login", "blocked"} {
		for _, link := range []string{"", "https://login.tailscale.com/a/current-office"} {
			snapshot := tray.Snapshot{State: state, Mode: "tun", RoutingMode: "global", Active: true, TrafficExit: "office", LoginURL: link,
				Exits: map[string]tray.ExitSnapshot{"office": {BackendState: "NeedsLogin"}}}
			controller := &desktopLoginFixture{snapshot: snapshot}
			var flow tray.LoginFlow
			if err := requestDesktopExitLogin(controller, &flow, "office"); err != nil || len(controller.calls) != 0 || !reflect.DeepEqual(controller.snapshot, snapshot) {
				t.Fatal("current device recovery replaced Global helper", controller.calls, err)
			}
			if flow.Next(snapshot) != link || flow.Next(snapshot) != "" {
				t.Fatal("current authorization did not open exactly once")
			}
		}
	}
}

func TestDesktopExitLoginAfterExplicitDisconnectStaysStoppedOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		controller := &desktopLoginFixture{snapshot: tray.Snapshot{State: "stopped", RoutingMode: "global"}}
		if fail {
			controller.err = errors.New("login failed")
		}
		var flow tray.LoginFlow
		err := requestDesktopExitLogin(controller, &flow, "office")
		if (err != nil) != fail || !reflect.DeepEqual(controller.calls, []string{"start-exit-login:office"}) {
			t.Fatal("explicitly disconnected login did unexpected operations", controller.calls, err)
		}
		if fail && (controller.snapshot.Active || flow.Next(tray.Snapshot{BackendState: "NeedsLogin", LoginURL: "https://login.tailscale.com/a/stale"}) != "") {
			t.Fatal("failed login restarted traffic or retained browser intent")
		}
	}
}

func TestDesktopLoginStartFailureDoesNotLeaveBrowserIntent(t *testing.T) {
	controller := &desktopLoginFixture{err: errors.New("fixture start failure")}
	var flow tray.LoginFlow
	if err := requestDesktopLogin(controller, &flow); err == nil {
		t.Fatal("login helper startup failure was ignored")
	}
	if link := flow.Next(tray.Snapshot{BackendState: "NeedsLogin", LoginURL: "https://login.tailscale.com/a/stale-fixture"}); link != "" {
		t.Fatal("failed login startup left a pending browser request")
	}
}
