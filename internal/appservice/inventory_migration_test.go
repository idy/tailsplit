package appservice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/idy/tailsplit"
)

func migrationInventoryConfig(t *testing.T, count int, mode, focus string) tailsplit.Config {
	t.Helper()
	config := tailsplit.Config{
		StateDir:  privateTestDirectory(t),
		Tailscale: &tailsplit.TailscaleConfig{Hostname: "fixture-main", ExitNode: "100.64.0.10"},
		Rules:     tailsplit.RulesConfig{Mode: mode, GlobalExit: fmt.Sprintf("exit-%d", count)},
	}
	for index := 1; index <= count; index++ {
		id := fmt.Sprintf("exit-%d", index)
		config.Exits = append(config.Exits, tailsplit.ExitConfig{ID: id, Hostname: "fixture-" + id, ExitNode: fmt.Sprintf("100.101.0.%d", index)})
	}
	var err error
	config, err = tailsplit.Normalize(config, t.TempDir())
	if err != nil {
		t.Fatal("CLI configuration should retain its larger exit inventory:", err)
	}
	if focus != "" {
		config, err = config.FocusExit(focus)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range config.ExitIDs() {
		path := filepath.Join(config.ExitStateDir(id), "tailscaled.state")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("identity-"+id), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return config
}

func TestMigrationInventoryBoundaryKeepsFocusedAndGlobalInventory(t *testing.T) {
	for _, scenario := range []struct{ name, mode, focus string }{
		{"split", tailsplit.RoutingSplit, ""},
		{"focused-primary", tailsplit.RoutingSplit, tailsplit.MainExit},
		{"focused-extra", tailsplit.RoutingSplit, "exit-1"},
		{"global", tailsplit.RoutingGlobal, ""},
		{"global-focused-extra", tailsplit.RoutingGlobal, "exit-1"},
	} {
		for _, count := range []int{maxExits, maxExits + 1} {
			t.Run(fmt.Sprintf("%s/%d", scenario.name, count), func(t *testing.T) {
				config := migrationInventoryConfig(t, count, scenario.mode, scenario.focus)
				if len(config.Exits) != count {
					t.Fatal("focus narrowed the inventory before migration")
				}
				directory := filepath.Join(t.TempDir(), "service-user")
				_, serviceErr := serviceConfig(config, directory)
				validationErr := ValidateAppConfig(config)
				for _, seed := range []bool{true, false} {
					files, err := userFiles(&config, seed)
					if count > maxExits {
						if serviceErr == nil || validationErr == nil || err == nil {
							t.Fatalf("oversized inventory was accepted: service=%v validation=%v collection=%v", serviceErr, validationErr, err)
						}
						if !reflect.DeepEqual(files, Files{}) {
							t.Fatal("rejected migration returned partial request contents")
						}
						continue
					}
					if serviceErr != nil || validationErr != nil || err != nil {
						t.Fatalf("supported inventory rejected: service=%v validation=%v collection=%v", serviceErr, validationErr, err)
					}
					if !seed {
						if !reflect.DeepEqual(files, Files{}) {
							t.Fatal("initialized service was sent stale migration state")
						}
						continue
					}
					if len(files.ExitStates) != maxExits || string(files.TailscaleState) != "identity-main" {
						t.Fatalf("supported migration lost identities: primary=%q extras=%d", files.TailscaleState, len(files.ExitStates))
					}
					base := privateTestDirectory(t)
					imported, err := prepareRuntimeUser(base, "fixture-user", files)
					if err != nil || !runtimeInitialized(imported) {
						t.Fatalf("supported migration did not initialize: %v", err)
					}
					service, err := serviceConfig(config, imported)
					if err != nil || !reflect.DeepEqual(service.Exits, config.Exits) || service.Focus != config.Focus {
						t.Fatalf("service changed the migration inventory or focus: %v", err)
					}
					for _, id := range service.ExitIDs() {
						data, err := os.ReadFile(filepath.Join(service.ExitStateDir(id), "tailscaled.state"))
						if err != nil || string(data) != "identity-"+id {
							t.Fatalf("identity %q was not imported: %q, %v", id, data, err)
						}
					}
				}
				if _, err := os.Stat(directory); !os.IsNotExist(err) {
					t.Fatalf("service validation wrote its runtime directory: %v", err)
				}
			})
		}
	}
}

func migrationRuntimeSnapshot(t *testing.T, base string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[relative+"/"] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		files[relative] = string(data)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func existingMigrationRuntime(t *testing.T, base, user, state string) {
	t.Helper()
	if state == "fresh" {
		return
	}
	directory := runtimeUserDirectory(base, user)
	for name, contents := range map[string]string{
		"tailscale/tailscaled.state":              "existing-primary",
		"exits/exit-1/tailscale/tailscaled.state": "existing-extra",
		"cache.db": "existing-cache", "gfwlist.txt": "existing-rules",
	} {
		path := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if state == "initialized" {
		if err := os.WriteFile(filepath.Join(directory, ".initialized"), []byte("existing-marker\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOversizedMigrationRejectsBeforeAnyRuntimeWrite(t *testing.T) {
	files := Files{TailscaleState: []byte("replacement-primary"), Cache: []byte("replacement-cache"), Rules: []byte("replacement-rules"), ExitStates: map[string][]byte{}}
	for index := 1; index <= maxExits+1; index++ {
		files.ExitStates[fmt.Sprintf("exit-%d", index)] = []byte("replacement-extra")
	}
	for _, state := range []string{"fresh", "uninitialized", "initialized"} {
		for _, updateRules := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/rules=%t", state, updateRules), func(t *testing.T) {
				base := filepath.Join(privateTestDirectory(t), "runtime")
				existingMigrationRuntime(t, base, "fixture-user", state)
				before := migrationRuntimeSnapshot(t, base)
				if _, err := prepareRuntimeUserWithRules(base, "fixture-user", files, updateRules); err == nil {
					t.Fatal("oversized migration was accepted")
				}
				if after := migrationRuntimeSnapshot(t, base); !reflect.DeepEqual(after, before) {
					t.Fatalf("rejected migration changed state or its initialization marker: before=%v after=%v", before, after)
				}
			})
		}
	}
}

func TestServiceRejectsOversizedInventoryWithoutPartialSuccess(t *testing.T) {
	for _, scenario := range []struct{ operation, mode, focus string }{
		{"start", tailsplit.RoutingSplit, ""},
		{"login", tailsplit.RoutingSplit, "exit-1"},
		{"discover", tailsplit.RoutingGlobal, ""},
	} {
		for _, state := range []string{"fresh", "uninitialized", "initialized"} {
			t.Run(scenario.operation+"/"+state, func(t *testing.T) {
				var runs atomic.Int32
				path, server, cancel, done := testServer(t, os.Getuid(), func(_ context.Context, _ tailsplit.Config, output io.Writer) error {
					runs.Add(1)
					return json.NewEncoder(output).Encode(map[string]string{"event": "unexpected-success"})
				})
				existingMigrationRuntime(t, server.Base, server.userKey(), state)
				before := migrationRuntimeSnapshot(t, server.Base)
				config := migrationInventoryConfig(t, maxExits+1, scenario.mode, scenario.focus)
				config.StateDir = ""
				// Eight transferred states reproduce the old client's silent truncation.
				files := Files{TailscaleState: []byte("replacement-primary"), Rules: []byte("replacement-rules"), ExitStates: map[string][]byte{}}
				for index := 1; index <= maxExits; index++ {
					files.ExitStates[fmt.Sprintf("exit-%d", index)] = []byte("replacement-extra")
				}
				connection, decoder := session(t, path, Request{Protocol: Protocol, Op: scenario.operation, Config: &config, Files: files})
				var event map[string]string
				err := decoder.Decode(&event)
				_ = connection.Close()
				cancel()
				if serveErr := <-done; serveErr != nil {
					t.Fatal(serveErr)
				}
				if err != nil || event["event"] != "error" || !strings.Contains(event["message"], "8") || runs.Load() != 0 {
					t.Fatalf("oversized service request partially succeeded: event=%v runs=%d error=%v", event, runs.Load(), err)
				}
				if after := migrationRuntimeSnapshot(t, server.Base); !reflect.DeepEqual(after, before) {
					t.Fatalf("rejected request changed existing runtime state: before=%v after=%v", before, after)
				}
			})
		}
	}
}
