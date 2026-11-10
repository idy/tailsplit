package main

import (
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestFallbackMenuActionMatchesTheOverallSession(t *testing.T) {
	tests := []struct {
		snapshot tray.Snapshot
		action   string
	}{
		{tray.Snapshot{State: "stopped"}, "连接"},
		{tray.Snapshot{State: "authenticated", BackendState: "Running"}, "连接"},
		{tray.Snapshot{State: "stopped", BackendState: "NeedsLogin"}, "连接"},
		{tray.Snapshot{State: "running", Mode: "tun", Active: true}, "断开连接"},
		{tray.Snapshot{State: "running", Mode: "proxy", Active: true}, "断开连接"},
		{tray.Snapshot{State: "blocked", Mode: "tun", RoutingMode: "global", Active: true}, "断开连接"},
		{tray.Snapshot{State: "discovering", Active: true}, "断开连接"},
		{tray.Snapshot{State: "authenticating", Active: true}, "断开连接"},
		{tray.Snapshot{State: "failed"}, "连接"},
		// Cached connection labels cannot make a finished session look active.
		{tray.Snapshot{State: "running", Mode: "tun"}, "连接"},
	}
	for _, test := range tests {
		view := connectionPresentation(test.snapshot)
		if view.Action != test.action || view.Summary == "" {
			t.Fatalf("wrong connection action for %+v: %+v", test.snapshot, view)
		}
	}
}

func TestOnlyALiveSessionCountsAsConnected(t *testing.T) {
	for _, test := range []struct {
		snapshot  tray.Snapshot
		connected bool
	}{
		{tray.Snapshot{State: "running", Active: true, Mode: "tun"}, true},
		{tray.Snapshot{State: "running", Active: true, Mode: "proxy"}, true},
		{tray.Snapshot{State: "running", Active: true, Mode: "tun", RoutingMode: "global"}, true},
		{tray.Snapshot{State: "connecting", Active: true, Mode: "tun"}, false},
		{tray.Snapshot{State: "waiting-exit", Active: true, Mode: "proxy"}, false},
		{tray.Snapshot{State: "blocked", Active: true, Mode: "tun", RoutingMode: "global"}, false},
		{tray.Snapshot{State: "stopping", Active: true, Mode: "tun"}, false},
		{tray.Snapshot{State: "running", Active: true, Mode: "discovery"}, false},
		{tray.Snapshot{State: "stopped"}, false},
		// A finished session's cached state is not a connection.
		{tray.Snapshot{State: "running", Mode: "tun"}, false},
	} {
		if actual := isConnected(test.snapshot); actual != test.connected {
			t.Fatalf("connected=%v for %+v; want %v", actual, test.snapshot, test.connected)
		}
	}
}

func TestFallbackMenuIgnoresPrimaryDeviceLoginWhenTrafficUsesAnotherExit(t *testing.T) {
	for _, test := range []struct {
		snapshot tray.Snapshot
		summary  string
		action   string
	}{
		{tray.Snapshot{State: "running", Active: true, Mode: "tun", RoutingMode: "global"}, "Global proxy · Connected", "Disconnect"},
		{tray.Snapshot{State: "running", Active: true, Mode: "proxy"}, "Proxy connected", "Disconnect"},
		{tray.Snapshot{State: "blocked", Active: true, Mode: "tun"}, "Exit unavailable · Traffic blocked", "Disconnect"},
		{tray.Snapshot{State: "authorizing", Active: true, Mode: "tun"}, "Waiting for network service installation", "Disconnect"},
		{tray.Snapshot{State: "stopped"}, "Disconnected", "Connect"},
		{tray.Snapshot{State: "failed"}, "Connection failed; view logs", "Connect"},
	} {
		test.snapshot.BackendState = "NeedsLogin"
		test.snapshot.TrafficExit = "office"
		test.snapshot.Exits = map[string]tray.ExitSnapshot{"office": {BackendState: "Running"}}
		view := connectionPresentationForLanguage(test.snapshot, "en")
		if view.Summary != test.summary || view.Action != test.action {
			t.Fatalf("primary sign-in state overrode the overall session: %+v, want %q / %q", view, test.summary, test.action)
		}
	}
}
