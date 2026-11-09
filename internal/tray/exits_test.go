package tray

import (
	"bytes"
	"errors"
	"github.com/idy/tailsplit"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAdditionalExitEventsStayOnTheirOwnDevice(t *testing.T) {
	controller, err := New(Options{Helper: "helper", ConfigPath: filepath.Join(t.TempDir(), "config.yaml"), LogPath: filepath.Join(t.TempDir(), "log")})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.log.Close()
	controller.command = &exec.Cmd{}
	controller.snapshot = Snapshot{State: "running", Mode: "tun", BackendState: "Running", NetworkName: "home"}
	controller.onEvent([]byte(`{"event":"status","exit":"office","backend_state":"Running","network_name":"office.ts.net","exit_online":true,"ping_available":true,"exit_nodes":[{"id":"100.101.0.11","name":"gw","online":true}]}`))
	snapshot := controller.Snapshot()
	if snapshot.NetworkName != "home" || snapshot.State != "running" {
		t.Fatalf("an additional exit replaced the primary state: %+v", snapshot)
	}
	office := snapshot.Exits["office"]
	if office.NetworkName != "office.ts.net" || len(office.ExitNodes) != 1 || !office.PingAvailable {
		t.Fatalf("office state: %+v", office)
	}
	// Snapshots are copies; callers cannot mutate controller state.
	snapshot.Exits["office"] = ExitSnapshot{}
	if controller.Snapshot().Exits["office"].NetworkName != "office.ts.net" {
		t.Fatal("snapshot shares its exits map")
	}

	// A focused login drives the session state and its browser link only.
	controller.snapshot = Snapshot{State: "starting", Mode: "login", Focus: "lab", BackendState: "Running", Exits: controller.snapshot.Exits}
	controller.onEvent([]byte(`{"event":"status","exit":"lab","backend_state":"NeedsLogin","auth_url":"https://login.tailscale.com/a/abc123"}`))
	snapshot = controller.Snapshot()
	if snapshot.State != "login" || snapshot.LoginURL == "" || snapshot.BackendState != "Running" {
		t.Fatalf("focused login state: %+v", snapshot)
	}
	flow := LoginFlow{}
	flow.Request()
	if got := flow.Next(snapshot); got != snapshot.LoginURL {
		t.Fatalf("login flow must use the focused device's link, got %q", got)
	}
	controller.onEvent([]byte(`{"event":"authenticated","mode":"login"}`))
	snapshot = controller.Snapshot()
	if snapshot.Exits["lab"].BackendState != "Running" || snapshot.Exits["office"].NetworkName != "office.ts.net" {
		t.Fatalf("authenticated exit state: %+v", snapshot.Exits)
	}
}

func TestFocusedLoginPreservesPrimaryAuthentication(t *testing.T) {
	controller := testController(t, "manual-events")
	controller.snapshot = Snapshot{State: "stopped", BackendState: "Running", NetworkName: "home.ts.net"}
	if err := controller.StartExitLogin("office"); err != nil {
		t.Fatal(err)
	}
	if got := controller.Snapshot(); got.BackendState != "Running" || got.NetworkName != "home.ts.net" {
		t.Fatalf("starting another device's login cleared the primary identity: %+v", got)
	}
	controller.onEvent([]byte(`{"event":"status","exit":"office","backend_state":"NeedsLogin"}`))
	controller.onEvent([]byte(`{"event":"authenticated","mode":"login"}`))
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := controller.Snapshot(); got.BackendState != "Running" || got.Exits["office"].BackendState != "Running" {
		t.Fatalf("finishing a focused login lost a device's authentication: %+v", got)
	}
}

func TestFocusedLoginWaitsForFreshAdditionalDeviceState(t *testing.T) {
	const link = "https://login.tailscale.com/a/fresh-office"
	for _, test := range []struct {
		name, event, wantURL string
	}{
		{"expired session", `{"event":"status","exit":"office","backend_state":"NeedsLogin","auth_url":"` + link + `"}`, link},
		{"saved session", `{"event":"status","exit":"office","backend_state":"Running"}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller := testController(t, "manual-events")
			controller.snapshot = Snapshot{State: "stopped", BackendState: "Running", NetworkName: "home.ts.net",
				Exits: map[string]ExitSnapshot{
					"office": {BackendState: "Running", NetworkName: "office.ts.net"},
					"lab":    {BackendState: "Running", NetworkName: "lab.ts.net"},
				}}
			if err := controller.StartExitLogin("office"); err != nil {
				t.Fatal(err)
			}
			var flow LoginFlow
			flow.Request()
			assertWaiting := func() {
				t.Helper()
				snapshot := controller.Snapshot()
				if got := flow.Next(snapshot); got != "" || !flow.pending || flow.Opening() {
					t.Fatalf("cached Running consumed the new login intent: %+v", snapshot)
				}
				if snapshot.BackendState != "Running" || snapshot.NetworkName != "home.ts.net" ||
					snapshot.Exits["lab"].BackendState != "Running" || snapshot.Exits["lab"].NetworkName != "lab.ts.net" {
					t.Fatal("focused login changed another saved device", snapshot)
				}
			}
			assertWaiting()
			waitState(t, controller, "connecting")
			assertWaiting()
			controller.onEvent([]byte(`{"event":"preflight","mode":"login"}`))
			assertWaiting()
			controller.onEvent([]byte(test.event))
			if got := flow.Next(controller.Snapshot()); got != test.wantURL || flow.pending {
				t.Fatalf("fresh helper event did not complete the login request: got %q, pending=%t", got, flow.pending)
			}
			if flow.Next(controller.Snapshot()) != "" {
				t.Fatal("the same authorization link opened more than once")
			}
			if test.wantURL != "" && !flow.Opening() || test.wantURL == "" && (flow.Opening() || flow.ShowMessage()) {
				t.Fatal("fresh authentication result was not reflected in login flow", flow.Message())
			}
		})
	}
}

func TestStartupPeerDiscoveryStaysInCheckingUntilReady(t *testing.T) {
	for _, id := range []string{tailsplit.MainExit, "office"} {
		controller, err := New(Options{Helper: "helper", ConfigPath: filepath.Join(t.TempDir(), "config.yaml"), LogPath: filepath.Join(t.TempDir(), "log")})
		if err != nil {
			t.Fatal(err)
		}
		controller.command = &exec.Cmd{}
		controller.trafficExit = id
		controller.snapshot = Snapshot{State: "checking-exit", Mode: "tun", RoutingMode: "split"}
		exit := ""
		if id != tailsplit.MainExit {
			exit = `,"exit":"office"`
		}
		for _, readiness := range []string{`"exit_visible":false,"exit_online":false`, `"exit_visible":true,"exit_online":false`, `"exit_visible":true,"exit_online":true`} {
			controller.onEvent([]byte(`{"event":"status","backend_state":"Running","phase":"preflight",` + readiness + exit + `}`))
			if got := controller.Snapshot(); got.State != "checking-exit" {
				t.Fatalf("%s asked for a new choice before discovery completed: %+v", id, got)
			}
		}
		controller.onEvent([]byte(`{"event":"ready","mode":"tun"}`))
		controller.onEvent([]byte(`{"event":"status","backend_state":"Running","exit_visible":true,"exit_online":true` + exit + `}`))
		if got := controller.Snapshot(); got.State != "running" {
			t.Fatalf("%s did not continue its existing connection after discovery: %+v", id, got)
		}
		controller.onEvent([]byte(`{"event":"status","backend_state":"Running","exit_visible":false,"exit_online":false` + exit + `}`))
		if got := controller.Snapshot(); got.State != "missing-exit" {
			t.Fatal("real runtime disappearance must remain distinguishable", got)
		}
		_ = controller.log.Close()
	}
}

func TestLoginFlowWaitsForTheFocusedDevice(t *testing.T) {
	flow := LoginFlow{}
	flow.Request()
	// The primary device being signed in must not end an additional exit's login.
	snapshot := Snapshot{Focus: "lab", BackendState: "Running", Exits: map[string]ExitSnapshot{"lab": {BackendState: "NeedsLogin"}}}
	if got := flow.Next(snapshot); got != "" || !flow.pending {
		t.Fatalf("flow ended early: %q pending=%t", got, flow.pending)
	}
}

func TestGlobalSelectedExtraDrivesAggregateStateWithoutReplacingPrimary(t *testing.T) {
	controller, err := New(Options{Helper: "helper", ConfigPath: filepath.Join(t.TempDir(), "config.yaml"), LogPath: filepath.Join(t.TempDir(), "log")})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.log.Close()
	controller.command = &exec.Cmd{}
	controller.trafficExit = "office"
	controller.snapshot = Snapshot{State: "checking-exit", Mode: "tun", RoutingMode: "global", BackendState: "NeedsLogin",
		NetworkName: "primary.ts.net", Hostname: "primary-device", ExitNode: "primary-node"}
	for _, test := range []struct{ status, want string }{
		{`{"event":"status","exit":"office","backend_state":"Running","network_name":"office.ts.net","hostname":"office-device","exit_node":"office-node","exit_online":true,"exit_visible":true,"phase":"preflight"}`, "checking-exit"},
		{`{"event":"status","exit":"office","backend_state":"Running","network_name":"office.ts.net","hostname":"office-device","exit_node":"office-node","exit_online":true,"exit_visible":true,"ping_available":true}`, "running"},
		{`{"event":"status","exit":"office","backend_state":"Running","exit_online":false,"exit_visible":true}`, "blocked"},
		{`{"event":"status","exit":"office","backend_state":"NeedsLogin","auth_url":"https://login.tailscale.com/a/office"}`, "blocked"},
		{`{"event":"status","exit":"office","backend_state":"Running","exit_online":true,"exit_visible":true,"ping_available":true}`, "running"},
	} {
		controller.onEvent([]byte(test.status))
		snapshot := controller.Snapshot()
		if snapshot.State != test.want || !snapshot.Active || snapshot.Mode != "tun" || snapshot.RoutingMode != "global" {
			t.Fatalf("aggregate state did not follow selected device: %+v, want %q", snapshot, test.want)
		}
		if snapshot.NetworkName != "primary.ts.net" || snapshot.Hostname != "primary-device" || snapshot.ExitNode != "primary-node" || snapshot.BackendState != "NeedsLogin" {
			t.Fatalf("extra identity was attributed to primary row: %+v", snapshot)
		}
		if snapshot.Exits["office"].BackendState == "" {
			t.Fatal("selected device metadata was not retained in Exits")
		}
		// Other device events update metadata without changing protection state.
		controller.onEvent([]byte(`{"event":"status","exit":"spare","backend_state":"NeedsLogin"}`))
		if controller.Snapshot().State != test.want {
			t.Fatal("unrelated extra changed the Global connection state")
		}
	}
	controller.onEvent([]byte(`{"event":"status","backend_state":"NeedsLogin","network_name":"primary.ts.net"}`))
	if controller.Snapshot().State != "running" {
		t.Fatal("unrelated primary changed the selected extra's connection state")
	}
	controller.snapshot.BackendState, controller.snapshot.PingAvailable = "Running", true
	if controller.exitPingAvailableLocked("") {
		t.Fatal("cached primary metadata allows ping through a device omitted by Global")
	}
	if controller.exitPingAvailableLocked("spare") {
		t.Fatal("unrelated extra allows ping during Global connection")
	}
}

func TestTrafficExitReadsSettingsSavedWhileIdle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	controller, err := New(Options{Helper: "helper", ConfigPath: path, LogPath: filepath.Join(t.TempDir(), "log")})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.log.Close()
	controller.snapshot = Snapshot{State: "stopped", RoutingMode: "split", BackendState: "NeedsLogin"}
	controller.trafficExit = "main"
	for _, test := range []struct{ policy, want string }{
		{"mode: global\n  global_exit: office", "office"},
		{"mode: split\n  global_exit: office", "main"},
		{"mode: global", "main"},
	} {
		text := "state_dir: ./state\ntailscale: {}\nexits:\n  - id: office\n    exit_node: 100.101.0.11\nrules:\n  " + test.policy + "\n"
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if id, err := controller.TrafficExitID(); err != nil || id != test.want {
			t.Fatalf("saved traffic exit=%q error=%v, want %q", id, err, test.want)
		}
	}
	if err := os.WriteFile(path, []byte("rules: [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.TrafficExitID(); err == nil {
		t.Fatal("invalid saved configuration silently fell back to primary")
	}
}

func TestSplitDefaultExtraDrivesAggregateState(t *testing.T) {
	controller, err := New(Options{Helper: "helper", ConfigPath: filepath.Join(t.TempDir(), "config.yaml"), LogPath: filepath.Join(t.TempDir(), "log")})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.log.Close()
	controller.command = &exec.Cmd{}
	controller.trafficExit = "office"
	controller.snapshot = Snapshot{State: "checking-exit", Mode: "proxy", RoutingMode: "split", BackendState: "NeedsLogin"}
	controller.onEvent([]byte(`{"event":"status","exit":"office","backend_state":"Running","exit_online":true,"exit_visible":true,"network_name":"office.ts.net"}`))
	if got := controller.Snapshot(); got.State != "running" || got.BackendState != "NeedsLogin" || got.NetworkName != "" || got.Exits["office"].NetworkName != "office.ts.net" {
		t.Fatalf("Split aggregate status or primary identity is wrong: %+v", got)
	}
	controller.onEvent([]byte(`{"event":"status","backend_state":"NeedsLogin"}`))
	if controller.Snapshot().State != "running" {
		t.Fatal("unrelated primary displaced selected Split default state")
	}
}

const removalFixture = `# retained local policy
state_dir: ./state
tailscale:
  hostname: original-primary
  exit_node: 100.64.0.10
exits:
  - id: office
    hostname: original-office
    exit_node: 100.101.0.11
  - id: lab
    hostname: original-lab
    exit_node: 100.102.0.12
rules:
  default_exit: main
  global_exit: main
  source: ./missing-rules.txt
  proxy_domains: [proxy.example]
  direct_domains: [local.example]
  final: office
  routes:
    - exit: main
      domains: [main.example]
    - exit: office
      domains: [office.example]
    - exit: lab
      domains: [lab.example]
`

func TestRemoveExitMainExtraAndLastPreservesDormantPolicyAndStatePaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(removalFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := tailsplit.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	originalPaths := map[string]string{}
	for _, id := range config.ExitIDs() {
		originalPaths[id] = config.ExitStateDir(id)
	}
	for _, id := range []string{"main", "office", "lab"} {
		_, revision, _ := ReadConfigDocument(path)
		var calls []string
		if err := RemoveExitDocument(path, id, revision, func(target string) error {
			calls = append(calls, target)
			return nil
		}); err != nil {
			t.Fatalf("remove %s: %v", id, err)
		}
		if len(calls) != 1 || calls[0] != id {
			t.Fatalf("removal did not focus exactly its local client: %v", calls)
		}
		config, err = tailsplit.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, remaining := range config.ExitIDs() {
			if config.ExitStateDir(remaining) != originalPaths[remaining] || remaining == id {
				t.Fatal("removal renamed/promoted remaining device state or retained its target")
			}
		}
		if config.Tailscale != nil {
			t.Fatal("main removal promoted another device into primary identity")
		}
		want := tailsplit.UnsetExit
		if config.Rules.DefaultExit != want || config.Rules.GlobalExit != want {
			t.Fatalf("selected exit was not reset deliberately: %+v", config.Rules)
		}
		if len(config.Rules.ProxyDomains) != 1 || config.Rules.ProxyDomains[0] != "proxy.example" ||
			len(config.Rules.DirectDomains) != 1 || config.Rules.GFWList == "" {
			t.Fatal("removal discarded dormant Split policy")
		}
		for _, route := range config.Rules.Routes {
			if route.Exit == id {
				t.Fatal("explicit group still targets a removed device")
			}
		}
		if id == "office" && config.Rules.Final != "direct" {
			t.Fatal("explicit final pointing to removed extra was not reset to direct")
		}
	}
	if len(config.ExitIDs()) != 0 || len(config.Rules.Routes) != 0 {
		t.Fatal("last local device was not removed")
	}
	if err := tailsplit.ValidateTrafficExit(config); err == nil {
		t.Fatal("last removal silently converted dormant proxy policy to direct routing")
	}
	data, _, _ := ReadConfigDocument(path)
	if !bytes.Contains(data, []byte("# retained local policy")) || !bytes.Contains(data, []byte("source: ./missing-rules.txt")) {
		t.Fatal("removal rewrote unrelated comments or relative policy paths")
	}
}

func TestRemoveExitChecksRevisionBeforeLogoutAndAgainBeforeSave(t *testing.T) {
	for _, failure := range []string{"stale", "unknown", "logout", "concurrent edit"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			original := []byte(removalFixture)
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			_, revision, _ := ReadConfigDocument(path)
			id := "main"
			if failure == "stale" {
				revision = "stale"
			}
			if failure == "unknown" {
				id = "../not-a-device"
			}
			calls := 0
			want := original
			err := RemoveExitDocument(path, id, revision, func(target string) error {
				calls++
				if target != "main" {
					t.Fatal("logout changed target")
				}
				if failure == "logout" {
					return errors.New("simulated local logout failure")
				}
				want = append(append([]byte{}, original...), []byte("# concurrent edit\n")...)
				return os.WriteFile(path, want, 0o600)
			})
			if err == nil {
				t.Fatal("failed removal was accepted")
			}
			if (failure == "stale" || failure == "concurrent edit") && !errors.Is(err, ErrConfigChanged) {
				t.Fatalf("revision conflict lost its error identity: %v", err)
			}
			if (failure == "stale" || failure == "unknown") && calls != 0 {
				t.Fatal("invalid removal signed a device out before checking its request")
			}
			current, _, _ := ReadConfigDocument(path)
			if !bytes.Equal(current, want) {
				t.Fatal("failed removal hid or overwrote retryable configuration")
			}
		})
	}
}

func TestControllerRemovalNeverReconnectsAndForgetsOnlyRemovedMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(removalFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	controller, err := New(Options{Helper: "must-not-run", ConfigPath: path, LogPath: filepath.Join(t.TempDir(), "log")})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.log.Close()
	controller.command = &exec.Cmd{}
	controller.snapshot = Snapshot{State: "running", Mode: "tun", BackendState: "Running", NetworkName: "primary.ts.net",
		Exits: map[string]ExitSnapshot{"office": {BackendState: "Running"}, "lab": {NetworkName: "lab.ts.net"}}}
	_, revision, _ := ReadConfigDocument(path)
	if err := controller.removeExit("office", revision, func(target string) error {
		if !controller.reconfiguring || target != "office" {
			t.Fatal("removal escaped the serialized controller operation")
		}
		// Simulate LogoutExit's Stop without launching a real helper or device.
		controller.command = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := controller.Snapshot(); got.Active || got.State != "stopped" || got.Mode != "" || got.Exits["lab"].NetworkName != "lab.ts.net" || got.NetworkName != "primary.ts.net" {
		t.Fatalf("removal reconnected or discarded unrelated metadata: %+v", got)
	}
	if _, exists := controller.Snapshot().Exits["office"]; exists {
		t.Fatal("removed device metadata still appears in snapshots")
	}
}
