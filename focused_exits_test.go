package tailsplit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func focusedExitTestConfig(t *testing.T) Config {
	t.Helper()
	config, err := loadTestConfig(t, `
state_dir: ./state
tailscale:
  hostname: laptop
  exit_node: 100.64.0.10
  tailnet: primary.ts.net
  auth_key_env: FIXTURE_FOCUSED_EXIT_KEY
dns:
  magic_domains: [primary.ts.net]
exits:
  - id: office
    hostname: laptop-office
    exit_node: 100.101.0.11
    tailnet: office.ts.net
  - id: spare
    hostname: laptop-spare
rules:
  final: office
  routes:
    - exit: office
      domains: [corp.example]
tun:
  enabled: true
`)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func focusedPreflightEndpoint(t *testing.T, config Config) map[string]any {
	t.Helper()
	compiled, err := compilePreflight(config, true)
	if err != nil {
		t.Fatal(err)
	}
	if TailscaleEnabled {
		if err := Check(compiled); err != nil {
			t.Fatal(err)
		}
	}
	var native map[string]any
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		t.Fatal(err)
	}
	if native["inbounds"] != nil {
		t.Fatal("focused device session retained listeners")
	}
	// Desktop logging can force sing-box to open a cache even when disabled.
	// Keep it separate from the connection's saved FakeIP cache (0.7.16 fix).
	experimental, _ := native["experimental"].(map[string]any)
	cache, _ := experimental["cache_file"].(map[string]any)
	if cache["enabled"] != false || cache["store_fakeip"] != false ||
		cache["path"] != filepath.Join(config.StateDir, "preflight-cache.db") {
		t.Fatalf("focused device session did not isolate its logging cache: %v", cache)
	}
	endpoints := native["endpoints"].([]any)
	if len(endpoints) != 1 {
		t.Fatalf("focused session runs %d devices", len(endpoints))
	}
	return endpoints[0].(map[string]any)
}

func TestCompileNormalizesDirectFocusedConfig(t *testing.T) {
	for _, api := range []string{"Compile", "CompileContext", "ValidateTrafficExit"} {
		for _, test := range []struct {
			name    string
			primary *TailscaleConfig
			node    string
			wantErr bool
		}{
			{name: "extras only", node: "100.101.0.11"},
			{name: "pending primary ready extra", primary: &TailscaleConfig{Hostname: "primary"}, node: "100.101.0.11"},
			{name: "ready primary pending extra", primary: &TailscaleConfig{Hostname: "primary", ExitNode: "100.64.0.10"}, wantErr: true},
			{name: "extras only pending", wantErr: true},
		} {
			t.Run(api+"/"+test.name, func(t *testing.T) {
				config := Config{
					StateDir: t.TempDir(), Tailscale: test.primary, Focus: "office",
					Exits: []ExitConfig{{ID: "office", Hostname: "office", ExitNode: test.node}},
				}
				var compiled Compiled
				var err error
				switch api {
				case "Compile":
					compiled, err = Compile(config)
				case "CompileContext":
					compiled, err = CompileContext(t.Context(), config)
				case "ValidateTrafficExit":
					err = ValidateTrafficExit(config)
				}
				if test.wantErr {
					if err == nil {
						t.Fatal("a ready primary hid the pending focused device")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if api == "ValidateTrafficExit" {
					return
				}
				if TailscaleEnabled {
					if err := Check(compiled); err != nil {
						t.Fatal(err)
					}
				}
				var native map[string]any
				if err := json.Unmarshal(compiled.JSON, &native); err != nil {
					t.Fatal(err)
				}
				tags := endpointTags(native)
				if len(tags) != 1 || tags["tailnet"]["exit_node"] != test.node ||
					tags["tailnet"]["state_directory"] != config.ExitStateDir("office") {
					t.Fatal("direct Config focus did not select its own node and state", tags)
				}
			})
		}
	}
}

func TestFocusedRuntimeRetainsEveryExitForMigration(t *testing.T) {
	for _, id := range []string{MainExit, "office"} {
		t.Run(id, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			before, _ := json.Marshal(config)
			focused, err := config.FocusExit(id)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(focused.Exits, config.Exits) {
				t.Fatal("focusing discarded saved-device migration inventory")
			}
			data, _ := json.Marshal(focused)
			var received Config
			if err := json.Unmarshal(data, &received); err != nil {
				t.Fatal(err)
			}
			received, err = Normalize(received, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if received.Focus != id || !reflect.DeepEqual(received.Exits, config.Exits) {
				t.Fatal("service normalization changed focus or migration inventory")
			}
			endpoint := focusedPreflightEndpoint(t, received)
			if endpoint["tag"] != "tailnet" || endpoint["state_directory"] != config.ExitStateDir(id) ||
				endpoint["exit_node"] != focused.Tailscale.ExitNode {
				t.Fatalf("focused device uses another exit's identity: %v", endpoint)
			}
			after, _ := json.Marshal(config)
			if string(before) != string(after) {
				t.Fatal("focusing or compiling changed the caller's configuration")
			}
			if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
				t.Fatal("focused compile/check created saved-device state")
			}
		})
	}
}

func TestSavedExitPingScopesAndSanitizesOnlyItsDevice(t *testing.T) {
	for _, test := range []struct {
		name, initialFocus, requestedExit, wantFocus string
	}{
		{name: "default main", wantFocus: MainExit},
		{name: "explicit main", requestedExit: MainExit, wantFocus: MainExit},
		{name: "additional exit", requestedExit: "office", wantFocus: "office"},
		{name: "already focused exit", initialFocus: "office", wantFocus: "office"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			if test.initialFocus != "" {
				var err error
				config, err = config.FocusExit(test.initialFocus)
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := json.Marshal(config)
			ping, err := savedExitPingConfig(config, test.requestedExit)
			if err != nil {
				t.Fatal(err)
			}
			if ping.Focus != test.wantFocus || ping.Tailscale.ExitNode != "" || ping.Tailscale.AuthKeyEnv != "" {
				t.Fatalf("ping did not scope and sanitize its device: %+v", ping.Tailscale)
			}
			for index, exit := range ping.Exits {
				want := config.Exits[index]
				if exit.ID == test.wantFocus {
					want.ExitNode = ""
				}
				if exit != want {
					t.Fatalf("ping changed unrelated exit fields: %+v", exit)
				}
			}
			endpoint := focusedPreflightEndpoint(t, ping)
			if endpoint["state_directory"] != config.ExitStateDir(test.wantFocus) || endpoint["exit_node"] != "" || endpoint["auth_key"] != nil {
				t.Fatalf("normalization restored an exit selection or another device: %v", endpoint)
			}
			after, _ := json.Marshal(config)
			if string(before) != string(after) {
				t.Fatal("ping mutated the caller's saved selection or exit slice")
			}
			if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
				t.Fatal("ping compile/check opened saved-device state")
			}
		})
	}
}

func TestSavedExitPingRejectsUnknownFocus(t *testing.T) {
	if _, err := savedExitPingConfig(focusedExitTestConfig(t), "unknown"); !errors.Is(err, ErrPingUnavailable) {
		t.Fatalf("unknown focused device accepted: %v", err)
	}
}
