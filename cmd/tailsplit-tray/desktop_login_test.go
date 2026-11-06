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
	err      error
}

func (c *desktopLoginFixture) Snapshot() tray.Snapshot { return c.snapshot }

func (c *desktopLoginFixture) StartLogin() error {
	c.starts++
	if c.err != nil {
		return c.err
	}
	c.snapshot = tray.Snapshot{State: "starting", Mode: "login", Active: true}
	return nil
}

func TestDesktopLoginPreservesActiveAuthenticationRecovery(t *testing.T) {
	const link = "https://login.tailscale.com/a/desktop-recovery-fixture"
	for _, mode := range []string{"proxy", "tun"} {
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
				if controller.starts != 0 || !reflect.DeepEqual(controller.snapshot, before) {
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
