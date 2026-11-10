package appservice

import (
	"context"
	"io"
	"path/filepath"
	"slices"
	"testing"

	"github.com/idy/tailsplit"
)

func TestNewExitReservationPreservesPendingCacheMigration(t *testing.T) {
	config := newExitConfig(t)
	server, dial := newExitPipeServer(t, func(context.Context, tailsplit.Config, io.Writer) error { return nil })
	ctx := context.Background()
	connection, err := openLeasedSession(ctx, "login-new", config, "", Info{}, dial)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := Forward(ctx, connection, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	directory := runtimeUserDirectory(server.Base, server.userKey())
	plan, err := queryImports(ctx, []string{tailsplit.MainExit, "saved", "new-exit"}, dial)
	if err != nil || !plan.Cache || !slices.Equal(plan.IDs, []string{tailsplit.MainExit, "saved"}) {
		t.Fatal("fresh device reservation changed pending migration", plan, err)
	}
	if runtimeInitialized(directory) {
		t.Fatal("device-only operation published the global marker")
	}
	assertMigrationFile(t, filepath.Join(directory, "exits", "new-exit", ".initialized"), "1\n")

	server.active.Lock()
	defer server.active.Unlock()
	cache := "retained-fake-ip-mappings"
	if _, err := prepareRuntimeUser(server.Base, server.userKey(), Files{Cache: []byte(cache)}); err != nil {
		t.Fatal(err)
	}
	assertMigrationFile(t, filepath.Join(directory, "cache.db"), cache)
	plan, err = runtimeImports(directory, []string{"new-exit"})
	if err != nil || plan.Cache || len(plan.IDs) != 0 {
		t.Fatal("completed migration or device authority was lost", plan, err)
	}
	if _, err := prepareRuntimeUser(server.Base, server.userKey(), Files{Cache: []byte("stale-local-cache")}); err != nil {
		t.Fatal(err)
	}
	assertMigrationFile(t, filepath.Join(directory, "cache.db"), cache)
}

func TestNewExitReservationPreservesInitializedCache(t *testing.T) {
	base := filepath.Join(t.TempDir(), "service")
	if err := privateDirectory(base); err != nil {
		t.Fatal(err)
	}
	unlock, err := tailsplit.LockSavedState(base)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	cache := "existing-service-fake-ip-mappings"
	directory, err := prepareRuntimeUser(base, "fixture-user", Files{Cache: []byte(cache)})
	if err != nil {
		t.Fatal(err)
	}
	config := newExitConfig(t)
	if err := prepareNewExitRuntime(base, "fixture-user", config, Files{}); err != nil {
		t.Fatal(err)
	}
	assertMigrationFile(t, filepath.Join(directory, "cache.db"), cache)
	if !runtimeInitialized(directory) {
		t.Fatal("existing global initialization was removed")
	}
}
