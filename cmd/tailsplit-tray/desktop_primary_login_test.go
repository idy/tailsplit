package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestDesktopPrimaryLoginReplacesAnotherReadyDevice(t *testing.T) {
	const mainLink = "https://login.tailscale.com/a/main-after-office"
	for _, mode := range []string{"proxy", "tun", "login"} {
		t.Run(mode, func(t *testing.T) {
			snapshot := tray.Snapshot{State: "running", Mode: mode, Active: true, TrafficExit: "office", BackendState: "NeedsLogin",
				Exits: map[string]tray.ExitSnapshot{"office": {BackendState: "Running"}}}
			if mode == "login" {
				snapshot.Focus, snapshot.State = "office", "authenticated"
			}
			controller := &desktopLoginFixture{snapshot: snapshot}
			var flow tray.LoginFlow
			if err := requestDesktopLogin(controller, &flow); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(controller.calls, []string{"stop", "start-login"}) {
				t.Fatal("primary login reused a helper that cannot authorize main", controller.calls)
			}
			if got := flow.Next(controller.snapshot); got != "" || !flow.ShowMessage() {
				t.Fatal("primary intent did not wait for its fresh authorization")
			}
			controller.snapshot = tray.Snapshot{State: "login", Mode: "login", Focus: "main", BackendState: "NeedsLogin", LoginURL: mainLink, Active: true}
			if got := flow.Next(controller.snapshot); got != mainLink || flow.Next(controller.snapshot) != "" {
				t.Fatal("primary authorization link did not open exactly once", got)
			}
		})
	}
}

func TestDesktopPrimaryLoginTransitionFailureClearsIntent(t *testing.T) {
	for _, phase := range []string{"stop", "start"} {
		t.Run(phase, func(t *testing.T) {
			controller := &desktopLoginFixture{snapshot: tray.Snapshot{State: "running", Mode: "tun", Active: true, TrafficExit: "office", BackendState: "NeedsLogin",
				Exits: map[string]tray.ExitSnapshot{"office": {BackendState: "Running"}}}}
			want := []string{"stop"}
			if phase == "stop" {
				controller.stopErr = errors.New("fixture stop failure")
			} else {
				controller.err = errors.New("fixture start failure")
				want = append(want, "start-login")
			}
			var flow tray.LoginFlow
			flow.Request()
			if err := requestDesktopLogin(controller, &flow); err == nil || !reflect.DeepEqual(controller.calls, want) {
				t.Fatal("failed primary transition continued with another device's helper", controller.calls, err)
			}
			if flow.Next(tray.Snapshot{BackendState: "NeedsLogin", LoginURL: "https://login.tailscale.com/a/stale-office"}) != "" {
				t.Fatal("failed primary transition retained browser intent")
			}
		})
	}
}

func TestDesktopPrimaryLoginPreservesActiveTrafficAuthorization(t *testing.T) {
	const link = "https://login.tailscale.com/a/office-recovery"
	for _, mode := range []string{"proxy", "tun", "login"} {
		for _, hasLink := range []bool{false, true} {
			snapshot := tray.Snapshot{State: "login", Mode: mode, Active: true, TrafficExit: "office", BackendState: "NeedsLogin",
				Exits: map[string]tray.ExitSnapshot{"office": {BackendState: "NeedsLogin"}}}
			if mode == "tun" {
				snapshot.State = "blocked"
			}
			if mode == "login" {
				snapshot.Focus = "office"
			}
			if hasLink {
				snapshot.LoginURL = link
			}
			controller := &desktopLoginFixture{snapshot: snapshot}
			var flow tray.LoginFlow
			if err := requestDesktopLogin(controller, &flow); err != nil || len(controller.calls) != 0 || !reflect.DeepEqual(controller.snapshot, snapshot) {
				t.Fatal("active traffic authorization was restarted", controller.calls, err)
			}
			if hasLink {
				if flow.Next(snapshot) != link || flow.Next(snapshot) != "" {
					t.Fatal("active authorization link did not open exactly once")
				}
				continue
			}
			if flow.Next(snapshot) != "" || !flow.ShowMessage() {
				t.Fatal("authentication intent did not wait for the active device")
			}
			// A valid saved session can finish without ever needing a browser URL.
			snapshot.Exits["office"] = tray.ExitSnapshot{BackendState: "Running"}
			snapshot.State = "running"
			if flow.Next(snapshot) != "" || flow.ShowMessage() || flow.Message() != "设备已登录" {
				t.Fatal("active device completion was attributed to the signed-out primary", mode, flow.Message())
			}
		}
	}
}
