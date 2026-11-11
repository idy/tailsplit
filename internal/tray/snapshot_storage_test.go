package tray

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/idy/tailsplit/internal/privatefs"
)

// Controller startup now sweeps a per-user namespace. Never let tests inspect
// the real user's namespace, including subprocess fixtures and unrelated tests.
func TestMain(m *testing.M) {
	if os.Getenv("TAILSPLIT_TEST_CONFIG_ROOT") != "" {
		os.Exit(m.Run())
	}
	root, err := os.MkdirTemp("", "tailsplit-test-user-config-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var envErr error
	switch runtime.GOOS {
	case "darwin":
		envErr = os.Setenv("HOME", root)
	case "windows":
		envErr = errors.Join(os.Setenv("APPDATA", filepath.Join(root, "config")), os.Setenv("LOCALAPPDATA", filepath.Join(root, "cache")))
	default:
		envErr = errors.Join(os.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config")), os.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache")))
	}
	if err := errors.Join(envErr, os.Setenv("TAILSPLIT_TEST_CONFIG_ROOT", root)); err != nil {
		_ = os.RemoveAll(root)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

func TestStartupFallbackRejectsReplaceableParentForCreationAndCleanup(t *testing.T) {
	configPath, data, _, _ := readOnlyStartupConfig(t)
	fallback, err := connectionSnapshotFallbackPath()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(fallback)
	if err := privatefs.StableDir(root, true); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, connectionSnapshotPrefix+"1234-ABCDEFGHIJKLMNOP")
	file, err := privatefs.CreateFile(orphan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(root)
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	if _, _, cleanup, err := createConnectionSnapshot(configPath, data); err == nil {
		cleanup()
		t.Fatal("published a snapshot under a replaceable parent")
	}
	if err := cleanupStartupConnectionSnapshots(configPath); err == nil {
		t.Fatal("cleanup adopted a replaceable snapshot root")
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("unsafe-root cleanup removed a snapshot", err)
	}
}

func TestStartupFallbackIgnoresWritableCustomLogParent(t *testing.T) {
	configPath, _, stateDir, rulePath := readOnlyStartupConfig(t)
	controller := startupTestController(t, configPath, stateDir, rulePath, "traffic")
	logParent := filepath.Dir(controller.options.LogPath)
	if err := os.Chmod(logParent, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(logParent, 0o700) })
	if err := controller.Start(); err != nil {
		t.Fatal("log permissions affected independent snapshot storage", err)
	}
	waitState(t, controller, "running")
	helperPath := startupHelperConfigPath(t, controller)
	fallback, err := connectionSnapshotFallbackPath()
	if err != nil || filepath.Dir(helperPath) != filepath.Dir(fallback) {
		t.Fatal("helper did not use the fixed per-user root", helperPath, err)
	}
	if _, err := os.Lstat(filepath.Join(logParent, ".tailsplit-connection-snapshots")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created snapshot storage under a writable custom-log parent", err)
	}
}
