package main

import (
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestPrimaryMenuActionMatchesTheActualSession(t *testing.T) {
	tests := []struct {
		snapshot tray.Snapshot
		action   string
	}{
		{tray.Snapshot{State: "stopped"}, "连接"},
		{tray.Snapshot{State: "authenticated", BackendState: "Running"}, "连接"},
		{tray.Snapshot{State: "stopped", BackendState: "NeedsLogin"}, "登录 Tailscale…"},
		{tray.Snapshot{State: "running", Mode: "tun", Active: true}, "断开连接"},
		{tray.Snapshot{State: "running", Mode: "proxy", Active: true}, "断开连接"},
		{tray.Snapshot{State: "blocked", Mode: "tun", RoutingMode: "global", Active: true}, "断开连接"},
		{tray.Snapshot{State: "discovering", Active: true}, "取消"},
		{tray.Snapshot{State: "authenticating", Active: true}, "取消登录"},
		{tray.Snapshot{State: "failed"}, "重试连接"},
		// Cached connection labels cannot make a finished session look active.
		{tray.Snapshot{State: "running", Mode: "tun"}, "连接"},
	}
	for _, test := range tests {
		view := connectionPresentation(test.snapshot)
		if view.Action != test.action || view.Summary == "" {
			t.Fatalf("wrong primary action for %+v: %+v", test.snapshot, view)
		}
	}
	if got := shortMenuTitle("一个很长的设备名称用来验证菜单不会无限变宽12345678901234567890"); len([]rune(got)) > 26 {
		t.Fatal("long device name expands the top-level menu")
	}
}

func TestReconnectRemainsAvailableAfterFailure(t *testing.T) {
	for _, test := range []struct {
		snapshot tray.Snapshot
		enabled  bool
	}{
		{tray.Snapshot{State: "failed"}, true},
		{tray.Snapshot{State: "failed", Mode: "tun"}, true},
		{tray.Snapshot{State: "stopped"}, true},
		{tray.Snapshot{State: "authenticated", BackendState: "Running"}, true},
		{tray.Snapshot{State: "running", Active: true, Mode: "tun"}, true},
		{tray.Snapshot{State: "running", Active: true, Mode: "proxy"}, true},
		{tray.Snapshot{State: "discovering", Active: true, Mode: "discovery"}, false},
		{tray.Snapshot{State: "login", Active: true, Mode: "login"}, false},
		{tray.Snapshot{State: "authorizing", Active: true, Mode: "tun"}, false},
		{tray.Snapshot{State: "stopping", Active: true, Mode: "tun"}, false},
		{tray.Snapshot{State: "logging-out", Active: true}, false},
	} {
		if actual := canReconnect(test.snapshot); actual != test.enabled {
			t.Fatalf("Reconnect enabled=%v for %+v; want %v", actual, test.snapshot, test.enabled)
		}
	}
}
