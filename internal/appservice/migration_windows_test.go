package appservice

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/idy/tailsplit"
)

// Included by the native CI TestWindows filter on both architectures. It uses
// actual Windows private directories and atomic state publication, no Tailnet.
func TestWindowsUnusedPrimaryMigrationAndLogoutAuthority(t *testing.T) {
	legacy := privateTestDirectory(t)
	if err := privateDirectory(filepath.Join(legacy, "tailscale")); err != nil {
		t.Fatal(err)
	}
	sentinel := []byte("isolated-ci-migration-sentinel-not-a-real-device-credential\n")
	userState := filepath.Join(legacy, "tailscale", "tailscaled.state")
	if err := replacePrivate(userState, sentinel); err != nil {
		t.Fatal(err)
	}
	config, err := tailsplit.Normalize(tailsplit.Config{StateDir: legacy}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ids, err := migrationIDs(config, "start", "")
	if err != nil || len(ids) != 0 {
		t.Fatal("direct-only operation requested an unused identity", ids, err)
	}
	base := privateTestDirectory(t)
	directory := runtimeUserDirectory(base, "fixture-user")
	plan, err := runtimeImports(directory, ids)
	if err != nil {
		t.Fatal(err)
	}
	files, err := userFiles(&config, plan)
	if err != nil || len(files.TailscaleState) != 0 {
		t.Fatal("direct-only collection imported an unused primary", err)
	}
	if _, err := prepareRuntimeUser(base, "fixture-user", files); err != nil {
		t.Fatal(err)
	}
	plan, err = runtimeImports(directory, []string{tailsplit.MainExit})
	if err != nil || !reflect.DeepEqual(plan.IDs, []string{tailsplit.MainExit}) || plan.Cache {
		t.Fatal("cache initialization consumed future primary eligibility", plan, err)
	}
	// Later explicit primary use imports the original state completely.
	config.Tailscale = &tailsplit.TailscaleConfig{Hostname: "fixture-main", ExitNode: "100.64.0.10"}
	files, err = userFiles(&config, plan)
	if err != nil || string(files.TailscaleState) != string(sentinel) {
		t.Fatal("eligible primary state was not collected", err)
	}
	if _, err := prepareRuntimeUser(base, "fixture-user", files); err != nil {
		t.Fatal(err)
	}
	serviceState := filepath.Join(directory, "tailscale", "tailscaled.state")
	if data, err := os.ReadFile(serviceState); err != nil || string(data) != string(sentinel) {
		t.Fatal("primary import did not publish the identity", err)
	}
	// Owned state cannot become a prerequisite or be overwritten by stale files.
	if err := os.Remove(userState); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(userState, 0o700); err != nil {
		t.Fatal(err)
	}
	plan, err = runtimeImports(directory, []string{tailsplit.MainExit})
	if err != nil || len(plan.IDs) != 0 {
		t.Fatal("service-owned primary was eligible for stale re-import", plan, err)
	}
	files, err = userFiles(&config, plan)
	if err != nil || len(files.TailscaleState) != 0 {
		t.Fatal("owned primary's nonregular stale copy blocked collection", err)
	}
	// Logout retains authority even if the engine removes its entire state dir.
	if err := claimLogoutExit(tailsplit.Config{StateDir: directory, Focus: tailsplit.MainExit}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(serviceState)); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareRuntimeUser(base, "fixture-user", Files{TailscaleState: sentinel}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(serviceState); !os.IsNotExist(err) {
		t.Fatal("logged-out primary was restored from a stale copy", err)
	}
}
