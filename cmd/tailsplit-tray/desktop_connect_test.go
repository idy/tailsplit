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

func (c *desktopConnectFixture) StartRevision(string) error { return c.Start() }

func (c *desktopConnectFixture) Start() error {
	c.calls = append(c.calls, "connect")
	return c.err
}

func TestDesktopConnectKeepsTrafficIntentThroughAuthorization(t *testing.T) {
	const link = "https://login.tailscale.com/a/connect-fixture"
	for _, exit := range []string{"main", "office"} {
		t.Run(exit, func(t *testing.T) {
			controller := &desktopConnectFixture{trafficExit: exit}
			controller.snapshot = tray.Snapshot{State: "stopped", BackendState: "NeedsLogin",
				Exits: map[string]tray.ExitSnapshot{"office": {BackendState: "NeedsLogin"}}}
			var flow tray.LoginFlow
			operation, err := desktopConnectOperation(controller, &flow)
			if err != nil || operation == nil || len(controller.calls) != 0 {
				t.Fatal("traffic start not deferred", err)
			}
			if err := operation(); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(controller.calls, []string{"connect"}) {
				t.Fatal("Connect diverted into login-only helper", controller.calls)
			}
			controller.snapshot.Active, controller.snapshot.Mode = true, "tun"
			controller.snapshot.TrafficExit, controller.snapshot.State = exit, "login"
			if flow.Next(controller.snapshot) != "" {
				t.Fatal("worker changed browser intent")
			}
			if err := finishDesktopConnect(&flow, nil); err != nil {
				t.Fatal(err)
			}
			controller.snapshot.LoginURL = link
			if flow.Next(controller.snapshot) != link || !flow.Opening() {
				t.Fatal("traffic authorization did not open the browser")
			}
			flow.Complete(nil)
			controller.snapshot.LoginURL, controller.snapshot.State = "", "running"
			if flow.Next(controller.snapshot) != "" || len(controller.calls) != 1 {
				t.Fatal("authentication replaced the existing connection")
			}
		})
	}
}

func TestDesktopConnectCancellationAndFailuresDiscardBrowserIntent(t *testing.T) {
	for _, failure := range []error{nil, errors.New("start failed")} {
		var flow tray.LoginFlow
		flow.Request()
		if err := finishDesktopConnect(&flow, failure); !errors.Is(err, failure) {
			t.Fatal("start error lost")
		}
		if failure == nil {
			flow.Cancel()
		}
		if flow.Next(tray.Snapshot{State: "login", LoginURL: "https://login.tailscale.com/a/canceled"}) != "" {
			t.Fatal("canceled connection opened login")
		}
	}
}

func TestDesktopConnectOptionalSignedOutPrimaryUsesTrafficPreflight(t *testing.T) {
	controller := &desktopConnectFixture{trafficExit: "main"}
	controller.snapshot.BackendState = "NeedsLogin"
	var flow tray.LoginFlow
	operation, err := desktopConnectOperation(controller, &flow)
	if err != nil || operation == nil {
		t.Fatal("cached optional identity prevented traffic preflight", err)
	}
	if err := operation(); err != nil || !slices.Equal(controller.calls, []string{"connect"}) {
		t.Fatal("optional identity was forced into a login helper", err, controller.calls)
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
