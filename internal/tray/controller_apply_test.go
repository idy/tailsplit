package tray

import (
	"errors"
	"testing"
	"time"

	"github.com/idy/tailsplit"
)

func TestNewTrafficSessionClearsEveryDevicesCachedAvailability(t *testing.T) {
	controller := testController(t, "manual-events")
	cached := ExitSnapshot{BackendState: "Running", NetworkName: "cached.example", MagicDNSSuffix: "cached.ts.net", Hostname: "cached-device",
		ExitNode: "100.101.0.11", ExitOnline: true, ExitVisible: true, PingAvailable: true,
		ExitNodes: []tailsplit.ExitNode{{ID: "100.101.0.11", Online: true}}}
	controller.snapshot = Snapshot{State: "stopped", BackendState: "Running", NetworkName: "home.example", PingAvailable: true,
		ExitNodes: []tailsplit.ExitNode{{ID: "100.64.0.10", Online: true}}, Exits: map[string]ExitSnapshot{"office": cached, "lab": cached}}
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	got := controller.Snapshot()
	if got.BackendState != "" || got.PingAvailable || len(got.ExitNodes) != 0 || got.NetworkName != "home.example" {
		t.Fatal("new traffic session reused the primary's availability", got)
	}
	for id, exit := range got.Exits {
		if exit.BackendState != "" || exit.PingAvailable || exit.ExitOnline || exit.ExitVisible || len(exit.ExitNodes) != 0 || exit.ExitNode != "" ||
			exit.NetworkName != cached.NetworkName || exit.MagicDNSSuffix != cached.MagicDNSSuffix || exit.Hostname != cached.Hostname {
			t.Fatalf("new session reused %s availability or lost saved identity: %+v", id, exit)
		}
	}
	controller.onEvent([]byte(`{"event":"status","backend_state":"Running","exit_online":true}`))
	controller.onEvent([]byte(`{"event":"status","exit":"office","phase":"isolated","backend_state":"","network_name":"cached.example"}`))
	got = controller.Snapshot()
	if got.State != "running" || got.Exits["office"].Phase != "isolated" || got.Exits["office"].BackendState != "" || got.Exits["lab"].PingAvailable {
		t.Fatal("unseen optional isolation invented login state or affected the selected device", got)
	}
}

func TestIsolationStatusClearsAvailabilityWithoutInventingSDKIdentity(t *testing.T) {
	controller := testController(t, "manual-events")
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	controller.mu.Lock()
	controller.trafficExit = "office"
	controller.snapshot.State = "running"
	controller.setExit("office", ExitSnapshot{BackendState: "Running", ExitOnline: true})
	controller.mu.Unlock()
	for _, event := range []string{
		`{"event":"status","phase":"isolated","backend_state":"Running","network_name":"home.example","magic_dns_suffix":"home.ts.net","hostname":"home-device","exit_node":"100.64.0.10","exit_online":true,"exit_visible":true,"ping_available":true,"exit_nodes":[{"id":"100.64.0.10","online":true}]}`,
		`{"event":"status","exit":"lab","phase":"isolated","backend_state":"NeedsLogin","network_name":"lab.example","magic_dns_suffix":"lab.ts.net","hostname":"lab-device","auth_url":"https://login.tailscale.com/a/unused","exit_online":true,"ping_available":true}`,
	} {
		controller.onEvent([]byte(event))
	}
	got := controller.Snapshot()
	if got.State != "running" || got.Phase != "isolated" || got.BackendState != "Running" || got.NetworkName != "home.example" || got.MagicDNSSuffix != "home.ts.net" ||
		got.Hostname != "home-device" || got.PingAvailable || got.ExitNode != "" || len(got.ExitNodes) != 0 || got.LoginURL != "" {
		t.Fatal("primary isolation changed its actual identity or retained availability", got)
	}
	lab := got.Exits["lab"]
	if lab.Phase != "isolated" || lab.BackendState != "NeedsLogin" || lab.NetworkName != "lab.example" || lab.MagicDNSSuffix != "lab.ts.net" || lab.Hostname != "lab-device" || lab.PingAvailable || lab.ExitOnline || lab.ExitVisible {
		t.Fatal("optional isolation changed its actual identity or retained availability", lab)
	}
}

