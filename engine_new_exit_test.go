package tailsplit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func freshExitTestConfig(t *testing.T) Config {
	t.Helper()
	config, err := Normalize(Config{
		StateDir:  filepath.Join(t.TempDir(), "state"),
		Tailscale: &TailscaleConfig{Hostname: "fixture-main"},
		Exits:     []ExitConfig{{ID: "saved", Hostname: "fixture-saved"}, {ID: "new-exit", Hostname: "fixture-new"}},
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err = config.FocusExit("new-exit")
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func TestFreshExitOpenReservesOnlyAfterLeaseAndKeepsClaimOnFailure(t *testing.T) {
	config := freshExitTestConfig(t)
	unlock, err := LockSavedState(config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	// Invalid native options stop before box.New: no real Tailscale endpoint is
	// constructed or started in this test, even after the reservation succeeds.
	invalid := Compiled{JSON: []byte("invalid fixture native options")}
	if engine, err := openWithLogWriter(t.Context(), config, invalid, nil, true); err == nil || engine != nil {
		t.Fatal("fresh authentication bypassed an existing lease")
	}
	namespace := filepath.Dir(config.ExitStateDir(config.Focus))
	if _, err := os.Lstat(namespace); !os.IsNotExist(err) {
		t.Fatal("fresh namespace was claimed before obtaining the state lease", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if engine, err := openWithLogWriter(t.Context(), config, invalid, nil, true); err == nil || engine != nil {
		t.Fatal("invalid fixture native options unexpectedly opened an engine")
	}
	marker := filepath.Join(namespace, ".initialized")
	if data, err := os.ReadFile(marker); err != nil || string(data) != "1\n" {
		t.Fatal("failed initialization lost its namespace claim", err)
	}
	// Saved logout/import treats this namespace as a private-directory root.
	// Windows therefore requires a protected ACL, not only private inheritance.
	if err := privateState(namespace); err != nil {
		t.Fatal("fresh namespace cannot be used by later saved-device operations", err)
	}
	if _, err := os.Stat(config.ExitStateDir(config.Focus)); !os.IsNotExist(err) {
		t.Fatal("reservation created or imported endpoint state", err)
	}
	if engine, err := openWithLogWriter(t.Context(), config, invalid, nil, true); err == nil || !strings.Contains(err.Error(), "namespace already exists") || engine != nil {
		t.Fatal("retry reused an unpublished namespace instead of requiring a fresh ID", err)
	}
	release, err := LockSavedState(config.StateDir)
	if err != nil {
		t.Fatal("failed initialization retained the engine lease", err)
	}
	_ = release()
}

func TestFreshExitRejectsEveryExistingNamespaceWithoutChangingIdentities(t *testing.T) {
	for _, kind := range []string{"empty", "marker", "identity", "file", "symlink", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			config := freshExitTestConfig(t)
			unlock, err := LockSavedState(config.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			exits := filepath.Join(config.StateDir, "exits")
			if err := privateState(exits); err != nil {
				t.Fatal(err)
			}
			namespace := filepath.Join(exits, config.Focus)
			saved := filepath.Join(config.ExitStateDir("saved"), "tailscaled.state")
			primary := filepath.Join(config.ExitStateDir(MainExit), "tailscaled.state")
			for _, path := range []string{saved, primary} {
				if err := privateState(filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("existing-fixture-identity"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var existing string
			switch kind {
			case "file":
				existing = namespace
			case "symlink", "dangling-symlink":
				target := filepath.Dir(primary)
				if kind == "dangling-symlink" {
					target = filepath.Join(config.StateDir, "missing")
				}
				if err := os.Symlink(target, namespace); err != nil {
					t.Skip("fixture symlink unavailable", err)
				}
			default:
				if err := privateState(namespace); err != nil {
					t.Fatal(err)
				}
				if kind == "marker" {
					existing = filepath.Join(namespace, ".initialized")
				} else if kind == "identity" {
					existing = filepath.Join(config.ExitStateDir(config.Focus), "tailscaled.state")
					if err := privateState(filepath.Dir(existing)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if existing != "" {
				if err := os.WriteFile(existing, []byte("existing-fixture-state"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(namespace)
			if err != nil {
				t.Fatal(err)
			}
			if err := ReserveNewExitState(config); err == nil {
				t.Fatal("existing namespace was accepted as a new device")
			}
			after, err := os.Lstat(namespace)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("existing namespace was replaced", err)
			}
			for _, path := range []string{saved, primary, existing} {
				if path == "" {
					continue
				}
				want := "existing-fixture-identity"
				if path == existing {
					want = "existing-fixture-state"
				}
				if data, err := os.ReadFile(path); err != nil || string(data) != want {
					t.Fatal("fresh authentication changed an existing identity or marker", path, err)
				}
			}
		})
	}
}

func TestFreshAuthenticationRequiresConfiguredAdditionalFocus(t *testing.T) {
	for _, focus := range []string{"", MainExit, "missing", "../escape", "con"} {
		t.Run(focus, func(t *testing.T) {
			config := Config{StateDir: filepath.Join(t.TempDir(), "untouched"), Focus: focus, Tailscale: &TailscaleConfig{}, Exits: []ExitConfig{{ID: "new-exit"}}}
			if err := AuthenticateNewTailnet(context.Background(), config, nil, nil); err == nil {
				t.Fatal("invalid fresh authentication focus was accepted")
			}
			if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
				t.Fatal("invalid focus touched state storage", err)
			}
		})
	}
}

func TestOrdinarySavedOpenDoesNotRequireFreshNamespace(t *testing.T) {
	config := freshExitTestConfig(t)
	unlock, err := LockSavedState(config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReserveNewExitState(config); err != nil {
		t.Fatal(err)
	}
	_ = unlock()
	// Saved login retains its existing open path. Stop at native parsing rather
	// than constructing an endpoint; a freshness check here would change login.
	_, err = OpenWithLogWriter(t.Context(), config, Compiled{JSON: []byte("invalid fixture native options")}, nil)
	if err == nil || !strings.Contains(err.Error(), "validate generated sing-box config") {
		t.Fatal("ordinary saved login unexpectedly applied Add's freshness restriction", err)
	}
}
