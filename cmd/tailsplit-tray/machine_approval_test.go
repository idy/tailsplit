package main

import (
	"testing"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

func TestGlobalProtectionRemainsVisibleWhileDeviceNeedsApproval(t *testing.T) {
	snapshot := tray.Snapshot{State: "blocked", Active: true, Mode: "tun", RoutingMode: tailsplit.RoutingGlobal, BackendState: "NeedsMachineAuth"}
	view := connectionPresentation(snapshot)
	if view.Summary != "出口不可用 · 已阻断" || view.Action != "断开连接" {
		t.Fatalf("approval hid Global protection: %+v", view)
	}
}

func TestPrimaryLoginApprovalIgnoresOtherTrafficOwner(t *testing.T) {
	for _, mode := range []string{"login", "discovery"} {
		snapshot := tray.Snapshot{State: "authenticating", Active: true, Mode: mode,
			TrafficExit: "office", BackendState: "NeedsMachineAuth",
			Exits: map[string]tray.ExitSnapshot{"office": {BackendState: "Running"}}}
		if view := connectionPresentation(snapshot); view.Summary != "等待管理员批准设备" {
			t.Fatalf("primary helper's approval attributed to traffic owner: %+v", view)
		}
		snapshot.BackendState = "Running"
		snapshot.Exits["office"] = tray.ExitSnapshot{BackendState: "NeedsMachineAuth"}
		if view := connectionPresentation(snapshot); view.Summary == "等待管理员批准设备" {
			t.Fatalf("unrelated device's approval attributed to primary helper: %+v", view)
		}
	}
}

func TestDeviceApprovalIsDistinctFromBrowserLogin(t *testing.T) {
	for _, focus := range []string{"", "office"} {
		snapshot := tray.Snapshot{State: "authenticating", Active: true, Mode: "login", Focus: focus, BackendState: "NeedsMachineAuth"}
		if focus != "" {
			snapshot.BackendState = "Running"
			snapshot.Exits = map[string]tray.ExitSnapshot{focus: {BackendState: "NeedsMachineAuth"}}
		}
		view := connectionPresentationForLanguage(snapshot, "en")
		if view.Summary != "Awaiting device approval" || view.Action != "Cancel sign-in" {
			t.Fatalf("wrong approval presentation: %+v", view)
		}
	}
}