func TestGlobalScopeInvalidatesOmittedDevicesAndFocusedLoginPreservesOthers(t *testing.T) {
	controller := testController(t, "manual-events")
	cached := ExitSnapshot{BackendState: "Running", NetworkName: "cached.example", PingAvailable: true,
		ExitNodes: []tailsplit.ExitNode{{ID: "100.101.0.11", Online: true}}}
	controller.snapshot = Snapshot{State: "stopped", BackendState: "Running", PingAvailable: true,
		Exits: map[string]ExitSnapshot{"office": cached, "lab": cached}}
	controller.invalidateDeviceStatuses(tailsplit.RoutingGlobal, "office", true)
	got := controller.Snapshot()
	if got.Phase != "omitted" || got.BackendState != "" || got.PingAvailable || got.Exits["lab"].Phase != "omitted" ||
		got.Exits["lab"].BackendState != "" || got.Exits["lab"].PingAvailable || got.Exits["office"].Phase != "" {
		t.Fatal("Global reused nonselected devices' old availability", got)
	}
	controller.snapshot.BackendState = "Running"
	controller.setExit("lab", cached)
	if err := controller.StartExitLogin("office"); err != nil {
		t.Fatal(err)
	}
	got = controller.Snapshot()
	if got.BackendState != "Running" || got.Exits["lab"].BackendState != "Running" || !got.Exits["lab"].PingAvailable || len(got.Exits["lab"].ExitNodes) != 1 {
		t.Fatal("focused login cleared another device's cached authentication", got)
	}
}

func TestCheckedReconfigureUsesCurrentSessionBeforeStopping(t *testing.T) {
	controller := testController(t, "normal")
	// An HTTP request can observe this inactive snapshot before tray Connect
	// acquires the controller. The checked apply must use the newer session.
	if controller.Snapshot().Active {
		t.Fatal("fixture must begin disconnected")
	}
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitState(t, controller, "running")
	controller.mu.Lock()
	command := controller.command
	controller.mu.Unlock()
	rejected := errors.New("candidate is an incomplete draft")
	updated := false
	err := controller.ReconfigureChecked(func(snapshot Snapshot) error {
		if !snapshot.Active || snapshot.Mode != "proxy" {
			t.Fatalf("checked apply did not observe concurrent Connect: %+v", snapshot)
		}
		return rejected
	}, func() error {
		updated = true
		return nil
	})
	controller.mu.Lock()
	unchanged := controller.command == command && !controller.stopping
	controller.mu.Unlock()
	if !errors.Is(err, rejected) || updated || !unchanged || !controller.Snapshot().Active {
		t.Fatalf("rejected candidate stopped or replaced the current helper: %v, updated=%v, unchanged=%v", err, updated, unchanged)
	}
}

func TestCheckedReconfigureBlocksPublicStartsUntilApplyFinishes(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "reconnect"}[active], func(t *testing.T) {
			controller := testController(t, "normal")
			if active {
				if err := controller.Start(); err != nil {
					t.Fatal(err)
				}
				waitState(t, controller, "running")
			}
			checked, releaseCheck := make(chan struct{}), make(chan struct{})
			updated, releaseUpdate := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- controller.ReconfigureChecked(func(snapshot Snapshot) error {
					close(checked)
					<-releaseCheck
					return nil
				}, func() error {
					close(updated)
					<-releaseUpdate
					return nil
				})
			}()
			releasedCheck, releasedUpdate := false, false
			defer func() {
				if !releasedCheck {
					close(releaseCheck)
				}
				if !releasedUpdate {
					close(releaseUpdate)
				}
			}()
			assertStartsBlocked := func() {
				t.Helper()
				for name, start := range map[string]func() error{
					"connect": controller.Start, "login": controller.StartLogin,
					"exit login": func() error { return controller.StartExitLogin("office") },
					"restart":    controller.Restart,
				} {
					if err := start(); !errors.Is(err, tailsplit.ErrPingBusy) {
						t.Fatalf("%s entered during checked apply: %v", name, err)
					}
				}
			}
			select {
			case <-checked:
			case <-time.After(5 * time.Second):
				t.Fatal("candidate check did not start")
			}
			assertStartsBlocked()
			close(releaseCheck)
			releasedCheck = true
			select {
			case <-updated:
			case <-time.After(5 * time.Second):
				t.Fatal("configuration update did not start")
			}
			assertStartsBlocked()
			if err := controller.RefreshExitNodes(); !errors.Is(err, tailsplit.ErrPingBusy) {
				t.Fatalf("discovery entered during the stopped update gap: %v", err)
			}
			close(releaseUpdate)
			releasedUpdate = true
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("internal reconnect was blocked by its own gate:", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("checked apply did not finish")
			}
			if active {
				waitState(t, controller, "running")
			} else if controller.Snapshot().Active {
				t.Fatal("an idle save started a helper")
			}
		})
	}
}
