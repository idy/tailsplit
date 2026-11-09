package appservice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/idy/tailsplit"
)

func writeMigrationFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertMigrationFile(t *testing.T, path, contents string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != contents {
		t.Fatalf("migration state %s = %q, %v; want %q", path, data, err, contents)
	}
}

func TestInitializedMigrationImportsOnlyUnclaimedExtraIdentities(t *testing.T) {
	for _, scenario := range []struct{ name, mode, focus string }{
		{"focused-primary", tailsplit.RoutingSplit, tailsplit.MainExit},
		{"focused-extra", tailsplit.RoutingSplit, "exit-4"},
		{"global", tailsplit.RoutingGlobal, ""},
		{"global-focused-extra", tailsplit.RoutingGlobal, "exit-4"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			config := migrationInventoryConfig(t, 5, scenario.mode, scenario.focus)
			base := privateTestDirectory(t)
			const user = "fixture-user"
			// Reproduce an upgraded helper: the global marker and service-owned
			// primary/cache/extra predate per-exit migration authority.
			existingMigrationRuntime(t, base, user, "initialized")
			directory := runtimeUserDirectory(base, user)
			service, err := serviceConfig(config, directory)
			if err != nil {
				t.Fatal(err)
			}
			writeMigrationFile(t, filepath.Join(service.ExitStateDir("exit-2"), "tailscaled.state"), "")
			// An older logout may leave only the service directory. It is still
			// authoritative even with no state file or per-exit marker.
			if err := os.MkdirAll(service.ExitStateDir("exit-3"), 0o700); err != nil {
				t.Fatal(err)
			}
			lateState := filepath.Join(config.ExitStateDir("exit-5"), "tailscaled.state")
			if err := os.Remove(lateState); err != nil {
				t.Fatal(err)
			}
			// This is the seed=false call made by BOTH installed clients after
			// hello reports Initialized, with the full focused/Global inventory.
			files, err := userFiles(&config, false)
			if err != nil || len(files.ExitStates) != 4 || len(files.TailscaleState) != 0 || len(files.Cache) != 0 {
				t.Fatalf("initialized client omitted extra candidates or reseeded the primary/cache: %+v, %v", files, err)
			}
			// An explicit empty candidate also must not claim future import.
			files.ExitStates["exit-5"] = nil
			request := Request{Protocol: Protocol, Op: "login", Config: &config, Files: files}
			data, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			received, err := readRequest(bufio.NewReader(bytes.NewReader(append(data, '\n'))))
			if err != nil {
				t.Fatal(err)
			}
			// Stale or malicious primary/cache payloads remain ineffective even
			// though additional-exit import now runs after initialization.
			received.Files.TailscaleState = []byte("stale-primary")
			received.Files.Cache = []byte("stale-cache")
			if _, err := prepareRuntimeUser(base, user, received.Files); err != nil {
				t.Fatal(err)
			}
			assertMigrationFile(t, filepath.Join(directory, ".initialized"), "existing-marker\n")
			assertMigrationFile(t, filepath.Join(directory, "tailscale", "tailscaled.state"), "existing-primary")
			assertMigrationFile(t, filepath.Join(directory, "cache.db"), "existing-cache")
			assertMigrationFile(t, filepath.Join(service.ExitStateDir("exit-1"), "tailscaled.state"), "existing-extra")
			assertMigrationFile(t, filepath.Join(service.ExitStateDir("exit-2"), "tailscaled.state"), "")
			if _, err := os.Stat(filepath.Join(service.ExitStateDir("exit-3"), "tailscaled.state")); !os.IsNotExist(err) {
				t.Fatalf("migration resurrected a previously service-owned exit: %v", err)
			}
			imported := filepath.Join(service.ExitStateDir("exit-4"), "tailscaled.state")
			assertMigrationFile(t, imported, "identity-exit-4")
			assertMigrationFile(t, filepath.Join(directory, "exits", "exit-4", ".initialized"), "1\n")
			if _, err := os.Stat(filepath.Join(directory, "exits", "exit-5")); !os.IsNotExist(err) {
				t.Fatalf("missing legacy state prematurely claimed an unseen exit: %v", err)
			}
			writeMigrationFile(t, imported, "newer-service-identity")
			if _, err := prepareRuntimeUser(base, user, received.Files); err != nil {
				t.Fatal(err)
			}
			assertMigrationFile(t, imported, "newer-service-identity")
			// CLI/fallback authentication can occur after an earlier request had
			// no state for this ID, including outside the current runtime focus.
			writeMigrationFile(t, lateState, "late-cli-identity")
			files, err = userFiles(&config, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := prepareRuntimeUserWithRules(base, user, files, false); err != nil {
				t.Fatal(err)
			}
			assertMigrationFile(t, filepath.Join(service.ExitStateDir("exit-5"), "tailscaled.state"), "late-cli-identity")
			assertMigrationFile(t, imported, "newer-service-identity")
		})
	}
}

