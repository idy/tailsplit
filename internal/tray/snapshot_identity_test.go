package tray

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStatusSnapshotsKeepDNSSuffixOnItsOwnDevice(t *testing.T) {
	controller, err := New(Options{Helper: "helper", ConfigPath: filepath.Join(t.TempDir(), "config.yaml"), LogPath: filepath.Join(t.TempDir(), "log")})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.log.Close()
	controller.command = &exec.Cmd{}
	controller.trafficExit = "office"
	controller.snapshot = Snapshot{State: "checking-exit", Mode: "tun", RoutingMode: "global"}
	controller.onEvent([]byte(`{"event":"status","backend_state":"Running","network_name":"example.com","magic_dns_suffix":"tail123.ts.net","hostname":"primary-device"}`))
	controller.onEvent([]byte(`{"event":"status","exit":"office","backend_state":"Running","network_name":"office.example","magic_dns_suffix":"tail456.ts.net","exit_online":true,"exit_visible":true}`))
	snapshot := controller.Snapshot()
	if snapshot.State != "running" || snapshot.NetworkName != "example.com" || snapshot.MagicDNSSuffix != "tail123.ts.net" {
		t.Fatalf("selected extra exit replaced primary DNS identity: %+v", snapshot)
	}
	if office := snapshot.Exits["office"]; office.NetworkName != "office.example" || office.MagicDNSSuffix != "tail456.ts.net" {
		t.Fatalf("additional exit DNS identity was lost: %+v", office)
	}
	snapshot.Exits["office"] = ExitSnapshot{MagicDNSSuffix: "changed.ts.net"}
	if controller.Snapshot().Exits["office"].MagicDNSSuffix != "tail456.ts.net" {
		t.Fatal("snapshot metadata shares the controller's exit map")
	}
	controller.onEvent([]byte(`{"event":"status","backend_state":"NeedsLogin","network_name":"example.com"}`))
	if got := controller.Snapshot(); got.MagicDNSSuffix != "" || got.Exits["office"].MagicDNSSuffix != "tail456.ts.net" || got.State != "running" {
		t.Fatalf("omitted primary DNS metadata affected another device: %+v", got)
	}
	controller.onEvent([]byte(`{"event":"status","exit":"office","backend_state":"NeedsLogin","network_name":"office.example"}`))
	if got := controller.Snapshot().Exits["office"]; got.MagicDNSSuffix != "" {
		t.Fatalf("old DNS metadata survived a new status without a suffix: %+v", got)
	}
}

func TestFocusedLoginPreservesSeparateDNSMetadata(t *testing.T) {
	controller := testController(t, "manual-events")
	controller.snapshot = Snapshot{State: "stopped", BackendState: "Running", NetworkName: "example.com", MagicDNSSuffix: "tail123.ts.net"}
	if err := controller.StartExitLogin("office"); err != nil {
		t.Fatal(err)
	}
	controller.onEvent([]byte(`{"event":"status","exit":"office","backend_state":"Running","network_name":"office.example","magic_dns_suffix":"tail456.ts.net"}`))
	controller.onEvent([]byte(`{"event":"authenticated","mode":"login"}`))
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	got := controller.Snapshot()
	if got.BackendState != "Running" || got.NetworkName != "example.com" || got.MagicDNSSuffix != "tail123.ts.net" ||
		got.Exits["office"].BackendState != "Running" || got.Exits["office"].MagicDNSSuffix != "tail456.ts.net" {
		t.Fatalf("focused login changed a device's DNS identity: %+v", got)
	}
}
