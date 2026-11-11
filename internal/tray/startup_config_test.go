package tray

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/privatefs"

	"gopkg.in/yaml.v3"
)

func TestStartupConfigHelperProcess(t *testing.T) {
	mode := os.Getenv("TAILSPLIT_STARTUP_HELPER")
	if mode == "" {
		return
	}
	fail := func(message string, err error) {
		fmt.Fprintln(os.Stderr, message, err)
		os.Exit(81)
	}
	index := slices.Index(os.Args, "-c")
	if index < 0 || index+1 >= len(os.Args) {
		fail("missing configuration argument", nil)
	}
	config, err := tailsplit.LoadDraft(os.Args[index+1])
	if err != nil {
		fail("could not load helper configuration", err)
	}
	if config.StateDir != os.Getenv("TAILSPLIT_STARTUP_STATE") || config.Rules.GFWList != os.Getenv("TAILSPLIT_STARTUP_RULES") {
		fail("configuration relative paths changed", nil)
	}
	current, currentErr := os.Stat(".")
	base, baseErr := os.Stat(os.Getenv("TAILSPLIT_STARTUP_BASE"))
	if currentErr != nil || baseErr != nil || !os.SameFile(current, base) {
		fail("helper working directory changed", errors.Join(currentErr, baseErr))
	}
	command := "run"
	if mode == "discovery" {
		command = "discover"
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		command = "app-run"
		if mode == "login" {
			command = "app-login"
		} else if mode == "discovery" {
			command = "app-discover"
		}
	}
	if !slices.Contains(os.Args, command) {
		fail("wrong startup command", nil)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	unlock, err := tailsplit.LockSavedState(config.StateDir)
	if err != nil {
		fail("could not acquire saved state lease", err)
	}
	fmt.Println(`{"event":"ready"}`)
	exit := ""
	if mode == "traffic" && config.TrafficExitID() != tailsplit.MainExit {
		exit = config.TrafficExitID()
	}
	fmt.Printf("{\"event\":\"status\",\"exit\":%q,\"backend_state\":\"Running\",\"exit_online\":true}\n", exit)
	if mode == "login" {
		fmt.Println(`{"event":"authenticated","mode":"login"}`)
	}
	<-ctx.Done()
	if err := unlock(); err != nil {
		fail("could not release saved state lease", err)
	}
	os.Exit(0)
}

func startupTestController(t *testing.T, configPath, stateDir, rulePath, mode string) *Controller {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("production-argument fixture uses a POSIX executable wrapper")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	helper := filepath.Join(directory, "startup helper")
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexec "+quoted+" -test.run=^TestStartupConfigHelperProcess$ -- \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	controller, err := New(Options{
		Helper: helper, ConfigPath: configPath, LogPath: filepath.Join(directory, "logs", "core.log"),
		ExtraEnv: []string{"TAILSPLIT_STARTUP_HELPER=" + mode,
			"TAILSPLIT_STARTUP_STATE=" + stateDir, "TAILSPLIT_STARTUP_RULES=" + rulePath,
			"TAILSPLIT_STARTUP_BASE=" + filepath.Dir(configPath)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	return controller
}

func startupHelperConfigPath(t *testing.T, controller *Controller) string {
	t.Helper()
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.command == nil {
		t.Fatal("startup helper has already exited")
	}
	index := slices.Index(controller.command.Args, "-c")
	if index < 0 || index+1 >= len(controller.command.Args) {
		t.Fatal("startup helper has no configuration argument", controller.command.Args)
	}
	return controller.command.Args[index+1]
}

func waitStartupSnapshotRemoved(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("startup snapshot survived helper cleanup", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNativeStartupFollowsConfigSymlinkWithoutRelaxingEditing(t *testing.T) {
	for _, mode := range []string{"traffic", "login", "discovery"} {
		t.Run(mode, func(t *testing.T) {
			base, targetDir := t.TempDir(), t.TempDir()
			configPath, target := filepath.Join(base, "config.yaml"), filepath.Join(targetDir, "managed.yaml")
			original := bytes.Replace(connectionSnapshotConfig, []byte("source: ''"), []byte("source: ./rules.txt"), 1)
			if err := os.WriteFile(target, original, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(base, "rules.txt"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, configPath); err != nil {
				t.Skip("config symlinks unavailable", err)
			}
			controller := startupTestController(t, configPath, filepath.Join(base, "state"), filepath.Join(base, "rules.txt"), mode)
			start, state := controller.Start, "running"
			if mode == "login" {
				start, state = controller.StartLogin, "authenticated"
			} else if mode == "discovery" {
				start, state = controller.RefreshExitNodes, "discovering"
			}
			if err := start(); err != nil {
				t.Fatal("trusted startup rejected a readable config symlink", err)
			}
			snapshot := waitState(t, controller, state)
			helperPath := startupHelperConfigPath(t, controller)
			revision := fmt.Sprintf("%x", sha256.Sum256(original))
			if mode == "traffic" {
				data, helperRevision, err := ReadConfigDocument(helperPath)
				if err != nil || !bytes.Equal(data, original) || helperRevision != revision || snapshot.ActiveRevision != revision || helperPath == configPath {
					t.Fatal("symlink startup lost its immutable source document/revision", snapshot, err)
				}
				if err := privatefs.CheckFile(helperPath); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, append(bytes.Clone(original), "# external edit\n"...), 0o600); err != nil {
					t.Fatal(err)
				}
				data, _, err = ReadConfigDocument(helperPath)
				if err != nil || !bytes.Equal(data, original) || controller.Snapshot().ActiveRevision != revision {
					t.Fatal("editing the symlink target changed the running policy", err)
				}
			} else if helperPath != configPath || snapshot.ActiveRevision != "" {
				t.Fatal("non-traffic startup changed source or exposed a traffic revision", snapshot)
			}
			if _, _, err := ReadConfigDocument(configPath); err == nil {
				t.Fatal("web editing reader accepted a config symlink")
			}
			if err := SaveConfigDocument(configPath, revision, original); err == nil {
				t.Fatal("web editing followed a config symlink")
			}
			if err := controller.Stop(); err != nil {
				t.Fatal(err)
			}
			if err := controller.StartRevision(revision); err == nil {
				t.Fatal("revision-based web admission followed a symlink")
			}
			if mode == "traffic" {
				waitStartupSnapshotRemoved(t, helperPath)
			}
			if info, err := os.Lstat(configPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("startup or cleanup replaced the config symlink", err)
			}
		})
	}
}

func readOnlyStartupConfig(t *testing.T) (configPath string, data []byte, stateDir, rulePath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("read-only directory fixture uses POSIX permissions")
	}
	base := t.TempDir()
	source := filepath.Join(base, "read-only")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath, rulePath, stateDir = filepath.Join(source, "config.yaml"), filepath.Join(source, "rules.txt"), filepath.Join(base, "state")
	data = bytes.Replace(connectionSnapshotConfig, []byte("state_dir: ./state"), []byte("state_dir: ../state"), 1)
	data = bytes.Replace(data, []byte("source: ''"), []byte("source: ./rules.txt"), 1)
	data = append(data, "# retain this exact source revision\n"...)
	if err := os.WriteFile(configPath, data, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulePath, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(source, 0o700) })
	probe := filepath.Join(source, "write-probe")
	if err := os.WriteFile(probe, nil, 0o600); err == nil {
		_ = os.Remove(probe)
		t.Skip("process can write to a read-only directory")
	} else if !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	return configPath, data, stateDir, rulePath
}

func TestNativeStartupUsesPrivateStateSnapshotWithReadOnlyConfigDirectory(t *testing.T) {
	for _, checked := range []bool{false, true} {
		t.Run(fmt.Sprintf("revision-checked=%v", checked), func(t *testing.T) {
			configPath, original, stateDir, rulePath := readOnlyStartupConfig(t)
			controller := startupTestController(t, configPath, stateDir, rulePath, "traffic")
			revision := fmt.Sprintf("%x", sha256.Sum256(original))
			if err := controller.StartRevision("stale"); !errors.Is(err, ErrConfigChanged) {
				t.Fatal("stale admission reached fallback startup", err)
			}
			if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("stale admission created snapshot state", err)
			}
			var err error
			if checked {
				err = controller.StartRevision(revision)
			} else {
				err = controller.Start()
			}
			if err != nil {
				t.Fatal("read-only config directory blocked startup", err)
			}
			if snapshot := waitState(t, controller, "running"); snapshot.ActiveRevision != revision {
				t.Fatal("relocated snapshot lost the exact source revision", snapshot)
			}
			helperPath := startupHelperConfigPath(t, controller)
			if filepath.Dir(helperPath) != filepath.Join(stateDir, ".tailsplit-connection-snapshots") {
				t.Fatal("snapshot was not placed in private state", helperPath)
			}
			if err := privatefs.CheckFile(helperPath); err != nil {
				t.Fatal("relocated snapshot is not private", err)
			}
			for _, directory := range []string{stateDir, filepath.Dir(helperPath), filepath.Join(filepath.Dir(helperPath), ".tailsplit-connection-lock")} {
				info, err := os.Lstat(directory)
				if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
					t.Fatal("snapshot state/lock directory is not private", directory, err)
				}
			}
			want, wantErr := tailsplit.LoadDraft(configPath)
			got, gotErr := tailsplit.LoadDraft(helperPath)
			wantYAML, wantEncodeErr := yaml.Marshal(want)
			gotYAML, gotEncodeErr := yaml.Marshal(got)
			if err := errors.Join(wantErr, gotErr, wantEncodeErr, gotEncodeErr); err != nil || !bytes.Equal(gotYAML, wantYAML) {
				t.Fatal("relocating the snapshot changed normalized configuration", err)
			}
			captured, _, err := ReadConfigDocument(helperPath)
			if err != nil {
				t.Fatal(err)
			}
			if unlock, err := tailsplit.LockSavedState(stateDir); err == nil {
				_ = unlock()
				t.Fatal("helper did not retain the saved-state lease")
			}
			if err := cleanupStartupConnectionSnapshots(configPath); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(helperPath); err != nil {
				t.Fatal("cleanup removed a live relocated snapshot", err)
			}
			if err := os.Chmod(configPath, 0o600); err != nil {
				t.Fatal(err)
			}
			changed := append(bytes.Clone(original), "# changed after dispatch\n"...)
			if err := os.WriteFile(configPath, changed, 0o600); err != nil {
				t.Fatal(err)
			}
			current, _, err := ReadConfigDocument(helperPath)
			if err != nil || !bytes.Equal(current, captured) || controller.Snapshot().ActiveRevision != revision {
				t.Fatal("source edit changed relocated helper bytes or revision", err)
			}
			if err := controller.Stop(); err != nil {
				t.Fatal(err)
			}
			waitStartupSnapshotRemoved(t, helperPath)
			if snapshot := controller.Snapshot(); snapshot.Active || snapshot.ActiveRevision != "" {
				t.Fatal("stopped helper retained an active revision", snapshot)
			}
			unlock, err := tailsplit.LockSavedState(stateDir)
			if err != nil {
				t.Fatal("stopped helper retained its saved-state lease", err)
			}
			_ = unlock()
			entries, err := os.ReadDir(filepath.Dir(configPath))
			if err != nil || len(entries) != 2 {
				t.Fatal("startup wrote snapshot metadata beside a read-only source", entries, err)
			}
		})
	}
}

func TestReadOnlyStartupSnapshotCleansRejectedAdmissionAndFailedLaunch(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprintf("stale=%v", stale), func(t *testing.T) {
			configPath, original, stateDir, rulePath := readOnlyStartupConfig(t)
			controller := startupTestController(t, configPath, stateDir, rulePath, "traffic")
			revision := fmt.Sprintf("%x", sha256.Sum256(original))
			if stale {
				if err := os.Chmod(configPath, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(configPath, append(bytes.Clone(original), "# edit before admission\n"...), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				controller.options.Helper = filepath.Join(t.TempDir(), "missing-helper")
			}
			controller.mu.Lock()
			err := controller.startConnectionLocked("proxy", "", original, revision)
			controller.mu.Unlock()
			if err == nil || stale && !errors.Is(err, ErrConfigChanged) {
				t.Fatal("rejected startup was admitted", err)
			}
			paths, globErr := filepath.Glob(filepath.Join(stateDir, ".tailsplit-connection-snapshots", connectionSnapshotPrefix+"*"))
			if globErr != nil || len(paths) != 0 || controller.Snapshot().Active || controller.Snapshot().ActiveRevision != "" {
				t.Fatal("rejected startup leaked an immutable snapshot or active revision", paths, globErr)
			}
		})
	}
}

func TestStartupCleanupPreservesSelectedStateSnapshot(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	snapshotDir := filepath.Join(stateDir, ".tailsplit-connection-snapshots")
	if err := privatefs.EnsureDir(snapshotDir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(snapshotDir, fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, os.Getpid(), rand.Text()))
	data := bytes.Replace(connectionSnapshotConfig, []byte("state_dir: ./state"), []byte(fmt.Sprintf("state_dir: %q", stateDir)), 1)
	file, err := privatefs.CreateFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cleanupStartupConnectionSnapshots(configPath); err != nil {
		t.Fatal(err)
	}
	if current, err := os.ReadFile(configPath); err != nil || !bytes.Equal(current, data) {
		t.Fatal("cleanup adopted the configured source as an orphaned state snapshot", err)
	}
}

func TestStartupReclaimsOrphanedStateSnapshots(t *testing.T) {
	configPath, data, stateDir, rulePath := readOnlyStartupConfig(t)
	config, err := startupConfig(configPath, data)
	if err != nil {
		t.Fatal(err)
	}
	statePath := stateConnectionSnapshotPath(config)
	if err := privatefs.EnsureDir(filepath.Dir(statePath)); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(filepath.Dir(statePath), fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, os.Getpid(), rand.Text()))
	file, err := privatefs.CreateFile(orphan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	_ = startupTestController(t, configPath, stateDir, rulePath, "traffic")
	if _, err := os.Lstat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("next launch retained an unlocked state snapshot", err)
	}
}