func TestLogoutClaimsOnlyItsExitBeforeOpeningTheRunner(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		initialized bool
		migrated    bool
		legacy      bool
		failLogout  bool
	}{
		{name: "fresh-missing"},
		{name: "upgraded-missing", initialized: true},
		{name: "fresh-legacy", legacy: true},
		{name: "upgraded-legacy", initialized: true, legacy: true},
		{name: "already-migrated", initialized: true, migrated: true, legacy: true},
		{name: "failed-before-state-exists", initialized: true, failLogout: true},
	} {
		for _, mode := range []string{tailsplit.RoutingSplit, tailsplit.RoutingGlobal} {
			t.Run(scenario.name+"/"+mode, func(t *testing.T) {
				config := migrationInventoryConfig(t, 3, mode, "exit-1")
				// An unrelated, currently missing exit must remain eligible for
				// migration even when it is retained in the logout inventory.
				if err := os.Remove(filepath.Join(config.ExitStateDir("exit-3"), "tailscaled.state")); err != nil {
					t.Fatal(err)
				}
				legacyPath := filepath.Join(config.ExitStateDir("exit-1"), "tailscaled.state")
				if !scenario.legacy {
					if err := os.Remove(legacyPath); err != nil {
						t.Fatal(err)
					}
				}
				ran := false
				path, server, cancel, done := testServer(t, os.Getuid(), func(_ context.Context, received tailsplit.Config, _ io.Writer) error {
					ran = true
					if received.Focus != "exit-1" || len(received.Exits) != 3 {
						t.Error("logout lost its focus or migration inventory")
					}
					assertMigrationFile(t, filepath.Join(received.StateDir, "exits", "exit-1", ".initialized"), "1\n")
					state := filepath.Join(received.ExitStateDir("exit-1"), "tailscaled.state")
					if scenario.legacy {
						assertMigrationFile(t, state, "identity-exit-1")
					} else if _, err := os.Stat(state); !os.IsNotExist(err) {
						t.Errorf("logout unexpectedly acquired an identity: %v", err)
					}
					// Match a logout that removes saved state. Its durable authority
					// must outlive all engine-owned files, including on failure.
					if err := os.RemoveAll(received.ExitStateDir("exit-1")); err != nil {
						return err
					}
					if scenario.failLogout {
						return errors.New("fixture logout failed")
					}
					return nil
				})
				if scenario.initialized {
					directory := runtimeUserDirectory(server.Base, server.userKey())
					writeMigrationFile(t, filepath.Join(directory, ".initialized"), "old-marker\n")
				}
				if scenario.migrated {
					if _, err := prepareRuntimeUser(server.Base, server.userKey(), Files{ExitStates: map[string][]byte{"exit-1": []byte("identity-exit-1")}}); err != nil {
						t.Fatal(err)
					}
				}
				files, err := userFiles(&config, !scenario.initialized)
				if err != nil {
					t.Fatal(err)
				}
				config.StateDir = ""
				connection, decoder := session(t, path, Request{Protocol: Protocol, Op: "logout", Config: &config, Files: files})
				var event map[string]string
				if err := decoder.Decode(&event); err != nil {
					t.Fatal(err)
				}
				want := "stopped"
				if scenario.failLogout {
					want = "error"
				}
				if event["event"] != want {
					t.Fatalf("logout result = %v, want %s", event, want)
				}
				_ = connection.Close()
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if !ran {
					t.Fatal("logout runner did not run")
				}
				// A legacy copy can arrive after logout even if the original
				// logout request contained no state at all.
				files.ExitStates["exit-1"] = []byte("stale-legacy-identity")
				files.ExitStates["exit-3"] = []byte("new-unrelated-cli-identity")
				if _, err := prepareRuntimeUser(server.Base, server.userKey(), files); err != nil {
					t.Fatal(err)
				}
				directory := runtimeUserDirectory(server.Base, server.userKey())
				if _, err := os.Stat(filepath.Join(directory, "exits", "exit-1", "tailscale")); !os.IsNotExist(err) {
					t.Fatalf("stale legacy copy resurrected a removed device: %v", err)
				}
				assertMigrationFile(t, filepath.Join(directory, "exits", "exit-2", "tailscale", "tailscaled.state"), "identity-exit-2")
				assertMigrationFile(t, filepath.Join(directory, "exits", "exit-3", "tailscale", "tailscaled.state"), "new-unrelated-cli-identity")
			})
		}
	}
}

