package tailsplit

import (
	"bytes"
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagernet/bbolt"
	"github.com/sagernet/sing-box/adapter"
	singlog "github.com/sagernet/sing-box/log"
)

func TestPreflightHasNoListenersOrTUNEvenWhenConfigured(t *testing.T) {
	config, err := loadTestConfig(t, `
state_dir: ./state
tailscale:
  exit_node: 100.64.0.10
  tailnet: original@example.com
tun:
  enabled: true
`)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compilePreflight(config, true)
	if err != nil {
		t.Fatal(err)
	}
	var native map[string]any
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		t.Fatal(err)
	}
	if _, exists := native["inbounds"]; exists {
		t.Fatal("preflight can start listeners or capture system traffic")
	}
	if native["endpoints"].([]any)[0].(map[string]any)["system_interface"] != false {
		t.Fatal("preflight can create a Tailscale system interface")
	}
	if !config.TUN.Enabled {
		t.Fatal("preflight changed the user's selected TUN mode")
	}
	config.Tailscale.ExitNode = ""
	discovery, err := compilePreflight(config, true)
	if err != nil {
		t.Fatal("device discovery needs a preconfigured exit:", err)
	}
	if TailscaleEnabled {
		if err := Check(discovery); err != nil {
			t.Fatal("exit discovery is not a valid listener-free core:", err)
		}
		if err := Check(compiled); err != nil {
			t.Fatal("preflight is not a valid embedded core config:", err)
		}
		engine, err := Open(context.Background(), config, compiled)
		if err != nil {
			t.Fatal(err)
		}
		defer engine.Close()
		if engine.hasTUN || engine.closeDNS != nil {
			t.Fatal("the preflight engine can install a system DNS override")
		}
	}
}

type preflightLogWriter struct{}

func (preflightLogWriter) WriteMessage(singlog.Level, string) {}

func TestListenerFreePreflightDoesNotOpenOrChangeFakeIPCache(t *testing.T) {
	// A service's working directory may be read-only. A directory at the core's
	// default cache pathname reliably rejects fallback writes on every platform.
	t.Chdir(t.TempDir())
	if err := os.Mkdir("cache.db", 0o700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		writer singlog.PlatformWriter
	}{
		{name: "CLI"},
		{name: "desktop", writer: preflightLogWriter{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			testPreflightPreservesFakeIPCache(t, test.writer)
		})
	}
}

