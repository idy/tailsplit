package main

import (
	"errors"
	"reflect"
	"testing"

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