func TestFocusedFirstMigrationImportsEverySavedExitOnce(t *testing.T) {
	for _, focus := range []string{tailsplit.MainExit, "office"} {
		t.Run(focus, func(t *testing.T) {
			config, err := tailsplit.Normalize(tailsplit.Config{
				StateDir: t.TempDir(),
				Tailscale: &tailsplit.TailscaleConfig{
					Hostname: "fixture-main", ExitNode: "100.64.0.10",
				},
				Exits: []tailsplit.ExitConfig{
					{ID: "office", Hostname: "fixture-office", ExitNode: "100.101.0.11"},
					{ID: "spare", Hostname: "fixture-spare"},
				},
			}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range config.ExitIDs() {
				directory := config.ExitStateDir(id)
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "tailscaled.state"), []byte("fixture-state-"+id), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			focused, err := config.FocusExit(focus)
			if err != nil {
				t.Fatal(err)
			}
			files, err := userFiles(&focused, true)
			if err != nil {
				t.Fatal(err)
			}
			if string(files.TailscaleState) != "fixture-state-main" || len(files.ExitStates) != len(config.Exits) {
				t.Fatalf("focused request omitted saved identities: %+v", files)
			}
			// Match the ordinary-user request, then the service's independent
			// normalization and one-time import, without opening any endpoint.
			focused.StateDir = ""
			data, err := json.Marshal(Request{Protocol: Protocol, Op: "login", Config: &focused, Files: files})
			if err != nil {
				t.Fatal(err)
			}
			request, err := readRequest(bufio.NewReader(bytes.NewReader(append(data, '\n'))))
			if err != nil {
				t.Fatal(err)
			}
			base := privateTestDirectory(t)
			directory := runtimeUserDirectory(base, "fixture-user")
			received, err := serviceConfig(*request.Config, directory)
			if err != nil {
				t.Fatal(err)
			}
			if received.Focus != focus || !reflect.DeepEqual(received.Exits, config.Exits) {
				t.Fatal("service discarded focus or the complete exit inventory")
			}
			if _, err := prepareRuntimeUser(base, "fixture-user", request.Files); err != nil {
				t.Fatal(err)
			}
			if !runtimeInitialized(directory) {
				t.Fatal("first migration did not finish")
			}
			for _, id := range config.ExitIDs() {
				path := filepath.Join(received.ExitStateDir(id), "tailscaled.state")
				state, err := os.ReadFile(path)
				if err != nil || string(state) != "fixture-state-"+id {
					t.Fatalf("saved identity %q was not imported to its own directory: %q, %v", id, state, err)
				}
				if err := os.WriteFile(path, []byte("logged-out-"+id), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// A subsequent stale import must never resurrect any logged-out
			// identity, including the exits outside the original runtime focus.
			if _, err := prepareRuntimeUser(base, "fixture-user", request.Files); err != nil {
				t.Fatal(err)
			}
			for _, id := range config.ExitIDs() {
				state, err := os.ReadFile(filepath.Join(received.ExitStateDir(id), "tailscaled.state"))
				if err != nil || string(state) != "logged-out-"+id {
					t.Fatalf("stale migration restored identity %q: %q, %v", id, state, err)
				}
			}
		})
	}
}