func testPreflightPreservesFakeIPCache(t *testing.T, writer singlog.PlatformWriter) {
	t.Helper()
	config, err := loadTestConfig(t, "version: 1\n")
	if err != nil {
		t.Fatal(err)
	}
	config.StateDir = filepath.Join(t.TempDir(), "state")
	if err := privateState(config.StateDir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.StateDir, "cache.db")
	metadata := &adapter.FakeIPMetadata{
		Inet4Range: netip.MustParsePrefix("198.18.0.0/15"), Inet6Range: netip.MustParsePrefix("2001:2::/48"),
		Inet4Current: netip.MustParseAddr("198.18.0.2"), Inet6Current: netip.MustParseAddr("2001:2::2"),
	}
	encoded, err := metadata.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	address := netip.MustParseAddr("198.18.0.2").AsSlice()
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("fakeip_address"))
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte("fakeip_metadata"), encoded); err != nil {
			return err
		}
		return bucket.Put(address, []byte("example.test"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	compiled, err := compilePreflight(config, true)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := OpenWithLogWriter(context.Background(), config, compiled, writer)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	// Inspect while the engine is running: an engine using this cache holds
	// its lock and removes metadata until a clean shutdown.
	db, err = bbolt.Open(path, 0o600, &bbolt.Options{ReadOnly: true, Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("preflight locked the persistent FakeIP cache: %v", err)
	}
	defer db.Close()
	if err := db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte("fakeip_address"))
		if bucket == nil || !bytes.Equal(bucket.Get([]byte("fakeip_metadata")), encoded) || string(bucket.Get(address)) != "example.test" {
			t.Error("preflight changed saved FakeIP metadata or mappings")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExitCheckCannotEnableTUNInTheWrongTailnet(t *testing.T) {
	tests := []struct {
		name         string
		expected     string
		status       Status
		ready        bool
		wrongNetwork bool
	}{
		{"wrong personal network", "original@example.com", Status{BackendState: "Running", NetworkName: "personal@example.com", ExitVisible: true, ExitOnline: true}, false, true},
		{"missing exit", "original@example.com", Status{BackendState: "Running", NetworkName: "original@example.com"}, false, false},
		{"offline exit", "original@example.com", Status{BackendState: "Running", NetworkName: "original@example.com", ExitVisible: true}, false, false},
		{"network not received", "original@example.com", Status{BackendState: "Running", ExitVisible: true, ExitOnline: true}, false, false},
		{"correct network", "original@example.com", Status{BackendState: "Running", NetworkName: "original@example.com", ExitVisible: true, ExitOnline: true}, true, false},
		{"correct suffix", "tail-example.ts.net", Status{BackendState: "Running", NetworkName: "original@example.com", MagicDNSSuffix: "tail-example.ts.net", ExitVisible: true, ExitOnline: true}, true, false},
		{"no guard still needs exit", "", Status{BackendState: "Running"}, false, false},
		{"no guard ready", "", Status{BackendState: "Running", ExitVisible: true, ExitOnline: true}, true, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ready, err := preflightReady(test.expected, test.status)
			if ready != test.ready || (err != nil) != test.wrongNetwork {
				t.Fatalf("ready=%v error=%v", ready, err)
			}
		})
	}
}

func TestAdditionalTrafficExitWaitsForLoginWithoutDiscardingAuthorization(t *testing.T) {
	device := engineExit{id: "office", tailnet: "office.ts.net"}
	latest := map[string]Status{"office": {BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/fixture"}}
	done, waiting, _, err := tailnetReadiness([]engineExit{device}, latest, true, false)
	if err != nil || done || waiting || latest["office"].AuthURL == "" {
		t.Fatalf("connect aborted or timed out an interactive extra login: done=%v waiting=%v err=%v", done, waiting, err)
	}
	latest[""] = Status{BackendState: "Running", ExitVisible: true, ExitOnline: false}
	if done, waiting, _, err := tailnetReadiness([]engineExit{{id: ""}, device}, latest, true, false); err != nil || done || waiting {
		t.Fatal("another offline device started a timeout during interactive login", done, waiting, err)
	}
	latest["office"] = Status{BackendState: "Running", NetworkName: "office.ts.net", ExitVisible: true, ExitOnline: true}
	if done, _, _, err := tailnetReadiness([]engineExit{device}, latest, true, false); err != nil || !done {
		t.Fatal("the same preflight did not complete after authorization", done, err)
	}
	latest["office"] = Status{BackendState: "NeedsLogin"}
	if done, _, _, err := tailnetReadiness([]engineExit{device}, latest, false, true); err != nil || !done {
		t.Fatal("discovery waited for interactive login", done, err)
	}
}

func TestPreflightOnlyRequiresExplicitTrafficGateways(t *testing.T) {
	config, err := loadTestConfig(t, `tailscale:
  exit_node: 100.64.0.10
exits:
  - id: office
    exit_node: 100.64.0.20
  - id: spare
    exit_node: 100.64.0.30
rules:
  default_exit: main
  final: direct
`)
	if err != nil {
		t.Fatal(err)
	}
	devices := []engineExit{{id: ""}, {id: "office"}, {id: "spare"}}
	latest := map[string]Status{
		"":       {BackendState: "Running", ExitVisible: true, ExitOnline: true},
		"office": {BackendState: "Running", ExitVisible: true, ExitOnline: false},
		"spare":  {BackendState: "NeedsLogin"},
	}
	check := func(config Config, wantReady bool, pending string) {
		t.Helper()
		var required []engineExit
		for _, device := range devices {
			if requiresTrafficExit(config, device.id) {
				required = append(required, device)
			}
		}
		done, waiting, target, err := tailnetReadiness(required, latest, true, false)
		if err != nil || done != wantReady || waiting == wantReady || target != pending {
			t.Fatalf("wrong readiness done=%v waiting=%v pending=%q err=%v", done, waiting, target, err)
		}
	}
	check(config, true, "") // Unused offline and signed-out devices do not gate main.
	config.Rules.Routes = []RouteConfig{{Exit: "office"}}
	check(config, true, "") // Empty rule groups carry no traffic.
	config.Rules.Routes[0].Domains = []string{"corp.example"}
	check(config, false, "office")
	config.Rules.Routes = nil
	config.Rules.Final = "office"
	check(config, false, "office")
	config.Rules.Final, config.Rules.DefaultExit = "direct", "office"
	check(config, false, "office")
	config.Rules.Mode, config.Rules.GlobalExit = RoutingGlobal, MainExit
	check(config, true, "") // Global ignores retained Split selections.
	config.Focus = "spare"
	if requiresTrafficExit(config, "") || !requiresTrafficExit(config, "spare") {
		t.Fatal("focused device operations inherited another traffic gateway")
	}
}

func TestLoginCompletesIndependentlyOfExitAvailability(t *testing.T) {
	for _, status := range []Status{
		{BackendState: "Running", NetworkName: "original@example.com"},
		{BackendState: "Running", NetworkName: "original@example.com", ExitVisible: true},
	} {
		ready, err := authenticationReady("original@example.com", status)
		if err != nil || !ready {
			t.Fatal("device login incorrectly required a visible, online exit:", ready, err)
		}
		if tunReady, err := preflightReady("original@example.com", status); err != nil || tunReady {
			t.Fatal("TUN preflight stopped enforcing an online approved exit")
		}
	}
	if ready, err := authenticationReady("original@example.com",
		Status{BackendState: "Running", NetworkName: "personal@example.com"}); ready || err == nil {
		t.Fatal("login-only startup accepted the wrong Tailnet")
	}
}
