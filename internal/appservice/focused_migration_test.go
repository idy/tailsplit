package appservice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
)

func migrationDial(path string) sessionDial {
	return func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}
}

// This is the session path shared by the authenticated Unix and Windows clients.
// Fixtures replace only their endpoint authentication/dialing and never open an
// actual engine or any user state outside t.TempDir.
func runMigrationSession(t *testing.T, path string, server *Server, operation string, config tailsplit.Config, target string) {
	t.Helper()
	info := Info{Initialized: runtimeInitialized(runtimeUserDirectory(server.Base, server.userKey()))}
	connection, err := openLeasedSession(context.Background(), operation, config, target, info, migrationDial(path))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if operation == "ping" {
		if _, err := readPingReply(context.Background(), connection); err != nil {
			t.Fatal(err)
		}
	} else {
		var event map[string]string
		if err := json.NewDecoder(connection).Decode(&event); err != nil || event["event"] != "stopped" {
			t.Fatalf("%s failed: %v, %v", operation, event, err)
		}
	}
	if release, err := tailsplit.LockSavedState(config.StateDir); err == nil {
		_ = release()
		t.Fatal("session released the legacy state lease before its connection closed")
	}
}

func TestExtrasFirstSessionKeepsPrimaryEligibleUntilImportOrLogout(t *testing.T) {
	for _, firstOperation := range []string{"start", "login", "ping"} {
		t.Run(firstOperation, func(t *testing.T) {
			config := migrationInventoryConfig(t, 2, tailsplit.RoutingGlobal, "")
			legacyPrimary := filepath.Join(config.StateDir, "tailscale", "tailscaled.state")
			if err := os.RemoveAll(filepath.Dir(legacyPrimary)); err != nil {
				t.Fatal(err)
			}
			logoutRequested := make(chan struct{}, 1)
			path, server, cancel, done := testServer(t, os.Getuid(), func(_ context.Context, received tailsplit.Config, _ io.Writer) error {
				select {
				case <-logoutRequested:
					assertMigrationFile(t, filepath.Join(received.StateDir, ".primary-initialized"), "1\n")
					return os.RemoveAll(received.ExitStateDir(tailsplit.MainExit))
				default:
					return nil
				}
			})
			server.Ping = func(_ context.Context, _ tailsplit.Config, target string) (tailsplit.ExitPingResult, error) {
				return tailsplit.ExitPingResult{ID: target}, nil
			}
			first := config
			if firstOperation == "login" {
				var err error
				first, err = config.FocusExit("exit-2")
				if err != nil {
					t.Fatal(err)
				}
			}
			runMigrationSession(t, path, server, firstOperation, first, "exit-2|100.64.0.10")
			directory := runtimeUserDirectory(server.Base, server.userKey())
			assertMigrationFile(t, filepath.Join(directory, ".initialized"), identityInitialization)
			plan, err := runtimeImports(directory, []string{tailsplit.MainExit})
			if err != nil || !reflect.DeepEqual(plan.IDs, []string{tailsplit.MainExit}) || plan.Cache {
				t.Fatalf("extras-only initialization claimed the primary or reseeded cache: %+v %v", plan, err)
			}
			primary, err := config.FocusExit(tailsplit.MainExit)
			if err != nil {
				t.Fatal(err)
			}
			// A request with no legacy state must not spend future eligibility.
			runMigrationSession(t, path, server, "discover", primary, "")
			writeMigrationFile(t, legacyPrimary, "later-cli-primary")
			runMigrationSession(t, path, server, "login", primary, "")
			servicePrimary := filepath.Join(directory, "tailscale", "tailscaled.state")
			assertMigrationFile(t, servicePrimary, "later-cli-primary")
			writeMigrationFile(t, legacyPrimary, "stale-primary")
			runMigrationSession(t, path, server, "login", primary, "")
			assertMigrationFile(t, servicePrimary, "later-cli-primary")
			logoutRequested <- struct{}{}
			runMigrationSession(t, path, server, "logout", primary, "")
			runMigrationSession(t, path, server, "login", primary, "")
			if _, err := os.Stat(servicePrimary); !os.IsNotExist(err) {
				t.Fatalf("actual primary logout was resurrected: %v", err)
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrimaryLogoutWithoutStateClaimsAuthority(t *testing.T) {
	for _, old := range []bool{false, true} {
		t.Run(fmt.Sprint(old), func(t *testing.T) {
			base := privateTestDirectory(t)
			directory, err := prepareRuntimeUser(base, "fixture-user", Files{})
			if err != nil {
				t.Fatal(err)
			}
			if old {
				writeMigrationFile(t, filepath.Join(directory, ".initialized"), "1\n")
			}
			if err := claimLogoutExit(tailsplit.Config{StateDir: directory}); err != nil {
				t.Fatal(err)
			}
			if _, err := prepareRuntimeUser(base, "fixture-user", Files{TailscaleState: []byte("stale-primary")}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(directory, "tailscale")); !os.IsNotExist(err) {
				t.Fatal("logout without primary state permitted later resurrection", err)
			}
		})
	}
}

func TestPrimaryAuthorityPreservesOlderRuntimeCompatibility(t *testing.T) {
	for _, old := range []string{"1\n", "existing-marker\n", strings.Repeat("x", 65), "markerless-directory"} {
		t.Run(strings.TrimSpace(old), func(t *testing.T) {
			base := privateTestDirectory(t)
			directory := runtimeUserDirectory(base, "fixture-user")
			if old == "markerless-directory" {
				if err := os.MkdirAll(filepath.Join(directory, "tailscale"), 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				writeMigrationFile(t, filepath.Join(directory, ".initialized"), old)
			}
			plan, err := runtimeImports(directory, []string{tailsplit.MainExit})
			if err != nil || len(plan.IDs) != 0 {
				t.Fatalf("older runtime lost primary authority: %+v %v", plan, err)
			}
			if _, err := prepareRuntimeUser(base, "fixture-user", Files{TailscaleState: []byte("stale-primary")}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(directory, "tailscale", "tailscaled.state")); !os.IsNotExist(err) {
				t.Fatal("older runtime resurrected the primary", err)
			}
		})
	}
}

func TestSessionsIgnoreOwnedAndUnrelatedLegacyFiles(t *testing.T) {
	for _, operation := range []string{"start", "login", "discover", "logout", "ping"} {
		for _, badKind := range []string{"oversized", "nonregular"} {
			t.Run(operation+"/"+badKind, func(t *testing.T) {
				config := migrationInventoryConfig(t, 3, tailsplit.RoutingGlobal, "")
				config.Rules.GlobalExit = "exit-2"
				if operation != "start" {
					var err error
					config, err = config.FocusExit("exit-2")
					if err != nil {
						t.Fatal(err)
					}
				}
				path, server, cancel, done := testServer(t, os.Getuid(), func(context.Context, tailsplit.Config, io.Writer) error { return nil })
				server.Ping = func(_ context.Context, _ tailsplit.Config, target string) (tailsplit.ExitPingResult, error) {
					return tailsplit.ExitPingResult{ID: target}, nil
				}
				directory, err := prepareRuntimeUser(server.Base, server.userKey(), Files{
					TailscaleState: []byte("service-primary"), ExitStates: map[string][]byte{"exit-1": []byte("service-extra")},
				})
				if err != nil {
					t.Fatal(err)
				}
				makeBad := func(id string) {
					t.Helper()
					state := filepath.Join(config.ExitStateDir(id), "tailscaled.state")
					if badKind == "nonregular" {
						if err := os.Remove(state); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(state, 0o700); err != nil {
							t.Fatal(err)
						}
					} else if err := os.Truncate(state, maxState+1); err != nil {
						t.Fatal(err)
					}
				}
				for _, id := range []string{tailsplit.MainExit, "exit-1", "exit-3"} {
					makeBad(id)
				}
				runMigrationSession(t, path, server, operation, config, "exit-2|100.64.0.10")
				assertMigrationFile(t, filepath.Join(directory, "exits", "exit-2", "tailscale", "tailscaled.state"), "identity-exit-2")
				makeBad("exit-2")
				runMigrationSession(t, path, server, operation, config, "exit-2|100.64.0.10")
				assertMigrationFile(t, filepath.Join(directory, "tailscale", "tailscaled.state"), "service-primary")
				assertMigrationFile(t, filepath.Join(directory, "exits", "exit-1", "tailscale", "tailscaled.state"), "service-extra")
				if _, err := os.Stat(filepath.Join(directory, "exits", "exit-3")); !os.IsNotExist(err) {
					t.Fatalf("unrelated exit was read or claimed: %v", err)
				}
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

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
			// Query all identities here to exercise the shared authority contract.
			// Clients further scope this query to their operation's devices.
			plan, err := runtimeImports(directory, config.ExitIDs())
			if err != nil {
				t.Fatal(err)
			}
			files, err := userFiles(&config, plan)
			if err != nil || len(files.ExitStates) != 1 || len(files.TailscaleState) != 0 || len(files.Cache) != 0 {
				t.Fatalf("initialized client read owned identities or reseeded the cache: %+v, %v", files, err)
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
			plan, err = runtimeImports(directory, config.ExitIDs())
			if err != nil {
				t.Fatal(err)
			}
			files, err = userFiles(&config, plan)
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
				files, err := userFiles(&config, inventoryPlan(config, !scenario.initialized))
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
			files, err := userFiles(&focused, inventoryPlan(focused, true))
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
