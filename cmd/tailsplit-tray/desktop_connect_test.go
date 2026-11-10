package main

import (
	"errors"
	"slices"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

type desktopConnectFixture struct {
	desktopLoginFixture
	trafficExit string
	trafficErr  error
}

func (c *desktopConnectFixture) TrafficExitID() (string, error) {
	return c.trafficExit, c.trafficErr
}

func (c *desktopConnectFixture) Start() error {
	c.calls = append(c.calls, "connect")
	return c.err
}

func TestDesktopConnectUsesSelectedDeviceLoginOnBothHosts(t *testing.T) {
	const link = "https://login.tailscale.com/a/connect-fixture"
	for _, exit := range []string{"main", "office"} {
		t.Run(exit, func(t *testing.T) {
			controller := &desktopConnectFixture{trafficExit: exit}
			controller.snapshot = tray.Snapshot{State: "stopped", BackendState: "NeedsLogin",
				Exits: map[string]tray.ExitSnapshot{"office": {BackendState: "NeedsLogin"}}}
			var flow tray.LoginFlow
			operation, err := desktopConnectOperation(controller, &flow)
			want := "start-login"
			if exit != "main" {
				want = "start-exit-login:" + exit
			}
			if err != nil || operation != nil || !slices.Equal(controller.calls, []string{want}) {
				t.Fatalf("Connect did not start selected device login: %v, %v", controller.calls, err)
			}
			if flow.Next(controller.snapshot) != "" {
				t.Fatal("browser opened without a fresh link")
			}
			controller.snapshot.LoginURL = link
			if flow.Next(controller.snapshot) != link || !flow.Opening() {
				t.Fatal("Connect did not request browser authorization")
			}
			if flow.Next(controller.snapshot) != "" {
				t.Fatal("authorization opened more than once")
			}
		})
	}
}

func TestDesktopConnectReadyExtraIgnoresSignedOutPrimary(t *testing.T) {
	controller := &desktopConnectFixture{trafficExit: "office"}
	controller.snapshot = tray.Snapshot{BackendState: "NeedsLogin", Exits: map[string]tray.ExitSnapshot{"office": {BackendState: "Running"}}}
	var flow tray.LoginFlow
	flow.Request()
	operation, err := desktopConnectOperation(controller, &flow)
	if err != nil || operation == nil || len(controller.calls) != 0 {
		t.Fatal("connection was not deferred", err)
	}
	if err := operation(); err != nil || !slices.Equal(controller.calls, []string{"connect"}) {
		t.Fatal("ready selected device did not connect", err)
	}
	if flow.Next(tray.Snapshot{LoginURL: "https://login.tailscale.com/a/stale"}) != "" {
		t.Fatal("old browser intent survived Connect")
	}
}

func TestDesktopConnectPreservesActiveSessionAndConfigErrors(t *testing.T) {
	controller := &desktopConnectFixture{trafficExit: "office"}
	controller.snapshot.Active = true
	var flow tray.LoginFlow
	operation, err := desktopConnectOperation(controller, &flow)
	if err != nil || operation != nil || len(controller.calls) != 0 {
		t.Fatal("active session was replaced")
	}
	controller.snapshot.Active = false
	controller.trafficErr = errors.New("invalid gateway")
	operation, err = desktopConnectOperation(controller, &flow)
	if !errors.Is(err, controller.trafficErr) || operation != nil || len(controller.calls) != 0 {
		t.Fatal("invalid selection started networking")
	}
}
